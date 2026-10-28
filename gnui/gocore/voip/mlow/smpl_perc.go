// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_perc.rs, MIT.
//
// MLow perceptual-weighting front-end: FFT-based perceptual autocorrelation to a perceptual LPC
// response, plus the per-subframe pulse budget and importance from the bitrate controller, with the
// leaf LPC/vector/window helpers they need.
//
// A self-contained mixed-radix complex FFT avoids a C-only FFT dependency (not portable to
// wasm32/esp32). The spectrum is re-packed into the ordered real layout the rest of the codec
// expects (`f[0]`=DC, `f[1]`=Nyquist, then interleaved `[re,im]` for bins 1..N/2-1) so `smth_filt`
// and the inverse index identical bins and the returned autocorrelation `R[]` keeps the same
// structure.
// PERCW_NFFT is 576 = 2^6 * 3^2, NOT a power of two, hence the mixed-radix (radix-2/3 + naive base)
// DFT.
//
// Self-contained on purpose: small leaf helpers (vector ops, window generation, ac2rc/rc2a) are
// duplicated locally rather than shared with `smpl_celp.rs`, so this front-end can be wired in
// separately without coupling.
//
// Leaf constants/helpers (SMPL_PERC_RESP_LEN, SMPL_TRUE, smpl_nrg) are intentionally duplicated
// locally and one bitrate-controller state field is parity scaffolding the wired-in path doesn't
// read yet.
//
// Clamp is written as min(max(x, lo), hi); the `.max().min()` form preserves that operand order.
// The `% k == 0`, `(k+1)/2`, `>= x+1`, and range comparisons below stay in their literal arithmetic
// form to keep the bit-exact integer behavior obvious.
//
// Go port notes: SMPL_PI (3.1415926535897 as f32) is the shared smplPi, the same f32. Every float
// product that feeds an addition or subtraction is wrapped in float32(...)/float64(...) so Go
// cannot fuse it into an FMA.

package mlow

import "fmt"

// Constants

const percwNfft = 512 + 64 // 576
const percwFsKhz float32 = 16.0

const smplPercMaskSmth float32 = 0.1158
const smplPercMelFcHz float32 = 320.0

const smplWinnextWbLongLen = 16 * 4 // 64
const smplWinprevPercLen = 16 * 12  // 192
const smplPercWin1_10msLen = 192
const smplPercWin1_20msLen = 352

const smplPercReg float32 = 1e-3

const smplTrue int32 = 1
const smplFalse int32 = 0

// Bitrate-controller constants
const smplRateContScale float32 = 26.0
const smplE float32 = 2.7182818284590

// SmplFrameTypes
const backgroundNoise = 0
const unvoiced = 1
const voiced = 2

// [lowRate][BACKGROUND_NOISE/UNVOICED/VOICED]
var smplMaxPulsesPerFrame = [2][3]uint8{{80, 160, 160}, {16, 32, 32}}

// [framelenidx][lowrate][8]
var smplRateControlModelComp5 = [4][2][8]float32{
	{
		{
			5.166876656946171,
			-8.981699804753452,
			0.07280811614105594,
			0.1301196310618402,
			-0.01597680442864421,
			1.7601470147884113,
			-3.8161195433141755,
			0.3038629198331684,
		},
		{
			-71.71229978402292,
			14.197572549553076,
			-0.9863630205846172,
			0.032124893286072924,
			-0.0003538411576874928,
			1.803705259861388e-11,
			10.0,
			1.2454667523627154,
		},
	},
	{
		{
			32.5371190670542,
			-41.270234279452104,
			10.490270829170875,
			-1.102121269442237,
			0.03848319274046071,
			3.405326741403831,
			-5.102658181889428,
			0.2141935195026695,
		},
		{
			-177.10486363500775,
			43.952329593498376,
			-3.7049735533247454,
			0.14239771116996938,
			-0.001919963993993193,
			7.953695588409639e-6,
			5.220317075476664,
			0.6435364076926223,
		},
	},
	{
		{
			-79.2663194911617,
			45.00981883522089,
			-10.063311543498518,
			1.2311531056576501,
			-0.06023559069137118,
			0.059204788212259364,
			3.033961466462233,
			1.0111383197827808,
		},
		{
			-122.04861900525415,
			31.62096398905459,
			-2.613237037423586,
			0.10050433143234094,
			-0.0013233009240188039,
			2.14859438836692e-7,
			1.9077791307787761,
			0.7059420500333776,
		},
	},
	{
		{
			-182.64255084224325,
			122.90780796179816,
			-31.308790671748525,
			3.7850563849431462,
			-0.1750480676903051,
			0.05399618467364628,
			3.009451055091342,
			1.1243365512229038,
		},
		{
			-132.4565456943888,
			34.361297004632966,
			-2.7956546289118887,
			0.10428149547078584,
			-0.001322667891395693,
			2.678747426340249e-6,
			6.9940208056381925,
			0.7551244069345737,
		},
	},
}

// [framelenidx][lowrate]
var smplRateControlThrsComp5 = [4][2]uint16{{7500, 10000}, {4500, 5750}, {4000, 5000}, {4000, 4750}}

// Perceptual emphasis coefficients, exposed for the caller selecting voiced vs unvoiced emphasis.
var smplPercEmphV = [2]float32{-0.72, -0.77}
var smplPercEmphUv = [2]float32{-0.55, -0.6}

// Leaf vector helpers, duplicated locally.

func percSmplNrg(x []float32) float32 {
	nrg := float32(0.0)
	for _, v := range x {
		nrg += float32(v * v)
	}
	return nrg
}

// out = input .* win
func smplMulVec(input, win, out []float32, l int) {
	for i := 0; i < l; i++ {
		out[i] = win[i] * input[i]
	}
}

// y = x * g
func percSmplScaleVec(x, y []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		y[i] = x[i] * g
	}
}

// y = x0 + g * x1
func percSmplAddScaleVec(x0, x1, y []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		y[i] = x0[i] + float32(g*x1[i])
	}
}

// y += g * x
func percSmplAddScaleVecInplace(x, y []float32, l int, g float32) {
	for i := 0; i < l; i++ {
		y[i] += float32(g * x[i])
	}
}

func percSmplSigmoid(x float32) float32 {
	if x > 80.0 {
		return 1.0
	}
	if x < -80.0 {
		return 0.0
	}
	return 1.0 / (1.0 + expF32(-x))
}

// 2nd-order MA filter, may be non-monic. State sits in `state[0..2]`.
func percSmplFiltMa2(x []float32, n int, coef []float32, state *[2]float32, y []float32) {
	if coef[0] == 1.0 {
		// monic
		percSmplAddScaleVec(x[1:], x, y[1:], n-1, coef[1])
	} else {
		percSmplScaleVec(x, y, n, coef[0])
		percSmplAddScaleVecInplace(x, y[1:], n-1, coef[1])
	}
	percSmplAddScaleVecInplace(x, y[2:], n-2, coef[2])
	y[0] = float32(coef[0]*x[0]) + float32(coef[1]*state[0]) + float32(coef[2]*state[1])
	y[1] += float32(coef[2] * state[0])
	// The state update is dead for our single call (state is a stack temp), so we drop it.
}

// autocorrelation -> reflection coeffs, Levinson, double precision.
func smplAc2rcDbl(corr []float64, order int, reg float64, rc []float32) {
	if !(order < smplMaxLResp) {
		panic("assertion failed: order < SMPL_MAX_L_RESP")
	}
	var c0 [smplMaxLResp]float64
	var c1 [smplMaxLResp]float64
	copy(c0[:order+1], corr[:order+1])
	c0[0] *= 1.0 + reg
	copy(c1[:order+1], c0[:order+1])
	for i := range rc[:order] {
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

// Float wrapper that promotes to double precision before Levinson.
func smplAc2rc(corr []float32, order int, reg float32, rc []float32) {
	if !(order < smplMaxLResp) {
		panic("assertion failed: order < SMPL_MAX_L_RESP")
	}
	var corrDbl [smplMaxLResp]float64
	for i := 0; i < order+1; i++ {
		corrDbl[i] = float64(corr[i])
	}
	smplAc2rcDbl(corrDbl[:], order, float64(reg), rc)
}

// reflection coeffs -> LPC polynomial A[0..order].
func smplRc2a(rc []float32, order int, a []float32) {
	for i := range a[1 : order+1] {
		a[1+i] = 0.0
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

// Self-contained mixed-radix complex FFT (no external deps).

type cpx struct {
	re float32
	im float32
}

func cpxZero() cpx {
	return cpx{re: 0.0, im: 0.0}
}

func (c cpx) add(o cpx) cpx {
	return cpx{
		re: c.re + o.re,
		im: c.im + o.im,
	}
}

func (c cpx) mul(o cpx) cpx {
	return cpx{
		re: float32(c.re*o.re) - float32(c.im*o.im),
		im: float32(c.re*o.im) + float32(c.im*o.re),
	}
}

// smallestFactor is the smallest prime factor of `n` (>= 2). For our use n is always composite of 2
// and 3.
func smallestFactor(n int) int {
	if n%2 == 0 {
		return 2
	}
	p := 3
	for p*p <= n {
		if n%p == 0 {
			return p
		}
		p += 2
	}
	return n
}

// fftLevel is one level of the factorization chain: at length `n` the recursion splits into `p`
// sub-DFTs of length `m = n/p`. Every node at a given recursion depth sees the same `n`, so the
// chain is a flat list indexed by depth.
type fftLevel struct {
	n int
	p int
	m int
	// Combine-step twiddles for this level, indexed `k * p + q` (length `n * p`).
	tw []cpx
}

// fftTwiddles is the precomputed factorization plan and twiddle factors for one `(top-N, sign)`
// FFT. Both depend only on `(n, index)`, never on the input, so they are computed once at init and
// read in the hot loop.
//
// The plan is why the levels are a flat slice indexed by depth rather than an `(n, table)`
// association list: `n` is fixed per stream (512 for LPC analysis, 576 for the perceptual model),
// so the whole chain of `(n, p, m)` is known at init. Re-deriving `p` with `smallest_factor` and
// re-finding the twiddle table by scanning for `n` at every node cost 5361 trial divisions and 5361
// linear scans per encoded frame -- the recursion visits 511 nodes per 512-point transform and 319
// per 576-point one, at 15 transforms per frame. Indexing by depth removes both without touching a
// single arithmetic operation, so the output stays bit-identical.
//
// Each table reproduces the EXACT inline angle arithmetic (same f32 op order, same std cos/sin), so
// reading from it is bit-identical to the inline recompute; proven by `twiddle_table_is_bit_exact`.
type fftTwiddles struct {
	// The chain from the top length down to (but excluding) the prime base, indexed by depth.
	levels []fftLevel
	// The prime length the chain terminates in, and its base-DFT twiddles indexed `k * n + j`.
	baseN int
	base  []cpx
}

// combineTwiddle computes the combine twiddle for `(n, k, q, sign)` exactly as the inline butterfly
// does.
func combineTwiddle(n, k, q int, sign float32) cpx {
	ang := sign * 2.0 * smplPi * float32(k) * float32(q) / float32(n)
	im, re := sincosF32(ang)
	return cpx{re: re, im: im}
}

// baseTwiddle computes the prime-base twiddle for `(n, k, j, sign)` exactly as the inline base DFT
// does (two-step: `ang_k = sign*2π*k/n` then `ang = ang_k * j`).
func baseTwiddle(n, k, j int, sign float32) cpx {
	angK := sign * 2.0 * smplPi * float32(k) / float32(n)
	ang := angK * float32(j)
	im, re := sincosF32(ang)
	return cpx{re: re, im: im}
}

// newFftTwiddles builds the plan and twiddle tables for the chain of `n` values the recursion visits
// from `top` down, for the given `sign`.
func newFftTwiddles(top int, sign float32) fftTwiddles {
	var levels []fftLevel
	n := top
	baseN, base := 1, []cpx(nil)
	for n > 1 {
		p := smallestFactor(n)
		if p == n {
			baseN = n
			base = make([]cpx, 0, n*n)
			for k := 0; k < n; k++ {
				for j := 0; j < n; j++ {
					base = append(base, baseTwiddle(n, k, j, sign))
				}
			}
			break
		}
		tw := make([]cpx, 0, n*p)
		for k := 0; k < n; k++ {
			for q := 0; q < p; q++ {
				tw = append(tw, combineTwiddle(n, k, q, sign))
			}
		}
		levels = append(levels, fftLevel{n: n, p: p, m: n / p, tw: tw})
		n /= p
	}
	return fftTwiddles{
		levels: levels,
		baseN:  baseN,
		base:   base,
	}
}

// fftScratch is the reusable FFT workspace owned by `percModelState`, so the per-frame perc model
// runs its ~12 forward+backward FFTs of N=PERCW_NFFT without re-allocating the recursion scratch
// every call and without recomputing the input-independent butterfly cos/sin.
//
// A real transform of length `n` is one complex transform of length `m = n/2` plus an O(m)
// recombination, so this plan is sized for `m`, not `n`: `wn` is the recombination twiddle,
// `twFwd`/`twBwd` the butterfly twiddles for the two signs, `arena` backs `fftRec`'s per-level
// `sub` buffers, and `z`/`zt` hold the packed input and its transform. Reuse never touches the
// arithmetic, so the output is bit-identical across calls.
type fftScratch struct {
	// `W_n^k = exp(-2*pi*i*k/n)` for `k` in `0..n/2`: the recombination twiddle. Built from the
	// same `combineTwiddle` the butterflies use, so both stages share one angle formula.
	wn    []cpx
	twFwd fftTwiddles // length n/2, sign = -1.0
	twBwd fftTwiddles // length n/2, sign = +1.0
	arena []cpx
	// The packed input `z[j] = x[2j] + i*x[2j+1]` and its transform.
	z  []cpx
	zt []cpx
}

func newFftScratch(n int) fftScratch {
	// `n / 2` rounds an odd or zero `n` down to a plan the callers never use: the two
	// production lengths (512, 576) are even, and the odd case is rejected at the call.
	m := n / 2
	wn := make([]cpx, m)
	for k := 0; k < m; k++ {
		wn[k] = combineTwiddle(n, k, 1, -1.0)
	}
	return fftScratch{
		wn:    wn,
		twFwd: newFftTwiddles(m, -1.0),
		twBwd: newFftTwiddles(m, 1.0),
		arena: make([]cpx, fftArenaLen(m)),
		z:     make([]cpx, m),
		zt:    make([]cpx, m),
	}
}

// spLenMatches reports whether a scratch was built for the half-length `m` the caller is about to
// run at.
func spLenMatches(sc *fftScratch, m int) bool {
	return len(sc.z) == m && len(sc.zt) == m && len(sc.wn) == m
}

// fftArenaLen is the worst-case `fftRec` arena length: at each level we carve `n` `cpx` for `sub`
// then recurse on `m = n/p` (children are sequential, so they reuse the remainder; the bound is
// additive down one path, not across siblings). Computed at state init for the fixed N
// (576 -> 1143).
func fftArenaLen(n int) int {
	if n <= 1 {
		return 0
	}
	p := smallestFactor(n)
	if p == n {
		return 0 // prime base case allocates nothing
	}
	return n + fftArenaLen(n/p)
}

// fftRec is the recursive mixed-radix Cooley-Tukey DFT. `x[i]` are the n input samples at stride
// `stride` within the parent array; `out` is contiguous. `scratch` is the remaining arena: each
// non-base level splits off the first `n` entries for its `sub` buffer and passes the rest down
// (siblings are sequential, so the rest is safely shared). `tw` supplies the precomputed butterfly
// twiddles for this FFT's sign, bit-identical to the inline cos/sin it replaces.
func fftRec(x []cpx, stride, depth int, out, scratch []cpx, tw *fftTwiddles) {
	// Past the last split level the chain terminates in its prime base: the naive O(n^2) DFT.
	if depth >= len(tw.levels) {
		n := tw.baseN
		if n <= 1 {
			// A length-1 transform is the identity. `newFftTwiddles(1)` splits nothing and builds
			// no base table, so this must return before indexing one -- the guard the pre-plan
			// recursion carried as its `n == 1` arm.
			out[0] = x[0]
			return
		}
		bt := tw.base
		for k := 0; k < n; k++ {
			acc := cpxZero()
			row := k * n
			for j := 0; j < n; j++ {
				acc = acc.add(x[j*stride].mul(bt[row+j]))
			}
			out[k] = acc
		}
		return
	}
	lvl := &tw.levels[depth]
	n, p, m := lvl.n, lvl.p, lvl.m
	// Carve this level's `sub` (size n) from the arena; the remainder feeds the child sub-DFTs.
	sub, rest := scratch[:n], scratch[n:]
	// Compute p sub-DFTs of length m over the decimated inputs (sub[q*m .. (q+1)*m]).
	for q := 0; q < p; q++ {
		fftRec(x[q*stride:], stride*p, depth+1, sub[q*m:(q+1)*m], rest, tw)
	}
	// Combine: out[k] = sum_q twiddle(q,k) * sub_q[k mod m], with the radix-p butterfly.
	ct := lvl.tw
	for k := 0; k < n; k++ {
		kmod := k % m
		acc := cpxZero()
		row := k * p
		for q := 0; q < p; q++ {
			acc = acc.add(sub[q*m+kmod].mul(ct[row+q]))
		}
		out[k] = acc
	}
}

// cfft is the complex FFT of a power-/mixed-radix length into `out`. `sign=-1` forward, `+1`
// inverse.
func cfft(input, out []cpx, sign float32, arena []cpx, tw *fftTwiddles) {
	fftRec(input, 1, 0, out, arena, tw)
}

// rfftForwardOrderedSc is the forward real FFT of `n` real samples via one half-length complex
// transform, in the ordered REAL layout: f[0]=DC.re, f[1]=Nyquist.re, then [re,im] pairs for bins
// 1..n/2-1. Output length is n.
//
// Packing `z[j] = x[2j] + i*x[2j+1]` turns the length-`n` real transform into a length-`m = n/2`
// complex one; the even- and odd-sample spectra are then recovered from `Z` by its Hermitian
// split and recombined with `W_n^k`. This is the same technique PFFFT uses, which is what the
// `smpl` C reference the per-stage vectors came from runs -- `front_end_a_matches_c` measures
// this path as marginally closer to that oracle than the full-length one it replaced.
func rfftForwardOrderedSc(time, f []float32, sc *fftScratch) {
	n := len(time)
	// Checked in release, not just under `debug_assert!`: every way these can disagree fails
	// SILENTLY rather than loudly. A mismatched scratch truncates at the `zip` below, an odd `n`
	// leaves `f[n - 1]` untouched, and a short `f` drops the top bins -- each yielding a
	// plausible-looking spectrum the encoder would happily quantize. Measured at +0.26% on a
	// 486 s encode, i.e. inside this machine's run-to-run noise, so the check is free.
	if len(f) != n {
		panic(fmt.Sprintf("rfft forward: output len %d != input len %d", len(f), n))
	}
	if !(n >= 2 && n%2 == 0) {
		panic(fmt.Sprintf("rfft forward: length %d must be even and >= 2", n))
	}
	if !spLenMatches(sc, n/2) {
		panic(fmt.Sprintf("rfft forward: FftScratch built for a different length than %d", n))
	}
	m := n / 2
	sp := sc
	// z[j] = x[2j] + i*x[2j+1]: even samples into the real part, odd into the imaginary.
	for j := range sp.z {
		sp.z[j].re = time[2*j]
		sp.z[j].im = time[2*j+1]
	}
	fftRec(sp.z, 1, 0, sp.zt, sp.arena, &sp.twFwd)

	// X[k] = E[k] + W_n^k * O[k], where E and O are the DFTs of the even and the odd samples:
	//   E[k] = (Z[k] + conj(Z[m-k])) / 2      O[k] = (Z[k] - conj(Z[m-k])) / 2i
	// At k = 0 both collapse onto Z[0], giving X[0] = Re Z0 + Im Z0 and X[n/2] = Re Z0 - Im Z0 --
	// the two real bins the ordered layout keeps in f[0] and f[1].
	z0 := sp.zt[0]
	f[0] = z0.re + z0.im
	f[1] = z0.re - z0.im
	for k := 1; k < m; k++ {
		a := sp.zt[k]
		b := sp.zt[m-k]
		eRe := float32(0.5 * (a.re + b.re))
		eIm := float32(0.5 * (a.im - b.im))
		oRe := float32(0.5 * (a.im + b.im))
		oIm := float32(0.5 * (b.re - a.re))
		w := sp.wn[k]
		f[2*k] = eRe + (float32(oRe*w.re) - float32(oIm*w.im))
		f[2*k+1] = eIm + (float32(oRe*w.im) + float32(oIm*w.re))
	}
}

// rfftBackwardOrderedSc is the inverse real FFT via one half-length complex transform, consuming
// the ordered REAL layout and producing n real time samples, unnormalized
// (BACKWARD(FORWARD(x)) = n*x).
func rfftBackwardOrderedSc(f, time []float32, sc *fftScratch) {
	n := len(f)
	// Release-checked for the same reason as the forward twin: a short `time` is only partially
	// written and the caller reads stale samples as signal.
	if len(time) != n {
		panic(fmt.Sprintf("rfft backward: output len %d != input len %d", len(time), n))
	}
	if !(n >= 2 && n%2 == 0) {
		panic(fmt.Sprintf("rfft backward: length %d must be even and >= 2", n))
	}
	if !spLenMatches(sc, n/2) {
		panic(fmt.Sprintf("rfft backward: FftScratch built for a different length than %d", n))
	}
	m := n / 2
	sp := sc
	// Invert the forward recombination: Z[k] = E[k] + i*O[k] with
	//   E[k] = (X[k] + conj(X[m-k])) / 2      O[k] = conj(W_n^k) * (X[k] - conj(X[m-k])) / 2
	// k = 0 pairs DC with Nyquist, which the ordered layout keeps in f[0] and f[1].
	sp.z[0] = cpx{
		re: float32(0.5 * (f[0] + f[1])),
		im: float32(0.5 * (f[0] - f[1])),
	}
	for k := 1; k < m; k++ {
		aRe := f[2*k]
		aIm := f[2*k+1]
		cRe := f[2*(m-k)]
		cIm := -f[2*(m-k)+1]
		eRe := float32(0.5 * (aRe + cRe))
		eIm := float32(0.5 * (aIm + cIm))
		dRe := float32(0.5 * (aRe - cRe))
		dIm := float32(0.5 * (aIm - cIm))
		w := sp.wn[k]
		oRe := float32(dRe*w.re) + float32(dIm*w.im)
		oIm := float32(dIm*w.re) - float32(dRe*w.im)
		sp.z[k] = cpx{
			re: eRe - oIm,
			im: eIm + oRe,
		}
	}
	fftRec(sp.z, 1, 0, sp.zt, sp.arena, &sp.twBwd)
	// The length-m inverse yields m*(x[2j] + i*x[2j+1]); the reference is unnormalized at length
	// n = 2m, so the factor 2 restores its scale.
	for j := 0; j < m; j++ {
		z := sp.zt[j]
		time[2*j] = float32(2.0 * z.re)
		time[2*j+1] = float32(2.0 * z.im)
	}
}

// Perceptual model

// percWindows holds the window tables built once, covering the perc-relevant windows.
type percWindows struct {
	percWin1_10ms []float32 // gen_sin_win, len 192
	percWin1_20ms []float32 // gen_sin_win, len 352
	win3Short     []float32 // gen_cos_win, len 32
	win3Long      []float32 // gen_cos_win, len 64
}

// win[i] = sin((i+1)/(N+1) * pi/2)
func percGenSinWin(n int) []float32 {
	w := make([]float32, n)
	for i := 0; i < n; i++ {
		w[i] = sinF32((float32(i) + 1.0) / (float32(n) + 1.0) * smplPi / 2.0)
	}
	return w
}

// win[i] = cos((i+1)/(N+1) * pi/2)
func percGenCosWin(n int) []float32 {
	w := make([]float32, n)
	for i := 0; i < n; i++ {
		w[i] = cosF32((float32(i) + 1.0) / (float32(n) + 1.0) * smplPi / 2.0)
	}
	return w
}

func newPercWindows() percWindows {
	return percWindows{
		percWin1_10ms: percGenSinWin(smplPercWin1_10msLen),
		percWin1_20ms: percGenSinWin(smplPercWin1_20msLen),
		win3Short:     percGenCosWin(smplWin3ShortLen),
		win3Long:      percGenCosWin(smplWin3LongLen),
	}
}

// Windowing for the perc case (use_lpc_win == FALSE).
func smplWindowPerc(win *percWindows, input, out []float32, l int, frameMs int32, useLongWin bool) {
	var win1len int
	var win1 []float32
	if frameMs == 10 {
		win1len, win1 = smplPercWin1_10msLen, win.percWin1_10ms
	} else {
		win1len, win1 = smplPercWin1_20msLen, win.percWin1_20ms
	}
	var win3len int
	var win3 []float32
	if useLongWin {
		win3len, win3 = smplWin3LongLen, win.win3Long
	} else {
		win3len, win3 = smplWin3ShortLen, win.win3Short
	}

	smplMulVec(input, win1, out, win1len)
	mid := l - win1len - smplWin3LongLen
	copy(out[win1len:win1len+mid], input[win1len:win1len+mid])
	smplMulVec(input[l-smplWin3LongLen:], win3, out[l-smplWin3LongLen:], win3len)
	if !useLongWin {
		start := l - smplWin3LongLen + smplWin3ShortLen
		for i := start; i < l; i++ {
			out[i] = 0.0
		}
	}
}

// Bidirectional masking smooth across the power spectrum.
func smthFilt(f, smthcoef []float32) {
	half := percwNfft / 2
	f2smth := f[0]
	for i := 1; i < half; i++ {
		f2new := f[2*i]
		f2smth = f2new + float32(smthcoef[i]*(f2smth-f2new))
		f[2*i] = f2smth
	}
	f[1] = f[1] + float32(smthcoef[half]*(f2smth-f[1]))
	f2smth = f[1]
	i := half - 1
	for i > 0 {
		f2new := f[2*i]
		f2smth = f2new + float32(smthcoef[i]*(f2smth-f2new))
		f[2*i] = f2smth
		i--
	}
	f[0] = f[0] + float32(smthcoef[0]*(f2smth-f[0]))
}

// percModelState is the state carried across `smplPercModel` calls (the `buf` history of length
// PERCW_NFFT), plus the reusable per-call FFT/windowing scratch so the perceptual model allocates
// nothing per frame.
type percModelState struct {
	buf      [percwNfft]float32
	smthcoef []float32 // length PERCW_NFFT/2 + 1
	windows  percWindows
	fft      fftScratch
	bufWin   []float32 // length PERCW_NFFT, windowed time-domain frame
	f        []float32 // length PERCW_NFFT, ordered REAL spectrum / power
}

func newPercModelState() percModelState {
	// Per-bin mel-width smoothing coefficients.
	fsStep := (percwFsKhz * 1000.0) / float32(percwNfft)
	smthcoef := make([]float32, percwNfft/2+1)
	for i := 0; i < percwNfft/2+1; i++ {
		percWidthPerBin := smplPercMaskSmth * (float32(fsStep*float32(i)) + smplPercMelFcHz) / fsStep
		smthcoef[i] = percWidthPerBin / (percWidthPerBin + 1.0)
	}
	return percModelState{
		smthcoef: smthcoef,
		windows:  newPercWindows(),
		fft:      newFftScratch(percwNfft),
		bufWin:   make([]float32, percwNfft),
		f:        make([]float32, percwNfft),
	}
}

// smplPercModel: windowed power spectrum -> bidirectional masking smooth -> inverse -> 1/NFFT
// scale. Returns the first `lenR` autocorrelation lags. The `buf` history advances by dropping the
// consumed front, keeping the tail, and appending the new subframe. It allocates the result; the
// encoder uses [smplPercModelInto].
func smplPercModel(state *percModelState, xsubfr []float32, xsubfrLen int, frameMs int32, isLastSubfr int32, lenR int) []float32 {
	return smplPercModelInto(state, xsubfr, xsubfrLen, frameMs, isLastSubfr, lenR, make([]float32, lenR))
}

// smplPercModelInto is Rust `smpl_perc_model` writing the `lenR` lags into `r` (len >= lenR)
// instead of a fresh Vec, and returning `r[:lenR]`.
func smplPercModelInto(state *percModelState, xsubfr []float32, xsubfrLen int, frameMs int32, isLastSubfr int32, lenR int, r []float32) []float32 {
	// Advance history: drop the consumed front, keep the (NFFT - xsubfr_len) tail, then append xsubfr.
	// Source offset is xsubfr_len - (WB_LONG_LEN - WB_LEN) = xsubfr_len - 32.
	srcOff := xsubfrLen - (smplWinnextWbLongLen - smplWinnextWbLen)
	keep := percwNfft - xsubfrLen
	copy(state.buf[0:keep], state.buf[srcOff:srcOff+keep])
	copy(state.buf[keep:keep+xsubfrLen], xsubfr[:xsubfrLen])

	winlen := smplWinprevPercLen + int(frameMs)*16 + smplWin3LongLen
	skipSamples := percwNfft - winlen

	// Reuse the persistent scratch; re-zero bufWin so the windowed frame matches a fresh
	// `vec![0.0; NFFT]` (the skip region plus any window bins the windowing leaves untouched).
	clear(state.bufWin)
	smplWindowPerc(
		&state.windows,
		state.buf[skipSamples:],
		state.bufWin[skipSamples:],
		winlen,
		frameMs,
		isLastSubfr == smplFalse,
	)

	rfftForwardOrderedSc(state.bufWin, state.f, &state.fft)
	f := state.f
	f[0] = f[0] * f[0] // DC
	f[1] = f[1] * f[1] // Nyquist
	for i := 1; i < percwNfft/2; i++ {
		f[2*i] = float32(f[2*i]*f[2*i]) + float32(f[2*i+1]*f[2*i+1])
		f[2*i+1] = 0.0
	}
	smthFilt(f, state.smthcoef)
	rfftBackwardOrderedSc(state.f, state.bufWin, &state.fft)

	// Rust allocates the returned Vec per call; this writes the caller's buffer.
	r = r[:lenR]
	percSmplScaleVec(state.bufWin, r, lenR, float32(1.0)/float32(percwNfft))
	return r
}

// smplPercAc2a: ma2 (b={pe, 1+pe^2, pe}) on R[1..] then Levinson + rc2a -> A[0..perc_resp_len].
func smplPercAc2a(r []float32, lenR int, percEmph float32, percRespLen int, reg float32) []float32 {
	a := make([]float32, percRespLen)
	smplPercAc2aInto(r, lenR, percEmph, percRespLen, reg, a)
	return a
}

// smplPercAc2aInto writes the perceptual LPC response into `a[..percRespLen]` without allocating.
// Overwrites every active coefficient, including the leading 1.0, and leaves
// any destination tail untouched. Input and output storage can be reused across calls.
//
// Requires `2 <= percRespLen <= SMPL_MAX_L_RESP`,
// `len(r) >= lenR >= percRespLen + 1`, and `len(a) >= percRespLen`.
// These are internal caller invariants, not recoverable input errors.
func smplPercAc2aInto(r []float32, lenR int, percEmph float32, percRespLen int, reg float32, a []float32) {
	b := [3]float32{percEmph, 1.0 + float32(percEmph*percEmph), percEmph}
	state := [2]float32{r[0], r[1]}
	// Fixed intermediates avoid heap allocation but contribute to the runtime stack peak.
	var r_ [smplMaxLResp]float32
	percSmplFiltMa2(r[1:], percRespLen, b[:], &state, r_[:])

	var rc [smplMaxLResp]float32
	smplAc2rc(r_[:], percRespLen-1, reg, rc[:])

	smplRc2a(rc[:], percRespLen-1, a)
}

// Bitrate controller

func bitrate2pulses(rateKbps float32, coeff *[8]float32) float32 {
	return coeff[0] +
		float32(coeff[1]*rateKbps) +
		float32(coeff[2]*rateKbps*rateKbps) +
		float32(coeff[3]*powF32(rateKbps, 3.0)) +
		float32(coeff[4]*powF32(rateKbps, 4.0)) +
		float32(coeff[5]*powF32(smplE, (rateKbps-coeff[6])*coeff[7]))
}

func bitrate2pulsesHrFec(rateKbps float32, coeff *[8]float32, onePulseRateBps float32) float32 {
	const rateThresKbps float32 = 9.0 // only compensate below this value
	if rateKbps >= rateThresKbps {
		return bitrate2pulses(rateKbps, coeff)
	} else if onePulseRateBps >= rateThresKbps*1000.0 {
		return 1.0
	}
	pulsesThres := bitrate2pulses(rateThresKbps, coeff)
	sc := (rateThresKbps - rateKbps) / (rateThresKbps - onePulseRateBps/1000.0)
	return pulsesThres - float32(sc*(pulsesThres-1.0))
}

// bitrateControllerInputs are the encoder-control inputs the controller reads.
type bitrateControllerInputs struct {
	internalSampleRate       int32
	payloadSizeMs            int32
	fecBitRate               int32
	mainBitRate              int32
	complexity               int32
	useFecRateCompensation   int32
	useDtx                   int32
	subFrameImportanceFactor float32
}

// bitrateController is the BitrateController state carried across frames.
type bitrateController struct {
	prevVoiced           int32
	rateContWnrgSmth     float32
	rateContBitrateScale [smplCelpMaxRates]float32
	bitrateDeltaSmth     [smplCelpMaxRates]float32
	rateContBitrate      [smplCelpMaxRates]float32
	adjustmentFactor     [smplCelpMaxRates]float32
}

// newBitrateController is the controller init plus zeroed state.
func newBitrateController() bitrateController {
	return bitrateController{
		prevVoiced:       0,
		rateContWnrgSmth: 0.0,
		adjustmentFactor: [smplCelpMaxRates]float32{1.0, 1.0},
	}
}

// control returns (max_pulses_per_subfr, subfr_importance).
func (b *bitrateController) control(
	enc *bitrateControllerInputs,
	dtxSidFrame int32,
	codedAsActiveVoice int32,
	spActProb float32,
	nonflatness float32,
	voicingStrength float32,
	voicedFlag int32,
	wnrg float32,
	wnrgNext float32,
	lowRate int32,
	framelen int32,
	subfrlen int32,
) ([smplCelpMaxRates]int16, [smplCelpMaxRates]float32) {
	bweBitrate := int32(0)
	if enc.internalSampleRate > 16000 {
		if lowRate != 0 {
			bweBitrate += 450
		} else {
			bweBitrate += 750
		}
		if enc.payloadSizeMs == 10 {
			bweBitrate += 450
		}
	}

	b.rateContWnrgSmth += float32(0.6 * (wnrg - b.rateContWnrgSmth))

	var framelenIdx int
	switch {
	case enc.payloadSizeMs == 10:
		framelenIdx = 0
	case enc.payloadSizeMs == 20:
		framelenIdx = 1
	case enc.payloadSizeMs == 60:
		framelenIdx = 2
	default:
		framelenIdx = 3
	}

	var maxPulsesPerSubfr [smplCelpMaxRates]int16
	var subfrImportance [smplCelpMaxRates]float32

	// Operator precedence: `SMPL_CELP_IDX_FEC + (a == 0) || (b == c)` parses as
	// `(SMPL_CELP_IDX_FEC + (a == 0)) || (b == c)` (a `||` whose value is 0 or 1).
	startR := 0
	if (int32(smplCelpIdxFec)+b2i32(enc.fecBitRate == 0)) != 0 || enc.fecBitRate == enc.mainBitRate {
		startR = 1
	}

	lrIdx := 1
	if lowRate != 0 {
		lrIdx = 0
	}

	for r := startR; r <= smplCelpIdxMain; r++ {
		var bitRate float32
		if r == smplCelpIdxFec {
			bitRate = float32(enc.fecBitRate)
		} else {
			bitRate = float32(enc.mainBitRate)
		}
		bitRate = minF32(bitRate, 30000.0) // don't extrapolate pulses_per_20ms_target_max curves
		rateKbps := (bitRate - float32(bweBitrate)) / 1000.0
		if lowRate == 0 {
			switch enc.complexity {
			case 1:
				rateKbps *= 0.9900990
			case 2:
				rateKbps *= 0.9900990
			case 3:
				rateKbps *= 1.0101010
			case 4:
				rateKbps *= 1.0101010
			default:
				rateKbps *= 1.0
			}
		}

		var pulsesPer20msTargetMax float32
		rateControlThrs := float32(smplRateControlThrsComp5[framelenIdx][lrIdx])
		if (bitRate - float32(bweBitrate)) < rateControlThrs {
			pulsesPer20msTargetMax = 1.0
		} else {
			coeff := &smplRateControlModelComp5[framelenIdx][lrIdx]
			if (r == smplCelpIdxFec) && lowRate == 0 && enc.useFecRateCompensation != 0 {
				pulsesPer20msTargetMax = maxF32(bitrate2pulsesHrFec(rateKbps, coeff, rateControlThrs), 1.0)
			} else {
				pulsesPer20msTargetMax = maxF32(bitrate2pulses(rateKbps, coeff), 1.0)
			}
		}

		relPulserate := pulsesPer20msTargetMax / 16.0 * (320.0 / float32(framelen))
		relPulserateLog := lnF32(relPulserate)
		if b.rateContBitrate[r] != bitRate {
			bitrateScale := smplRateContScale *
				relPulserate *
				(1.0 + float32(0.4*relPulserateLog*relPulserateLog))
			b.rateContBitrateScale[r] = bitrateScale
			b.rateContBitrate[r] = bitRate
		}

		numsubfrs := framelen / subfrlen
		mpps := 1 + f32ToI32(roundF32(pulsesPer20msTargetMax*(1.0+0.5)/float32(numsubfrs)))
		if enc.useDtx != 0 && dtxSidFrame != 0 {
			mpps = 0
		} else {
			mpps = f32ToI32(roundF32(float32(mpps) * (0.5 + float32(0.5*sqrtF32(spActProb+1e-12)))))
			var frameType int
			switch {
			case codedAsActiveVoice == 0:
				frameType = backgroundNoise
			case voicedFlag == 1:
				frameType = voiced
			default:
				frameType = unvoiced
			}
			// smpl_max_pulses_per_frame is indexed by the raw low_rate flag (0=high-rate row),
			// unlike the model/thrs tables which use `low_rate ? 0 : 1`.
			maxPulses := int32(smplMaxPulsesPerFrame[int(lowRate)][frameType]) *
				framelen /
				320
			mpps = min(mpps, maxPulses/numsubfrs) // don't overshoot the PDF
		}
		maxPulsesPerSubfr[r] = int16(mpps)

		imp := (wnrg + float32(0.01*wnrgNext)) / (b.rateContWnrgSmth + float32(0.02*wnrgNext) + 1e-12)
		if voicedFlag != 0 {
			if bitRate <= 9000.0 {
				imp = sqrtF32(imp + 1e-12)
			}
		} else {
			imp *= 0.9 + float32(0.3*percSmplSigmoid(nonflatness-2.0))
			imp *= 0.8
		}
		if voicedFlag != b.prevVoiced {
			imp *= 1.1
		}
		imp *= 0.9 + float32(0.3*1.0/(1.0+float32(25.0*voicingStrength*voicingStrength)))

		// Speech-activity bitrate allocation shaping.
		impFactor := enc.subFrameImportanceFactor
		if impFactor <= 1.0 {
			imp *= (1.0 - impFactor) + float32(impFactor*sqrtF32(spActProb+1e-12))
		} else if impFactor <= 2.0 {
			impFactor -= 1.0
			imp *= (1.0 - impFactor) + float32(impFactor*spActProb)
		} else {
			impFactor -= 2.0
			imp *= (1.0 - impFactor) + float32(impFactor*spActProb*spActProb)
		}
		imp *= b.adjustmentFactor[r] * b.rateContBitrateScale[r]
		subfrImportance[r] = imp
		b.prevVoiced = voicedFlag
	}

	return maxPulsesPerSubfr, subfrImportance
}
