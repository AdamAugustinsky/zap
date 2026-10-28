// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_decode.rs, MIT.

package mlow

import (
	"slices"
	"testing"
)

// Validates the first-internal-frame LSF parse (range coder at body start, the reliable
// validation point) against the captured vectors over every active frame.
func TestSmplDecode_LsfFrame0MatchesGo(t *testing.T) {
	recs := loadJSON(t, "lsf_vectors.json")
	tbl := loadSmplTables()
	arr := recs.Arr()
	if len(arr) == 0 {
		t.Fatalf("assertion failed: !arr.is_empty()")
	}
	for _, rec := range arr {
		frame := rec.Get("frame").Hex()
		st := defaultSmplLsfState()
		dec := newRangeDecoder(frame[1:])
		idx := decodeSmplLsf(&dec, tbl, &st, 0, 0, true)
		if got, want := idx.stage1, int32(rec.Get("stage1").I64()); got != want {
			t.Fatalf("stage1: got %d want %d", got, want)
		}
		if got, want := idx.grid, int32(rec.Get("grid").I64()); got != want {
			t.Fatalf("grid: got %d want %d", got, want)
		}
		if got, want := idx.extra, int32(rec.Get("extra").I64()); got != want {
			t.Fatalf("extra: got %d want %d", got, want)
		}
		want2 := rec.Get("stage2").I32s()
		if !slices.Equal(idx.stage2[:], want2) {
			t.Fatalf("stage2: got %v want %v", idx.stage2, want2)
		}
		if dec.err != 0 {
			t.Fatalf("no decode error: err = %d", dec.err)
		}
	}
}
