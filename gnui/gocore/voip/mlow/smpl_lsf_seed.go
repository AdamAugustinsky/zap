// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_lsf_seed.rs, MIT.
//
// Build-from-seed for the MLow LSF runtime tables. The expanded LSF tables (`SmplSynthTables`,
// `SmplTables`, `LsfCb`) are the expansion of one small packed ROM (`lsf_seed.bin`), so we store the
// ROM and rerun the init at load instead of committing the pre-expanded f32. The float op order here
// is load-bearing (matmul accumulation, sqrt-then-reciprocal in `rot_apply_wght`, integer truncation
// in `dcmf_to_cmf`, scalar `unpack8`) so the rebuilt tables are bit-faithful.
//
// `min_spacing` and `lsf_extra` are NOT separate ROM: `min_spacing[v]` is `min_dist[1-v]`, and
// `lsf_extra` is the extra-symbol selector CDF carried in the seed. The decoder bounds-checks
// `sym < numQlvls`, so `valtables` is built at width `numQlvls` (no trailing read).
//
// Explicit indexed loops are intentional: the float accumulation order is load-bearing for bit-exact
// output.

package mlow

import (
	_ "embed"
	"slices"
	"sync"
)

const (
	order     = 16                      // SMPL_LPC_ORDER
	centroids = 16                      // LSF_CB_CENTROIDS
	cinvLen   = order * (order + 1) / 2 // 136
	st2Len    = 9593                    // LSF_ST2_ALL_QLVLS_LEN
)

// Per-voiced (index 0 = unvoiced, 1 = voiced) scale/min constants.
var (
	cbMin        = [2]float32{-0.5873778, -0.24721986}
	cbScale      = [2]float32{1.3145164e-5, 7.226229e-6}
	cinvMin      = [2]float32{-3.5960955e-5, -2.778548e-5}
	cinvScale    = [2]float32{1.8589316e-9, 1.2180106e-9}
	rotMin       = [2]float32{-0.9124832, -0.8455929}
	rotScale     = [2]float32{0.006554049, 0.0069253775}
	rotCondMin   = [2]float32{-0.67291605, -0.8248211}
	rotCondScale = [2]float32{0.0052386564, 0.0064186584}
)

const (
	st2QlvlsMin   float32 = -0.45
	st2QlvlsScale float32 = 0.0034478905
	qstepCondMult float32 = 0.9 // LSF_QSTEP_COND_MULT
)

// The on-disk packed LSF ROM (flat row-major; `tables.proto` `LsfSeed`, Rust's re-exported
// `smpl_tables_blob::tables::LsfSeed`) is smpl_tables_blob.go's lsfSeed. Reshaped before expansion.
//
// grid16_w/alpha/matrices and centroids16/matrices16 are not stored: the synth grid16 tables are
// derived at load (grid16_w = mean[1-v], grid16_alpha = reg_cond, grid16_matrices = unpack8(rot_cond_8)),
// and the grid==16 centroids/matrices rows are never read (grid==16 returns before indexing them).

//go:embed testdata/lsf_seed.bin
var lsfSeedBin []byte

// lsfSeedNested is the packed ROM reshaped into the nested arrays the expansion indexes. Outer
// index `[voiced]`.
type lsfSeedNested struct {
	cb16            [][][]uint16  // [2][16][16]
	cinv16          [][]uint16    // [2][136]
	rot8            [][][][]uint8 // [2][16][16][16]
	rotCond8        [][][][]uint8 // [2][2][16][16]
	mean            [][]float32   // [2][16]
	cmf             [][]uint16    // [2][17]
	cmfCond         [][]uint16    // [2][18]
	minDist         [][]float32   // [2][17]
	regCond         []float32     // [2]
	st2MinQi        [][][][]int8  // [2][2][17][16]
	st2MaxQi        [][][][]int8  // [2][2][17][16]
	qstep           [][]float32   // [2][2]
	st2AllQlvls8    []uint8       // [9593]
	st2AllQlvlDcmfs []uint8       // [9593]
	lsfSel          [][]uint16    // [3][3]
	lsfExtra        []uint16      // [3]
}

// lsfBuiltT holds the three LSF runtime structs rebuilt from one seed.
type lsfBuiltT struct {
	synth  smplSynthTables
	tables smplTables
	cb     lsfCb
}

// reshape helpers (flat row-major -> nested)

func u32ToU16(v []uint32) []uint16 {
	out := make([]uint16, len(v))
	for i, x := range v {
		out[i] = uint16(x)
	}
	return out
}

// rows splits `flat` into `outer` rows of length `inner`.
func rows[T any](flat []T, outer, inner int) [][]T {
	_ = outer // Rust: debug_assert_eq!(flat.len(), outer * inner)
	out := make([][]T, 0, len(flat)/inner)
	for i := 0; i+inner <= len(flat); i += inner {
		out = append(out, slices.Clone(flat[i:i+inner]))
	}
	return out
}

// qi4d reshapes a `[2][2][17][16]` flat i8 ROM.
func qi4d(flat []uint8) [][][][]int8 {
	p := 0
	out := make([][][][]int8, 0, 2)
	for range 2 {
		a := make([][][]int8, 0, 2)
		for range 2 {
			b := make([][]int8, 0, 17)
			for range 17 {
				row := make([]int8, order)
				for k, x := range flat[p : p+order] {
					row[k] = int8(x)
				}
				p += order
				b = append(b, row)
			}
			a = append(a, b)
		}
		out = append(out, a)
	}
	return out
}

func (s *lsfSeed) reshape() lsfSeedNested {
	// rot_8 [2][16][16][16] u8
	rot8 := make([][][][]uint8, 0, 2)
	p := 0
	for range 2 {
		cents := make([][][]uint8, 0, centroids)
		for range centroids {
			mat := make([][]uint8, 0, order)
			for range order {
				mat = append(mat, slices.Clone(s.rot8[p:p+order]))
				p += order
			}
			cents = append(cents, mat)
		}
		rot8 = append(rot8, cents)
	}

	// rot_cond_8 [2][2][16][16] u8
	rotCond8 := make([][][][]uint8, 0, 2)
	p = 0
	for range 2 {
		lr := make([][][]uint8, 0, 2)
		for range 2 {
			mat := make([][]uint8, 0, order)
			for range order {
				mat = append(mat, slices.Clone(s.rotCond8[p:p+order]))
				p += order
			}
			lr = append(lr, mat)
		}
		rotCond8 = append(rotCond8, lr)
	}

	// cb_16 [2][16][16] u16
	cb16U16 := u32ToU16(s.cb16)
	cb16 := make([][][]uint16, 0, 2)
	p = 0
	for range 2 {
		cents := make([][]uint16, 0, centroids)
		for range centroids {
			cents = append(cents, slices.Clone(cb16U16[p:p+order]))
			p += order
		}
		cb16 = append(cb16, cents)
	}

	return lsfSeedNested{
		cb16:            cb16,
		cinv16:          rows(u32ToU16(s.cinv16), 2, cinvLen),
		rot8:            rot8,
		rotCond8:        rotCond8,
		mean:            rows(s.mean, 2, order),
		cmf:             rows(u32ToU16(s.cmf), 2, 17),
		cmfCond:         rows(u32ToU16(s.cmfCond), 2, 18),
		minDist:         rows(s.minDist, 2, 17),
		regCond:         slices.Clone(s.regCond),
		st2MinQi:        qi4d(s.st2MinQi),
		st2MaxQi:        qi4d(s.st2MaxQi),
		qstep:           rows(s.qstep, 2, 2),
		st2AllQlvls8:    slices.Clone(s.st2AllQlvls8),
		st2AllQlvlDcmfs: slices.Clone(s.st2AllQlvlDcmfs),
		lsfSel:          rows(u32ToU16(s.lsfSel), 3, 3),
		lsfExtra:        u32ToU16(s.lsfExtra),
	}
}

func (s *lsfSeed) build() lsfBuiltT {
	n := s.reshape()
	return n.build()
}

// lsfSeedMatrixMultTransp16 is the transposed 16x16 matrix-vector multiply:
// `y[i] = sum_j C[j*16+i] * x[j]` (accumulation order: seed with j=0, then `+=` for j>0).
func lsfSeedMatrixMultTransp16(c *[order][order]float32, x *[order]float32) [order]float32 {
	var y [order]float32
	x0 := x[0]
	for i := 0; i < order; i++ {
		y[i] = float32(c[0][i] * x0)
	}
	for j := 1; j < order; j++ {
		xj := x[j]
		for i := 0; i < order; i++ {
			y[i] += float32(c[j][i] * xj)
		}
	}
	return y
}

// laroia computes the Laroia inverse-gap LSF weights, with the gap floored at 1e-3.
func laroia(lsf *[order]float32) [order]float32 {
	const pi = smplPi // std::f32::consts::PI
	const minDist float32 = 1e-3
	var inv [order + 1]float32
	inv[0] = 1.0 / maxF32(lsf[0], minDist)
	for i := 1; i < order; i++ {
		inv[i] = 1.0 / maxF32(lsf[i]-lsf[i-1], minDist)
	}
	inv[order] = 1.0 / maxF32(pi-lsf[order-1], minDist)
	var w [order]float32
	for i := 0; i < order; i++ {
		w[i] = inv[i] + inv[i+1]
	}
	return w
}

// lsfSeedRotApplyWght applies the Laroia weights to the rotation: `lsfw = sqrt(laroia(lsf))`,
// `we[i][j]=rot[i][j]/lsfw[j]`, `wie[j][i]=rot[i][j]*lsfw[j]`.
func lsfSeedRotApplyWght(
	rot *[order][order]float32,
	lsf *[order]float32,
) ([order][order]float32, [order][order]float32) {
	lsfw := laroia(lsf)
	for i := range lsfw {
		lsfw[i] = sqrtF32(lsfw[i])
	}
	var lsfwInv [order]float32
	for i := 0; i < order; i++ {
		lsfwInv[i] = 1.0 / lsfw[i]
	}
	var we, wie [order][order]float32
	for i := 0; i < order; i++ {
		for j := 0; j < order; j++ {
			we[i][j] = rot[i][j] * lsfwInv[j]
			wie[j][i] = rot[i][j] * lsfw[j]
		}
	}
	return we, wie
}

// cmfToBits is the per-symbol bit cost: `bits[i] = -log2f((cmf[i+1]-cmf[i]) / (f32)cmf[len-1])`.
func cmfToBits(cmf []uint16) []float32 {
	n := len(cmf)
	den := float32(cmf[n-1])
	bits := make([]float32, 0, n-1)
	for i := 0; i < n-1; i++ {
		num := float32(int32(cmf[i+1]) - int32(cmf[i]))
		bits = append(bits, -log2F32(num/den))
	}
	return bits
}

// lsfSeedDcmfToCmf is the integer expansion of a delta-CMF to a cumulative u16 CDF of length
// `len+1`.
func lsfSeedDcmfToCmf(dcmf []uint8) []uint16 {
	dcmfLen := len(dcmf)
	cmf := make([]uint16, dcmfLen+1)
	var sum int64
	for n := 0; n < dcmfLen; n++ {
		tmp := int32(dcmf[n]) + 1
		tmp *= tmp
		if tmp > 65535 {
			tmp = 65535
		}
		cmf[n+1] = uint16(tmp)
		sum += int64(tmp)
	}
	cmf[0] = 0
	for n := 1; n < dcmfLen+1; n++ {
		prev := int64(cmf[n-1])
		add := (int64(cmf[n])*(32767-int64(dcmfLen)))/sum + 1
		cmf[n] = uint16(prev + add)
	}
	return cmf
}

func unpack816x16(packed [][]uint8, scale float32, mn float32) [order][order]float32 {
	var out [order][order]float32
	for i := 0; i < order; i++ {
		for j := 0; j < order; j++ {
			out[i][j] = mn + float32(float32(packed[i][j])*scale)
		}
	}
	return out
}

// lsfSeedMatRows is Rust's `m.iter().map(|r| r.to_vec()).collect()` for a 16x16 matrix.
func lsfSeedMatRows(m *[order][order]float32) [][]float32 {
	out := make([][]float32, order)
	for i := range m {
		out[i] = slices.Clone(m[i][:])
	}
	return out
}

// build runs the LSF codebook expansion to produce the three runtime structs.
func (n *lsfSeedNested) build() lsfBuiltT {
	// Stage 1: per-voiced
	st1 := make([]st1Json, 0, 2)
	// Decoder-side accumulators for SmplSynthTables (centroids/matrices = cbhalf/we).
	synthCentroids := make([][][]float32, 2)
	synthMatrices := make([][][][]float32, 2)
	// grid==16 decorr matrices: the same Rotcond unpack8(rot_cond_8), flattened [lr][256].
	synthGrid16Matrices := make([][][]float32, 2)

	for voiced := 0; voiced < 2; voiced++ {
		// cInv (symmetric lower-triangular fill).
		var cInv [order][order]float32
		p := 0
		for i := 0; i < order; i++ {
			for j := 0; j <= i; j++ {
				v := cinvMin[voiced] + float32(cinvScale[voiced]*float32(n.cinv16[voiced][p]))
				cInv[i][j] = v
				cInv[j][i] = v
				p++
			}
		}

		var cbhalf, cbCinv [order][order]float32
		var we, wie [centroids][order][order]float32
		for c := 0; c < centroids; c++ {
			var lsfCbV [order]float32
			for i := 0; i < order; i++ {
				lsfCbV[i] = cbMin[voiced] +
					float32(float32(n.cb16[voiced][c][i])*cbScale[voiced]) +
					n.mean[voiced][i]
				cbhalf[c][i] = lsfCbV[i] * 0.5
			}
			cbCinv[c] = lsfSeedMatrixMultTransp16(&cInv, &lsfCbV)
			rot := unpack816x16(n.rot8[voiced][c], rotScale[voiced], rotMin[voiced])
			weC, wieC := lsfSeedRotApplyWght(&rot, &lsfCbV)
			we[c] = weC
			wie[c] = wieC
		}

		// Rotcond[lowRate] = unpack8(rot_cond_8[lowRate]).
		var rotcond [2][order][order]float32
		for lr := 0; lr < 2; lr++ {
			rotcond[lr] = unpack816x16(
				n.rotCond8[voiced][lr],
				rotCondScale[voiced],
				rotCondMin[voiced],
			)
		}

		bits := cmfToBits(n.cmf[voiced])         // 16
		bitsCond := cmfToBits(n.cmfCond[voiced]) // 17

		rotcondV := make([][][]float32, len(rotcond))
		for i := range rotcond {
			rotcondV[i] = lsfSeedMatRows(&rotcond[i])
		}
		weV := make([][][]float32, len(we))
		wieV := make([][][]float32, len(wie))
		for i := range we {
			weV[i] = lsfSeedMatRows(&we[i])
			wieV[i] = lsfSeedMatRows(&wie[i])
		}
		st1 = append(st1, st1Json{
			cbhalf:   lsfSeedMatRows(&cbhalf),
			cInv:     lsfSeedMatRows(&cInv),
			bitsCond: slices.Clone(bitsCond),
			rotcond:  rotcondV,
			cbCinv:   lsfSeedMatRows(&cbCinv),
			we:       weV,
			bits:     slices.Clone(bits),
			wie:      wieV,
		})

		// SmplSynthTables decoder centroids/matrices: grid g<16 == cbhalf[g]/we[g]. The grid==16
		// row is never read (grid==16 returns before indexing it), so it is not appended.
		synthCentroids[voiced] = make([][]float32, centroids)
		synthMatrices[voiced] = make([][][]float32, centroids)
		for g := 0; g < centroids; g++ {
			synthCentroids[voiced][g] = slices.Clone(cbhalf[g][:])
			synthMatrices[voiced][g] = lsfSeedMatRows(&we[g])
		}
		// grid16_matrices[voiced][lr] = the Rotcond computed above, flattened row-major to 256.
		synthGrid16Matrices[voiced] = make([][]float32, 2)
		for lr := 0; lr < 2; lr++ {
			flat := make([]float32, 0, order*order)
			for _, row := range rotcond[lr] {
				flat = append(flat, row[:]...)
			}
			synthGrid16Matrices[voiced][lr] = flat
		}
	}

	// Stage 2: the flat QlvlsTable / cmfTable / numBitsTable
	st2 := make([][][]st2Json, 0, 2)
	// SmplSynthTables.valtables[voiced][config][grid][coeff], width numQlvls. config == lowRate.
	valtables := make([][][][][]float32, 0, 2)
	// SmplTables.lsf_stage2[voiced][config][grid][coeff] = the cmfTable slice (cumulative CDF).
	lsfStage2 := make([][][][][]uint16, 0, 2)

	qlvlsFlat := make([]float32, st2Len)
	var numqlvlsFlat [][][][order]int32
	var qoffFlat [][][][order]int
	var cmfSlices [][][][][]uint16
	var numbitsSlices [][][][][]float32

	qPtr := 0    // QlvlsRomPtr (index into qlvls_flat)
	q8Ptr := 0   // QlvlsRomPtr8 (index into st2_all_qlvls_8)
	dcmfPtr := 0 // QlvlsDCmfRomPtr

	for voiced := 0; voiced < 2; voiced++ {
		nqV := make([][][order]int32, 0, 2)
		qoffV := make([][][order]int, 0, 2)
		cmfV := make([][][][]uint16, 0, 2)
		nbV := make([][][][]float32, 0, 2)
		for lr := 0; lr < 2; lr++ {
			nqLr := make([][order]int32, 0, centroids+1)
			qoffLr := make([][order]int, 0, centroids+1)
			cmfLr := make([][][]uint16, 0, centroids+1)
			nbLr := make([][][]float32, 0, centroids+1)
			for c := 0; c < centroids+1; c++ {
				qstep := n.qstep[voiced][lr]
				if c == centroids {
					qstep *= qstepCondMult
				}
				var nqC [order]int32
				var qoffC [order]int
				cmfC := make([][]uint16, 0, order)
				nbC := make([][]float32, 0, order)
				for i := 0; i < order; i++ {
					minQi := int32(n.st2MinQi[voiced][lr][c][i])
					maxQi := int32(n.st2MaxQi[voiced][lr][c][i])
					numQlvls := int(maxQi - minQi + 1)
					nqC[i] = int32(numQlvls)
					qoffC[i] = qPtr
					for lvl := 0; lvl < numQlvls; lvl++ {
						q8 := float32(n.st2AllQlvls8[q8Ptr])
						qlvlsFlat[qPtr] =
							(st2QlvlsMin + float32(st2QlvlsScale*q8) + float32(lvl) + float32(minQi)) *
								qstep
						qPtr++
						q8Ptr++
					}
					dcmf := n.st2AllQlvlDcmfs[dcmfPtr : dcmfPtr+numQlvls]
					cmf := lsfSeedDcmfToCmf(dcmf) // num_qlvls+1
					nb := cmfToBits(cmf)          // num_qlvls
					dcmfPtr += numQlvls
					cmfC = append(cmfC, cmf)
					nbC = append(nbC, nb)
				}
				nqLr = append(nqLr, nqC)
				qoffLr = append(qoffLr, qoffC)
				cmfLr = append(cmfLr, cmfC)
				nbLr = append(nbLr, nbC)
			}
			nqV = append(nqV, nqLr)
			qoffV = append(qoffV, qoffLr)
			cmfV = append(cmfV, cmfLr)
			nbV = append(nbV, nbLr)
		}
		numqlvlsFlat = append(numqlvlsFlat, nqV)
		qoffFlat = append(qoffFlat, qoffV)
		cmfSlices = append(cmfSlices, cmfV)
		numbitsSlices = append(numbitsSlices, nbV)
	}
	// The flat-pointer walks must consume exactly ST2_LEN. Rust checks this with `debug_assert`
	// only: the seed is a checked-in constant ROM, so a release miscount can only come from a
	// corrupted blob (which the golden-checksum test catches), never from input data.

	// Assemble st2 (LsfCb) with Qlvls sliced [off..off+numQlvls] from the flat table.
	for voiced := 0; voiced < 2; voiced++ {
		st2V := make([][]st2Json, 0, 2)
		for lr := 0; lr < 2; lr++ {
			st2Lr := make([]st2Json, 0, centroids+1)
			for c := 0; c < centroids+1; c++ {
				nq := &numqlvlsFlat[voiced][lr][c]
				qoff := &qoffFlat[voiced][lr][c]
				qlvls := make([][]float32, 0, order)
				for i := 0; i < order; i++ {
					k := int(nq[i])
					qlvls = append(qlvls, slices.Clone(qlvlsFlat[qoff[i]:qoff[i]+k]))
				}
				numBits := make([][]float32, len(numbitsSlices[voiced][lr][c]))
				for i, v := range numbitsSlices[voiced][lr][c] {
					numBits[i] = slices.Clone(v)
				}
				st2Lr = append(st2Lr, st2Json{
					numQlvls: slices.Clone(nq[:]),
					qlvls:    qlvls,
					numBits:  numBits,
				})
			}
			st2V = append(st2V, st2Lr)
		}
		st2 = append(st2, st2V)
	}

	// valtables[voiced][config][grid][coeff] = flat QlvlsTable sliced numQlvls.
	for voiced := 0; voiced < 2; voiced++ {
		vtV := make([][][][]float32, 0, 2)
		s2V := make([][][][]uint16, 0, 2)
		for lr := 0; lr < 2; lr++ {
			vtLr := make([][][]float32, 0, centroids+1)
			s2Lr := make([][][]uint16, 0, centroids+1)
			for c := 0; c < centroids+1; c++ {
				nq := &numqlvlsFlat[voiced][lr][c]
				qoff := &qoffFlat[voiced][lr][c]
				vtC := make([][]float32, 0, order)
				for i := 0; i < order; i++ {
					k := int(nq[i])
					vtC = append(vtC, slices.Clone(qlvlsFlat[qoff[i]:qoff[i]+k]))
				}
				vtLr = append(vtLr, vtC)
				s2C := make([][]uint16, len(cmfSlices[voiced][lr][c]))
				for i, v := range cmfSlices[voiced][lr][c] {
					s2C[i] = slices.Clone(v)
				}
				s2Lr = append(s2Lr, s2C)
			}
			vtV = append(vtV, vtLr)
			s2V = append(s2V, s2Lr)
		}
		valtables = append(valtables, vtV)
		lsfStage2 = append(lsfStage2, s2V)
	}

	// Assemble the runtime structs
	cbJSON := lsfCbJson{
		st1:       st1,
		st2:       st2,
		minQi:     cloneQi(n.st2MinQi),
		maxQi:     cloneQi(n.st2MaxQi),
		qstep:     lsfSeedClone2(n.qstep),
		meanV:     slices.Clone(n.mean[1]),
		meanUv:    slices.Clone(n.mean[0]),
		regCond:   slices.Clone(n.regCond),
		minDistV:  slices.Clone(n.minDist[1]),
		minDistUv: slices.Clone(n.minDist[0]),
	}

	tables := smplTables{
		lsfSel: lsfSeedClone2(n.lsfSel),
		lsfGrid: lsfGrid{
			// match1 = CMF_cond_v, match1_alt = CMF_cond_uv, match0 = CMF_uv, match0_alt = CMF_v.
			match1:    slices.Clone(n.cmfCond[1]),
			match1Alt: slices.Clone(n.cmfCond[0]),
			match0:    slices.Clone(n.cmf[0]),
			match0Alt: slices.Clone(n.cmf[1]),
		},
		lsfStage2: lsfStage2,
		lsfExtra:  slices.Clone(n.lsfExtra),
	}

	// min_spacing[v] = min_dist[1-v] (the index swap), not separate ROM.
	minSpacing := [][]float32{slices.Clone(n.minDist[1]), slices.Clone(n.minDist[0])}

	synth := smplSynthTables{
		valtables:  valtables,
		centroids:  synthCentroids,
		matrices:   synthMatrices,
		minSpacing: minSpacing,
		// grid16_w[v] = mean[1-v] (the 1-v swap bakes in the synth's INVERTED selection);
		// grid16_alpha = reg_cond; grid16_matrices = unpack8(rot_cond_8) computed above.
		grid16W:        [][]float32{slices.Clone(n.mean[1]), slices.Clone(n.mean[0])},
		grid16Alpha:    slices.Clone(n.regCond),
		grid16Matrices: synthGrid16Matrices,
	}

	return lsfBuiltT{
		synth:  synth,
		tables: tables,
		cb:     lsfCbFromJson(cbJSON),
	}
}

// lsfSeedClone2 is Rust's `.clone()` of a `Vec<Vec<T>>` (a deep copy).
func lsfSeedClone2[T any](v [][]T) [][]T {
	out := make([][]T, len(v))
	for i, r := range v {
		out[i] = slices.Clone(r)
	}
	return out
}

func cloneQi(qi [][][][]int8) [][][][]int32 {
	out := make([][][][]int32, len(qi))
	for a := range qi {
		out[a] = make([][][]int32, len(qi[a]))
		for b := range qi[a] {
			out[a][b] = make([][]int32, len(qi[a][b]))
			for c := range qi[a][b] {
				row := make([]int32, len(qi[a][b][c]))
				for k, x := range qi[a][b][c] {
					row[k] = int32(x)
				}
				out[a][b][c] = row
			}
		}
	}
	return out
}

var lsfBuiltOnce = sync.OnceValue(func() *lsfBuiltT {
	seed := loadBlobBuffa[lsfSeed](lsfSeedBin)
	b := seed.build()
	return &b
})

// lsfBuilt loads the LSF seed ROM and builds all three runtime structs once.
func lsfBuilt() *lsfBuiltT {
	return lsfBuiltOnce()
}
