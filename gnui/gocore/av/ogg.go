// Ogg pages: the reader is adapted from zapfast-go 3104c46
// (internal/playback/ogg.go, oggPackets, oggCRC and oggPage), MIT.

package av

import (
	"encoding/binary"
	"errors"
	"math"
)

// noGranule is the granule position of a packet that does not end a page.
const noGranule = math.MaxUint64

// oggCRC is the Ogg checksum: CRC-32, polynomial 0x04c11db7, no reflection,
// over the page with its checksum field zeroed.
func oggCRC(page []byte) uint32 {
	var crc uint32
	for _, b := range page {
		crc = crc<<8 ^ crcTable[byte(crc>>24)^b]
	}
	return crc
}

var crcTable = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for range 8 {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
}()

// oggPackets calls visit with each packet of the first logical stream in
// data, in order: its bytes (valid until visit returns), the granule
// position of the page it ends (noGranule unless it is the page's last), and
// whether that page ends the stream. Pages of other streams interleaved with
// it are skipped, and so is anything after its end. Checksums and page
// sequence are checked. A stream cut short without its end page reads as far
// as its last whole packet, as upstream's reader does.
func oggPackets(data []byte, visit func(packet []byte, granule uint64, eos bool) error) error {
	var partial []byte
	var serial, sequence uint32
	first := true
	for len(data) > 0 {
		if len(data) < 27 {
			if first {
				return errors.New("not an Ogg stream")
			}
			break // a cut-off page
		}
		header := data[:27]
		if string(header[:4]) != "OggS" || header[4] != 0 {
			if first {
				return errors.New("not an Ogg stream")
			}
			return errors.New("damaged Ogg page")
		}
		flags := header[5]
		granule := binary.LittleEndian.Uint64(header[6:14])
		pageSerial := binary.LittleEndian.Uint32(header[14:18])
		pageSeq := binary.LittleEndian.Uint32(header[18:22])
		nseg := int(header[26])
		if len(data) < 27+nseg {
			break
		}
		lacing := data[27 : 27+nseg]
		size := 0
		for _, l := range lacing {
			size += int(l)
		}
		if len(data) < 27+nseg+size {
			break
		}
		page := data[:27+nseg+size]
		data = data[len(page):]

		want := binary.LittleEndian.Uint32(page[22:26])
		if pageCRC(page) != want {
			return errors.New("Ogg checksum mismatch")
		}
		if first {
			if flags&2 == 0 {
				return errors.New("the Ogg stream has no beginning")
			}
			serial, sequence, first = pageSerial, pageSeq, false
		} else if pageSerial != serial {
			continue
		}
		if pageSeq != sequence {
			return errors.New("an Ogg page is missing")
		}
		sequence++
		if (flags&1 != 0) != (len(partial) > 0) {
			return errors.New("a broken Ogg packet")
		}
		body := page[27+nseg:]
		last := -1 // the lacing index that ends the page's last packet
		for i, l := range lacing {
			if l < 255 {
				last = i
			}
		}
		eos := flags&4 != 0
		at := 0
		for i, l := range lacing {
			partial = append(partial, body[at:at+int(l)]...)
			at += int(l)
			if l == 255 {
				continue
			}
			g := uint64(noGranule)
			if i == last {
				g = granule
			}
			if err := visit(partial, g, eos && i == last); err != nil {
				return err
			}
			partial = partial[:0]
		}
		if eos {
			return nil
		}
	}
	if first {
		return errors.New("not an Ogg stream")
	}
	return nil
}

// pageCRC is the checksum of a page whose checksum field is filled in.
func pageCRC(page []byte) uint32 {
	var crc uint32
	for i, b := range page {
		if i >= 22 && i < 26 {
			b = 0
		}
		crc = crc<<8 ^ crcTable[byte(crc>>24)^b]
	}
	return crc
}

// lastGranule is the granule position of the last Ogg page in data that has
// one: for an Opus stream its length in 48 kHz samples plus the pre-skip. ok
// is false without such a page. It looks at the bytes only, as zfcore's
// voice.rs does, so a stream that says it is too long is refused before it is
// decoded. Unlike zfcore it passes over pages whose granule is -1 (no packet
// ends on them): WhatsApp's own notes end with an empty page like that, which
// zfcore reads as a stream far too long.
func lastGranule(data []byte) (granule uint64, ok bool) {
	for i := len(data) - 4; i >= 0; i-- {
		if string(data[i:i+4]) != "OggS" || i+14 > len(data) {
			continue
		}
		if g := binary.LittleEndian.Uint64(data[i+6:]); g != noGranule {
			return g, true
		}
	}
	return 0, false
}

// oggWriter lays out the pages of one logical stream.
type oggWriter struct {
	out      []byte
	serial   uint32
	sequence uint32
	lacing   []byte
	body     []byte
	granule  uint64 // of the last packet in the open page
	started  bool
}

// maxOggPacket is the largest packet one page holds (255 lacing values).
const maxOggPacket = 255*255 - 1

// packet adds a packet whose last sample is at granule. end closes the page
// after it; eos ends the stream there. A page that would go over 255 lacing
// values is closed first.
func (w *oggWriter) packet(p []byte, granule uint64, end, eos bool) error {
	if len(p) > maxOggPacket {
		return errors.New("an Ogg packet over 64 KiB")
	}
	n := len(p)/255 + 1
	if len(w.lacing)+n > 255 {
		w.flush(false)
	}
	for range n - 1 {
		w.lacing = append(w.lacing, 255)
	}
	w.lacing = append(w.lacing, byte(len(p)%255))
	w.body = append(w.body, p...)
	w.granule = granule
	if end || eos {
		w.flush(eos)
	}
	return nil
}

// flush writes the open page.
func (w *oggWriter) flush(eos bool) {
	if len(w.lacing) == 0 && !eos {
		return
	}
	var flags byte
	if !w.started {
		flags |= 2
		w.started = true
	}
	if eos {
		flags |= 4
	}
	start := len(w.out)
	w.out = append(w.out, "OggS"...)
	w.out = append(w.out, 0, flags)
	w.out = binary.LittleEndian.AppendUint64(w.out, w.granule)
	w.out = binary.LittleEndian.AppendUint32(w.out, w.serial)
	w.out = binary.LittleEndian.AppendUint32(w.out, w.sequence)
	w.out = append(w.out, 0, 0, 0, 0, byte(len(w.lacing)))
	w.out = append(w.out, w.lacing...)
	w.out = append(w.out, w.body...)
	binary.LittleEndian.PutUint32(w.out[start+22:], oggCRC(w.out[start:]))
	w.sequence++
	w.lacing, w.body = w.lacing[:0], w.body[:0]
}
