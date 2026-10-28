// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_lsf_seed.rs, MIT.

package mlow

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
)

// lsfSeedSeedFromJson parses the (gitignored) lsf_seed.json dump into the packed seed (Rust's
// #[cfg(test)] seed_from_json / seed_json::parse; the VOIP_GEN_TABLES generator calls it).
func lsfSeedSeedFromJson(s string) lsfSeed {
	return lsfSeedParse(s)
}

// lsfSeedRawSeed mirrors the Rust test's serde RawSeed. Integers are read as int and narrowed like
// serde's u8/u32 targets; floats are read as float64 and then narrowed, which is how serde_json
// produces an f32. grid16_* / centroids16 / matrices16 / min_spacing / qlvls_flat_tail are present
// in the JSON but derived at load (not stored); encoding/json ignores them.
type lsfSeedRawSeed struct {
	Cb16            [][][]int64   `json:"cb_16"`
	Cinv16          [][]int64     `json:"cinv_16"`
	Rot8            [][][][]int64 `json:"rot_8"`
	RotCond8        [][][][]int64 `json:"rot_cond_8"`
	Mean            [][]float64   `json:"mean"`
	Cmf             [][]int64     `json:"cmf"`
	CmfCond         [][]int64     `json:"cmf_cond"`
	MinDist         [][]float64   `json:"min_dist"`
	RegCond         []float64     `json:"reg_cond"`
	St2MinQi        [][][][]int64 `json:"st2_min_qi"`
	St2MaxQi        [][][][]int64 `json:"st2_max_qi"`
	Qstep           [][]float64   `json:"qstep"`
	St2AllQlvls8    []int64       `json:"st2_all_qlvls_8"`
	St2AllQlvlDcmfs []int64       `json:"st2_all_qlvl_dcmfs"`
	LsfSel          [][]int64     `json:"lsf_sel"`
	LsfExtra        []int64       `json:"lsf_extra"`
}

func lsfSeedU8(x int64) uint8 {
	if x < 0 || x > math.MaxUint8 {
		panic("lsf_seed.json: value out of u8 range")
	}
	return uint8(x)
}

func lsfSeedU32(x int64) uint32 {
	if x < 0 || x > math.MaxUint32 {
		panic("lsf_seed.json: value out of u32 range")
	}
	return uint32(x)
}

func lsfSeedU8s(v []int64) []uint8 {
	out := make([]uint8, len(v))
	for i, x := range v {
		out[i] = lsfSeedU8(x)
	}
	return out
}

func lsfSeedFlatF32(v [][]float64) []float32 {
	var out []float32
	for _, r := range v {
		for _, x := range r {
			out = append(out, float32(x))
		}
	}
	return out
}

func lsfSeedFlatU32(v [][]int64) []uint32 {
	var out []uint32
	for _, r := range v {
		for _, x := range r {
			out = append(out, lsfSeedU32(x))
		}
	}
	return out
}

func lsfSeedParse(s string) lsfSeed {
	var r lsfSeedRawSeed
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		panic("lsf_seed.json: " + err.Error())
	}
	// rot_8 [2][16][16][16] -> bytes
	var rot8 []uint8
	for _, v := range r.Rot8 {
		for _, c := range v {
			for _, row := range c {
				rot8 = append(rot8, lsfSeedU8s(row)...)
			}
		}
	}
	var rotCond8 []uint8
	for _, v := range r.RotCond8 {
		for _, lr := range v {
			for _, row := range lr {
				rotCond8 = append(rotCond8, lsfSeedU8s(row)...)
			}
		}
	}
	var cb16 []uint32
	for _, v := range r.Cb16 {
		for _, c := range v {
			for _, x := range c {
				cb16 = append(cb16, lsfSeedU32(x))
			}
		}
	}
	var st2MinQi, st2MaxQi []uint8
	for _, pair := range []struct {
		dst *[]uint8
		src [][][][]int64
	}{
		{&st2MinQi, r.St2MinQi},
		{&st2MaxQi, r.St2MaxQi},
	} {
		for _, v := range pair.src {
			for _, lr := range v {
				for _, row := range lr {
					for _, x := range row {
						if x < math.MinInt32 || x > math.MaxInt32 {
							panic("lsf_seed.json: value out of i32 range")
						}
						*pair.dst = append(*pair.dst, uint8(int8(int32(x))))
					}
				}
			}
		}
	}
	lsfExtra := make([]uint32, len(r.LsfExtra))
	for i, x := range r.LsfExtra {
		lsfExtra[i] = lsfSeedU32(x)
	}
	regCond := make([]float32, len(r.RegCond))
	for i, x := range r.RegCond {
		regCond[i] = float32(x)
	}
	return lsfSeed{
		rot8:            rot8,
		rotCond8:        rotCond8,
		st2AllQlvls8:    lsfSeedU8s(r.St2AllQlvls8),
		st2AllQlvlDcmfs: lsfSeedU8s(r.St2AllQlvlDcmfs),
		st2MinQi:        st2MinQi,
		st2MaxQi:        st2MaxQi,
		cb16:            cb16,
		cinv16:          lsfSeedFlatU32(r.Cinv16),
		cmf:             lsfSeedFlatU32(r.Cmf),
		cmfCond:         lsfSeedFlatU32(r.CmfCond),
		lsfSel:          lsfSeedFlatU32(r.LsfSel),
		lsfExtra:        lsfExtra,
		mean:            lsfSeedFlatF32(r.Mean),
		minDist:         lsfSeedFlatF32(r.MinDist),
		regCond:         regCond,
		qstep:           lsfSeedFlatF32(r.Qstep),
	}
}

// The field-by-field equivalence between the seed-built structs and the expanded blobs was
// validated before those blobs were removed: every int + every non-sqrt f32 was bit-identical,
// and the sqrt-derived `we`/`wie`/`matrices` matched within 4 ULP. Those fields pass through
// `sqrtf` in `rot_apply_wght`; the reference `sqrtf` rounds a few ULP from IEEE correctly-rounded.
// The difference is benign: `golden_roundtrip_no_regression` stays byte-identical and
// `lsf_quant_matches_c` still matches the reference `qi`/`qlsf`. The tripwire below pins the init
// output without the removed blobs.

// Permanent no-regression tripwire for the LSF init: checksum the seed-built struct fields
// against committed golden constants, independent of the downstream codec tests.
func TestSmplLsfSeed_LsfSeedBuildGoldenChecksums(t *testing.T) {
	built := lsfBuilt()
	st1v := built.cb.st1(1)
	bitsEq := func(got float32, want uint32, msg string) {
		t.Helper()
		if g := math.Float32bits(got); g != want {
			t.Fatalf("%s: got %#x want %#x", msg, g, want)
		}
	}
	bitsEq(st1v.cbhalf[0][0], 0x3d93b440, "cbhalf[1][0][0]")
	bitsEq(st1v.we[0][0][0], 0x3e0ff885, "we[1][0][0][0]")
	bitsEq(st1v.wie[0][0][0], 0x4062b10f, "wie[1][0][0][0]")
	bitsEq(st1v.bits[0], 0x40883c1d, "bits[1][0]")
	if got := built.cb.st2(1, 0, 0).numQlvls[0]; got != 6 {
		t.Fatalf("numQlvls[1][0][0][0]: got %d want 6", got)
	}
	bitsEq(built.cb.st2(1, 0, 0).qlvls[0][0], 0xbf2c0e76, "Qlvls[1][0][0][0][0]")
	wantStage2 := []uint16{0, 33, 140, 932, 6942, 28552, 32763}
	if got := built.tables.lsfStage2[1][0][0][0]; !slices.Equal(got, wantStage2) {
		t.Fatalf("lsf_stage2[1][0][0][0]: got %v want %v", got, wantStage2)
	}
	if got := len(built.synth.valtables[1][0][0][0]); got != 6 {
		t.Fatalf("valtables width = numQlvls: got %d want 6", got)
	}
}

// Tripwire pinning the grid16 derivations so a future seed regen can't silently desync the
// computed synth tables from the seed fields they're derived from.
func TestSmplLsfSeed_LsfSeedGrid16Derivation(t *testing.T) {
	seed := loadBlobBuffa[lsfSeed](lsfSeedBin)
	nested := seed.reshape()
	built := seed.build()
	synth := &built.synth

	// grid16_alpha == reg_cond.
	if !slices.Equal(synth.grid16Alpha, nested.regCond) {
		t.Fatalf("grid16_alpha == reg_cond: got %v want %v", synth.grid16Alpha, nested.regCond)
	}

	for v := 0; v < 2; v++ {
		// grid16_w[v] == mean[1-v].
		if !slices.Equal(synth.grid16W[v], nested.mean[1-v]) {
			t.Fatalf("grid16_w[%d] == mean[1-v]: got %v want %v", v, synth.grid16W[v], nested.mean[1-v])
		}
		// grid16_matrices[v][lr] == unpack8(rot_cond_8[v][lr]), flattened row-major.
		for lr := 0; lr < 2; lr++ {
			m := unpack816x16(
				nested.rotCond8[v][lr],
				rotCondScale[v],
				rotCondMin[v],
			)
			var want []float32
			for _, row := range m {
				want = append(want, row[:]...)
			}
			if !slices.Equal(synth.grid16Matrices[v][lr], want) {
				t.Fatalf("grid16_matrices[%d][%d] == unpack8(rot_cond_8)", v, lr)
			}
		}
	}
}
