// Ported from whatsapp-rust 5e72baf wacore/src/voip/opus_packet.rs, MIT.

package opus

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

const desktopTOC = 0x58

func seq(n int, f func(i int) byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func cat(head []byte, tail []byte) []byte { return append(append([]byte{}, head...), tail...) }

func iota8(n int) []byte { return seq(n, func(i int) byte { return byte(i) }) }

func frames(t *testing.T, p []byte) (uint8, bool) {
	t.Helper()
	s, ok := Shape(p)
	return s.Frames, ok
}

func TestTheTocBehindTheBugReadsAsSilkWidebandSixtyMilliseconds(t *testing.T) {
	shape, ok := Shape(cat([]byte{desktopTOC}, iota8(80)))
	if !ok {
		t.Fatal("a valid code-0 packet")
	}
	if shape.Mode != Silk || shape.Bandwidth != Wide || shape.Stereo || shape.Frames != 1 || shape.FrameDurationUS != 60_000 {
		t.Fatalf("shape %+v", shape)
	}
	if n, ok := shape.TotalSamples(16_000); !ok || n != 960 {
		t.Fatalf("samples %d %v", n, ok)
	}
}

func TestNoPacketTheMlowDecoderAcceptsCanBeMistakenForA960SampleOpusPacket(t *testing.T) {
	for _, toc := range []byte{0x10, 0x11, 0x12, 0x13, 0x50, 0x51, 0x52, 0x53} {
		for n := 1; n < 400; n++ {
			p := cat([]byte{toc}, seq(n, func(i int) byte { return byte(i % 251) }))
			if shape, ok := Shape(p); ok {
				if s, ok := shape.TotalSamples(16_000); ok && s == 960 {
					t.Fatalf("TOC %#04x with a %d-byte body must never claim 960 samples", toc, n)
				}
			}
		}
	}
}

func TestCodeOneRequiresAnEvenBody(t *testing.T) {
	if _, ok := Shape(cat([]byte{0x59}, iota8(7))); ok {
		t.Fatal("odd body accepted")
	}
	shape, ok := Shape(cat([]byte{0x59}, iota8(8)))
	if !ok || shape.Frames != 2 {
		t.Fatalf("even body: %+v %v", shape, ok)
	}
	if n, ok := shape.TotalSamples(16_000); !ok || n != 1920 {
		t.Fatalf("samples %d %v", n, ok)
	}
}

func TestAPacketLongerThanTheRfcCeilingIsRejected(t *testing.T) {
	if _, ok := Shape(cat([]byte{0x5b, 0x03}, iota8(30))); ok {
		t.Fatal("180 ms accepted")
	}
}

func TestACodeThreeFrameCountMustBeWithinRange(t *testing.T) {
	for _, count := range []byte{0, 49, 63} {
		if _, ok := Shape(cat([]byte{0x0b, count}, iota8(30))); ok {
			t.Fatalf("frame count %d is out of range", count)
		}
	}
	if f, ok := frames(t, cat([]byte{0x0b, 0x06}, iota8(30))); !ok || f != 6 {
		t.Fatalf("got %d %v", f, ok)
	}
}

func TestATruncatedCodeTwoLengthIsRejected(t *testing.T) {
	if _, ok := Shape([]byte{0x5a, 200, 1, 2}); ok {
		t.Fatal("truncated accepted")
	}
	if f, ok := frames(t, cat([]byte{0x5a, 3}, iota8(8))); !ok || f != 2 {
		t.Fatalf("got %d %v", f, ok)
	}
}

func TestATwoByteCodeTwoLengthNeedsBothBytes(t *testing.T) {
	if _, ok := Shape([]byte{0x5a, 252}); ok {
		t.Fatal("accepted")
	}
}

func TestPaddingThatOverrunsThePacketIsRejected(t *testing.T) {
	if _, ok := Shape([]byte{0x0b, 0x41, 200, 1, 2}); ok {
		t.Fatal("accepted")
	}
}

func TestAPaddingRunAccumulatesRatherThanBeingCheckedByteByByte(t *testing.T) {
	if _, ok := Shape(cat([]byte{0x0b, 0x43, 255, 255, 255, 3}, iota8(300))); ok {
		t.Fatal("accepted")
	}
}

func TestAConstantBitratePacketWhoseBodyDoesNotDivideIsRejected(t *testing.T) {
	if _, ok := Shape(cat([]byte{0x0b, 0x03}, iota8(10))); ok {
		t.Fatal("accepted")
	}
	if f, ok := frames(t, cat([]byte{0x0b, 0x03}, iota8(9))); !ok || f != 3 {
		t.Fatalf("got %d %v", f, ok)
	}
	if f, ok := frames(t, cat([]byte{0x0b, 0x83}, iota8(10))); !ok || f != 3 {
		t.Fatalf("got %d %v", f, ok)
	}
}

func TestAVariableBitratePacketWhoseFramesAreNotThereIsRejected(t *testing.T) {
	if _, ok := Shape([]byte{0x4b, 0x83}); ok {
		t.Fatal("accepted")
	}
	if _, ok := Shape(cat([]byte{0x4b, 0x83, 200, 200}, iota8(100))); ok {
		t.Fatal("accepted")
	}
	if f, ok := frames(t, cat([]byte{0x4b, 0x83, 200, 200}, iota8(450))); !ok || f != 3 {
		t.Fatalf("got %d %v", f, ok)
	}
	if _, ok := Shape([]byte{0x4b, 0x82, 252}); ok {
		t.Fatal("accepted")
	}
}

func TestAnEmptyPayloadHasNoShape(t *testing.T) {
	if _, ok := Shape(nil); ok {
		t.Fatal("accepted")
	}
}

func TestADurationTheClockCannotExpressYieldsNoSampleCount(t *testing.T) {
	shape := PacketShape{Mode: Celt, Bandwidth: Full, Frames: 1, FrameDurationUS: 2_500}
	if n, ok := shape.TotalSamples(8_000); !ok || n != 20 {
		t.Fatalf("8 kHz: %d %v", n, ok)
	}
	if _, ok := shape.TotalSamples(300); ok {
		t.Fatal("300 Hz expressed")
	}
}

func TestEveryTocByteEitherParsesOrIsRefusedWithoutPanicking(t *testing.T) {
	for toc := range 256 {
		for n := range 12 {
			if shape, ok := Shape(cat([]byte{byte(toc)}, iota8(n))); ok {
				if shape.Frames < 1 || shape.Frames > maxFrames {
					t.Fatalf("frames %d", shape.Frames)
				}
				if shape.FrameDurationUS*uint32(shape.Frames) > maxPacketDurationUS {
					t.Fatal("over the ceiling")
				}
			}
		}
	}
}

// TestShapeMatchesRustGolden checks Shape against opus_packet_shape run by
// the real Rust code (zfcalls/tests/voip_golden.rs).
func TestShapeMatchesRustGolden(t *testing.T) {
	if _, err := os.Stat(filepath.Join(kat.Dir(), "golden.json")); err != nil {
		t.Skip("no golden.json")
	}
	g := kat.Golden(t)
	vectors := g.List(t, "opus_shape")
	if len(vectors) == 0 {
		t.Fatal("no opus_shape vectors")
	}
	for i, v := range vectors {
		payload := v.Hex(t, "payload")
		got, ok := Shape(payload)
		want, _ := v.Get(t, "shape").(map[string]any)
		if (want != nil) != ok {
			t.Fatalf("vector %d (%s): Go ok=%v, Rust shape=%v", i, hex.EncodeToString(payload), ok, want)
		}
		if !ok {
			continue
		}
		s := v.Sub(t, "shape")
		stereo, _ := s.Get(t, "stereo").(bool)
		if got.Mode.String() != s.Str(t, "mode") || got.Bandwidth.String() != s.Str(t, "bandwidth") ||
			got.Stereo != stereo || uint64(got.Frames) != s.Uint(t, "frames") ||
			uint64(got.FrameDurationUS) != s.Uint(t, "frame_duration_us") {
			t.Fatalf("vector %d (%s): Go %+v, Rust %v", i, hex.EncodeToString(payload), got, want)
		}
	}
}
