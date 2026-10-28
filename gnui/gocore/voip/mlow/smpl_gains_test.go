// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_gains.rs, MIT.

package mlow

import (
	"slices"
	"testing"
)

// Force-runs gains on each active frame's post-pulse decoder state (semantically a voiced frame,
// so the bits aren't "really" gains, but the decode is deterministic and must match the reference
// exactly, validating the gains arithmetic + memory reads byte-for-byte).
func TestSmplGains_GainsMatchGo(t *testing.T) {
	recs := loadJSON(t, "gains_vectors.json")
	tbl := loadSmplTables()
	cc := loadCcTables()
	arr := recs.Arr()
	if len(arr) == 0 {
		t.Fatalf("assertion failed: !arr.is_empty()")
	}
	for _, rec := range arr {
		frame := rec.Get("frame").Hex()
		st := defaultSmplLsfState()
		dec := newRangeDecoder(frame[1:])
		lsf := decodeSmplLsf(&dec, tbl, &st, 0, 0, true)
		pulses := decodeSmplPulses(&dec, cc, 320, 4, 1, 0, lsf.stage1)
		g := decodeSmplGains(&dec, cc, 4, pulses.subfr)
		if want := rec.Get("gain_q").I32s(); !slices.Equal(g.gainQ[:], want) {
			t.Fatalf("gain_q: got %v want %v", g.gainQ, want)
		}
		if want := rec.Get("nrg_res").I32s(); !slices.Equal(g.nrgRes[:], want) {
			t.Fatalf("nrg_res: got %v want %v", g.nrgRes, want)
		}
	}
}
