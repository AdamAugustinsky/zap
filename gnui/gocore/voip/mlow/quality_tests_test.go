// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/quality_tests.rs, MIT.
//
// Audio-quality regression tests for the MLow encoder and decoder.
//
// The shipped encoder picks voiced (LTP) or unvoiced per internal frame via analysis-by-synthesis,
// so a voiced input is reconstructed with its pitch periodicity, and an aperiodic input stays
// broadband. These tests pin that behaviour in both directions:
//
// OUTBOUND (encode → decode round-trip):
//   - Voiced signals (harmonic-rich, fundamental + harmonics) reconstruct with periodicity intact
//     (autocorrelation peak at the pitch lag well above the noise floor).
//   - Pure-tone signals reconstruct with spectral concentration (Goertzel ratio well above 1).
//   - Silence / near-silence produces no spurious energy.
//   - Unvoiced noise reconstructs as broadband (NOT pitched): the voiced path is not applied to an
//     aperiodic signal.
//
// INBOUND (decoder energy-envelope tracking):
//   - The decoder tracks RMS energy of the synthetic frames in e2e_vectors.json: silence frames stay
//     near zero, active frames produce measurable output, confirming inbound audio is not silent.
//
// The byte-exact wire-format guarantee lives in the decoder round-trip vectors and the golden test,
// not here; these tests guard perceptual/structural properties of the audio.

package mlow

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Signal generators

// qualityTestsGenTone is a pure cosine tone: n samples @16 kHz, amplitude amp in [-1,1].
func qualityTestsGenTone(freqHz float64, n int, amp float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		t := float64(i) / 16000.0
		out[i] = float32(float64(amp) * math.Cos(2.0*math.Pi*freqHz*t))
	}
	return out
}

// qualityTestsGenVoicedHarmonic is a harmonic-rich voiced signal: fundamental f0 + 5 harmonics
// with 1/k amplitude (band-limited sawtooth). The archetypal voiced-speech spectral shape: periodic
// AND multi-harmonic, so the encoder selects its voiced (LTP) path for these frames.
func qualityTestsGenVoicedHarmonic(f0Hz float64, n int, amp float32) []float32 {
	const nHarm = 6
	out := make([]float32, n)
	for i := range out {
		var s float64
		for k := 1; k <= nHarm; k++ {
			t := float64(i) / 16000.0
			s += float64((1.0 / float64(k)) * math.Cos(2.0*math.Pi*float64(k)*f0Hz*t))
		}
		// /2 to keep peak within ±1
		out[i] = float32(float64(amp) * s / 2.0)
	}
	return out
}

// qualityTestsGenWhiteNoise is white noise, amplitude-bounded. Deterministic (LCG) so the test is
// reproducible.
func qualityTestsGenWhiteNoise(n int, amp float32) []float32 {
	state := uint32(0x12345678)
	out := make([]float32, n)
	for i := range out {
		state = state*1664525 + 1013904223
		v := float64(int32(state)) / float64(math.MaxInt32)
		out[i] = float32(float64(amp) * v)
	}
	return out
}

// qualityTestsGenSilence is silence (all zeros).
func qualityTestsGenSilence(n int) []float32 {
	return make([]float32, n)
}

// qualityTestsGenChirp is a frequency chirp sweeping from fLo to fHi over n samples @16 kHz,
// amplitude amp.
func qualityTestsGenChirp(fLo, fHi float64, n int, amp float32) []float32 {
	phase := 0.0
	out := make([]float32, n)
	for i := range out {
		f := fLo + float64((fHi-fLo)*(float64(i)/float64(n)))
		phase += 2.0 * math.Pi * f / 16000.0
		out[i] = float32(float64(amp) * math.Cos(phase))
	}
	return out
}

// synth_mic.raw generator

// qualityTestsLcg is the deterministic LCG (same constants as `gen_white_noise`), threaded across
// all segments so the whole stream is one reproducible sequence.
type qualityTestsLcg struct{ s uint32 }

func (l *qualityTestsLcg) nextUnit() float64 {
	l.s = l.s*1664525 + 1013904223
	return float64(int32(l.s)) / float64(math.MaxInt32)
}

const (
	qualityTestsSynthSR    float64 = 16000.0
	qualityTestsSynthFrame         = 960
	qualityTestsSynthSeg           = 10 // frames per archetype block
)

// qualityTestsVoiced is a formant-shaped voiced block: f0 + 5 harmonics, 1/k^1.3 tilt with a soft
// bump near 700 Hz so the spectrum has width (not a pure line). `t0` is the absolute sample offset
// for phase continuity.
func qualityTestsVoiced(out []float32, f0, amp, noise float64, lcg *qualityTestsLcg, t0 int) []float32 {
	for i := 0; i < qualityTestsSynthSeg*qualityTestsSynthFrame; i++ {
		t := float64(t0+i) / qualityTestsSynthSR
		var s float64
		for k := 1; k <= 5; k++ {
			fk := float64(float64(k) * f0)
			g := (1.0 / math.Pow(float64(k), 1.3)) *
				(0.6 + float64(0.5*math.Exp(-powiF64((fk-700.0)/600.0, 2))))
			s += float64(g * math.Cos(2.0*math.Pi*fk*t))
		}
		n := lcg.nextUnit()
		out = append(out, float32(float64(amp*s/2.0)+float64(noise*n)))
	}
	return out
}

func qualityTestsNoiseBlock(out []float32, amp float64, lcg *qualityTestsLcg) []float32 {
	for i := 0; i < qualityTestsSynthSeg*qualityTestsSynthFrame; i++ {
		out = append(out, float32(amp*lcg.nextUnit()))
	}
	return out
}

func qualityTestsSilenceBlock(out []float32) []float32 {
	return append(out, make([]float32, qualityTestsSynthSeg*qualityTestsSynthFrame)...)
}

// synthMicPcm reproduces the committed `testdata/synth_mic.raw` (s16le, 16 kHz, mono, 110 frames)
// byte-for-byte as i16 samples. This is the in-repo recipe for the synthetic mic input: a
// deterministic sequence of speech-like archetypes (formant-shaped voiced harmonics, unvoiced
// noise, voiced+noise, silence) chosen so the VAD / pitch / LSF / gennoise / pulse / gains paths
// are all exercised and the encode produces config-1/config-2 frames as well as active config-0
// frames.
//
// `synth_mic.raw` is the oracle INPUT that the captured encoder/decoder fixtures were derived from
// (see PROVENANCE.md), so this generator must stay byte-exact: `synth_mic_raw_matches_generator`
// asserts equality, and the env-gated `regen_synth_mic_raw` rewrites the file from it.
func synthMicPcm() []int16 {
	out := make([]float32, 0, 11*qualityTestsSynthSeg*qualityTestsSynthFrame)
	lcg := &qualityTestsLcg{0x1234_5678}
	// Segment order is load-bearing (the captured fixtures consume this exact byte stream).
	out = qualityTestsVoiced(out, 150.0, 0.30, 0.03, lcg, len(out))
	out = qualityTestsVoiced(out, 130.0, 0.30, 0.03, lcg, len(out))
	out = qualityTestsNoiseBlock(out, 0.12, lcg)
	out = qualityTestsVoiced(out, 150.0, 0.22, 0.04, lcg, len(out))
	out = qualityTestsVoiced(out, 130.0, 0.30, 0.03, lcg, len(out))
	out = qualityTestsVoiced(out, 150.0, 0.30, 0.03, lcg, len(out))
	out = qualityTestsSilenceBlock(out)
	out = qualityTestsNoiseBlock(out, 0.06, lcg)
	out = qualityTestsVoiced(out, 130.0, 0.30, 0.03, lcg, len(out))
	out = qualityTestsNoiseBlock(out, 0.02, lcg)
	out = qualityTestsVoiced(out, 150.0, 0.30, 0.03, lcg, len(out))

	pcm := make([]int16, len(out))
	for i, v := range out {
		pcm[i] = f32ToI16(clampF32(v*32767.0, -32768.0, 32767.0))
	}
	return pcm
}

// qualityTestsSynthMicBytes is the s16le little-endian byte serialization of [synthMicPcm],
// matching the on-disk `.raw` layout.
func qualityTestsSynthMicBytes() []byte {
	pcm := synthMicPcm()
	b := make([]byte, 0, len(pcm)*2)
	for _, s := range pcm {
		b = binary.LittleEndian.AppendUint16(b, uint16(s))
	}
	return b
}

// The in-repo generator must reproduce the committed `synth_mic.raw` byte-for-byte. The committed
// file is the oracle input for the captured fixtures (PROVENANCE.md); if this drifts, those fixtures
// no longer correspond to a reproducible input.
func TestQualityTests_SynthMicRawMatchesGenerator(t *testing.T) {
	committed := testdataBytes(t, "synth_mic.raw")
	generated := qualityTestsSynthMicBytes()
	if len(generated) != len(committed) {
		t.Fatalf("synth_mic.raw length drifted: generator %d bytes vs committed %d bytes",
			len(generated), len(committed))
	}
	if !bytes.Equal(generated, committed) {
		t.Fatalf("synth_mic_pcm() no longer matches committed testdata/synth_mic.raw byte-for-byte")
	}
}

// Rewrites `testdata/synth_mic.raw` from [synthMicPcm] when `MLOW_GEN_SYNTH=1` (mirrors the
// `VOIP_GEN_TABLES` pattern). Regenerating invalidates every captured fixture derived from this
// input (PROVENANCE.md), so it is opt-in and otherwise a no-op.
func TestQualityTests_RegenSynthMicRaw(t *testing.T) {
	if os.Getenv("MLOW_GEN_SYNTH") != "1" {
		t.Skip("MLOW_GEN_SYNTH=1 not set")
	}
	path := filepath.Join("testdata", "synth_mic.raw")
	if err := os.WriteFile(path, qualityTestsSynthMicBytes(), 0o644); err != nil {
		t.Fatalf("write synth_mic.raw: %v", err)
	}
	t.Logf("wrote %s", path)
}

// Measurement helpers

// qualityTestsCorr is the Pearson correlation coefficient between two equal-length slices.
func qualityTestsCorr(a, b []float32) float64 {
	var sxy, sxx, syy float64
	for i := 0; i < min(len(a), len(b)); i++ {
		x, y := float64(a[i]), float64(b[i])
		sxy += float64(x * y)
		sxx += float64(x * x)
		syy += float64(y * y)
	}
	if sxx < 1e-12 || syy < 1e-12 {
		return 0.0
	}
	return sxy / math.Sqrt(sxx*syy)
}

// qualityTestsRms is the RMS of a signal.
func qualityTestsRms(x []float32) float64 {
	ss := math.Copysign(0, -1)
	for _, v := range x {
		ss += float64(float64(v) * float64(v))
	}
	return math.Sqrt(ss / float64(len(x)))
}

// qualityTestsGoertzel is the Goertzel power at `freqHz` (single-bin DFT). Returns unnormalized
// power.
func qualityTestsGoertzel(x []float32, freqHz, sr float64) float64 {
	w := 2.0 * math.Pi * freqHz / sr
	c := 2.0 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s0 := float64(v) + float64(c*s1) - s2
		s2 = s1
		s1 = s0
	}
	return float64(s1*s1) + float64(s2*s2) - float64(c*s1*s2)
}

// qualityTestsNormalizedAutocorr is the pitch-periodicity check: normalized autocorrelation at lag
// `lag`. For a perfectly periodic signal this is 1.0; for white noise it is ≈ 0.
func qualityTestsNormalizedAutocorr(x []float32, lag int) float64 {
	if lag >= len(x) {
		return 0.0
	}
	var num, e1, e2 float64
	for i := lag; i < len(x); i++ {
		a, b := float64(x[i]), float64(x[i-lag])
		num += float64(a * b)
		e1 += float64(a * a)
		e2 += float64(b * b)
	}
	if e1 < 1e-12 || e2 < 1e-12 {
		return 0.0
	}
	return num / math.Sqrt(e1*e2)
}

// qualityTestsPeakAutocorr is the peak normalized autocorrelation over a range of lags
// [lagLo, lagHi] (inclusive).
func qualityTestsPeakAutocorr(x []float32, lagLo, lagHi int) float64 {
	acc := 0.0
	for lag := lagLo; lag <= lagHi; lag++ {
		acc = maxF64(acc, math.Abs(qualityTestsNormalizedAutocorr(x, lag)))
	}
	return acc
}

// Shared encode-decode helper

// qualityTestsRoundTrip encodes `pcm` as a stream of 60 ms MLow frames and decodes them back.
// Returns the decoded PCM (same length as input). The first 60 ms (960 samples) are the cold-start
// onset (LPC history is zero); quality measurements should skip it.
func qualityTestsRoundTrip(t testing.TB, pcm []float32) []float32 {
	t.Helper()
	const frame = 960
	enc := NewEncoder()
	dec := NewDecoder()
	out := make([]float32, 0, len(pcm))
	i := 0
	for i+frame <= len(pcm) {
		pkt, err := enc.Encode(pcm[i : i+frame])
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		out = dec.AppendDecode(out, pkt)
		i += frame
	}
	return out
}

// qualityTestsEnvelope is the short-time RMS envelope of `x` over `win`-sample blocks.
func qualityTestsEnvelope(x []float32, win int) []float32 {
	out := make([]float32, len(x)/win)
	for i := range out {
		s := float32(math.Copysign(0, -1))
		for _, v := range x[i*win : (i+1)*win] {
			s += float32(v * v)
		}
		out[i] = sqrtF32(s / float32(win))
	}
	return out
}

// Speech-like round-trip regression: the deterministic synthetic mic (`synth_mic.raw`, reproduced
// in-repo by [synthMicPcm]: voiced harmonics + noise + silence) encoded by `Encoder` and
// decoded by `Decoder` must reconstruct the spectral ENERGY CONTOUR of the input (short-time
// envelope correlation) and not collapse to silence or constant noise. The waveform-phase
// (sample-level) match is bounded by the excitation fine-structure not reproduced here, so the guard
// is on the perceptually-dominant envelope.
func TestQualityTests_SpeechEnergyContourTracksInput(t *testing.T) {
	pcm := decoderRefPCM(t, "synth_mic.raw")
	out := qualityTestsRoundTrip(t, pcm)

	// The decoder's harmonic postfilter delays the output ~48 samples; align before correlating.
	const delay = 48
	const win = 320
	ea := qualityTestsEnvelope(pcm, win)
	eb := qualityTestsEnvelope(out[min(delay, len(out)):], win)
	m := min(len(ea), len(eb))
	ec := qualityTestsCorr(ea[:m], eb[:m])
	if !(ec > 0.45) {
		t.Fatalf("real-speech envelope correlation %.3f too low (outbound mute regression)", ec)
	}

	// Level must be in the right ballpark (not silent, not blown up).
	ri, ro := qualityTestsRms(pcm), qualityTestsRms(out)
	if !(ro > 0.3*ri && ro < 3.0*ri) {
		t.Fatalf("decoded RMS %.4f not within [0.3x,3x] of input RMS %.4f", ro, ri)
	}
}

// OUTBOUND: encode→decode quality tests

// A 550 Hz pure tone must reconstruct with frequency concentration (Goertzel signal/control >> 1).
// The existing test in encode.rs requires corr > 0.5 after 8 frames; this test adds a tighter
// spectral-concentration check that would also fail for the unvoiced-only encoder on voiced inputs.
func TestQualityTests_ToneSpectralConcentration(t *testing.T) {
	const freq = 550.0
	const nFrames = 5
	const frame = 960
	pcm := qualityTestsGenTone(freq, frame*nFrames, 0.4)
	out := qualityTestsRoundTrip(t, pcm)
	// Skip the cold-start onset frame.
	steady := out[frame:]
	signalPower := qualityTestsGoertzel(steady, freq, 16000.0)
	// Control bands: inter-harmonic neighbours that only noise occupies for a pure tone.
	controls := [...]float64{
		qualityTestsGoertzel(steady, freq/2.0, 16000.0),
		qualityTestsGoertzel(steady, freq*1.7, 16000.0),
		qualityTestsGoertzel(steady, freq*2.0+50.0, 16000.0),
		qualityTestsGoertzel(steady, 1500.0, 16000.0),
	}
	controlPower := 0.0
	for _, c := range controls {
		controlPower = maxF64(controlPower, c)
	}
	ratio := math.Inf(1)
	if controlPower > 0.0 {
		ratio = signalPower / controlPower
	}
	if !(ratio > 3.0) {
		t.Fatalf("pure tone %v Hz: spectral concentration ratio %.1fx < 3x (constant noise would fail this)", freq, ratio)
	}
}

// Silence must produce near-zero output energy. Catches a bug where an encoder always emits
// non-zero pulses even for a zero-signal input.
func TestQualityTests_SilenceStaysSilent(t *testing.T) {
	const nFrames = 4
	const frame = 960
	pcm := qualityTestsGenSilence(frame * nFrames)
	out := qualityTestsRoundTrip(t, pcm)
	steady := out[frame:]
	r := qualityTestsRms(steady)
	if !(r < 0.01) {
		t.Fatalf("silence: decoded RMS %.6f is non-negligible (encoder should not inject energy into silence)", r)
	}
}

// Regression floor: voiced speech (harmonic-rich, fundamental + harmonics) reconstructs with PITCH
// PERIODICITY intact: the decoded autocorrelation at the pitch lag stays above 0.4. On this clean
// synthetic signal the LPC poles alone already reach well past it, so 0.4 is a wide floor; any
// future change that drops it below 0.4 here signals a serious encoder defect stripping
// periodicity. The tighter spectral guard is `voiced_fundamental_survives_in_spectrum`.
func TestQualityTests_VoicedHarmonicHasPitchPeriodicity(t *testing.T) {
	const nFrames = 6
	const frame = 960
	sr := 16000.0
	// Three fundamental frequencies in the voiced range (lag 80..143 samples @ 16 kHz):
	// 150 Hz -> lag ~107, 130 Hz -> lag ~123, 120 Hz -> lag ~133.
	for _, f0 := range []float64{150.0, 130.0, 120.0} {
		pcm := qualityTestsGenVoicedHarmonic(f0, frame*nFrames, 0.3)
		out := qualityTestsRoundTrip(t, pcm)
		// Skip the onset frame; measure steady-state.
		steady := out[frame:]

		lag := f64ToUsize(math.Round(sr / f0))
		autocorrAtLag := math.Abs(qualityTestsNormalizedAutocorr(steady, lag))

		// Sanity: the INPUT itself should be strongly periodic at this lag.
		inputSteady := pcm[frame:]
		inputAutocorr := math.Abs(qualityTestsNormalizedAutocorr(inputSteady, lag))
		if !(inputAutocorr > 0.5) {
			t.Fatalf("f0=%vHz: input autocorr at lag %d is %.3f; test signal is not voiced enough", f0, lag, inputAutocorr)
		}

		if !(autocorrAtLag > 0.4) {
			t.Fatalf("f0=%vHz (lag=%d): decoded autocorr at pitch lag = %.3f < 0.4; "+
				"the encoder is stripping pitch periodicity", f0, lag, autocorrAtLag)
		}
	}
}

// Corollary: white noise must NOT have a significant autocorrelation peak in the pitched range,
// so the encoder should not spuriously apply LTP. This tests that the voiced path is not triggered
// for an aperiodic signal.
func TestQualityTests_NoiseDoesNotProducePitchPeriodicity(t *testing.T) {
	const nFrames = 4
	const frame = 960
	pcm := qualityTestsGenWhiteNoise(frame*nFrames, 0.3)
	out := qualityTestsRoundTrip(t, pcm)
	steady := out[frame:]

	// Peak autocorrelation over the voiced lag range (80..143 samples).
	peak := qualityTestsPeakAutocorr(steady, 80, 143)
	if !(peak < 0.8) {
		t.Fatalf("noise: peak autocorr in voiced range = %.3f >= 0.8; encoder is spuriously applying LTP", peak)
	}
	// Must still produce some energy (not silence).
	r := qualityTestsRms(steady)
	if !(r > 0.001) {
		t.Fatalf("noise: decoded RMS %.6f is too low; encoder is discarding signal energy", r)
	}
}

// A chirp sweeping 300 Hz → 800 Hz must track the input: the dominant frequency of the second
// half of the decoded output must be higher than the first half, confirming the spectral envelope
// adapts frame by frame.
func TestQualityTests_ChirpTracksFrequency(t *testing.T) {
	const nFrames = 8
	const frame = 960
	pcm := qualityTestsGenChirp(300.0, 800.0, frame*nFrames, 0.3)
	out := qualityTestsRoundTrip(t, pcm)

	findDomFreq := func(seg []float32) float64 {
		bestPower, bestF := 0.0, 0.0
		f := 100.0
		for f < 1500.0 {
			p := qualityTestsGoertzel(seg, f, 16000.0)
			if p > bestPower {
				bestPower = p
				bestF = f
			}
			f += 5.0
		}
		return bestF
	}

	// early = frames 1..3 (skip cold onset), late = last 3 frames
	earlyStart := frame
	earlyEnd := 3 * frame
	lateStart := (nFrames - 3) * frame
	lateEnd := nFrames * frame

	if len(out) < lateEnd {
		t.Fatalf("decoded output too short (%d < %d); the chirp decode regressed", len(out), lateEnd)
	}
	domEarly := findDomFreq(out[earlyStart:earlyEnd])
	domLate := findDomFreq(out[lateStart:lateEnd])

	if !(domLate > domEarly) {
		t.Fatalf("chirp: dominant frequency did not rise (early=%.0fHz late=%.0fHz); "+
			"encoder is not tracking the time-varying spectral envelope", domEarly, domLate)
	}
}

// Energy tracking: the decoded RMS must roughly follow the input RMS across a range of signal
// levels. A constant-noise output would have nearly constant RMS regardless of input level,
// which this test catches by checking that a louder input produces louder output.
//
// Uses broadband white noise (the unvoiced/fricative case the codec's `nrgres` level path targets):
// its LPC residual scales directly with input amplitude. A single sinusoid or a few discrete
// harmonics instead whiten almost entirely to the LPC's numerical floor, so their residual energy
// (hence `nrgres`) does NOT track amplitude; carrying their level needs the per-pulse FCB gain
// precision (the excitation refinement not implemented here), so they are not a valid
// level-tracking probe.
func TestQualityTests_EnergyTracksInputLevel(t *testing.T) {
	const nFrames = 4
	const frame = 960
	prevRms := 0.0
	for _, amp := range []float32{0.05, 0.15, 0.35} {
		pcm := qualityTestsGenWhiteNoise(frame*nFrames, amp)
		out := qualityTestsRoundTrip(t, pcm)
		steady := out[frame:]
		r := qualityTestsRms(steady)
		// A louder input must produce louder output (monotone, not necessarily proportional).
		if !(r > prevRms) {
			t.Fatalf("energy not monotone: amp=%v decoded RMS=%.4f not above prev %.4f", amp, r, prevRms)
		}
		prevRms = r
	}
	// A silent input must produce near-silence.
	silentPcm := qualityTestsGenSilence(frame * nFrames)
	silentOut := qualityTestsRoundTrip(t, silentPcm)
	silentSteady := silentOut[frame:]
	if !(qualityTestsRms(silentSteady) < prevRms/5.0) {
		t.Fatalf("energy not quashed for silence: silent decoded RMS %.4f is too close to loud %.4f",
			qualityTestsRms(silentSteady), prevRms)
	}
}

// Voiced harmonic signal: the Goertzel power at the fundamental must dominate inter-harmonic bands
// in the decoded output. This is an ALTERNATIVE formulation of
// `voiced_harmonic_has_pitch_periodicity` that uses spectral evidence instead of time-domain
// autocorrelation.
func TestQualityTests_VoicedFundamentalSurvivesInSpectrum(t *testing.T) {
	const nFrames = 6
	const frame = 960
	for _, f0 := range []float64{150.0, 130.0, 120.0} {
		pcm := qualityTestsGenVoicedHarmonic(f0, frame*nFrames, 0.3)
		out := qualityTestsRoundTrip(t, pcm)
		steady := out[frame:]

		eF0 := qualityTestsGoertzel(steady, f0, 16000.0)
		// Control: inter-harmonic bands that are NOT f0 or its harmonics.
		controls := [...]float64{
			qualityTestsGoertzel(steady, f0*0.5, 16000.0),
			qualityTestsGoertzel(steady, f0*1.5, 16000.0),
			qualityTestsGoertzel(steady, f0*2.5, 16000.0),
		}
		controlMax := 0.0
		for _, c := range controls {
			controlMax = maxF64(controlMax, c)
		}
		ratio := math.Inf(1)
		if controlMax > 0.0 {
			ratio = eF0 / controlMax
		}
		if !(ratio > 5.0) {
			t.Fatalf("f0=%vHz: fundamental/inter-harmonic ratio %.1fx < 5x in spectrum; "+
				"voiced signal is not reconstructing the fundamental (unvoiced noise smears it)", f0, ratio)
		}
	}
}

// INBOUND: decoder energy-envelope tracking from captured frames

// The decoder must reproduce meaningful energy from active frames (not silence). This is a guard
// against a regression where the decoder always returns zeros for all active frames.
//
// Uses e2e_vectors.json (see PROVENANCE.md): synthetic frames paired with a reference PCM decode,
// with inactive-TOC frames zeroed to match the decoder's own DTX routing. The test checks that
// active frames (reference RMS > 0.005) produce Go-decoded RMS in the same order of magnitude
// (within 10x), confirming energy-envelope tracking.
func TestQualityTests_DecoderTracksEnergyEnvelope(t *testing.T) {
	recs := loadPacked(t, "e2e_vectors.cbor.zst")
	arr := recs.Arr()
	dec := NewDecoder()
	var activePairs [][2]float64 // (ref_rms, rust_rms)

	for _, rec := range arr {
		frame := rec.Get("frame").Hex()
		want := rec.Get("pcm").F32s()
		got := dec.Decode(frame)
		refR := qualityTestsRms(want)
		rustR := qualityTestsRms(got)
		if refR > 0.005 {
			activePairs = append(activePairs, [2]float64{refR, rustR})
		}
	}

	if len(activePairs) == 0 {
		t.Fatalf("no active frames found in e2e_vectors.json; capture may be DTX-only")
	}

	// For every active frame, the Go RMS must be within 10x of the reference RMS (either
	// direction). A regression to silence would give Go RMS near 0 while the reference RMS > 0.
	failures := 0
	for i, p := range activePairs {
		refR, rustR := p[0], p[1]
		if rustR < refR/10.0 {
			t.Logf("  active frame %d: ref RMS=%.4f Rust RMS=%.6f (Rust %.1fx quieter)",
				i, refR, rustR, refR/maxF64(rustR, 1e-9))
			failures++
		} else if rustR > refR*10.0 {
			t.Logf("  active frame %d: ref RMS=%.4f Rust RMS=%.6f (Rust %.1fx louder)",
				i, refR, rustR, rustR/maxF64(refR, 1e-9))
			failures++
		}
	}
	if failures != 0 {
		t.Fatalf("%d/%d active frames: Rust decoder RMS is >10x off the reference (either direction)",
			failures, len(activePairs))
	}
}

// The decoder must produce ZERO output for silence frames. Confirms that DTX/SID frames don't
// inject energy.
func TestQualityTests_DecoderSilenceFramesProduceZero(t *testing.T) {
	recs := loadPacked(t, "e2e_vectors.cbor.zst")
	arr := recs.Arr()
	dec := NewDecoder()

	for i, rec := range arr {
		frame := rec.Get("frame").Hex()
		want := rec.Get("pcm").F32s()
		refR := qualityTestsRms(want)
		got := dec.Decode(frame)
		rustR := qualityTestsRms(got)
		// Only a SID frame is genuinely silence. This fixture also holds coded-inactive frames
		// (VoA=0, hang-over clear) whose PCM was ZEROED when it was generated, to match a decoder
		// that routed them to silence. The reference decodes those to background noise, so they are
		// not an oracle for this property and asserting over them would re-pin the old behavior;
		// `decoder.rs::dtx_off_frames_decode_to_audio` covers them against an unmodified vector.
		isSid := frame[0]&0x80 != 0
		if refR < 0.001 && isSid {
			if !(rustR < 0.001) {
				t.Fatalf("frame %d: reference is silence (RMS=%.6f) but Rust produced RMS=%.6f", i, refR, rustR)
			}
		}
	}
}

// Voiced harmonic signal at LOW amplitude: the voiced path's LTP prediction carries energy that an
// unvoiced-only path (constrained by its fixed gain step, placing few pulses at low amplitude)
// could not, so the reconstruction stays correlated with the input even when the pulse budget is
// thin. Guards that the voiced path keeps reconstructing the signal at amp 0.05.
func TestQualityTests_VoicedHarmonicLowAmplitudeTracksInput(t *testing.T) {
	const nFrames = 8
	const frame = 960
	// Low amplitude: 0.05 (int16-scaled ~1638 peak), where the pulse budget is thin.
	pcm := qualityTestsGenVoicedHarmonic(130.0, frame*nFrames, 0.05)
	out := qualityTestsRoundTrip(t, pcm)
	// The decoder's harmonic postfilter both delays the output (~48 samples group delay) and resonates
	// it at the pitch period, so for this synthetic 130 Hz tone the reconstruction is a phase-shifted
	// (and partly inverted) copy. Search the best phase alignment over one pitch period and assert the
	// magnitude tracks; this guards the encoder voiced path, not the exact decoder phase.
	steadyIn := pcm[frame:]
	best := 0.0
	for d := 0; d < 128; d++ {
		if frame+d >= len(out) {
			break
		}
		n := min(len(steadyIn), len(out)-frame-d)
		best = maxF64(best, math.Abs(qualityTestsCorr(steadyIn[:n], out[frame+d:frame+d+n])))
	}
	if !(best > 0.7) {
		t.Fatalf("low-amplitude harmonic: best phase-aligned |corr|=%.3f < 0.7; the voiced path is not "+
			"reconstructing the signal", best)
	}
}

// Encoding a multi-frame harmonic stream produces valid output for every frame: each frame carries
// the active config=0 TOC (`0x50`) and decodes to a full 960-sample block, so the encode→decode
// chain stays total across frame boundaries.
func TestQualityTests_VoicedHarmonicMultipleFramesNoPanic(t *testing.T) {
	const nFrames = 6
	const frame = 960
	pcm := qualityTestsGenVoicedHarmonic(130.0, frame*nFrames, 0.3)
	// Just confirm encoding all frames produces valid output without panicking.
	enc := NewEncoder()
	dec := NewDecoder()
	totalDecoded := 0
	for i := 0; i < nFrames; i++ {
		pkt, err := enc.Encode(pcm[i*frame : (i+1)*frame])
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if pkt[0] != 0x50 {
			t.Fatalf("expected active config=0 TOC, got %#04x", pkt[0])
		}
		decoded := dec.Decode(pkt)
		totalDecoded += len(decoded)
	}
	if totalDecoded != frame*nFrames {
		t.Fatalf("expected %d*%d decoded samples, got %d", frame, nFrames, totalDecoded)
	}
}

// The encoder's voiced/unvoiced decision is pitch/VAD-driven, so there is no byte-exact-encoder gate
// here: the wire format stays pinned by the byte-exact decoder round-trip vectors and the golden test.

// A captured, negotiated MLOW stream must decode to clean, voice-level audio. The stream is a
// captured-encoder fixture (see PROVENANCE.md): not reproducible here because our encoder does not
// match the peer's exact wire bytes, so it is committed and pinned by the tripwire test below.
func TestQualityTests_CapturedInboundMlowDecodesClean(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	if len(frames) == 0 {
		t.Fatalf("capture is empty")
	}

	dec := NewDecoder()
	var all []float32
	activeFrames := 0

	for _, frame := range frames {
		pcm := dec.Decode(frame)
		// `Decode` is total: even a TOC the standard Opus decoder would reject yields valid PCM.
		// Active frames are 960 samples (60 ms); DTX/CN frames return a shorter block, never empty.
		if len(pcm) == 0 {
			t.Fatalf("every mlow frame decodes to non-empty PCM")
		}
		if qualityTestsRms(pcm) > 0.005 {
			activeFrames++
		}
		all = append(all, pcm...)
	}

	// Clean decode is voice-level and never saturates.
	overallRms := qualityTestsRms(all)
	var peak float32 = 0.0
	clip := 0
	for _, x := range all {
		peak = maxF32(peak, absF32(x))
		if absF32(x) >= 0.999 {
			clip++
		}
	}
	clipPct := 100.0 * float64(clip) / float64(len(all))

	if !(overallRms >= 0.01 && overallRms < 0.20) {
		t.Errorf("inbound RMS %.4f outside voice range (mis-routed decode would be louder)", overallRms)
	}
	if !(peak < 0.95) {
		t.Errorf("inbound peak %.4f near full-scale; clipping noise, not clean voice", peak)
	}
	if !(clipPct < 0.5) {
		t.Errorf("inbound %.2f%% samples clipped (mis-routed decode clips heavily)", clipPct)
	}
	if !(activeFrames > len(frames)/10) {
		t.Errorf("only %d/%d active frames; stream decoded to silence", activeFrames, len(frames))
	}
}

// Branch-coverage tripwire for `inbound_capture_frames.json`: the committed stream must keep at
// least one config-1 (TOC `0x10`) AND one config-2 (TOC `0x12`) frame, alongside the active
// config-0 (`0x50`) frames. The clean-decode test above exercises the per-TOC decode branches, so a
// regenerated fixture that lost the `0x12` frames would silently stop covering the config-2 path.
//
// This fixture is NOT reproducible here: it is the captured mlow encode of `synth_mic.raw` (see
// PROVENANCE.md), and this package ships no encoder producing those exact wire bytes. The tripwire
// is what lets the committed bytes be trusted without an in-repo regenerator.
func TestQualityTests_InboundCaptureFramesCoverConfig1AndConfig2Tocs(t *testing.T) {
	arr := loadJSON(t, "inbound_capture_frames.json").Arr()
	has := func(want byte) bool {
		for _, v := range arr {
			b, err := hexDecode(v.Str())
			if err == nil && len(b) > 0 && b[0] == want {
				return true
			}
		}
		return false
	}
	if !has(0x10) {
		t.Errorf("fixture lost config-1 (0x10) frames: config-1 decode branch no longer covered")
	}
	if !has(0x12) {
		t.Errorf("fixture lost config-2 (0x12) frames: config-2 decode branch no longer covered")
	}
	if !has(0x50) {
		t.Errorf("fixture lost active config-0 (0x50) frames: capture is not a normal call stream")
	}
}

// The wasm-derived oracle pair: `wasm_derived_frames.json` is the shipped
// VoIP engine's own encode of `synth_mic.raw` (derived with
// `oracle derive --spec specs/mlow_110frames.json` in unwasm, pinned by
// module hash — see PROVENANCE.md), and `wasm_derived_ref.raw` is the same
// engine's decode of those packets, 960 s16le samples per frame.
//
// Compares our decoder with the shipped decoder on the shipped encoder's
// packets. Correlation allows numeric differences in DSP implementations;
// the independent C oracle remains covered by the adjacent fixture tests.
func TestQualityTests_WasmDerivedFramesDecodeToWasmReference(t *testing.T) {
	frames := decoderFrames(t, "wasm_derived_frames.json")
	refp := decoderRefPCM(t, "wasm_derived_ref.raw")
	if len(refp) != len(frames)*960 {
		t.Fatalf("ref must hold 960 samples/frame")
	}

	dec := NewDecoder()
	nActive := 0
	corrSum := 0.0
	worstCorr := 1.0
	for k, frame := range frames {
		out := dec.Decode(frame)
		if len(out) != 960 {
			t.Fatalf("frame %d: the complete coded packet must decode", k)
		}
		expected := refp[k*960 : (k+1)*960]
		eOut, eRef := qualityTestsRms(out), qualityTestsRms(expected)
		if eRef < 0.005 {
			// Quiet in, quiet out: correlation is undefined on near-silence
			// (`corr` returns 0.0), so assert the envelope instead.
			if !(eOut < 0.02) {
				t.Fatalf("frame %d: quiet ref %.4f decoded loud at %.4f", k, eRef, eOut)
			}
			continue
		}
		c := qualityTestsCorr(out, expected)
		worstCorr = minF64(worstCorr, c)
		corrSum += c
		nActive++
		if r := eOut / eRef; !(r >= 0.25 && r < 4.0) {
			t.Fatalf("frame %d: energy ratio out %.4f vs ref %.4f outside 4x band", k, eOut, eRef)
		}
	}
	if !(nActive > len(frames)/2) {
		t.Fatalf("only %d/%d active frames; stream decoded short", nActive, len(frames))
	}
	meanCorr := corrSum / float64(nActive)
	if !(meanCorr > 0.90) {
		t.Fatalf("mean active-frame correlation %.4f too low against the shipped decoder", meanCorr)
	}
	if !(worstCorr > 0.70) {
		t.Fatalf("worst active-frame correlation %.4f too low against the shipped decoder", worstCorr)
	}
}
