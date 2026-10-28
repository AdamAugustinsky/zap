package calls

import (
	"context"
	"errors"
	"net/netip"
	"syscall"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"

	"zapfast/gocore/voip/engine"
	"zapfast/gocore/voip/relay"
	"zapfast/gocore/voip/video"
)

// The Go engine behind Media, offline: the relay's dial is refused before
// any packet leaves, and no device is opened.

type quietAudio struct{ mic chan []int16 }

func (a quietAudio) Microphone() <-chan []int16 { return a.mic }
func (quietAudio) Play([]int16)                 {}
func (quietAudio) SetMicrophone(string)         {}
func (quietAudio) SetSpeaker(string)            {}
func (quietAudio) Close()                       {}

func offlineMedia(dialed chan<- netip.AddrPort) Media {
	frames := video.NewFrames()
	return NewMedia(engine.Devices{
		Dial: func(_ context.Context, addr netip.AddrPort) (relay.Transport, error) {
			dialed <- addr
			return nil, syscall.ECONNREFUSED
		},
		OpenAudio: func(string, string) (engine.Audio, error) { return quietAudio{make(chan []int16)}, nil },
		NewOpus:   NewOpus,
		Frames:    frames,
	}, nil, frames)
}

func nextKinds(t *testing.T, m Media, generation uint64) []string {
	t.Helper()
	var kinds []string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && (len(kinds) == 0 || kinds[len(kinds)-1] != MediaEnded) {
		ev, ok, err := m.Next(200 * time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			if ev.Generation != generation {
				t.Fatalf("event %+v", ev)
			}
			kinds = append(kinds, ev.Kind)
		}
	}
	return kinds
}

func TestTheEngineMediaRunsACallAndReportsItsEnd(t *testing.T) {
	dialed := make(chan netip.AddrPort, 4)
	m := offlineMedia(dialed)
	defer m.Free()
	r := relayNode()
	p := OpenParams{CallID: "00112233445566778899aabbccddeeff", Generation: 9, Outgoing: true,
		SelfLID: ownLID.String(), PeerLID: peerLID.String(), CallKey: callKey, Relay: &r, MLow: true}
	if err := m.Open(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Command(8, muteCommand(true)); err == nil {
		t.Fatal("another generation's command was taken")
	}
	if err := m.Command(9, MediaCommand{"op": "bogus"}); err == nil {
		t.Fatal("an unknown command was taken")
	}
	if _, ok := m.Frame(1); ok {
		t.Fatal("a frame before any video")
	}
	kinds := nextKinds(t, m, 9)
	if len(kinds) != 2 || kinds[0] != MediaFailed || kinds[1] != MediaEnded {
		t.Fatalf("events %q", kinds)
	}
	if addr := <-dialed; !addr.IsValid() {
		t.Fatalf("dialed %v", addr)
	}
	// The call is over: another may open, and a second open while it is up
	// is refused.
	p.Generation = 10
	if err := m.Open(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(10); err != nil {
		t.Fatal(err)
	}
	if kinds := nextKinds(t, m, 10); kinds[len(kinds)-1] != MediaEnded {
		t.Fatalf("events %q", kinds)
	}
}

func TestTheEngineMediaRefusesACallWithoutARelay(t *testing.T) {
	m := offlineMedia(make(chan netip.AddrPort, 1))
	defer m.Free()
	err := m.Open(OpenParams{CallID: "x", Generation: 1, CallKey: callKey, Relay: &waBinary.Node{Tag: "offer"}})
	if !errors.Is(err, ErrNoRelay) {
		t.Fatalf("no relay: %v", err)
	}
}

func TestMediaCommandsReachTheEngineAsItsOps(t *testing.T) {
	for _, tc := range []struct {
		cmd  MediaCommand
		want engine.Command
	}{
		{muteCommand(true), engine.Command{Op: engine.OpMute, On: true}},
		{rekeyCommand("1:2@lid", "opus"), engine.Command{Op: engine.OpRekey, LID: "1:2@lid", Codec: "opus"}},
		{videoStartCommand("cam", true), engine.Command{Op: engine.OpVideoStart, Device: "cam", AwaitAccept: true}},
		{cameraOffCommand(), engine.Command{Op: engine.OpCameraOff}},
		{orientationCommand(3), engine.Command{Op: engine.OpOrientation, Value: 3}},
		{speakerCommand(nil), engine.Command{Op: engine.OpSpeaker}},
	} {
		got, err := engineCommand(tc.cmd)
		if err != nil || got != tc.want {
			t.Errorf("%v: %+v %v", tc.cmd, got, err)
		}
	}
}
