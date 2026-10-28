package sys

import (
	"encoding/binary"
	"math"
	"sync"

	"gonativeui/app"
	"gonativeui/media"
)

// The ringtone of a call to us (zf:src/call_tones.rs): two soft bell notes,
// E5 then B4, played twice and followed by a rest, every three seconds,
// synthesised rather than bundled. It plays while the call rings and its
// notification may make a sound (zf:src/app/calls.rs sync_call_tone). The
// core's tones were meant to cover this (the plan, D33), but it makes none.

const (
	toneRate   = 48_000
	toneCycle  = 3.0 // seconds, rest included
	noteLength = 0.6
	toneLevel  = 0.28
	toneEdge   = 0.02
)

// ringtoneNotes are when each note starts within the cycle, and its pitch.
var ringtoneNotes = [4]struct{ start, hz float64 }{{0.0, 659.25}, {0.4, 493.88}, {0.9, 659.25}, {1.3, 493.88}}

// ringtoneCycle is one cycle of the ringtone: mono, 48 kHz, samples in -1…1.
func ringtoneCycle() []float32 {
	total := int(toneCycle * toneRate)
	out := make([]float32, total)
	for _, n := range ringtoneNotes {
		first := int(n.start * toneRate)
		length := int(noteLength * toneRate)
		for i := 0; i < length && first+i < total; i++ {
			t := float64(i) / toneRate
			// A struck bell: a quick rise, a decay, and a fade at the end of
			// its slot so the next note starts from silence; two quiet
			// overtones give it body.
			env := math.Min(t/0.008, 1) * math.Exp(-t/0.18) * edge(t, noteLength)
			ph := 2 * math.Pi * n.hz * t
			bell := math.Sin(ph) + 0.25*math.Sin(2*ph) + 0.08*math.Sin(3*ph)
			out[first+i] += float32(toneLevel * env * bell / 1.33)
		}
	}
	return out
}

// edge is a raised-cosine fade over toneEdge at both ends of a span of length
// seconds.
func edge(t, length float64) float64 {
	ramp := func(x float64) float64 {
		return 0.5 - 0.5*math.Cos(math.Pi*math.Min(math.Max(x/toneEdge, 0), 1))
	}
	return ramp(t) * ramp(length-t)
}

// ringtonePCM is the cycle as 16-bit little-endian PCM, made once.
var ringtonePCM = sync.OnceValue(func() []byte {
	s := ringtoneCycle()
	b := make([]byte, 0, 2*len(s))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint16(b, uint16(int16(math.Round(float64(v)*32767))))
	}
	return b
})

// tonePort is the ringtone's output.
type tonePort interface {
	// Start plays the ringtone from its beginning; Stop ends it.
	Start() error
	Stop()
	// Ended is whether e is the end of a cycle: it plays the next while the
	// ringtone is wanted.
	Ended(e *app.Event) bool
}

// osTone plays the cycle again each time it ends.
type osTone struct{ p *media.Player }

func (t *osTone) Start() error {
	if t.p != nil {
		return nil
	}
	p, err := media.NewPlayer(ringtonePCM(), media.Format{SampleRate: toneRate, Channels: 1, Sample: media.Int16})
	if err != nil {
		return err
	}
	if err := p.Play(); err != nil {
		p.Close()
		return err
	}
	t.p = p
	return nil
}

func (t *osTone) Stop() {
	if t.p != nil {
		t.p.Close()
		t.p = nil
	}
}

func (t *osTone) Ended(e *app.Event) bool {
	if t.p == nil {
		return false
	}
	ev, ok := media.EventOf(e)
	if !ok || ev.ID != t.p.ID() {
		return false
	}
	if ev.Kind == media.Ended {
		_ = t.p.Play()
	}
	return true
}
