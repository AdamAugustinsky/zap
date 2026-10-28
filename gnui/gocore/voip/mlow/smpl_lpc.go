// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_lpc.rs, MIT.
//
// MLow LPC ANALYSIS FRONT-END (encoder side): `smpl_window` / `smpl_lpc` / `smpl_bwe_expand` plus
// `smpl_A2NLSF_16` (wrapping silk `silk_A2NLSF`). It turns a 20 ms windowed LPC buffer into the
// post-bandwidth-expansion LPC coefficients `A[0..16]` and their analysis NLSF, which feed the
// (bit-exact) LSF quantizer.
//
// The autocorrelation is derived NOT by time-domain lags but by an FFT of the windowed signal
// (512-pt real FFT) -> power spectrum -> a cosine transform (`brute_dct`) -> reflection coefficients
// (`smpl_ac2rc`) -> `A` (`smpl_rc2a`), then bandwidth expansion (`A[i] *= 0.9999^i`). A time-domain
// autocorr + Gaussian lag window would produce wrong formants and mute the outbound audio.
//
// Float tolerance: the reference uses PFFFT; our portable mixed-radix FFT (shared with `smpl_perc`)
// differs only in FFT-internal rounding, so the resulting `A`/NLSF match to a tight float tolerance,
// not bit-for-bit (documented like the decoder postfilters).
//
// Go port notes: SMPL_LPC_ORDER, SMPL_WIN3_LONG_LEN, SMPL_WIN3_SHORT_LEN and SILK_INT16_MAX are the
// shared constants; SMPL_PI (the reference's truncated literal `3.1415926535897f`) is the shared
// smplPi, the same f32. The Rust file `include!`s silk_lsf_cos_tab.rs, ported as silk_lsf_cos_tab.go.

package mlow

import (
	"math"
	"sync"
)

const smplLpcNfft = 512

// The reference uses the exact truncated literal (`SMPL_PI 3.1415926535897f`); smplPi is that f32.
// smplPiF64 is it widened to f64 (typed-constant conversion keeps the f32 value).
const smplPiF64 float64 = float64(smplPi)
const smplLpcReg float32 = 5e-7

// J#10736 expands the LPC coefficients with f32 bits 0x3f7f9db2.
const smplLpcBwe float32 = 0.9985

const smplLpcWin1_20msLen = 264

// smplLpcBufLen is the 20 ms LPC analysis buffer length (`lpcbuf_len` for 60 ms packets).
const smplLpcBufLen = 448

// window generation (gen_sin_win / gen_cos_win)
//
// The Rust versions are const-generic `[f32; N]` builders; here they fill the slice they are
// given, N = len(w).

// lpcGenSinWin is `gen_sin_win`: `win[i] = sinf((i+1)/(N+1) * PI/2)`.
func lpcGenSinWin(w []float32) {
	n := len(w)
	for i := range w {
		w[i] = sinF32((float32(i) + 1.0) / (float32(n) + 1.0) * smplPi / 2.0)
	}
}

// lpcGenCosWin is `gen_cos_win`: `win[i] = cosf((i+1)/(N+1) * PI/2)`.
func lpcGenCosWin(w []float32) {
	n := len(w)
	for i := range w {
		w[i] = cosF32((float32(i) + 1.0) / (float32(n) + 1.0) * smplPi / 2.0)
	}
}

// lpcWindowsT: the short taper has its own denominator; it is not a prefix of the long taper.
type lpcWindowsT struct {
	leading       [smplLpcWin1_20msLen]float32
	trailingLong  [smplWin3LongLen]float32
	trailingShort [smplWin3ShortLen]float32
}

var lpcWindowsOnce = sync.OnceValue(func() *lpcWindowsT {
	w := &lpcWindowsT{}
	lpcGenSinWin(w.leading[:])
	lpcGenCosWin(w.trailingLong[:])
	lpcGenCosWin(w.trailingShort[:])
	return w
})

// lpcWindows shares immutable windows across streams, preserving the original f32 formula on each
// target.
func lpcWindows() *lpcWindowsT { return lpcWindowsOnce() }

// smplWindowLpc20 is `smpl_window` for the LPC 20 ms path (`use_lpc_win=true`, `frame_ms=20`,
// `len=448`). `useLongWin` selects the 64-tap vs 32-tap trailing cosine window; the short variant
// zeros the trailing 32 samples. Produces the windowed buffer the FFT consumes.
func smplWindowLpc20(input *[smplLpcBufLen]float32, useLongWin bool) [smplLpcBufLen]float32 {
	windows := lpcWindows()
	win1 := &windows.leading
	var win3 []float32
	if useLongWin {
		win3 = windows.trailingLong[:]
	} else {
		win3 = windows.trailingShort[:]
	}
	win3len := len(win3)
	var out [smplLpcBufLen]float32
	for i := 0; i < smplLpcWin1_20msLen; i++ {
		out[i] = input[i] * win1[i]
	}
	// Untouched middle section copies straight through (no window taper).
	mid := smplLpcBufLen - smplLpcWin1_20msLen - smplWin3LongLen
	copy(out[smplLpcWin1_20msLen:smplLpcWin1_20msLen+mid], input[smplLpcWin1_20msLen:smplLpcWin1_20msLen+mid])
	base := smplLpcBufLen - smplWin3LongLen
	for i := 0; i < win3len; i++ {
		out[base+i] = input[base+i] * win3[i]
	}
	if !useLongWin {
		for i := base + smplWin3ShortLen; i < base+smplWin3LongLen; i++ {
			out[i] = 0.0
		}
	}
	return out
}

// brute_dct cosine tables (gen_cos_row)

const nfft4 = smplLpcNfft / 4 // 128

// lpcRemEuclidF64 is Rust's f64::rem_euclid.
func lpcRemEuclidF64(a, b float64) float64 {
	r := math.Mod(a, b)
	if r < 0.0 {
		return r + math.Abs(b)
	}
	return r
}

// genCosRow is `gen_cos_row`: `row[k] = cos(omega)*scale` with `omega` accumulated via
// `fmod(omega+domega, 2pi)` in f64 (NOT `cos(k*domega)`; the running fmod differs in float
// rounding).
func genCosRow(domega, scale float64) [nfft4]float64 {
	var row [nfft4]float64
	omega := 0.0
	twoPi := 2.0 * smplPiF64
	for k := 0; k < nfft4; k++ {
		row[k] = float64(dmCos(omega) * scale)
		omega = lpcRemEuclidF64(omega+domega, twoPi)
	}
	return row
}

type dctTablesT struct {
	cdif     [smplLpcOrder / 2][nfft4]float64 // 8 rows
	csumdiff [smplLpcOrder / 4][nfft4]float64 // 4 rows
	csumsum  [smplLpcOrder / 4][nfft4]float64 // 4 rows
}

// buildDctTables is the Rust init: every row from genCosRow.
func buildDctTables() *dctTablesT {
	twoPi := 2.0 * smplPiF64
	t := &dctTablesT{}
	for j := 0; j < smplLpcOrder/2; j++ {
		t.cdif[j] = genCosRow(
			float64(1+j*2)*twoPi/float64(smplLpcNfft),
			2.0/float64(smplLpcNfft),
		)
	}
	for j := 0; j < smplLpcOrder/4; j++ {
		t.csumdiff[j] = genCosRow(
			float64(2+j*4)*twoPi/float64(smplLpcNfft),
			1.0/float64(smplLpcNfft),
		)
	}
	for j := 0; j < smplLpcOrder/4; j++ {
		t.csumsum[j] = genCosRow(
			float64(4+j*4)*twoPi/float64(smplLpcNfft),
			1.0/float64(smplLpcNfft),
		)
	}
	return t
}

// bruteDct is `brute_dct`: autocorrelation `R[0..16]` from the power spectrum `F2[0..256]` via the
// precomputed cosine sums. All accumulation in f64 (F2 is cast to double first).
func bruteDct(t *dctTablesT, f2 []float64, order int, r []float64) {
	half := smplLpcNfft / 2 // 256
	f2sum := 0.0
	var f2Dif [nfft4]float64
	var f2Sumsum [nfft4]float64
	var f2Sumdif [nfft4]float64
	for n := 0; n < nfft4; n++ {
		f2sum += f2[n] + f2[nfft4+n]
		f2Dif[n] = f2[n] - f2[half-n]
		f2Sumsum[n] = f2[n] + f2[half-n] + f2[nfft4+n] + f2[nfft4-n]
		f2Sumdif[n] = f2[n] + f2[half-n] - f2[nfft4+n] - f2[nfft4-n]
	}
	f2Dif[0] *= 0.5
	r[0] = (float64(2.0*f2sum) - f2[0] + f2[half]) / float64(smplLpcNfft)

	for j := 0; j < order/2; j++ {
		rtmp := 0.0
		row := &t.cdif[j]
		for k := 0; k < nfft4; k++ {
			rtmp += float64(row[k] * f2Dif[k])
		}
		r[1+j*2] = rtmp
	}
	for j := 0; j < order/4; j++ {
		rtmp := 0.0
		row := &t.csumdiff[j]
		for k := 0; k < nfft4; k++ {
			rtmp += float64(row[k] * f2Sumdif[k])
		}
		r[2+j*4] = rtmp
	}
	for j := 0; j < order/4; j++ {
		rtmp := 0.0
		row := &t.csumsum[j]
		for k := 0; k < nfft4; k++ {
			rtmp += float64(row[k] * f2Sumsum[k])
		}
		r[4+j*4] = rtmp
	}
}

// ac2rcDbl is `smpl_ac2rc_dbl`: autocorrelation `R[0..order]` -> reflection coefficients (Schur),
// with `C0[0] *= (1 + reg)`. `rc[k]` is truncated to f32 each step (load-bearing for
// bit-faithfulness).
func ac2rcDbl(corr []float64, order int, reg float32, rc []float32) {
	var c0 [smplLpcOrder + 1]float64
	var c1 [smplLpcOrder + 1]float64
	copy(c0[:order+1], corr[:order+1])
	// J#10797 promotes reg before adding 1. Rounding that sum in f32
	// changes the LPC solve on quiet, highly correlated input.
	c0[0] *= 1.0 + float64(reg)
	copy(c1[:order+1], c0[:order+1])
	for i := 0; i < order && i < len(rc); i++ {
		rc[i] = 0.0
	}
	for k := 0; k < order; k++ {
		if c0[k+1] > c1[0] {
			rc[k] = -1.0
			break
		}
		if c0[k+1] < -c1[0] {
			rc[k] = 1.0
			break
		}
		if c1[0] == 0.0 {
			break
		}
		rcTmp := -c0[k+1] / c1[0]
		rc[k] = float32(rcTmp)
		for n := 0; n < order-k; n++ {
			ctmp1 := c0[n+k+1]
			ctmp2 := c1[n]
			c0[n+k+1] = ctmp1 + float64(ctmp2*rcTmp)
			c1[n] = ctmp2 + float64(ctmp1*rcTmp)
		}
	}
}

// rc2a is `smpl_rc2a`: reflection coefficients -> monic LPC `A[0..order]` (`A[0]=1`). All f32.
func rc2a(rc []float32, order int, a []float32) {
	for i := 1; i < order+1 && i < len(a); i++ {
		a[i] = 0.0
	}
	a[0] = 1.0
	for k := 0; k < order; k++ {
		rcTmp := rc[k]
		for n := 0; n < (k+1)/2; n++ {
			tmp1 := a[n+1]
			tmp2 := a[k-n]
			a[n+1] = tmp1 + float32(tmp2*rcTmp)
			a[k-n] = tmp2 + float32(tmp1*rcTmp)
		}
		a[k+1] = rcTmp
	}
}

// bweExpand is `smpl_bwe_expand` (the `bwe>0` path): `A[i] *= bwe^i` for i in 1..=order.
func bweExpand(a []float32, order int, bwe float32) {
	c := bwe
	for i := 1; i < order+1; i++ {
		a[i] *= c
		c *= bwe
	}
}

// smplFLen is the number of power-spectrum bins `smpl_pitch` / `smpl_get_signal_mode` read
// (`SMPL_F_LEN`).
const smplFLen = smplLpcNfft/2 + 1

// newLpcFftScratch is the reusable scratch for the fixed 512-pt LPC-analysis FFT. Held in the
// encoder state so the twiddle tables are built once per stream instead of rebuilt on every
// internal frame.
func newLpcFftScratch() fftScratch {
	return newFftScratch(smplLpcNfft)
}

var dctTablesOnce = sync.OnceValue(buildDctTables)

// dctTables: cached DCT cos tables, deterministic in the fixed NFFT/order, built once instead of
// recomputing ~2k cos() per LPC analysis.
func dctTables() *dctTablesT { return dctTablesOnce() }

// smplLpcAnalyzeWithF2 is the full `smpl_lpc` + `smpl_bwe_expand` over a 20 ms windowed LPC buffer
// (`SMPL_LPC_BUF_LEN`), returning the post-BWE monic LPC `A[0..16]` (`A[0]=1`) and the power
// spectrum `F2[0..256]` (`lpcbuf_F2`) that `smpl_pitch` (harm_strength) and `smpl_get_signal_mode`
// (spectral tilt) consume.
func smplLpcAnalyzeWithF2(windowed *[smplLpcBufLen]float32, fft *fftScratch) ([smplLpcOrder + 1]float32, [smplFLen]float32) {
	a, f2 := lpcAnalyzeUnexpanded(windowed, fft)
	bweExpand(a[:], smplLpcOrder, smplLpcBwe)
	return a, f2
}

func lpcAnalyzeUnexpanded(windowed *[smplLpcBufLen]float32, fft *fftScratch) ([smplLpcOrder + 1]float32, [smplFLen]float32) {
	// Zero-pad to NFFT and forward real FFT (pffft ordered layout).
	var xbuf [smplLpcNfft]float32
	copy(xbuf[:smplLpcBufLen], windowed[:])
	var f [smplLpcNfft]float32
	rfftForwardOrderedSc(xbuf[:], f[:], fft)

	// Power spectrum F2[0..256] (float), pffft packs DC in F[0], Nyquist in F[1].
	var f2 [smplFLen]float32
	f2[0] = f[0] * f[0]
	f2[smplLpcNfft/2] = f[1] * f[1]
	for i := 1; i < smplLpcNfft/2; i++ {
		f2[i] = float32(f[2*i]*f[2*i]) + float32(f[2*i+1]*f[2*i+1])
	}
	// Rust collects this into a Vec<f64>; a fixed array is the same values without the allocation.
	var f2d [smplFLen]float64
	for i, v := range f2 {
		f2d[i] = float64(v)
	}

	var r [smplLpcOrder + 1]float64
	bruteDct(dctTables(), f2d[:], smplLpcOrder, r[:])

	var rc [smplLpcOrder]float32
	ac2rcDbl(r[:], smplLpcOrder, smplLpcReg, rc[:])
	var a [smplLpcOrder + 1]float32
	rc2a(rc[:], smplLpcOrder, a[:])
	return a, f2
}

// LPC interpolation (smpl_lpc_interpol)

const smplSubfrs = 4

// smplLsfInterpol4Tbl is `smpl_lsf_interpol_4[idx]`: per-subframe interpolation weights (idx 0 and
// the alternative idx 1 that `lsf_interpol_search` may pick when it lowers the residual energy).
var smplLsfInterpol4Tbl = [2][smplSubfrs]float32{{0.55, 0.88, 1.0, 1.0}, {0.3, 0.65, 0.95, 1.0}}

const maxRcStable float32 = 0.9995

// lpcIsStable is `smpl_lpc_is_stable`: true if the monic LPC `A[0..16]` (`A[0]=1`) is a stable
// all-pole filter.
func lpcIsStable(a []float32) bool {
	order := smplLpcOrder
	if a[order]*a[order] > maxRcStable {
		return false
	}
	var a0 [smplLpcOrder]float64
	for i := 0; i < order; i++ {
		a0[i] = float64(a[i+1])
	}
	var a1 [smplLpcOrder]float64
	m := order - 1
	for {
		den := 1.0 - float64(a0[m]*a0[m])
		if den == 0.0 {
			return false
		}
		inv := 1.0 / den
		for k := 0; k < m; k++ {
			a1[k] = (a0[k] - float64(a0[m]*a0[m-k-1])) * inv
		}
		if a1[m-1]*a1[m-1] > float64(maxRcStable) {
			return false
		}
		if m == 1 {
			return true
		}
		m--
		den = 1.0 - float64(a1[m]*a1[m])
		if den == 0.0 {
			return false
		}
		inv = 1.0 / den
		for k := 0; k < m; k++ {
			a0[k] = (a1[k] - float64(a1[m]*a1[m-k-1])) * inv
		}
		if a0[m-1]*a0[m-1] > float64(maxRcStable) {
			return false
		}
		if m == 1 {
			return true
		}
		m--
	}
}

// lpcStabilize is `smpl_lpc_stabilize`: bandwidth-expand until stable.
func lpcStabilize(a []float32) {
	if lpcIsStable(a) {
		return
	}
	iter := int32(0)
	for {
		iter++
		bweExpand(a, smplLpcOrder, 1.0-float32(float32(iter)*0.001))
		if lpcIsStable(a) {
			return
		}
	}
}

// smplLpcInterpol: per-subframe interpolated LPC predcoefs (`smpl_lpc_interpol`, lsf_interpol_idx
// 0), and the updated `prev_lsf` (the last subframe's interpolated NLSF, carried to the next
// internal frame). `nlsf2a` is the decoder's NLSF->A. On a reset (`prevLsf` all zero) `lsf` is
// copied into `prev_lsf`.
//
// Go port note: Rust takes the NLSF->A as an `impl Fn` parameter, and every caller passes
// `smpl_nlsf2a`. Monomorphised, that is a direct call; through a Go func value it would be an
// indirect one that forces the scratch below onto the heap on every call. So the parameter is
// dropped and the body calls [smplNlsf2aInto] directly.
func smplLpcInterpol(lsf, prevLsf []float32) ([smplSubfrs][smplLpcOrder + 1]float32, [smplLpcOrder]float32) {
	return smplLpcInterpolIdx(lsf, prevLsf, 0)
}

// smplLpcInterpolIdx is as [smplLpcInterpol] but for an explicit `interpolIdx` (the
// `smpl_lsf_interpol_4` row).
func smplLpcInterpolIdx(lsf, prevLsf []float32, interpolIdx int) ([smplSubfrs][smplLpcOrder + 1]float32, [smplLpcOrder]float32) {
	interp := &smplLsfInterpol4Tbl[min(interpolIdx, 1)]
	var prev [smplLpcOrder]float32
	if len(prevLsf) == smplLpcOrder && prevLsf[smplLpcOrder-1] != 0.0 {
		copy(prev[:], prevLsf)
	} else {
		copy(prev[:], lsf[:smplLpcOrder])
	}
	var predcoefs [smplSubfrs][smplLpcOrder + 1]float32
	var ilsf [smplLpcOrder]float32
	for j := 0; j < smplSubfrs; j++ {
		w := interp[j]
		if w == 1.0 {
			copy(ilsf[:], lsf[:smplLpcOrder])
		} else {
			for k := 0; k < smplLpcOrder; k++ {
				ilsf[k] = float32((1.0-w)*prev[k]) + float32(w*lsf[k])
			}
		}
		var aBuf [smplLpcOrder + 1]float32
		a := smplNlsf2aInto(aBuf[:], ilsf[:])
		var pc [smplLpcOrder + 1]float32
		for i := 0; i < len(a) && i < smplLpcOrder+1; i++ {
			pc[i] = a[i]
		}
		pc[0] = 1.0
		lpcStabilize(pc[:])
		predcoefs[j] = pc
	}
	return predcoefs, ilsf
}

// silk_A2NLSF (fixed-point forward A -> NLSF)

const lsfCosTabSzFix = 128
const binDivStepsA2nlsfFix int32 = 3
const maxIterationsA2nlsfFix int32 = 16

// silkLsfCosTabFixQ12 (the SILK cosine table the Rust file `include!`s from silk_lsf_cos_tab.rs)
// lives in silk_lsf_cos_tab.go.

func silkRshiftRound(a, shift int32) int32 {
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}

func silkSmlaww(a32, b32, c32 int32) int32 {
	// silk_SMLAWW in its canonical 64-bit form: a32 + ((i64)b32 * c32 >> 16).
	return int32(int64(a32) + ((int64(b32) * int64(c32)) >> 16))
}

func silkDiv32(a, b int32) int32 {
	return a / b
}

func silkDiv32_16(a, b int32) int32 {
	return a / b
}

// silkBwexpander32 is `silk_bwexpander_32`: chirp (bandwidth expand) LPC coefficients in Q16 in
// place. `chirp_Q16 += RSHIFT_ROUND(MUL(chirp, chirp-65536), 16)` with `MUL` a wrapping i32
// multiply.
func silkBwexpander32(ar []int32, d int, chirpQ16 int32) {
	chirp := chirpQ16
	chirpMinusOne := chirpQ16 - 65536
	for i := 0; i < d-1; i++ {
		ar[i] = int32((int64(chirp) * int64(ar[i])) >> 16) // silk_SMULWW
		mul := chirp * chirpMinusOne                       // silk_MUL (i32 wrap)
		chirp = chirp + silkRshiftRound(mul, 16)
	}
	ar[d-1] = int32((int64(chirp) * int64(ar[d-1])) >> 16)
}

func silkA2nlsfTransPoly(p []int32, dd int) {
	for k := 2; k <= dd; k++ {
		for n := dd; n >= k+1; n-- {
			p[n-2] -= p[n]
		}
		p[k-2] -= p[k] << 1
	}
}

func silkA2nlsfEvalPoly(p []int32, x int32, dd int) int32 {
	xQ16 := x << 4
	y32 := p[dd]
	for n := dd - 1; n >= 0; n-- {
		y32 = silkSmlaww(p[n], y32, xQ16)
	}
	return y32
}

func silkA2nlsfInit(aQ16, p, q []int32, dd int) {
	p[dd] = 1 << 16
	q[dd] = 1 << 16
	for k := 0; k < dd; k++ {
		p[k] = -aQ16[dd-k-1] - aQ16[dd+k]
		q[k] = -aQ16[dd-k-1] + aQ16[dd+k]
	}
	for k := dd; k >= 1; k-- {
		p[k-1] -= p[k]
		q[k-1] += q[k]
	}
	silkA2nlsfTransPoly(p, dd)
	silkA2nlsfTransPoly(q, dd)
}

// silkA2nlsf is `silk_A2NLSF`: monic whitening coefficients (Q16) -> NLSF (Q15, 0..2^15-1).
// Mutates `aQ16` (bandwidth expansion on non-convergence). `d` is the filter order (even, at most
// SMPL_LPC_ORDER here: the Rust `vec![0; dd + 1]` P/Q buffers are fixed arrays in this port).
func silkA2nlsf(nlsf, aQ16 []int32, d int) {
	dd := d >> 1
	var pBuf, qBuf [smplLpcOrder/2 + 1]int32
	p := pBuf[:dd+1]
	q := qBuf[:dd+1]

	silkA2nlsfInit(aQ16, p, q, dd)

	// pSel: false => P, true => Q
	sel := func(useQ bool) []int32 {
		if useQ {
			return q
		}
		return p
	}
	useQ := false
	xlo := silkLsfCosTabFixQ12[0]
	ylo := silkA2nlsfEvalPoly(sel(useQ), xlo, dd)

	var rootIx int
	if ylo < 0 {
		nlsf[0] = 0
		useQ = true
		ylo = silkA2nlsfEvalPoly(q, xlo, dd)
		rootIx = 1
	} else {
		rootIx = 0
	}
	k := 1
	i := int32(0)
	thr := int32(0)
	for {
		xhi := silkLsfCosTabFixQ12[k]
		yhi := silkA2nlsfEvalPoly(sel(useQ), xhi, dd)

		if (ylo <= 0 && yhi >= thr) || (ylo >= 0 && yhi <= -thr) {
			if yhi == 0 {
				thr = 1
			} else {
				thr = 0
			}
			xloL := xlo
			yloL := ylo
			xhiL := xhi
			ffrac := int32(-256)
			for m := int32(0); m < binDivStepsA2nlsfFix; m++ {
				xmid := silkRshiftRound(xloL+xhiL, 1)
				ymid := silkA2nlsfEvalPoly(sel(useQ), xmid, dd)
				if (yloL <= 0 && ymid >= 0) || (yloL >= 0 && ymid <= 0) {
					xhiL = xmid
					yhi = ymid
				} else {
					xloL = xmid
					yloL = ymid
					ffrac += 128 >> m
				}
			}
			if absI32(yloL) < 65536 {
				den := yloL - yhi
				nom := (yloL << (8 - binDivStepsA2nlsfFix)) + (den >> 1)
				if den != 0 {
					ffrac += silkDiv32(nom, den)
				}
			} else {
				ffrac += silkDiv32(yloL, (yloL-yhi)>>(8-binDivStepsA2nlsfFix))
			}
			nlsf[rootIx] = min((int32(k)<<8)+ffrac, int32(silkInt16Max))

			rootIx++
			if rootIx >= d {
				break
			}
			useQ = (rootIx & 1) != 0
			xlo = silkLsfCosTabFixQ12[k-1]
			ylo = (1 - (int32(rootIx) & 2)) << 12
		} else {
			k++
			xlo = xhi
			ylo = yhi
			thr = 0
			if k > lsfCosTabSzFix {
				i++
				if i > maxIterationsA2nlsfFix {
					nlsf[0] = silkDiv32_16(1<<15, int32(d)+1)
					for kk := 1; kk < d; kk++ {
						nlsf[kk] = nlsf[kk-1] + nlsf[0]
					}
					return
				}
				silkBwexpander32(aQ16, d, 65536-(int32(1)<<i))
				silkA2nlsfInit(aQ16, p, q, dd)
				useQ = false
				xlo = silkLsfCosTabFixQ12[0]
				ylo = silkA2nlsfEvalPoly(p, xlo, dd)
				if ylo < 0 {
					nlsf[0] = 0
					useQ = true
					ylo = silkA2nlsfEvalPoly(q, xlo, dd)
					rootIx = 1
				} else {
					rootIx = 0
				}
				k = 1
			}
		}
	}
}

// smplA2nlsf16 is `smpl_A2NLSF_16`: post-BWE float `A[0..16]` (`A[0]=1`) -> analysis NLSF in
// radians (0..pi).
func smplA2nlsf16(a []float32) [smplLpcOrder]float32 {
	var aQ16 [smplLpcOrder]int32
	for i := 0; i < smplLpcOrder; i++ {
		aQ16[i] = f32ToI32(roundF32(-a[i+1] * 65536.0))
	}
	var lsfQ15 [smplLpcOrder]int32
	silkA2nlsf(lsfQ15[:], aQ16[:], smplLpcOrder)
	var nlsf [smplLpcOrder]float32
	for i := 0; i < smplLpcOrder; i++ {
		nlsf[i] = float32(lsfQ15[i]) / 32768.0 * smplPi
	}
	return nlsf
}
