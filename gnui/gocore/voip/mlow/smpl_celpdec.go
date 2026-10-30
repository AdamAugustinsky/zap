// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_celpdec.rs, MIT.
//
// Decoder-side CELP synthesis in the codec's native float domain: the per-subframe loop
// (excitation, CELP/ACB decode, gen_noise, LPC synthesis) plus the LSF interpolation and the FCB
// gain tables. Output is float in [-1, 1].
//
// Self-contained on purpose (mirrors `smpl_celp.rs`): the leaf helpers are local so the decoder
// path is decoupled from the encoder.
//
// SMPL_LPC_ORDER, SMPL_SUBFR_LEN (80: 5 ms at 16 kHz, num_subframes==4), SMPL_LAG_SUBFRLEN,
// SMPL_LTP_INTERPOL_DELAY, SMPL_MAX_PITCH_LAG, SMPL_ACBG_M, SMPL_PITCH_SHARPENING_COEF,
// SMPL_FCBG_V_N, SMPL_UV_GAIN_IDX_LEN, the gain dB constants and the 16-tap symmetric LTP
// interpolation kernel SMPL_INTERPOL_KERNEL are shared.go's.

package mlow

import "sync"

const (
	smplNumSubfr = 4
	smplFrameLen = 320 // 20 ms
)

// smplDecAcbHighBoost holds the ACB high-boost endpoints.
var smplDecAcbHighBoost = [2]float32{0.35, 0.18}

// smplLsfInterpol4 holds the LSF->LPC interpolation factors per subframe, `[lsf_interpol_idx][sf]`.
var smplLsfInterpol4 = [2][4]float32{{0.55, 0.88, 1.0, 1.0}, {0.3, 0.65, 0.95, 1.0}}

// acbgainsCbHr / acbgainsCbLr are the per-subframe ACB-gain codebooks (Q14), high-rate and
// low-rate. Only the high-rate table is exercised by this capture (low_rate==0).
func acbgainsCbHr() []int16 {
	return cbAcbgainsHrQ14()
}

func acbgainsCbLr() []int16 {
	return cbAcbgainsLrQ14()
}

type fcbGainsT struct {
	uv [smplUvGainIdxLen + 1]float32
	v  [smplFcbgVN]float32
}

// fcbGainsOnce backs fcbGains (the Rust OnceLock).
var fcbGainsOnce = sync.OnceValue(func() *fcbGainsT {
	t := &fcbGainsT{}
	for ix := 0; ix <= smplUvGainIdxLen; ix++ {
		t.uv[ix] = powF32(10.0, 0.05*(float32(float32(ix)*smplUvGainStepDb)+smplUvGainMinDb))
	}
	for ix := 0; ix < smplFcbgVN; ix++ {
		t.v[ix] = powF32(10.0, 0.05*(float32(float32(ix)*smplVGainStepDb)+smplVGainMinDb))
	}
	return t
})

func fcbGains() *fcbGainsT {
	return fcbGainsOnce()
}

func celpdecSmplDotProd(a []float32, b []float32, l int) float32 {
	r := float32(0.0)
	for i := 0; i < l; i++ {
		r += float32(a[i] * b[i])
	}
	return r
}

// nlsf2a converts order-16 NLSF (radians) -> LPC `a[0..16]`, a[0]=1. Rust collects the cosines
// into a Vec per call; here it is a fixed [SMPL_LPC_ORDER] array (nlsf is always order 16).
func nlsf2a(nlsf []float32) [smplLpcOrder + 1]float32 {
	order := smplLpcOrder
	half := order / 2
	var cosv [smplLpcOrder]float64
	for i, x := range nlsf {
		cosv[i] = dmCos(float64(x))
	}
	var p [smplLpcOrder/2 + 1]float64
	var q [smplLpcOrder/2 + 1]float64
	nlsfPoly(p[:], cosv[:], half, 0)
	nlsfPoly(q[:], cosv[:], half, 1)
	var a [smplLpcOrder + 1]float32
	a[0] = 1.0
	for k := 0; k < half; k++ {
		pt := p[k+1] + p[k]
		qt := q[k+1] - q[k]
		a[k+1] = float32(0.5 * (pt + qt))
		a[order-k] = float32(0.5 * (pt - qt))
	}
	return a
}

func nlsfPoly(out []float64, cosv []float64, half int, parity int) {
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

// lpcInterpol is the per-subframe interpolation of the LSF between `prev_lsf` and `lsf`, then
// NLSF->A. Returns the per-subframe A coefficients and writes the per-subframe interpolated LSF.
// Mutates `prev_lsf` to the last interpolated LSF (codec carries it across frames).
func lpcInterpol(
	lsf []float32,
	prevLsf *[smplLpcOrder]float32,
	interpol *[4]float32,
	aOut *[smplNumSubfr][smplLpcOrder + 1]float32,
	lsfsOut *[smplNumSubfr][smplLpcOrder]float32,
) {
	if prevLsf[smplLpcOrder-1] == 0.0 {
		copy(prevLsf[:smplLpcOrder], lsf[:smplLpcOrder])
	}
	var ilsf [smplLpcOrder]float32
	prevFactor := float32(-1.0)
	for j := 0; j < smplNumSubfr; j++ {
		if interpol[j] == prevFactor {
			aOut[j] = aOut[j-1]
		} else {
			if interpol[j] == 1.0 {
				copy(ilsf[:], lsf[:smplLpcOrder])
			} else {
				for k := 0; k < smplLpcOrder; k++ {
					ilsf[k] = float32(prevLsf[k]*(1.0-interpol[j])) + float32(lsf[k]*interpol[j])
				}
			}
			aOut[j] = nlsf2a(ilsf[:])
		}
		prevFactor = interpol[j]
		lsfsOut[j] = ilsf
	}
	*prevLsf = ilsf
}

func celpdecAcbDequant(lowRate bool, acbIdx int32, acbG *[smplAcbgM]float32) {
	var cb []int16
	if lowRate {
		cb = acbgainsCbLr()
	} else {
		cb = acbgainsCbHr()
	}
	sc := float32(1.0) / float32(int32(1)<<14)
	for m := 0; m < smplAcbgM; m++ {
		acbG[m] = float32(cb[int(acbIdx)*smplAcbgM+m]) * sc
	}
}

// celpdecAcbSynthesize adjusts the ACB gains, then 3-tap symmetric ACB synthesis with high-boost
// applied.
func celpdecAcbSynthesize(
	fcbSubfrlen int,
	acbBasis []float32,
	acbGIn *[smplAcbgM]float32,
	highBoost float32,
	acb []float32,
) {
	acbG := *acbGIn
	if highBoost != 0.0 {
		f0 := acbG[0] + float32(2.0*acbG[1])
		f1 := acbG[0] - acbG[1]
		absF2new := minF32(absF32(f1)+highBoost, absF32(f0))
		f1 = float32(f1 * (absF2new / (absF32(f1) + 1e-12)))
		acbG[0] = (f0 + float32(2.0*f1)) / 3.0
		acbG[1] = (f0 - f1) / 3.0
	}
	for i := 0; i < fcbSubfrlen; i++ {
		acb[i] = float32(acbG[0] * acbBasis[i])
	}
	for i := 0; i < fcbSubfrlen; i++ {
		acb[i] += float32(acbG[1] * acbBasis[fcbSubfrlen+i])
	}
}

func pitchSharp(x []float32, lag int, l int) {
	for i := lag; i < l; i++ {
		x[i] += float32(x[i-lag] * smplPitchSharpeningCoef)
	}
}

// synLtpBasis builds the ACB basis from the excitation history; mutates `state` forward. `state` is
// the full ACB state; the logical start is `state[state_len - n_lags*40]`.
func synLtpBasis(lags []float32, nLags int, state []float32, stateLen int, acbBasis []float32) {
	p := stateLen - nLags*smplLagSubfrlen
	for subfr := 0; subfr < nLags; subfr++ {
		iLag := f32ToI32(floorF32(lags[subfr]))
		if float32(iLag) == lags[subfr] {
			il := int(iLag)
			for i := 0; i < smplLagSubfrlen; i++ {
				state[p+i] = state[(p+i)-il]
			}
			for i := 0; i < smplLagSubfrlen; i++ {
				acbBasis[subfr*smplLagSubfrlen+i] = state[p+i]
			}
			for i := 0; i < smplLagSubfrlen; i++ {
				a := state[(p+i)-il-1]
				b := state[(p+i)-il+1]
				acbBasis[(nLags+subfr)*smplLagSubfrlen+i] = a + b
			}
		} else {
			il := iLag
			baseFirst := int32(p) + (-1 - il - int32(smplLtpInterpolDelay))
			first := celpdecSmplDotProd(state[int(baseFirst):], smplInterpolKernel[:], 2*smplLtpInterpolDelay)
			{
				srcBase := int32(p) + (-il - int32(smplLtpInterpolDelay))
				for nn := 0; nn < smplLagSubfrlen; nn++ {
					ret := float32(0.0)
					for i := 0; i < 8; i++ {
						s0 := state[int(srcBase+int32(nn)+int32(i))]
						s1 := state[int(srcBase+int32(nn)+15-int32(i))]
						ret += float32((s0 + s1) * smplInterpolKernel[i])
					}
					state[p+nn] = ret
				}
			}
			// The index runs -1 -> 0 -> +SMPL_LAG_SUBFRLEN here, so the tap base for the last sample
			// is p + (SMPL_LAG_SUBFRLEN - i_lag - delay), not -1 of that.
			baseLast := int32(p) + (int32(smplLagSubfrlen) - il - int32(smplLtpInterpolDelay))
			last := celpdecSmplDotProd(state[int(baseLast):], smplInterpolKernel[:], 2*smplLtpInterpolDelay)
			for i := 0; i < smplLagSubfrlen; i++ {
				acbBasis[subfr*smplLagSubfrlen+i] = state[p+i]
			}
			b1 := (nLags + subfr) * smplLagSubfrlen
			acbBasis[b1] = first + state[p+1]
			for i := 0; i < smplLagSubfrlen-2; i++ {
				acbBasis[b1+1+i] = state[p+i] + state[p+i+2]
			}
			iLast := smplLagSubfrlen - 1
			acbBasis[b1+iLast] = state[p+iLast-1] + last
		}
		p += smplLagSubfrlen
	}
}

// celpDecode is the voiced branch: add the ACB (LTP) contribution into `lpc_res`, then push the
// subframe into the ACB state. `acb_state_len = subfrlen + 2*MAX_PITCH_LAG + LTP_INTERPOL_DELAY`.
//
// Rust allocates `acb_basis` and `acb` per voiced subframe (`vec!`); here they are zeroed stack
// arrays bounded by SMPL_MAX_SF_LEN (the decoder always passes SMPL_SUBFR_LEN = 80).
func celpDecode(
	acbState []float32,
	acbStateLen int,
	voiced bool,
	acbGainIdx int32,
	lags []float32,
	numLags int,
	subfrlen int,
	lowRate bool,
	normalizedBitrate float32,
	lpcRes []float32,
) {
	if voiced {
		highBoost := smplDecAcbHighBoost[0] +
			float32((smplDecAcbHighBoost[1]-smplDecAcbHighBoost[0])*normalizedBitrate)
		iLag := f32ToI32(lags[numLags-1])
		if lowRate {
			pitchSharp(lpcRes, int(iLag), subfrlen)
		}
		var acbBasisBuf [smplMaxSfLen * smplAcbgM]float32
		var acbBuf [smplMaxSfLen]float32
		acbBasis := acbBasisBuf[:subfrlen*smplAcbgM]
		acb := acbBuf[:subfrlen]
		synLtpBasis(lags, numLags, acbState, acbStateLen, acbBasis)
		var acbGain [smplAcbgM]float32
		celpdecAcbDequant(lowRate, acbGainIdx, &acbGain)
		celpdecAcbSynthesize(subfrlen, acbBasis, &acbGain, highBoost, acb)
		for i := 0; i < subfrlen; i++ {
			lpcRes[i] += acb[i]
		}
	}
	// Update ACB state: shift left by subfrlen, append this subframe's excitation.
	copy(acbState[0:], acbState[subfrlen:acbStateLen-subfrlen])
	copy(acbState[acbStateLen-2*subfrlen:acbStateLen-subfrlen], lpcRes[:subfrlen])
}

// filtAr16 is AR(16) over one subframe: `y[n] = x[n] - sum_{i} coef[16-i]*y[n-16+i]`. `ybuf` holds
// a 16-sample history prefix at `ybuf[base-16 .. base]`, so synthesis flows contiguously across the
// frame (cross-subframe and cross-frame history is the same buffer).
func filtAr16(x []float32, a *[smplLpcOrder + 1]float32, ybuf []float32, base int, n int) {
	for nn := 0; nn < n; nn++ {
		res := x[nn]
		for i := 0; i < smplLpcOrder; i++ {
			res -= float32(a[smplLpcOrder-i] * ybuf[base+nn-smplLpcOrder+i])
		}
		ybuf[base+nn] = res
	}
}

// celpDecParams holds the per-subframe decoded params the synthesis consumes.
type celpDecParams struct {
	voiced       bool
	sfPulses     [smplNumSubfr]int32
	fcbgIdx      [smplNumSubfr]int32
	nrgresDbqQ14 [smplNumSubfr]int32
	// acbgIdx is the ACB gain index per subframe (voiced only).
	acbgIdx [smplNumSubfr]int32
	// blockLags is the per-40-block pitch lag (codec units: float), 8 per frame, 0 for unvoiced.
	// Synthesis hands the two blocks of subframe `sf` (`block_lags[2*sf]`, `block_lags[2*sf+1]`)
	// to the ACB/LTP basis, so fractional intra-subframe lag changes are preserved
	// (lags_per_subframe == 2).
	blockLags   [2 * smplNumSubfr]float32
	totalPulses int32
}

// celpDecState is the persistent decoder synthesis state (float domain).
type celpDecState struct {
	noise       noiseGenerator
	acbState    []float32
	acbStateLen int
	lpcSynthMem [smplLpcOrder]float32
	lsfPrev     [smplLpcOrder]float32
	prevNrgres  float32
	// hp is the post-LPC HP (pitch-harmonic) postfilter state, persistent across the stream.
	hp hpPostfilterState
	// dbgExcPre is the test-only capture of the per-subframe pre-noise excitation (`exc_pre`),
	// 80/subframe. Rust compiles the field only under cfg(test); here it is always present but
	// nil by default, and synthFrame appends to it only when a test has made it non-nil.
	dbgExcPre []float32
}

// clone is hand-written so the test-only excitation trace is NOT carried along. It accumulates
// every synthesized subframe for the whole stream and is never cleared, so copying it in the
// per-packet rollback snapshot would make decoding quadratic in stream length. It is a diagnostic
// capture, not codec state that concealment has to restore.
func (c *celpDecState) clone() celpDecState {
	return celpDecState{
		noise:       c.noise,
		acbState:    append([]float32(nil), c.acbState...),
		acbStateLen: c.acbStateLen,
		lpcSynthMem: c.lpcSynthMem,
		lsfPrev:     c.lsfPrev,
		prevNrgres:  c.prevNrgres,
		hp:          c.hp,
		dbgExcPre:   nil,
	}
}

// cloneFrom reuses the destination's buffers instead of replacing them.
//
// Written out because a hand-rolled `Clone` gets the trait's default `clone_from`, which is
// `*self = source.clone()` and therefore allocates every buffer afresh on each call. The
// decoder does exactly one of these per packet for the concealment snapshot, so the default
// turns a `memcpy` into a round of allocate-and-drop roughly seventeen times a second, on a
// heap an ESP32 target has to keep unfragmented.
func (c *celpDecState) cloneFrom(source *celpDecState) {
	c.noise = source.noise
	c.acbState = append(c.acbState[:0], source.acbState...)
	c.acbStateLen = source.acbStateLen
	c.lpcSynthMem = source.lpcSynthMem
	c.lsfPrev = source.lsfPrev
	c.prevNrgres = source.prevNrgres
	c.hp.copyStateFrom(&source.hp)
	// `dbg_exc_pre` is deliberately not carried, for the reason above the `clone` impl.
}

func defaultCelpDecState() celpDecState {
	acbStateLen := smplSubfrLen + 2*smplMaxPitchLag + smplLtpInterpolDelay
	return celpDecState{
		noise:       defaultNoiseGenerator(),
		acbState:    make([]float32, acbStateLen),
		acbStateLen: acbStateLen,
		hp:          defaultHpPostfilterState(),
	}
}

// synthFrame synthesizes one 20 ms internal frame (4 subframes) into 320 float samples in [-1, 1]
// via the subframe loop. `nlsf` is the reconstructed order-16 NLSF (radians); `pulses` are the
// signed FCB pulse magnitudes (320 positions). `low_rate` is the TOC bit.
func (c *celpDecState) synthFrame(
	nlsf []float32,
	lsfInterpolIdx int,
	pulses []int32,
	params *celpDecParams,
	lowRate bool,
	frameLength16 int32,
	out []float32,
) {
	gains := fcbGains()
	// Per-subframe LPC interpolation.
	var a [smplNumSubfr][smplLpcOrder + 1]float32
	var lsfs [smplNumSubfr][smplLpcOrder]float32
	interpol := &smplLsfInterpol4[min(lsfInterpolIdx, 1)]
	lpcInterpol(nlsf, &c.lsfPrev, interpol, &a, &lsfs)

	normalizedBitrate := smplGetNormalizedBitrate(params.totalPulses, frameLength16)

	// Excitation: sparse FCB pulses scaled by the per-subframe FCB gain.
	var lpcRes [smplFrameLen]float32
	var gainTab []float32
	if params.voiced {
		gainTab = gains.v[:]
	} else {
		gainTab = gains.uv[:]
	}
	for pos := 0; pos < smplFrameLen; pos++ {
		if pulses[pos] != 0 {
			sf := pos / smplSubfrLen
			lpcRes[pos] = float32(float32(pulses[pos]) * gainTab[int(params.fcbgIdx[sf])])
		}
	}

	lagsPerSubfr := 2 // 80-sample subframe / 40-sample lag subframe
	// Contiguous synthesis buffer: 16-sample history prefix + 320 frame samples.
	var ybuf [smplLpcOrder + smplFrameLen]float32
	copy(ybuf[:smplLpcOrder], c.lpcSynthMem[:])
	for sf := 0; sf < smplNumSubfr; sf++ {
		base := sf * smplSubfrLen
		// CELP (ACB/LTP) decode: adds the voiced adaptive-codebook contribution + updates state.
		// With lags_per_subframe == 2 the two 40-blocks of this subframe carry independent lags.
		sfLags := [2]float32{params.blockLags[2*sf], params.blockLags[2*sf+1]}
		celpDecode(
			c.acbState,
			c.acbStateLen,
			params.voiced,
			params.acbgIdx[sf],
			sfLags[:],
			lagsPerSubfr,
			smplSubfrLen,
			lowRate,
			normalizedBitrate,
			lpcRes[base:base+smplSubfrLen],
		)

		if c.dbgExcPre != nil {
			c.dbgExcPre = append(c.dbgExcPre, lpcRes[base:base+smplSubfrLen]...)
		}

		// Residual noise energy + shaped noise (the dominant unvoiced fix).
		nrgres := smplDecodeResnrg(params.nrgresDbqQ14[sf], int32(smplSubfrLen))
		if !params.voiced {
			c.prevNrgres = nrgres
		}

		var noise [160]float32
		smplCelpGenNoise(
			&c.noise,
			lpcRes[base:base+smplSubfrLen],
			smplSubfrLen,
			params.voiced,
			params.sfPulses[sf],
			nrgres,
			params.fcbgIdx[sf],
			lsfs[sf][:],
			normalizedBitrate,
			gains.uv[:],
			noise[:],
		)
		for i := 0; i < smplSubfrLen; i++ {
			lpcRes[base+i] += noise[i]
		}

		// LPC synthesis (contiguous history across subframes/frames).
		filtAr16(lpcRes[base:base+smplSubfrLen], &a[sf], ybuf[:], smplLpcOrder+base, smplSubfrLen)
	}
	copy(out[:smplFrameLen], ybuf[smplLpcOrder:])
	copy(c.lpcSynthMem[:], ybuf[smplLpcOrder+smplFrameLen-smplLpcOrder:])

	// Post-LPC HP (pitch-harmonic) postfilter (LPC postfilter is off on this stream, tilt
	// postfilter is low_rate-only). The comb lag is the energy-weighted mean of the 8 per-40-block
	// lags (0 -> the default fixed-corner curve, unvoiced).
	var lag float32
	if params.voiced {
		sl, sll := float32(0), float32(0)
		for _, l := range params.blockLags {
			sl += l
			sll += float32(l * l)
		}
		if sl > 0.0 {
			lag = sll / sl
		} else {
			lag = 0.0
		}
	} else {
		lag = 0.0
	}
	var hpOut [smplFrameLen]float32
	smplHpPostfilter(&c.hp, out[:smplFrameLen], smplFrameLen, lag, hpOut[:])
	copy(out[:smplFrameLen], hpOut[:])
}
