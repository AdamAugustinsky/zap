// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/ssrc.rs (tests), MIT.

package kdf

import (
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

func TestSSRCMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	callID, lid := k.Str(t, "inputs", "callId"), k.Str(t, "inputs", "peerLid")
	if got := ParticipantSSRC(callID, lid, 0); uint64(got) != k.Uint(t, "voip_crypto", "ssrc_slot0") {
		t.Fatalf("slot 0: %d", got)
	}
	if got := ParticipantSSRC(callID, lid, 1); uint64(got) != k.Uint(t, "voip_crypto", "ssrc_slot1") {
		t.Fatalf("slot 1: %d", got)
	}
}

func TestVideoSSRCDiffersFromAudioSSRC(t *testing.T) {
	audio := ParticipantSSRC("CALL-ID-0001", "12345:0@lid", 0)
	video := VideoParticipantSSRC("CALL-ID-0001", "12345:0@lid")
	if audio == video {
		t.Fatal("audio and video SSRC collide")
	}
	if video != 0xf8d7_0484 {
		t.Fatalf("WhatsApp WASM video-slot KAT: %#x", video)
	}
}

func TestRelayStreamBundleUsesTheProtocolSlotOrder(t *testing.T) {
	bundle := RelayStreamSSRCs("CALL-ID-0001", "12345:0@lid")
	for i, slot := range WASMRelayStreamSlotWords {
		if bundle[i] != ParticipantSSRC("CALL-ID-0001", "12345:0@lid", slot) {
			t.Fatalf("index %d", i)
		}
	}
	if bundle[3] != VideoParticipantSSRC("CALL-ID-0001", "12345:0@lid") {
		t.Fatal("bundle[3] is not the video SSRC")
	}
}

func TestFormatParticipantIDRules(t *testing.T) {
	for in, want := range map[string]string{
		"12345@lid":            "12345:0@lid",
		"12345:6@lid":          "12345:6@lid",
		"12345@s.whatsapp.net": "12345@s.whatsapp.net",
		"12345@lid/res":        "12345:0@lid",
		"@lid":                 "@lid",
		"plain":                "plain",
	} {
		if got := FormatParticipantID(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestAppendVarint(t *testing.T) {
	for v, want := range map[uint64]string{0: "\x00", 0x7f: "\x7f", 0x80: "\x80\x01", 300: "\xac\x02"} {
		if got := string(AppendVarint(nil, v)); got != want {
			t.Errorf("%d: %x", v, got)
		}
	}
}
