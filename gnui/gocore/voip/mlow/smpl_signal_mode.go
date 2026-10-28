// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_signal_mode.rs, MIT.
//
// Voiced/unvoiced classifier and the spectral-harmonicity measure (`spectral_harmonicity`) it shares
// with the pitch estimator. The classifier folds five strengths (pitch correlation, VAD, spectral
// tilt, harmonicity, lag) plus a per-stream hysteresis into a single `voicing_strength`; the encoder
// codes a frame voiced when that is positive and the packet is coded-as-active-voice.

package mlow

// smplVuvWeights are the weights on corrs, vad, tilt, harmonicity, short lags. The reference
// declares 6 entries but only sums the first 5, so this holds 5.
var smplVuvWeights = [5]float32{1.0, 0.5, 0.5, 0.7, 0.3}

// J#10736 and the live 330-frame trace use f32 bits 0xbe051eb8.
const smplVuvBias float32 = -0.13
const smplVuvHyst float32 = 0.05

// transitionIx is `SMPL_F_LEN / 3` (the transition index between the low/high spectral-tilt bands).
const transitionIx = smplFLen / 3
const harmonicityUndef float32 = -10000.0

func signalModeSmplSigmoid(x float32) float32 {
	if x > 80.0 {
		return 1.0
	}
	if x < -80.0 {
		return 0.0
	}
	return 1.0 / (1.0 + expF32(-x))
}

func smplInvSigmoid(x float32) float32 {
	return -lnF32((1.0 / x) - 1.0)
}

func signalModeSmplDotProd(a, b []float32, l int) float32 {
	s := float32(0.0)
	for i := 0; i < l; i++ {
		s += float32(a[i] * b[i])
	}
	return s
}

func smplSumVec(x []float32, l int) float32 {
	s := float32(0.0)
	for _, v := range x[:min(l, len(x))] {
		s += v
	}
	return s
}

// vuvMode is the per-stream voicing hysteresis + spectral-tilt background tracker. The encoder
// threads one instance across the whole stream.
type vuvMode struct {
	nrgLoBgn    float32
	nrgHiBgn    float32
	voicingPrev float32
	lastLagPrev float32
}

// defaultVuvMode: all fields start at 0.
func defaultVuvMode() vuvMode {
	return vuvMode{}
}

// spectralHarmonicity is the harmonic peak/valley energy ratio at low frequencies, from the per-bin
// weighted power spectrum `f2w` (= `F2[i] * (i+3)` with `f2w[0,1]=0`). `cache` is a per-call
// harmonicity memo keyed by harmonic bin; `reset` clears it.
func spectralHarmonicity(avgLag float32, f2w []float32, cache []float32, reset bool) float32 {
	if reset {
		for i := range cache {
			cache[i] = harmonicityUndef
		}
	}
	invF2StepHz := float32(2.0) * float32(smplFLen-1) / 16000.0
	harmHz := 16000.0 / avgLag
	harmIx := f32ToI32(roundF32(harmHz * 2.0 * invF2StepHz))
	cacheLen := int32(len(cache))
	if harmIx >= cacheLen {
		// The reference asserts this never happens; guard defensively and fall through to recompute.
		return recomputeHarmonicity(harmHz, invF2StepHz, f2w)
	}
	if cache[harmIx] > harmonicityUndef {
		return cache[harmIx]
	}
	hs := recomputeHarmonicity(harmHz, invF2StepHz, f2w)
	cache[harmIx] = hs
	return hs
}

const numHarms = 4

func recomputeHarmonicity(harmHz, invF2StepHz float32, f2w []float32) float32 {
	harmWidth := float32(harmHz * invF2StepHz)
	harmStrength := float32(0.1)
	if harmWidth > 1.97 {
		var peakValleyMags [2*numHarms + 1]float32
		for numHarm := range peakValleyMags {
			ixStart := float32(0.5 * float32(numHarm) * harmWidth)
			ixEnd := ixStart + harmWidth
			idxStart := f32ToI32(ceilF32(ixStart))
			idxEnd := f32ToI32(floorF32(ixEnd))
			weightsLen := int(max(idxEnd-idxStart+1, 0))
			var weights [20]float32
			invHarmWidth := 1.0 / harmWidth
			for i := 0; i < min(weightsLen, len(weights)); i++ {
				tmp := float32((float32(idxStart) - ixStart + float32(i)) * invHarmWidth)
				tmp -= float32(tmp * tmp)
				weights[i] = tmp * tmp
			}
			base := min(int(max(idxStart, 0)), len(f2w))
			// The reference assumes the harmonic window stays within F2w; clamp defensively so a
			// degenerate (too-short) lag can't read past the spectrum.
			avail := min(len(f2w)-base, weightsLen)
			peakValleyNrg := signalModeSmplDotProd(f2w[base:], weights[:], avail) / smplSumVec(weights[:], weightsLen)
			peakValleyMags[numHarm] = sqrtF32(peakValleyNrg + 1e-30)
		}
		var magRatiosLog [numHarms]float32
		var magWeights [numHarms]float32
		magPeakWeights := [3]float32{1.0, 10.0, 1.0}
		magValleyWeights := [3]float32{5.0, 2.0, 5.0}
		for numHarm := 0; numHarm < numHarms; numHarm++ {
			magPeak := float32(magPeakWeights[0]*peakValleyMags[2*numHarm]) +
				float32(magPeakWeights[1]*peakValleyMags[2*numHarm+1]) +
				float32(magPeakWeights[2]*peakValleyMags[2*numHarm+2])
			magValley := float32(magValleyWeights[0]*peakValleyMags[2*numHarm]) +
				float32(magValleyWeights[1]*peakValleyMags[2*numHarm+1]) +
				float32(magValleyWeights[2]*peakValleyMags[2*numHarm+2])
			magRatiosLog[numHarm] = lnF32(magPeak / magValley)
			magWeights[numHarm] = sqrtF32(magPeak + magValley + 1e-30)
		}
		harmStrength = signalModeSmplDotProd(magWeights[:], magRatiosLog[:], numHarms) /
			smplSumVec(magWeights[:], numHarms)
	}
	return harmStrength
}

// buildF2w builds `f2w` (`F2[i] * (i+3)`, with `f2w[0]=f2w[1]=0`) consumed by `spectral_harmonicity`.
func buildF2w(f2 *[smplFLen]float32) [smplFLen]float32 {
	var f2w [smplFLen]float32
	for i := 2; i < smplFLen; i++ {
		f2w[i] = f2[i] * float32(i+3)
	}
	return f2w
}

// harmStrengthAt is the harmonicity at `avg_lag` (computed right after the pitch search, fresh
// cache). Reused by the pitch estimator so its `harm_strength` matches the value fed to
// `smpl_get_signal_mode`.
func harmStrengthAt(avgLag float32, f2w *[smplFLen]float32) float32 {
	var cache [50]float32
	return spectralHarmonicity(avgLag, f2w[:], cache[:], true)
}

// smplGetSignalMode combines the five voicing strengths + hysteresis into `voicing_strength`.
// `lags` is the per-lag-subframe pitch lag in samples (`framelen / SMPL_LAG_SUBFRLEN` entries); `f2`
// is the power spectrum `F2[0..256]`. Mutates `vuv` (background tilt + hysteresis state).
func smplGetSignalMode(pitchcorr float32, lags []float32, avgLag, harmStrength float32, f2 *[smplFLen]float32, spActProb float32, vuv *vuvMode) float32 {
	return signalModeWithBias(pitchcorr, lags, avgLag, harmStrength, f2, spActProb, vuv, smplVuvBias)
}

// The C auditor is pinned to an older tuning (-0.1038). Keep the same kernel
// testable under that tuning without carrying it into the shipped profile.
func signalModeWithBias(pitchcorr float32, lags []float32, avgLag, harmStrength float32, f2 *[smplFLen]float32, spActProb float32, vuv *vuvMode, bias float32) float32 {
	corrStrength := smplInvSigmoid(0.1 + float32(0.75*clampF32(pitchcorr, 0.0, 1.0))) // -1.4 .. 1.4
	vadStrength := 0.04 * (1.0 - 1.04/(spActProb+0.04))                               // -1 .. 0

	// spectral tilt
	nrgLo := float32(0.0)
	for i := 2; i < transitionIx; i++ {
		tmp := f2[i] * float32(i+3)
		nrgLo += float32(tmp * float32(transitionIx-i))
	}
	nrgHi := float32(0.0)
	for i := transitionIx; i < smplFLen; i++ {
		tmp := f2[i] * float32(i+3)
		nrgHi += float32(tmp * float32(i-transitionIx))
	}
	if vadStrength < -0.1 {
		smthCoef := -0.5 * vadStrength
		vuv.nrgLoBgn += float32(smthCoef * (nrgLo - vuv.nrgLoBgn))
		vuv.nrgHiBgn += float32(smthCoef * (nrgHi - vuv.nrgHiBgn))
	}
	tiltLin := (maxF32(nrgLo-vuv.nrgLoBgn, 0.0) - maxF32(nrgHi-vuv.nrgHiBgn, 0.0)) /
		(nrgLo + nrgHi + 1e-9)
	tiltStrength := tiltLin * tiltLin * tiltLin // cubed to make the measure less binary
	lagStrength := -signalModeSmplSigmoid(0.25 * (38.0 - avgLag))

	voicingStrength := (float32(smplVuvWeights[0]*corrStrength)+
		float32(smplVuvWeights[1]*vadStrength)+
		float32(smplVuvWeights[2]*tiltStrength)+
		float32(smplVuvWeights[3]*harmStrength)+
		float32(smplVuvWeights[4]*lagStrength))/
		smplSumVec(smplVuvWeights[:], 5) +
		bias

	// hysteresis
	if vuv.lastLagPrev > 0.0 {
		tmp := log2F32(lags[0] / vuv.lastLagPrev)
		if tmp > 0.0 {
			tmp *= 0.5
		}
		vuv.voicingPrev /= 0.4 + float32(tmp*tmp)
	}
	voicingStrength += float32(vuv.voicingPrev * smplVuvHyst)
	vuv.voicingPrev = float32(dmTanh(float64(3.0 * voicingStrength)))
	vuv.lastLagPrev = lags[len(lags)-1]

	return voicingStrength
}
