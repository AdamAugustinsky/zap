// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pitch_enc.rs, MIT.
//
// The `#[cfg(test)]` PitchTables::debug_checksum and the `tests` module.

package mlow

import (
	"math"
	"testing"
)

// debugChecksum is the FNV-1a digest over every field (order-deterministic), for the seed-build
// tripwire.
func (p *pitchTables) debugChecksum() uint64 {
	h := uint64(0xcbf2_9ce4_8422_2325)
	feed := func(x uint64) {
		h ^= x
		h *= 0x0000_0100_0000_01b3
	}
	for i := range p.blocksegs {
		s := &p.blocksegs[i]
		feed(uint64(s.nblocks))
		for _, b := range s.blocks {
			feed(uint64(b))
		}
		for _, l := range s.seglens {
			feed(uint64(l))
		}
	}
	for i := range p.blocktracks {
		t := &p.blocktracks[i]
		for _, v := range t.track {
			feed(uint64(v))
		}
		feed(uint64(math.Float32bits(t.meanblock)))
		feed(uint64(math.Float32bits(t.trackdeltas)))
	}
	for _, v := range p.blocksegs2idx {
		feed(uint64(v))
	}
	for _, v := range p.blocksegIdxCmf {
		feed(uint64(v))
	}
	for _, row := range p.deltaLagCmfs {
		for _, v := range row {
			feed(uint64(v))
		}
	}
	for _, q := range p.blocksegsIx {
		feed(uint64(q[0]))
		feed(uint64(q[1]))
	}
	for _, q := range p.firstblockRange {
		feed(uint64(q[0]))
		feed(uint64(q[1]))
	}
	for _, row := range p.blockTransitionCmf {
		for _, v := range row {
			feed(uint64(v))
		}
	}
	return h
}

func smplPitchEncFloats(v val) []float32 {
	a := v.Arr()
	out := make([]float32, len(a))
	for i, x := range a {
		out[i] = float32(x.F64())
	}
	return out
}

// Feed the reference encoder's exact per-frame ltp_buf + F2 into the estimator (threading the
// cross-frame predictor + frame-boundary reset) and require the outputs to converge to the
// reference: pitchcorr (tight float tol), avg_lag, per-subframe laginds, blockseg_idx,
// harm_strength (cache-aliasing tol). This is the rigorous proof the estimator is faithful.
func TestSmplPitchEnc_PitchEstimatorMatchesCGroundTruth(t *testing.T) {
	arr := loadPacked(t, "pitchio_ground_truth.cbor.zst").Arr()
	if !(len(arr) >= 30) {
		t.Fatalf("expected >=30 records, got %d", len(arr))
	}

	// Thread prev_lag/prev_pitch_corr across frames (the estimator carries these), but seed
	// prev_lagblk/prev_lagidx from the reference dump per frame so the rate-bias survivor selection
	// uses the exact predictor (its reset timing depends on the voiced decision, out of scope for
	// the isolated estimator test).
	st := defaultPitchEstState()
	maxPcErr := float32(0.0)
	maxAvglagErr := float32(0.0)
	maxHarmErr := float32(0.0)
	lagindMismatch := 0
	bsxMismatch := 0
	checked := 0
	for _, rec := range arr {
		_ = rec.Get("frame").I64()
		cav := rec.Get("cav").I64() != 0
		st.prevLagblk = int32(rec.Get("prev_lagblk").I64())
		st.prevLagidx = int32(rec.Get("prev_lagidx").I64())
		ltpBuf := smplPitchEncFloats(rec.Get("ltp_buf"))
		if len(ltpBuf) != maxLtpBufLen {
			t.Fatalf("ltp_buf length %d != %d", len(ltpBuf), maxLtpBufLen)
		}
		f2v := smplPitchEncFloats(rec.Get("F2"))
		if len(f2v) != fLen {
			t.Fatalf("F2 length %d != %d", len(f2v), fLen)
		}
		var f2 [fLen]float32
		copy(f2[:], f2v)

		res := pitchWithSearch(
			&st,
			ltpBuf,
			&f2,
			cav,
			pitchSearch{
				survivors:      24,
				lowComplexity:  false,
				lowRate:        false,
				previousWeight: 0.7981,
				deltaWeight:    0.1439,
			},
		)

		if cav {
			pcC := float32(rec.Get("pitchcorr").F64())
			avgC := float32(rec.Get("avg_lag").F64())
			harmC := float32(rec.Get("harm").F64())
			bsxC := int(rec.Get("blockseg_idx").I64())
			lagindsC := rec.Get("laginds").I32s()
			maxPcErr = maxF32(maxPcErr, absF32(res.pitchcorr-pcC))
			maxAvglagErr = maxF32(maxAvglagErr, absF32(res.avgLag-avgC))
			maxHarmErr = maxF32(maxHarmErr, absF32(res.harmStrength-harmC))
			lagMm := false
			for sf := 0; sf < numSubframes; sf++ {
				if res.laginds[sf] != lagindsC[sf] {
					lagMm = true
				}
			}
			if res.blocksegIdx != bsxC {
				bsxMismatch++
			}
			if lagMm {
				lagindMismatch++
			}
			checked++
		}
	}
	if !(checked >= 20) {
		t.Fatalf("too few active frames checked: %d", checked)
	}
	if !(maxPcErr < 1e-3) {
		t.Fatalf("pitchcorr diverges from reference: max_err=%v", maxPcErr)
	}
	if !(maxAvglagErr < 1e-3) {
		t.Fatalf("avg_lag diverges from reference: max_err=%v", maxAvglagErr)
	}
	if lagindMismatch != 0 {
		t.Fatalf("per-subframe laginds diverge from reference: %d != 0", lagindMismatch)
	}
	if bsxMismatch != 0 {
		t.Fatalf("blockseg_idx diverges from reference: %d != 0", bsxMismatch)
	}
	if !(maxHarmErr < 0.05) {
		t.Fatalf("harm_strength diverges beyond cache-aliasing tol: %v", maxHarmErr)
	}
}

func TestSmplPitchEnc_PitchEstimatorMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_pitch.cbor.zst").Arr()
	if len(records) != 330 {
		t.Fatalf("records %d != 330", len(records))
	}
	for i, r := range records {
		state := pitchEstState{
			prevLag:       float32(r.Get("prev_lag").F64()),
			prevPitchCorr: float32(r.Get("prev_pitch_corr").F64()),
			prevLagblk:    int32(r.Get("prev_lagblk").I64()),
			prevLagidx:    int32(r.Get("prev_lagidx").I64()),
		}
		f2v := smplPitchEncFloats(r.Get("F2"))
		if len(f2v) != fLen {
			t.Fatalf("frame %d: F2 length %d != %d", i, len(f2v), fLen)
		}
		var f2 [fLen]float32
		copy(f2[:], f2v)
		ltp := smplPitchEncFloats(r.Get("ltp_buf"))
		if len(ltp) != maxLtpBufLen {
			t.Fatalf("frame %d: ltp_buf length %d != %d", i, len(ltp), maxLtpBufLen)
		}
		result := pitchWithSearch(
			&state,
			ltp,
			&f2,
			r.Get("cav").I64() != 0,
			pitchSearch{
				survivors:      int(r.Get("numstates").U64()),
				lowComplexity:  r.Get("low_complexity").I64() != 0,
				lowRate:        r.Get("low_rate").I64() != 0,
				previousWeight: prevwght,
				deltaWeight:    pitchDeltawght,
			},
		)
		if !(absF32(result.pitchcorr-float32(r.Get("pitchcorr").F64())) < 1e-3) {
			t.Fatalf("frame %d: pitch correlation %v vs %v, prev=%v, lag=%v vs %v",
				i, result.pitchcorr, r.Get("pitchcorr").F64(), state.prevPitchCorr, result.avgLag, r.Get("avg_lag").F64())
		}
		if !(absF32(result.avgLag-float32(r.Get("avg_lag").F64())) < 1e-3) {
			t.Fatalf("frame %d: average lag %v vs %v", i, result.avgLag, r.Get("avg_lag").F64())
		}
		if !(absF32(result.harmStrength-float32(r.Get("harm").F64())) < 0.05) {
			t.Fatalf("frame %d: harmonicity", i)
		}
		want := r.Get("laginds").Arr()
		for k := 0; k < min(len(result.laginds), len(want)); k++ {
			if result.laginds[k] != int32(want[k].I64()) {
				t.Fatalf("frame %d: lag index: %d != %d", i, result.laginds[k], want[k].I64())
			}
		}
		if result.blocksegIdx != int(r.Get("blockseg_idx").U64()) {
			t.Fatalf("frame %d: contour: %d != %d", i, result.blocksegIdx, r.Get("blockseg_idx").U64())
		}
	}
}
