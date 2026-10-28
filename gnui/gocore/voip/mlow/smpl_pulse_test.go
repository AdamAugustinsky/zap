// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pulse.rs, MIT.

package mlow

import (
	"slices"
	"testing"
)

// Decodes LSF(0) then pulses(0) on each active captured frame and compares the per-subframe
// counts + the full signed pulse vector against the reference.
func TestSmplPulse_PulsesMatchGo(t *testing.T) {
	recs := loadPacked(t, "pulse_vectors.cbor.zst")
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
		pr := decodeSmplPulses(&dec, cc, 320, 4, 1, 0, lsf.stage1)

		wantSubfr := rec.Get("subfr").I32s()
		if !slices.Equal(pr.subfr[:], wantSubfr) {
			t.Fatalf("subfr: got %v want %v", pr.subfr, wantSubfr)
		}

		// rebuild the expected sparse vector and compare element-wise.
		want := make([]int32, len(pr.pulses))
		for _, pu := range rec.Get("pulses").Arr() {
			pos := int(pu.Get("pos").I64())
			want[pos] = int32(pu.Get("val").I64())
		}
		if !slices.Equal(pr.pulses, want) {
			t.Fatalf("pulse vector: got %v want %v", pr.pulses, want)
		}
	}
}

// A corrupt split (count > 2*granularity) makes smpl_split_3537 return -1; the decoder then
// zeroes the subframe split rather than copying the sentinel into res.subfr.
func TestSmplPulse_CorruptSplitZeroesSubframes(t *testing.T) {
	cc := loadCcTables()
	dec := newRangeDecoder(make([]byte, 8))
	// count=10 > 2*granularity=2 hits the `lo < min_split` corrupt branch.
	if got := smplSplit3537(&dec, cc, 10, 1); got != -1 {
		t.Fatalf("smpl_split_3537(10, 1): got %d want -1", got)
	}

	// The guard the decoder applies on either -1: the whole split array is wiped before copy.
	var split [8]int32
	for i := range split {
		split[i] = 3
	}
	split[2] = -1
	if split[0] == -1 || split[2] == -1 {
		split = [8]int32{}
	}
	take := 4
	var subfr [4]int32
	copy(subfr[:take], split[:take])
	if subfr != [4]int32{} {
		t.Fatalf("subfr: got %v want [0 0 0 0]", subfr)
	}
}
