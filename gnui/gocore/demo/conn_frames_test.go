package demo_test

import (
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// frameOn waits for a frame on stream and checks it is a whole I420
// picture of w × h in the core's memory.
func (s *session) frameOn(stream uint32, w, h int) core.Frame {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := s.c.Drain(s.h, 0); err != nil {
			s.t.Fatal(err)
		}
		if f, ok := s.c.Frame(stream); ok {
			cw, ch := (w+1)/2, (h+1)/2
			if f.W != w || f.H != h || f.Format != core.FrameI420 || f.Stride != [3]int{w, cw, cw} || len(f.Px) != w*h+2*cw*ch {
				s.t.Fatalf("stream %d: %d×%d format %d strides %v, %d bytes", stream, f.W, f.H, f.Format, f.Stride, len(f.Px))
			}
			if f.Px[0] == 0 || f.Px[len(f.Px)-1] == 0 {
				s.t.Fatalf("stream %d: the planes are not the test pattern", stream)
			}
			return f
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("no frame on stream %d within 10 s", stream)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func phase(p abi.CallPhase) func(*abi.Event) bool {
	return func(e *abi.Event) bool { return e.Tag == abi.EventTagCall && e.Call.Update.Phase == p }
}

func TestDemoVideoCallFrames(t *testing.T) {
	s := openDemo(t)
	if _, ok := s.c.Frame(core.StreamRemote); ok {
		t.Fatal("a frame before any call")
	}
	gen := s.c.CallGeneration()
	if gen == 0 || s.c.CallGeneration() <= gen {
		t.Fatalf("generations %d, then not larger", gen)
	}
	var cmd abi.Command
	cmd.Tag = abi.CommandTagStartCall
	cmd.StartCall = abi.CommandStartCall{Chat: []byte(ada), Video: true, Generation: gen, Cancel: 1}
	s.send(&cmd)
	s.until("ringing", phase(abi.CallPhaseRinging))
	s.until("the call up", phase(abi.CallPhaseActive))

	local := s.frameOn(core.StreamLocal, 320, 180)
	if _, ok := s.c.Frame(core.StreamLocal); ok {
		t.Fatal("a second frame of a held stream")
	}
	pts := local.PTS
	s.c.ReleaseFrame(core.StreamLocal)
	s.c.ReleaseFrame(core.StreamLocal) // nothing held: nothing happens
	remote := s.frameOn(core.StreamRemote, 640, 360)
	s.c.ReleaseFrame(core.StreamRemote)
	if _, ok := s.c.Frame(core.StreamMember); ok {
		t.Fatal("a member's frame on a one-to-one call")
	}

	// New frames keep coming, newer each time.
	time.Sleep(150 * time.Millisecond)
	if f := s.frameOn(core.StreamLocal, 320, 180); f.PTS <= pts {
		t.Errorf("pts %d after %d", f.PTS, pts)
	}
	s.c.ReleaseFrame(core.StreamLocal)
	_ = remote

	// Taking and giving back a frame allocates nothing.
	if n := testing.AllocsPerRun(100, func() {
		if _, ok := s.c.Frame(core.StreamRemote); ok {
			s.c.ReleaseFrame(core.StreamRemote)
		}
	}); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}

	cmd = abi.Command{Tag: abi.CommandTagHangupCall}
	s.send(&cmd)
	s.until("the call over", phase(abi.CallPhaseEnded))
	if _, ok := s.c.Frame(core.StreamRemote); ok {
		t.Error("a frame after the call ended")
	}
}

func TestDemoGroupCallRingsAndStreamsMembers(t *testing.T) {
	s := openDemo(t)
	var cmd abi.Command
	core.DemoCall(&cmd, "120363012345678901@g.us", "441632960123@s.whatsapp.net", true, 0)
	s.send(&cmd)
	var gen uint64
	var members int
	s.until("the call ringing", func(e *abi.Event) bool {
		if e.Tag != abi.EventTagCall || e.Call.Update.Phase != abi.CallPhaseIncoming {
			return false
		}
		gen, members = e.Call.Update.Generation, len(e.Call.Update.Group.Participants)
		return e.Call.Update.HasGroup && e.Call.Update.Direction == abi.CallDirectionIncoming
	})
	if members != 3 || gen == 0 {
		t.Fatalf("%d members, generation %d", members, gen)
	}
	if next := s.c.CallGeneration(); next <= gen {
		t.Errorf("the UI's next generation %d is not after the core's %d", next, gen)
	}
	cmd = abi.Command{Tag: abi.CommandTagAnswerCall}
	s.send(&cmd)
	s.until("the call up", phase(abi.CallPhaseActive))
	s.frameOn(core.StreamMember, 320, 180)
	s.c.ReleaseFrame(core.StreamMember)
	s.frameOn(core.StreamMember+1, 320, 180)
	s.c.ReleaseFrame(core.StreamMember + 1)
	if _, ok := s.c.Frame(core.StreamRemote); ok {
		t.Error("a one-to-one picture on a group call")
	}
	cmd = abi.Command{Tag: abi.CommandTagHangupCall}
	s.send(&cmd)
	s.until("the call over", phase(abi.CallPhaseEnded))
}

func TestDemoCallLogDevicesAndAvatars(t *testing.T) {
	s := openDemo(t)
	var cmd abi.Command
	cmd.Tag = abi.CommandTagLoadCallLog
	s.send(&cmd)
	s.until("the call log", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagCallLog && e.CallLog.Last && len(e.CallLog.Messages) == 5 &&
			e.CallLog.Messages[0].Content.Tag == abi.ContentTagCall
	})
	cmd = abi.Command{Tag: abi.CommandTagRefreshCallDevices}
	s.send(&cmd)
	s.until("the devices", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagCallDevices && len(e.CallDevices.Devices.Cameras) == 2
	})
	cmd = abi.Command{Tag: abi.CommandTagFetchAvatar}
	cmd.FetchAvatar = abi.CommandFetchAvatar{ID: []byte(ada)}
	s.send(&cmd)
	s.until("Ada's picture", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagAvatar && e.Avatar.HasPath && len(e.Avatar.Path) > 0
	})
	cmd.FetchAvatar = abi.CommandFetchAvatar{ID: []byte("15550009999@s.whatsapp.net")}
	s.send(&cmd)
	s.until("no picture for a stranger", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagAvatar && !e.Avatar.HasPath
	})
}
