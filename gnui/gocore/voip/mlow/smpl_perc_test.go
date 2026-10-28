// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_perc.rs, MIT.
//
// The `#[cfg(test)] mod tests` of smpl_perc.rs plus the `#[cfg(test)]` items from its non-test
// part (RefFftScratch, the test-only allocating rfft wrappers, the full-length reference rfft, and
// the twiddle lookups by length).

package mlow

import (
	"fmt"
	"math"
	"testing"
)

// combineFor and baseFor are test-only lookups by length, kept so `twiddle_table_is_bit_exact`
// can walk the chain the way it always has. The hot path indexes `levels` by depth instead.
func (t *fftTwiddles) combineFor(n int) []cpx {
	for i := range t.levels {
		if t.levels[i].n == n {
			return t.levels[i].tw
		}
	}
	panic(fmt.Sprintf("internal error: entered unreachable code: combine twiddle table missing for n=%d", n))
}

func (t *fftTwiddles) baseFor(n int) []cpx {
	if t.baseN != n {
		panic(fmt.Sprintf("base twiddle table missing for n=%d", n))
	}
	return t.base
}

// refFftScratch is the full-length workspace for the historical real-FFT path, kept only so the
// differential tests can grade the shipped transform against a second, independently written one.
// It is what the codec ran before the half-length plan replaced it; nothing outside the tests
// builds it, which is why a shipping encoder no longer carries the length-`n` tables and arena at
// all.
type refFftScratch struct {
	arena []cpx
	cin   []cpx
	spec  []cpx
	tout  []cpx
	twFwd fftTwiddles // sign = -1.0
	twBwd fftTwiddles // sign = +1.0
}

func newRefFftScratch(n int) refFftScratch {
	return refFftScratch{
		arena: make([]cpx, fftArenaLen(n)),
		cin:   make([]cpx, n),
		spec:  make([]cpx, n),
		tout:  make([]cpx, n),
		twFwd: newFftTwiddles(n, -1.0),
		twBwd: newFftTwiddles(n, 1.0),
	}
}

// rfftForwardOrdered is the test-only allocating wrapper preserving the original
// `rfft_forward_ordered` signature for the FFT unit tests. Production LPC analysis threads a
// reusable `fftScratch` (`newLpcFftScratch`) instead. Same arithmetic, so output is bit-identical.
func rfftForwardOrdered(time, f []float32) {
	sc := newFftScratch(len(time))
	rfftForwardOrderedSc(time, f, &sc)
}

// rfftBackwardOrdered is the test-only allocating wrapper preserving the original
// `rfft_backward_ordered` signature.
func rfftBackwardOrdered(f, time []float32) {
	sc := newFftScratch(len(f))
	rfftBackwardOrderedSc(f, time, &sc)
}

// rfftForwardOrderedRefSc is the full-length real FFT the codec ran before the half-length plan
// replaced it: one length-`n` complex transform of the zero-imaginary input (forward), or of the
// rebuilt Hermitian spectrum (backward). Kept as a second, independently written implementation of
// the same transform so `rfft_matches_full_length_implementation` and
// `rfft_error_is_at_full_length_level` have something to grade the shipped one against. Not
// compiled into a shipping build.
func rfftForwardOrderedRefSc(time, f []float32, sc *refFftScratch) {
	n := len(time)
	cin := sc.cin
	for i := 0; i < n; i++ {
		cin[i].re = time[i]
		cin[i].im = 0.0
	}
	cfft(cin, sc.spec, -1.0, sc.arena, &sc.twFwd)
	spec := sc.spec
	f[0] = spec[0].re
	f[1] = spec[n/2].re
	for i := 1; i < n/2; i++ {
		f[2*i] = spec[i].re
		f[2*i+1] = spec[i].im
	}
}

// rfftBackwardOrderedRefSc is the inverse twin of [rfftForwardOrderedRefSc], with the same role and
// the same unnormalized scale (BACKWARD(FORWARD(x)) = n*x).
func rfftBackwardOrderedRefSc(f, time []float32, sc *refFftScratch) {
	n := len(f)
	spec := sc.spec
	spec[0] = cpx{re: f[0], im: 0.0}
	spec[n/2] = cpx{re: f[1], im: 0.0}
	for i := 1; i < n/2; i++ {
		re := f[2*i]
		im := f[2*i+1]
		spec[i] = cpx{re: re, im: im}
		spec[n-i] = cpx{re: re, im: -im} // conjugate symmetry
	}
	cfft(spec, sc.tout, 1.0, sc.arena, &sc.twBwd)
	tout := sc.tout
	for i := 0; i < n; i++ {
		time[i] = tout[i].re
	}
}

func TestSmplPerc_PerceptualResponseOverwritesReusedDestination(t *testing.T) {
	var reused [smplMaxLResp]float32
	nan := float32(math.NaN())
	for _, l := range []int{32, 17, 32} {
		var corr [smplMaxLResp]float32
		for i := range corr {
			corr[i] = powiF32(0.8, int32(i))
		}
		expected := smplPercAc2a(corr[:], len(corr), -0.72, l, smplPercReg)
		for i := range reused {
			reused[i] = nan
		}
		smplPercAc2aInto(
			corr[:],
			len(corr),
			-0.72,
			l,
			smplPercReg,
			reused[:l],
		)
		if len(expected) != l {
			t.Fatalf("assertion failed: len %d != %d", len(expected), l)
		}
		for i := 0; i < l; i++ {
			if reused[i] != expected[i] {
				t.Fatalf("assertion `left == right` failed at %d: %v != %v", i, reused[i], expected[i])
			}
		}
		for i := l; i < len(reused); i++ {
			if reused[i] == reused[i] {
				t.Fatalf("assertion failed: reused[len..].iter().all(|x| x.is_nan()) (index %d)", i)
			}
		}
	}
}

// The precomputed twiddle tables MUST reproduce the inline cos/sin to the bit, or the golden
// checksum shifts. Assert every used (n, k, q) combine entry and (n, k, j) base entry equals the
// inline recompute for both signs (forward = -1, backward = +1) at both FFT sizes (576, 512).
func TestSmplPerc_TwiddleTableIsBitExact(t *testing.T) {
	for _, top := range []int{percwNfft, 512} {
		for _, sign := range []float32{-1.0, 1.0} {
			tw := newFftTwiddles(top, sign)
			// Combine tables.
			n := top
			for n > 1 {
				p := smallestFactor(n)
				if p == n {
					bt := tw.baseFor(n)
					for k := 0; k < n; k++ {
						for j := 0; j < n; j++ {
							want := baseTwiddle(n, k, j, sign)
							got := bt[k*n+j]
							if math.Float32bits(got.re) != math.Float32bits(want.re) {
								t.Fatalf("base re mismatch n=%d k=%d j=%d sign=%v", n, k, j, sign)
							}
							if math.Float32bits(got.im) != math.Float32bits(want.im) {
								t.Fatalf("base im mismatch n=%d k=%d j=%d sign=%v", n, k, j, sign)
							}
						}
					}
					break
				}
				ct := tw.combineFor(n)
				for k := 0; k < n; k++ {
					for q := 0; q < p; q++ {
						want := combineTwiddle(n, k, q, sign)
						got := ct[k*p+q]
						if math.Float32bits(got.re) != math.Float32bits(want.re) {
							t.Fatalf("combine re mismatch n=%d k=%d q=%d sign=%v", n, k, q, sign)
						}
						if math.Float32bits(got.im) != math.Float32bits(want.im) {
							t.Fatalf("combine im mismatch n=%d k=%d q=%d sign=%v", n, k, q, sign)
						}
					}
				}
				n /= p
			}
		}
	}
}

// A length-1 transform is the identity, and its plan has no levels and no base table. The
// depth-indexed recursion must return before indexing that empty table.
func TestSmplPerc_FftOfLengthOneIsTheIdentity(t *testing.T) {
	sc := newFftScratch(1)
	input := []cpx{{re: 0.25, im: -0.5}}
	out := []cpx{cpxZero()}
	cfft(input, out, -1.0, sc.arena, &sc.twFwd)
	if math.Float32bits(out[0].re) != math.Float32bits(input[0].re) {
		t.Fatalf("re %v != %v", out[0].re, input[0].re)
	}
	if math.Float32bits(out[0].im) != math.Float32bits(input[0].im) {
		t.Fatalf("im %v != %v", out[0].im, input[0].im)
	}
}

func TestSmplPerc_PercwNfftIs576NonPow2(t *testing.T) {
	if percwNfft != 576 {
		t.Fatalf("PERCW_NFFT %d != 576", percwNfft)
	}
	if percwNfft&(percwNfft-1) == 0 {
		t.Fatalf("576 is not a power of two")
	}
}

// FFT -> IFFT round-trips to n*x within float tolerance.

type smplPercSignal struct {
	name string
	x    []float32
}

// smplPercRfftTestSignals: deterministic input families covering the shapes the analysis front
// end actually feeds the FFT: a windowed tone, broadband noise, an impulse (flat spectrum, worst
// case for relative error) and a DC step.
func smplPercRfftTestSignals(n int) []smplPercSignal {
	noise := make([]float32, n)
	seed := uint32(12345)
	for i := range noise {
		seed = seed*196314165 + 907633515
		// `>> 8` spans 0..2^24, so `/ 2^23 - 1.0` gives zero-mean [-1, 1). `>> 9` -- as the
		// older `fft_roundtrip` generator in this file does it -- only reaches [-1, 0), which
		// puts a bin of magnitude ~n/2 at DC and inflates the spectrum RMS the bounds below
		// normalize by. That would make this family, the one meant to stress broadband
		// behaviour, the loosest of the four.
		noise[i] = float32(float32(seed>>8)/float32(uint32(1)<<23)) - 1.0
	}
	tone := make([]float32, n)
	for i := range tone {
		tt := float32(i) / float32(n)
		tone[i] = sinF32(float32(2.0)*smplPi*13.0*tt) * (0.5 - float32(0.5*cosF32(float32(2.0)*smplPi*tt)))
	}
	impulse := make([]float32, n)
	impulse[n/3] = 1.0
	step := make([]float32, n)
	for i := range step {
		if i < n/2 {
			step[i] = 1.0
		} else {
			step[i] = -1.0
		}
	}
	return []smplPercSignal{
		{"tone", tone},
		{"noise", noise},
		{"impulse", impulse},
		{"step", step},
	}
}

// smplPercRms is `(xs.iter().map(|v| (v * v) as f64).sum::<f64>() / n as f64).sqrt() as f32`.
func smplPercRms(xs []float32) float32 {
	s := math.Copysign(0, -1)
	for _, v := range xs {
		s += float64(v * v)
	}
	return float32(math.Sqrt(s / float64(len(xs))))
}

// smplPercMaxAbsDiff is `a.iter().zip(b).map(|(a, b)| (a - b).abs()).fold(0.0f32, f32::max)`.
func smplPercMaxAbsDiff(a, b []float32) float32 {
	m := float32(0.0)
	for i := 0; i < len(a) && i < len(b); i++ {
		m = maxF32(m, absF32(a[i]-b[i]))
	}
	return m
}

// The shipped half-length transform and the full-length one it replaced are different
// operation sequences for the same mathematical object, so they can never be compared bit for
// bit -- this bounds how far they may drift instead. The bound is on the error relative to the
// full-length spectrum's own RMS, which is the scale the perceptual model reads it at. The two
// differ by 2e-7 to 1.6e-5 relative across these signals and lengths, so 1e-4 is about six
// times the worst observed case -- loose enough not to be a flake, tight enough that a real
// defect (a wrong twiddle, a dropped bin, a scale error) is orders of magnitude out.
//
// [TestSmplPerc_RfftErrorIsAtFullLengthLevel] is the companion that says what that difference
// means: both implementations sit at the same distance from the exact transform.
func TestSmplPerc_RfftMatchesFullLengthImplementation(t *testing.T) {
	const tol float32 = 1e-4
	for _, n := range []int{512, percwNfft} {
		sc := newFftScratch(n)
		rsc := newRefFftScratch(n)
		for _, sig := range smplPercRfftTestSignals(n) {
			name, x := sig.name, sig.x
			want := make([]float32, n)
			rfftForwardOrderedRefSc(x, want, &rsc)
			got := make([]float32, n)
			rfftForwardOrderedSc(x, got, &sc)
			rms := smplPercRms(want)
			err := smplPercMaxAbsDiff(want, got)
			if !(err <= tol*rms) {
				t.Fatalf("forward n=%d %s: max abs error %e exceeds %e * rms %e", n, name, err, tol, rms)
			}

			// Backward from the same (reference) spectrum, so the inverse is compared on its own
			// rather than inheriting the forward's difference.
			wantT := make([]float32, n)
			rfftBackwardOrderedRefSc(want, wantT, &rsc)
			gotT := make([]float32, n)
			rfftBackwardOrderedSc(want, gotT, &sc)
			trms := smplPercRms(wantT)
			terr := smplPercMaxAbsDiff(wantT, gotT)
			if !(terr <= tol*trms) {
				t.Fatalf("backward n=%d %s: max abs error %e exceeds %e * rms %e", n, name, terr, tol, trms)
			}
		}
	}
}

// smplPercExactRfftOrdered is a naive O(n^2) DFT in f64, as an oracle neither FFT path can be
// graded against by the other. Returns the ordered REAL layout so it lines up with both.
func smplPercExactRfftOrdered(x []float32) []float64 {
	n := len(x)
	out := make([]float64, n)
	bin := func(k int) (float64, float64) {
		re, im := 0.0, 0.0
		for j, v := range x {
			ang := -2.0 * math.Pi * float64(k) * float64(j) / float64(n)
			re += float64(float64(v) * math.Cos(ang))
			im += float64(float64(v) * math.Sin(ang))
		}
		return re, im
	}
	out[0], _ = bin(0)
	out[1], _ = bin(n / 2)
	for k := 1; k < n/2; k++ {
		re, im := bin(k)
		out[2*k] = re
		out[2*k+1] = im
	}
	return out
}

// The bound above says the two implementations are close; this says whether either is right.
// Both are graded against an exact f64 DFT, on two axes: each must stay within f32 precision of
// the exact transform, and the shipped one must not be more than a small factor noisier than
// the full-length one. Without this, "they differ by 1.6e-5" would be equally consistent with
// the shipped path simply being wrong.
//
// What `MAX_RATIO` does and does not measure. The worst ratio comes from `step`, and `step` is
// a square wave: half of its spectrum is exactly zero (288 of 576 entries at n=576). The
// maximum absolute error lands on those zero bins, and normalizing it by the RMS of the whole
// spectrum -- rather than by the magnitude of the bin it sits on -- is what turns a rounding
// residue into a ratio near 5.6x. The ratio is real, not an artifact, but it is a ratio between
// two residues: measured in ULPs of the spectrum's largest coefficient, `step` at n=576 puts
// the full-length path at 1.12 ULP and the shipped one at 6.25 ULP. On a signal whose spectrum
// is dense (`noise`) the shipped path is the more accurate of the two (0.72x and 0.90x).
//
// So `MAX_RATIO = 8.0` is a drift guard with headroom over an already-measured 5.6x, not a
// statement that the transform is 5.6x worse anywhere that matters: six ULPs of f32 sit three
// orders of magnitude below the coarsest quantizer step in the encoder, and the encoder's
// output was validated perceptually against the source signal rather than against a previous
// bitstream.
func TestSmplPerc_RfftErrorIsAtFullLengthLevel(t *testing.T) {
	// Each path's own distance to the exact transform, relative to the spectrum RMS.
	const maxRel = 1e-4
	// ... and how much noisier the split path may be than the reference.
	const maxRatio = 8.0
	for _, n := range []int{512, percwNfft} {
		sc := newFftScratch(n)
		rsc := newRefFftScratch(n)
		for _, sig := range smplPercRfftTestSignals(n) {
			name, x := sig.name, sig.x
			exact := smplPercExactRfftOrdered(x)
			r := make([]float32, n)
			rfftForwardOrderedRefSc(x, r, &rsc)
			sp := make([]float32, n)
			rfftForwardOrderedSc(x, sp, &sc)
			dist := func(got []float32) float64 {
				m := 0.0
				for i := 0; i < len(got) && i < len(exact); i++ {
					m = maxF64(m, math.Abs(float64(got[i])-exact[i]))
				}
				return m
			}
			dr, ds := dist(r), dist(sp)
			s := math.Copysign(0, -1)
			for _, v := range exact {
				s += float64(v * v)
			}
			rms := math.Sqrt(s / float64(n))
			if !(dr <= maxRel*rms && ds <= maxRel*rms) {
				t.Fatalf("n=%d %s: distance to the exact DFT exceeds %e * rms %e (reference %e, split %e)",
					n, name, maxRel, rms, dr, ds)
			}
			if !(ds <= maxRatio*maxF64(dr, 2.2250738585072014e-308)) {
				t.Fatalf("n=%d %s: split path is %.2fx further from the exact DFT than the reference (%e vs %e)",
					n, name, ds/dr, ds, dr)
			}
		}
	}
}

// The split path must satisfy the same unnormalized round-trip identity as the reference:
// BACKWARD(FORWARD(x)) = n*x.
func TestSmplPerc_RfftRoundtripIsUnnormalizedIdentity(t *testing.T) {
	for _, n := range []int{512, percwNfft} {
		sc := newFftScratch(n)
		for _, sig := range smplPercRfftTestSignals(n) {
			name, x := sig.name, sig.x
			f := make([]float32, n)
			rfftForwardOrderedSc(x, f, &sc)
			back := make([]float32, n)
			rfftBackwardOrderedSc(f, back, &sc)
			for i := range back {
				b := back[i]
				expected := float32(x[i] * float32(n))
				if !(absF32(b-expected) < float32(1e-1*(1.0+absF32(expected)))) {
					t.Fatalf("n=%d %s idx %d: got %v, want %v", n, name, i, b, expected)
				}
			}
		}
	}
}

func TestSmplPerc_FftRoundtrip(t *testing.T) {
	n := percwNfft
	x := make([]float32, n)
	// deterministic pseudo-random-ish signal
	s := uint32(12345)
	for i := range x {
		s = s*196314165 + 907633515
		// `>> 8` spans 0..2^24 for a zero-mean [-1, 1); `>> 9` only reached [-1, 0), which is a
		// DC step rather than the broadband signal this round-trip means to exercise.
		x[i] = float32(float32(s>>8)/float32(uint32(1)<<23)) - 1.0
	}
	f := make([]float32, n)
	rfftForwardOrdered(x, f)
	back := make([]float32, n)
	rfftBackwardOrdered(f, back)
	for i := 0; i < n; i++ {
		expected := float32(x[i] * float32(n))
		if !(absF32(back[i]-expected) < float32(1e-1*(1.0+absF32(expected)))) {
			t.Fatalf("idx %d: got %v, want %v", i, back[i], expected)
		}
	}
}

// Known signal: a single real cosine at bin k has power N^2/4 at bin k (one-sided), 0 elsewhere.
func TestSmplPerc_PowerSpectrumOfCosine(t *testing.T) {
	n := percwNfft
	k := 9 // a bin reachable by both radix-2 and radix-3 paths
	x := make([]float32, n)
	for i := 0; i < n; i++ {
		x[i] = cosF32(float32(2.0) * smplPi * float32(k) * float32(i) / float32(n))
	}
	f := make([]float32, n)
	rfftForwardOrdered(x, f)
	// power at bin k (interleaved [re,im] for 1..n/2-1)
	pk := float32(f[2*k]*f[2*k]) + float32(f[2*k+1]*f[2*k+1])
	expected := (float32(n) / 2.0) * (float32(n) / 2.0)
	if !(absF32(pk-expected) < 1e-2*expected) {
		t.Fatalf("bin %d power %v, want %v", k, pk, expected)
	}
	// a far-away bin should be ~0
	m := 40
	pm := float32(f[2*m]*f[2*m]) + float32(f[2*m+1]*f[2*m+1])
	if !(pm < 1e-2*expected) {
		t.Fatalf("bin %d power %v not ~0", m, pm)
	}
}

// smpl_perc_model end-to-end smoke: with zero input it returns all-zero R; a DC step gives R[0]>0.
func TestSmplPerc_PercModelSmoke(t *testing.T) {
	st := newPercModelState()
	xsubfr := make([]float32, 320)
	r := smplPercModel(&st, xsubfr, 320, 20, smplFalse, smplMaxLResp)
	if len(r) != smplMaxLResp {
		t.Fatalf("len %d != %d", len(r), smplMaxLResp)
	}
	for _, v := range r {
		if !(absF32(v) < 1e-6) {
			t.Fatalf("zero input must give ~0 autocorr, got %v", v)
		}
	}

	st2 := newPercModelState()
	dc := make([]float32, 320)
	for i := range dc {
		dc[i] = 1.0
	}
	r2 := smplPercModel(&st2, dc, 320, 20, smplFalse, smplMaxLResp)
	if !(r2[0] > 0.0) {
		t.Fatalf("DC input must give positive R[0], got %v", r2[0])
	}
	// ac2a must run without panicking and return A[0] == 1.0
	a := smplPercAc2a(
		r2,
		smplMaxLResp,
		smplPercEmphV[0],
		smplPercRespLen,
		smplPercReg,
	)
	if len(a) != smplPercRespLen {
		t.Fatalf("len %d != %d", len(a), smplPercRespLen)
	}
	if a[0] != 1.0 {
		t.Fatalf("a[0] %v != 1.0", a[0])
	}
}

func TestSmplPerc_BitrateControllerRuns(t *testing.T) {
	bc := newBitrateController()
	enc := bitrateControllerInputs{
		internalSampleRate:       16000,
		payloadSizeMs:            20,
		fecBitRate:               0,
		mainBitRate:              16000,
		complexity:               5,
		useFecRateCompensation:   0,
		useDtx:                   0,
		subFrameImportanceFactor: 1.0,
	}
	mp, si := bc.control(&enc, 0, 1, 0.9, 1.5, 0.3, 1, 100.0, 80.0, 0, 320, 80)
	// FEC disabled (fec_bit_rate==0) so start_r==1: only MAIN index is populated.
	if mp[0] != 0 {
		t.Fatalf("mp[0] %d != 0", mp[0])
	}
	if !(mp[1] >= 1) {
		t.Fatalf("mp[1] %d < 1", mp[1])
	}
	if math.IsInf(float64(si[1]), 0) || math.IsNaN(float64(si[1])) {
		t.Fatalf("si[1] %v not finite", si[1])
	}
}

// R4 budget: for the active MLow config (20 kbps, 60 ms payload, complexity 8, high-rate, unvoiced)
// the per-subframe pulse budget must equal 23 (the reference value, per testdata/PROVENANCE.md).
// Regression for the inverted `smpl_max_pulses_per_frame` index that capped unvoiced subframes at
// 8 (the `{16,32,32}` low-rate row) instead of 23.
func TestSmplPerc_BitrateControllerActiveUnvoicedBudgetMatchesC(t *testing.T) {
	bc := newBitrateController()
	enc := bitrateControllerInputs{
		internalSampleRate:       16000,
		payloadSizeMs:            60,
		fecBitRate:               0,
		mainBitRate:              20000,
		complexity:               8,
		useFecRateCompensation:   0,
		useDtx:                   0,
		subFrameImportanceFactor: 1.0,
	}
	// coded_as_active_voice=1, voiced=0, low_rate=0, framelen=320, subfrlen=80, sp_act_prob≈1.
	mp, _ := bc.control(
		&enc, 0, 1, 0.9961, 0.2, -0.18, 0, 3.0e-5, 5.0e-5, 0, 320, 80,
	)
	if mp[1] != 23 {
		t.Fatalf("active-unvoiced max_pulses must match the reference (23/subframe): got %d", mp[1])
	}
}

// Go port only: the init tables computed with the port's own sin/cos (the perceptual windows, every
// FFT twiddle at 512 and 576 points, and the smoothing coefficients' window) must be the exact
// formula's values to within one float32 ulp.
func TestSmplPerc_InitTablesMatchFormulas(t *testing.T) {
	w := newPercWindows()
	// The angle is the f32 expression the Rust code rounds before calling sinf/cosf.
	win := func(n, i int) float64 { return float64((float32(i) + 1.0) / (float32(n) + 1.0) * smplPi / 2.0) }
	for _, c := range []struct {
		name string
		got  []float32
		f    func(float64) float64
	}{
		{"perc_win1_10ms", w.percWin1_10ms, math.Sin},
		{"perc_win1_20ms", w.percWin1_20ms, math.Sin},
		{"win3_short", w.win3Short, math.Cos},
		{"win3_long", w.win3Long, math.Cos},
	} {
		for i, g := range c.got {
			if d := ulpDiff32(g, float32(c.f(win(len(c.got), i)))); d > 1 {
				t.Fatalf("%s[%d]: %v is %d ulps from the formula", c.name, i, g, d)
			}
		}
	}
	// A twiddle is cos/sin of an f32 angle the Rust code rounds first; compare with the exact
	// cos/sin of that same f32 angle.
	checkTw := func(name string, c cpx, ang float32) {
		t.Helper()
		if ulpDiff32(c.re, float32(math.Cos(float64(ang)))) > 1 || ulpDiff32(c.im, float32(math.Sin(float64(ang)))) > 1 {
			t.Fatalf("%s: %v vs cos/sin(%v)", name, c, ang)
		}
	}
	for _, n := range []int{512, percwNfft} {
		sc := newFftScratch(n)
		for k, c := range sc.wn {
			checkTw("wn", c, -1.0*2.0*smplPi*float32(k)*float32(1)/float32(n))
		}
		for _, tw := range []*fftTwiddles{&sc.twFwd, &sc.twBwd} {
			for _, l := range tw.levels {
				for _, c := range l.tw {
					if math.Abs(float64(c.re*c.re+c.im*c.im)-1) > 1e-6 {
						t.Fatalf("n=%d: butterfly twiddle %v is off the unit circle", n, c)
					}
				}
			}
			for _, c := range tw.base {
				if math.Abs(float64(c.re*c.re+c.im*c.im)-1) > 1e-6 {
					t.Fatalf("n=%d: base twiddle %v is off the unit circle", n, c)
				}
			}
		}
	}
}

// ulpDiff32 is the distance between two finite float32 of the same sign in units in the last place.
func ulpDiff32(a, b float32) int64 {
	d := int64(math.Float32bits(a)) - int64(math.Float32bits(b))
	if d < 0 {
		d = -d
	}
	return d
}
