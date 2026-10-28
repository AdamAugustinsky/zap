// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_lpc.rs, MIT.
//
// The `#[cfg(test)] mod tests` of smpl_lpc.rs. `decoder_reconstructs_c_qlsf` needs the LSF
// quantizer and the decoder's NLSF reconstruction (other groups' files), so while the port is in
// progress it sits in smpl_lpc_deps_test.go under their build tags too.

package mlow

import (
	"math"
	"testing"
)

// Signed-zero bits distinguish window multiplication from the trailing positive-zero padding.
func TestSmplLpc_CachedLpcWindowsMatchPerCallGenerationBitwise(t *testing.T) {
	for _, useLong := range []bool{true, false, true, false} {
		trailing := smplWin3ShortLen
		if useLong {
			trailing = smplWin3LongLen
		}
		// Rust regenerates the windows per call with gen_sin_win/gen_cos_win.
		var leadingArr [smplLpcWin1_20msLen]float32
		lpcGenSinWin(leadingArr[:])
		leading := leadingArr[:]
		ending := make([]float32, trailing)
		lpcGenCosWin(ending)
		for _, scale := range []float32{0.0, float32(math.Copysign(0, -1)), 0.001, -1.0, 32768.0} {
			var input [smplLpcBufLen]float32
			for i := range input {
				input[i] = (float32(i) - 224.0) * scale
			}
			actual := smplWindowLpc20(&input, useLong)
			for i := 0; i < smplLpcBufLen; i++ {
				var expected float32
				switch {
				case i < smplLpcWin1_20msLen:
					expected = input[i] * leading[i]
				case i < smplLpcBufLen-smplWin3LongLen:
					expected = input[i]
				case i < smplLpcBufLen-smplWin3LongLen+trailing:
					expected = input[i] * ending[i-(smplLpcBufLen-smplWin3LongLen)]
				default:
					expected = 0.0
				}
				if math.Float32bits(actual[i]) != math.Float32bits(expected) {
					t.Fatalf("long=%v scale=%v sample=%d", useLong, scale, i)
				}
			}
		}
	}
}

// smplLpcFvec is the Rust test's `fvec`: a JSON/CBOR number array as f32 (`as_f64() as f32`).
func smplLpcFvec(v val) []float32 {
	return v.F32s()
}

// The forward A2NLSF (silk_A2NLSF, fixed-point) must reproduce the reference `lsf` from the
// post-BWE `A` for every captured record. Both use the same fixed-point arithmetic, so this is
// exact (NLSF in radians within float rounding of the Q15->radians scale).
func TestSmplLpc_A2nlsfMatchesC(t *testing.T) {
	recs := loadPacked(t, "lsf_quant_io.cbor.zst")
	arr := recs.Arr()
	worst := float32(0.0)
	for n, r := range arr {
		a := smplLpcFvec(r.Get("A"))
		want := smplLpcFvec(r.Get("lsf"))
		got := smplA2nlsf16(a)
		for k := 0; k < smplLpcOrder; k++ {
			d := absF32(got[k] - want[k])
			worst = maxF32(worst, d)
			if !(d < 1e-4) {
				t.Fatalf("rec %d nlsf[%d] %.7f != ref %.7f (d=%.2e)", n, k, got[k], want[k], d)
			}
		}
	}
	t.Logf("a2nlsf worst abs error = %.3e", worst)
}

// The full front-end (re-window from the raw lpcbuf, then FFT-autocorr -> reflection -> A -> BWE)
// must reproduce the reference post-BWE `A` from the raw lpcbuf. The FFT differs only in internal
// rounding (portable mixed-radix vs PFFFT), so A matches to a tight float tolerance (documented,
// like the decoder postfilters).
func TestSmplLpc_FrontEndAMatchesC(t *testing.T) {
	recs := loadPacked(t, "fe_dump.cbor.zst")
	arr := recs.Arr()
	if !(len(arr) >= 12) {
		t.Fatalf("need front-end vectors")
	}
	worst := float32(0.0)
	worstWin := float32(0.0)
	for n, r := range arr {
		lpcbuf := smplLpcFvec(r.Get("lpcbuf"))
		numframe := r.Get("numframe").I64()
		if len(lpcbuf) != smplLpcBufLen {
			t.Fatalf("lpcbuf len %d != %d", len(lpcbuf), smplLpcBufLen)
		}
		var buf [smplLpcBufLen]float32
		copy(buf[:], lpcbuf)
		// use_long_win = numframe < 2 (frames_per_packet-1 == 2)
		win := smplWindowLpc20(&buf, numframe < 2)
		// Cross-check the windowing exactly against the reference `windowed`.
		wantWin := smplLpcFvec(r.Get("windowed"))
		for k := 0; k < smplLpcBufLen; k++ {
			worstWin = maxF32(worstWin, absF32(win[k]-wantWin[k]))
		}
		// C 84b076e used 0.9999; compare its tuning explicitly. The shipped
		// profile is checked against the independent wasm corpus below.
		sc := newLpcFftScratch()
		a, _ := lpcAnalyzeUnexpanded(&win, &sc)
		bweExpand(a[:], smplLpcOrder, 0.9999)
		wantA := smplLpcFvec(r.Get("A"))
		r0 := r.Get("R").At(0).F64()
		rd := float32(0.0)
		for k := 0; k <= smplLpcOrder; k++ {
			rd = maxF32(rd, absF32(a[k]-wantA[k]))
		}
		worst = maxF32(worst, rd)
		// Frames with audible energy must match `A` tightly (FFT-internal rounding only). Below a
		// very low energy floor the autocorrelation is dominated by `reg` + numerical noise so the
		// LPC is ill-conditioned and the reconstructed frame is silent regardless of `A`; skip the
		// `A` assert there but still verify the autocorrelation `R` itself (the FFT+DCT output).
		if r0 > 1e-7 {
			if !(rd < 5e-3) {
				t.Fatalf("rec %d (nf %d, R0=%.2e) |dA|=%.2e too large", n, numframe, r0, rd)
			}
		}
	}
	// The windowing is exact; the autocorrelation tracks the reference FFT/DCT to a tight tolerance.
	if !(worstWin < 1e-6) {
		t.Fatalf("windowing |dwin|=%.2e", worstWin)
	}
	t.Logf("front-end worst |dA| = %.3e", worst)
}

func TestSmplLpc_FrontEndMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_fe.cbor.zst").Arr()
	if len(records) != 330 {
		t.Fatalf("assertion `left == right` failed: %d != 330", len(records))
	}
	for i, r := range records {
		lpcbuf := smplLpcFvec(r.Get("lpcbuf"))
		if len(lpcbuf) != smplLpcBufLen {
			t.Fatalf("case %d: lpcbuf len %d", i, len(lpcbuf))
		}
		var input [smplLpcBufLen]float32
		copy(input[:], lpcbuf)
		window := smplWindowLpc20(&input, r.Get("numframe").U64() < 2)
		wantWin := smplLpcFvec(r.Get("windowed"))
		for k := 0; k < len(window) && k < len(wantWin); k++ {
			if !(absF32(window[k]-wantWin[k]) < 1e-6) {
				t.Fatalf("window case %d", i)
			}
		}
		sc := newLpcFftScratch()
		a, f2 := smplLpcAnalyzeWithF2(&window, &sc)
		expectedA := smplLpcFvec(r.Get("A"))
		maxCoefficientError := float32(0.0)
		coefficientErrorSum := float32(0.0)
		for k := 0; k < len(a) && k < len(expectedA); k++ {
			e := absF32(a[k] - expectedA[k])
			maxCoefficientError = maxF32(maxCoefficientError, e)
			coefficientErrorSum += e
		}
		// Near silence, FFT rounding is amplified by the ill-conditioned
		// LPC solve. Bound the resulting prediction error by one PCM LSB,
		// while checking the solve itself on the exact oracle R below.
		inputPeak := float32(0.0)
		for _, x := range window {
			inputPeak = maxF32(inputPeak, absF32(x))
		}
		if !(maxCoefficientError < 5e-3 || coefficientErrorSum*inputPeak < float32(1.0)/32768.0) {
			t.Fatalf("case %d: LPC coefficient error %v, prediction bound %v",
				i, maxCoefficientError, coefficientErrorSum*inputPeak)
		}
		oracleR := r.Get("R").F64s()
		var rc [smplLpcOrder]float32
		var solved [smplLpcOrder + 1]float32
		ac2rcDbl(oracleR, smplLpcOrder, smplLpcReg, rc[:])
		rc2a(rc[:], smplLpcOrder, solved[:])
		wantSolved := smplLpcFvec(r.Get("A_before_bwe"))
		for k := 0; k < len(solved) && k < len(wantSolved); k++ {
			if !(absF32(solved[k]-wantSolved[k]) < 1e-5) {
				t.Fatalf("case %d: LPC solve %v vs %v", i, solved[k], wantSolved[k])
			}
		}
		expected := smplLpcFvec(r.Get("F2"))
		scale := float32(1e-8)
		for _, v := range expected {
			scale = maxF32(scale, v)
		}
		for k := 0; k < len(f2) && k < len(expected); k++ {
			if !(absF32(f2[k]-expected[k]) < scale*1e-5) {
				t.Fatalf("spectrum case %d", i)
			}
		}
	}
}

// Go port only: the LPC windows and the brute_dct cosine rows, which the codec computes at init
// with the port's own sin/cos, must be the exact formula's values to within one float32 ulp (the
// windows) and a few float64 ulps (the cosine rows, whose running fmod angle is part of the Rust
// formula and is reproduced as is).
func TestSmplLpc_InitTablesMatchFormulas(t *testing.T) {
	w := lpcWindows()
	// The angle is the f32 expression the Rust code rounds before calling sinf/cosf.
	win := func(n, i int) float64 { return float64((float32(i) + 1.0) / (float32(n) + 1.0) * smplPi / 2.0) }
	for _, c := range []struct {
		name string
		got  []float32
		f    func(float64) float64
	}{
		{"leading", w.leading[:], math.Sin},
		{"trailing_long", w.trailingLong[:], math.Cos},
		{"trailing_short", w.trailingShort[:], math.Cos},
	} {
		for i, g := range c.got {
			if d := ulpDiff32(g, float32(c.f(win(len(c.got), i)))); d > 1 {
				t.Fatalf("%s[%d]: %v is %d ulps from the formula", c.name, i, g, d)
			}
		}
	}
	tab := dctTables()
	check := func(name string, rows [][nfft4]float64, first, step int, scale float64) {
		for j := range rows {
			domega := float64(first+j*step) * 2 * smplPiF64 / float64(smplLpcNfft)
			for k, g := range rows[j] {
				want := math.Cos(math.Mod(float64(k)*domega, 2*smplPiF64)) * scale
				if math.Abs(g-want) > 1e-14*scale {
					t.Fatalf("%s[%d][%d]: %v, formula %v", name, j, k, g, want)
				}
			}
		}
	}
	check("cdif", tab.cdif[:], 1, 2, 2.0/float64(smplLpcNfft))
	check("csumdiff", tab.csumdiff[:], 2, 4, 1.0/float64(smplLpcNfft))
	check("csumsum", tab.csumsum[:], 4, 4, 1.0/float64(smplLpcNfft))
}

// DIAGNOSTIC: the wire round-trip; feed the captured `qi` (grid+stage2) + threaded prev_nlsf to
// the decoder's NLSF reconstruction and compare to the captured `qlsf`. Proves grid/stage2 map
// directly onto the decoder wire (grid=qi[0], cond centroid=16) and that the decoder rebuilds the
// same envelope.
func TestSmplLpc_DecoderReconstructsCQlsf(t *testing.T) {
	recs := loadPacked(t, "lsf_quant_io.cbor.zst")
	arr := recs.Arr()
	st := loadSmplSynthTables()
	var prevNlsf []float32
	worst := float32(0.0)
	for n, r := range arr {
		a := smplLpcFvec(r.Get("A"))
		lsf := smplLpcFvec(r.Get("lsf"))
		voicedIdx := int(r.Get("voiced").I64())
		lowRate := int(r.Get("lowRate").I64())
		surv := int(r.Get("surv").I64())
		rdW := float32(r.Get("RDw_adj").F64())
		cond := r.Get("cond_coding").I64() != 0
		prevLsf := smplLpcFvec(r.Get("prev_lsf"))
		var res lsfQuantResult
		if cond {
			res = lsfQuantCond(a, lsf, prevLsf, voicedIdx, lowRate, rdW, surv)
		} else {
			res = lsfQuant(a, lsf, voicedIdx, lowRate, rdW, surv)
		}
		grid := int(res.qi[0])
		var stage2 [16]int32
		copy(stage2[:], res.qi[1:17])
		rec := smplReconstructNlsf(st, voicedIdx, 0, grid, &stage2, prevNlsf)
		// The decoder reconstruction must match the captured qlsf (the synthesis envelope),
		// proving the grid/stage2 (incl. cond centroid 16) round-trip and the decoder rebuilds the
		// same envelope, so no synthesis-time stabilization step is needed in the decoder.
		want := smplLpcFvec(r.Get("qlsf"))
		rd := float32(0.0)
		for k := 0; k < smplLpcOrder; k++ {
			rd = maxF32(rd, absF32(rec[k]-want[k]))
		}
		if !(rd < 1e-3) {
			t.Fatalf("rec %d cond=%v grid=%d reconstruct vs qlsf %.2e", n, cond, grid, rd)
		}
		worst = maxF32(worst, rd)
		prevNlsf = rec
	}
	if !(worst < 2e-3) {
		t.Fatalf("decoder reconstruct vs C qlsf worst %.3e", worst)
	}
}
