package av

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The Opus stream upstream's voice.rs writes.
const (
	// frame is one Opus packet: 20 ms.
	frame = 960
	// bitrate is the encoder's target, about what the phone sends.
	bitrate = 32000
	// pageEvery is how many audio packets go in one Ogg page.
	pageEvery = 50
	// serial is the Ogg stream's serial number ("VASA" little-endian), as
	// upstream writes it.
	serial = 0x56415341
	// vendor is the OpusTags vendor string.
	vendor = "ZapFast"
)

// opusEncoder encodes mono Rate float32 samples to 20 ms Opus packets.
type opusEncoder interface {
	// preSkip is the encoder's lookahead: the samples the decoder drops.
	preSkip() int
	// encode encodes all of samples, then the lookahead's worth of
	// silence, calling packet with each packet (valid until it returns).
	encode(samples []float32, packet func([]byte) error) error
	close()
}

// opusDecoder decodes Opus packets to interleaved float32 at Rate.
type opusDecoder interface {
	// decode decodes all of packets in order, appending the interleaved
	// samples to dst. lead is how many leading samples a channel the codec
	// itself drops: the caller drops pre-skip minus lead.
	decode(dst []float32, packets [][]byte) ([]float32, error)
	lead() int
	close()
}

// EncodeOpus is an Ogg/Opus file of mono samples at Rate: one 20 ms packet
// after another at 32 kbit/s, 50 to a page, the stream's last granule
// trimming the silence that flushes the encoder. It is upstream's
// voice::encode. Without an Opus encoder it fails with ErrUnavailable.
func EncodeOpus(samples []float32) ([]byte, error) {
	enc, err := newOpusEncoder()
	if err != nil {
		return nil, err
	}
	defer enc.close()
	pre := enc.preSkip()
	w := &oggWriter{serial: serial}
	if err := w.packet(opusHead(1, pre), 0, true, false); err != nil {
		return nil, err
	}
	if err := w.packet(opusTags(), 0, true, false); err != nil {
		return nil, err
	}
	var packets [][]byte
	if err := enc.encode(samples, func(p []byte) error {
		packets = append(packets, append([]byte(nil), p...))
		return nil
	}); err != nil {
		return nil, err
	}
	if len(packets) == 0 {
		return nil, fmt.Errorf("av: the Opus encoder gave no audio")
	}
	for i, p := range packets {
		last := i == len(packets)-1
		granule := uint64((i + 1) * frame)
		if last {
			granule = uint64(pre + len(samples))
		}
		if err := w.packet(p, granule, (i+1)%pageEvery == 0, last); err != nil {
			return nil, err
		}
	}
	return w.out, nil
}

// DecodeOpus is the mono samples at Rate of an Ogg/Opus file: stereo is
// mixed down, the pre-skip dropped, and the last packet left whole, as
// upstream's voice::decode leaves it. Bytes that are not Ogg/Opus, or are
// damaged, are ErrMalformed; without an Opus decoder it fails with
// ErrUnavailable.
func DecodeOpus(data []byte) ([]float32, error) {
	return decodeOpus(data, 0)
}

// decodeOpus is DecodeOpus, refusing with ErrTooLong a stream over limit
// samples (0: no limit) before decoding any of it.
func decodeOpus(data []byte, limit int) ([]float32, error) {
	var (
		channels, pre int
		tagged        bool
		packets       [][]byte
		total         int
	)
	err := oggPackets(data, func(p []byte, _ uint64, _ bool) error {
		switch {
		case channels == 0:
			if len(p) < 8 || string(p[:8]) != "OpusHead" {
				return malformed("not an Opus stream")
			}
			if len(p) < 19 {
				return malformed("a truncated Opus header")
			}
			channels = int(p[9])
			if channels != 1 && channels != 2 {
				return malformed(fmt.Sprintf("%d channels", channels))
			}
			pre = int(binary.LittleEndian.Uint16(p[10:]))
		case !tagged:
			tagged = true // the comment header
		case len(p) == 0:
			return malformed("an empty Opus packet")
		default:
			n, ok := packetSamples(p)
			if !ok {
				return malformed("a damaged Opus packet")
			}
			total += n
			if limit > 0 && total > limit {
				return ErrTooLong
			}
			packets = append(packets, append([]byte(nil), p...))
		}
		return nil
	})
	if err != nil {
		if errorsIsAny(err, ErrMalformed, ErrTooLong) {
			return nil, err
		}
		return nil, malformed(err.Error())
	}
	if channels == 0 {
		return nil, malformed("not an Opus stream")
	}
	dec, err := newOpusDecoder(channels)
	if err != nil {
		return nil, err
	}
	defer dec.close()
	pcm, err := dec.decode(make([]float32, 0, total*channels), packets)
	if err != nil {
		return nil, err
	}
	mono := pcm
	if channels == 2 {
		mono = pcm[:len(pcm)/2]
		for i := range mono {
			mono[i] = (pcm[2*i] + pcm[2*i+1]) * 0.5
		}
	}
	skip := min(max(pre-dec.lead(), 0), len(mono))
	return mono[skip:], nil
}

// opusHead is the identification header (RFC 7845 §5.1), channel mapping 0.
func opusHead(channels, preSkip int) []byte {
	h := make([]byte, 0, 19)
	h = append(h, "OpusHead"...)
	h = append(h, 1, byte(channels))
	h = binary.LittleEndian.AppendUint16(h, uint16(preSkip))
	h = binary.LittleEndian.AppendUint32(h, Rate)
	h = binary.LittleEndian.AppendUint16(h, 0) // output gain
	return append(h, 0)
}

// opusTags is the comment header: the vendor and no comments.
func opusTags() []byte {
	t := make([]byte, 0, 16+len(vendor))
	t = append(t, "OpusTags"...)
	t = binary.LittleEndian.AppendUint32(t, uint32(len(vendor)))
	t = append(t, vendor...)
	return binary.LittleEndian.AppendUint32(t, 0)
}

// packetSamples is how many samples at 48 kHz an Opus packet holds, from its
// TOC byte and frame count (RFC 6716 §3.1), and whether that is valid: at
// most 120 ms.
func packetSamples(p []byte) (int, bool) {
	if len(p) == 0 {
		return 0, false
	}
	config := int(p[0] >> 3)
	var size int // one frame, in samples at 48 kHz
	switch {
	case config < 12: // SILK: 10, 20, 40, 60 ms
		size = [4]int{480, 960, 1920, 2880}[config&3]
	case config < 16: // hybrid: 10, 20 ms
		size = [2]int{480, 960}[config&1]
	default: // CELT: 2.5, 5, 10, 20 ms
		size = [4]int{120, 240, 480, 960}[config&3]
	}
	frames := 1
	switch p[0] & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(p) < 2 {
			return 0, false
		}
		frames = int(p[1] & 0x3f)
	}
	n := frames * size
	return n, frames > 0 && n <= 5760
}

func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
