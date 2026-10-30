// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_celpdec.rs, MIT.
//
// The `#[cfg(test)]` module of smpl_celpdec.rs.

package mlow

import "testing"

// TestSmplCelpdec_ExcPreMatchesC validates the pre-noise excitation (FCB pulses * gain + voiced ACB)
// against the reference `exc_pre` dump, per subframe. This proves the excitation domain
// (`fcbgains_uv/v[fcbg_idx]`) and the voiced ACB/LTP synthesis are faithful, independent of the
// PRNG-driven noise.
func TestSmplCelpdec_ExcPreMatchesC(t *testing.T) {
	recs := loadPacked(t, "exc_pre_lags.cbor.zst")
	frames := smplCelpdecFrames(t, "inbound_capture_frames.json")
	smplCelpdecCheckExcitation(t, recs, frames, false)
}

func TestSmplCelpdec_ExcitationMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_gennoise.cbor.zst")
	if got := len(records.Arr()); got != 1320 {
		t.Fatalf("assertion `left == right` failed: %d != 1320", got)
	}
	frames := smplCelpdecFrames(t, "wasm_derived_frames.json")
	smplCelpdecCheckExcitation(t, records, frames, true)
}

// smplCelpdecFrames reads a JSON array of hex-encoded packets (serde_json::from_str::<Vec<String>>).
func smplCelpdecFrames(t *testing.T, name string) []string {
	t.Helper()
	arr := loadJSON(t, name).Arr()
	out := make([]string, len(arr))
	for i, v := range arr {
		out[i] = v.Str()
	}
	return out
}

func smplCelpdecCheckExcitation(t *testing.T, recs val, frames []string, includeInactive bool) {
	t.Helper()
	carr := recs.Arr()

	// Key the reference exc_pre by (packet, frame, sf).
	cmap := make(map[[3]int64]val, len(carr))
	for _, c := range carr {
		cmap[[3]int64{c.Get("packet").I64(), c.Get("frame").I64(), c.Get("sf").I64()}] = c
	}

	tbl := loadSmplTables()
	synthT := loadSmplSynthTables()
	mem := loadSmplMem()
	cc := loadCcTables()
	lstate := defaultSmplLsfState()
	celp := defaultCelpDecState()
	celp.dbgExcPre = make([]float32, 0, smplNumSubfr*smplSubfrLen)
	var prevNlsf []float32

	uvOk, uvBad, vOk, vBad := 0, 0, 0, 0
	worst := float32(0)
	for packet, hexFrame := range frames {
		frame, err := hexDecode(hexFrame)
		if err != nil {
			t.Fatal(err)
		}
		if len(frame) == 0 {
			continue
		}
		toc := parseMlowToc(frame[0])
		if toc.stdOpus || toc.sid || (!includeInactive && !toc.active) {
			continue
		}
		config := int(frame[0]>>2) & 1
		lowRate := (frame[0]>>2)&1 != 0
		dec := newRangeDecoder(frame[1:])
		for f := 0; f < 3; f++ {
			lsf := decodeSmplLsf(&dec, tbl, &lstate, config, f, toc.active)
			pulses := decodeSmplPulses(&dec, cc, 320, 4, b2i32(toc.active), int32(config), lsf.stage1)
			voiced := lsf.stage1 == 1
			params := celpDecParams{
				voiced:   voiced,
				sfPulses: pulses.subfr,
			}
			for _, n := range pulses.subfr {
				params.totalPulses += n
			}
			if voiced {
				pr := decodeSmplPitch(&dec, mem, cc, &lstate, 320, 4, int32(config), pulses.subfr)
				for b := 0; b < 8; b++ {
					params.blockLags[b] = float32(minF64(float64(float64(pr.blockLags[b])*0.5)+32.0, 320.0))
				}
				for sf := 0; sf < 4; sf++ {
					params.acbgIdx[sf] = pr.gainIdx[sf]
					params.fcbgIdx[sf] = max(pr.filtIdx[sf], 0)
				}
			} else {
				g := decodeSmplGains(&dec, cc, 4, pulses.subfr)
				params.nrgresDbqQ14 = g.gainQ
				params.fcbgIdx = g.nrgRes
			}
			nlsf := smplReconstructNlsf(synthT, int(lsf.stage1), config, int(lsf.grid), &lsf.stage2, prevNlsf)
			celp.dbgExcPre = celp.dbgExcPre[:0]
			var sig [smplFrameLen]float32
			celp.synthFrame(nlsf, int(lsf.extra), pulses.pulses, &params, lowRate, 320, sig[:])
			prevNlsf = nlsf
			// Compare each subframe's exc_pre to the reference.
			for sf := 0; sf < 4; sf++ {
				c, ok := cmap[[3]int64{int64(packet), int64(f), int64(sf)}]
				if !ok {
					continue
				}
				// Cross-check that our reconstructed per-block lags equal the reference dump's two
				// lags for this subframe (the decode that drives the ACB/LTP basis).
				if voiced {
					clags := c.Get("lags").Arr()
					c0 := float32(clags[0].F64())
					c1 := float32(clags[1].F64())
					if params.blockLags[2*sf] != c0 || params.blockLags[2*sf+1] != c1 {
						t.Fatalf("per-block lags diverge at pkt=%d f=%d sf=%d: (%v, %v) != (%v, %v)",
							packet, f, sf, params.blockLags[2*sf], params.blockLags[2*sf+1], c0, c1)
					}
				}
				// Reconstruct the dense reference exc_pre from the sparse nonzero list.
				var cexc [smplSubfrLen]float32
				for _, pair := range c.Get("nz").Arr() {
					p := pair.Arr()
					idx := int(p[0].U64())
					cexc[idx] = float32(p[1].F64())
				}
				base := sf * smplSubfrLen
				bad := false
				for i := 0; i < smplSubfrLen; i++ {
					d := absF32(celp.dbgExcPre[base+i] - cexc[i])
					worst = maxF32(worst, d)
					// Excitation amplitudes are ~1e-4; a tight absolute tolerance.
					if d > 2e-5 {
						bad = true
					}
				}
				if voiced {
					if bad {
						vBad++
					} else {
						vOk++
					}
				} else if bad {
					uvBad++
				} else {
					uvOk++
				}
			}
		}
	}
	t.Logf("exc_pre vs reference: unvoiced ok=%d bad=%d; voiced ok=%d bad=%d; worst abs diff=%.2e", uvOk, uvBad, vOk, vBad, worst)
	// Unvoiced excitation is deterministic (pulses * fcbgains_uv), so it must match.
	if uvBad != 0 {
		t.Fatalf("unvoiced exc_pre diverges from reference (%d subframes)", uvBad)
	}
	// Voiced excitation (FCB pulses * gain + ACB/LTP per-block-lag synthesis) is also deterministic.
	if vBad != 0 {
		t.Fatalf("voiced exc_pre diverges from reference (%d subframes)", vBad)
	}
}
