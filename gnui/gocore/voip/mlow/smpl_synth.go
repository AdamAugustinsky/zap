// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_synth.rs, MIT.
//
// MLow (smpl_audio_codec) SYNTHESIS LB-core: turns LB parameters (LSF, pulses, LTP, gains) into
// 16 kHz PCM. Implements WASM func 3597's low-band core (NLSF reconstruct, NLSF2A, pulse excitation
// times gain, fractional LTP, order-16 LPC synthesis). `synth_internal_frame` is the
// encoder's analysis-by-synthesis pass (driven from `analysis.rs`), not the decoder's playout. The
// decoder's real HP pitch-harmonic postfilter is the ungated `smpl_hp_postfilter` call in
// `smpl_celpdec.rs`; the per-subframe excitation comb here is unused at the current operating point.
//
// The float constants below are byte-exact copies of the reference f32 literals (the extra digits
// round to the same f32 the WASM uses).

package mlow

import "math"

// SMPL_SUBFR_LEN (80, 5 ms @ 16 kHz) is shared.go's smplSubfrLen.
const (
	smplOrder                  = 16
	smplIntfLen                = 320 // 20 ms internal frame
	smplSubfrCount             = 4
	smplLtpHist                = 728
	smplFracStateLen   int32   = 728
	smplVoicedNormGain float64 = 1.0
	ltpHistLen                 = smplLtpHist + smplIntfLen + 64
)

const (
	smplNlsfWeightWMax    float32 = 999.9999
	smplNlsfWeightEps     float32 = 0.0009999999
	smplPiF32             float32 = 3.1415927410125
	smplStabilizeMaxLoops int32   = 1000
	smplStabilizeEps      float32 = 9.5367431640625e-07
)

// smplFir16 is the 16-tap symmetric fractional-delay interpolation FIR (WASM mem 0xe8780,
// func 3523/3507).
var smplFir16 = [16]float32{
	-0.000006392598606907995,
	0.00011064113641623408,
	-0.0009153038263320923,
	0.0048477197997272015,
	-0.018698347732424736,
	0.05759090930223465,
	-0.15997476875782013,
	0.617045521736145,
	0.6170454621315002,
	-0.15997475385665894,
	0.05759090557694435,
	-0.018698347732424736,
	0.0048477197997272015,
	-0.0009153038263320923,
	0.00011064114369219169,
	-0.0000063925981521606445,
}

type smplSynthTables struct {
	// `[stage1][config][grid][coeff]` -> per-symbol NLSF residual values.
	valtables [][][][][]float32
	// `[stage1][grid]` -> 16 base NLSF (radians, half-scale).
	centroids [][][]float32
	// `[stage1][grid]` -> 16x16 decorrelation matrix (`mat[row][col]`).
	matrices [][][][]float32
	// `[stage1]` -> 17 NLSF stabilize minimum spacings.
	minSpacing [][]float32
	// grid==16 base NLSF tables (selected INVERTED by signal type).
	grid16W     [][]float32
	grid16Alpha []float32
	// `[sig][config]` -> 256-entry flat column-major grid==16 decorrelation matrix.
	grid16Matrices [][][]float32
}

func loadSmplSynthTables() *smplSynthTables {
	return &lsfBuilt().synth
}

// smplNlsfLaroiaWeights is func 3530 (silk_NLSF_VQ_weights_laroia): inverse-gap weights
// w[k] = invgap[k] + invgap[k+1].
func smplNlsfLaroiaWeights(nlsf []float32, out []float32) {
	var inv [smplOrder + 1]float32
	clamp := func(gap float32) float32 {
		if gap > smplNlsfWeightEps {
			return 1.0 / gap
		}
		return smplNlsfWeightWMax
	}
	inv[0] = clamp(nlsf[0])
	prev := nlsf[0]
	for k := 1; k < smplOrder; k++ {
		gap := nlsf[k] - prev
		inv[k] = clamp(gap)
		prev = nlsf[k]
	}
	inv[smplOrder] = clamp(smplPiF32 - nlsf[smplOrder-1])
	for k := 0; k < smplOrder; k++ {
		out[k] = inv[k] + inv[k+1]
	}
}

// smplNlsfDecorr is func 3485 (decorrelation-matrix apply): out[r] = sum_c mat[c*16 + r] * vec[c]
// (column-major mat).
func smplNlsfDecorr(mat []float32, vec []float32, out []float32) {
	var scr [smplOrder]float32
	v0 := vec[0]
	for r := 0; r < smplOrder; r++ {
		scr[r] = float32(v0 * mat[r])
	}
	for c := 1; c < min(len(vec), smplOrder); c++ {
		v := vec[c]
		base := c * smplOrder
		for r := 0; r < smplOrder; r++ {
			scr[r] += float32(mat[base+r] * v)
		}
	}
	copy(out[:smplOrder], scr[:])
}

// smplReconstructNlsf reconstructs the order-16 NLSF (radians) from the decoded LSF indices
// (f3597 1437..2002). It allocates the result; the hot paths use [smplReconstructNlsfInto].
func smplReconstructNlsf(
	t *smplSynthTables,
	stage1 int,
	config int,
	grid int,
	stage2 *[16]int32,
	prevNlsf []float32,
) []float32 {
	return smplReconstructNlsfInto(make([]float32, smplOrder), t, stage1, config, grid, stage2, prevNlsf)
}

// smplReconstructNlsfInto is Rust `smpl_reconstruct_nlsf` writing the NLSF into `out` (length
// smplOrder) instead of a fresh Vec, and returning it. `out` may alias `prevNlsf`: every read of
// `prevNlsf` happens before the first write to `out`.
func smplReconstructNlsfInto(
	out []float32,
	t *smplSynthTables,
	stage1 int,
	config int,
	grid int,
	stage2 *[16]int32,
	prevNlsf []float32,
) []float32 {
	out = out[:smplOrder]
	val := t.valtables[stage1][config][grid]
	var resid [smplOrder]float32
	for k := range resid {
		sym := stage2[k]
		if sym >= 0 && int(sym) < len(val[k]) {
			resid[k] = val[k][sym]
		}
	}

	if grid == 16 {
		// grid==16: interpolate base between prevNLSF and the inverted grid16 base table.
		var base [smplOrder]float32
		baseTbl := t.grid16W[1-stage1]
		alpha := t.grid16Alpha[stage1]
		for k := 0; k < smplOrder; k++ {
			var pv float32
			if k < len(prevNlsf) {
				pv = prevNlsf[k]
			} else {
				pv = 0.0
			}
			base[k] = pv + float32(alpha*(baseTbl[k]-pv))
		}
		var w [smplOrder]float32
		smplNlsfLaroiaWeights(base[:], w[:])
		for i := range w {
			w[i] = float32(math.Sqrt(float64(w[i])))
		}
		var decorr [smplOrder]float32
		mat16 := t.grid16Matrices[stage1][config]
		smplNlsfDecorr(mat16, resid[:], decorr[:])
		for k := 0; k < smplOrder; k++ {
			out[k] = base[k] + decorr[k]/w[k]
		}
		smplStabilizeNlsf(out, t.minSpacing[stage1])
		return out
	}

	// matrix case (grid < 16): NLSF[r] = 2*centroid[r] + sum_c mat[c][r]*resid[c].
	cent := t.centroids[stage1][grid]
	mat := t.matrices[stage1][grid]
	for r := 0; r < smplOrder; r++ {
		acc := float32(2.0 * cent[r])
		for c := 0; c < smplOrder; c++ {
			acc += float32(mat[c][r] * resid[c])
		}
		out[r] = acc
	}
	smplStabilizeNlsf(out, t.minSpacing[stage1])
	return out
}

// smplStabilizeNlsf is func 3533 (silk_NLSF_stabilize): enforce minimum spacing + ordering in the
// margin domain.
func smplStabilizeNlsf(nlsf []float32, minSpacing []float32) {
	const pi = smplPiF32
	const l = smplOrder
	var marg [l + 1]float32
	marg[0] = nlsf[0] - minSpacing[0]
	for i := 1; i < l; i++ {
		marg[i] = nlsf[i] - nlsf[i-1] - minSpacing[i]
	}
	marg[l] = pi - nlsf[l-1] - minSpacing[l]

	argmin := func(marg *[l + 1]float32) (float32, int) {
		m := marg[0]
		idx := 0
		for i := 1; i < l+1; i++ {
			v := marg[i]
			if v < m {
				m = v
				idx = i
			}
		}
		return m, idx
	}
	mn, sel := argmin(&marg)
	loopN := int32(0)
	for mn < 0.0 {
		d := float32(float32(loopN)*smplStabilizeEps) - mn
		if sel == 0 {
			marg[0] += d
			marg[1] -= d
		} else if sel == l {
			marg[l] += d
			marg[l-1] -= d
		} else {
			marg[sel] += d
			half := float32(d * 0.5)
			marg[sel-1] -= half
			marg[sel+1] -= half
		}
		m, s := argmin(&marg)
		mn = m
		sel = s
		if mn < 0.0 {
			loopN++
			if loopN == smplStabilizeMaxLoops {
				break
			}
		}
	}
	nlsf[0] = minSpacing[0] + marg[0]
	run := nlsf[0]
	for i := 1; i < l; i++ {
		run = run + marg[i] + minSpacing[i]
		nlsf[i] = run
	}
}

// smplNlsf2aMaxOrder is the stack-scratch bound for [smplNlsf2a]: the codec's LPC order is 16 and
// never varies, so this is a headroom constant, not a limit anyone is expected to reach.
const smplNlsf2aMaxOrder = 16

// smplNlsf2a is func 3513 (silk_NLSF2A): order-16 NLSF (radians) -> LPC coefficients a[0..16],
// a[0]=1.0. It allocates the result; the hot paths use [smplNlsf2aInto].
func smplNlsf2a(nlsf []float32) []float32 {
	return smplNlsf2aInto(make([]float32, len(nlsf)+1), nlsf)
}

// smplNlsf2aInto is Rust `smpl_nlsf2a` writing `a` (length len(nlsf)+1) into the caller's buffer
// instead of a fresh Vec, and returning it.
func smplNlsf2aInto(a []float32, nlsf []float32) []float32 {
	order := len(nlsf)
	half := order / 2
	// The LPC order is a fixed 16 and the working polynomials are `order/2 + 1` long, so they fit on
	// the stack. Only the returned `a` needs the heap (the callers, including the `nlsf2a` closure
	// `smpl_lpc_interpol` takes, are typed on `Vec<f32>`). This runs 4x per internal frame via the
	// LSF quantizer and the per-subframe LPC interpolation.
	var cosvArr [smplNlsf2aMaxOrder]float64
	for i := 0; i < min(len(cosvArr), len(nlsf)); i++ {
		cosvArr[i] = dmCos(float64(nlsf[i]))
	}
	cosv := cosvArr[:order]
	var p [smplNlsf2aMaxOrder/2 + 1]float64
	var q [smplNlsf2aMaxOrder/2 + 1]float64
	smplNlsfPoly(p[:], cosv, half, 0)
	smplNlsfPoly(q[:], cosv, half, 1)

	a = a[:order+1]
	a[0] = 1.0
	for k := 0; k < half; k++ {
		pt := p[k+1] + p[k]
		qt := q[k+1] - q[k]
		a[k+1] = float32(0.5 * (pt + qt))
		a[order-k] = float32(0.5 * (pt - qt))
	}
	return a
}

func smplNlsfPoly(out []float64, cosv []float64, half int, parity int) {
	out[0] = 1.0
	out[1] = float64(-2.0 * cosv[parity])
	for k := 1; k < half; k++ {
		c := float64(-2.0 * cosv[2*k+parity])
		out[k+1] = float64(2.0*out[k-1]) + float64(c*out[k])
		n := k
		for n > 1 {
			out[n] += out[n-2] + float64(c*out[n-1])
			n--
		}
		out[1] += c
	}
}

// smplLpcSynthesis is func 3503 (order-16 LPC synthesis): out[n] = ex[n] - sum_{j=1..16}
// a[j]*out[n-j]. `state` holds the previous `order` outputs (carried across subframes/frames),
// updated in place.
func smplLpcSynthesis(ex []float32, a []float32, out []float32, state []float32) {
	order := smplOrder
	for n := 0; n < len(ex); n++ {
		acc := float64(ex[n])
		for j := 1; j <= order; j++ {
			var prev float64
			if n >= j {
				prev = float64(out[n-j])
			} else {
				prev = float64(state[order+n-j])
			}
			acc -= float64(float64(a[j]) * prev)
		}
		out[n] = float32(acc)
	}
	if len(out) >= order {
		copy(state[:order], out[len(out)-order:])
	}
}

// smplGainLin is func 3597 gain: quantized log-gain -> linear gain (fast pow2 bit-cast).
func smplGainLin(gainQ int32) float64 {
	y := float32(float32(gainQ)*6.103515625e-05*0.10000000149011612*27749388.0) + 1064866816.0
	var i int32
	if y < 2147483648.0 && y > -2147483648.0 {
		i = f32ToI32(y)
	} else {
		i = -2147483648
	}
	f := math.Float32frombits(uint32(i)) - 3.1622775509276835e-09
	if f < 0.0 {
		f = 0.0
	}
	return float64(f)
}

func smplFloorF32(x float32) float32 {
	i := f32ToI32(x)
	if float32(i) > x {
		i--
	}
	return float32(i)
}

// synthAbsF32 is the Rust file's local abs_f32 (`if x < 0 { -x } else { x }`), which keeps -0.0
// and NaN as they are, unlike rustmath's bit-clearing absF32.
func synthAbsF32(x float32) float32 {
	if x < 0.0 {
		return -x
	}
	return x
}

// smplFir8 is func 3507: 8-tap symmetric FIR16 application, IN-PLACE over `sig` (the WASM passes
// in==out, so overlapping read/write regions must see prior writes). f32 accumulation order matches
// the WASM.
func smplFir8(sig []float32, inBase int32, outBase int32, cnt int32) {
	for jj := int32(0); jj < cnt; jj++ {
		acc := float32(0)
		for i := int32(0); i < 8; i++ {
			acc += float32((sig[inBase+jj+i] + sig[inBase+jj+15-i]) * smplFir16[i])
		}
		sig[outBase+jj] = acc
	}
}

// smplFracLtp is func 3523 (fractional LTP + interpolation). Reads `sig` backward from `sigEnd`
// and writes two regions per subframe into `out` (len 2*num_subfr*40); also mutates `sig` in place.
func smplFracLtp(
	lag []float32,
	numSubfr int32,
	sig []float32,
	sigEnd int32,
	stateLen int32,
	out []float32,
) {
	lb := sigEnd - (40*numSubfr - stateLen)
	for sf := int32(0); sf < numSubfr; sf++ {
		fl := smplFloorF32(lag[sf])
		intLag := f32ToI32(fl)
		if float32(intLag) == lag[sf] {
			for k := int32(0); k < 40; k++ {
				sig[lb+k] = sig[lb+k-intLag]
			}
			for k := int32(0); k < 40; k++ {
				out[sf*40+k] = sig[lb+k]
				out[(numSubfr+sf)*40+k] =
					sig[lb+k-intLag-1] + sig[lb+k-intLag+1]
			}
		} else {
			b := (numSubfr + sf) * 40
			for k := int32(0); k < 40; k++ {
				out[b+k] =
					sig[lb-intLag-1+k] + sig[lb-intLag+1+k]
			}
			l10 := float32(0)
			for j := int32(0); j < 16; j++ {
				l10 += float32(sig[lb-9-intLag+j] * smplFir16[j])
			}
			smplFir8(sig, lb-intLag-8, lb, 40)
			l11 := float32(0)
			for j := int32(0); j < 16; j++ {
				l11 += float32(sig[lb+32-intLag+j] * smplFir16[j])
			}
			for k := int32(0); k < 40; k++ {
				out[sf*40+k] = sig[lb+k]
			}
			out[b] = l10 + sig[lb+1]
			for k := int32(0); k < 38; k++ {
				out[b+1+k] = sig[lb+k] + sig[lb+2+k]
			}
			out[b+39] = l11 + sig[lb+38]
		}
		lb += 40
	}
}

// smplExcGainState is the func 3522 (per-subframe LTP gain-apply) state, reset per internal frame.
type smplExcGainState struct {
	s0 float32
	s1 float32
}

// defaultSmplExcGainState is the derived Default: both fields zero.
func defaultSmplExcGainState() smplExcGainState { return smplExcGainState{} }

func smplExcGainApply(
	subLen int,
	input []float32,
	st *smplExcGainState,
	out []float32,
	gain float32,
) {
	if gain != 0.0 {
		s5 := st.s1
		s6 := (s5 + s5) + st.s0
		d := st.s0 - s5
		absD := synthAbsF32(d)
		absS6 := synthAbsF32(s6)
		mn := absD + gain
		if absS6 < mn {
			mn = absS6
		}
		t := d * mn / (absD + 1e-12)
		st.s1 = (s6 - t) / 3.0
		st.s0 = (float32(2.0*t) + s6) / 3.0
	}
	if subLen == 0 {
		return
	}
	s0 := st.s0
	for n := 0; n < subLen; n++ {
		out[n] = float32(s0 * input[n])
	}
	s1 := st.s1
	for n := 0; n < subLen; n++ {
		out[n] += float32(s1 * input[subLen+n])
	}
}

// smplLtpFracGain is func 3597 offset 4342: fractional-LTP gain = normGain*-0.17 + 0.35.
func smplLtpFracGain(normGain float64) float32 {
	return float32(float32(normGain)*-0.16999998688697815) + 0.3499999940395355
}

// smplLtpSubframePred is one 80-sample subframe's fractional-LTP prediction (func 3523 + func 3522).
func smplLtpSubframePred(
	hist []float32,
	histPos int32,
	lagF float32,
	gainFrac float32,
	gst *smplExcGainState,
	predOut []float32,
) {
	var fracOut [2 * 2 * 40]float32
	lags := [2]float32{lagF, lagF}
	smplFracLtp(
		lags[:],
		2,
		hist,
		histPos-648,
		smplFracStateLen,
		fracOut[:],
	)
	smplExcGainApply(smplSubfrLen, fracOut[:], gst, predOut, gainFrac)
}

// smplPitchSynth holds the LTP parameters the synthesis needs for one internal frame.
type smplPitchSynth struct {
	voiced   bool
	lagSubfr [4]float64 // per-subframe func-3523 lag = intLagQ6[sf]*0.5 + 32
	normGain float64
}

// smplFrameSynth is the cross-internal-frame synthesis state (LPC + LTP/excitation history + the
// post-LPC band tail).
type smplFrameSynth struct {
	lpcState [smplOrder]float32
	ltpHist  []float32
	gst      smplExcGainState
	// Go-only: the signal buffer [synthInternalFrameInto] returns (Rust allocates a fresh Vec per
	// frame). Not part of the codec state; `reset` leaves it alone.
	out [smplIntfLen]float32
}

func defaultSmplFrameSynth() smplFrameSynth {
	return smplFrameSynth{
		lpcState: [smplOrder]float32{},
		ltpHist:  make([]float32, ltpHistLen),
		gst:      defaultSmplExcGainState(),
	}
}

// reset puts `s` back to [defaultSmplFrameSynth] in place, reusing its LTP history buffer (Go-only:
// the encoder analysis builds a fresh default shadow synth per packet).
func (s *smplFrameSynth) reset() {
	s.lpcState = [smplOrder]float32{}
	if len(s.ltpHist) != ltpHistLen {
		s.ltpHist = make([]float32, ltpHistLen)
	}
	clear(s.ltpHist)
	s.gst = defaultSmplExcGainState()
}

// synthInternalFrame turns one 20 ms internal frame's decoded parameters into 320 LB PCM samples
// (float, ~int16-scaled). Returns (signal, nlsf); `nlsf` becomes the next frame's `prev_nlsf`.
// It allocates the NLSF; the encoder uses [synthInternalFrameInto].
func synthInternalFrame(
	t *smplSynthTables,
	st *smplFrameSynth,
	stage1 int,
	config int,
	grid int,
	stage2 *[16]int32,
	prevNlsf []float32,
	pulses []int32,
	gainQ *[4]int32,
	pitch *smplPitchSynth,
) ([]float32, []float32) {
	return synthInternalFrameInto(make([]float32, smplOrder), t, st, stage1, config, grid, stage2, prevNlsf, pulses, gainQ, pitch)
}

// synthInternalFrameInto is Rust `synth_internal_frame` without its per-call Vecs: the NLSF is
// written into `nlsfOut` (length smplOrder, must not alias `prevNlsf`), the excitation and LPC
// coefficients live on the stack, and the returned signal aliases `st.out` (valid until the next
// call on `st`).
func synthInternalFrameInto(
	nlsfOut []float32,
	t *smplSynthTables,
	st *smplFrameSynth,
	stage1 int,
	config int,
	grid int,
	stage2 *[16]int32,
	prevNlsf []float32,
	pulses []int32,
	gainQ *[4]int32,
	pitch *smplPitchSynth,
) ([]float32, []float32) {
	nlsf := smplReconstructNlsfInto(nlsfOut, t, stage1, config, grid, stage2, prevNlsf)
	var aBuf [smplOrder + 1]float32
	a := smplNlsf2aInto(aBuf[:], nlsf)

	subGain := func(sf int) float64 {
		var gq int32
		if sf < len(gainQ) {
			gq = gainQ[sf]
		}
		return smplGainLin(gq) * float64(smplSubfrLen)
	}

	var exBuf [smplIntfLen]float32
	ex := exBuf[:]
	for n := 0; n < smplIntfLen; n++ {
		ex[n] = float32(float64(pulses[n]) * subGain(n/smplSubfrLen))
	}
	hist := st.ltpHist

	if pitch.voiced {
		const gLtp float32 = 0.949999988079071
		gainFrac := smplLtpFracGain(pitch.normGain)
		var predOut [smplSubfrLen]float32
		st.gst = defaultSmplExcGainState()
		for sf := 0; sf < smplSubfrCount; sf++ {
			lagF := float32(pitch.lagSubfr[sf])
			intLag := f32ToI32(lagF)
			if intLag <= 0 {
				from := sf * smplSubfrLen
				to := (sf + 1) * smplSubfrLen
				copy(hist[smplLtpHist+from:smplLtpHist+to], ex[from:to])
				continue
			}
			exBase := sf * smplSubfrLen
			histPos := int32(smplLtpHist + exBase)
			if intLag > 0 && int(intLag) < smplSubfrLen {
				for n := int(intLag); n < smplSubfrLen; n++ {
					ex[exBase+n] += float32(gLtp * ex[exBase+n-int(intLag)])
				}
			}
			smplLtpSubframePred(hist, histPos, lagF, gainFrac, &st.gst, predOut[:])
			for n := 0; n < smplSubfrLen; n++ {
				ex[exBase+n] += predOut[n]
			}
			copy(hist[int(histPos):int(histPos)+smplSubfrLen], ex[exBase:exBase+smplSubfrLen])
		}
	} else {
		copy(hist[smplLtpHist:smplLtpHist+smplIntfLen], ex)
	}

	out := st.out[:]
	smplLpcSynthesis(ex, a, out, st.lpcState[:])

	// roll the LTP history forward by one internal frame; clear the forward margin.
	copy(hist[0:], hist[smplIntfLen:smplLtpHist+smplIntfLen])
	for i := smplLtpHist + smplIntfLen; i < min(len(hist), ltpHistLen); i++ {
		hist[i] = 0.0
	}
	return out, nlsf
}

// smplDecoderState is the cross-frame decoder state (the persistent LSF/pitch predictor, prev NLSF,
// CELP synthesis).
type smplDecoderState struct {
	lstate   smplLsfState
	prevNlsf []float32
	// C-float-domain CELP synthesis state (excitation + ACB + gen_noise + LPC synth).
	celp celpDecState
	// Per-packet harmonic postfilter state (runs once per packet after all internal frames).
	harm harmPostfilterState
}

// defaultSmplDecoderState is the derived Default, field by field: an empty prev NLSF and each
// field type's own Default (CelpDecState's and HarmPostfilterState's are non-zero).
func defaultSmplDecoderState() smplDecoderState {
	return smplDecoderState{
		lstate:   defaultSmplLsfState(),
		prevNlsf: nil,
		celp:     defaultCelpDecState(),
		harm:     defaultHarmPostfilterState(),
	}
}

// smplDecodeRollback is the cross-frame state one packet's internal-frame loop advances,
// snapshotted so a malformed body can be undone instead of seeding the next packet with parameters
// invented past its end.
//
// `harm` is deliberately not here. The harmonic postfilter runs once per packet AFTER the endpoint
// check, so a concealed packet cannot have advanced it — and its comb state is 9 KB, larger than
// everything in this struct put together. Carrying it would be 9 KB of `memcpy` per packet to
// restore a value that provably did not change.
//
// Owned by the decoder and reused across packets, because `#[derive(Clone)]` generates only
// `clone` and the trait's default `clone_from` is `*self = source.clone()`: that is why the fields
// below are copied through hand-written `clone_from`/`copy_state_from` impls rather than assigned.
// The snapshot is written on every packet and read only on a malformed one, so it must neither
// allocate nor copy more than it has to.
type smplDecodeRollback struct {
	lstate   smplLsfState
	prevNlsf []float32
	celp     celpDecState
}

// defaultSmplDecodeRollback sizes the NLSF vector up front for the same reason the snapshot is
// owned rather than taken fresh: its length is the LPC order for every packet, so a decoder whose
// first packet grows this buffer would pay an allocation on the decode path for a size known here.
func defaultSmplDecodeRollback() smplDecodeRollback {
	return smplDecodeRollback{
		lstate:   defaultSmplLsfState(),
		prevNlsf: make([]float32, 0, smplOrder),
		celp:     defaultCelpDecState(),
	}
}

// save takes the pre-decode copy, into the buffers this snapshot already owns.
func (r *smplDecodeRollback) save(state *smplDecoderState) {
	r.lstate = state.lstate
	r.prevNlsf = append(r.prevNlsf[:0], state.prevNlsf...)
	r.celp.cloneFrom(&state.celp)
}

// restore puts the saved state back, undoing what the internal-frame loop advanced.
func (r *smplDecodeRollback) restore(state *smplDecoderState) {
	state.lstate = r.lstate
	state.prevNlsf = append(state.prevNlsf[:0], r.prevNlsf...)
	state.celp.cloneFrom(&r.celp)
}
