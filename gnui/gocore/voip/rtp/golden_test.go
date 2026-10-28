package rtp

import (
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// WARP headers from whatsapp-rust 5e72baf's RtpStream (piggyback on) and
// VideoRtpStream.
func TestGoldenWARPHeaders(t *testing.T) {
	g := kat.Golden(t).Sub(t, "rtp_headers")
	a := g.Sub(t, "audio")
	s := NewStream(uint32(a.Uint(t, "ssrc")), uint32(a.Uint(t, "samples_per_packet")), true)
	headers := a.List(t, "headers")
	for i, p := range a.List(t, "payloads") {
		payload, _ := hex.DecodeString(p.Get(t).(string))
		if got := hex.EncodeToString(s.NextPacket(payload, false).Encode()); got != headers[i].Get(t).(string) {
			t.Fatalf("audio %d: %s want %s", i, got, headers[i].Get(t))
		}
	}
	v := g.Sub(t, "video")
	vs, _ := NewVideoStream(uint32(v.Uint(t, "ssrc")), uint32(v.Uint(t, "ts_stride")))
	vh := v.List(t, "headers")
	for i, step := range v.List(t, "steps") {
		st := step.Get(t).([]any)
		if got := hex.EncodeToString(vs.NextVideoPacket(st[0].(bool), uint8(st[1].(float64))).Encode()); got != vh[i].Get(t).(string) {
			t.Fatalf("video %d: %s", i, got)
		}
	}
}
