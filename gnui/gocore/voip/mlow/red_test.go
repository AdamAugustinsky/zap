// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/red.rs, MIT.

package mlow

import (
	"bytes"
	"testing"
)

func redFrameEq(a, b mlowFrame) bool {
	return bytes.Equal(a.data, b.data) && a.timeCode == b.timeCode && a.isMain == b.isMain
}

func TestRed_DepackOneRedundantPlusMain(t *testing.T) {
	// N=1: hdr [0x85,0x03] [main_marker 0x00] | red payload [AA BB CC] | main [50 11 22 33].
	p := []byte{0x85, 0x03, 0x00, 0xAA, 0xBB, 0xCC, 0x50, 0x11, 0x22, 0x33}
	frames, err := depackSplitRed(p)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len %d != 2", len(frames))
	}
	if !redFrameEq(frames[0], mlowFrame{data: []byte{0xAA, 0xBB, 0xCC}, timeCode: 5, isMain: false}) {
		t.Fatalf("frames[0] = %+v", frames[0])
	}
	if !redFrameEq(frames[1], mlowFrame{data: []byte{0x50, 0x11, 0x22, 0x33}, timeCode: 0, isMain: true}) {
		t.Fatalf("frames[1] = %+v", frames[1])
	}
}

func TestRed_DepackNoRedundantJustMain(t *testing.T) {
	// header is just the main marker (0x00), then the main payload.
	p := []byte{0x00, 0x50, 0x11, 0x22}
	frames, err := depackSplitRed(p)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len %d != 1", len(frames))
	}
	if !frames[0].isMain {
		t.Fatalf("not main")
	}
	if !bytes.Equal(frames[0].data, []byte{0x50, 0x11, 0x22}) {
		t.Fatalf("data %x", frames[0].data)
	}
}

func TestRed_EmptyPacketErrors(t *testing.T) {
	if _, err := depackSplitRed(nil); err != redErrorPktSizeZero {
		t.Fatalf("got %v want PktSizeZero", err)
	}
}

func TestRed_BareFrameIsRejected(t *testing.T) {
	// A bare MLow frame (high-bit-set TOC like a DTX 0x90) must NOT parse as SplitRed.
	if _, err := depackSplitRed([]byte{0x90, 0x01, 0x02}); err == nil {
		t.Fatalf("expected an error")
	}
}
