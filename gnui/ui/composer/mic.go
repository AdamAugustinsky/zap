package composer

import (
	"encoding/binary"
	"math"
	"sync/atomic"
	"time"

	"gonativeui/app/perm"
	"gonativeui/media"
)

// mic is the microphone the composer records from: the system's (app/perm
// and media.Recorder), the demo's synthetic tone, or a test's fake. Its
// recorder methods are media.Recorder's; it is used on the main thread.
type mic interface {
	// Access is the user's answer about the microphone so far.
	Access() (perm.State, error)
	// Ask asks the user for it; the answer comes as an app event with
	// the ticket (perm.ResultOf).
	Ask() (perm.Ticket, error)
	// Settings opens System Settings where the user grants it.
	Settings() error

	Start() error
	Stop() error
	Cancel()
	State() media.RecorderState
	// ID is the recorder's, which its Recorded event carries.
	ID() uint64
	Elapsed() time.Duration
	// Level is the input's amplitude now, 0 to 1.
	Level() float32
	Take() ([]byte, media.Format, error)
}

// deviceMic is the system's microphone.
type deviceMic struct {
	rec *media.Recorder
}

func (d *deviceMic) Access() (perm.State, error) { return perm.Status(perm.Microphone) }
func (d *deviceMic) Ask() (perm.Ticket, error)   { return perm.Request(perm.Microphone) }

func (d *deviceMic) Settings() error { return perm.OpenSettings(perm.Microphone) }

func (d *deviceMic) Start() error {
	if d.rec == nil {
		d.rec = media.NewRecorder(media.RecorderOptions{})
	}
	return d.rec.Start()
}

func (d *deviceMic) Stop() error {
	if d.rec == nil {
		return media.ErrState
	}
	return d.rec.Stop()
}

func (d *deviceMic) Cancel() {
	if d.rec != nil {
		d.rec.Cancel()
	}
}

func (d *deviceMic) State() media.RecorderState {
	if d.rec == nil {
		return media.Idle
	}
	return d.rec.State()
}

func (d *deviceMic) ID() uint64 {
	if d.rec == nil {
		return 0
	}
	return d.rec.ID()
}

func (d *deviceMic) Elapsed() time.Duration {
	if d.rec == nil {
		return 0
	}
	return d.rec.Elapsed()
}

func (d *deviceMic) Level() float32 {
	if d.rec == nil {
		return 0
	}
	return d.rec.Level().Amplitude()
}

func (d *deviceMic) Take() ([]byte, media.Format, error) {
	if d.rec == nil {
		return nil, media.Format{}, media.ErrState
	}
	return d.rec.Take()
}

// demoMic is the demo's recorder: it never opens the microphone, and what
// it records is a speech-like tone as long as the recording
// (zf:src/audio.rs rehearse).
type demoMic struct {
	now     func() time.Time
	wake    func()
	state   media.RecorderState
	started time.Time
	length  time.Duration
	pcm     []byte
	done    atomic.Bool
}

// demoRate is the demo tone's sample rate.
const demoRate = 48000

func (d *demoMic) Access() (perm.State, error) { return perm.Authorized, nil }
func (d *demoMic) Ask() (perm.Ticket, error)   { return 0, nil }
func (d *demoMic) ID() uint64                  { return 0 }
func (d *demoMic) Settings() error             { return nil }

func (d *demoMic) Start() error {
	if d.state != media.Idle {
		return media.ErrState
	}
	d.state, d.started = media.Recording, d.now()
	return nil
}

func (d *demoMic) Stop() error {
	if d.state != media.Recording {
		return media.ErrState
	}
	d.length = d.Elapsed()
	d.state = media.Finishing
	d.done.Store(false)
	n := int(d.length.Seconds() * demoRate)
	go func() {
		pcm := make([]byte, 2*n)
		for i := range n {
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(demoTone(float64(i)/demoRate)*32767)))
		}
		d.pcm = pcm
		d.done.Store(true)
		d.wake()
	}()
	return nil
}

func (d *demoMic) Cancel() { d.state, d.pcm = media.Idle, nil }

func (d *demoMic) State() media.RecorderState {
	if d.state == media.Finishing && d.done.Load() {
		d.state = media.Ready
	}
	return d.state
}

func (d *demoMic) Elapsed() time.Duration {
	if d.state != media.Recording {
		return 0
	}
	return max(d.now().Sub(d.started), 0)
}

// Level is the tone's envelope now, as upstream's levels (a peak × 0.7).
func (d *demoMic) Level() float32 {
	t := d.Elapsed().Seconds()
	return float32(0.35 * 0.7 * math.Abs(math.Sin(t*2.3)*math.Cos(t*0.9)))
}

func (d *demoMic) Take() ([]byte, media.Format, error) {
	if d.State() != media.Ready {
		return nil, media.Format{}, media.ErrState
	}
	pcm := d.pcm
	d.state, d.pcm = media.Idle, nil
	return pcm, media.Format{SampleRate: demoRate, Channels: 1, Sample: media.Int16}, nil
}

// demoTone is the demo's sample at t seconds: 180 Hz under a slow
// envelope.
func demoTone(t float64) float64 {
	return math.Sin(t*180*2*math.Pi) * 0.35 * math.Abs(math.Sin(t*2.3)*math.Cos(t*0.9))
}
