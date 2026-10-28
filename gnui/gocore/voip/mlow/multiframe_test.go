// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/multiframe.rs, MIT.

package mlow

import (
	"bytes"
	"testing"
)

// multiframeEnvelope is the envelope a peer with frames-per-packet 2 emits: the indicator carries
// the sub-frames' fixed bits, and each body is a complete MLow packet.
func multiframeEnvelope(t *testing.T, frames [][]byte) []byte {
	out := []byte{0x82 | (frames[0][0] & 0x39), uint8(len(frames))}
	for _, frame := range frames[:len(frames)-1] {
		if len(frame) >= 252 {
			t.Fatalf("fixture keeps lengths single-byte")
		}
		out = append(out, uint8(len(frame)))
	}
	for _, frame := range frames {
		out = append(out, frame...)
	}
	return out
}

func multiframeSeq(lo, hi int) []byte {
	var out []byte
	for i := lo; i < hi; i++ {
		out = append(out, uint8(i))
	}
	return out
}

func multiframeErrIs(err error, want multiframeError) bool {
	got, ok := err.(multiframeError)
	return ok && got == want
}

// Two 60 ms blocks in one envelope: exactly what the real client emits for a 120 ms frame
// length, and exactly what this port used to read as a SID.
func TestMultiframe_ATwoFrameEnvelopeSplitsIntoTwoWholePackets(t *testing.T) {
	first := append([]byte{0x50}, multiframeSeq(0, 40)...)
	second := append([]byte{0x50}, multiframeSeq(40, 90)...)
	packet := multiframeEnvelope(t, [][]byte{first, second})
	if !isMultiframe(packet) {
		t.Fatalf("not multiframe")
	}
	frames, err := splitMultiframe(packet)
	if err != nil {
		t.Fatalf("a well-formed envelope: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len %d", len(frames))
	}
	if !bytes.Equal(frames[0], first) || !bytes.Equal(frames[1], second) {
		t.Fatalf("frame mismatch")
	}
}

// The indicator is unreachable as a real TOC because the encoder never emits a SID frame that
// also carries FEC. If this ever stopped holding, an ordinary frame would be split as an
// envelope and the call would break in a new way.
//
// Asserted against the REAL parser, not a local re-derivation of the bit layout: a test that
// recomputes the classification it is checking proves only that two copies of the same
// expression agree.
func TestMultiframe_TheIndicatorIsNotReachableAsAnOrdinaryToc(t *testing.T) {
	envelopes := 0
	for i := 0; i <= 255; i++ {
		b := uint8(i)
		toc := parseMlowToc(b)
		looksLikeEnvelope := isMultiframe([]byte{b, 0x01, 0xaa})
		if !looksLikeEnvelope {
			continue
		}
		envelopes++
		// Every byte the splitter claims is an envelope must be one the encoder cannot emit as
		// a frame: it declares SID, and the SID path is the one that would have silenced it.
		if !toc.sid {
			t.Fatalf("envelope indicator %#04x would otherwise be a decodable frame", b)
		}
		if toc.stdOpus {
			t.Fatalf("the CELT escape must not be mistaken for an envelope (%#04x)", b)
		}
		// A SID never carries FEC in anything the encoder produces, which is what makes the
		// bit-1 collision safe. `mlowToc.fec` is `vad && bit1`, and a SID has vad clear, so the
		// raw bit is what has to be checked here.
		if b&0x02 != 0x02 {
			t.Fatalf("bit 1 clear in %#04x", b)
		}
		if toc.fec {
			t.Fatalf("a SID frame never reports FEC")
		}
	}
	if envelopes != 32 {
		t.Fatalf("the indicator space is bits 7 and 1 set with bit 6 clear: got %d", envelopes)
	}
}

func TestMultiframe_ASingleBytePayloadIsNeverAnEnvelope(t *testing.T) {
	if isMultiframe([]byte{0x92}) {
		t.Fatalf("single byte is an envelope")
	}
}

func TestMultiframe_AFrameCountOutsideTheAcceptedRangeIsRefused(t *testing.T) {
	for _, count := range []uint8{0, 19, 63} {
		packet := append([]byte{0x92, count}, multiframeSeq(0, 40)...)
		_, err := splitMultiframe(packet)
		if !multiframeErrIs(err, multiframeErrorBadFrameCount(int(count))) {
			t.Fatalf("count %d: got %v", count, err)
		}
	}
}

func TestMultiframe_ALengthThatRunsPastTheEndIsRefused(t *testing.T) {
	// Two frames, the first declared as 200 bytes inside a 10-byte body.
	packet := append([]byte{0x92, 0x02, 200}, multiframeSeq(0, 10)...)
	if _, err := splitMultiframe(packet); !multiframeErrIs(err, multiframeErrorLengthOverrun) {
		t.Fatalf("got %v", err)
	}
}

func TestMultiframe_AnEnvelopeWithNothingLeftForTheLastFrameIsRefused(t *testing.T) {
	// Two frames, the first taking the whole body.
	packet := append([]byte{0x92, 0x02, 10}, multiframeSeq(0, 10)...)
	if _, err := splitMultiframe(packet); !multiframeErrIs(err, multiframeErrorEmptySubFrame) {
		t.Fatalf("got %v", err)
	}
}

func TestMultiframe_ATwoByteLengthIsDecodedTheWayOpusEncodesIt(t *testing.T) {
	// 252 + 1*4 = 256 bytes in the first frame.
	packet := append([]byte{0x92, 0x02, 252, 1}, multiframeSeq(0, 300)...)
	frames, err := splitMultiframe(packet)
	if err != nil {
		t.Fatalf("well formed: %v", err)
	}
	if len(frames[0]) != 256 || len(frames[1]) != 300-256 {
		t.Fatalf("lens %d %d", len(frames[0]), len(frames[1]))
	}
}

func TestMultiframe_DeclaredPaddingIsRemovedFromTheLastFrame(t *testing.T) {
	// One frame, 4 bytes of padding: the body is 20 bytes, so the frame is 16.
	packet := append([]byte{0x92, 0x41, 4}, multiframeSeq(0, 20)...)
	frames, err := splitMultiframe(packet)
	if err != nil {
		t.Fatalf("well formed: %v", err)
	}
	if len(frames) != 1 || len(frames[0]) != 16 {
		t.Fatalf("frames %d, len %d", len(frames), len(frames[0]))
	}
}

func TestMultiframe_PaddingThatOverrunsTheEnvelopeIsRefused(t *testing.T) {
	packet := append([]byte{0x92, 0x41, 200}, multiframeSeq(0, 10)...)
	if _, err := splitMultiframe(packet); !multiframeErrIs(err, multiframeErrorLengthOverrun) {
		t.Fatalf("got %v", err)
	}
}

func TestMultiframe_ATruncatedEnvelopeIsRefusedWithoutPanicking(t *testing.T) {
	if _, err := splitMultiframe([]byte{0x92}); !multiframeErrIs(err, multiframeErrorTruncated) {
		t.Fatalf("got %v", err)
	}
	if _, err := splitMultiframe([]byte{0x92, 0x02}); !multiframeErrIs(err, multiframeErrorTruncated) {
		t.Fatalf("got %v", err)
	}
	if _, err := splitMultiframe([]byte{0x92, 0x02, 5}); !multiframeErrIs(err, multiframeErrorLengthOverrun) {
		t.Fatalf("got %v", err)
	}
}

// Every input either splits into a sane set of sub-frames or is refused, and the refusals are
// counted so a splitter that started rejecting everything could not pass by never entering the
// `Ok` arm.
func TestMultiframe_EveryShortInputEitherSplitsSanelyOrIsRefused(t *testing.T) {
	ok, refused := 0, 0
	for c := 0; c <= 255; c++ {
		for n := 0; n < 24; n++ {
			packet := []byte{0x92, uint8(c)}
			for i := 0; i < n; i++ {
				packet = append(packet, uint8(i%253))
			}
			frames, err := splitMultiframe(packet)
			if err != nil {
				refused++
				continue
			}
			ok++
			if len(frames) < 1 || len(frames) > maxFrames {
				t.Fatalf("frame count %d", len(frames))
			}
			total := 0
			for _, frame := range frames {
				if len(frame) == 0 {
					t.Fatalf("empty frame")
				}
				total += len(frame)
			}
			if total > len(packet) {
				t.Fatalf("total %d > %d", total, len(packet))
			}
		}
	}
	if ok == 0 {
		t.Fatalf("a splitter that refuses everything must not pass this test")
	}
	if refused == 0 {
		t.Fatalf("malformed envelopes must still be refused")
	}
}
