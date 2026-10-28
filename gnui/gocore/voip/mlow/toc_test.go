// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/toc.rs, MIT.

package mlow

import (
	"errors"
	"testing"
)

// Exhaustively validates the parse over every byte value against the captured vectors.
func TestToc_TocMatchesGoAll256(t *testing.T) {
	recs := loadJSON(t, "toc_vectors.json")
	arr := recs.Arr()
	if len(arr) != 256 {
		t.Fatalf("len %d != 256", len(arr))
	}
	for _, rec := range arr {
		b := uint8(rec.Get("b").U64())
		tc := parseMlowToc(b)
		if tc.stdOpus != rec.Get("std").Bool() {
			t.Errorf("std b=0x%02x", b)
		}
		if tc.sid != rec.Get("sid").Bool() {
			t.Errorf("sid b=0x%02x", b)
		}
		if tc.vad != rec.Get("vad").Bool() {
			t.Errorf("vad b=0x%02x", b)
		}
		if tc.sampleRate != int32(rec.Get("sr").I64()) {
			t.Errorf("sr b=0x%02x", b)
		}
		// The escape range is deliberately NOT checked against this fixture. Its `ms` there
		// records the RFC 6716 reading (`config = b >> 3`), which is not the layout the escape
		// uses; `celtEscapeFrameMs` and `escape_durations_agree_with_the_escape_writer`
		// cover it instead. Editing the fixture to agree with the code is what hid a decoder
		// bug in this module once already, so it is left alone and its stale range is named.
		if !tc.stdOpus && tc.frameMs != int32(rec.Get("ms").I64()) {
			t.Errorf("ms b=0x%02x", b)
		}
		if tc.fec != rec.Get("voiced").Bool() {
			t.Errorf("voiced b=0x%02x", b)
		}
		if tc.active != rec.Get("active").Bool() {
			t.Errorf("active b=0x%02x", b)
		}
		if tc.lowRate != rec.Get("f2").Bool() {
			t.Errorf("f2 b=0x%02x", b)
		}
		if tc.stereo != rec.Get("f0").Bool() {
			t.Errorf("f0 b=0x%02x", b)
		}
	}
}

// tocPacketizeOpusForMlow stands in for `crate::voip::audio::packetize_opus_for_mlow`, which lives
// outside the mlow module and has no Go port yet. It reproduces that function's DTX, NotCelt,
// silence and code-0 paths verbatim (the only ones this test reaches: every packet here is a
// 4-byte code-0 CELT packet) and refuses the multi-frame packings it does not need.
func tocPacketizeOpusForMlow(packet *[]byte) error {
	p := *packet
	if len(p) == 0 {
		return errors.New("empty Opus packet")
	}
	toc := p[0]
	if len(p) <= 2 {
		p[0] = 0x90
		*packet = p[:1]
		return nil
	}
	config := toc >> 3
	if config < 16 {
		return errors.New("Opus config is not CELT-only")
	}
	packing := toc & 0x03
	if packing != 0 {
		return errors.New("tocPacketizeOpusForMlow: only code-0 packets are ported")
	}
	// opus_celt_packet_is_silence, code-0 arm.
	if body := p[1:]; len(body) == 0 || (len(body) == 2 && body[0] == 0xFF && body[1] == 0xFE) {
		*packet = append(p[:0], 0x90)
		return nil
	}
	mode := config - 16
	stereo := (toc >> 2) & 1
	var multi uint8
	if packing != 0 {
		multi = 1
	}
	p[0] = 0xC0 | mode<<2 | stereo<<1 | multi
	return nil
}

// The escape reader must agree with the escape writer, which is in this same crate.
//
// `packetize_opus_for_mlow` builds the byte from an RFC config, so round-tripping every CELT
// configuration through it and back gives an independent statement of what each escape byte
// means. They disagreed for 56 of the 64 escape bytes before this: the writer was right and
// the reader was reading an RFC 6716 TOC that the escape does not use.
func TestToc_EscapeDurationsAgreeWithTheEscapeWriter(t *testing.T) {
	// RFC 6716 table 2, CELT half: durations {2.5, 5, 10, 20} ms by the low two config bits.
	// 2.5 rounds up to 3 for the same reason the parser does.
	celtMs := [4]int32{3, 5, 10, 20}
	checked := 0
	for config := uint8(16); config < 32; config++ {
		for _, stereo := range []bool{false, true} {
			packet := []byte{config<<3 | uint8(b2i(stereo))<<2, 0x11, 0x22, 0x33}
			if err := tocPacketizeOpusForMlow(&packet); err != nil {
				t.Fatalf("every CELT config escapes: %v", err)
			}
			toc := parseMlowToc(packet[0])
			if !toc.stdOpus {
				t.Fatalf("config %d must escape into the CELT range", config)
			}
			if toc.frameMs != celtMs[config&3] {
				t.Fatalf("config %d round-tripped to the wrong duration (byte %#04x)", config, packet[0])
			}
			checked++
		}
	}
	if checked != 32 {
		t.Fatalf("every CELT configuration must be covered")
	}
}
