// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_harm_postfilter.rs, MIT.
//
// The `#[cfg(test)]` module of smpl_harm_postfilter.rs, plus its test-only `TOT_POSTFILT_DELAY`.

package mlow

import (
	"encoding/binary"
	"math"
	"testing"
)

// smplHarmPostfilterTotPostfiltDelay is the total group delay the harmonic postfilter introduces
// (`SMPL_TOT_POSTFILT_DELAY`), so the decoded PCM aligns at lag 0 with the reference. Used by the
// delay-aligned round-trip/validation tests.
const smplHarmPostfilterTotPostfiltDelay = fbDelay + harmDelay // 48

func smplHarmPostfilterRf32(b []byte, o *int) float32 {
	v := math.Float32frombits(binary.LittleEndian.Uint32(b[*o : *o+4]))
	*o += 4
	return v
}

func smplHarmPostfilterRi32(b []byte, o *int) int32 {
	v := int32(binary.LittleEndian.Uint32(b[*o : *o+4]))
	*o += 4
	return v
}

// TestSmplHarmPostfilter_HarmPostfilterMatchesC validates `smpl_harm_postfilter` against the
// instrumented reference decoder, processing the full active packet sequence in order (the filter
// carries StateComb/state1/prev_lag across packets). The dump carries, per packet, the per-block
// lags, the packet bitrate, the input (post-hp) signal, and the reference output.
//
// The reference is `-ffast-math` (reassociating/FMA-contracting), so this is not bit-for-bit.
// Two regimes: every voiced packet and every steady silence packet match within the i16 output
// quantization step (the comb math is feed-forward there). The only larger residual is the first
// 48 samples of a *silence packet immediately following voiced*: the comb's zero-input response,
// driven recursively by the prior frame's `-ffast-math`-built LP coefficients. That residual is
// bounded by `TRANSITION_TOL` and only lands on near-silent transitions, so it is inaudible.
func TestSmplHarmPostfilter_HarmPostfilterMatchesC(t *testing.T) {
	const i16Lsb float32 = 1.0 / 32768.0
	// The voiced→silence transition zero-input response under -ffast-math; bulk stays under I16_LSB.
	const transitionTol float32 = 6.0e-4
	data := loadZstd(t, "harm_postfilter_vectors.raw.zst")
	o := 0
	count := smplHarmPostfilterRi32(data, &o)
	st := defaultHarmPostfilterState()
	worst := float32(0)
	worstSteady := float32(0)
	for range count {
		_ = smplHarmPostfilterRi32(data, &o) // packet
		plen := int(smplHarmPostfilterRi32(data, &o))
		nlags := int(smplHarmPostfilterRi32(data, &o))
		nbr := smplHarmPostfilterRf32(data, &o)
		lags := make([]float32, nlags)
		for i := range lags {
			lags[i] = smplHarmPostfilterRf32(data, &o)
		}
		inp := make([]float32, plen)
		for i := range inp {
			inp[i] = smplHarmPostfilterRf32(data, &o)
		}
		cout := make([]float32, plen)
		for i := range cout {
			cout[i] = smplHarmPostfilterRf32(data, &o)
		}
		// A silent packet (lag0 == 0) carries the transition zero-input response in its first 48
		// samples; everywhere else is the i16-exact regime.
		transition := lags[0] == 0.0
		// The C audit corpus predates the shipped tuning.
		harmPostfilterWithTuning(&st, inp, plen, lags, nlags, nbr, harmTuning{
			feedback: 0.4734,
			strength: 0.6438,
		})
		for i := 0; i < plen; i++ {
			d := absF32(inp[i] - cout[i])
			worst = maxF32(worst, d)
			if !(transition && i < smplHarmPostfilterTotPostfiltDelay) {
				worstSteady = maxF32(worstSteady, d)
			}
		}
	}
	t.Logf("harm_postfilter vs reference: packets=%d worst=%.2e worst_steady=%.2e (i16 LSB=%.2e)", count, worst, worstSteady, i16Lsb)
	if !(worstSteady < i16Lsb) {
		t.Fatalf("harm_postfilter steady-state diverges from reference by %.2e (>= i16 LSB %.2e)", worstSteady, i16Lsb)
	}
	if !(worst < transitionTol) {
		t.Fatalf("harm_postfilter transition residual %.2e exceeds tolerance %.2e", worst, transitionTol)
	}
}

func TestSmplHarmPostfilter_HarmPostfilterMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_harm_postfilter.cbor.zst").Arr()
	if len(records) != 110 {
		t.Fatalf("assertion `left == right` failed: %d != 110", len(records))
	}
	for i, r := range records {
		initial := r.Get("state_in").F32s()
		state := harmPostfilterState{
			state1:        [16]float32(initial[:16]),
			lpcoefs:       [17]float32(initial[16:33]),
			stateComb:     append([]float32(nil), initial[33:2313]...),
			prevLag:       f32ToI32(initial[2313]),
			prevDidFilter: f32ToI32(initial[2314]),
		}
		out := r.Get("input").F32s()
		lags := r.Get("lags").F32s()
		smplHarmPostfilter(&state, out, 960, lags, 24, float32(r.Get("norm_br").F64()))
		expectedState := r.Get("state_out").F32s()
		if want := f32ToI32(expectedState[2313]); state.prevLag != want {
			t.Fatalf("packet %d: previous lag: %d != %d", i, state.prevLag, want)
		}
		if want := f32ToI32(expectedState[2314]); state.prevDidFilter != want {
			t.Fatalf("packet %d: filter state: %d != %d", i, state.prevDidFilter, want)
		}
		stateValues := make([]float32, 0, 2313)
		stateValues = append(stateValues, state.state1[:]...)
		stateValues = append(stateValues, state.lpcoefs[:]...)
		stateValues = append(stateValues, state.stateComb...)
		for k := 0; k < len(stateValues) && k < 2313; k++ {
			got, want := stateValues[k], expectedState[k]
			if !(absF32(got-want) < 6e-4) {
				t.Fatalf("packet %d: harmonic state %v vs %v", i, got, want)
			}
		}
		wantOut := r.Get("output").F32s()
		for k := 0; k < len(out) && k < len(wantOut); k++ {
			if !(absF32(out[k]-wantOut[k]) < 6e-4) {
				t.Fatalf("packet %d: harmonic output %v vs %v", i, out[k], wantOut[k])
			}
		}
	}
}
