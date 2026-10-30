//go:build darwin

package audio

import (
	"errors"
	"os"
	"testing"
	"time"
)

// Listing devices opens none of them: it is safe on any machine, and only
// counts are logged.
func TestCoreAudioDevices(t *testing.T) {
	mics, err := Microphones()
	if err != nil {
		t.Fatalf("Microphones: %v", err)
	}
	spks, err := Speakers()
	if err != nil {
		t.Fatalf("Speakers: %v", err)
	}
	for _, d := range append(mics, spks...) {
		if d.ID == "" || d.Label == "" || hidden(d.ID) {
			t.Fatalf("a device without an id or a name, or the voice unit's aggregate: %q", d.ID)
		}
	}
	t.Logf("%d microphones, %d speakers", len(mics), len(spks))
	b, _ := system()
	for _, input := range []bool{true, false} {
		id, err := b.defaultDevice(input)
		if err != nil && !errors.Is(err, ErrNoDevice) {
			t.Fatalf("defaultDevice(%v): %v", input, err)
		}
		_ = id
	}
	stop, err := WatchDevices(func() {})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
}

// A device that is not there, and a framework call that fails, are typed,
// and nothing is opened on the way.
func TestCoreAudioErrors(t *testing.T) {
	b, err := system()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.openInput("no-such-device-uid", nil); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("an unknown microphone: %v, want ErrNoDevice", err)
	}
	if _, err := b.openOutput("no-such-device-uid", nil); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("an unknown speaker: %v, want ErrNoDevice", err)
	}
	if _, err := b.openDuplex("no-such-device-uid", "", nil, nil); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("an unknown device for voice processing: %v", err)
	}
	if _, err := newUnit(fourCC("zzzz")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a unit the system lacks: %v, want ErrUnavailable", err)
	}
	// An AUHAL instance does no I/O until started: point it at a device
	// that does not exist.
	unit, err := newUnit(unitHAL)
	if err != nil {
		t.Fatal(err)
	}
	defer ca.dispose(unit)
	u := &unitSetup{unit: unit}
	u.u32("CurrentDevice", propCurrentDevice, auGlobal, 0, 0x7fff_fff0)
	var se *StatusError
	if !errors.As(u.err, &se) || se.Op != "CurrentDevice" || se.Status == 0 {
		t.Fatalf("a device id that is not one: %v, want a *StatusError", u.err)
	}
}

func live(t *testing.T) {
	t.Helper()
	if os.Getenv("ZAP_LIVE_AV") != "1" {
		t.Skip("records from the real microphone and plays on the speaker: set ZAP_LIVE_AV=1")
	}
}

// ZAP_LIVE_AV=1: the default microphone delivers 16 kHz frames and the
// default speaker takes samples, plainly and with voice processing. The
// speaker plays silence.
func TestCoreAudioLive(t *testing.T) {
	live(t)
	for _, echo := range []bool{false, true} {
		a, err := Open(Config{EchoCancel: echo})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.After(3 * time.Second)
		frames := 0
		for frames < 10 {
			select {
			case f := <-a.Microphone():
				if len(f) != FrameSamples {
					t.Fatalf("a frame of %d", len(f))
				}
				frames++
			case <-deadline:
				t.Fatalf("echo %v: %d frames in 3 s", echo, frames)
			}
		}
		st := a.Stats()
		if st.Playout.Played == 0 {
			t.Fatalf("echo %v: the speaker took nothing: %+v", echo, st)
		}
		t.Logf("echo cancel asked %v, on %v: %+v", echo, st.EchoCancel, st)
		a.Close()
	}
}

// ZAP_LIVE_AV=1: each speaker opens, plays silence, and switches to the
// next behind the same buffer. No microphone is opened.
func TestCoreAudioLiveSpeaker(t *testing.T) {
	live(t)
	spks, err := Speakers()
	if err != nil || len(spks) == 0 {
		t.Skipf("no speaker: %v", err)
	}
	b, _ := system()
	p := NewPlayout(DefaultPlayoutCapacity, DefaultPlayoutTarget)
	a := start(b, Config{Speaker: spks[0].ID, Playout: p}, defaultTiming)
	defer a.Close()
	for _, d := range append(spks[1:], spks[0]) {
		before := p.Stats().Played
		deadline := time.Now().Add(2 * time.Second)
		for p.Stats().Played-before < Rate/4 {
			if time.Now().After(deadline) {
				t.Fatalf("the speaker took %d samples in 2 s: %+v", p.Stats().Played-before, a.Stats())
			}
			a.Play(make([]int16, FrameSamples))
			time.Sleep(20 * time.Millisecond)
		}
		a.SetSpeaker(d.ID)
	}
	t.Logf("%+v", a.Stats())
}
