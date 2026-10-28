// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_lsf_quant.rs, MIT.
//
// MLow LSF QUANTIZER (encoder side). Given the analysis NLSF, it runs the VQ_temp Mahalanobis
// shortlist over the stage-1 centroids, then an RD beam (`0.5*order*log2(werr)*RDw_adj + bits`) with
// per-coeff stage-2 clamps, returning `qi[0..16]`. `qi[0]` is the Rust wire `grid`, `qi[1..16]` the
// `stage2`. Replaces the SSE-only grid heuristic that froze `grid` at 0 and produced wrong formants.
//
// The codebook tables (`cbhalf/cbCinv/wie/we/bits/Rotcond/cInv`, stage-2 `Qlvls/numBits`, the clamp
// tables, qstep, means, reg_cond, min_dist) are stored verbatim, so the quantizer math is bit-exact
// against the reference.
//
// Validated 60/60 bit-exact against the reference quantizer (see the test). It is the dominant fix
// for the frozen-grid mute and is now wired live in `analysis.rs`, fed the faithful front-end NLSF
// (`smpl_lpc::smpl_a2nlsf_16`). The earlier "resonant NLSF rings up" regression was a symptom of the
// WRONG LPC input (the old heuristic front-end), not a missing decoder stabilizer: the decoder's
// `smpl_reconstruct_nlsf` already reproduces the reference `qlsf` (proven in `smpl_lpc`'s
// `decoder_reconstructs_c_qlsf`), so no synthesis-time stabilization was needed.

package mlow

import "math"

// SMPL_LPC_ORDER (16) and SMPL_PI are smplLpcOrder and smplPi in shared.go.
const lsfCbCentroids = 16
const lsfQstepCondMult float32 = 0.9

// st1Json is the stage-1 codebook of one voicing class (serde-shaped in Rust).
type st1Json struct {
	cbhalf   [][]float32   // [16][16]
	cInv     [][]float32   // [16][16]
	bitsCond []float32     // [17]
	rotcond  [][][]float32 // [2][16][16]
	cbCinv   [][]float32   // [16][16]
	we       [][][]float32 // [16][16][16]
	bits     []float32     // [16]
	wie      [][][]float32 // [16][16][16]
}

// st2Json is one stage-2 codebook.
type st2Json struct {
	numQlvls []int32     // [16]
	qlvls    [][]float32 // [16][numQlvls[i]]
	numBits  [][]float32 // [16][numQlvls[i]]
}

type lsfCbJson struct {
	st1       []st1Json     // [2]
	st2       [][][]st2Json // [2][2][17]
	minQi     [][][][]int32 // [2][2][17][16]
	maxQi     [][][][]int32 // [2][2][17][16]
	qstep     [][]float32   // [2][2]
	meanV     []float32     // [16]
	meanUv    []float32     // [16]
	regCond   []float32     // [2]
	minDistV  []float32     // [17]
	minDistUv []float32     // [17]
}

type lsfCb struct {
	j lsfCbJson
}

func lsfCbFromJson(j lsfCbJson) lsfCb {
	return lsfCb{j: j}
}

func loadLsfCb() *lsfCb {
	return &lsfBuilt().cb
}

// Vector helpers.

func subVec(y, z, out []float32) {
	for i := 0; i < smplLpcOrder; i++ {
		out[i] = y[i] - z[i]
	}
}

func lsfQuantDotProd(a, b []float32) float32 {
	s := float32(0.0)
	for i := 0; i < smplLpcOrder; i++ {
		s += float32(a[i] * b[i])
	}
	return s
}

func werr(x, y, w []float32) float32 {
	s := float32(0.0)
	for k := 0; k < smplLpcOrder; k++ {
		e := x[k] - y[k]
		s += float32(w[k] * e * e)
	}
	return s
}

// lsfQuantMatrixMultTransp16 is the transposed 16-wide matrix-vector multiply:
// `y[i] = sum_j C[j][i]*x[j]`.
func lsfQuantMatrixMultTransp16(c [][]float32, x, y []float32, lenX int) {
	var yt [smplLpcOrder]float32
	xtmp := x[0]
	for i := 0; i < smplLpcOrder; i++ {
		yt[i] = c[0][i] * xtmp
	}
	for j := 1; j < lenX; j++ {
		xtmp := x[j]
		for i := 0; i < smplLpcOrder; i++ {
			yt[i] += float32(c[j][i] * xtmp)
		}
	}
	copy(y[:smplLpcOrder], yt[:])
}

// lsfQuantGetMaxiK returns the top-K indices of the K largest values in `x` (descending), ties
// broken toward the lower index. Used for the VQ shortlist + alt-refinement; the caller re-evaluates
// survivors, so the survivor SET is the load-bearing output.
func lsfQuantGetMaxiK(x []float32, idx []int32, k int) {
	n := len(x)
	// Stack-scratch the mask, as `smpl_celp::smpl_get_maxi_k` already does for its own copy of this
	// selection. `n` is either the stage-1 centroid count (16, or 17 with the conditional centroid)
	// or the LPC order (16), both bounded by the codebook geometry.
	var usedBuf [lsfCbCentroids + 1]bool
	used := usedBuf[:n]
	k = min(k, len(idx))
	for s := 0; s < k; s++ {
		bestI := int32(-1)
		bestV := float32(math.Inf(-1))
		for i, v := range x {
			if used[i] {
				continue
			}
			if v > bestV {
				bestV = v
				bestI = int32(i)
			}
		}
		if bestI < 0 {
			idx[s] = 0
		} else {
			used[bestI] = true
			idx[s] = bestI
		}
	}
}

// lsfWeightsSpectral is the LSF RD weights, spectral inverse-envelope variant
// (`SMPL_USE_SPEC_LSW_WEIGHT`, the active build path): the weight at each LSF is the inverse
// spectral envelope magnitude `1/sqrt(|A(e^jw)|^2 * scale)`, `scale = 1/min`. `a` is the monic LPC
// `A[0..16]` (`A[0]=1`).
func lsfWeightsSpectral(a, lsf []float32) [smplLpcOrder]float32 {
	var lsfw [smplLpcOrder]float32
	for i := 0; i < smplLpcOrder; i++ {
		eIm, eRe := sincosF32(lsf[i])
		accRe := float32(1.0)
		accIm := float32(0.0)
		epRe := eRe
		epIm := eIm
		for j := 1; j < smplLpcOrder; j++ {
			accRe += float32(epRe * a[j])
			accIm -= float32(epIm * a[j])
			// ep *= e
			nr := float32(epRe*eRe) - float32(epIm*eIm)
			ni := float32(epRe*eIm) + float32(epIm*eRe)
			epRe = nr
			epIm = ni
		}
		accRe += float32(epRe * a[smplLpcOrder])
		accIm -= float32(epIm * a[smplLpcOrder])
		lsfw[i] = float32(accRe*accRe) + float32(accIm*accIm)
	}
	minLsfw := lsfw[0]
	for _, v := range lsfw[1:] {
		if v < minLsfw {
			minLsfw = v
		}
	}
	scale := 1.0 / minLsfw
	for i := range lsfw {
		lsfw[i] = 1.0 / sqrtF32(lsfw[i]*scale)
	}
	return lsfw
}

func lsfWeightsLaroia(lsf []float32) [smplLpcOrder]float32 {
	minDist := float32(1e-3)
	var invDelta [smplLpcOrder + 1]float32
	invDelta[0] = 1.0 / maxF32(lsf[0], minDist)
	for i := 1; i < smplLpcOrder; i++ {
		invDelta[i] = 1.0 / maxF32(lsf[i]-lsf[i-1], minDist)
	}
	invDelta[smplLpcOrder] = 1.0 / maxF32(smplPi-lsf[smplLpcOrder-1], minDist)
	var lsfw [smplLpcOrder]float32
	for i := 0; i < smplLpcOrder; i++ {
		lsfw[i] = invDelta[i] + invDelta[i+1]
	}
	return lsfw
}

// lsfMinDistFindMin is the `find_min` closure of lsf_min_dist.
func lsfMinDistFindMin(d *[smplLpcOrder + 1]float32) (float32, int) {
	n := smplLpcOrder
	m := d[0]
	mi := 0
	for i := 1; i < n+1; i++ {
		if d[i] < m {
			m = d[i]
			mi = i
		}
	}
	return m, mi
}

// lsfMinDist pushes LSFs apart so consecutive spacings exceed `min_dist`.
func lsfMinDist(lsfs, minDist []float32) {
	n := smplLpcOrder
	var dlsfs [smplLpcOrder + 1]float32
	dlsfs[0] = lsfs[0] - minDist[0]
	for i := 1; i < n; i++ {
		dlsfs[i] = (lsfs[i] - lsfs[i-1]) - minDist[i]
	}
	dlsfs[n] = (smplPi - lsfs[n-1]) - minDist[n]
	dm, minIx := lsfMinDistFindMin(&dlsfs)
	if dm > 0.0 {
		return
	}
	for k := 0; k < 1000; k++ {
		delta := float32(float32(k)*1.0e-6) - dm
		dlsfs[minIx] += delta
		if minIx == 0 {
			dlsfs[1] -= delta
		} else if minIx == n {
			dlsfs[n-1] -= delta
		} else {
			delta = float32(delta * 0.5)
			dlsfs[minIx-1] -= delta
			dlsfs[minIx+1] -= delta
		}
		dm, minIx = lsfMinDistFindMin(&dlsfs)
		if dm >= 0.0 {
			lsfs[0] = dlsfs[0] + minDist[0]
			for i := 1; i < n; i++ {
				lsfs[i] = lsfs[i-1] + (dlsfs[i] + minDist[i])
			}
			return
		}
	}
	// The reference asserts unreachable here; we fall through with the best-effort spacing.
}

type condParams struct {
	st1Cbhalf [smplLpcOrder]float32
	st1CbCinv [smplLpcOrder]float32
	st1We     [][]float32 // [16][16]
	st1Wie    [][]float32 // [16][16]
}

// vqTemp is `VQ_temp`: Mahalanobis shortlist of `surv` stage-1 centroids (plus the cond centroid
// when present).
func vqTemp(lsf []float32, cbhalf, cbCinv [][]float32, cond *condParams, surv int, idxs []int32) {
	var err [lsfCbCentroids + 1]float32
	var tmp [smplLpcOrder]float32
	for s := 0; s < lsfCbCentroids; s++ {
		subVec(cbhalf[s], lsf, tmp[:])
		err[s] = -lsfQuantDotProd(tmp[:], cbCinv[s])
	}
	cbCentroids := lsfCbCentroids
	if cond != nil {
		subVec(cond.st1Cbhalf[:], lsf, tmp[:])
		err[lsfCbCentroids] = -lsfQuantDotProd(tmp[:], cond.st1CbCinv[:])
		cbCentroids++
	}
	lsfQuantGetMaxiK(err[:cbCentroids], idxs, surv)
}

func smplSign(a float32) int32 {
	if a > 0.0 {
		return 1
	} else if a == 0.0 {
		return 0
	}
	return -1
}

// lsfQuantResult is the result of one LSF quantization: `qi[0]` (=grid), `qi[1..16]` (=stage2), and
// the reconstructed quantized NLSF (so the caller's synthesis uses the SAME envelope the decoder
// will rebuild).
type lsfQuantResult struct {
	qi   [smplLpcOrder + 1]int32
	qlsf [smplLpcOrder]float32
}

// lsfQuantCore is the core LSF quantizer. `nlsf` is the analysis NLSF (`smpl_A2NLSF_16(A)`),
// `voiced` 0/1 selects the codebook, `low_rate` 0/1, `cond` the conditional-coding params (non-nil
// => qi[0]==16 reachable), `rd_w_adj` the RD weight. `surv` is the beam width (= lsf_surv).
func lsfQuantCore(cb *lsfCb, a, nlsf []float32, voiced, lowRate int, cond *condParams, rdWAdj float32, surv int) lsfQuantResult {
	st1 := &cb.j.st1[voiced]
	st2v := cb.j.st2[voiced][lowRate]
	minQi := cb.j.minQi[voiced][lowRate]
	maxQi := cb.j.maxQi[voiced][lowRate]
	var minDist []float32
	if voiced == 1 {
		minDist = cb.j.minDistV
	} else {
		minDist = cb.j.minDistUv
	}

	var lsf [smplLpcOrder]float32
	copy(lsf[:], nlsf[:smplLpcOrder])
	// RD weights are the spectral inverse-envelope (SMPL_USE_SPEC_LSW_WEIGHT).
	wlsf := lsfWeightsSpectral(a, lsf[:])

	qstep := cb.j.qstep[voiced][lowRate]
	qstepCond := qstep * lsfQstepCondMult

	var qim1 [lsfCbCentroids + 1]int32
	vqTemp(lsf[:], st1.cbhalf, st1.cbCinv, cond, surv, qim1[:])

	rdBest := float32(math.MaxFloat32)
	var outQi [smplLpcOrder + 1]int32
	var outQlsf [smplLpcOrder]float32

	for s1 := 0; s1 < surv; s1++ {
		qi1 := int(qim1[s1])
		isCond := qi1 == lsfCbCentroids

		// lsfq1 = 2 * cbhalf[qi1] (or cond centroid).
		var lsfq1 [smplLpcOrder]float32
		if isCond {
			if cond == nil {
				panic("cond centroid requires cond params")
			}
			for i := 0; i < smplLpcOrder; i++ {
				lsfq1[i] = cond.st1Cbhalf[i] * 2.0
			}
		} else {
			for i := 0; i < smplLpcOrder; i++ {
				lsfq1[i] = st1.cbhalf[qi1][i] * 2.0
			}
		}

		// qerr = wie^T * (lsf - lsfq1).
		var qerr_ [smplLpcOrder]float32
		subVec(lsf[:], lsfq1[:], qerr_[:])
		var wiePtr [][]float32
		if isCond {
			wiePtr = cond.st1Wie
		} else {
			wiePtr = st1.wie[qi1]
		}
		var qerr [smplLpcOrder]float32
		lsfQuantMatrixMultTransp16(wiePtr, qerr_[:], qerr[:], smplLpcOrder)

		var invQstep float32
		if !isCond {
			invQstep = 1.0 / qstep
		} else {
			invQstep = 1.0 / qstepCond
		}
		for i := range qerr {
			qerr[i] *= invQstep
		}

		var bits float32
		if cond == nil {
			bits = st1.bits[qi1]
		} else {
			bits = st1.bitsCond[qi1]
		}

		// The clamp tables are indexed by qi1 (0..16); the cond centroid uses the qi1==16 row.
		var alt [smplLpcOrder]int32
		var absQerr [smplLpcOrder]float32
		var qres [smplLpcOrder]float32
		var qi2 [smplLpcOrder]int32
		st2 := &st2v[qi1]
		for i := 0; i < smplLpcOrder; i++ {
			qi2_ := f32ToI32(roundF32(qerr[i]))
			mn := minQi[qi1][i]
			mx := maxQi[qi1][i]
			qi2_ = max(min(qi2_, mx), mn)
			qerr[i] -= float32(qi2_)
			alt[i] = smplSign(qerr[i])
			if (qi2_ == mx && alt[i] > 0) || (qi2_ == mn && alt[i] < 0) {
				absQerr[i] = -1.0
			} else {
				absQerr[i] = absF32(qerr[i])
			}
			qi2_ -= mn
			qi2u := int(qi2_)
			bits += st2.numBits[i][qi2u]
			qres[i] = st2.qlvls[i][qi2u]
			qi2[i] = qi2_
		}

		var iAlt [smplLpcOrder]int32
		lsfQuantGetMaxiK(absQerr[:], iAlt[:], surv)

		var wePtr [][]float32
		if isCond {
			wePtr = cond.st1We
		} else {
			wePtr = st1.we[qi1]
		}
		var lsfq [smplLpcOrder]float32
		lsfQuantMatrixMultTransp16(wePtr, qres[:], lsfq[:], smplLpcOrder)
		for i := 0; i < smplLpcOrder; i++ {
			lsfq[i] += lsfq1[i]
		}

		surv2 := surv - s1
		indChgd := int32(-1)
		bitsOrig := bits
		// The beam base `lsfq_` is FIXED to the initial lsfq before the loop; each refinement flips
		// ONE coeff relative to this base, undoing the previous flip.
		lsfqBase := lsfq
		curBits := bits
		for s2 := 0; s2 < surv2; s2++ {
			lsfMinDist(lsfq[:], minDist)
			w := werr(lsf[:], lsfq[:], wlsf[:])
			rd := float32(0.5*float32(smplLpcOrder)*log2F32(w)*rdWAdj) + curBits
			if rd < rdBest {
				rdBest = rd
				outQi[0] = int32(qi1)
				copy(outQi[1:smplLpcOrder+1], qi2[:])
				outQlsf = lsfq
			}
			if s2 == surv2-1 || absQerr[iAlt[s2]] < 0.25 {
				break
			}
			if s2 > 0 {
				ic := int(indChgd)
				qi2[ic] -= alt[ic]
			}
			indChgd = iAlt[s2]
			ic := int(indChgd)
			qi2Old := qi2[ic]
			qi2[ic] += alt[ic]
			qi2New := qi2[ic]
			qlvlsDiff := st2.qlvls[ic][qi2New] - st2.qlvls[ic][qi2Old]
			for i := 0; i < smplLpcOrder; i++ {
				lsfq[i] = lsfqBase[i] + float32(qlvlsDiff*wePtr[ic][i])
			}
			curBits = bitsOrig + st2.numBits[ic][qi2New] - st2.numBits[ic][qi2Old]
		}
	}

	return lsfQuantResult{
		qi:   outQi,
		qlsf: outQlsf,
	}
}

// lsfQuant is the non-conditional LSF quantization. `a` is the monic LPC `A[0..16]` (A[0]=1).
func lsfQuant(a, nlsf []float32, voiced, lowRate int, rdWAdj float32, surv int) lsfQuantResult {
	cb := loadLsfCb()
	return lsfQuantCore(cb, a, nlsf, voiced, lowRate, nil, rdWAdj, surv)
}

// lsfQuantCond is the conditional LSF quantization given the previous frame's quantized NLSF. `a`
// is the monic LPC `A[0..16]` (A[0]=1).
func lsfQuantCond(a, nlsf, lsfqPrev []float32, voiced, lowRate int, rdWAdj float32, surv int) lsfQuantResult {
	cb := loadLsfCb()
	st1 := &cb.j.st1[voiced]
	var cbLsfsMean []float32
	if voiced == 1 {
		cbLsfsMean = cb.j.meanV
	} else {
		cbLsfsMean = cb.j.meanUv
	}
	reg := cb.j.regCond[voiced]
	var lsfqPrev_ [smplLpcOrder]float32
	var st1Cbhalf [smplLpcOrder]float32
	for i := 0; i < smplLpcOrder; i++ {
		lsfqPrev_[i] = lsfqPrev[i] + float32(reg*(cbLsfsMean[i]-lsfqPrev[i]))
		st1Cbhalf[i] = 0.5 * lsfqPrev_[i]
	}
	var st1CbCinv [smplLpcOrder]float32
	lsfQuantMatrixMultTransp16(st1.cInv, lsfqPrev_[:], st1CbCinv[:], smplLpcOrder)
	// Stack storage for the two 16x16 matrices Rust allocates as Vec<Vec<f32>>. The row slices are
	// built here rather than in the callee: storing them through a pointer parameter would move
	// the matrices to the heap.
	var we, wie [smplLpcOrder][smplLpcOrder]float32
	lsfQuantRotApplyWghtInto(st1.rotcond[lowRate], lsfqPrev_[:], &we, &wie)
	var weRows, wieRows [smplLpcOrder][]float32
	for i := 0; i < smplLpcOrder; i++ {
		weRows[i] = we[i][:]
		wieRows[i] = wie[i][:]
	}
	st1We, st1Wie := weRows[:], wieRows[:]
	cond := condParams{
		st1Cbhalf: st1Cbhalf,
		st1CbCinv: st1CbCinv,
		st1We:     st1We,
		st1Wie:    st1Wie,
	}
	return lsfQuantCore(cb, a, nlsf, voiced, lowRate, &cond, rdWAdj, surv)
}

// lsfQuantRotApplyWght builds `wrot1` (=we) and `wrot2` (=wie) for the cond centroid from the
// rotation matrix and the Laroia-weighted previous LSF. It allocates; lsfQuantCond uses
// [lsfQuantRotApplyWghtInto].
func lsfQuantRotApplyWght(rot [][]float32, lsf []float32) ([][]float32, [][]float32) {
	back1 := new([smplLpcOrder][smplLpcOrder]float32)
	back2 := new([smplLpcOrder][smplLpcOrder]float32)
	lsfQuantRotApplyWghtInto(rot, lsf, back1, back2)
	wrot1 := make([][]float32, smplLpcOrder)
	wrot2 := make([][]float32, smplLpcOrder)
	for i := 0; i < smplLpcOrder; i++ {
		wrot1[i] = back1[i][:]
		wrot2[i] = back2[i][:]
	}
	return wrot1, wrot2
}

// lsfQuantRotApplyWghtInto is Rust `rot_apply_wght` writing the two 16x16 results (Rust
// Vec<Vec<f32>>) into caller-owned matrices: `wrot1` (=we) and `wrot2` (=wie).
func lsfQuantRotApplyWghtInto(
	rot [][]float32,
	lsf []float32,
	wrot1, wrot2 *[smplLpcOrder][smplLpcOrder]float32,
) {
	lsfw := lsfWeightsLaroia(lsf)
	for i := range lsfw {
		lsfw[i] = sqrtF32(lsfw[i])
	}
	var lsfwInv [smplLpcOrder]float32
	for i := 0; i < smplLpcOrder; i++ {
		lsfwInv[i] = 1.0 / lsfw[i]
	}
	for i := 0; i < smplLpcOrder; i++ {
		for j := 0; j < smplLpcOrder; j++ {
			wrot1[i][j] = rot[i][j] * lsfwInv[j]
			wrot2[j][i] = rot[i][j] * lsfw[j]
		}
	}
}
