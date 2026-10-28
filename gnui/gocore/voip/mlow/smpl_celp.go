// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_celp.rs, MIT.
//
// MLow (smpl_audio_codec) CELP excitation encoder.
//
// This is the correctness-critical per-subframe analyzer. It builds a perceptually weighted impulse
// response (`Phi`), an LTP/ACB basis for voiced frames, runs the FCB pulse search (greedy +
// delayed-decision beam), and RD-quantizes the adaptive- and fixed-codebook gains against entropy
// tables built once at init. `smplDcmfToCmf` keeps integer truncating division because the RD
// winner depends on it bit-exactly.
//
// Self-contained on purpose: small leaf helpers are duplicated locally rather than shared with
// `smpl_synth.rs` / `smpl_harmcomb.rs`, so the pipeline integration can be wired separately without
// coupling.
//
// The encode path is not yet fully wired: a duplicated leaf vector op (smplSubVecInplace) and the
// celpSubframeOut.nPulses/excLpc outputs the consumer doesn't read yet are scaffolding.
//
// Go port notes: Rust's per-call `Vec` scratch (and the `mem::take`n pooled buffers) become fixed
// arrays, either encoder fields or stack locals, so steady-state encoding does not allocate.
// celpSubframeOut.excLpc is a fixed [smplMaxSfLen] array whose first fcbSubfrlen samples are the
// subframe (Rust returns a fresh Vec of that length).

package mlow

import (
	"math"
	"sync"
)

// Tier 0: constants. SMPL_PI, SMPL_MAX_SF_LEN, SMPL_MAX_L_RESP, SMPL_PERC_RESP_LEN,
// SMPL_LPC_ORDER, SMPL_LTP_INTERPOL_DELAY, SMPL_LAG_SUBFRLEN, SMPL_MAX_PITCH_LAG,
// SMPL_MAX_PULSES_PER_SF, SMPL_ACBG_M, SMPL_FCBG_V_N, SMPL_UV_GAIN_IDX_LEN, the V/UV gain
// min/step dB, SMPL_PITCH_SHARPENING_COEF, SMPL_CELP_IDX_* and SMPL_CELP_MAX_RATES live in
// shared.go. SMPL_PI is the exact literal `3.1415926535897f` the codec uses (the same f32 as PI).

const (
	smplMaxpitchLen = 320 // 20ms * 16kHz

	smplAcbgN = 16

	smplFcbgVDeltaN = 67

	smplCelpMaxNumsurv = 8

	nGainSteps = 2
)

const (
	smplGAcbRdMu     float32 = 0.014999999664723873
	smplVGainMaxDb   float32 = 0.0
	smplUvGainMaxDb  float32 = 0.0
	smplRateAcbScale float32 = 0.9
)

const smplFcbSrvMax int32 = 4

// Raw constant codec tables.

var smplCbAcbgainsLrQ14 = [smplAcbgN * smplAcbgM]int16{
	2812, 2484,
	0, 0,
	-362, 2465,
	-337, 703,
	3033, 1474,
	13536, 220,
	-2630, 9226,
	6032, 3499,
	-220, 441,
	7661, 4243,
	11521, 0,
	1430, 779,
	4495, 2724,
	15535, 343,
	-779, 1559,
	480, 481,
}

var smplCbAcbgainsHrQ14 = [smplAcbgN * smplAcbgM]int16{
	16039, 91,
	0, 0,
	4310, 4930,
	-1431, 2862,
	2893, 0,
	8009, 4075,
	2754, 4223,
	8367, 354,
	4640, 1254,
	-176, 2734,
	-1222, 5017,
	-476, 1506,
	11351, 567,
	1243, 0,
	10601, 22,
	14088, 108,
}

var smplAcbgainsDcmfLr = [(smplAcbgN + 1) * smplAcbgN]uint8{
	103, 70, 48, 3, 122, 135, 47, 192, 2, 255, 99, 96, 186, 194, 4, 28,
	161, 90, 76, 3, 181, 60, 37, 219, 2, 132, 81, 146, 255, 43, 3, 36,
	114, 222, 55, 6, 203, 34, 42, 154, 6, 255, 33, 209, 225, 78, 6, 45,
	198, 161, 110, 8, 239, 26, 35, 162, 4, 117, 42, 214, 255, 33, 6, 72,
	55, 255, 124, 55, 124, 55, 55, 55, 55, 78, 55, 215, 111, 55, 55, 167,
	154, 136, 77, 4, 220, 33, 38, 166, 2, 144, 50, 196, 255, 43, 4, 41,
	56, 21, 19, 3, 48, 255, 38, 220, 2, 225, 107, 31, 122, 227, 2, 11,
	63, 38, 23, 4, 77, 85, 58, 190, 4, 255, 53, 53, 145, 138, 4, 14,
	95, 47, 33, 2, 110, 146, 53, 255, 2, 219, 79, 73, 198, 122, 2, 15,
	84, 255, 84, 84, 147, 84, 84, 84, 84, 120, 84, 120, 84, 84, 84, 84,
	73, 58, 25, 1, 95, 99, 52, 175, 1, 255, 48, 69, 151, 184, 1, 15,
	105, 32, 43, 2, 84, 225, 34, 255, 2, 156, 129, 49, 189, 124, 3, 19,
	152, 230, 89, 6, 253, 28, 40, 153, 2, 195, 31, 255, 249, 58, 5, 61,
	138, 84, 54, 3, 173, 96, 45, 247, 2, 176, 83, 128, 255, 69, 2, 26,
	22, 17, 8, 1, 23, 106, 26, 88, 1, 182, 37, 18, 50, 255, 1, 6,
	218, 174, 228, 65, 186, 65, 65, 92, 65, 65, 65, 255, 174, 65, 65, 174,
	117, 255, 101, 16, 180, 20, 33, 94, 10, 131, 20, 222, 143, 38, 15, 105,
}

var smplAcbgainsDcmfHr = [(smplAcbgN + 1) * smplAcbgN]uint8{
	254, 105, 212, 26, 110, 255, 202, 93, 152, 121, 110, 43, 150, 20, 81, 176,
	255, 28, 100, 5, 26, 184, 61, 29, 36, 26, 28, 9, 61, 4, 27, 116,
	121, 255, 161, 39, 195, 215, 191, 75, 186, 178, 119, 82, 68, 41, 43, 56,
	188, 65, 243, 15, 74, 255, 205, 79, 123, 84, 95, 26, 139, 13, 67, 154,
	81, 219, 173, 70, 219, 165, 234, 102, 231, 255, 191, 119, 87, 60, 62, 59,
	106, 255, 182, 49, 242, 196, 233, 95, 247, 228, 152, 96, 81, 45, 54, 61,
	236, 55, 178, 10, 56, 255, 131, 54, 85, 58, 59, 18, 93, 9, 43, 133,
	123, 95, 224, 24, 113, 202, 255, 105, 186, 134, 135, 38, 141, 18, 82, 111,
	126, 97, 204, 34, 126, 186, 255, 141, 210, 147, 149, 46, 165, 22, 113, 122,
	96, 156, 185, 42, 188, 178, 255, 116, 248, 199, 157, 66, 109, 29, 69, 75,
	102, 207, 194, 57, 224, 193, 255, 107, 253, 242, 180, 95, 97, 44, 60, 64,
	105, 119, 202, 39, 140, 189, 255, 110, 207, 173, 165, 54, 119, 24, 75, 85,
	74, 255, 142, 59, 214, 150, 182, 76, 194, 215, 138, 122, 61, 56, 41, 45,
	200, 53, 255, 17, 66, 238, 222, 109, 129, 78, 101, 21, 227, 11, 110, 243,
	74, 255, 128, 50, 187, 149, 154, 63, 165, 184, 115, 101, 52, 47, 37, 34,
	159, 66, 232, 26, 86, 196, 255, 146, 171, 113, 134, 31, 245, 16, 145, 190,
	255, 29, 182, 7, 33, 235, 115, 55, 59, 37, 47, 11, 139, 6, 60, 234,
}

var smplFcbgVDcmf = [smplFcbgVN]uint8{
	107, 12, 17, 25, 31, 41, 52, 65, 83, 103, 122, 146, 169, 191, 210, 227,
	240, 249, 255, 253, 246, 229, 200, 161, 120, 82, 51, 29, 14, 6, 2, 2,
	2, 2,
}

var smplFcbgVDeltaDcmf = [smplFcbgVDeltaN]uint8{
	1, 1, 1, 1, 1, 1, 1, 1, 2, 3, 4, 6, 8, 10, 12, 12,
	12, 13, 14, 14, 14, 13, 12, 11, 10, 9, 8, 9, 15, 33, 65, 119,
	196, 255, 220, 144, 90, 57, 36, 23, 17, 14, 12, 12, 12, 13, 12, 12,
	12, 12, 12, 11, 11, 10, 9, 7, 6, 4, 3, 2, 1, 1, 1, 1,
	1, 1, 1,
}

// SMPL_INTERPOL_KERNEL is smplInterpolKernel in shared.go.

// Tier 1: leaf math helpers.

func celpSmplDotProd(a, b []float32, l int) float32 {
	// Strict left fold in the same order, so the f32 rounding sequence is unchanged.
	var ret float32
	a = a[:l]
	b = b[:l]
	for i, x := range a {
		ret += float32(x * b[i])
	}
	return ret
}

func celpSmplNrg(x []float32, n int) float32 {
	var nrg float32
	for _, v := range x[:n] {
		nrg += float32(v * v)
	}
	return nrg
}

func smplReverse(x []float32, l int) {
	for i := 0; i < l/2; i++ {
		x[i], x[l-i-1] = x[l-i-1], x[i]
	}
}

// x -= y
func smplSubVecInplace(y, x []float32, l int) {
	for i := 0; i < l; i++ {
		x[i] -= y[i]
	}
}

// x = y - z
func smplSubVec(y, z, x []float32, l int) {
	for i := 0; i < l; i++ {
		x[i] = y[i] - z[i]
	}
}

// x += y
func smplAddVecInplace(y, x []float32, l int) {
	for i := 0; i < l; i++ {
		x[i] += y[i]
	}
}

func smplScaleVecInplace(x []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		x[i] *= g
	}
}

func celpSmplScaleVec(x, y []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		y[i] = x[i] * g
	}
}

// y += g * x
func celpSmplAddScaleVecInplace(x, y []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		y[i] += float32(g * x[i])
	}
}

// y = x0 + g * x1
func celpSmplAddScaleVec(x0, x1, y []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		y[i] = x0[i] + float32(g*x1[i])
	}
}

func smplMulVecInplace(x, y []float32, l int) {
	for i := 0; i < l; i++ {
		y[i] *= x[i]
	}
}

func smplCelpQ(num, den []float32, l int, q []float32) {
	q = q[:l]
	num = num[:l]
	den = den[:l]
	for i := range q {
		q[i] = (num[i] * num[i]) / den[i]
	}
}

// smplMultSymtoepl2 is `y[n] = sum C[.] * x[.]` for a symmetric Toeplitz multiply. `C` must carry
// the trailing zero at index `2*L_resp-1` and `x` must be readable up to `N + L_resp` (zero padded
// past `N`): the inner loop deliberately reads one extra zero element for SIMD friendliness.
func smplMultSymtoepl2(c []float32, lResp int, x, y []float32, n int) {
	ln := lResp
	nn := 0
	for nn < lResp-1 {
		y[nn] = celpSmplDotProd(c[lResp-1-nn:], x[0:], ln)
		ln++
		nn++
	}
	ln = 2 * lResp
	for nn < n-lResp {
		// nn >= l_resp-1 here, so (nn + 1 - l_resp) avoids the usize underflow of nn - l_resp + 1.
		y[nn] = celpSmplDotProd(c[0:], x[nn+1-lResp:], ln)
		nn++
	}
	for nn < n {
		ln--
		y[nn] = celpSmplDotProd(c[0:], x[nn+1-lResp:], ln)
		nn++
	}
}

// smplFiltAr16 is a 16th-order AR filter. The 16-sample state sits in `y[base-16 .. base]`;
// `x`/`y` are slices whose index 0 is the logical sample 0 (so callers pass an offset slice for
// the history).
func smplFiltAr16(x []float32, n int, coef []float32, yBase int, y []float32) {
	for nn := 0; nn < n; nn++ {
		res := x[nn]
		for i := 0; i < 16; i++ {
			res -= float32(coef[16-i] * y[yBase+nn-16+i])
		}
		y[yBase+nn] = res
	}
}

// smplFiltMa is an MA filter: the `(coef_len-1)` history samples sit before `x[0]` (caller passes
// an offset). `x != y`.
//
// The caller's contract, spelled out because the tap windows below depend on it: `coef_len - 1`
// history samples sit before `x[0]`, so every `x[x_base - i ..]` starts in bounds; and the
// `coef[0] == 1.0` branch needs a `coef[1]` to read. Both hold at the only call site
// (`percFiltMa`, where n == coef_len == perc_resp_len, which is 32 whenever it routes here --
// perc_resp_len == 10 goes to `smplFiltMa9`). Taps are applied in ascending order into the same
// accumulator, so the sum order -- and the output bits -- match the per-element form.
func smplFiltMa(x []float32, xBase, n int, coef []float32, coefLen int, y []float32) {
	var i int
	yv := y[:n]
	if coef[0] == 1.0 {
		// y[k] = x[k] + coef[1]*x[k-1]
		c1 := coef[1]
		x0 := x[xBase : xBase+n]
		xm1 := x[xBase-1 : xBase-1+n]
		for k := range yv {
			yv[k] = x0[k] + float32(c1*xm1[k])
		}
		i = 2
	} else {
		c0 := coef[0]
		xs := x[xBase : xBase+n]
		for k := range yv {
			yv[k] = c0 * xs[k]
		}
		i = 1
	}
	for i < coefLen {
		ci := coef[i]
		xs := x[xBase-i : xBase-i+n]
		for k := range yv {
			yv[k] += float32(ci * xs[k])
		}
		i++
	}
}

// smplFiltMa9 is a 9th-order MA. The 9-sample history sits before `x[0]` (caller passes an
// offset).
func smplFiltMa9(x []float32, xBase, n int, coef []float32, _ int, y []float32) {
	for nn := 0; nn < n; nn++ {
		var res float32
		for i := 0; i < 10; i++ {
			res += float32(coef[i] * x[xBase+nn-i])
		}
		y[nn] = res
	}
}

// celpDcmfToCmf is `smpl_dcmf_to_cmf` returning the cumulative u16 CDF (len `len(dcmf)+1`).
// Table-build only; it allocates.
func celpDcmfToCmf(dcmf []uint8) []uint16 {
	cmf := make([]uint16, len(dcmf)+1)
	smplDcmfToCmf(dcmf, len(dcmf), cmf)
	return cmf
}

// smplDcmfToCmf is INTEGER, bit-exact: `cmf[n+1] = min((dcmf[n]+1)^2, 65535)`, then
// truncating-int normalize. Note the `(32767 - dcmf_len)` uses the dcmf length, not the cmf
// length; this is intentional, not a typo.
func smplDcmfToCmf(dcmf []uint8, dcmfLen int, cmf []uint16) {
	var sum int32
	for n := 0; n < dcmfLen; n++ {
		tmp := int32(dcmf[n])
		tmp++
		tmp *= tmp
		if tmp > 65535 {
			tmp = 65535
		}
		cmf[n+1] = uint16(tmp)
		sum += tmp
	}
	cmf[0] = 0
	for n := 1; n < dcmfLen+1; n++ {
		cmf[n] = cmf[n-1] + uint16((int32(cmf[n])*(32767-int32(dcmfLen)))/sum) + 1
	}
}

func smplCmfToBits(cmf []uint16, cmfLen int, bits []float32) {
	for i := 0; i < cmfLen-1; i++ {
		bits[i] = -log2F32(float32(cmf[i+1]-cmf[i]) / float32(cmf[cmfLen-1]))
	}
}

// smplGetMaxi is argmax (lowest index on tie). The reference resolves ties to the lowest index; a
// forward scan that only replaces on strict `>` is equivalent. An empty range (xLen <= 1) returns
// 0 without reading past x[0].
func smplGetMaxi(x []float32, xLen int) int {
	i := 0
	maxtmp := x[0]
	for n := 1; n < xLen; n++ {
		if x[n] > maxtmp {
			maxtmp = x[n]
			i = n
		}
	}
	return i
}

// smplGetMaxiK writes the best `k` indices into `idx[..k]`, descending by value with lowest-index
// ties. The remaining destination is untouched. Storage is the caller's bounded survivor array.
func smplGetMaxiK(x []float32, idx []int32, xLen, k int) {
	if k == 0 {
		return
	}
	values := x[:xLen]
	if k == 1 {
		idx[0] = int32(smplGetMaxi(values, xLen))
		return
	}
	// A NaN wins only when it is the first unselected value in the legacy scan.
	// That ordering cannot be represented by a value comparator.
	for _, v := range values {
		if v != v {
			smplGetMaxiKScan(values, idx, xLen, k)
			return
		}
	}
	if len(values) == 0 {
		panic("assertion failed: !values.is_empty()")
	}
	limit := min(k, xLen)
	ln := 0
	for i, v := range values {
		if ln == limit && v <= values[idx[limit-1]] {
			continue
		}
		pos := min(ln, limit-1)
		for pos > 0 && v > values[idx[pos-1]] {
			idx[pos] = idx[pos-1]
			pos--
		}
		idx[pos] = int32(i)
		ln = min(ln+1, limit)
	}
	// The old scan repeats index zero after exhausting the candidates.
	clear(idx[limit:k])
}

// smplGetMaxiKScan preserves repeated-selection semantics, including index zero after nonempty
// input is exhausted.
func smplGetMaxiKScan(x []float32, idx []int32, xLen, k int) {
	var takenBuf [smplMaxSfLen]bool
	taken := takenBuf[:xLen]
	xs := x[:xLen]
	for kk := 0; kk < k; kk++ {
		best := float32(-math.MaxFloat32)
		bi := 0
		found := false
		for n, t := range taken {
			v := xs[n]
			if !t && (!found || v > best) {
				best = v
				bi = n
				found = true
			}
		}
		taken[bi] = true
		idx[kk] = int32(bi)
	}
}

// Tier 2: entropy/gain table builder.

type celpTablesT struct {
	acbgainsCmfLr     [(smplAcbgN + 1) * (smplAcbgN + 1)]uint16
	acbgainsCmfHr     [(smplAcbgN + 1) * (smplAcbgN + 1)]uint16
	acbgInvProbLr     [(smplAcbgN + 1) * smplAcbgN]float32
	acbgInvProbHr     [(smplAcbgN + 1) * smplAcbgN]float32
	fcbgainsVCmf      [smplFcbgVN + 1]uint16
	fcbgainsVDeltaCmf [smplFcbgVDeltaN + 1]uint16
	fcbgainsV         [smplFcbgVN]float32
	fcbgainsUv        [smplUvGainIdxLen + 1]float32
	fcbgVInvProb      [smplFcbgVN]float32
	fcbgVDeltaInvProb [smplFcbgVDeltaN]float32
}

var celpTablesOnce = sync.OnceValue(func() *celpTablesT {
	t := buildCelpTables()
	return &t
})

func celpTables() *celpTablesT { return celpTablesOnce() }

func buildCelpTables() celpTablesT {
	var t celpTablesT

	// (a) acbgains cmf, 17 rows of 16-dcmf -> 17-cmf
	for i := 0; i < smplAcbgN+1; i++ {
		smplDcmfToCmf(smplAcbgainsDcmfLr[i*smplAcbgN:], smplAcbgN, t.acbgainsCmfLr[i*(smplAcbgN+1):])
		smplDcmfToCmf(smplAcbgainsDcmfHr[i*smplAcbgN:], smplAcbgN, t.acbgainsCmfHr[i*(smplAcbgN+1):])
	}

	// (b) acbg inv-prob: per row, cmf_to_bits(17) -> bits[16], then 2^(bits*mu)
	for i := 0; i < smplAcbgN+1; i++ {
		smplCmfToBits(t.acbgainsCmfLr[i*(smplAcbgN+1):], smplAcbgN+1, t.acbgInvProbLr[i*smplAcbgN:])
		smplCmfToBits(t.acbgainsCmfHr[i*(smplAcbgN+1):], smplAcbgN+1, t.acbgInvProbHr[i*smplAcbgN:])
		for j := 0; j < smplAcbgN; j++ {
			t.acbgInvProbLr[i*smplAcbgN+j] = powF32(2.0, t.acbgInvProbLr[i*smplAcbgN+j]*smplGAcbRdMu)
			t.acbgInvProbHr[i*smplAcbgN+j] = powF32(2.0, t.acbgInvProbHr[i*smplAcbgN+j]*smplGAcbRdMu)
		}
	}

	// (c) fcb voiced gain cmfs + inv-prob
	smplDcmfToCmf(smplFcbgVDcmf[:], smplFcbgVN, t.fcbgainsVCmf[:])
	smplDcmfToCmf(smplFcbgVDeltaDcmf[:], smplFcbgVDeltaN, t.fcbgainsVDeltaCmf[:])
	smplCmfToBits(t.fcbgainsVCmf[:], smplFcbgVN+1, t.fcbgVInvProb[:])
	for i := 0; i < smplFcbgVN; i++ {
		t.fcbgVInvProb[i] = powF32(2.0, t.fcbgVInvProb[i]*smplGAcbRdMu)
	}
	smplCmfToBits(t.fcbgainsVDeltaCmf[:], smplFcbgVDeltaN+1, t.fcbgVDeltaInvProb[:])
	for i := 0; i < smplFcbgVDeltaN; i++ {
		t.fcbgVDeltaInvProb[i] = powF32(2.0, t.fcbgVDeltaInvProb[i]*smplGAcbRdMu)
	}

	// (d) gain magnitude tables
	for ix := 0; ix < smplFcbgVN; ix++ {
		fcbGainDb := float32(float32(ix)*smplVGainStepDb) + smplVGainMinDb
		t.fcbgainsV[ix] = powF32(10.0, 0.05*fcbGainDb)
	}
	for ix := 0; ix <= smplUvGainIdxLen; ix++ {
		fcbGainDb := float32(float32(ix)*smplUvGainStepDb) + smplUvGainMinDb
		t.fcbgainsUv[ix] = powF32(10.0, 0.05*fcbGainDb)
	}

	return t
}

// Tier 3: LTP / ACB synthesis.

func celpAcbDequant(lowRate bool, acbIdx int32, acbG *[smplAcbgM]float32) {
	cb := smplCbAcbgainsHrQ14[:]
	if lowRate {
		cb = smplCbAcbgainsLrQ14[:]
	}
	scQ14 := float32(1.0) / float32(int32(1)<<14)
	for m := 0; m < smplAcbgM; m++ {
		acbG[m] = float32(cb[int(acbIdx)*smplAcbgM+m]) * scQ14
	}
}

// celpAcbSynthesize: gain adjustment with `high_boost==0` is a no-op (the encoder always passes
// 0), so the synth is just the 3-tap symmetric basis combination.
func celpAcbSynthesize(fcbSubfrlen int, acbBasis []float32, acbG *[smplAcbgM]float32, acb []float32) {
	celpSmplScaleVec(acbBasis, acb, fcbSubfrlen, acbG[0])
	celpSmplAddScaleVecInplace(acbBasis[fcbSubfrlen:], acb, fcbSubfrlen, acbG[1])
}

func smplPitchSharp(x []float32, lag, l int) {
	for i := lag; i < l; i++ {
		x[i] += float32(x[i-lag] * smplPitchSharpeningCoef)
	}
}

// smplSynLtpBasis builds the LTP basis (basis0 = pitch-extended excitation, basis1 = its 3-tap
// symmetric neighbor sum) per 40-sample sub-block, and MUTATES `state` in place by extending the
// excitation forward. `state` is the full `acb_state`; `state_off` is
// `&state[state_len - n_lags*40]`.
func smplSynLtpBasis(lags []float32, nLags int, state []float32, stateLen int, acbBasis []float32) {
	p := stateLen - nLags*smplLagSubfrlen // index into `state` at the excitation tail
	for subfr := 0; subfr < nLags; subfr++ {
		iLag := f32ToI32(floorF32(lags[subfr]))
		if float32(iLag) == lags[subfr] {
			il := int(iLag)
			for i := 0; i < smplLagSubfrlen; i++ {
				// p[i] = p[i - i_lag]; i_lag <= i offset so source already written/available
				state[p+i] = state[(p+i)-il]
			}
			for i := 0; i < smplLagSubfrlen; i++ {
				acbBasis[subfr*smplLagSubfrlen+i] = state[p+i]
			}
			// basis1[i] = p[i - i_lag - 1] + p[i - i_lag + 1]
			for i := 0; i < smplLagSubfrlen; i++ {
				a := state[(p+i)-il-1]
				b := state[(p+i)-il+1]
				acbBasis[(nLags+subfr)*smplLagSubfrlen+i] = a + b
			}
		} else {
			// Fractional lag.
			// first = dot(p[-i_lag-8 ..], kernel, 16)
			il := int(iLag) // may be used in signed arithmetic
			baseFirst := p + (-1 - il - smplLtpInterpolDelay)
			first := celpSmplDotProd(state[baseFirst:], smplInterpolKernel[:], 2*smplLtpInterpolDelay)
			// smpl_interpol(p - i_lag - 8, p, 40)  (writes p[0..40] from the history before it)
			{
				srcBase := p + (-il - smplLtpInterpolDelay)
				for nn := 0; nn < smplLagSubfrlen; nn++ {
					var ret float32
					for i := 0; i < 8; i++ {
						s0 := state[srcBase+nn+i]
						s1 := state[srcBase+nn+15-i]
						ret += float32((s0 + s1) * smplInterpolKernel[i])
					}
					state[p+nn] = ret
				}
			}
			// last = dot(p[SMPL_LAG_SUBFRLEN - i_lag - 8 ..], kernel, 16). No -1: matches the C
			// reference (smpl_celp_util.c uses i==SMPL_LAG_SUBFRLEN) and our own decoder basis.
			baseLast := p + (smplLagSubfrlen - il - smplLtpInterpolDelay)
			last := celpSmplDotProd(state[baseLast:], smplInterpolKernel[:], 2*smplLtpInterpolDelay)
			for i := 0; i < smplLagSubfrlen; i++ {
				acbBasis[subfr*smplLagSubfrlen+i] = state[p+i]
			}
			b1 := (nLags + subfr) * smplLagSubfrlen
			acbBasis[b1] = first + state[p+1]
			// basis1[1..39] = p[i] + p[i+2] for i in 0..38 -> dst index i+1
			for i := 0; i < smplLagSubfrlen-2; i++ {
				acbBasis[b1+1+i] = state[p+i] + state[p+i+2]
			}
			iLast := smplLagSubfrlen - 1
			acbBasis[b1+iLast] = state[p+iLast-1] + last
		}
		p += smplLagSubfrlen
	}
}

// Tier 4: FCB search.

type fcb struct {
	wnrg        float32
	nPulses     int32
	posNew      int32
	signNew     float32
	sgntr       uint64
	fcbStateIdx int
}

func defaultFcb() fcb { return fcb{} }

// fcbState is fixed-length (smplMaxSfLen) scratch indexed by subframe position; never resized.
// Inline arrays so the per-subframe `new()` and `clone_from` in the FCB survivor search are memcpys
// instead of heap alloc/free.
type fcbState struct {
	pulsePositions [smplMaxSfLen]int32
	pulseSigns     [smplMaxSfLen]float32
	num            [smplMaxSfLen]float32
	den            [smplMaxSfLen]float32
}

func newFcbState() fcbState { return fcbState{} }

func calcDAbsAndSign(d []float32, l int, dAbs, dSign []float32) {
	for i := 0; i < l; i++ {
		if d[i] > 0.0 {
			dAbs[i] = d[i]
			dSign[i] = 1.0
		} else {
			dAbs[i] = -d[i]
			dSign[i] = -1.0
		}
	}
}

func checkIfBetter(wnrg float32, nrgThr *float32, wnrgPerPulse float32) bool {
	*nrgThr += wnrgPerPulse
	if wnrg > *nrgThr {
		*nrgThr = wnrg
		return true
	}
	return false
}

// phiColOffset is the `PhiFlip` column for `col`: the column starts at
// `PhiFlip[SMPL_MAX_SF_LEN - col]` and is read at arbitrary non-negative indices, so this returns
// the start offset into `phi_flip`.
func phiColOffset(col int32) int32 {
	return smplMaxSfLen - col
}

func nonZeroRange(col int32, percRespLen, fcbSubfrlen int) (int, int) {
	lo := int(max(col-int32(percRespLen)+1, 0))
	hi := int(min(col+int32(percRespLen), int32(fcbSubfrlen)))
	return lo, hi
}

// Public output of the per-subframe encoder.

type celpSubframeOut struct {
	// Packed pulses for each rate. Only the prefix selected by `nPulses`
	// belongs to the subframe; consumers must ignore the remaining capacity.
	pulses  [smplCelpMaxRates][smplMaxPulsesPerSf]int16
	nPulses [smplCelpMaxRates]int16
	acbIdx  [smplCelpMaxRates]int16
	gainIdx [smplCelpMaxRates]int16
	// excLpc[:fcbSubfrlen] is the subframe excitation; the rest is zero. Rust returns a Vec of
	// exactly fcbSubfrlen; a fixed array keeps the hot path allocation-free.
	excLpc [smplMaxSfLen]float32
}

// ACBG params, carried within a single encodeSubframe call.

type acbgParams struct {
	werrIn      float32
	phiAcb      [smplAcbgM * smplAcbgM]float32
	dAcbLpc     [smplAcbgM]float32
	acbBasisPhi [smplAcbgM * smplMaxSfLen]float32 // SMPL_ACBG_M * fcb_subfrlen live
}

// Persistent encoder state.

type celpEncoder struct {
	// state_wght has SMPL_LPC_ORDER samples of history before the "logical" start; logical index 0
	// == stateWghtBuf[SMPL_LPC_ORDER].
	stateWghtBuf   [smplMaxSfLen + smplLpcOrder]float32
	stateErrLpcSyn [smplLpcOrder]float32
	hanningWin     []float32 // perc_resp_len
	sgntrs         [smplMaxSfLen]uint64
	acbState       []float32 // SMPL_MAX_PITCH_LAG + SMPL_MAX_SF_LEN + SMPL_LTP_INTERPOL_DELAY
	acbStateLen    int
	prevAcbIdx     [smplCelpMaxRates]int32
	prevFcbIdx     [smplCelpMaxRates]int32
	subfrCnt       int32
	subfrPerPacket int32
	fcbSubfrlen    int
	percRespLen    int
	lowRate        bool
	ignoreZir      bool
	fcbgain        float32
	useMa9         bool

	// Scratch reused across calls.
	impLpcBuf [smplMaxSfLen + smplLpcOrder]float32 // logical start at SMPL_LPC_ORDER
	phi       [smplMaxSfLen]float32
	phiFlip   [2 * smplMaxSfLen]float32
	// Greedy-FCB-search scratch (per subframe + per pulse), each fully rewritten over
	// [0..fcb_subfrlen] before it is read, so it carries no state between calls.
	fcbGreedy fcbGreedyScratch
	// Delayed-decision beam-search scratch (the heavier path). `reset()` clears its bookkeeping.
	fcbSearch fcbSearchScratch
	// Per-subframe working buffers for `encodeSubframe`, zeroed over the live length at each use
	// so they match a fresh `vec![0.0; N]` without allocating.
	sf subframeScratch
}

// subframeScratch is the pooled per-`encodeSubframe` scratch (see celpEncoder.sf). Each buffer
// is the maximum size Rust's `zeroed(.., n)` ever requests.
type subframeScratch struct {
	impLpcRev [2*smplMaxLResp - 1]float32
	resLpcPad [smplMaxSfLen + smplMaxLResp + 1]float32
	dLpc      [smplMaxSfLen]float32
	zirLpc    [smplMaxSfLen]float32
	acbBasis  [smplMaxSfLen * smplAcbgM]float32
	acb       [smplMaxSfLen]float32
	dLtp      [smplMaxSfLen]float32
	wtgtTmp   [smplMaxSfLen + 2*smplMaxLResp - 1]float32
	wtgt      [smplMaxSfLen + smplMaxLResp]float32
	excFcb    [smplMaxSfLen]float32
	excFcbRaw [smplMaxSfLen]float32
}

func defaultSubframeScratch() subframeScratch { return subframeScratch{} }

// subframeScratchZeroed returns buf[:n] holding exactly `n` zeros -- the drop-in replacement for
// `vec![0.0f32; n]` that reuses the pooled storage.
func subframeScratchZeroed(buf []float32, n int) []float32 {
	v := buf[:n]
	clear(v)
	return v
}

// fcbGreedyScratch holds per-call working buffers for `smplFcbSearch`, hoisted off the
// per-frame/per-pulse hot path.
type fcbGreedyScratch struct {
	dAbs  [smplMaxSfLen]float32
	dSign [smplMaxSfLen]float32
	num   [smplMaxSfLen]float32
	den   [smplMaxSfLen]float32
	q     [smplMaxSfLen]float32
}

func defaultFcbGreedyScratch() fcbGreedyScratch { return fcbGreedyScratch{} }

func newCelpEncoder(lowRate bool, percRespLen, fcbSubfrlen, subfrPerPacket int) celpEncoder {
	// Force table build at first construction.
	_ = celpTables()

	acbStateLen := fcbSubfrlen + smplMaxpitchLen + smplLtpInterpolDelay

	// `sgntrs` are random u64; only used to dedup identical pulse SETS in the deldec beam. A
	// deterministic distinct-per-position sequence is correctness-equivalent (a different beam
	// tie-break can pick a different but equally valid candidate; bit-exact reproduction of the
	// reference's RNG is not required for a correct encoder). Use a fixed LCG so the result is
	// reproducible.
	var sgntrs [smplMaxSfLen]uint64
	s := uint64(0x9E3779B97F4A7C15)
	for i := range sgntrs {
		s = s*6364136223846793005 + 1442695040888963407
		sgntrs[i] = s
	}

	hanningWin := make([]float32, percRespLen)
	scale := float32(1.0) / float32(2*smplPercRespLen+1)
	for i := 0; i < percRespLen; i++ {
		hanningWin[i] = sinF32(smplPi * float32(percRespLen+i+1) * scale)
	}
	useMa9 := percRespLen == 10

	return celpEncoder{
		hanningWin:     hanningWin,
		sgntrs:         sgntrs,
		acbState:       make([]float32, smplMaxPitchLag+smplMaxSfLen+smplLtpInterpolDelay),
		acbStateLen:    acbStateLen,
		prevAcbIdx:     [smplCelpMaxRates]int32{-1, -1},
		prevFcbIdx:     [smplCelpMaxRates]int32{-1, -1},
		subfrCnt:       0,
		subfrPerPacket: int32(subfrPerPacket),
		fcbSubfrlen:    fcbSubfrlen,
		percRespLen:    percRespLen,
		lowRate:        lowRate,
		ignoreZir:      false,
		fcbgain:        0.0,
		useMa9:         useMa9,
		fcbGreedy:      defaultFcbGreedyScratch(),
		fcbSearch:      newFcbSearchScratch(),
		sf:             defaultSubframeScratch(),
	}
}

func (e *celpEncoder) percFiltMa(x []float32, xBase, n int, coef []float32, coefLen int, y []float32) {
	if e.useMa9 {
		smplFiltMa9(x, xBase, n, coef, coefLen, y)
	} else {
		smplFiltMa(x, xBase, n, coef, coefLen, y)
	}
}

// FCB search scratch: the deldec-relevant working buffers. The encoder holds one populated
// instance (built by newFcbSearchScratch) and every search reuses it, paying no per-call
// allocation. (Rust `mem::take`s it out of the encoder for the borrow checker; Go passes a
// pointer to the field.)
type fcbSearchScratch struct {
	// Double-buffered candidate states (read/write ping-pong); swapRw swaps the slice headers.
	statesRead  []fcbState // SMPL_CELP_MAX_NUMSURV
	statesWrite []fcbState // SMPL_CELP_MAX_NUMSURV
	fcbs        []fcb      // SMPL_CELP_MAX_NUMSURV
	fcbsSize    int
	// up to NUMSURV*NUMSURV
	fcbCandidates     []fcb
	fcbCandidatesSize int
	uniqueSgntr       []uint64
	uniqueSgntrSize   int
	// Per-`addPulse` working buffers (each call is on the innermost beam loop). `q` is fully
	// written by `smplCelpQ` before it is read; `ddDen` accumulates so it is zeroed per call.
	q     []float32 // SMPL_MAX_SF_LEN
	ddDen []float32 // SMPL_MAX_SF_LEN
	// The pitch-sharpened deldec target. Fully overwritten (copy then accumulation) over the live
	// region before it is read.
	dNew []float32 // SMPL_MAX_SF_LEN
}

func newFcbSearchScratch() fcbSearchScratch {
	return fcbSearchScratch{
		statesRead:    make([]fcbState, smplCelpMaxNumsurv),
		statesWrite:   make([]fcbState, smplCelpMaxNumsurv),
		fcbs:          make([]fcb, smplCelpMaxNumsurv),
		fcbCandidates: make([]fcb, smplCelpMaxNumsurv*smplCelpMaxNumsurv),
		uniqueSgntr:   make([]uint64, smplCelpMaxNumsurv*smplCelpMaxNumsurv),
		q:             make([]float32, smplMaxSfLen),
		ddDen:         make([]float32, smplMaxSfLen),
		dNew:          make([]float32, smplMaxSfLen),
	}
}

// reset resets the bookkeeping to a fresh-search state (the buffers are overwritten before they
// are read, so only the sizes need clearing). Lets one pooled instance serve every search.
func (sc *fcbSearchScratch) reset() {
	// The read/write roles are not reset: which of the two buffers plays which role at the
	// start of a search cannot be observed, because every live element is written before it is
	// read (`num`/`den` over `..fcb_subfrlen`, the pulse arrays over `..n_pulses`).
	sc.fcbsSize = 0
	sc.fcbCandidatesSize = 0
	sc.uniqueSgntrSize = 0
}

func (sc *fcbSearchScratch) swapRw() {
	sc.statesRead, sc.statesWrite = sc.statesWrite, sc.statesRead
}

func (sc *fcbSearchScratch) isUnique(sgntr uint64) bool {
	for i := 0; i < sc.uniqueSgntrSize; i++ {
		if sc.uniqueSgntr[i] == sgntr {
			return false
		}
	}
	return true
}

// celpUsizeGe is Rust's `(x as usize) >= n` for an i16 x: a negative x sign-extends to a huge usize.
func celpUsizeGe(x int16, n int) bool {
	return uint(int(x)) >= uint(n)
}

// smplFcbSearch is the greedy FCB search.
func (e *celpEncoder) smplFcbSearch(
	d []float32,
	wnrgPerPulse *[smplCelpMaxRates]float32,
	fcbPulsesMax *[smplCelpMaxRates]int16,
	pulses *[smplCelpMaxRates][smplMaxPulsesPerSf]int16,
	nPulses *[smplCelpMaxRates]int16,
	wnrg *[smplCelpMaxRates]float32,
	gainFromSearch *[smplCelpMaxRates]float32,
	fcbWnrg *[smplCelpMaxRates]float32,
) {
	fcbSubfrlen := e.fcbSubfrlen
	percRespLen := e.percRespLen
	*nPulses = [smplCelpMaxRates]int16{}

	phi := &e.phi
	phiFlip := &e.phiFlip
	g := &e.fcbGreedy
	dAbs := g.dAbs[:]
	dSign := g.dSign[:]
	num := g.num[:]
	den := g.den[:]
	q := g.q[:]

	var positions [smplMaxPulsesPerSf]int32
	phi0 := phi[0]
	calcDAbsAndSign(d, fcbSubfrlen, dAbs, dSign)

	for i := 0; i < fcbSubfrlen; i++ {
		den[i] = phi0 + 1e-16
	}
	copy(num[:fcbSubfrlen], dAbs[:fcbSubfrlen])
	positions[0] = int32(smplGetMaxi(num, fcbSubfrlen))
	var nrgThr [smplCelpMaxRates]float32
	p0 := int(positions[0])
	ratio := num[p0] / den[p0]
	wnrg_ := float32(num[p0] * ratio)
	if checkIfBetter(wnrg_, &nrgThr[smplCelpIdxMain], wnrgPerPulse[smplCelpIdxMain]) {
		nPulses[smplCelpIdxMain] = 1
		wnrg[smplCelpIdxMain] = wnrg_
		wnrg[smplCelpIdxFec] = wnrg_
		gainFromSearch[smplCelpIdxMain] = ratio
		gainFromSearch[smplCelpIdxFec] = ratio
		fcbWnrg[smplCelpIdxMain] = den[p0]
		fcbWnrg[smplCelpIdxFec] = den[p0]
		if fcbPulsesMax[smplCelpIdxFec] > 0 {
			nPulses[smplCelpIdxFec] = nPulses[smplCelpIdxMain]
			wnrg[smplCelpIdxFec] = wnrg[smplCelpIdxMain]
			gainFromSearch[smplCelpIdxFec] = gainFromSearch[smplCelpIdxMain]
			fcbWnrg[smplCelpIdxFec] = fcbWnrg[smplCelpIdxMain]
		}
	}

	for pulseNr := 1; pulseNr < int(fcbPulsesMax[smplCelpIdxMain]); pulseNr++ {
		position := positions[pulseNr-1]
		sgn := dSign[position]
		for i := 0; i < fcbSubfrlen; i++ {
			num[i] += dAbs[position]
		}
		nz0, nz1 := nonZeroRange(position, percRespLen, fcbSubfrlen)
		colOff := phiColOffset(position)
		var dDen float32
		for i := 0; i < pulseNr-1; i++ {
			pi := positions[i]
			dDen += float32(phiFlip[colOff+pi] * dSign[pi])
		}
		dDen = float32(dDen * (2.0 * sgn))
		dDen += phiFlip[colOff+position]
		for i := 0; i < fcbSubfrlen; i++ {
			den[i] += dDen
		}
		for i := nz0; i < nz1; i++ {
			den[i] += float32(2.0 * sgn * dSign[i] * phiFlip[int(colOff)+i])
		}
		smplCelpQ(num, den, fcbSubfrlen, q)
		positions[pulseNr] = int32(smplGetMaxi(q, fcbSubfrlen))
		pp := int(positions[pulseNr])
		if checkIfBetter(q[pp], &nrgThr[smplCelpIdxMain], wnrgPerPulse[smplCelpIdxMain]) {
			nPulses[smplCelpIdxMain] = int16(pulseNr + 1)
			wnrg[smplCelpIdxMain] = q[pp]
			gainFromSearch[smplCelpIdxMain] = num[pp] / den[pp]
			fcbWnrg[smplCelpIdxMain] = den[pp]
		}
		if celpUsizeGe(fcbPulsesMax[smplCelpIdxFec], pulseNr) &&
			checkIfBetter(q[pp], &nrgThr[smplCelpIdxFec], wnrgPerPulse[smplCelpIdxFec]) {
			nPulses[smplCelpIdxFec] = int16(pulseNr + 1)
			wnrg[smplCelpIdxFec] = q[pp]
			gainFromSearch[smplCelpIdxFec] = num[pp] / den[pp]
			fcbWnrg[smplCelpIdxFec] = den[pp]
		}
	}

	for r := smplCelpIdxFec; r < smplCelpIdxMain+1; r++ {
		if nrgThr[r] > 0.0 {
			for i := 0; i < int(nPulses[r]); i++ {
				position := positions[i]
				if dSign[position] > 0.0 {
					pulses[r][i] = 1 + int16(position)
				} else {
					pulses[r][i] = -(1 + int16(position))
				}
			}
		} else {
			wnrg[r] = 0.0
			gainFromSearch[r] = 0.0
			fcbWnrg[r] = 0.0
			nPulses[r] = 0
		}
	}
}

// addPulse is the deldec beam helper.
func (e *celpEncoder) addPulse(
	sc *fcbSearchScratch,
	fcbIdxIn int, // index of `fcb` within sc.fcbs
	dAbs []float32,
	dSign []float32,
	numsurv int,
	idx int,
	lag int32,
	pitchSharp float32,
) {
	fcbSubfrlen := e.fcbSubfrlen
	percRespLen := e.percRespLen
	phiFlip := &e.phiFlip

	// Snapshot the read-side fcb (the `fcb` arg is a mutable copy in sc.fcbs[fcb_idx_in]).
	fcbPosNew := sc.fcbs[fcbIdxIn].posNew
	fcbSignNew := sc.fcbs[fcbIdxIn].signNew
	fcbNPulses := sc.fcbs[fcbIdxIn].nPulses
	fcbStateIdx := sc.fcbs[fcbIdxIn].fcbStateIdx
	fcbSgntrBase := sc.fcbs[fcbIdxIn].sgntr

	{
		readSt := &sc.statesRead[fcbStateIdx]
		writeSt := &sc.statesWrite[idx]
		ddDen := sc.ddDen
		n := fcbSubfrlen
		nI := int32(n)
		np := int(fcbNPulses)

		// num_w[i] = num_r[i] + d_abs[pos_new]
		add := dAbs[fcbPosNew]
		wn := writeSt.num[:n]
		rn := readSt.num[:n]
		for i := range wn {
			wn[i] = rn[i] + add
		}
		// den_w = copy(den_r)
		copy(writeSt.den[:n], readSt.den[:n])
		wden := writeSt.den[:n]
		rpos := readSt.pulsePositions[:np]
		rsgn := readSt.pulseSigns[:np]

		if pitchSharp == 0.0 {
			nz0, nz1 := nonZeroRange(fcbPosNew, percRespLen, n)
			colOff := phiColOffset(fcbPosNew)
			var dDen float32
			for k, pos := range rpos {
				dDen += float32(phiFlip[colOff+pos] * rsgn[k])
			}
			dDen = float32(dDen * (2.0 * fcbSignNew))
			dDen += phiFlip[colOff+fcbPosNew]
			for i := range wden {
				wden[i] += dDen
			}
			if nz0 < nz1 {
				// `phi_flip` is walked at a fixed offset from the den index.
				base := int(colOff) + nz0
				ws := writeSt.den[nz0:nz1]
				ss := dSign[nz0:nz1]
				ps := phiFlip[base : base+(nz1-nz0)]
				for i := range ws {
					ws[i] += float32(2.0 * fcbSignNew * ss[i] * ps[i])
				}
			}
		} else {
			// Pitch-sharpened cross terms.
			var g1 float32
			var dDen float32
			// cross term: new pulse train vs each previous pulse train
			{
				g1 = 1.0
				pos := fcbPosNew
				for pos < nI {
					colOff := phiColOffset(pos)
					for k, pulsePos := range rpos {
						pulseSgn := rsgn[k]
						g2 := g1
						pos_ := pulsePos
						for pos_ < nI {
							dDen += float32(g2 * phiFlip[colOff+pos_] * pulseSgn)
							g2 *= pitchSharp
							pos_ += lag
						}
					}
					g1 *= pitchSharp
					pos += lag
				}
			}
			dDen = float32(dDen * (2.0 * fcbSignNew))
			// self term: new pulse train vs itself
			{
				g1 = 1.0
				pos1 := fcbPosNew
				for pos1 < nI {
					colOff := phiColOffset(pos1)
					g2 := g1
					pos2 := fcbPosNew
					for pos2 < nI {
						dDen += float32(g2 * phiFlip[colOff+pos2])
						g2 *= pitchSharp
						pos2 += lag
					}
					g1 *= pitchSharp
					pos1 += lag
				}
			}
			for i := range wden {
				wden[i] += dDen
			}
			// dd_den (accumulated, so zero the live region first).
			clear(ddDen[:n])
			g1 = 1.0
			pos := fcbPosNew
			for pos < nI {
				nz0, nz1 := nonZeroRange(pos, percRespLen, n)
				colOff := phiColOffset(pos)
				g2 := g1
				k := int32(0)
				for k < nI {
					startI := max(int32(nz0)-k, 0)
					endI := min(nI-k, int32(nz1)-k)
					if startI < endI {
						base := int(colOff + startI + k)
						ln := int(endI - startI)
						dd := ddDen[startI:endI]
						ps := phiFlip[base : base+ln]
						for i := range dd {
							dd[i] += float32(g2 * ps[i])
						}
					}
					g2 *= pitchSharp
					k += lag
				}
				g1 *= pitchSharp
				pos += lag
			}
			ss := dSign[:n]
			dd := ddDen[:n]
			for i := range wden {
				wden[i] += float32(2.0 * fcbSignNew * ss[i] * dd[i])
			}
		}

		// Append the new pulse to the state.
		copy(writeSt.pulsePositions[:np], rpos)
		copy(writeSt.pulseSigns[:np], rsgn)
		writeSt.pulsePositions[np] = fcbPosNew
		writeSt.pulseSigns[np] = fcbSignNew
	}

	// The upstream C bumped `fcb->n_pulses` and set `fcb->fcb_state_idx` in place. Here the
	// read-side `sc.fcbs[fcb_idx_in]` is left alone: both values go straight into each candidate
	// emitted below, and the caller rebuilds `sc.fcbs` from the candidates after `swapRw`.
	newNPulses := fcbNPulses + 1

	// Q = num^2/den; top-numsurv -> candidates with unique-signature dedup.
	smplCelpQ(sc.statesWrite[idx].num[:], sc.statesWrite[idx].den[:], fcbSubfrlen, sc.q)
	var sortIx [smplCelpMaxNumsurv]int32
	smplGetMaxiK(sc.q, sortIx[:], fcbSubfrlen, numsurv)

	for i := 0; i < numsurv; i++ {
		pos := int(sortIx[i])
		sgntr := fcbSgntrBase + e.sgntrs[pos]
		if sc.isUnique(sgntr) {
			cs := sc.fcbCandidatesSize
			sc.fcbCandidates[cs] = fcb{
				wnrg:        sc.q[pos],
				nPulses:     newNPulses,
				posNew:      int32(pos),
				signNew:     dSign[pos],
				sgntr:       sgntr,
				fcbStateIdx: idx,
			}
			sc.fcbCandidatesSize++
			us := sc.uniqueSgntrSize
			sc.uniqueSgntr[us] = sgntr
			sc.uniqueSgntrSize++
		}
	}
}

// smplFcbSearchDeldec is the delayed-decision beam FCB search.
func (e *celpEncoder) smplFcbSearchDeldec(
	sc *fcbSearchScratch,
	d []float32,
	pitchSharp float32,
	lag int32,
	wnrgPerPulse *[smplCelpMaxRates]float32,
	fcbPulsesMax *[smplCelpMaxRates]int16,
	surv []int16,
	pulses *[smplCelpMaxRates][smplMaxPulsesPerSf]int16,
	nPulses *[smplCelpMaxRates]int16,
	wnrg *[smplCelpMaxRates]float32,
	gainFromSearch *[smplCelpMaxRates]float32,
	fcbWnrg *[smplCelpMaxRates]float32,
) {
	fcbSubfrlen := e.fcbSubfrlen
	fsl := int32(fcbSubfrlen)
	sc.reset()

	// Rust allocates `d_abs`/`d_sign` per call; stack arrays here.
	var dAbsBuf, dSignBuf [smplMaxSfLen]float32
	dAbs := dAbsBuf[:]
	dSign := dSignBuf[:]
	phi0 := e.phi[0]

	if pitchSharp != 0.0 && lag > 0 && lag < fsl {
		copy(sc.dNew[:fcbSubfrlen], d[:fcbSubfrlen])
		for j := 0; j < fcbSubfrlen; j++ {
			g := pitchSharp
			i := lag + int32(j)
			for i < fsl {
				sc.dNew[j] += float32(g * d[i])
				g *= pitchSharp
				i += lag
			}
		}
		calcDAbsAndSign(sc.dNew, fcbSubfrlen, dAbs, dSign)
	} else {
		calcDAbsAndSign(d, fcbSubfrlen, dAbs, dSign)
		pitchSharp = 0.0
	}

	bestFcb := [smplCelpMaxRates]fcb{defaultFcb(), defaultFcb()}
	bestFcbState := [smplCelpMaxRates]fcbState{newFcbState(), newFcbState()}
	var nrgThr [smplCelpMaxRates]float32

	// Initialize the first state buffer (write slot 0).
	{
		st := &sc.statesWrite[0]
		copy(st.num[:fcbSubfrlen], dAbs[:fcbSubfrlen])
		if pitchSharp == 0.0 {
			v := phi0 + 1e-16
			for i := range st.den[:fcbSubfrlen] {
				st.den[i] = v
			}
		} else {
			offset := fsl - 1
			i := fsl - 1
			for i >= 0 {
				res := float32(1e-16)
				g_1 := float32(1.0)
				j := i
				for j < fsl {
					colOff := phiColOffset(j)
					g_2 := float32(1.0)
					k := i
					for k < fsl {
						res += float32(g_1 * g_2 * e.phiFlip[colOff+k])
						g_2 *= pitchSharp
						k += lag
					}
					g_1 *= pitchSharp
					j += lag
				}
				ln := min(lag, offset+1)
				for jj := int32(0); jj < ln; jj++ {
					st.den[offset-jj] = res
				}
				offset -= ln
				i -= lag
			}
		}
	}

	sc.swapRw()
	{
		// fcb_state is the slot just written: after the swap it is `statesRead[0]`.
		st := &sc.statesRead[0]
		if pitchSharp == 0.0 {
			copy(sc.q[:fcbSubfrlen], st.num[:fcbSubfrlen])
		} else {
			smplCelpQ(st.num[:], st.den[:], fcbSubfrlen, sc.q)
		}
	}

	var sortIx [smplCelpMaxNumsurv]int32
	smplGetMaxiK(sc.q, sortIx[:], fcbSubfrlen, int(surv[0]))
	sc.fcbsSize = 0
	{
		for i := 0; i < int(surv[0]); i++ {
			pos := int(sortIx[i])
			st0 := &sc.statesRead[0]
			sc.fcbs[sc.fcbsSize] = fcb{
				sgntr:       e.sgntrs[pos],
				posNew:      int32(pos),
				signNew:     dSign[pos],
				wnrg:        (st0.num[pos] * st0.num[pos]) / st0.den[pos],
				nPulses:     0,
				fcbStateIdx: 0,
			}
			sc.fcbsSize++
		}
	}

	e.checkIfBetterDeldec(sc, 0, &bestFcb[smplCelpIdxMain], &bestFcbState[smplCelpIdxMain],
		&nrgThr[smplCelpIdxMain], wnrgPerPulse[smplCelpIdxMain])
	if fcbPulsesMax[smplCelpIdxFec] > 0 {
		e.checkIfBetterDeldec(sc, 0, &bestFcb[smplCelpIdxFec], &bestFcbState[smplCelpIdxFec],
			&nrgThr[smplCelpIdxFec], wnrgPerPulse[smplCelpIdxFec])
	}

	if fcbPulsesMax[smplCelpIdxMain] > 1 {
		for pulseNr := 2; pulseNr < int(fcbPulsesMax[smplCelpIdxMain]); pulseNr++ {
			sc.fcbCandidatesSize = 0
			sc.uniqueSgntrSize = 0
			fcbsSize := sc.fcbsSize
			// `idx` increments lockstep with the loop counter, so idx == i.
			for i := 0; i < fcbsSize; i++ {
				e.addPulse(sc, i, dAbs, dSign, int(surv[pulseNr-1]), i, lag, pitchSharp)
			}
			sc.swapRw()
			// Sort candidates by wnrg.
			candSize := sc.fcbCandidatesSize
			for i := 0; i < candSize; i++ {
				sc.q[i] = sc.fcbCandidates[i].wnrg
			}
			smplGetMaxiK(sc.q, sortIx[:], candSize, int(surv[pulseNr-1]))
			sc.fcbsSize = 0
			for i := 0; i < int(surv[pulseNr-1]); i++ {
				sc.fcbs[sc.fcbsSize] = sc.fcbCandidates[sortIx[i]]
				sc.fcbsSize++
			}
			e.checkIfBetterDeldec(sc, 0, &bestFcb[smplCelpIdxMain], &bestFcbState[smplCelpIdxMain],
				&nrgThr[smplCelpIdxMain], wnrgPerPulse[smplCelpIdxMain])
			if celpUsizeGe(fcbPulsesMax[smplCelpIdxFec], pulseNr) {
				e.checkIfBetterDeldec(sc, 0, &bestFcb[smplCelpIdxFec], &bestFcbState[smplCelpIdxFec],
					&nrgThr[smplCelpIdxFec], wnrgPerPulse[smplCelpIdxFec])
			}
		}
		// Last pulse (surv=1, MAIN only).
		sc.fcbCandidatesSize = 0
		sc.uniqueSgntrSize = 0
		fcbsSize := sc.fcbsSize
		for i := 0; i < fcbsSize; i++ {
			e.addPulse(sc, i, dAbs, dSign, 1, i, lag, pitchSharp)
		}
		sc.swapRw()
		bestIdx := 0
		maxWnrg := sc.fcbCandidates[0].wnrg
		for i := 1; i < sc.fcbCandidatesSize; i++ {
			if sc.fcbCandidates[i].wnrg > maxWnrg {
				maxWnrg = sc.fcbCandidates[i].wnrg
				bestIdx = i
			}
		}
		// check_if_better_deldec on a candidate (not in fcbs); its fcb_state_idx points into the
		// current read buffer.
		e.checkIfBetterDeldecCand(sc, bestIdx, &bestFcb[smplCelpIdxMain], &bestFcbState[smplCelpIdxMain],
			&nrgThr[smplCelpIdxMain], wnrgPerPulse[smplCelpIdxMain])
	}

	for r := smplCelpIdxFec; r < smplCelpIdxMain+1; r++ {
		bf := &bestFcb[r]
		bs := &bestFcbState[r]
		for i := 0; i < int(bf.nPulses); i++ {
			if bs.pulseSigns[i] > 0.0 {
				pulses[r][i] = 1 + int16(bs.pulsePositions[i])
			} else {
				pulses[r][i] = -(1 + int16(bs.pulsePositions[i]))
			}
		}
		if bf.signNew > 0.0 {
			pulses[r][bf.nPulses] = 1 + int16(bf.posNew)
		} else {
			pulses[r][bf.nPulses] = -(1 + int16(bf.posNew))
		}

		if bf.wnrg > 0.0 {
			wnrg[r] = bf.wnrg
			pn := int(bf.posNew)
			gainFromSearch[r] = bs.num[pn] / bs.den[pn]
			fcbWnrg[r] = bs.den[pn]
			nPulses[r] = int16(bf.nPulses) + 1
		} else {
			wnrg[r] = 0.0
			gainFromSearch[r] = 0.0
			fcbWnrg[r] = 0.0
			nPulses[r] = 0
		}
	}
}

// checkIfBetterDeldec is check_if_better_deldec where `fcb` is sc.fcbs[fcbsIdx]; copies the
// read-side state.
func (e *celpEncoder) checkIfBetterDeldec(
	sc *fcbSearchScratch,
	fcbsIdx int,
	bestFcb *fcb,
	bestFcbState *fcbState,
	nrgThr *float32,
	wnrgPerPulse float32,
) {
	*nrgThr += wnrgPerPulse
	f := &sc.fcbs[fcbsIdx]
	if f.wnrg > *nrgThr {
		*nrgThr = f.wnrg
		*bestFcb = *f
		*bestFcbState = sc.statesRead[f.fcbStateIdx]
	}
}

// checkIfBetterDeldecCand is the variant where `fcb` is a candidate in sc.fcbCandidates.
func (e *celpEncoder) checkIfBetterDeldecCand(
	sc *fcbSearchScratch,
	candIdx int,
	bestFcb *fcb,
	bestFcbState *fcbState,
	nrgThr *float32,
	wnrgPerPulse float32,
) {
	*nrgThr += wnrgPerPulse
	f := &sc.fcbCandidates[candIdx]
	if f.wnrg > *nrgThr {
		*nrgThr = f.wnrg
		*bestFcb = *f
		*bestFcbState = sc.statesRead[f.fcbStateIdx]
	}
}

// Tier 5: gain quantization.

func smplWnrg2(c, x []float32) float32 {
	return float32(x[0]*(float32(c[0]*x[0])+float32(c[1]*x[1]))) +
		float32(x[1]*(float32(c[2]*x[0])+float32(c[3]*x[1])))
}

func smplWnrg3(c, x []float32) float32 {
	return float32(x[0]*(float32(c[0]*x[0])+float32(c[1]*x[1])+float32(c[2]*x[2]))) +
		float32(x[1]*(float32(c[3]*x[0])+float32(c[4]*x[1])+float32(c[5]*x[2]))) +
		float32(x[2]*(float32(c[6]*x[0])+float32(c[7]*x[1])+float32(c[8]*x[2])))
}

func quantGainUv(gainFromSearch float32) int16 {
	gainDb := float32(20.0 * log10F32(gainFromSearch+1.0e-16))
	gainDb = minF32(maxF32(gainDb, smplUvGainMinDb), smplUvGainMaxDb)
	return f32ToI16(roundF32((gainDb - smplUvGainMinDb) / smplUvGainStepDb))
}

func fcbSynthesize(fcbSubfrlen int, pulses []int16, nPulses int, f []float32) {
	clear(f[:min(fcbSubfrlen, len(f))])
	for n := 0; n < nPulses; n++ {
		// sign = 1 + 2*(p>>15); pos = p*sign - 1 (arithmetic shift on the signed pulse).
		sign := int32(1) + 2*(int32(pulses[n])>>15)
		pos := (int32(pulses[n]) * sign) - 1
		f[pos] += float32(sign)
	}
}

// calcAcbGain returns the best acbg idx; fills acbg and dLtp.
func (e *celpEncoder) calcAcbGain(
	lResp int,
	acbBasis []float32,
	dLpc []float32,
	acbg *acbgParams,
	dLtp []float32,
) int32 {
	tbl := celpTables()
	fcbSubfrlen := e.fcbSubfrlen

	for m := 0; m < smplAcbgM; m++ {
		// acb_basis_phi[m] = symtoepl2(PhiFlip + MAX_SF_LEN - L_resp + 1, ...)
		cOff := smplMaxSfLen - lResp + 1
		var tmp [smplMaxSfLen]float32 // Rust: a fresh vec![0.0; fcb_subfrlen] per m
		smplMultSymtoepl2(e.phiFlip[cOff:], lResp, acbBasis[m*fcbSubfrlen:], tmp[:fcbSubfrlen], fcbSubfrlen)
		copy(acbg.acbBasisPhi[m*fcbSubfrlen:m*fcbSubfrlen+fcbSubfrlen], tmp[:fcbSubfrlen])
		for i := 0; i < smplAcbgM; i++ {
			acbg.phiAcb[m*smplAcbgM+i] = celpSmplDotProd(acbBasis[i*fcbSubfrlen:], acbg.acbBasisPhi[m*fcbSubfrlen:], fcbSubfrlen)
		}
		acbg.dAcbLpc[m] = celpSmplDotProd(acbBasis[m*fcbSubfrlen:], dLpc, fcbSubfrlen)
	}

	bestRd := float32(1e30)
	bestAcbgIdx := int32(0)
	transitionIdx := int32(0)
	if e.prevAcbIdx[smplCelpIdxMain] != -1 {
		transitionIdx = e.prevAcbIdx[smplCelpIdxMain] + 1
	}
	acbgInvProbFull := tbl.acbgInvProbHr[:]
	cb := smplCbAcbgainsHrQ14[:]
	if e.lowRate {
		acbgInvProbFull = tbl.acbgInvProbLr[:]
		cb = smplCbAcbgainsLrQ14[:]
	}
	acbgInvProb := acbgInvProbFull[int(transitionIdx)*smplAcbgN:]
	scQ14 := float32(1.0) / float32(int32(1)<<14)
	var acbGains [smplAcbgM]float32
	for n := 0; n < smplAcbgN; n++ {
		for m := 0; m < smplAcbgM; m++ {
			acbGains[m] = float32(cb[n*smplAcbgM+m]) * scQ14
		}
		werrOut := acbg.werrIn + smplWnrg2(acbg.phiAcb[:], acbGains[:]) -
			float32(2.0*(float32(acbg.dAcbLpc[0]*acbGains[0])+float32(acbg.dAcbLpc[1]*acbGains[1])))
		rd := werrOut * acbgInvProb[n]
		if rd < bestRd {
			bestRd = rd
			bestAcbgIdx = int32(n)
		}
	}

	// fcb target signal
	g0 := float32(-cb[int(bestAcbgIdx)*smplAcbgM]) * scQ14
	celpSmplAddScaleVec(dLpc, acbg.acbBasisPhi[:], dLtp, fcbSubfrlen, g0)
	g1 := float32(-cb[int(bestAcbgIdx)*smplAcbgM+1]) * scQ14
	celpSmplAddScaleVecInplace(acbg.acbBasisPhi[fcbSubfrlen:], dLtp, fcbSubfrlen, g1)

	return bestAcbgIdx
}

// calcGainsV (g_acb_rd_mu > 0 always -> RD branch). Returns the chosen voiced fcb gain.
func (e *celpEncoder) calcGainsV(
	fcbWnrg float32,
	gainFromSearch float32,
	excFcb []float32,
	dLpc []float32,
	acbg *acbgParams,
	rateIdx int,
	acbIdx *[smplCelpMaxRates]int16,
	fcbIdx *[smplCelpMaxRates]int16,
) float32 {
	tbl := celpTables()
	fcbSubfrlen := e.fcbSubfrlen

	fcbgain := maxF32(gainFromSearch, 0.0)
	gainDb := float32(20.0 * log10F32(fcbgain+1.0e-16))
	gainDb = minF32(maxF32(gainDb, smplVGainMinDb), smplVGainMaxDb)
	vMax, vMin, vStep := smplVGainMaxDb, smplVGainMinDb, smplVGainStepDb
	maxGainIdx := f32ToI32(roundF32((vMax - vMin) / vStep))

	// g_acb_rd_mu > 0 -> always RD branch
	bestAcbgIdx := int32(0)
	bestFcbgIdx := int32(0)

	var acbFcb [smplAcbgM]float32
	for i := 0; i < smplAcbgM; i++ {
		acbFcb[i] = celpSmplDotProd(acbg.acbBasisPhi[i*fcbSubfrlen:], excFcb, fcbSubfrlen)
	}
	// Phi_all is (M+1)x(M+1) row-major.
	var phiAll [(smplAcbgM + 1) * (smplAcbgM + 1)]float32
	const stride = smplAcbgM + 1
	for i := 0; i < smplAcbgM; i++ {
		for j := 0; j < smplAcbgM; j++ {
			phiAll[i*stride+j] = acbg.phiAcb[i*smplAcbgM+j]
		}
	}
	for i := 0; i < smplAcbgM; i++ {
		phiAll[i*stride+smplAcbgM] = acbFcb[i]
		phiAll[smplAcbgM*stride+i] = acbFcb[i]
	}
	phiAll[smplAcbgM*stride+smplAcbgM] = fcbWnrg

	var dall [smplAcbgM + 1]float32
	copy(dall[:smplAcbgM], acbg.dAcbLpc[:])
	dall[smplAcbgM] = celpSmplDotProd(dLpc, excFcb, fcbSubfrlen)

	var gainIdxs [nGainSteps]int32
	var fcbgains [nGainSteps]float32
	var fcbgInvProb [nGainSteps]float32
	firstGainIdx := max(f32ToI32(floorF32((gainDb-smplVGainMinDb)/smplVGainStepDb))-(int32(nGainSteps)-1)/2, 0)
	firstGainIdx = min(firstGainIdx, maxGainIdx-1)
	offset := f32ToI32(floorF32((vMin - vMax) / vStep))
	for i := 0; i < nGainSteps; i++ {
		gainIdxs[i] = firstGainIdx + int32(i)
		fcbgains[i] = tbl.fcbgainsV[gainIdxs[i]]
		if e.prevFcbIdx[rateIdx] == -1 {
			fcbgInvProb[i] = tbl.fcbgVInvProb[gainIdxs[i]]
		} else {
			delta := e.prevFcbIdx[rateIdx] - gainIdxs[i]
			cmfIdx := delta - offset
			fcbgInvProb[i] = tbl.fcbgVDeltaInvProb[cmfIdx]
		}
	}

	bestRd := float32(1e30)
	transitionIdx := int32(0)
	if e.prevAcbIdx[rateIdx] != -1 {
		transitionIdx = e.prevAcbIdx[rateIdx] + 1
	}
	cb := smplCbAcbgainsHrQ14[:]
	acbgInvProbFull := tbl.acbgInvProbHr[:]
	if e.lowRate {
		cb = smplCbAcbgainsLrQ14[:]
		acbgInvProbFull = tbl.acbgInvProbLr[:]
	}
	acbgInvProb := acbgInvProbFull[int(transitionIdx)*smplAcbgN:]
	scQ14 := float32(1.0) / float32(int32(1)<<14)
	for n := 0; n < smplAcbgN; n++ {
		var gains [smplAcbgM + 1]float32
		for m := 0; m < smplAcbgM; m++ {
			gains[m] = float32(cb[n*smplAcbgM+m]) * scQ14
		}
		for i := 0; i < nGainSteps; i++ {
			gains[smplAcbgM] = fcbgains[i]
			werrOut := acbg.werrIn + smplWnrg3(phiAll[:], gains[:]) -
				float32(2.0*(float32(dall[0]*gains[0])+float32(dall[1]*gains[1])+float32(dall[2]*gains[2])))
			rd := werrOut * fcbgInvProb[i] * acbgInvProb[n]
			if rd < bestRd {
				bestRd = rd
				bestAcbgIdx = int32(n)
				bestFcbgIdx = gainIdxs[i]
			}
		}
	}
	acbIdx[rateIdx] = int16(bestAcbgIdx)
	fcbIdx[rateIdx] = int16(bestFcbgIdx)

	fcbIdx[rateIdx] = min(max(fcbIdx[rateIdx], 0), int16(maxGainIdx))
	return tbl.fcbgainsV[fcbIdx[rateIdx]]
}

// Tier 6: main per-subframe encoder.

func (e *celpEncoder) encodeSubframe(
	resLpc []float32,
	predcoef *[17]float32,
	percWghtResp []float32,
	lags []float32,
	subfrImportance [smplCelpMaxRates]float32,
	fcbPulsesMax [smplCelpMaxRates]int16,
	surv []int16,
) celpSubframeOut {
	lResp := e.percRespLen
	fcbSubfrlen := e.fcbSubfrlen
	voiced := lags[1] > 0.0

	// imp_lpc = filt_ar16(perc_wght_resp, L_resp, predcoef) * hanning_win.
	// The 16-sample AR lead (impLpcBuf[0..SMPL_LPC_ORDER]) is zeroed once at construction and
	// never written by the filter (it only writes indices >= SMPL_LPC_ORDER), so it stays zero
	// across calls, matching the reference's one-time zero of imp_lpc_.
	smplFiltAr16(percWghtResp, lResp, predcoef[:], smplLpcOrder, e.impLpcBuf[:])
	smplMulVecInplace(e.hanningWin, e.impLpcBuf[smplLpcOrder:], lResp)
	impLpc := e.impLpcBuf[smplLpcOrder : smplLpcOrder+lResp]

	// imp_lpc_rev: buffer of 2*MAX_L_RESP-1, logical ptr at MAX_L_RESP-1.
	impLpcRev := subframeScratchZeroed(e.sf.impLpcRev[:], 2*smplMaxLResp-1)
	revBase := smplMaxLResp - 1
	// reverse_into(imp, &imp_lpc_rev[rev_base..], L_resp)
	for i := 0; i < lResp; i++ {
		impLpcRev[revBase+i] = impLpc[lResp-i-1]
	}
	// Zero the (L_resp-1) lead before rev_base; already zero, kept explicit for clarity.
	for i := 0; i < lResp-1; i++ {
		impLpcRev[revBase-(lResp-1)+i] = 0.0
	}
	// Phi = perc_filt_ma(imp_lpc_rev, L_resp, imp_lpc, L_resp)
	{
		var phi [smplMaxSfLen]float32
		e.percFiltMa(impLpcRev, revBase, lResp, impLpc, lResp, phi[:])
		smplReverse(phi[:], lResp)
		for i := lResp; i < fcbSubfrlen; i++ {
			phi[i] = 0.0
		}
		e.phi = phi
	}
	// PhiFlip
	clear(e.phiFlip[:])
	e.phiFlip[smplMaxSfLen] = e.phi[0]
	for i := 0; i < lResp+1; i++ {
		e.phiFlip[smplMaxSfLen-i] = e.phi[i]
		e.phiFlip[smplMaxSfLen+i] = e.phi[i]
	}

	// d_lpc = symtoepl2(PhiFlip + MAX_SF_LEN - L_resp + 1, L_resp, res_lpc, fcb_subfrlen)
	// res_lpc must be readable up to fcb_subfrlen + L_resp (zero padded). Build a padded copy.
	resLpcPad := subframeScratchZeroed(e.sf.resLpcPad[:], fcbSubfrlen+lResp+1)
	copy(resLpcPad[:fcbSubfrlen], resLpc[:fcbSubfrlen])
	dLpc := subframeScratchZeroed(e.sf.dLpc[:], smplMaxSfLen)
	{
		cOff := smplMaxSfLen - lResp + 1
		smplMultSymtoepl2(e.phiFlip[cOff:], lResp, resLpcPad, dLpc, fcbSubfrlen)
	}

	var acbg acbgParams
	zirLpc := subframeScratchZeroed(e.sf.zirLpc[:], smplMaxSfLen)

	if !e.ignoreZir {
		// zir_lpc_tmp buffer: MAX_SF_LEN + MAX_L_RESP - 1, logical ptr at MAX_L_RESP-1
		var zirTmp [smplMaxSfLen + smplMaxLResp - 1]float32
		zt := smplMaxLResp - 1
		// Ht_zir buffer: 2*MAX_L_RESP-1, ptr at MAX_L_RESP-1
		var htZir [2*smplMaxLResp - 1]float32
		ht := smplMaxLResp - 1

		// zir_lpc_tmp[0..L_resp]=0 (already)
		stateLen := max(smplLpcOrder, lResp-1)
		// copy state_wght[fcb_subfrlen - state_len .. fcb_subfrlen] into the state_len samples
		// before zir_lpc_tmp[0]. state_wght logical start at stateWghtBuf[SMPL_LPC_ORDER].
		for i := 0; i < stateLen; i++ {
			zirTmp[zt-stateLen+i] = e.stateWghtBuf[smplLpcOrder+(fcbSubfrlen-stateLen)+i]
		}
		// In-place AR over zir_tmp at base zt (x==y). The input term x[n] for n in 0..L_resp is
		// the zeroed region, read before being overwritten, while y[n-16..n-1] supply the
		// just-copied history; writing y[n] after reading x[n] keeps the aliasing safe.
		for nn := 0; nn < lResp; nn++ {
			res := zirTmp[zt+nn] // x[nn] (currently 0 in [0..L_resp])
			for i := 0; i < 16; i++ {
				res -= float32(predcoef[16-i] * zirTmp[zt+nn-16+i])
			}
			zirTmp[zt+nn] = res
		}
		// zir_lpc = perc_filt_ma(zir_lpc_tmp, L_resp, perc_wght_resp, L_resp)
		e.percFiltMa(zirTmp[:], zt, lResp, percWghtResp, lResp, zirLpc)

		// reverse_into(zir_lpc, zir_lpc_tmp, L_resp)
		for i := 0; i < lResp; i++ {
			zirTmp[zt+i] = zirLpc[lResp-i-1]
		}
		for i := 0; i < lResp-1; i++ {
			zirTmp[zt-(lResp-1)+i] = 0.0
		}
		// Ht_zir = perc_filt_ma(zir_lpc_tmp, L_resp, imp_lpc, L_resp)
		e.percFiltMa(zirTmp[:], zt, lResp, impLpc, lResp, htZir[ht:])
		smplReverse(htZir[ht:], lResp)

		if voiced {
			acbg.werrIn = celpSmplDotProd(dLpc, resLpc, fcbSubfrlen) +
				float32(2.0*celpSmplDotProd(htZir[ht:], resLpc, lResp)) +
				celpSmplNrg(zirLpc, lResp)
		} else {
			acbg.werrIn = 0.0
		}
		// d_lpc[0..L_resp] += Ht_zir
		for i := 0; i < lResp; i++ {
			dLpc[i] += htZir[ht+i]
		}
	} else {
		clear(zirLpc[:lResp])
		if voiced {
			acbg.werrIn = celpSmplDotProd(dLpc, resLpc, fcbSubfrlen)
		} else {
			acbg.werrIn = 0.0
		}
	}

	// ACB
	acbBasis := subframeScratchZeroed(e.sf.acbBasis[:], smplMaxSfLen*smplAcbgM)
	acb := subframeScratchZeroed(e.sf.acb[:], smplMaxSfLen)
	dLtp := subframeScratchZeroed(e.sf.dLtp[:], smplMaxSfLen)
	acbIdx := [smplCelpMaxRates]int16{-1, -1}

	if voiced {
		smplSynLtpBasis(lags, fcbSubfrlen/smplLagSubfrlen, e.acbState, e.acbStateLen, acbBasis)
		idx := e.calcAcbGain(lResp, acbBasis, dLpc, &acbg, dLtp)
		acbIdx[smplCelpIdxMain] = int16(idx)
		var acbGain [smplAcbgM]float32
		celpAcbDequant(e.lowRate, int32(acbIdx[smplCelpIdxMain]), &acbGain)
		celpAcbSynthesize(fcbSubfrlen, acbBasis, &acbGain, acb)
		acbIdx[smplCelpIdxFec] = acbIdx[smplCelpIdxMain]
	}

	// Weighted target + thresholds.
	// wtgt_tmp buffer: MAX_SF_LEN + 2*MAX_L_RESP - 1, ptr at MAX_L_RESP-1
	wtgtTmp := subframeScratchZeroed(e.sf.wtgtTmp[:], smplMaxSfLen+2*smplMaxLResp-1)
	wt := smplMaxLResp - 1
	wtgt := subframeScratchZeroed(e.sf.wtgt[:], smplMaxSfLen+smplMaxLResp)
	copy(wtgtTmp[wt:wt+fcbSubfrlen], resLpc[:fcbSubfrlen])
	if voiced {
		// wtgt_tmp += -RATE_ACB_SCALE * acb
		for i := 0; i < fcbSubfrlen; i++ {
			wtgtTmp[wt+i] += float32(-smplRateAcbScale * acb[i])
		}
	}
	for i := 0; i < lResp; i++ {
		wtgtTmp[wt+fcbSubfrlen+i] = 0.0
	}
	for i := 0; i < lResp-1; i++ {
		wtgtTmp[wt-(lResp-1)+i] = 0.0
	}
	e.percFiltMa(wtgtTmp, wt, fcbSubfrlen+lResp, impLpc, lResp, wtgt)
	for i := 0; i < lResp; i++ {
		wtgt[i] += zirLpc[i]
	}
	nrgWtgt := celpSmplNrg(wtgt, fcbSubfrlen+lResp)
	var wnrgPerPulse [smplCelpMaxRates]float32
	for r := 0; r < smplCelpMaxRates; r++ {
		wnrgPerPulse[r] = nrgWtgt / (subfrImportance[r] + 1.0e-3)
	}
	iLag := f32ToI32(lags[(fcbSubfrlen/smplLagSubfrlen)-1])

	var nPulses [smplCelpMaxRates]int16
	var gainFromSearch [smplCelpMaxRates]float32
	var fcbWnrg [smplCelpMaxRates]float32
	var wnrg [smplCelpMaxRates]float32
	var pulses [smplCelpMaxRates][smplMaxPulsesPerSf]int16

	if fcbPulsesMax[smplCelpIdxMain] > 0 {
		target := dLpc
		if voiced {
			target = dLtp
		}
		useGreedy := fcbPulsesMax[smplCelpIdxMain]-1 > 0 &&
			surv[int(fcbPulsesMax[smplCelpIdxMain]-2)] == 1 &&
			!e.lowRate
		if useGreedy {
			e.smplFcbSearch(target, &wnrgPerPulse, &fcbPulsesMax, &pulses, &nPulses, &wnrg,
				&gainFromSearch, &fcbWnrg)
		} else {
			var lr float32
			if e.lowRate {
				lr = 1.0
			}
			ps := smplPitchSharpeningCoef * lr
			e.smplFcbSearchDeldec(&e.fcbSearch, target, ps, iLag, &wnrgPerPulse, &fcbPulsesMax, surv,
				&pulses, &nPulses, &wnrg, &gainFromSearch, &fcbWnrg)
		}
	}

	// Per-rate gain quant + exc_fcb. fcbgain carries the last assigned (MAIN) value.
	gainIdx := [smplCelpMaxRates]int16{-1, -1}
	var fcbgain float32
	excFcb := subframeScratchZeroed(e.sf.excFcb[:], smplMaxSfLen)
	// Pooled twin of `excFcb`, kept a SEPARATE buffer as in Rust (there it preserves codegen;
	// here it simply mirrors the structure).
	excFcbRaw := subframeScratchZeroed(e.sf.excFcbRaw[:], smplMaxSfLen)
	tbl := celpTables()
	for r := 0; r < smplCelpMaxRates; r++ {
		fcbSynthesize(fcbSubfrlen, pulses[r][:], int(nPulses[r]), excFcbRaw)
		copy(excFcb[:fcbSubfrlen], excFcbRaw[:fcbSubfrlen])
		if nPulses[r] > 0 {
			if voiced {
				if e.lowRate {
					smplPitchSharp(excFcb, int(iLag), fcbSubfrlen)
				}
				fcbgain = e.calcGainsV(fcbWnrg[r], gainFromSearch[r], excFcb, dLpc, &acbg, r,
					&acbIdx, &gainIdx)
			} else {
				gainIdx[r] = quantGainUv(gainFromSearch[r])
				fcbgain = tbl.fcbgainsUv[gainIdx[r]]
			}
			smplScaleVecInplace(excFcb, fcbSubfrlen, fcbgain)
		}
	}

	// exc_lpc = exc_fcb (last r = MAIN); for voiced add the ACB contribution. The reference's
	// trailing `smpl_sub_vec_inplace(acb, res_ltp)` mutates only the throwaway plotting buffer
	// `res_ltp`, NOT exc_lpc, so the excitation fed back into the ACB state is exactly
	// exc_fcb + acb.
	var out celpSubframeOut
	excLpc := out.excLpc[:fcbSubfrlen]
	copy(excLpc, excFcb[:fcbSubfrlen])
	if voiced {
		var acbGain [smplAcbgM]float32
		celpAcbDequant(e.lowRate, int32(acbIdx[smplCelpIdxMain]), &acbGain)
		celpAcbSynthesize(fcbSubfrlen, acbBasis, &acbGain, acb)
		smplAddVecInplace(acb, excLpc, fcbSubfrlen)
	}

	// Update adaptive codebook state: shift down by fcb_subfrlen (keeping acb_state_len -
	// 2*fcb_subfrlen elements) and write exc_lpc at acb_state_len - 2*fcb_subfrlen, leaving the
	// trailing fcb_subfrlen untouched (an in-place shift, not an append-at-end).
	copy(e.acbState[0:], e.acbState[fcbSubfrlen:e.acbStateLen-fcbSubfrlen])
	writeOff := e.acbStateLen - 2*fcbSubfrlen
	copy(e.acbState[writeOff:writeOff+fcbSubfrlen], excLpc[:fcbSubfrlen])

	// Update ZIR state.
	if !e.ignoreZir {
		var lpcResErr [smplMaxSfLen]float32
		smplSubVec(resLpc, excLpc, lpcResErr[:], fcbSubfrlen)
		// state_wght[-16..0] = state_err_lpc_syn  (i.e. stateWghtBuf[0..16])
		copy(e.stateWghtBuf[:smplLpcOrder], e.stateErrLpcSyn[:])
		// filt_ar16(lpc_res_err, fcb_subfrlen, predcoef, state_wght) with y_base=SMPL_LPC_ORDER
		smplFiltAr16(lpcResErr[:], fcbSubfrlen, predcoef[:], smplLpcOrder, e.stateWghtBuf[:])
		// state_err_lpc_syn = state_wght[fcb_subfrlen-16 .. fcb_subfrlen]
		for i := 0; i < smplLpcOrder; i++ {
			e.stateErrLpcSyn[i] = e.stateWghtBuf[smplLpcOrder+(fcbSubfrlen-smplLpcOrder)+i]
		}
	}

	// Packet-boundary / prev-idx state.
	e.subfrCnt++
	if e.subfrCnt == e.subfrPerPacket {
		for r := 0; r < smplCelpMaxRates; r++ {
			e.prevAcbIdx[r] = -1
			e.prevFcbIdx[r] = -1
		}
		e.subfrCnt = 0
	} else {
		for r := 0; r < smplCelpMaxRates; r++ {
			if voiced {
				e.prevAcbIdx[r] = int32(acbIdx[r])
				e.prevFcbIdx[r] = int32(gainIdx[r])
			} else {
				e.prevAcbIdx[r] = -1
				e.prevFcbIdx[r] = -1
			}
		}
	}
	e.fcbgain = fcbgain

	out.pulses = pulses
	out.nPulses = nPulses
	out.acbIdx = acbIdx
	out.gainIdx = gainIdx
	return out
}

// Tier 7: survivor distribution.

func smplDistributeFcbSurv(numsurv []int16, maxPulses int32, totSurv int32) {
	if maxPulses <= 1 {
		numsurv[0] = 1
		return
	}
	for i := 0; i < int(maxPulses); i++ {
		numsurv[i] = 1
	}
	sumSurv := maxPulses
	extraSurv := totSurv - maxPulses
	extra := min(extraSurv/(maxPulses-1), smplFcbSrvMax-1)
	for i := 0; i < int(maxPulses-1); i++ {
		numsurv[i] += int16(extra)
	}
	sumSurv += extra * (maxPulses - 1)
	ix := maxPulses - 2
	for sumSurv < totSurv {
		if int32(numsurv[ix]) < smplFcbSrvMax {
			numsurv[ix]++
			sumSurv++
		}
		ix--
		if ix < 0 {
			break
		}
	}
}

// cbAcbgainsHrQ14 is the high-rate ACB gain codebook (`smpl_cb_acbgains_hr_Q14`), for the decoder
// ACB synthesis.
func cbAcbgainsHrQ14() []int16 { return smplCbAcbgainsHrQ14[:] }

// cbAcbgainsLrQ14 is the low-rate ACB gain codebook (`smpl_cb_acbgains_lr_Q14`).
func cbAcbgainsLrQ14() []int16 { return smplCbAcbgainsLrQ14[:] }
