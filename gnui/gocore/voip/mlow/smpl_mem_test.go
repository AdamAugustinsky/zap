// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_mem.rs, MIT.

package mlow

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// parseSmplMemJson parses the full heap-window JSON dump into a `smplMem` (the generator's carve
// source and the byte-identical oracle while Group D still reads the heap).
func parseSmplMemJson(s string) smplMem {
	var raw struct {
		Regions []struct {
			Base uint32 `json:"base"`
			B64  string `json:"b64"`
		} `json:"regions"`
		GCc    uint32 `json:"g_cc"`
		GNrg   uint32 `json:"g_nrg"`
		GPitch uint32 `json:"g_pitch"`
		Clk    uint32 `json:"clk"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		panic("smpl_cc_blob.json must parse: " + err.Error())
	}
	regions := make([]smplMemRegion, 0, len(raw.Regions))
	for _, r := range raw.Regions {
		data, err := base64.StdEncoding.DecodeString(r.B64)
		if err != nil {
			panic("smpl_cc_blob region b64: " + err.Error())
		}
		regions = append(regions, smplMemRegion{base: r.Base, data: data})
	}
	return smplMem{
		regions: regions,
		gCc:     raw.GCc,
		gNrg:    raw.GNrg,
		gPitch:  raw.GPitch,
		gClk:    raw.Clk,
	}
}

// tryLoadFullHeap loads the full heap dump from the (gitignored) `smpl_cc_blob.json` oracle; ok
// is false if absent (CI has only the carved `.bin`; the byte-identical gates run where the JSON
// lives).
func tryLoadFullHeap() (smplMem, bool) {
	b, err := os.ReadFile(filepath.Join("testdata", "smpl_cc_blob.json"))
	if err != nil {
		return smplMem{}, false
	}
	return parseSmplMemJson(string(b)), true
}

// Byte-identical gate: the window BUILT FROM THE SEED (`buildSmplMem`) must equal the full
// heap dump at every Group-D contour read (the pcfg pointer-chase). A read that the build gets
// wrong, or one that falls outside the built regions (reads 0 where the blob had data), would
// silently corrupt the bitstream. The p6!=0 LR gain/filter/weight tables moved to `ccTables`
// and are gated there. Skipped when the JSON oracle is absent (CI).
//
// DOES NOT RUN IN CI (oracle gitignored); the CI guard for these tables is
// `lsf_seed_build_golden_checksums` + `golden_roundtrip`. A green CI does not imply this ran.
func TestSmplMem_PitchContourWindowCompleteVsHeap(t *testing.T) {
	full, ok := tryLoadFullHeap()
	if !ok {
		t.Skip("smpl_cc_blob.json absent; skipping completeness gate")
	}
	window := loadSmplMem()
	gclk := full.gClk

	// Every concrete Group-D read lands inside a built region and equals the full heap there.
	pcfg := gclk + 0x5704
	chkU8 := func(a uint32) {
		if window.u8(a) != full.u8(a) {
			t.Fatalf("u8 miss @ %#x", a)
		}
	}
	chkU32 := func(a uint32) {
		if window.u32(a) != full.u32(a) {
			t.Fatalf("u32 miss @ %#x", a)
		}
	}
	chkCdf := func(a uint32, n int) {
		if !slices.Equal(window.cdfAt(a, n), full.cdfAt(a, n)) {
			t.Fatalf("cdf miss @ %#x", a)
		}
	}

	// pcfg header pointers.
	for _, off := range []uint32{22240, 22244, 22248, 22252, 22268} {
		chkU32(pcfg + off)
	}
	numContours := int32(full.u32(pcfg + 22240))
	contourMap := full.u32(pcfg + 22244)
	lagCdf := full.u32(pcfg + 22248)
	fracBase := full.u32(pcfg + 22252)
	deltaCdf := full.u32(pcfg + 22268)

	// primary-lag CDFs: absolute (n=num_contours+1) and all delta sub-slices.
	chkCdf(lagCdf, int(max(numContours+1, 0)))
	for di := uint32(0); di < 10; di++ {
		lo := int32(full.u8(0xe7ef0 + di*2))
		hi := int32(full.u8(0xe7ef0 + di*2 + 1))
		chkU8(0xe7ef0 + di*2)
		chkU8(0xe7ef0 + di*2 + 1)
		rN := (hi - lo) + 2
		if rN >= 2 {
			chkCdf(lagCdf+uint32(lo)*2, int(rN))
		}
	}

	// delta_cdf rows for every prev_lag the contour base-lags can produce.
	var maxPrev int32
	for c := int32(0); c < numContours; c++ {
		ctrBase := pcfg + uint32(c)*0x44
		sc := max(full.i32(ctrBase+0x1d78), 1)
		for s := int32(0); s < sc; s++ {
			maxPrev = max(maxPrev, full.i32(ctrBase+0x1d38+uint32(s)*4))
		}
	}
	for prev := int32(0); prev <= maxPrev; prev++ {
		chkCdf(deltaCdf+uint32(prev)*20, 10)
	}

	// contour_map[0..217].
	for i := uint32(0); i < 217; i++ {
		chkU8(contourMap + i)
	}

	// per-contour records + frac CDFs.
	for c := int32(0); c < numContours; c++ {
		ctrBase := pcfg + uint32(c)*0x44
		sc := full.i32(ctrBase + 0x1d78)
		chkU32(ctrBase + 0x1d78)
		chkU32(ctrBase + 0x1d38) // base lag
		chkU32(ctrBase + 0x1d58) // seg-len 0
		for s := int32(0); s < max(sc, 0); s++ {
			chkU32(ctrBase + 0x1d38 + uint32(s)*4)
			chkU32(ctrBase + 0x1d58 + uint32(s)*4)
		}
		// frac CDFs: every seg_sel * every reachable nl2 offset within the 0x280 table.
		for segSel := uint32(0); segSel < 3; segSel++ {
			fracSegBase := fracBase + segSel*0x280
			for nl2 := int32(0); nl2 < 0x40; nl2++ {
				off := fracSegBase + uint32(nl2*2) + 0xfe
				chkCdf(off, 65)
			}
		}
	}
}
