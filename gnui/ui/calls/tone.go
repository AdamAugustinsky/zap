package calls

import (
	"encoding/binary"
	"log/slog"
	"math"

	"gonativeui/media"

	"zapfast/abi"
	"zapfast/logic"
	"zapfast/state"
)

// The ringback of a call we place (zf:src/call_tones.rs): the ETSI tone,
// 425 Hz, one second on and four off, heard while the other phone rings.
// The core plays no tones, so it is synthesised here and played with
// media.Player, as package sys does for the ringtone of a call to us. As
// upstream's sync_call_tone has it, it plays only while an outgoing call's
// phase is Ringing and never in the demo; unlike upstream, which plays it
// whatever the settings say, it follows the notification sound, as the
// ringtone of a call to us does (Adam's decision, 2026-10-21): with the
// account's notifications off, or the sound that notifications make (the
// chat's own, else the message sound) set to none, the call is placed
// silently.

const (
	ringbackRate   = 48_000
	ringbackHz     = 425.0
	ringbackOn     = 1.0 // seconds of tone in a cycle
	ringbackCycle  = 5.0 // seconds, rest included
	ringbackLevel  = 0.18
	ringbackEdge   = 0.02 // the raised-cosine fade at each end of the tone
	ringbackCycles = 12   // one clip: a minute, longer than a call rings
)

// ringbackSamples is one cycle of the ringback: mono, 48 kHz, samples in -1…1.
func ringbackSamples() []float32 {
	total := int(ringbackCycle * ringbackRate)
	out := make([]float32, total)
	for i := 0; i < int(ringbackOn*ringbackRate); i++ {
		t := float64(i) / ringbackRate
		out[i] = float32(ringbackLevel * ringbackFade(t, ringbackOn) * math.Sin(2*math.Pi*ringbackHz*t))
	}
	return out
}

// ringbackFade is a raised-cosine fade over ringbackEdge at both ends of a
// span of length seconds.
func ringbackFade(t, length float64) float64 {
	ramp := func(x float64) float64 {
		return 0.5 - 0.5*math.Cos(math.Pi*math.Min(math.Max(x/ringbackEdge, 0), 1))
	}
	return ramp(t) * ramp(length-t)
}

// ringbackPCM is the clip as 16-bit little-endian PCM: the cycle repeated.
func ringbackPCM() []byte {
	cycle := ringbackSamples()
	one := make([]byte, 0, 2*len(cycle))
	for _, v := range cycle {
		one = binary.LittleEndian.AppendUint16(one, uint16(int16(math.Round(float64(v)*32767))))
	}
	out := make([]byte, 0, ringbackCycles*len(one))
	for range ringbackCycles {
		out = append(out, one...)
	}
	return out
}

// tonePort is what plays the ringback: a media.Player, or a fake in tests.
type tonePort interface {
	// Start plays the ringback from its beginning; Stop ends it.
	Start() error
	Stop()
}

// osRingback plays the clip on the default output.
type osRingback struct{ p *media.Player }

func (t *osRingback) Start() error {
	if t.p != nil {
		return nil
	}
	p, err := media.NewPlayer(ringbackPCM(), media.Format{SampleRate: ringbackRate, Channels: 1, Sample: media.Int16})
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

func (t *osRingback) Stop() {
	if t.p != nil {
		t.p.Close()
		t.p = nil
	}
}

// soundsOn is whether the call's notification would make a sound: the
// account's notifications are on and the sound they make is not none. It is
// package sys's rule for the ringtone of a call to us (notifyCall).
func (h *hub) soundsOn() bool {
	a := h.account()
	if a == nil || !a.Settings.Notifications {
		return false
	}
	var own *abi.NotificationSound
	if u := &h.call.u; !(u.HasGroup && u.Group.Link) {
		if ref := a.Model.Chats.Find(u.Chat); ref != 0 {
			var sc abi.Chat
			if ch := a.Model.Chats.At(ref); ch.Decode(&sc) == nil && sc.HasNotificationSound {
				own = &sc.NotificationSound
			}
		}
	}
	return logic.SoundFor(&h.s.Settings, own, false, false).Sound != state.SoundNone
}

// syncTone plays the tone the call's phase calls for and stops the one it no
// longer does (zf:src/app/calls.rs sync_call_tone): the ringback only while
// the call we placed rings at the other end. The hub never makes the sound
// in the demo.
func (h *hub) syncTone() {
	c := &h.call
	want := c.on && c.u.Phase == abi.CallPhaseRinging && c.u.Direction == abi.CallDirectionOutgoing && !h.s.Demo && h.soundsOn()
	if want == h.toneOn {
		return
	}
	if h.tone == nil {
		if !want {
			return
		}
		h.tone = &osRingback{}
	}
	if !want {
		h.tone.Stop()
		h.toneOn = false
		return
	}
	if err := h.tone.Start(); err != nil {
		slog.Warn("the ringback is silent", "err", err)
		return
	}
	h.toneOn = true
}
