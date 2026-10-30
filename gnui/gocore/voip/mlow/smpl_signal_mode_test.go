// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_signal_mode.rs, MIT.
//
// Tests of the voiced/unvoiced classifier.

package mlow

import "testing"

func smplSignalModeVec(v val) []float32 {
	a := v.Arr()
	out := make([]float32, len(a))
	for i, x := range a {
		out[i] = float32(x.F64())
	}
	return out
}

// Feed the reference encoder's exact per-frame pitchcorr/avg_lag/harm/lags/F2/sp_act_prob (in
// stream order, threading one VuvMode) and require our voicing_strength + voiced decision to match
// the reference output. Isolates the classifier from the pitch estimator.
func TestSmplSignalMode_SignalModeMatchesCGroundTruth(t *testing.T) {
	arr := loadPacked(t, "sigmode_ground_truth.cbor.zst").Arr()
	if !(len(arr) >= 12) {
		t.Fatalf("need vectors")
	}
	vuv := defaultVuvMode()
	maxErr := float32(0.0)
	maxHarmErr := float32(0.0)
	for _, rec := range arr {
		pitchcorr := float32(rec.Get("pitchcorr").F64())
		avgLag := float32(rec.Get("avg_lag").F64())
		harm := float32(rec.Get("harm").F64())
		sp := float32(rec.Get("sp_act_prob").F64())
		vstrC := float32(rec.Get("vstr").F64())
		voicedC := rec.Get("voiced").I64() != 0
		lags := smplSignalModeVec(rec.Get("lags"))
		f2v := smplSignalModeVec(rec.Get("F2"))
		var f2 [smplFLen]float32
		if len(f2v) != smplFLen {
			t.Fatalf("F2 length %d != %d", len(f2v), smplFLen)
		}
		copy(f2[:], f2v)

		// Validate harm_strength_at against the reference harm on frames where the pitch search
		// ran. On inactive frames the pitch search early-returns (lag clamped to the 32-sample
		// floor) and never computes harmonicity, leaving harm at its 0.0 init, not a recompute
		// target.
		if avgLag > 33.0 {
			f2w := buildF2w(&f2)
			harmRs := harmStrengthAt(avgLag, &f2w)
			maxHarmErr = maxF32(maxHarmErr, absF32(harmRs-harm))
		}

		vstrRs := signalModeWithBias(pitchcorr, lags, avgLag, harm, &f2, sp, &vuv, -0.1038)
		maxErr = maxF32(maxErr, absF32(vstrRs-vstrC))
		// Voiced decision (all dump frames are coded_as_active_voice).
		if (vstrRs > 0.0) != voicedC {
			t.Fatalf("voiced flip frame vstr_rs=%v vstr_c=%v", vstrRs, vstrC)
		}
	}
	if !(maxErr < 1e-4) {
		t.Fatalf("voicing_strength diverges from reference: max_err=%v", maxErr)
	}
	// harm_strength_at is exact PER call, but the reference reuses a survivor-loop cache keyed by
	// a quantized harm bin, so its FINAL value can be one computed at a different (bin-sharing)
	// survivor lag. A fresh-cache recompute is therefore close but not bit-exact without the full
	// pitch survivor sequence; bound the residual.
	if !(maxHarmErr < 0.05) {
		t.Fatalf("harm_strength diverges from reference beyond cache-aliasing tolerance: %v", maxHarmErr)
	}
}

func TestSmplSignalMode_SignalModeMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_signal_mode.cbor.zst").Arr()
	if len(records) != 330 {
		t.Fatalf("records %d != 330", len(records))
	}
	for i, r := range records {
		initial := smplSignalModeVec(r.Get("vuv_in"))
		state := vuvMode{
			voicingPrev: initial[0],
			lastLagPrev: initial[1],
			nrgLoBgn:    initial[2],
			nrgHiBgn:    initial[3],
		}
		f2v := smplSignalModeVec(r.Get("F2"))
		if len(f2v) != smplFLen {
			t.Fatalf("frame %d: F2 length %d != %d", i, len(f2v), smplFLen)
		}
		var f2 [smplFLen]float32
		copy(f2[:], f2v)
		lags := smplSignalModeVec(r.Get("lags"))
		value := smplGetSignalMode(
			float32(r.Get("pitchcorr").F64()),
			lags,
			float32(r.Get("avg_lag").F64()),
			float32(r.Get("harm").F64()),
			&f2,
			float32(r.Get("sp_act_prob").F64()),
			&state,
		)
		expected := float32(r.Get("vstr").F64())
		if !(absF32(value-expected) < 1e-5) {
			t.Fatalf("frame %d: strength %v vs %v", i, value, expected)
		}
		actual := [4]float32{state.voicingPrev, state.lastLagPrev, state.nrgLoBgn, state.nrgHiBgn}
		want := smplSignalModeVec(r.Get("vuv_out"))
		for k := 0; k < min(len(actual), len(want)); k++ {
			got, w := actual[k], want[k]
			if !(absF32(got-w) < float32(1e-5*maxF32(absF32(w), 1.0))) {
				t.Fatalf("frame %d: VUV state %v vs %v", i, got, w)
			}
		}
		voiced := b2i32(value > 0.0 && r.Get("cav").I64() != 0)
		if voiced != int32(r.Get("voiced").I64()) {
			t.Fatalf("frame %d: voiced %d != %d", i, voiced, r.Get("voiced").I64())
		}
	}
}
