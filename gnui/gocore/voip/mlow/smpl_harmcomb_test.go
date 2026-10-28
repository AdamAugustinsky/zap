// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_harmcomb.rs, MIT.
//
// The `#[cfg(test)]` module of smpl_harmcomb.rs.

package mlow

import (
	"encoding/binary"
	"math"
	"testing"
)

// smplHarmcombSeedState seeds a state from a snapshot (test-only; mirrors the dumped field order).
func smplHarmcombSeedState(
	lo1 float32,
	lo2 float32,
	hp [4]float32,
	lagOld float32,
	xOld [frameLen]float32,
	coefMa [3]float32,
	coefAr [3]float32,
) hpPostfilterState {
	st := defaultHpPostfilterState()
	st.stateLoEmph1 = lo1
	st.stateLoEmph2 = lo2
	st.stateHp = hp
	st.lagOld = lagOld
	st.xOld = xOld
	st.coefMa = coefMa
	st.coefAr = coefAr
	return st
}

func smplHarmcombRf32(b []byte, o *int) float32 {
	v := math.Float32frombits(binary.LittleEndian.Uint32(b[*o : *o+4]))
	*o += 4
	return v
}

func smplHarmcombRi32(b []byte, o *int) int32 {
	v := int32(binary.LittleEndian.Uint32(b[*o : *o+4]))
	*o += 4
	return v
}

// TestSmplHarmcomb_HpPostfilterMatchesC validates `smpl_hp_postfilter` against the instrumented
// reference decoder. Each active frame is self-contained: the dump carries the postfilter state in,
// the 8 per-40-block lags, the pre-hp signal, and the post-hp signal; we seed, run, and compare
// sample-for-sample. The frame lag is the energy-weighted mean `sum(l^2)/sum(l)` over the 8 lags (0
// -> default 50 Hz curve).
//
// The reference is built with `-ffast-math -mavx`, which reassociates the recursive AR/MA
// accumulations (and may contract to FMA). Straightforward IEEE-strict Rust therefore cannot
// reproduce its output to the last bit through the near-unit-circle pitch-comb feedback: a 1-ULP
// de-emphasis difference drifts to ~1.5e-5 across the resonant pole. We assert the error stays
// well under the i16 output quantization step (1/32768 ~= 3.05e-5), i.e. inaudible and identical
// once written to the 16-bit PCM the codec emits.
func TestSmplHarmcomb_HpPostfilterMatchesC(t *testing.T) {
	const i16Lsb float32 = 1.0 / 32768.0
	data := loadZstd(t, "hp_postfilter_vectors.raw.zst")
	o := 0
	count := smplHarmcombRi32(data, &o)
	worst := float32(0)
	for range count {
		_ = smplHarmcombRi32(data, &o) // packet
		_ = smplHarmcombRi32(data, &o) // frame
		var lags [8]float32
		for i := range lags {
			lags[i] = smplHarmcombRf32(data, &o)
		}
		lo1 := smplHarmcombRf32(data, &o)
		lo2 := smplHarmcombRf32(data, &o)
		var hp [4]float32
		for i := range hp {
			hp[i] = smplHarmcombRf32(data, &o)
		}
		lagOld := smplHarmcombRf32(data, &o)
		var xOld [frameLen]float32
		for i := range xOld {
			xOld[i] = smplHarmcombRf32(data, &o)
		}
		var coefMa [3]float32
		for i := range coefMa {
			coefMa[i] = smplHarmcombRf32(data, &o)
		}
		var coefAr [3]float32
		for i := range coefAr {
			coefAr[i] = smplHarmcombRf32(data, &o)
		}
		yPre := make([]float32, frameLen)
		for i := range yPre {
			yPre[i] = smplHarmcombRf32(data, &o)
		}
		yPost := make([]float32, frameLen)
		for i := range yPost {
			yPost[i] = smplHarmcombRf32(data, &o)
		}

		var lag float32
		if lags[0] > 0.0 {
			sl, sll := float32(0), float32(0)
			for _, l := range lags {
				sl += l
				sll += float32(l * l)
			}
			lag = sll / sl
		} else {
			lag = 0.0
		}

		st := smplHarmcombSeedState(lo1, lo2, hp, lagOld, xOld, coefMa, coefAr)
		out := make([]float32, frameLen)
		smplHpPostfilter(&st, yPre, frameLen, lag, out)
		for i := 0; i < frameLen; i++ {
			worst = maxF32(worst, absF32(out[i]-yPost[i]))
		}
	}
	t.Logf("hp_postfilter vs reference: frames=%d worst_abs_diff=%.2e (i16 LSB=%.2e)", count, worst, i16Lsb)
	if !(worst < i16Lsb) {
		t.Fatalf("hp_postfilter diverges from reference by %.2e (>= i16 LSB %.2e)", worst, i16Lsb)
	}
}

func TestSmplHarmcomb_HpPostfilterMatchesShippedWasm(t *testing.T) {
	records := loadPacked(t, "wasm_hp_postfilter.cbor.zst").Arr()
	if len(records) != 330 {
		t.Fatalf("assertion `left == right` failed: %d != 330", len(records))
	}
	for i, r := range records {
		s := r.Get("state_in").F32s()
		state := smplHarmcombSeedState(
			s[0],
			s[1],
			[4]float32(s[2:6]),
			s[6],
			[frameLen]float32(s[7:327]),
			[3]float32(s[327:330]),
			[3]float32(s[330:333]),
		)
		input := r.Get("input").F32s()
		out := make([]float32, frameLen)
		smplHpPostfilter(&state, input, frameLen, float32(r.Get("lag").F64()), out)
		nextState := []float32{state.stateLoEmph1, state.stateLoEmph2}
		nextState = append(nextState, state.stateHp[:]...)
		nextState = append(nextState, state.lagOld)
		nextState = append(nextState, state.xOld[:]...)
		nextState = append(nextState, state.coefMa[:]...)
		nextState = append(nextState, state.coefAr[:]...)
		want := r.Get("state_out").F32s()
		for k := 0; k < len(nextState) && k < len(want); k++ {
			if !(absF32(nextState[k]-want[k]) < 1.0/32768.0) {
				t.Fatalf("frame %d: HP state %v vs %v", i, nextState[k], want[k])
			}
		}
		wantOut := r.Get("output").F32s()
		for k := 0; k < len(out) && k < len(wantOut); k++ {
			if !(absF32(out[k]-wantOut[k]) < 1.0/32768.0) {
				t.Fatalf("frame %d: HP output %v vs %v", i, out[k], wantOut[k])
			}
		}
	}
}
