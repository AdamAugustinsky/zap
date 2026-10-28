package kdf

import (
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

func TestGoldenParticipantSSRCs(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "ssrc") {
		if got := ParticipantSSRC(v.Str(t, "call_id"), v.Str(t, "lid"), uint32(v.Uint(t, "slot"))); uint64(got) != v.Uint(t, "ssrc") {
			t.Fatalf("%s %s slot %d: %d", v.Str(t, "call_id"), v.Str(t, "lid"), v.Uint(t, "slot"), got)
		}
	}
}
