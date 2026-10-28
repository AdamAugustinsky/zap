package audio

import (
	"math"
	"math/rand/v2"
	"testing"
)

func tone(rate int, hz float64, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(0.5 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return out
}

// snr compares got with the tone it should be at rate, skipping the
// first and last skip samples (the filter's start and end).
func snr(got []float32, rate int, hz float64, skip int) float64 {
	var sig, noise float64
	for i := skip; i < len(got)-skip; i++ {
		want := 0.5 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))
		d := float64(got[i]) - want
		sig += want * want
		noise += d * d
	}
	return 10 * math.Log10(sig/noise)
}

func TestResamplerAccuracy(t *testing.T) {
	for _, c := range []struct {
		from, to int
		hz       float64
	}{
		{48000, 16000, 1000},
		{48000, 16000, 3400},
		{44100, 16000, 1000},
		{44100, 16000, 3000},
		{16000, 48000, 1000},
		{16000, 44100, 2500},
		{24000, 16000, 440},
		{8000, 16000, 300},
	} {
		in := tone(c.from, c.hz, c.from) // one second
		out := NewResampler(c.from, c.to).Process(nil, in)
		if q := snr(out, c.to, c.hz, 200); q < 60 {
			t.Errorf("%d → %d at %.0f Hz: SNR %.1f dB, want at least 60", c.from, c.to, c.hz, q)
		}
	}
}

// Going down to 16 kHz, what is above its 8 kHz is filtered out, not
// folded back into the voice band.
func TestResamplerRejectsAliases(t *testing.T) {
	for _, hz := range []float64{9000, 12000, 20000} {
		out := NewResampler(48000, 16000).Process(nil, tone(48000, hz, 48000))
		var e float64
		for _, v := range out[200 : len(out)-200] {
			e += float64(v) * float64(v)
		}
		rms := math.Sqrt(e / float64(len(out)-400))
		if db := 20 * math.Log10(rms/(0.5/math.Sqrt2)); db > -50 {
			t.Errorf("%.0f Hz came through at %.1f dB, want under -50", hz, db)
		}
	}
}

// Chunks of any size make the same output as one call, and a long stream
// keeps the exact rate.
func TestResamplerStreams(t *testing.T) {
	in := tone(44100, 700, 44100*3)
	whole := NewResampler(44100, 16000).Process(nil, in)
	r := NewResampler(44100, 16000)
	var parts []float32
	rnd := rand.New(rand.NewPCG(1, 2))
	for at := 0; at < len(in); {
		n := min(1+rnd.IntN(1500), len(in)-at)
		parts = r.Process(parts, in[at:at+n])
		at += n
	}
	if len(parts) != len(whole) {
		t.Fatalf("chunked %d samples, whole %d", len(parts), len(whole))
	}
	for i := range whole {
		if parts[i] != whole[i] {
			t.Fatalf("sample %d differs: %v, %v", i, parts[i], whole[i])
		}
	}
	// Three seconds in: 48000 out, less the kernel's lookahead.
	if want := 48000; len(whole) > want || len(whole) < want-30 {
		t.Fatalf("%d samples from 3 s, want about %d", len(whole), want)
	}
	// Two minutes at 44.1 kHz in ten-millisecond blocks do not drift.
	long := NewResampler(44100, 16000)
	block := make([]float32, 441)
	var n int
	var out []float32
	for range 12000 {
		out = long.Process(out[:0], block)
		n += len(out)
	}
	if want := 120 * 16000; n > want || n < want-30 {
		t.Fatalf("two minutes gave %d samples, want %d", n, want)
	}
}

func TestResamplerSameRate(t *testing.T) {
	in := tone(16000, 1000, 100)
	out := NewResampler(16000, 16000).Process(nil, in)
	if len(out) != len(in) || out[50] != in[50] {
		t.Fatal("the same rate is not passed through")
	}
}

func TestResamplerKeepsLevel(t *testing.T) {
	in := make([]float32, 4800)
	for i := range in {
		in[i] = 0.25
	}
	out := NewResampler(48000, 16000).Process(nil, in)
	for _, v := range out[100 : len(out)-100] {
		if math.Abs(float64(v)-0.25) > 1e-4 {
			t.Fatalf("a constant came out as %v", v)
		}
	}
}
