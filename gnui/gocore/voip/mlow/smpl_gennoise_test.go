// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_gennoise.rs, MIT.
//
// The `#[cfg(test)]` module of smpl_gennoise.rs.

package mlow

import "testing"

func smplGennoiseF32v(v val) []float32 {
	return v.F32s()
}

func smplGennoiseNgFrom(v val) noiseGenerator {
	g := func(k string) float32 { return float32(v.Get(k).F64()) }
	arr2 := func(k string) [2]float32 {
		a := smplGennoiseF32v(v.Get(k))
		return [2]float32{a[0], a[1]}
	}
	arr3 := func(k string) [3]float32 {
		a := smplGennoiseF32v(v.Get(k))
		return [3]float32{a[0], a[1], a[2]}
	}
	return noiseGenerator{
		envSmth:       g("env_smth"),
		envLast:       g("env_last"),
		outStateUv:    arr2("out_state_uv"),
		outStateV:     arr2("out_state_v"),
		corrSmth:      arr3("corr_smth"),
		shapeState:    arr2("shape_state"),
		prevVoiced:    v.Get("prev_voiced").I64() != 0,
		sinceUnvoiced: int32(v.Get("since_unvoiced").I64()),
		randSeed:      int32(v.Get("rand_seed").I64()),
	}
}

// TestSmplGennoise_GenNoiseMatchesC is T2: gen_noise is PRNG- and state-driven, so the only
// deterministic validation is bit-exactness against the reference vectors. Each vector carries the
// full input `NoiseGenerator` state, the excitation, params, lsf, and the expected `noise[80]` plus
// the output `NoiseGenerator` state (`ng_out`). Seeding the generator with the captured `ng_in` and
// running our implementation must reproduce both bit-for-bit across voiced and unvoiced paths.
func TestSmplGennoise_GenNoiseMatchesC(t *testing.T) {
	recs := loadPacked(t, "gennoise_vectors.cbor.zst")
	smplGennoiseCheckGennoise(t, recs)
}

func TestSmplGennoise_GenNoiseMatchesShippedWasm(t *testing.T) {
	recs := loadPacked(t, "wasm_gennoise.cbor.zst")
	if got := len(recs.Arr()); got != 1320 {
		t.Fatalf("assertion `left == right` failed: %d != 1320", got)
	}
	smplGennoiseCheckGennoise(t, recs)
}

func smplGennoiseCheckGennoise(t *testing.T, recs val) {
	t.Helper()
	// fcbgains_uv[ix] = 10^(0.05*(ix-90)), ix in 0..=90.
	fcbgainsUv := make([]float32, 0, 91)
	for ix := 0; ix <= 90; ix++ {
		fcbgainsUv = append(fcbgainsUv, powF32(10.0, 0.05*(float32(ix)-90.0)))
	}

	arr := recs.Arr()
	if len(arr) == 0 {
		t.Fatalf("assertion failed: !arr.is_empty()")
	}

	vChecked, uv0Checked, uvpChecked := 0, 0, 0
	for _, rec := range arr {
		voiced := rec.Get("voiced").I64() == 1
		exc := smplGennoiseF32v(rec.Get("exc_pre"))
		lsf := smplGennoiseF32v(rec.Get("lsf"))
		noiseExpected := smplGennoiseF32v(rec.Get("noise"))
		nrgres := float32(rec.Get("nrgres").F64())
		fcbg := int32(rec.Get("fcbg_idx").I64())
		np := int32(rec.Get("sf_pulses").I64())
		nbr := float32(rec.Get("norm_br").F64())
		seedOut := int32(rec.Get("seed_out").I64())

		ng := smplGennoiseNgFrom(rec.Get("ng_in"))
		ngOutExpected := smplGennoiseNgFrom(rec.Get("ng_out"))
		var noise [smplMaxSfLen]float32
		smplCelpGenNoise(&ng, exc, 80, voiced, np, nrgres, fcbg, lsf, nbr, fcbgainsUv, noise[:])

		// Seed transition is bit-exact (proves the PRNG + gen_rand_pulses call count).
		if ng.randSeed != seedOut {
			t.Fatalf("seed_out mismatch (voiced=%v np=%d): %d != %d", voiced, np, ng.randSeed, seedOut)
		}
		if ng.randSeed != ngOutExpected.randSeed {
			t.Fatalf("ng_out.rand_seed: %d != %d", ng.randSeed, ngOutExpected.randSeed)
		}

		// noise[80] bit-exact (tiny tolerance for f32 transcendental last-ULP across libm).
		for i := 0; i < 80; i++ {
			if !(absF32(noise[i]-noiseExpected[i]) < 1e-6) {
				t.Fatalf("noise[%d] mismatch (voiced=%v np=%d): rust=%v ref=%v", i, voiced, np, noise[i], noiseExpected[i])
			}
		}
		// Output generator state bit-exact (env_last / out states drive the next subframe).
		if !(absF32(ng.envLast-ngOutExpected.envLast) < 1e-6) {
			t.Fatalf("ng_out.env_last (voiced=%v np=%d): rust=%v ref=%v", voiced, np, ng.envLast, ngOutExpected.envLast)
		}
		for k := 0; k < 2; k++ {
			if !(absF32(ng.outStateUv[k]-ngOutExpected.outStateUv[k]) < 1e-6) {
				t.Fatalf("ng_out.out_state_uv[%d]", k)
			}
			if !(absF32(ng.outStateV[k]-ngOutExpected.outStateV[k]) < 1e-6) {
				t.Fatalf("ng_out.out_state_v[%d]", k)
			}
		}

		if voiced {
			vChecked++
		} else if np == 0 {
			uv0Checked++
		} else {
			uvpChecked++
		}
	}
	if !(vChecked > 0 && uv0Checked > 0 && uvpChecked > 0) {
		t.Fatalf("vectors must exercise all three paths: voiced=%d uv0=%d uvp=%d", vChecked, uv0Checked, uvpChecked)
	}
}
