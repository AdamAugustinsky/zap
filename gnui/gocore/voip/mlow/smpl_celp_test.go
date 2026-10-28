// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_celp.rs, MIT.
//
// Tests for the CELP excitation encoder (the Rust `#[cfg(test)] mod tests`).

package mlow

import (
	"math"
	"slices"
	"testing"
)

// smplCelpReferenceMaxiK keeps this oracle independent of the production fallback so a shared
// defect cannot self-validate.
func smplCelpReferenceMaxiK(x []float32, idx []int32, xLen, k int) {
	var taken [smplMaxSfLen]bool
	for d := range idx[:k] {
		best := float32(-math.MaxFloat32)
		bestIndex := 0
		found := false
		for i := 0; i < xLen; i++ {
			if !taken[i] && (!found || x[i] > best) {
				best = x[i]
				bestIndex = i
				found = true
			}
		}
		taken[bestIndex] = true
		idx[d] = int32(bestIndex)
	}
}

// smplCelpCheckMaxiK compares the entire destination to observe writes past the requested
// survivor count.
func smplCelpCheckMaxiK(t *testing.T, x []float32, xLen, k int) {
	t.Helper()
	var expected [smplCelpMaxNumsurv]int32
	for i := range expected {
		expected[i] = -123
	}
	actual := expected
	smplCelpReferenceMaxiK(x, expected[:], xLen, k)
	smplGetMaxiK(x, actual[:], xLen, k)
	if actual != expected {
		t.Fatalf("assertion `left == right` failed: k=%d, live=%v\n  left: %v\n right: %v", k, x[:xLen], actual, expected)
	}
}

// A total-order comparator would change the reference's NaN and signed-zero choices.
func TestSmplCelp_TopKMatchesSelectionForExhaustiveTiesAndNonfiniteValues(t *testing.T) {
	values := [7]float32{
		-1.0,
		float32(math.Copysign(0, -1)),
		0.0,
		1.0,
		float32(math.Inf(-1)),
		float32(math.Inf(1)),
		float32(math.NaN()),
	}
	smplCelpCheckMaxiK(t, []float32{}, 0, 0)
	pow := 1
	for ln := 1; ln <= 5; ln++ {
		pow *= 7
		for key0 := 0; key0 < pow; key0++ {
			key := key0
			var x [8]float32
			for i := range x {
				x[i] = float32(math.Inf(1))
			}
			for i := range x[:ln] {
				x[i] = values[key%7]
				key /= 7
			}
			for k := 0; k <= smplCelpMaxNumsurv; k++ {
				smplCelpCheckMaxiK(t, x[:], ln, k)
			}
		}
	}
}

// Mix raw IEEE-754 patterns with a small integer alphabet that forces repeated tied scores.
func TestSmplCelp_TopKMatchesSelectionOverAMillionVectors(t *testing.T) {
	seed := uint32(0x715fba91)
	next := func() uint32 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return seed
	}
	var x [smplMaxSfLen]float32
	for c := 0; c < 1_000_000; c++ {
		ln := 1 + int(next())%smplMaxSfLen
		k := int(next()) % (smplCelpMaxNumsurv + 1)
		for i := range x[:ln] {
			bits := next()
			if c%4 == 0 {
				x[i] = float32(int32(bits) % 5)
			} else {
				x[i] = math.Float32frombits(bits)
			}
		}
		smplCelpCheckMaxiK(t, x[:], ln, k)
	}
}

func TestSmplCelp_TablesBuildAndHaveExpectedShapes(t *testing.T) {
	tb := celpTables()
	// fcbgains_v[0] = 10^(0.05 * -100) and step 3dB.
	if !(absF32(tb.fcbgainsV[0]-powF32(10, float32(0.05)*-100.0)) < 1e-9) {
		t.Fatalf("fcbgains_v[0] = %v", tb.fcbgainsV[0])
	}
	if !(absF32(tb.fcbgainsV[33]-powF32(10, float32(0.05)*(float32(33.0)*3.0-100.0))) < 1e-6) {
		t.Fatalf("fcbgains_v[33] = %v", tb.fcbgainsV[33])
	}
	// fcbgains_uv spans -90..0 dB in 1dB steps; index 90 == 0 dB == 1.0.
	if !(absF32(tb.fcbgainsUv[90]-1.0) < 1e-6) {
		t.Fatalf("fcbgains_uv[90] = %v", tb.fcbgainsUv[90])
	}
	if !(absF32(tb.fcbgainsUv[0]-powF32(10, float32(0.05)*-90.0)) < 1e-9) {
		t.Fatalf("fcbgains_uv[0] = %v", tb.fcbgainsUv[0])
	}
	// inv-prob tables are positive (2^bits).
	for i, v := range tb.acbgInvProbLr {
		if !(v > 0.0) {
			t.Fatalf("acbg_inv_prob_lr[%d] = %v", i, v)
		}
	}
	for i, v := range tb.fcbgVInvProb {
		if !(v > 0.0) {
			t.Fatalf("fcbg_v_inv_prob[%d] = %v", i, v)
		}
	}
}

func TestSmplCelp_DcmfToCmfIsIntegerExact(t *testing.T) {
	// Reproduce the first acbgains_lr row through the integer path; the result must be a strictly
	// increasing u16 sequence starting at 0 (cumulative).
	var cmf [smplAcbgN + 1]uint16
	smplDcmfToCmf(smplAcbgainsDcmfLr[:smplAcbgN], smplAcbgN, cmf[:])
	if cmf[0] != 0 {
		t.Fatalf("cmf[0] = %d, want 0", cmf[0])
	}
	for i := 1; i < len(cmf); i++ {
		if !(cmf[i] > cmf[i-1]) {
			t.Fatalf("cmf not strictly increasing at %d: %v", i, cmf)
		}
	}
}

func smplCelpUnitResp(n int) []float32 {
	r := make([]float32, n)
	r[0] = 1.0
	return r
}

func smplCelpSine(n int, w, a float32) []float32 {
	r := make([]float32, n)
	for i := range r {
		r[i] = sinF32(float32(i)*w) * a
	}
	return r
}

func TestSmplCelp_EncodeUnvoicedRuns(t *testing.T) {
	percRespLen := 32
	fcbSubfrlen := 80
	enc := newCelpEncoder(false, percRespLen, fcbSubfrlen, 4)
	resLpc := smplCelpSine(fcbSubfrlen, 0.3, 0.1)
	var predcoef [17]float32
	predcoef[0] = 1.0
	predcoef[1] = -0.5
	percWghtResp := smplCelpUnitResp(percRespLen)
	// Unvoiced: lags[1] <= 0.
	lags := []float32{0.0, 0.0, 0.0}
	var surv [smplMaxPulsesPerSf]int16
	for i := range surv {
		surv[i] = 1
	}
	out := enc.encodeSubframe(resLpc, &predcoef, percWghtResp, lags, [2]float32{1.0, 1.0}, [2]int16{8, 8}, surv[:])
	if out.acbIdx[smplCelpIdxMain] != -1 {
		t.Fatalf("acb_idx[MAIN] = %d, want -1", out.acbIdx[smplCelpIdxMain])
	}
	if len(out.excLpc[:fcbSubfrlen]) != fcbSubfrlen || slices.ContainsFunc(out.excLpc[fcbSubfrlen:], func(v float32) bool { return v != 0 }) {
		t.Fatalf("exc_lpc is not exactly fcb_subfrlen samples")
	}
	if !(out.nPulses[smplCelpIdxMain] >= 0) {
		t.Fatalf("n_pulses[MAIN] = %d", out.nPulses[smplCelpIdxMain])
	}
}

func TestSmplCelp_EncodeVoicedRuns(t *testing.T) {
	percRespLen := 32
	fcbSubfrlen := 80
	enc := newCelpEncoder(false, percRespLen, fcbSubfrlen, 4)
	// Prime acb_state with a periodic signal so the LTP basis is meaningful.
	for i := range enc.acbState {
		enc.acbState[i] = sinF32(float32(i) * 0.2)
	}
	resLpc := smplCelpSine(fcbSubfrlen, 0.25, 0.2)
	var predcoef [17]float32
	predcoef[0] = 1.0
	predcoef[1] = -0.4
	percWghtResp := smplCelpUnitResp(percRespLen)
	// Voiced: integer lag 60 for both 40-sample sub-blocks (fcb_subfrlen/40 = 2).
	lags := []float32{60.0, 60.0, 60.0}
	var surv [smplMaxPulsesPerSf]int16
	for i := range surv {
		surv[i] = 2
	}
	out := enc.encodeSubframe(resLpc, &predcoef, percWghtResp, lags, [2]float32{1.0, 1.0}, [2]int16{6, 6}, surv[:])
	if !(out.acbIdx[smplCelpIdxMain] >= 0) {
		t.Fatalf("acb_idx[MAIN] = %d", out.acbIdx[smplCelpIdxMain])
	}
	if len(out.excLpc[:fcbSubfrlen]) != fcbSubfrlen || slices.ContainsFunc(out.excLpc[fcbSubfrlen:], func(v float32) bool { return v != 0 }) {
		t.Fatalf("exc_lpc is not exactly fcb_subfrlen samples")
	}
}

func TestSmplCelp_EncodeVoicedFractionalLagGreedyRuns(t *testing.T) {
	// High-rate (low_rate=false) + surv[max-2]==1 triggers the greedy search path; a fractional
	// lag exercises the interpolation branch of smplSynLtpBasis.
	percRespLen := 32
	fcbSubfrlen := 80
	enc := newCelpEncoder(false, percRespLen, fcbSubfrlen, 4)
	for i := range enc.acbState {
		enc.acbState[i] = sinF32(float32(i) * 0.17)
	}
	resLpc := smplCelpSine(fcbSubfrlen, 0.25, 0.2)
	var predcoef [17]float32
	predcoef[0] = 1.0
	predcoef[1] = -0.4
	percWghtResp := smplCelpUnitResp(percRespLen)
	lags := []float32{55.5, 55.5, 55.5} // fractional
	var surv [smplMaxPulsesPerSf]int16
	for i := range surv {
		surv[i] = 1
	}
	out := enc.encodeSubframe(resLpc, &predcoef, percWghtResp, lags, [2]float32{1.0, 1.0}, [2]int16{4, 4}, surv[:])
	if !(out.acbIdx[smplCelpIdxMain] >= 0) {
		t.Fatalf("acb_idx[MAIN] = %d", out.acbIdx[smplCelpIdxMain])
	}
	if len(out.excLpc[:fcbSubfrlen]) != fcbSubfrlen || slices.ContainsFunc(out.excLpc[fcbSubfrlen:], func(v float32) bool { return v != 0 }) {
		t.Fatalf("exc_lpc is not exactly fcb_subfrlen samples")
	}
}

// Go-only guard: the encoder is the hottest per-frame path and must stay allocation-free in
// steady state across the greedy, deldec and pitch-sharpened deldec searches.
func TestSmplCelp_EncodeSubframeDoesNotAllocate(t *testing.T) {
	percRespLen := 32
	fcbSubfrlen := 80
	resLpc := smplCelpSine(fcbSubfrlen, 0.25, 0.2)
	var predcoef [17]float32
	predcoef[0] = 1.0
	predcoef[1] = -0.4
	percWghtResp := smplCelpUnitResp(percRespLen)
	var numsurv [smplMaxPulsesPerSf]int16
	smplDistributeFcbSurv(numsurv[:], 10, 24)
	var greedy [smplMaxPulsesPerSf]int16
	for i := range greedy {
		greedy[i] = 1
	}
	for _, lowRate := range []bool{false, true} {
		for _, surv := range [][]int16{numsurv[:], greedy[:]} {
			enc := newCelpEncoder(lowRate, percRespLen, fcbSubfrlen, 4)
			for i := range enc.acbState {
				enc.acbState[i] = sinF32(float32(i) * 0.17)
			}
			sf := 0
			allocs := testing.AllocsPerRun(20, func() {
				lags := []float32{55.5, 60.0, 60.0}
				if sf%3 == 2 {
					lags = []float32{0, 0, 0}
				}
				sf++
				_ = enc.encodeSubframe(resLpc, &predcoef, percWghtResp, lags, [2]float32{1.0, 1.0}, [2]int16{10, 6}, surv)
			})
			if allocs != 0 {
				t.Fatalf("lowRate=%v: %v allocs per encodeSubframe", lowRate, allocs)
			}
		}
	}
}
