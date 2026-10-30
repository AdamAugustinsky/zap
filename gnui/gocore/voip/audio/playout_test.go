package audio

import (
	"math"
	"slices"
	"testing"
)

func ramp(from, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(from + i)
	}
	return out
}

func TestPlayoutPrimesAndPlaysInOrder(t *testing.T) {
	p := NewPlayout(100, 30)
	dst := make([]int16, 10)
	p.Write(ramp(1, 20))
	p.Read(dst)
	if slices.ContainsFunc(dst, func(v int16) bool { return v != 0 }) {
		t.Fatal("played before the target was in")
	}
	p.Write(ramp(21, 20))
	for want := 1; want <= 31; want += 10 {
		p.Read(dst)
		if !slices.Equal(dst, ramp(want, 10)) {
			t.Fatalf("read %v, want from %d", dst, want)
		}
	}
	if s := p.Stats(); s.Underruns != 0 || s.Buffered != 0 || s.Played != 50 {
		t.Fatalf("stats %+v", s)
	}
	// Dry: silence, an underrun, and priming again.
	p.Read(dst)
	if s := p.Stats(); s.Underruns != 1 || dst[0] != 0 {
		t.Fatalf("a dry read: %v %+v", dst, s)
	}
	p.Write(ramp(100, 10))
	p.Read(dst)
	if dst[0] != 0 {
		t.Fatal("played again before the target was back in")
	}
}

func TestPlayoutDropsOldest(t *testing.T) {
	p := NewPlayout(50, 0)
	if d := p.Write(ramp(0, 40)); d != 0 {
		t.Fatalf("dropped %d", d)
	}
	if d := p.Write(ramp(40, 30)); d != 20 {
		t.Fatalf("dropped %d, want 20", d)
	}
	dst := make([]int16, 50)
	p.Read(dst)
	if !slices.Equal(dst, ramp(20, 50)) {
		t.Fatalf("after the overflow the buffer plays %v", dst[:5])
	}
	if d := p.Write(ramp(0, 120)); d != 70 {
		t.Fatalf("a write over the capacity dropped %d", d)
	}
	p.Read(dst)
	if !slices.Equal(dst, ramp(70, 50)) {
		t.Fatal("a write over the capacity keeps its newest")
	}
	if s := p.Stats(); s.Dropped != 90 {
		t.Fatalf("dropped %d", s.Dropped)
	}
}

// The ring wraps without reordering anything.
func TestPlayoutWraps(t *testing.T) {
	p := NewPlayout(7, 0)
	dst := make([]int16, 3)
	next, want := 0, 0
	for range 50 {
		p.Write(ramp(next, 3))
		next += 3
		p.Read(dst)
		if !slices.Equal(dst, ramp(want, 3)) {
			t.Fatalf("read %v, want from %d", dst, want)
		}
		want += 3
	}
}

func TestCaptureMakesWholeFrames(t *testing.T) {
	c := newCapture(Format{48000, 2})
	var frames [][]int16
	in := tone(48000, 440, 48000)
	stereo := make([]float32, 2*len(in))
	for i, v := range in {
		stereo[2*i], stereo[2*i+1] = v, v
	}
	for at := 0; at < len(stereo); at += 2 * 441 { // odd sized reads
		end := min(at+2*441, len(stereo))
		c.push(stereo[at:end], func(f []int16) { frames = append(frames, f) })
	}
	if len(frames) != 16 { // 16000 samples, less the lookahead
		t.Fatalf("%d frames from one second", len(frames))
	}
	for _, f := range frames {
		if len(f) != FrameSamples {
			t.Fatalf("a frame of %d", len(f))
		}
	}
	peak := 0
	for _, v := range frames[5] {
		peak = max(peak, int(v))
	}
	if peak < 16000 || peak > 16500 { // 0.5 of full scale
		t.Fatalf("peak %d", peak)
	}
}

func TestRenderFillsEveryChannel(t *testing.T) {
	p := NewPlayout(DefaultPlayoutCapacity, 0)
	r := newRender(Format{48000, 2}, p)
	src := tone(16000, 1000, 1600)
	pcm := make([]int16, len(src))
	for i, v := range src {
		pcm[i] = toInt16(v)
	}
	p.Write(pcm)
	out := make([]float32, 2*480)
	var left []float32
	for range 9 {
		r.fill(out)
		for i := 0; i < len(out); i += 2 {
			if out[i] != out[i+1] {
				t.Fatal("the channels differ")
			}
			left = append(left, out[i])
		}
	}
	if q := snrAt(left, 48000, 1000, 100); q < 60 {
		t.Fatalf("the speaker got a %.1f dB tone", q)
	}
}

func snrAt(got []float32, rate int, hz float64, from int) float64 {
	var sig, noise float64
	for i := from; i < len(got)-100; i++ {
		want := 0.5 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))
		d := float64(got[i]) - want
		sig += want * want
		noise += d * d
	}
	return 10 * math.Log10(sig/noise)
}
