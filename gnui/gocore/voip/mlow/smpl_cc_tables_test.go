// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_cc_tables.rs, MIT.

package mlow

import (
	"encoding/json"
	"slices"
	"testing"
)

// ccTablesU8s converts a JSON number array (decoded as []int) to bytes, like serde's Vec<u8>.
func ccTablesU8s(v []int) []byte {
	if v == nil {
		panic("cc_seed.json: missing u8 array")
	}
	out := make([]byte, len(v))
	for i, x := range v {
		if x < 0 || x > 255 {
			panic("cc_seed.json: u8 out of range")
		}
		out[i] = byte(x)
	}
	return out
}

// ccTablesSeedFromJson parses the (gitignored) cc_seed.json dump.
func ccTablesSeedFromJson(s string) ccSeed {
	// encoding/json reads []byte as base64, so the u8 arrays go through []int.
	var r struct {
		NrgresGain4Dcmf    []int   `json:"nrgres_gain4_dcmf"`
		NrgresShape4Dcmf   []int   `json:"nrgres_shape4_dcmf"`
		FcbgOffsetDcmf     []int   `json:"fcbg_offset_dcmf"`
		AcbgainsHrDcmf     []int   `json:"acbgains_hr_dcmf"`
		FcbgainsVDcmf      []int   `json:"fcbgains_v_dcmf"`
		FcbgainsVDeltaDcmf []int   `json:"fcbgains_v_delta_dcmf"`
		AcbgainsCbHrQ14    []int32 `json:"acbgains_cb_hr_q14"`
		GainReconBase      uint32  `json:"gain_recon_base"`
		GainRecon          []int   `json:"gain_recon"`
		NPulsesDcmfBgn     []int   `json:"n_pulses_dcmf_bgn"`
		NPulsesDcmfUv      []int   `json:"n_pulses_dcmf_uv"`
		NPulsesDcmfV       []int   `json:"n_pulses_dcmf_v"`
	}
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		panic("cc_seed.json: " + err.Error())
	}
	return ccSeed{
		nrgresGain4Dcmf:    ccTablesU8s(r.NrgresGain4Dcmf),
		nrgresShape4Dcmf:   ccTablesU8s(r.NrgresShape4Dcmf),
		fcbgOffsetDcmf:     ccTablesU8s(r.FcbgOffsetDcmf),
		acbgainsHrDcmf:     ccTablesU8s(r.AcbgainsHrDcmf),
		fcbgainsVDcmf:      ccTablesU8s(r.FcbgainsVDcmf),
		fcbgainsVDeltaDcmf: ccTablesU8s(r.FcbgainsVDeltaDcmf),
		acbgainsCbHrQ14:    r.AcbgainsCbHrQ14,
		gainReconBase:      r.GainReconBase,
		gainRecon:          ccTablesU8s(r.GainRecon),
		nPulsesDcmfBgn:     ccTablesU8s(r.NPulsesDcmfBgn),
		nPulsesDcmfUv:      ccTablesU8s(r.NPulsesDcmfUv),
		nPulsesDcmfV:       ccTablesU8s(r.NPulsesDcmfV),
	}
}

// Transitional gate: every migrated logical table must equal the old `mem.cdfAt`/`i16` heap read
// byte-identical. The full `smpl_cc_blob.json` dump is the oracle (the carved `.bin` only holds
// Group D now); skipped when the JSON is absent (CI).
//
// DOES NOT RUN IN CI (oracle gitignored); the CI guard for these tables is
// `lsf_seed_build_golden_checksums` + `golden_roundtrip`. A green CI does not imply this ran.
func TestSmplCcTables_CcTablesByteIdenticalToHeap(t *testing.T) {
	cc := loadCcTables()
	mem, ok := tryLoadFullHeap()
	if !ok {
		t.Skip("smpl_cc_blob.json absent; skipping byte-identical gate")
	}
	gNrg := mem.gNrg
	gPitch := mem.gPitch

	// Group A: nrgres_gain4 / nrgres_shape4.
	if !slices.Equal(cc.nrgresGain4(), mem.cdfAt(gNrg+0x1362, 85)) {
		t.Fatalf("nrgres_gain4")
	}
	if !slices.Equal(cc.nrgresShape4(), mem.cdfAt(gNrg+0x1098, 99)) {
		t.Fatalf("nrgres_shape4")
	}

	// Group A: fcbg_offset[2][bucket] over all valid min_offset shifts.
	nrgBase := gNrg + 2*0x588
	for bucket := 0; bucket < 4; bucket++ {
		cdfp := nrgBase + uint32(bucket)*0x162
		for minOff := 0; minOff <= 85; minOff++ {
			off := cdfp + (uint32(minOff) << 1)
			if !slices.Equal(cc.fcbgOffset(2, bucket, minOff), mem.cdfAt(off, 92)) {
				t.Fatalf("fcbg_offset bucket=%d min_off=%d", bucket, minOff)
			}
		}
	}

	// Group C: HR ACB-gain rows (the active p6==0 path reads gp+0x302).
	for prev := int32(-1); prev < 16; prev++ {
		row := (gPitch + 0x302) + uint32(prev*0x22) + 0x22
		if !slices.Equal(cc.acbgainRow(prev), mem.cdfAt(row, 17)) {
			t.Fatalf("acbgain_row %d", prev)
		}
	}
	// Group C: HR ACB-gain weights at 0xe85b0.
	for gi := int32(0); gi < 16; gi++ {
		w0 := int32(mem.i16(0xe85b0 + uint32(gi)*4))
		w2 := int32(mem.i16(0xe85b0 + uint32(gi)*4 + 2))
		if a, b := cc.acbgainWeights(gi); a != w0 || b != w2 {
			t.Fatalf("acbgain_weights %d", gi)
		}
	}
	// Group C: fcbgains_v + delta. (The p6!=0 LR filter path reads these same shared tables.)
	if !slices.Equal(cc.fcbgainV(), mem.cdfAt(gPitch+0xdc4, 35)) {
		t.Fatalf("fcbgain_v")
	}
	for prevFilt := int32(0); prevFilt < 34; prevFilt++ {
		if !slices.Equal(cc.fcbgainVDelta(prevFilt), mem.cdfAt((gPitch+0xe4c)-uint32(prevFilt)*2, 35)) {
			t.Fatalf("fcbgain_v_delta %d", prevFilt)
		}
	}

	// Group C LR (p6!=0): ACB-gain rows at gp+0xc0 and weights at 0xe8460. This is the gate for
	// the low-rate pitch path, which the golden vectors may not exercise.
	for prev := int32(-1); prev < 16; prev++ {
		row := (gPitch + 0xc0) + uint32(prev*0x22) + 0x22
		if !slices.Equal(cc.acbgainRowLr(prev), mem.cdfAt(row, 17)) {
			t.Fatalf("acbgain_row_lr %d", prev)
		}
	}
	for gi := int32(0); gi < 16; gi++ {
		w0 := int32(mem.i16(0xe8460 + uint32(gi)*4))
		w2 := int32(mem.i16(0xe8460 + uint32(gi)*4 + 2))
		if a, b := cc.acbgainWeightsLr(gi); a != w0 || b != w2 {
			t.Fatalf("acbgain_weights_lr %d", gi)
		}
	}

	// Group E: nrg-step + gain-reconstruction codebook.
	for cfg := int32(0); cfg < 3; cfg++ {
		if cc.nrgStep(cfg) != int32(mem.i16(0xf35e0+uint32(cfg)*2)) {
			t.Fatalf("nrg_step %d", cfg)
		}
	}
	for _, p4 := range []bool{true, false} {
		var base uint32 = 0xf3970
		if p4 {
			base = 0xf35f0
		}
		for idx := int32(0); idx < 400; idx++ {
			if cc.gainRecon(p4, idx) != int32(mem.i16(base+uint32(idx)*2)) {
				t.Fatalf("gain_recon p4=%v idx=%d", p4, idx)
			}
		}
	}

	gCc := mem.gCc

	// Group B: pulse-count CDFs (the full stored CmfVec at g_cc+idx*8+0x11d8 -> ptr, +0x11dc -> len).
	for idx := int32(0); idx < 3; idx++ {
		ent := gCc + uint32(idx)*8
		ptr := mem.u32(ent + 0x11d8)
		n := int(mem.u32(ent + 0x11dc))
		if !slices.Equal(cc.nPulseCount(idx), mem.cdfAt(ptr, n)) {
			t.Fatalf("n_pulse_count %d", idx)
		}
	}

	// Group B: split CDFs split_CMFs[total-1] over all num_pulses, full row.
	for total := int32(1); total <= splitNumTables; total++ {
		ptr := mem.u32(gCc + uint32(total)*8 + 0xcd0)
		minSplit := max(total-int32(smplMaxPulsesPerSf)*2, 0)
		maxSplit := total - minSplit
		rowLen := int(maxSplit - minSplit + 2)
		if !slices.Equal(cc.splitCmf(total), mem.cdfAt(ptr, rowLen)) {
			t.Fatalf("split_cmf %d", total)
		}
	}

	// Group B: run-length CDFs per (oct, c), full row + max_samples base value.
	for oct := int32(1); oct <= numRunlenCmfs; oct++ {
		bucket := cc.runlen(oct)
		cBaseOff := int32(mem.u32(gCc + uint32(oct)*0xa4))
		if bucket.maxSamples() != cBaseOff {
			t.Fatalf("runlen max_samples oct=%d", oct)
		}
		rowLen := int(cBaseOff + 1)
		for c := int32(1); c <= smplMaxPulsesPerSf; c++ {
			ptr := mem.u32(gCc + uint32(oct)*0xa4 + uint32(c-1)*4 - 0xa0)
			if !slices.Equal(bucket.cmf(c), mem.cdfAt(ptr, rowLen)) {
				t.Fatalf("runlen cmf oct=%d c=%d", oct, c)
			}
		}
	}
}
