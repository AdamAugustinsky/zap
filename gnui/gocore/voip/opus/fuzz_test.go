package opus

// Native fuzz targets for the Opus packet-header reader a received audio
// payload goes through.
//
//	CGO_ENABLED=0 go test ./gocore/voip/opus -run '^$' -fuzz '^FuzzShape$' -fuzztime 45s

import (
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// FuzzShape reads an arbitrary packet header. An accepted packet has 1..48
// frames of a duration from RFC 6716 table 2 for its configuration, at
// most 120 ms in all, a whole number of samples at 48 kHz, and the TOC's
// stereo bit; reading is deterministic.
func FuzzShape(f *testing.F) {
	for _, v := range kat.Golden(f).List(f, "opus_shape") {
		b, _ := hex.DecodeString(v.Str(f, "payload"))
		f.Add(b)
	}
	f.Add([]byte{0xfb, 0xc3, 0xff, 0x01, 1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, p []byte) {
		s, ok := Shape(p)
		s2, ok2 := Shape(p)
		if ok != ok2 || s != s2 {
			t.Fatalf("Shape is not deterministic")
		}
		if !ok {
			if s != (PacketShape{}) {
				t.Fatalf("a refused packet with a shape %+v", s)
			}
			return
		}
		if s.Frames < 1 || s.Frames > maxFrames {
			t.Fatalf("%x: %d frames", p, s.Frames)
		}
		mode, bw, us := decodeConfig(p[0] >> 3)
		if s.Mode != mode || s.Bandwidth != bw || s.FrameDurationUS != us || s.Stereo != (p[0]&4 != 0) {
			t.Fatalf("%x: %+v, config says %v %v %d", p, s, mode, bw, us)
		}
		if uint32(s.Frames)*s.FrameDurationUS > maxPacketDurationUS {
			t.Fatalf("%x: %d × %d µs", p, s.Frames, s.FrameDurationUS)
		}
		if n, ok := s.TotalSamples(48000); !ok || n == 0 || n > 5760 {
			t.Fatalf("%x: %d samples at 48 kHz, %v", p, n, ok)
		}
		switch p[0] & 3 {
		case 0:
			if s.Frames != 1 {
				t.Fatalf("code 0 with %d frames", s.Frames)
			}
		case 1, 2:
			if s.Frames != 2 {
				t.Fatalf("code %d with %d frames", p[0]&3, s.Frames)
			}
		}
	})
}

// fuzzLength is RFC 6716 §3.2.1's one- or two-byte frame length.
func fuzzLength(n int) []byte {
	if n < 252 {
		return []byte{byte(n)}
	}
	first := 252 + (n-252)%4
	return []byte{byte(first), byte((n - first) / 4)}
}

// FuzzShapeBuild lays out a packet by RFC 6716 §3.2 (codes 0 to 3, CBR and
// VBR, with and without padding) from arbitrary frame sizes and reads it
// back: Shape accepts exactly the packets of at most 120 ms and reports
// the frames and configuration written. Cutting the last byte off a packet
// of equal-sized frames is refused.
func FuzzShapeBuild(f *testing.F) {
	f.Add(uint8(31), true, uint8(3), []byte{10, 20, 30}, uint16(300), true)
	f.Add(uint8(1), false, uint8(1), []byte{40}, uint16(0), false)
	f.Add(uint8(15), false, uint8(2), []byte{250, 3}, uint16(0), false)
	f.Fuzz(func(t *testing.T, config uint8, stereo bool, code uint8, sizesRaw []byte, padding uint16, vbr bool) {
		config &= 31
		code &= 3
		toc := config<<3 | code
		if stereo {
			toc |= 4
		}
		// Frame sizes up to 1275 bytes, the RFC's largest.
		var sizes []int
		for i := 0; i+1 < len(sizesRaw) && len(sizes) < 48; i += 2 {
			sizes = append(sizes, (int(sizesRaw[i])|int(sizesRaw[i+1])<<8)%1276)
		}
		if len(sizes) == 0 {
			sizes = []int{0}
		}
		frame := func(n int) []byte {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}
		p := []byte{toc}
		var frames int
		switch code {
		case 0:
			frames = 1
			p = append(p, frame(sizes[0])...)
		case 1:
			frames = 2
			p = append(append(p, frame(sizes[0])...), frame(sizes[0])...)
		case 2:
			frames = 2
			second := 0
			if len(sizes) > 1 {
				second = sizes[1]
			}
			p = append(p, fuzzLength(sizes[0])...)
			p = append(append(p, frame(sizes[0])...), frame(second)...)
		case 3:
			frames = len(sizes)
			pad := int(padding) % 600
			count := byte(frames)
			if vbr {
				count |= 0x80
			}
			if pad > 0 {
				count |= 0x40
			}
			p = append(p, count)
			if pad > 0 {
				for left := pad; ; left -= 254 {
					if left >= 255 {
						p = append(p, 255)
						continue
					}
					p = append(p, byte(left))
					break
				}
			}
			if vbr {
				for _, n := range sizes[:frames-1] {
					p = append(p, fuzzLength(n)...)
				}
				for _, n := range sizes {
					p = append(p, frame(n)...)
				}
			} else {
				for range sizes {
					p = append(p, frame(sizes[0])...)
				}
			}
			p = append(p, make([]byte, pad)...)
		}
		_, _, us := decodeConfig(config)
		fits := uint64(us)*uint64(frames) <= maxPacketDurationUS
		s, ok := Shape(p)
		if ok != fits {
			t.Fatalf("%x (%d frames of %d µs): accepted %v", p, frames, us, ok)
		}
		if ok && (int(s.Frames) != frames || s.Stereo != stereo || s.FrameDurationUS != us) {
			t.Fatalf("%x: %+v, wrote %d frames", p, s, frames)
		}
		// Cutting a byte off a body whose frames must be equal (code 1, or
		// code 3 CBR of several non-empty frames, no padding) leaves a body
		// that no longer divides.
		if !fits {
			return
		}
		cut := p[:len(p)-1]
		refused := (code == 1 && sizes[0] > 0) ||
			(code == 3 && !vbr && int(padding)%600 == 0 && frames > 1 && sizes[0] > 0)
		if refused {
			if _, ok := Shape(cut); ok {
				t.Fatalf("%x accepted with its last byte cut", cut)
			}
		}
	})
}
