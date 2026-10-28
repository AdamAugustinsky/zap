// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_harmcomb.rs, MIT.
//
// MLow HP (harmonic/pitch) postfilter: the post-LPC-synthesis comb that resonates the output at the
// PITCH frequency. Structure per frame:
//
//	de-emphasis (AR1 leaky integrator {1,-0.995}) -> ARMA2 comb (MA2 numerator + AR2 denominator,
//	coefficients derived from the pitch lag f=1/lag) -> companion pre-emphasis (MA1 differentiator).
//
// The comb keys on the PITCH LAG (f=1/lag), not an energy ratio, and the AR denominator radius factor
// uses the `arr` curve (negative -> stable pole), not `arf` (positive -> unstable).
//
// SMPL_PI (3.1415927410125) and FRAME_LEN (320) are shared.go's smplPi / frameLen.

package mlow

import "sync"

// loEmph is the low-emphasis coef pair {1.0, -0.995}: AR1 = de-emphasis (leaky integrator), MA1 =
// companion pre-emphasis (differentiator). The comb is bracketed by these.
var loEmph = [2]float32{1.0, -0.995}

// 1.2 dB peak voiced pitch-comb curve: maf, arf (cos angle), arr (radius).
const hpPitchMaf float32 = 0.1

var (
	hpPitchArf = [2]float32{0.608057355, 0.070939485}
	hpPitchArr = [2]float32{-2.187380512, 2.291030664}
)

// Default (lag<=0) curve, corner 50 Hz -> f = 50/16000.
const hpDefMaf float32 = 0.1

var (
	hpDefArf = [2]float32{0.728508218, 0.476039848}
	hpDefArr = [2]float32{-4.363803713, 8.441854006}
)

const hpDefFcornerHz float32 = 50.0

const (
	lagChangeThreshold     float32 = 1.25
	hpPostfTransitionSpeed float32 = 2.0
)

// hpPostfilterState is the persistent HP-postfilter state. `lag_old < 0` marks a fresh/reset filter.
// `scratch_*` are per-frame working buffers hoisted off the hot path; each is fully overwritten
// (`[..n]`) before it is read, so they carry no state between frames. All fields are arrays, so Go
// assignment is Rust's derived Clone.
type hpPostfilterState struct {
	stateLoEmph1 float32
	stateLoEmph2 float32
	stateHp      [4]float32 // [ma2 x[-1], ma2 x[-2], ar2 y[-1], ar2 y[-2]]
	lagOld       float32
	xOld         [frameLen]float32
	coefMa       [3]float32
	coefAr       [3]float32
	scratchX     [frameLen]float32
	scratchYOld  [frameLen]float32
	scratchYTmp  [frameLen]float32
	scratchDummy [frameLen]float32
}

func defaultHpPostfilterState() hpPostfilterState {
	return hpPostfilterState{lagOld: -1.0}
}

// copyStateFrom copies the persistent filter state out of `src`, leaving this value's scratch
// buffers alone.
//
// Named rather than folded into `clone_from` because it is deliberately not a full copy: the
// `scratch_*` buffers are 5 KB of the 6.4 KB this struct occupies and each is fully
// overwritten (`[..n]`) before it is read, so a rollback has nothing to restore in them. The
// decoder's concealment snapshot runs on every packet while the rollback it protects is rare,
// which is what makes 5 KB of `memcpy` worth not doing.
func (h *hpPostfilterState) copyStateFrom(src *hpPostfilterState) {
	h.stateLoEmph1 = src.stateLoEmph1
	h.stateLoEmph2 = src.stateLoEmph2
	h.stateHp = src.stateHp
	h.lagOld = src.lagOld
	h.xOld = src.xOld
	h.coefMa = src.coefMa
	h.coefAr = src.coefAr
}

// cosApprox is the small-angle cosine `cos_approx(x) = 1 - 0.5*x^2`.
func cosApprox(x float32) float32 {
	return 1.0 - float32(0.5*x*x)
}

// smplPfFir3 is a 3-tap FIR with carried 2-sample input history (general/monic). Also reused by the
// excitation postfilter (comb #1).
func smplPfFir3(input []float32, n int, coef [3]float32, state *[2]float32, out []float32) {
	xm1 := state[0]
	xm2 := state[1]
	for i := 0; i < n; i++ {
		var p1 float32
		if i >= 1 {
			p1 = input[i-1]
		} else {
			p1 = xm1
		}
		var p2 float32
		if i >= 2 {
			p2 = input[i-2]
		} else if i == 1 {
			p2 = xm1
		} else {
			p2 = xm2
		}
		out[i] = float32(coef[0]*input[i]) + float32(coef[1]*p1) + float32(coef[2]*p2)
	}
	if n >= 2 {
		state[0] = input[n-1]
		state[1] = input[n-2]
	} else if n == 1 {
		state[1] = xm1
		state[0] = input[0]
	}
}

// smplFiltAr2 is the 2nd-order all-pole `y[n] = in[n] - c1*y[n-1] - c2*y[n-2]` (monic), state
// {y[-1],y[-2]}. Uses the scalar 4-wide unrolled block (precomputed coefficient powers) so the
// floating-point rounding is bit-exact with the scalar reference (the codec was built without NEON).
func smplFiltAr2(input []float32, n int, c1 float32, c2 float32, state *[2]float32, out []float32) {
	ytmp0 := state[1]
	ytmp1 := state[0]
	ar1 := -c1
	ar2 := -c2
	ar12 := float32(ar1 * ar1)
	ar13 := float32(ar1 * ar12)
	ar14 := float32(ar1 * ar13)
	imp1 := ar1
	imp2 := ar12 + ar2
	imp3 := ar13 + float32(2.0*ar1*ar2)
	imp4 := ar14 + float32(ar2*ar2) + float32(3.0*ar12*ar2)
	ymp1 := ar2
	ymp2 := float32(ar2 * imp1)
	ymp3 := float32(ar2 * imp2)
	ymp4 := float32(ar2 * imp3)
	nn := 0
	for nn+3 < n {
		xtmp0 := input[nn]
		xtmp1 := input[nn+1]
		xtmp2 := input[nn+2]
		out[nn+2] = xtmp2 + float32(imp1*xtmp1) + float32(imp2*xtmp0) + float32(imp3*ytmp1) + float32(ymp3*ytmp0)
		xtmp3 := input[nn+3]
		out[nn+3] = xtmp3 + float32(imp1*xtmp2) + float32(imp2*xtmp1) + float32(imp3*xtmp0) + float32(imp4*ytmp1) + float32(ymp4*ytmp0)
		out[nn] = xtmp0 + float32(imp1*ytmp1) + float32(ymp1*ytmp0)
		out[nn+1] = xtmp1 + float32(imp1*xtmp0) + float32(imp2*ytmp1) + float32(ymp2*ytmp0)
		ytmp0 = out[nn+2]
		ytmp1 = out[nn+3]
		nn += 4
	}
	for nn < n {
		out[nn] = input[nn] + float32(ar1*ytmp1) + float32(ar2*ytmp0)
		ytmp0 = ytmp1
		ytmp1 = out[nn]
		nn++
	}
	state[1] = ytmp0
	state[0] = ytmp1
}

// harmcombSmplFiltAr1 is the AR1 leaky integrator `y[n] = x[n] - c1*y[n-1]` (here the de-emphasis
// {1,-0.995}). Uses the scalar 5-wide unrolled block (precomputed `ar1` powers) for bit-exact
// rounding.
func harmcombSmplFiltAr1(input []float32, n int, c1 float32, state *float32, out []float32) {
	ar1 := -c1
	ar12 := float32(ar1 * ar1)
	ar13 := float32(ar1 * ar12)
	ar14 := float32(ar1 * ar13)
	ar15 := float32(ar1 * ar14)
	ytmp := *state
	nn := 0
	for nn+4 < n {
		xtmp0 := input[nn]
		xtmp1 := input[nn+1]
		xtmp2 := input[nn+2]
		xtmp3 := input[nn+3]
		xtmp4 := input[nn+4]
		out[nn+4] = xtmp4 + float32(ar1*xtmp3) + float32(ar12*xtmp2) + float32(ar13*xtmp1) + float32(ar14*xtmp0) + float32(ar15*ytmp)
		out[nn] = xtmp0 + float32(ar1*ytmp)
		out[nn+1] = xtmp1 + float32(ar1*xtmp0) + float32(ar12*ytmp)
		out[nn+2] = xtmp2 + float32(ar1*xtmp1) + float32(ar12*xtmp0) + float32(ar13*ytmp)
		out[nn+3] = xtmp3 + float32(ar1*xtmp2) + float32(ar12*xtmp1) + float32(ar13*xtmp0) + float32(ar14*ytmp)
		ytmp = out[nn+4]
		nn += 5
	}
	for nn < n {
		ytmp = input[nn] + float32(ytmp*ar1)
		out[nn] = ytmp
		nn++
	}
	*state = ytmp
}

// harmcombSmplFiltMa1 is MA1 `y[n] = x[n] + c1*x[n-1]` (here the companion pre-emphasis
// {1,-0.995}).
func harmcombSmplFiltMa1(input []float32, n int, c1 float32, state *float32, out []float32) {
	prev := *state
	for i := n - 1; i >= 1; i-- {
		out[i] = input[i] + float32(c1*input[i-1])
	}
	if n > 0 {
		out[0] = input[0] + float32(c1*prev)
		*state = input[n-1]
	}
}

// smplGetHpCoefs is the default fixed-corner ARMA2 biquad (also the encoder's input high-pass).
// `fcorner_hz` clamped to [5, 1500]; `f = fcorner/16000`.
func smplGetHpCoefs(fcornerHz float32) ([3]float32, [3]float32) {
	fc := clampF32(fcornerHz, 5.0, 1500.0)
	return smplCalcHpCoefs(hpDefMaf, hpDefArf, hpDefArr, fc/16000.0)
}

// smplFiltArma2 is MA2 numerator then AR2 denominator, shared 4-wide state {ma x[-1],x[-2], ar
// y[-1],y[-2]}.
//
// Rust allocates the n-sample intermediate per call (`vec![0f32; n]`); here it lives on the stack
// for n <= opusFrameSamps (every caller: the decoder's 320-sample frames and the encoder's 960-sample
// packet high-pass), with a heap fallback beyond that.
func smplFiltArma2(input []float32, n int, coefMa [3]float32, coefAr [3]float32, state *[4]float32, out []float32) {
	var tmpBuf [opusFrameSamps]float32
	var tmp []float32
	if n <= len(tmpBuf) {
		tmp = tmpBuf[:n]
	} else {
		tmp = make([]float32, n)
	}
	maSt := [2]float32{state[0], state[1]}
	smplPfFir3(input, n, coefMa, &maSt, tmp)
	state[0] = maSt[0]
	state[1] = maSt[1]
	arSt := [2]float32{state[2], state[3]}
	smplFiltAr2(tmp, n, coefAr[1], coefAr[2], &arSt, out)
	state[2] = arSt[0]
	state[3] = arSt[1]
}

// smplCalcHpCoefs builds the unity-numerator-DC comb biquad. The AR denominator is a resonance at
// the pitch angle `2*pi*arf*f` with radius `1 + arr*f` (arr negative -> stable), then the MA
// numerator is scaled so the comb has unity DC gain.
func smplCalcHpCoefs(maf float32, arf [2]float32, arr [2]float32, f float32) ([3]float32, [3]float32) {
	coefMa := [3]float32{1.0, float32(-2.0 * cosApprox(2.0*smplPi*maf*f)), 1.0}
	far := float32(arf[0]*f) + float32(arf[1]*f*f)
	rar := float32(arr[0]*f) + float32(arr[1]*f*f)
	coefAr := [3]float32{
		1.0,
		float32(-2.0 * cosApprox(2.0*smplPi*far) * (1.0 + rar)),
		1.0 + (float32(2.0*rar) + float32(rar*rar)),
	}
	sc := (1.0 - coefAr[1] + coefAr[2]) / (1.0 - coefMa[1] + coefMa[2])
	for i := range coefMa {
		coefMa[i] = float32(coefMa[i] * sc)
	}
	return coefMa, coefAr
}

// newCoefsInto: voiced pitch curve when lag>0 (f=1/lag), else the default 50 Hz-corner curve.
func newCoefsInto(coefMa *[3]float32, coefAr *[3]float32, lag float32) {
	var ma, ar [3]float32
	if lag > 0.0 {
		f := 1.0 / lag
		ma, ar = smplCalcHpCoefs(hpPitchMaf, hpPitchArf, hpPitchArr, f)
	} else {
		fc := clampF32(hpDefFcornerHz, 5.0, 1500.0)
		ma, ar = smplCalcHpCoefs(hpDefMaf, hpDefArf, hpDefArr, fc/16000.0)
	}
	*coefMa = ma
	*coefAr = ar
}

// rampDnOnce backs rampDn (the Rust OnceLock).
var rampDnOnce = sync.OnceValue(func() []float32 {
	dOmega := smplPi / (2.0 * (float32(frameLen) + 1.0))
	omega := dOmega
	v := make([]float32, 0, frameLen)
	for range frameLen {
		v = append(v, powF32(cosF32(omega), hpPostfTransitionSpeed))
		omega += dOmega
	}
	return v
})

// rampDn is the `cos(omega)^2` down-ramp for the lag-change overlap-add. `omega` is accumulated by
// repeated addition (not `d_omega * i`) to stay bit-exact with the reference table. Rust
// debug-asserts `len == FRAME_LEN`; the ramp always has FRAME_LEN entries.
func rampDn(l int) []float32 {
	_ = l
	return rampDnOnce()
}

// smplHpPostfilter applies the HP (pitch-harmonic) postfilter to one frame's post-LPC output. `lag`
// is the frame's average pitch lag (`sum(l^2)/sum(l)` over the subframe lags), 0 for unvoiced.
func smplHpPostfilter(st *hpPostfilterState, xIn []float32, n int, lag float32, out []float32) {
	// de-emphasis (AR1) into the x scratch (disjoint fields keep the input/output/state alias-free
	// across the arma2 calls below; each scratch is fully written before any read).
	harmcombSmplFiltAr1(xIn, n, loEmph[1], &st.stateLoEmph1, st.scratchX[:n])

	overlap := false
	if st.lagOld < 0.0 {
		newCoefsInto(&st.coefMa, &st.coefAr, lag)
		st.lagOld = lag
	} else if lag > lagChangeThreshold*st.lagOld || lagChangeThreshold*lag < st.lagOld {
		overlap = true
		smplFiltArma2(st.scratchX[:n], n, st.coefMa, st.coefAr, &st.stateHp, st.scratchYOld[:n])
		newCoefsInto(&st.coefMa, &st.coefAr, lag)
		st.lagOld = lag
		smplFiltArma2(st.xOld[:n], n, st.coefMa, st.coefAr, &st.stateHp, st.scratchDummy[:n])
	} else if lag != st.lagOld {
		newCoefsInto(&st.coefMa, &st.coefAr, lag)
		st.lagOld = lag
	}
	copy(st.xOld[:n], st.scratchX[:n])

	smplFiltArma2(st.scratchX[:n], n, st.coefMa, st.coefAr, &st.stateHp, st.scratchYTmp[:n])

	if overlap {
		ramp := rampDn(n)
		for i := 0; i < n; i++ {
			st.scratchYTmp[i] += float32((st.scratchYOld[i] - st.scratchYTmp[i]) * ramp[i])
		}
	}

	// companion pre-emphasis (MA1).
	harmcombSmplFiltMa1(st.scratchYTmp[:n], n, loEmph[1], &st.stateLoEmph2, out)
}
