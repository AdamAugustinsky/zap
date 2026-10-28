// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_vad.rs, MIT.
//
// Tests of the SILK VAD (the `tests` and `ground_truth_tests` modules).

package mlow

import "testing"

func TestSmplVad_SilenceGivesLowActivity(t *testing.T) {
	vad := newSmplVadState()
	pcm := make([]int16, 960)
	r := vad.processPacket(pcm, 320)
	for _, v := range r.vadResults {
		if !(v <= 0.05) {
			t.Fatalf("silence activity %v", v)
		}
	}
}

func TestSmplVad_OnsetOfSpeechlikeSignalIsActive(t *testing.T) {
	// The SILK VAD adapts its noise floor toward a sustained signal, so a fair test settles the
	// floor on quiet input, then presents a loud broadband (speech-like) onset and checks the
	// activity rises on that onset before the floor catches up.
	vad := newSmplVadState()
	quiet := make([]int16, 960)
	for range 30 {
		vad.processPacket(quiet, 320)
	}
	seed := uint32(0x1234_5678)
	loud := make([]int16, 960)
	for i := range loud {
		tt := float32(i) / 16000.0
		tone := sinF32(float32(2.0)*smplPi*200.0*tt) +
			sinF32(float32(2.0)*smplPi*900.0*tt) +
			sinF32(float32(2.0)*smplPi*2500.0*tt)
		seed = seed*1664525 + 1013904223
		noise := float32(float32(seed>>16)/65536.0) - 0.5
		loud[i] = f32ToI16(6000.0 * (tone/3.0 + float32(0.2*noise)))
	}
	r := vad.processPacket(loud, 320)
	onset := maxF32(maxF32(r.vadResults[0], r.vadResults[1]), r.vadResults[2])
	if !(onset > 0.5) {
		t.Fatalf("speech-like onset activity %v", onset)
	}
}

// The SILK VAD is fixed-point, so its per-frame speech-activity probability and the packet-level
// coded_as_active_voice flag must match the reference bit-exactly. `vad_ground_truth.json` is the
// ground-truth SA_Q8 dump on the synthetic mic (`synth_mic.raw`), over the packets where the
// carried noise-level state stays bit-exact.
func TestSmplVad_VadMatchesCGroundTruth(t *testing.T) {
	samples := s16le(testdataBytes(t, "synth_mic.raw"))
	gt := loadJSON(t, "vad_ground_truth.json").Arr()

	vad := newSmplVadState()
	type res struct {
		spact float32
		cav   int32
	}
	var results []res
	for off := 0; off < len(samples); off += 960 {
		chunk := samples[off:min(off+960, len(samples))]
		if len(chunk) < 960 {
			break
		}
		r := vad.processPacket(chunk, 320)
		for f := 0; f < 3; f++ {
			results = append(results, res{r.vadResults[f], b2i32(r.codedAsActiveVoice)})
		}
	}

	for _, rec := range gt {
		pkt := int(rec.Get("pkt").U64())
		frame := int(rec.Get("frame").U64())
		idx := pkt*3 + frame
		spact, cav := results[idx].spact, results[idx].cav
		cSpact := float32(rec.Get("spact").F64())
		cCav := int32(rec.Get("cav").I64())
		if !(absF32(spact-cSpact) < 1e-4) {
			t.Fatalf("pkt %d frame %d: spact %v != ref %v", pkt, frame, spact, cSpact)
		}
		if cav != cCav {
			t.Fatalf("pkt %d frame %d: cav mismatch: %d != %d", pkt, frame, cav, cCav)
		}
	}
}

// The shipped encoder's own voice-activity routing, derived with
// `oracle derive --spec specs/mlow_110frames.json` in unwasm (see
// PROVENANCE.md): per 960-sample packet, `cav` is 0 for DTX (`0x10`)
// and 1 otherwise — hangover frames (`0x12`) count as active, matching
// the decoder's own routing (see the DTX-off test in decoder.rs) and
// the encoder's internal active/inactive counters. Our SILK VAD must
// route the same packets the same way, over the whole stream — the C
// ground truth above only covers a truncated prefix.
func TestSmplVad_VadMatchesWasmRouting(t *testing.T) {
	samples := s16le(testdataBytes(t, "synth_mic.raw"))
	gt := loadJSON(t, "wasm_derived_vad.json").Arr()

	vad := newSmplVadState()
	nActive := 0
	for pkt, off := 0, 0; off < len(samples); pkt, off = pkt+1, off+960 {
		chunk := samples[off:min(off+960, len(samples))]
		if len(chunk) < 960 {
			break
		}
		r := vad.processPacket(chunk, 320)
		rec := gt[pkt]
		wCav := int32(rec.Get("cav").I64())
		if b2i32(r.codedAsActiveVoice) != wCav {
			t.Fatalf("pkt %d: cav mismatch against shipped routing: %d != %d", pkt, b2i32(r.codedAsActiveVoice), wCav)
		}
		nActive += int(wCav)
	}
	if len(gt) != 110 {
		t.Fatalf("vector must cover the whole synth stream: %d != 110", len(gt))
	}
	if nActive != 99 {
		t.Fatalf("routing must retain 99 active / 11 inactive packets: %d != 99", nActive)
	}
}

func TestSmplVad_SpeechActivityMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_signal_mode.cbor.zst").Arr()
	samples := s16le(testdataBytes(t, "synth_mic.raw"))
	vad := newSmplVadState()
	if len(records) != 330 {
		t.Fatalf("records %d != 330", len(records))
	}
	for packet := 0; (packet+1)*960 <= len(samples); packet++ {
		pcm := samples[packet*960 : (packet+1)*960]
		result := vad.processPacket(pcm, 320)
		for internal := 0; internal < 3; internal++ {
			expected := float32(records[packet*3+internal].Get("sp_act_prob").F64())
			if result.vadResults[internal] != expected {
				t.Fatalf("packet %d, frame %d: speech activity: %v != %v", packet, internal, result.vadResults[internal], expected)
			}
		}
	}
}
