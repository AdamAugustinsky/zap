package av

// Native fuzz targets for the parts of this package that read bytes a peer
// sent and that run without a native library: the Ogg page reader and its
// writer, the Opus TOC count a voice note is measured by, WebP's RIFF
// chunks and sticker metadata, and the EXIF orientation readers. Opus
// decoding itself (AudioToolbox) and WebP pixels (libzfwebp, ImageIO) are
// not fuzzed here.
//
//	CGO_ENABLED=0 go test ./gocore/av -run '^$' -fuzz '^FuzzOggPackets$' -fuzztime 45s

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"runtime"
	"slices"
	"testing"
	"unsafe"

	"zapfast/gocore/voip/opus"
)

// fuzzHeapBytes is how many bytes one call of fn allocates, averaged over
// eight calls: runtime.MemStats counts the whole process, and a fuzz
// worker's own goroutines allocate a few KiB now and then, so a single call
// is not a reliable measurement. Callers allow a fixed slack on top of
// their bound for the same reason.
func fuzzHeapBytes(fn func()) uint64 {
	const runs = 8
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// fuzzWithin reports whether sub lies inside whole's backing array.
func fuzzWithin(sub, whole []byte) bool {
	if len(sub) == 0 {
		return true
	}
	if len(whole) == 0 {
		return false
	}
	s := uintptr(unsafe.Pointer(unsafe.SliceData(sub)))
	w := uintptr(unsafe.Pointer(unsafe.SliceData(whole)))
	return s >= w && s+uintptr(len(sub)) <= w+uintptr(len(whole))
}

// fuzzOggNote is a small synthetic Ogg/Opus stream as EncodeOpus lays one
// out: the two headers, then packets of the given sizes.
func fuzzOggNote(sizes ...int) []byte {
	w := &oggWriter{serial: serial}
	_ = w.packet(opusHead(1, 312), 0, true, false)
	_ = w.packet(opusTags(), 0, true, false)
	for i, n := range sizes {
		p := bytes.Repeat([]byte{0x78 ^ byte(i)}, n)
		p[0] = 15 << 3 // hybrid FB 20 ms
		_ = w.packet(p, uint64((i+1)*frame), (i+1)%pageEvery == 0, i == len(sizes)-1)
	}
	return w.out
}

type fuzzOggPacket struct {
	data    []byte
	granule uint64
	eos     bool
}

func fuzzReadOgg(data []byte) ([]fuzzOggPacket, error) {
	var out []fuzzOggPacket
	err := oggPackets(data, func(p []byte, g uint64, eos bool) error {
		out = append(out, fuzzOggPacket{append([]byte(nil), p...), g, eos})
		return nil
	})
	return out, err
}

// FuzzOggPackets reads an arbitrary Ogg stream. Properties: no panic; the
// packets' bytes add up to no more than the input; only a page's last
// packet carries a granule and only the very last packet can end the
// stream; reading is deterministic; and the reader's own allocation stays
// within a small multiple of the input, whatever lacing or sizes the pages
// claim. lastGranule never reads past the input either.
func FuzzOggPackets(f *testing.F) {
	f.Add(fuzzOggNote(10, 300, 255, 510))
	f.Add(fuzzOggNote(80, 80, 80))
	f.Add(fuzzOggNote())
	f.Add([]byte("OggS\x00\x02"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := fuzzReadOgg(data)
		total := 0
		for i, p := range got {
			total += len(p.data)
			if p.eos && i != len(got)-1 {
				t.Fatalf("packet %d of %d ends the stream", i, len(got))
			}
			if p.eos && err != nil {
				t.Fatalf("the stream ended and failed: %v", err)
			}
		}
		if total > len(data) {
			t.Fatalf("%d packet bytes out of %d input bytes", total, len(data))
		}
		// The reader's only buffer is the packet it reassembles, which never
		// outgrows the input, whatever the lacing or the sizes claim. 16 KiB
		// of slack for the process-wide measurement.
		allocated := fuzzHeapBytes(func() {
			_ = oggPackets(data, func([]byte, uint64, bool) error { return nil })
		})
		if limit := uint64(2*len(data)) + 16<<10; allocated > limit {
			t.Fatalf("reading %d bytes allocated %d", len(data), allocated)
		}
		a, errA := fuzzReadOgg(data)
		b, errB := fuzzReadOgg(data)
		if (errA == nil) != (errB == nil) || len(a) != len(b) || (err == nil) != (errA == nil) {
			t.Fatalf("reading is not deterministic")
		}
		for i := range a {
			if !bytes.Equal(a[i].data, b[i].data) || a[i].granule != b[i].granule || a[i].eos != b[i].eos {
				t.Fatalf("packet %d differs between two reads", i)
			}
		}
		if g, ok := lastGranule(data); ok && g == noGranule {
			t.Fatalf("lastGranule gave the no-granule marker")
		}
	})
}

// FuzzOggRoundTrip writes packets cut from the input with oggWriter and reads
// them back: the same packets in order, each page's last packet with the
// granule written for it, the last packet ending the stream, and
// lastGranule finding that packet's granule.
func FuzzOggRoundTrip(f *testing.F) {
	f.Add([]byte{3, 1, 2, 3, 0, 255, 254, 1})
	f.Add(bytes.Repeat([]byte{255}, 300))
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, script []byte) {
		// Each packet: a size byte (scaled so lacing runs of 255 come up),
		// a flag byte, then filler.
		var packets [][]byte
		var ends []bool
		for i := 0; len(script) >= 2 && i < 200; i++ {
			size := int(script[0]) * (1 + int(script[1]>>5)) // up to 255*8
			end := script[1]&1 != 0
			script = script[2:]
			p := make([]byte, size)
			for j := range p {
				p[j] = byte(i + j)
			}
			packets = append(packets, p)
			ends = append(ends, end)
		}
		if len(packets) == 0 {
			return
		}
		w := &oggWriter{serial: 0x1234}
		for i, p := range packets {
			if err := w.packet(p, uint64(i+1), ends[i], i == len(packets)-1); err != nil {
				t.Fatal(err)
			}
		}
		got, err := fuzzReadOgg(w.out)
		if err != nil {
			t.Fatalf("reading what oggWriter wrote: %v", err)
		}
		if len(got) != len(packets) {
			t.Fatalf("%d packets back of %d", len(got), len(packets))
		}
		for i, p := range got {
			if !bytes.Equal(p.data, packets[i]) {
				t.Fatalf("packet %d: %d bytes back, wrote %d", i, len(p.data), len(packets[i]))
			}
			if p.granule != noGranule && p.granule != uint64(i+1) {
				t.Fatalf("packet %d: granule %d", i, p.granule)
			}
			if ends[i] && p.granule != uint64(i+1) {
				t.Fatalf("packet %d closed its page but reads granule %d", i, p.granule)
			}
			if p.eos != (i == len(packets)-1) {
				t.Fatalf("packet %d: eos %v", i, p.eos)
			}
		}
		if g, ok := lastGranule(w.out); !ok || g != uint64(len(packets)) {
			t.Fatalf("lastGranule %d %v, want %d", g, ok, len(packets))
		}
	})
}

// FuzzPacketSamples measures an Opus packet by its TOC. The count is
// positive and at most 120 ms exactly when it is accepted, and wherever the
// stricter voip/opus.Shape accepts a packet, packetSamples agrees with its
// duration at 48 kHz.
func FuzzPacketSamples(f *testing.F) {
	for _, p := range [][]byte{{15 << 3}, {1 << 3}, {3<<3 | 1}, {31<<3 | 3, 6}, {31<<3 | 3, 7}, {31<<3 | 3}, {}} {
		f.Add(p)
	}
	f.Add([]byte{0x7b, 0x83, 0x02, 1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, p []byte) {
		n, ok := packetSamples(p)
		if ok != (n > 0 && n <= 5760) {
			t.Fatalf("%x: %d samples, ok %v", p, n, ok)
		}
		if n < 0 || n > 63*2880 {
			t.Fatalf("%x: %d samples", p, n)
		}
		if s, sok := opus.Shape(p); sok {
			want, wok := s.TotalSamples(48000)
			if !wok || !ok || uint32(n) != want {
				t.Fatalf("%x: packetSamples %d %v, Shape says %d %v", p, n, ok, want, wok)
			}
		}
	})
}

// FuzzRIFF reads a WebP's chunks and its sticker metadata. Every chunk lies
// inside the file, in order, without overlapping; reading is deterministic;
// and when the file is a WebP, writing sticker metadata into it and reading
// it back gives the metadata (after JSON's own normalization), keeps the
// other chunks, and writing it again changes nothing.
func FuzzRIFF(f *testing.F) {
	f.Add(appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "EXIF", tiffBlock([]byte(`{"sticker-pack-id":"x","emojis":["❤"]}`))), "🦆")
	f.Add(appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "EXIF", []byte(`junk{"emojis":[" 😂 ",""]}`)), "")
	f.Add(appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "VP8L", []byte{0x2f, 6, 0x40, 0, 0x10, 7}), "x")
	f.Add(appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "VP8 ", []byte{0, 0, 0, 0x9d, 1, 0x2a, 7, 0, 5, 0}), "y")
	f.Add(appendChunk(appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "VP8X", make([]byte, 10)), "ALPH", []byte{1}), "z")
	f.Fuzz(func(t *testing.T, webp []byte, emoji string) {
		chunks := riffChunks(webp)
		prevEnd := 12
		for _, c := range chunks {
			if len(c.id) != 4 || !fuzzWithin(c.data, webp) {
				t.Fatalf("chunk %q outside the file", c.id)
			}
			at := int(uintptr(unsafe.Pointer(unsafe.SliceData(c.data))) - uintptr(unsafe.Pointer(unsafe.SliceData(webp))))
			if len(c.data) > 0 && at < prevEnd+8 {
				t.Fatalf("chunk %q at %d overlaps the one before (ends %d)", c.id, at, prevEnd)
			}
			if len(c.data) > 0 {
				prevEnd = at + len(c.data)
			}
		}
		info1, ok1 := ReadStickerInfo(webp)
		info2, ok2 := ReadStickerInfo(webp)
		if ok1 != ok2 || !slices.Equal(info1.Emojis, info2.Emojis) || info1.PackID != info2.PackID {
			t.Fatalf("ReadStickerInfo is not deterministic")
		}
		_ = StickerEmojis(webp)

		info := StickerInfo{PackID: "fuzz", PackName: emoji, Emojis: []string{emoji, "🦆"}}
		out, err := WriteStickerInfo(webp, info)
		if err != nil {
			return
		}
		js, _ := json.Marshal(info)
		var want StickerInfo
		if err := json.Unmarshal(js, &want); err != nil {
			t.Fatal(err)
		}
		got, ok := ReadStickerInfo(out)
		if !ok || got.PackID != want.PackID || got.PackName != want.PackName || !slices.Equal(got.Emojis, want.Emojis) {
			t.Fatalf("wrote %+v, read %+v %v", want, got, ok)
		}
		if binary.LittleEndian.Uint32(out[4:]) != uint32(len(out)-8) {
			t.Fatalf("RIFF size %d for %d bytes", binary.LittleEndian.Uint32(out[4:]), len(out))
		}
		outChunks := riffChunks(out)
		if len(outChunks) == 0 || outChunks[0].id != "VP8X" || outChunks[len(outChunks)-1].id != "EXIF" {
			t.Fatalf("written layout %v", fuzzChunkIDs(outChunks))
		}
		var kept, wantKept []string
		for _, c := range outChunks[1 : len(outChunks)-1] {
			kept = append(kept, c.id)
		}
		if chunkIndex(chunks, "VP8X") >= 0 {
			for _, c := range chunks {
				if c.id != "VP8X" && c.id != "EXIF" {
					wantKept = append(wantKept, c.id)
				}
			}
		} else {
			wantKept = []string{chunks[0].id}
		}
		if !slices.Equal(kept, wantKept) {
			t.Fatalf("kept chunks %v, want %v", kept, wantKept)
		}
		again, err := WriteStickerInfo(out, info)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("writing the same metadata twice changed the file: %v", err)
		}
	})
}

func fuzzChunkIDs(cs []riffChunk) []string {
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.id)
	}
	return ids
}

// FuzzOrientation reads the EXIF orientation of arbitrary picture bytes,
// and the first TIFF directory entry for a tag. The orientation is always
// 1..8, and a TIFF entry's value lies inside the block.
func FuzzOrientation(f *testing.F) {
	tiff := []byte("II*\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00")
	f.Add(tiff, uint16(0x0112))
	f.Add(append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x16eXIf"), append(tiff, 0, 0, 0, 0)...), uint16(0x0112))
	f.Add(appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "EXIF", append([]byte("Exif\x00\x00"), tiff...)), uint16(0x0112))
	f.Add(tiffBlock([]byte(`{"emojis":["x"]}`)), uint16(stickerTag))
	f.Add([]byte("MM\x00*\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x08\x00\x00"), uint16(0x0112))
	f.Add([]byte{0xff, 0xd8, 0xff, 0xe1, 0, 8, 'E', 'x', 'i', 'f', 0, 0}, uint16(0x0112))
	f.Fuzz(func(t *testing.T, data []byte, tag uint16) {
		if o := orientation(data); o < 1 || o > 8 {
			t.Fatalf("orientation %d", o)
		}
		if o := tiffOrientation(data); o < 1 || o > 8 {
			t.Fatalf("tiffOrientation %d", o)
		}
		v, _, ok := tiffEntry(data, tag)
		if ok && !fuzzWithin(v, data) {
			t.Fatalf("entry value outside the block")
		}
		if !ok && v != nil {
			t.Fatalf("a value with no entry")
		}
	})
}
