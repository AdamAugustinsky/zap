// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/quality_metrics.rs, MIT.
//
// Decoder-quality regression harness: decode the captured inbound stream through [Decoder]
// and measure it against the reference decode (`ref_usesmpl_expected.raw`; see PROVENANCE.md).
// These metrics are the objective signal that the pure-Go decoder reproduces the codec's
// excitation/noise/spectral shape, not just its energy envelope.
//
// The reference is the byte-exact decode of the same 373 frames. It applies a 48-sample group
// delay in its harmonic postfilter, so the harness searches a small reference delay before
// correlating.

package mlow

import (
	"math"
	"os"
	"testing"
)

const qualityMetricsSR float64 = 16000.0

// qualityMetricsDecodeCapture decodes every captured frame and concatenates the PCM (float [-1,1]).
func qualityMetricsDecodeCapture(t testing.TB) []float32 {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	dec := NewDecoder()
	var out []float32
	for _, frame := range frames {
		out = append(out, dec.Decode(frame)...)
	}
	return out
}

// qualityMetricsLoadRef loads the decoder reference (s16le @ 16 kHz) as float [-1,1].
func qualityMetricsLoadRef(t testing.TB) []float32 {
	return decoderRefPCM(t, "ref_usesmpl_expected.raw")
}

func qualityMetricsRms(x []float32) float64 {
	if len(x) == 0 {
		return 0.0
	}
	ss := math.Copysign(0, -1)
	for _, v := range x {
		ss += float64(float64(v) * float64(v))
	}
	return math.Sqrt(ss / float64(len(x)))
}

// qualityMetricsCorrAtLag is the zero-mean Pearson correlation over the overlap, with the
// reference shifted by `lag` samples (ref[i+lag] vs out[i]).
func qualityMetricsCorrAtLag(refp, out []float32, lag int) float64 {
	n := min(satSubInt(len(refp), lag), len(out))
	if n < 16 {
		return 0.0
	}
	r := refp[lag : lag+n]
	o := out[:n]
	mr := math.Copysign(0, -1)
	for _, v := range r {
		mr += float64(v)
	}
	mr /= float64(n)
	mo := math.Copysign(0, -1)
	for _, v := range o {
		mo += float64(v)
	}
	mo /= float64(n)
	var sxy, sxx, syy float64
	for i := 0; i < n; i++ {
		dr := float64(r[i]) - mr
		dz := float64(o[i]) - mo
		sxy += float64(dr * dz)
		sxx += float64(dr * dr)
		syy += float64(dz * dz)
	}
	if sxx < 1e-12 || syy < 1e-12 {
		return 0.0
	}
	return sxy / math.Sqrt(sxx*syy)
}

// qualityMetricsBestDelayCorr is the best delay-aligned correlation: search reference delay
// 0..=maxLag, return (bestLag, corr).
func qualityMetricsBestDelayCorr(refp, out []float32, maxLag int) (int, float64) {
	bestLag, bestC := 0, math.Inf(-1)
	for lag := 0; lag <= maxLag; lag++ {
		c := qualityMetricsCorrAtLag(refp, out, lag)
		if c > bestC {
			bestLag, bestC = lag, c
		}
	}
	return bestLag, bestC
}

// qualityMetricsNearZeroFraction is the fraction of near-zero samples (|x| < 1e-4) over the active
// (RMS-bearing) regions. Buzzy/robotic excitation leaves long dead gaps between FCB pulses;
// broadband noise fill reduces this.
func qualityMetricsNearZeroFraction(x []float32) float64 {
	const w = 960
	zeros, total := 0, 0
	for off := 0; off < len(x); off += w {
		chunk := x[off:min(off+w, len(x))]
		if qualityMetricsRms(chunk) < 0.003 {
			continue
		}
		for _, v := range chunk {
			if absF32(v) < 1e-4 {
				zeros++
			}
			total++
		}
	}
	if total == 0 {
		return 0.0
	}
	return float64(zeros) / float64(total)
}

// qualityMetricsGoertzel is the Goertzel power at `freqHz` over the whole signal (unnormalized).
func qualityMetricsGoertzel(x []float32, freqHz float64) float64 {
	w := 2.0 * math.Pi * freqHz / qualityMetricsSR
	c := 2.0 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s0 := float64(v) + float64(c*s1) - s2
		s2 = s1
		s1 = s0
	}
	return float64(s1*s1) + float64(s2*s2) - float64(c*s1*s2)
}

// qualityMetricsBandEnergy is the 3-band energy via a bank of Goertzel bins: <700, 700-4000,
// >4000 Hz.
func qualityMetricsBandEnergy(x []float32) (float64, float64, float64) {
	var lo, mid, hi float64
	f := 100.0
	for f < 7900.0 {
		p := qualityMetricsGoertzel(x, f)
		if f < 700.0 {
			lo += p
		} else if f < 4000.0 {
			mid += p
		} else {
			hi += p
		}
		f += 100.0
	}
	return lo, mid, hi
}

// qualityMetricsReport prints the full metric battery (used to track each change's before/after).
func qualityMetricsReport(t testing.TB, out, refp []float32) {
	n := min(len(out), len(refp))
	lag, c := qualityMetricsBestDelayCorr(refp, out, 128)
	lo, mid, hi := qualityMetricsBandEnergy(out[:n])
	rlo, rmid, rhi := qualityMetricsBandEnergy(refp[:n])
	t.Logf(
		"METRICS len=%d rms=%.5f ref_rms=%.5f best_lag=%d corr=%.4f gap=%.4f "+
			"band(lo/mid/hi)=%.3e/%.3e/%.3e ref_band=%.3e/%.3e/%.3e "+
			"hi_ratio=%.3f mid_ratio=%.3f lo_ratio=%.3f",
		len(out),
		qualityMetricsRms(out),
		qualityMetricsRms(refp),
		lag,
		c,
		qualityMetricsNearZeroFraction(out),
		lo,
		mid,
		hi,
		rlo,
		rmid,
		rhi,
		hi/maxF64(rhi, 1e-30),
		mid/maxF64(rmid, 1e-30),
		lo/maxF64(rlo, 1e-30),
	)
}

// Baseline / progress probe; prints metrics, never asserts. Run with
// `MLOW_METRICS=1 go test -run TestQualityMetrics_DecodeMetricsReport -v` (Rust: `#[ignore]`).
func TestQualityMetrics_DecodeMetricsReport(t *testing.T) {
	if _, ok := os.LookupEnv("MLOW_METRICS"); !ok {
		t.Skip("diagnostic: prints metrics, used while iterating on the decoder")
	}
	out := qualityMetricsDecodeCapture(t)
	refp := qualityMetricsLoadRef(t)
	qualityMetricsReport(t, out, refp)
}

// Invariant: the decode reproduces the reference's spectral signature, not just its energy. Band
// ratios, inter-pulse gap fraction, RMS, and delay-aligned correlation all stay within the
// thresholds that prove broadband noise fill and excitation shape are correct.
func TestQualityMetrics_DecodeMatchesRefUsesmpl(t *testing.T) {
	out := qualityMetricsDecodeCapture(t)
	refp := qualityMetricsLoadRef(t)
	if len(out) != len(refp) {
		t.Fatalf("decode length must match the reference: %d != %d", len(out), len(refp))
	}

	lo, mid, hi := qualityMetricsBandEnergy(out)
	rlo, _, rhi := qualityMetricsBandEnergy(refp)
	ourRms := qualityMetricsRms(out)
	refRms := qualityMetricsRms(refp)
	gap := qualityMetricsNearZeroFraction(out)
	_, c := qualityMetricsBestDelayCorr(refp, out, 128)
	hiRatio := hi / maxF64(rhi, 1e-30)
	loRatio := lo / maxF64(rlo, 1e-30)

	if !(hiRatio >= 0.35 && hiRatio < 3.0) {
		t.Errorf("hi-band ratio %.3f out of [0.35, 3.0): out=%.3e ref=%.3e, mid=%.3e", hiRatio, hi, rhi, mid)
	}
	if !(loRatio < 2.5) {
		t.Errorf("low-band ratio %.3f too boomy: out=%.3e ref=%.3e", loRatio, lo, rlo)
	}
	if !(gap < 0.32) {
		t.Errorf("inter-pulse gap fraction %.4f >= 0.32, noise fill missing", gap)
	}
	if !(ourRms >= refRms*0.4 && ourRms < refRms*2.2) {
		t.Errorf("rms %.4f vs ref %.4f out of range", ourRms, refRms)
	}
	if !(c > 0.55) {
		t.Errorf("delay-aligned corr %.4f <= 0.55, excitation shape still wrong", c)
	}
}
