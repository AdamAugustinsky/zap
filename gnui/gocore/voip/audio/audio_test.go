package audio

import (
	"math"
	"slices"
	"testing"
	"time"
)

// crossings is how often a frame crosses zero: twice its frequency over
// its 60 ms.
func crossings(f []int16) int {
	n := 0
	for i := 1; i < len(f); i++ {
		if (f[i-1] < 0) != (f[i] < 0) {
			n++
		}
	}
	return n
}

// hz is a frame's frequency by its zero crossings.
func hz(f []int16) float64 { return float64(crossings(f)) / 2 / (float64(len(f)) / Rate) }

// waitFrame reads the microphone until a frame near want Hz comes.
func waitFrame(t *testing.T, mic <-chan []int16, want float64) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f, ok := <-mic:
			if !ok {
				t.Fatal("the microphone channel closed")
			}
			if len(f) != FrameSamples {
				t.Fatalf("a frame of %d samples", len(f))
			}
			if got := hz(f); got > want*0.9 && got < want*1.1 {
				return
			}
		case <-deadline:
			t.Fatalf("no %.0f Hz frame came", want)
		}
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// keepPlaying writes a 500 Hz tone to the speaker until stop closes.
func keepPlaying(a *Audio, stop <-chan struct{}) {
	pcm := make([]int16, FrameSamples)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*500*float64(i)/Rate))
	}
	go func() {
		tick := time.NewTicker(12 * time.Millisecond) // 60 ms of audio at five times real time
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				a.Play(pcm)
			}
		}
	}()
}

func loud(samples []float32) bool {
	return slices.ContainsFunc(samples, func(v float32) bool { return v > 0.1 || v < -0.1 })
}

func TestDeviceSwitchKeepsTheChannels(t *testing.T) {
	f := newFake()
	a := start(f, Config{Microphone: "a", Speaker: "s1"}, fastTiming)
	defer a.Close()
	mic, play := a.Microphone(), a.Playout()
	stop := make(chan struct{})
	defer close(stop)
	keepPlaying(a, stop)

	waitFrame(t, mic, 440) // a: 48 kHz stereo
	s1, s2 := f.dev(f.outs, "s1"), f.dev(f.outs, "s2")
	eventually(t, "s1 playing", func() bool { return loud(s1.heard()) })

	a.SetMicrophone("b")
	a.SetSpeaker("s2")
	waitFrame(t, mic, 1000) // b: 44.1 kHz mono, through the same channel
	eventually(t, "s2 playing", func() bool { return loud(s2.heard()) })

	if a.Microphone() != mic || a.Playout() != play {
		t.Fatal("a device switch replaced the engine's channel or buffer")
	}
	if m, s := a.Selected(); m != "b" || s != "s2" {
		t.Fatalf("Selected = %q, %q", m, s)
	}
	st := a.Stats()
	if st.MicOpens != 2 || st.SpeakerOpens != 2 || st.MicRestarts != 0 || st.SpeakerRestarts != 0 {
		t.Fatalf("stats %+v", st)
	}
	select {
	case s := <-a.FellBack():
		t.Fatalf("the %v fell back", s)
	default:
	}
	// The speaker's frames are at the device's own rate: 500 Hz at 16 kHz
	// on s2.
	heard := s2.heard()
	pcm := make([]int16, 0, FrameSamples)
	for _, v := range heard[len(heard)-FrameSamples:] {
		pcm = append(pcm, toInt16(v))
	}
	if got := hz(pcm); got < 450 || got > 550 {
		t.Fatalf("s2 heard %.0f Hz, want 500", got)
	}

	a.Close()
	if _, ok := <-drain(mic); ok {
		t.Fatal("the microphone channel is open after Close")
	}
	want := []string{"in:a", "out:s1", "in:b", "out:s2"}
	if got := f.opened(); !slices.Equal(got, want) {
		t.Fatalf("opened %v, want %v", got, want)
	}
}

// drain reads ch until it closes, and hands the end over.
func drain(ch <-chan []int16) <-chan []int16 {
	for range ch {
	}
	return ch
}

func TestAGoneDeviceFallsBackToTheDefault(t *testing.T) {
	f := newFake()
	a := start(f, Config{Microphone: "b"}, fastTiming)
	defer a.Close()
	waitFrame(t, a.Microphone(), 1000)
	f.remove("b")
	select {
	case s := <-a.FellBack():
		if s != Microphone {
			t.Fatalf("the %v fell back", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing fell back")
	}
	waitFrame(t, a.Microphone(), 440)
	if m, _ := a.Selected(); m != "" {
		t.Fatalf("the microphone is still %q", m)
	}
}

func TestADeviceThatWillNotOpenFallsBack(t *testing.T) {
	f := newFake()
	f.outs = append(f.outs, &fakeDev{id: "broken", f: Format{48000, 2}, failOpen: true})
	a := start(f, Config{Speaker: "broken"}, fastTiming)
	defer a.Close()
	select {
	case s := <-a.FellBack():
		if s != Speaker {
			t.Fatalf("the %v fell back", s)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing fell back")
	}
	if !slices.Contains(f.opened(), "out:s1") {
		t.Fatalf("opened %v, not the default", f.opened())
	}
	// Even the default refusing is retried, not given up.
	f.mu.Lock()
	f.ins[0].failOpen = true
	f.mu.Unlock()
	a.SetMicrophone("")
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	f.ins[0].failOpen = false
	f.mu.Unlock()
	waitFrame(t, a.Microphone(), 440)
}

func TestTheDefaultIsFollowed(t *testing.T) {
	f := newFake()
	a := start(f, Config{}, fastTiming)
	defer a.Close()
	waitFrame(t, a.Microphone(), 440)
	f.mu.Lock()
	f.defIn = "b"
	f.mu.Unlock()
	f.notify()
	waitFrame(t, a.Microphone(), 1000)
	select {
	case s := <-a.FellBack():
		t.Fatalf("following the default made the %v fall back", s)
	default:
	}
}

func TestAStalledSpeakerIsRestarted(t *testing.T) {
	f := newFake()
	a := start(f, Config{Speaker: "s1"}, fastTiming)
	defer a.Close()
	s1 := f.dev(f.outs, "s1")
	eventually(t, "s1 playing", func() bool { return len(s1.heard()) > 0 })
	s1.stall.Store(true)
	eventually(t, "a restart", func() bool { return a.Stats().SpeakerRestarts > 0 })
	// It had played, so it is not given up.
	if _, s := a.Selected(); s != "s1" {
		t.Fatalf("the speaker moved to %q", s)
	}
	s1.stall.Store(false)
	n := len(s1.heard())
	eventually(t, "s1 playing again", func() bool { return len(s1.heard()) > n })
}

func TestASpeakerThatNeverPlaysIsGivenUp(t *testing.T) {
	f := newFake()
	f.dev(f.outs, "s2").stall.Store(true)
	a := start(f, Config{Speaker: "s2"}, fastTiming)
	defer a.Close()
	select {
	case s := <-a.FellBack():
		if s != Speaker {
			t.Fatalf("the %v fell back", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing fell back")
	}
	if st := a.Stats(); st.SpeakerRestarts != fastTiming.unplayed {
		t.Fatalf("%d restarts before giving up, want %d", st.SpeakerRestarts, fastTiming.unplayed)
	}
}

func TestASilentMicrophoneFallsBackAtOnce(t *testing.T) {
	f := newFake()
	f.dev(f.ins, "b").stall.Store(true)
	a := start(f, Config{Microphone: "b"}, fastTiming)
	defer a.Close()
	waitFrame(t, a.Microphone(), 440)
	if st := a.Stats(); st.MicRestarts != 1 {
		t.Fatalf("%d restarts", st.MicRestarts)
	}
}

func TestEchoCancelOpensBothTogether(t *testing.T) {
	f := newFake()
	f.duplex = true
	a := start(f, Config{EchoCancel: true}, fastTiming)
	defer a.Close()
	stop := make(chan struct{})
	defer close(stop)
	keepPlaying(a, stop)
	waitFrame(t, a.Microphone(), 440)
	if !a.Stats().EchoCancel {
		t.Fatal("voice processing is not on")
	}
	a.SetMicrophone("b")
	waitFrame(t, a.Microphone(), 1000)
	s1 := f.dev(f.outs, "s1")
	n := len(s1.heard())
	eventually(t, "the speaker after the switch", func() bool { return len(s1.heard()) > n })
	want := []string{"duplex:a+s1", "duplex:b+s1"}
	if got := f.opened(); !slices.Equal(got, want) {
		t.Fatalf("opened %v, want %v", got, want)
	}
}

func TestEchoCancelWithoutVoiceProcessing(t *testing.T) {
	f := newFake()
	a := start(f, Config{EchoCancel: true}, fastTiming)
	defer a.Close()
	waitFrame(t, a.Microphone(), 440)
	if a.Stats().EchoCancel {
		t.Fatal("voice processing said on without it")
	}
	if got := f.opened(); !slices.Equal(got, []string{"in:a", "out:s1"}) {
		t.Fatalf("opened %v", got)
	}
}

// An engine that does not read loses the oldest frames, never blocks the
// device, and reads the newest when it comes back.
func TestASlowEngineLosesTheOldest(t *testing.T) {
	f := newFake()
	a := start(f, Config{}, fastTiming)
	defer a.Close()
	eventually(t, "frames dropped", func() bool { return a.Stats().MicDropped > 3 })
	if n := len(a.Microphone()); n != micQueue {
		t.Fatalf("%d frames queued", n)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	a := start(newFake(), Config{}, fastTiming)
	a.Close()
	a.Close()
	a.SetMicrophone("b") // after Close: nothing, and no hang
	a.DevicesChanged()
}
