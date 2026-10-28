// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pitch.rs, MIT.

package mlow

import (
	"slices"
	"testing"
)

// Decodes LSF(0) -> pulses(0) -> pitch(0) on each active captured frame and compares the pitch
// result (gains, filters, lag, contour, per-subframe int_lag_q6) against the reference vectors.
func TestSmplPitch_PitchMatchGo(t *testing.T) {
	recs := loadJSON(t, "pitch_vectors.json")
	tbl := loadSmplTables()
	mem := loadSmplMem()
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
		pr := decodeSmplPitch(&dec, mem, cc, &st, 320, 4, 0, pulses.subfr)

		if want := int32(rec.Get("lag").I64()); pr.lag != want {
			t.Fatalf("lag: got %d want %d", pr.lag, want)
		}
		if want := int32(rec.Get("contour").I64()); pr.contour != want {
			t.Fatalf("contour: got %d want %d", pr.contour, want)
		}
		if want := rec.Get("gain_idx").I32s(); !slices.Equal(pr.gainIdx[:], want) {
			t.Fatalf("gain_idx: got %v want %v", pr.gainIdx, want)
		}
		if want := rec.Get("filt_idx").I32s(); !slices.Equal(pr.filtIdx[:], want) {
			t.Fatalf("filt_idx: got %v want %v", pr.filtIdx, want)
		}
		if want := rec.Get("int_lag_q6").I32s(); !slices.Equal(pr.intLagQ6[:], want) {
			t.Fatalf("int_lag_q6: got %v want %v", pr.intLagQ6, want)
		}
		if dec.err != 0 {
			t.Fatalf("no decode error: err = %d", dec.err)
		}
	}
}
