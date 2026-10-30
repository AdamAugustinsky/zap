// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_lsf_quant.rs, MIT.
//
// The `#[cfg(test)]` LsfCb accessors and the `tests` module.

package mlow

import (
	"slices"
	"testing"
)

func (c *lsfCb) st1(voiced int) *st1Json {
	return &c.j.st1[voiced]
}

func (c *lsfCb) st2(voiced, lowRate, cc int) *st2Json {
	return &c.j.st2[voiced][lowRate][cc]
}

func (c *lsfCb) minQiRef() [][][][]int32 { return c.j.minQi }
func (c *lsfCb) maxQiRef() [][][][]int32 { return c.j.maxQi }
func (c *lsfCb) qstepRef() [][]float32   { return c.j.qstep }
func (c *lsfCb) meanVRef() []float32     { return c.j.meanV }
func (c *lsfCb) meanUvRef() []float32    { return c.j.meanUv }
func (c *lsfCb) regCondRef() []float32   { return c.j.regCond }
func (c *lsfCb) minDistVRef() []float32  { return c.j.minDistV }
func (c *lsfCb) minDistUvRef() []float32 { return c.j.minDistUv }

func lsfQuantFvec(v val) []float32 {
	a := v.Arr()
	out := make([]float32, len(a))
	for i, x := range a {
		out[i] = float32(x.F64())
	}
	return out
}

func lsfQuantIvec(v val) []int32 {
	a := v.Arr()
	out := make([]int32, len(a))
	for i, x := range a {
		out[i] = int32(x.I64())
	}
	return out
}

// Tier-B: feed the reference's post-A2NLSF `lsf` + LPC `A` + state to our quantizer; both the
// indices `qi[]` AND the reconstructed `qlsf` must match the reference bit-for-bit.
func TestSmplLsfQuant_LsfQuantMatchesC(t *testing.T) {
	arr := loadPacked(t, "lsf_quant_io.cbor.zst").Arr()
	if !(len(arr) >= 12) {
		t.Fatalf("need vectors")
	}
	bad := 0
	for n, r := range arr {
		lsf := lsfQuantFvec(r.Get("lsf"))
		a := lsfQuantFvec(r.Get("A"))
		voiced := int(r.Get("voiced").I64())
		lowRate := int(r.Get("lowRate").I64())
		surv := int(r.Get("surv").I64())
		rdW := float32(r.Get("RDw_adj").F64())
		cond := r.Get("cond_coding").I64() != 0
		prevLsf := lsfQuantFvec(r.Get("prev_lsf"))
		wantQi := lsfQuantIvec(r.Get("qi"))
		wantQlsf := lsfQuantFvec(r.Get("qlsf"))
		var res lsfQuantResult
		if cond {
			res = lsfQuantCond(a, lsf, prevLsf, voiced, lowRate, rdW, surv)
		} else {
			res = lsfQuant(a, lsf, voiced, lowRate, rdW, surv)
		}
		if !slices.Equal(res.qi[:], wantQi) {
			bad++
			if bad <= 6 {
				t.Logf("rec %d qi MISMATCH voiced=%d cond=%v\n  got  %v\n  want %v", n, voiced, cond, res.qi, wantQi)
			}
			continue
		}
		// Reconstructed NLSF must match (the envelope the decoder rebuilds).
		for k := 0; k < smplLpcOrder; k++ {
			if !(absF32(res.qlsf[k]-wantQlsf[k]) < 1e-4) {
				t.Fatalf("rec %d qlsf[%d] %.6f != ref %.6f", n, k, res.qlsf[k], wantQlsf[k])
			}
		}
	}
	if bad != 0 {
		t.Fatalf("lsf_quant qi mismatch vs reference: %d != 0", bad)
	}
}

func TestSmplLsfQuant_LsfQuantMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_lsf_quant.cbor.zst").Arr()
	if len(records) != 330 {
		t.Fatalf("records %d != 330", len(records))
	}
	previous := make([]float32, 16)
	conditional := 0
	for i, r := range records {
		a := lsfQuantFvec(r.Get("A"))
		nlsf := smplA2nlsf16(a)
		voiced := int(r.Get("voiced").U64())
		lowRate := int(r.Get("lowRate").U64())
		weight := float32(r.Get("RDw_adj").F64())
		survivors := int(r.Get("surv").U64())
		var result lsfQuantResult
		if r.Get("cond").IsNull() {
			result = lsfQuant(a, nlsf[:], voiced, lowRate, weight, survivors)
		} else {
			conditional++
			result = lsfQuantCond(a, nlsf[:], previous, voiced, lowRate, weight, survivors)
		}
		if want := lsfQuantIvec(r.Get("qi")); !slices.Equal(result.qi[:], want) {
			t.Fatalf("case %d: quantized indices: %v != %v", i, result.qi, want)
		}
		expected := lsfQuantFvec(r.Get("qlsf"))
		for k := 0; k < min(len(result.qlsf), len(expected)); k++ {
			if !(absF32(result.qlsf[k]-expected[k]) < 1e-4) {
				t.Fatalf("case %d: quantized LSF", i)
			}
		}
		previous = expected
	}
	if conditional != 219 {
		t.Fatalf("conditional %d != 219", conditional)
	}
}
