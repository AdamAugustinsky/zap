// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_harm_postfilter.rs, MIT.
//
// MLow harmonic postfilter: the final per-packet pitch-comb that runs on the full LB output after
// the HP postfilter. It enhances pitch harmonics by mixing `x[-lag] + x[+lag]` into the signal,
// low-pass filtered by a lag-dependent kernel, and it introduces the codec's
// `SMPL_TOT_POSTFILT_DELAY = 48`-sample group delay (8 FB + 40 lag-subframe).
//
// The reference is built with `-ffast-math -mavx`, so the recursive/accumulating math is not
// IEEE-strict; this matches it to within i16 output quantization, not bit-for-bit.
//
// FRAME_LEN and MAXPITCH_LEN (both 320) are shared.go's frameLen / maxpitchLen; SMPL_PI
// (std::f32::consts::PI) is smplPi.

package mlow

import "sync"

const (
	maxFramesPerPacket       = 6
	minPitchLag        int32 = 32          // SMPL_MINPITCH_MS(2) * 16
	maxPitchLag        int32 = 320         // SMPL_MAXPITCH_MS(20) * 16
	fbDelay                  = 8           // SMPL_HARM_POSTF_FB_DELAY
	lagSubfrLen              = 40          // SMPL_HARM_POSTF_LAG_SUBFR_LEN = FRAME_LEN / 8
	harmDelay                = lagSubfrLen // SMPL_HARM_POSTF_DELAY
)

const pitchNumSubframes = 8

// harmTuning: J#10726: feedback 0.4 (0x3ecccccd), comb strength 0.713 (0x3f36872b).
type harmTuning struct {
	feedback float32
	strength float32
}

var shippedTuning = harmTuning{
	feedback: 0.4,
	strength: 0.713,
}

const (
	cutoffHz     float32 = 4000.0
	nharmCutoff  float32 = 6.3
	reductionFac float32 = 0.0579
)

const (
	stateCombLen       = maxpitchLen + frameLen*maxFramesPerPacket + harmDelay
	lpFiltRes    int32 = 2500
	numLpFilt          = int((lpFiltRes / 80) - lpFiltRes/maxPitchLag + 1)
)

func lagToFiltIx(lag int32) int {
	return int(lpFiltRes/max(lag+30, 80) - lpFiltRes/maxPitchLag)
}

// harmTablesT is the LP-filter bank, one symmetric `2*FB_DELAY+1`-tap kernel per quantized lag
// bucket.
type harmTablesT struct {
	lpFilters [numLpFilt][2*fbDelay + 1]float32
}

// harmTablesOnce backs harmTables (the Rust OnceLock).
var harmTablesOnce = sync.OnceValue(func() *harmTablesT {
	var filtWin [fbDelay]float32
	dOmega := (0.5 * smplPi) / (float32(fbDelay) + 1.0)
	omega := dOmega
	for i := 0; i < fbDelay; i++ {
		filtWin[i] = cosF32(omega) / (float32(i) + 1.0)
		omega += dOmega
	}
	t := &harmTablesT{}
	ixPrev := int32(-1)
	for lag := minPitchLag; lag <= maxPitchLag; lag++ {
		ix := int32(lagToFiltIx(lag))
		if ix != ixPrev {
			omega0 := 2.0 * smplPi / float32(lag)
			createLpFilter(omega0, &filtWin, &t.lpFilters[ix])
			ixPrev = ix
		}
	}
	return t
})

func harmTables() *harmTablesT {
	return harmTablesOnce()
}

func createLpFilter(omega0 float32, filtWin *[fbDelay]float32, blp *[2*fbDelay + 1]float32) {
	omegaC := minF32(float32(omega0*nharmCutoff), cutoffHz/16000.0*smplPi)
	sumB := float32(0.0)
	omegaCSum := omegaC
	for i := 0; i < fbDelay; i++ {
		b := float32(filtWin[i] * sinF32(omegaCSum))
		omegaCSum += omegaC
		blp[fbDelay+i+1] = b
		blp[fbDelay-i-1] = b
		sumB += float32(2.0 * b)
	}
	blp[fbDelay] = omegaC
	sumB += omegaC
	sc := 1.0 / sumB
	for i := range blp {
		blp[i] = float32(blp[i] * sc)
	}
}

// harmPostfilterState is the persistent harm-postfilter state. `prev_lag = 0` after init (first
// packet's lag).
type harmPostfilterState struct {
	state1        [2 * fbDelay]float32
	lpcoefs       [2*fbDelay + 1]float32
	stateComb     []float32
	prevLag       int32
	prevDidFilter int32
}

func defaultHarmPostfilterState() harmPostfilterState {
	return harmPostfilterState{
		stateComb: make([]float32, stateCombLen),
	}
}

// clone is the derived Clone: Go assignment would share stateComb.
func (h *harmPostfilterState) clone() harmPostfilterState {
	c := *h
	c.stateComb = append([]float32(nil), h.stateComb...)
	return c
}

func harmPostfilterDotProd(a []float32, b []float32, l int) float32 {
	r := float32(0.0)
	for i := 0; i < l; i++ {
		r += float32(a[i] * b[i])
	}
	return r
}

func nrg(x []float32, n int) float32 {
	r := float32(0.0)
	for i := 0; i < n; i++ {
		r += float32(x[i] * x[i])
	}
	return r
}

// filtMa16Sym is the 17-tap symmetric MA. `x` has 16 samples of history at `x[-16..0]`; here it
// reads from a base offset into the shared buffer.
func filtMa16Sym(buf []float32, xBase int, n int, coef *[17]float32, out []float32) {
	for nn := 0; nn < n; nn++ {
		c := xBase + nn
		res := float32(buf[c-8] * coef[8])
		for i := 0; i < 8; i++ {
			res += float32(coef[i] * (buf[c-i] + buf[c-16+i]))
		}
		out[nn] = res
	}
}

// harmPostfilterCore is the core filter for one 40-sample lag block. `comb` is the StateComb buffer;
// `comb_x` is the index of this block's read pointer. `out`/`out_off` is the caller's `y_harm`
// destination; it doubles as scratch and holds the final block output. The filtered result is also
// fed back into `comb[comb_x - FB_DELAY ..]`, which is what makes the comb recursive and is the
// 48-sample-delayed location the next packets read from.
func harmPostfilterCore(
	lpcoefs *[2*fbDelay + 1]float32,
	comb []float32,
	combX int,
	futureSamples int32,
	lag int32,
	diff []float32,
	diffBase int,
	out []float32,
	outOff int,
	l int,
	fbStrength float32,
	prevDidFilter *int32,
	tuning harmTuning,
) {
	tables := harmTables()
	lagU := int(lag)
	xy := float32(0.0)
	// y_harm scratch lives in `out[out_off..out_off+l]`.
	if lag > 0 {
		lookforward := int32(l) + lag - futureSamples
		if lookforward > 0 {
			l2 := int(max(int32(l)-lookforward, 0))
			for i := 0; i < l2; i++ {
				out[outOff+i] = comb[combX+i-lagU] + comb[combX+i+lagU]
			}
			for i := 0; i < l-l2; i++ {
				out[outOff+l2+i] = comb[combX+l2+i-lagU] + comb[combX+l2+i]
			}
		} else {
			for i := 0; i < l; i++ {
				out[outOff+i] = comb[combX+i-lagU] + comb[combX+i+lagU]
			}
		}
		xy = harmPostfilterDotProd(comb[combX:], out[outOff:], l)
	}
	if lag > 0 && xy > 0.0 {
		xx := nrg(comb[combX:], l)
		yy := float32(0.25 * nrg(out[outOff:], l))
		strength := 0.5 * xy / maxF32(yy, xx)
		highLagReduction := 1.0 -
			float32(reductionFac*(float32(lag-minPitchLag)/float32(maxPitchLag-minPitchLag)))
		strength = float32(strength * highLagReduction * tuning.strength)
		for i := 0; i < l; i++ {
			out[outOff+i] = float32(out[outOff+i] * float32(0.5*strength))
		}
		// diff = -strength * x + y_harm
		for i := 0; i < l; i++ {
			diff[diffBase+i] = out[outOff+i] + float32((-strength)*comb[combX+i])
		}
		kernel := &tables.lpFilters[lagToFiltIx(lag)]
		for k := 0; k < 2*fbDelay+1; k++ {
			lpcoefs[k] = float32(kernel[k] * fbStrength)
		}
		coef17 := *lpcoefs
		// y_harm = MA(diff); then y_harm += comb[x - FB_DELAY] (the 48-delayed base signal). The comb
		// is read-only here; only y_harm is modified.
		var yh [lagSubfrLen]float32
		filtMa16Sym(diff, diffBase, l, &coef17, yh[:])
		for i := 0; i < l; i++ {
			out[outOff+i] = yh[i] + comb[combX-fbDelay+i]
		}
		*prevDidFilter = 1
	} else {
		for i := diffBase; i < diffBase+lagSubfrLen; i++ {
			diff[i] = 0.0
		}
		if *prevDidFilter != 0 {
			// zero-input response of the previous filter for the first 2*FB_DELAY samples, added onto
			// the delayed base; the tail is the plain 48-delayed comb.
			coef17 := *lpcoefs
			var yh [2 * fbDelay]float32
			filtMa16Sym(diff, diffBase, 2*fbDelay, &coef17, yh[:])
			for i := 0; i < 2*fbDelay; i++ {
				out[outOff+i] = yh[i] + comb[combX-fbDelay+i]
			}
			for i := 2 * fbDelay; i < l; i++ {
				out[outOff+i] = comb[combX+fbDelay+i-2*fbDelay]
			}
		} else {
			for i := 0; i < l; i++ {
				out[outOff+i] = comb[combX-fbDelay+i]
			}
		}
		*prevDidFilter = 0
	}
}

// smplHarmPostfilter applies the harmonic postfilter to a full packet in place. `x` is
// `packetlen_16` samples; `lags` are the per-40-block lags (`nlags = packetlen/40`),
// `normalized_bitrate` is the packet average.
func smplHarmPostfilter(st *harmPostfilterState, x []float32, xLen int, lags []float32, nLags int, normalizedBitrate float32) {
	harmPostfilterWithTuning(st, x, xLen, lags, nLags, normalizedBitrate, shippedTuning)
}

// diffPrefix: the diff buffer carries 16 samples of history ahead of the block data.
const diffPrefix = 2 * fbDelay

func harmPostfilterWithTuning(
	st *harmPostfilterState,
	x []float32,
	xLen int,
	lags []float32,
	nLags int,
	normalizedBitrate float32,
	tuning harmTuning,
) {
	// Rust debug-asserts x_len == n_lags * LAG_SUBFR_LEN.
	// diff buffer with 16 samples of history prefix: backing is FRAME_LEN + 2*FB_DELAY, diff starts
	// at +2*FB_DELAY. Rust allocates it per call (`vec!`); here it is a zeroed stack array.
	var diff [frameLen + diffPrefix]float32

	lag := st.prevLag
	// StateComb layout: [history | current packet]; current packet starts at MAX_PITCH_LAG + HARM_DELAY.
	combCur := int(maxPitchLag) + harmDelay
	copy(st.stateComb[combCur:combCur+xLen], x[:xLen])

	fbStrength := 1.0 - float32(tuning.feedback*normalizedBitrate)
	offset1 := 0

	lagCtr := 0
	for lagCtr < nLags {
		offset2 := 0
		// diff[-16..0] = state1
		copy(diff[diffPrefix-16:diffPrefix], st.state1[:])
		lagCtrEnd := min(lagCtr+pitchNumSubframes, nLags)
		for lagCtr < lagCtrEnd {
			combX := int(maxPitchLag) + offset1
			futureSamples := int32(harmDelay) + int32(xLen) - int32(offset1)
			harmPostfilterCore(
				&st.lpcoefs,
				st.stateComb,
				combX,
				futureSamples,
				lag,
				diff[:],
				diffPrefix+offset2,
				x,
				offset1,
				lagSubfrLen,
				fbStrength,
				&st.prevDidFilter,
				tuning,
			)
			offset1 += lagSubfrLen
			offset2 += lagSubfrLen
			lag = f32ToI32(roundF32(lags[lagCtr]))
			lagCtr++
		}
		// state1 = diff[offset2-16 .. offset2]
		copy(st.state1[:], diff[diffPrefix+offset2-16:diffPrefix+offset2])
	}

	st.prevLag = lag
	// shift StateComb left by x_len
	copy(st.stateComb[0:], st.stateComb[xLen:xLen+combCur])
}
