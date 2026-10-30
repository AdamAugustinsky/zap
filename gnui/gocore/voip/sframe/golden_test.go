package sframe

import (
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// Frames encrypted by whatsapp-rust 5e72baf's SframeSession, with counters
// across the one- and two-byte varint boundaries.
func TestGoldenFrames(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "sframe") {
		ck := v.Hex(t, "call_key")
		s, _ := NewSession(ck, v.Str(t, "self_lid"), v.Str(t, "peer_lid"))
		key, _ := DeriveKey(ck, s.PeerID)
		if hex.EncodeToString(key) != v.Str(t, "peer_key") {
			t.Fatal("peer key")
		}
		s.SetCounter(v.Uint(t, "counter_start"))
		peer, _ := NewSession(ck, v.Str(t, "peer_lid"), v.Str(t, "self_lid"))
		outs := v.List(t, "out")
		for i, f := range v.List(t, "frames") {
			plain, _ := hex.DecodeString(f.Get(t).(string))
			got := s.Encrypt(plain)
			if hex.EncodeToString(got) != outs[i].Get(t).(string) {
				t.Fatalf("frame %d:\n got  %x\n want %s", i, got, outs[i].Get(t))
			}
			if p, r := peer.Decrypt(got); r != Decrypted || hex.EncodeToString(p) != hex.EncodeToString(plain) {
				t.Fatal("peer decrypt")
			}
		}
	}
}
