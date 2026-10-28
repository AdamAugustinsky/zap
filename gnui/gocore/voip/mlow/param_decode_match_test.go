// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/param_decode_match.rs, MIT.
//
// Invariant: the unvoiced/voiced parameter decode produces the SAME per-subframe
// `nrgres_dbq_Q14` and `fcbg_idx` as the reference (`gennoise_params_dump.json`).
//
// The gains decode (`decodeSmplGains`) reads the same bits as the reference unvoiced decode,
// just under different field names: its `gain_q` IS the `nrgres_dbq_Q14` and its per-subframe
// `nrg_res` symbol IS the `fcbg_idx`. The voiced FCB gain index is the pitch block's `filt_idx`.
// This test pins that correspondence exactly so the excitation/gen_noise inputs stay faithful.

package mlow

import (
	"slices"
	"testing"
)

func TestParamDecodeMatch_NrgresFcbgMatchCReference(t *testing.T) {
	cdump := loadPacked(t, "gennoise_params_dump.cbor.zst")
	carr := cdump.Arr()
	cmap := make(map[[3]int64]val, len(carr))
	for _, c := range carr {
		cmap[[3]int64{
			c.Get("packet").I64(),
			c.Get("frame").I64(),
			c.Get("sf").I64(),
		}] = c
	}
	if len(cmap) != len(carr) {
		t.Fatalf("duplicate (packet, frame, sf) keys in the C param dump would hide coverage")
	}

	rust := diagDecodeParams(t)
	uvNrgres, uvFcbg, vFcbg, voicedClass := 0, 0, 0, 0
	for _, r := range rust {
		key := [3]int64{int64(r.packet), int64(r.frame), int64(r.sf)}
		c, ok := cmap[key]
		if !ok {
			continue
		}
		cv := c.Get("voiced").I64() == 1
		cnrg := int32(c.Get("nrgres_dbq_Q14").I64())
		cfcbg := int32(c.Get("fcbg_idx").I64())
		cnp := int32(c.Get("sf_pulses").I64())

		if r.voiced != cv {
			t.Fatalf("voiced flag at %v: %v != %v", key, r.voiced, cv)
		}
		voicedClass++
		if cv {
			// Voiced: the FCB gain index (filt_idx) must match the reference fcbg_idx where pulses exist.
			if cnp > 0 {
				if r.fcbgIdx != cfcbg {
					t.Fatalf("voiced fcbg_idx at %v: %d != %d", key, r.fcbgIdx, cfcbg)
				}
				vFcbg++
			}
		} else {
			if r.nrgresDbqQ14 != cnrg {
				t.Fatalf("unvoiced nrgres_dbq_Q14 at %v: %d != %d", key, r.nrgresDbqQ14, cnrg)
			}
			uvNrgres++
			if cnp > 0 {
				if r.fcbgIdx != cfcbg {
					t.Fatalf("unvoiced fcbg_idx at %v: %d != %d", key, r.fcbgIdx, cfcbg)
				}
				uvFcbg++
			}
		}
	}
	if !(voicedClass > 0 && uvNrgres > 0 && uvFcbg > 0 && vFcbg > 0) {
		t.Fatalf("coverage too thin: class=%d uv_nrgres=%d uv_fcbg=%d v_fcbg=%d",
			voicedClass, uvNrgres, uvFcbg, vFcbg)
	}
}

func TestParamDecodeMatch_AllWireParametersMatchShippedWasm(t *testing.T) {
	frames := decoderFrames(t, "wasm_derived_frames.json")
	records := loadPacked(t, "wasm_params.cbor.zst").Arr()
	if len(records) != len(frames)*3 {
		t.Fatalf("records %d != frames*3 %d", len(records), len(frames)*3)
	}
	tables := loadSmplTables()
	cc := loadCcTables()
	mem := loadSmplMem()
	state := defaultSmplLsfState()
	voicedCount := 0
	unvoicedCount := 0
	for packet, frame := range frames {
		toc := parseMlowToc(frame[0])
		decoder := newRangeDecoder(frame[1:])
		for internal := 0; internal < 3; internal++ {
			r := records[packet*3+internal]
			if got := r.Get("len").I64(); got != 320 {
				t.Fatalf("len %d != 320", got)
			}
			if got := r.Get("subframes").I64(); got != 4 {
				t.Fatalf("subframes %d != 4", got)
			}
			if got := int(r.Get("frame").U64()); got != internal {
				t.Fatalf("frame %d != %d", got, internal)
			}
			if got := r.Get("cav").I64() != 0; got != toc.active {
				t.Fatalf("cav %v != %v", got, toc.active)
			}
			lsf := decodeSmplLsf(&decoder, tables, &state, 0, internal, toc.active)
			if want := int32(r.Get("voiced").I64()); lsf.stage1 != want {
				t.Fatalf("packet %d/%d: voiced: %d != %d", packet, internal, lsf.stage1, want)
			}
			indices := append([]int32{lsf.grid}, lsf.stage2[:]...)
			if want := r.Get("lsf").I32s(); !slices.Equal(indices, want) {
				t.Fatalf("packet %d/%d: LSF: %v != %v", packet, internal, indices, want)
			}
			if want := int32(r.Get("interp").I64()); lsf.extra != want {
				t.Fatalf("packet %d/%d: interpolation: %d != %d", packet, internal, lsf.extra, want)
			}
			pulses := decodeSmplPulses(
				&decoder,
				cc,
				320,
				4,
				b2i32(toc.active),
				0,
				lsf.stage1,
			)
			if want := r.Get("pulses").I32s(); !slices.Equal(pulses.pulses, want) {
				t.Fatalf("packet %d/%d: pulse positions/magnitudes: %v != %v", packet, internal, pulses.pulses, want)
			}
			if want := r.Get("sf_pulses").I32s(); !slices.Equal(pulses.subfr[:], want) {
				t.Fatalf("packet %d/%d: pulse counts: %v != %v", packet, internal, pulses.subfr, want)
			}
			var fcbg [4]int32
			if lsf.stage1 == 1 {
				voicedCount++
				pitch := decodeSmplPitch(&decoder, mem, cc, &state, 320, 4, 0, pulses.subfr)
				if want := r.Get("acbg").I32s(); !slices.Equal(pitch.gainIdx[:], want) {
					t.Fatalf("packet %d/%d: ACB gain: %v != %v", packet, internal, pitch.gainIdx, want)
				}
				if want := r.Get("lags").I32s(); !slices.Equal(pitch.blockLags[:], want) {
					t.Fatalf("packet %d/%d: pitch lags: %v != %v", packet, internal, pitch.blockLags, want)
				}
				if want := int32(r.Get("contour").I64()); pitch.contour != want {
					t.Fatalf("packet %d/%d: contour: %d != %d", packet, internal, pitch.contour, want)
				}
				fcbg = pitch.filtIdx
			} else {
				unvoicedCount++
				gains := decodeSmplGains(&decoder, cc, 4, pulses.subfr)
				if want := r.Get("energy_q14").I32s(); !slices.Equal(gains.gainQ[:], want) {
					t.Fatalf("packet %d/%d: gain Q14: %v != %v", packet, internal, gains.gainQ, want)
				}
				fcbg = gains.nrgRes
			}
			for sf, expected := range r.Get("fcbg").I32s() {
				if pulses.subfr[sf] > 0 {
					if fcbg[sf] != expected {
						t.Fatalf("packet %d/%d/%d: FCB gain: %d != %d", packet, internal, sf, fcbg[sf], expected)
					}
				}
			}
			rangeArr := r.Get("range").Arr()
			rng := make([]uint32, len(rangeArr))
			for i, x := range rangeArr {
				rng[i] = uint32(x.U64())
			}
			st := decoder.oracleState()
			if !slices.Equal(st[:], rng) {
				t.Fatalf("packet %d/%d: entropy context: %v != %v", packet, internal, st, rng)
			}
		}
	}
	if !(voicedCount > 0 && unvoicedCount > 0) {
		t.Fatalf("assertion failed: voiced_count > 0 && unvoiced_count > 0")
	}
}
