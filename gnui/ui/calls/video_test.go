package calls

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core"
)

// spyYUV is the renderer the call's textures are made on, with a count of
// what went through it.
type spyYUV struct {
	inner  yuvSink
	made   []struct{ w, h int }
	staged []gpu.YUVImage // the planes' lengths and strides
	pts    int
	freed  int
	live   map[gpu.TextureID]bool
}

func (s *spyYUV) NewYUVTexture(f gpu.YUVFormat, w, h int, sp gpu.YUVSpace) (gpu.TextureID, error) {
	t, err := s.inner.NewYUVTexture(f, w, h, sp)
	if err == nil {
		s.made = append(s.made, struct{ w, h int }{w, h})
		if s.live == nil {
			s.live = map[gpu.TextureID]bool{}
		}
		s.live[t] = true
	}
	return t, err
}

func (s *spyYUV) StageYUV(t gpu.TextureID, im *gpu.YUVImage) {
	c := *im
	c.Y, c.U, c.V = make([]byte, len(im.Y)), make([]byte, len(im.U)), make([]byte, len(im.V))
	s.staged = append(s.staged, c)
	s.inner.StageYUV(t, im)
}

func (s *spyYUV) FreeTexture(t gpu.TextureID) {
	if s.live[t] {
		delete(s.live, t)
		s.freed++
	}
	s.inner.FreeTexture(t)
}

// colourFrame is an I420 frame of w × h (even) as the core hands one out:
// one buffer, the Y, U and V planes one after the other, each row padded
// (the padding is 0xFF, which would draw as a stripe if it were read), the
// left half red and the right half blue in limited-range BT.601.
func colourFrame(w, h int, pts int64) core.Frame {
	cw, ch := w/2, h/2
	st := [3]int{w + 12, cw + 6, cw + 10}
	px := make([]byte, st[0]*h+(st[1]+st[2])*ch)
	for i := range px {
		px[i] = 0xFF
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px[y*st[0]+x] = [2]byte{81, 41}[b2i(x >= w/2)]
		}
	}
	u0, v0 := st[0]*h, st[0]*h+st[1]*ch
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			right := x >= cw/2
			px[u0+y*st[1]+x] = [2]byte{90, 240}[b2i(right)]
			px[v0+y*st[2]+x] = [2]byte{240, 110}[b2i(right)]
		}
	}
	return core.Frame{Px: px, W: w, H: h, Stride: st, Format: core.FrameI420, PTS: pts}
}

// liveVideo is a world with a video call up, its remote stream showing frame
// f, and a spy on its textures; the window is open.
func liveVideo(t *testing.T, f core.Frame) (*world, *winRig, *spyYUV) {
	t.Helper()
	w := newWorld(t)
	spy := &spyYUV{}
	w.h.vid.wrap = func(in yuvSink) yuvSink { spy.inner = in; return spy }
	w.f.SetFrame(core.StreamRemote, f)
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.RemoteVideo, u.ActiveForMS = true, true, 5_000
	})
	return w, w.window(), spy
}

// CA-011: a frame from the core reaches its stream's texture, plane by plane
// with the strides the core gave and nothing of the padding; the texture is
// the frame's size and is written once for a picture, again for the next, and
// made again (the old one freed) when the size changes.
func TestAFrameReachesItsStreamsTexture(t *testing.T) {
	f := colourFrame(64, 36, 1)
	w, r, spy := liveVideo(t, f)
	if len(spy.made) != 1 || spy.made[0].w != 64 || spy.made[0].h != 36 {
		t.Fatalf("textures made: %+v", spy.made)
	}
	if len(spy.staged) != 1 {
		t.Fatalf("%d writes for one frame", len(spy.staged))
	}
	im := spy.staged[0]
	if im.YStride != 76 || im.UStride != 38 || im.VStride != 42 || len(im.Y) != 76*36 || len(im.U) != 38*18 || len(im.V) != 42*18 {
		t.Errorf("planes: Y %d×%d, U %d×%d, V %d×%d", im.YStride, len(im.Y), im.UStride, len(im.U), im.VStride, len(im.V))
	}

	// The same picture again and again: not written again.
	r.Frames(5)
	if len(spy.staged) != 1 {
		t.Errorf("%d writes for one picture", len(spy.staged))
	}
	// The next picture.
	w.f.SetFrame(core.StreamRemote, colourFrame(64, 36, 2))
	r.Frames(2)
	if len(spy.staged) != 2 || len(spy.made) != 1 {
		t.Errorf("next picture: %d writes, %d textures", len(spy.staged), len(spy.made))
	}
	// Another size: a new texture, the old one freed.
	w.f.SetFrame(core.StreamRemote, colourFrame(96, 54, 3))
	r.Frames(2)
	if len(spy.made) != 2 || spy.made[1].w != 96 || spy.freed != 1 || len(spy.live) != 1 {
		t.Errorf("new size: made %+v, freed %d, live %d", spy.made, spy.freed, len(spy.live))
	}
	// A frame that is not whole, or not I420, writes nothing and breaks nothing.
	bad := colourFrame(96, 54, 4)
	bad.Px = bad.Px[:len(bad.Px)-1]
	w.f.SetFrame(core.StreamRemote, bad)
	r.Frames(2)
	bad = colourFrame(96, 54, 5)
	bad.Format = core.FrameRGBA
	w.f.SetFrame(core.StreamRemote, bad)
	r.Frames(2)
	if len(spy.staged) != 3 || w.f.Held(core.StreamRemote) {
		t.Errorf("bad frames: %d writes, held %v", len(spy.staged), w.f.Held(core.StreamRemote))
	}
}

// CA-011: the textures are freed when the camera goes off, when the window
// goes and when the call ends.
func TestVideoTexturesAreFreedWithTheirReason(t *testing.T) {
	w, r, spy := liveVideo(t, colourFrame(64, 36, 1))
	if len(spy.live) != 1 {
		t.Fatalf("%d live textures", len(spy.live))
	}
	// Their camera goes off.
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.RemoteVideo = true, false
	})
	r.Frames(2)
	if len(spy.live) != 0 || spy.freed != 1 {
		t.Errorf("camera off: live %d, freed %d", len(spy.live), spy.freed)
	}
	// It comes back, and the window is put aside.
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.RemoteVideo = true, true
	})
	r.Frames(2)
	if len(spy.live) != 1 {
		t.Fatalf("camera on: live %d", len(spy.live))
	}
	w.h.leave()
	r.Frames(2)
	if len(spy.live) != 0 {
		t.Errorf("window put aside: live %d", len(spy.live))
	}
	// The call comes back to the screen and ends.
	w.h.returnTo()
	r.Frames(2)
	if len(spy.live) != 1 {
		t.Fatalf("returned: live %d", len(spy.live))
	}
	w.h.hangup()
	if len(spy.live) != 0 {
		t.Errorf("call over: live %d", len(spy.live))
	}
	if spy.freed != len(spy.made) {
		t.Errorf("%d textures made, %d freed", len(spy.made), spy.freed)
	}
}

// CA-011: the picture is drawn: the frame's left is red and its right blue
// on screen (the whole window, cropped from the middle), nothing mirrored and
// none of the row padding in it; the call's words stay over it.
func TestTheRemotePictureFillsTheWindowUnmirrored(t *testing.T) {
	_, r, _ := liveVideo(t, colourFrame(128, 72, 1))
	shot(t, r.Rig, "video-remote")
	img := r.Image()
	at := func(x, y int) [3]int {
		c := img.RGBAAt(x, y)
		return [3]int{int(c.R), int(c.G), int(c.B)}
	}
	if c := at(120, 200); c[0] < 200 || c[1] > 60 || c[2] > 70 {
		t.Errorf("left of the window %v, want red", c)
	}
	if c := at(600, 200); c[2] < 190 || c[0] > 70 || c[1] > 70 {
		t.Errorf("right of the window %v, want blue", c)
	}
	has(t, r.Rig, ui.RoleButton, "Hang up")
}

// CA-011: our own picture is in the corner at its own shape, a group call's
// tiles show each member's camera with their name on a plate; both from the
// core's frames.
func TestOurPreviewAndAGroupsTilesShowTheirCameras(t *testing.T) {
	w := newWorld(t)
	spy := &spyYUV{}
	w.h.vid.wrap = func(in yuvSink) yuvSink { spy.inner = in; return spy }
	w.f.SetFrame(core.StreamLocal, colourFrame(64, 36, 1))
	w.f.SetFrame(core.StreamRemote, colourFrame(128, 72, 1))
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.CameraOn, u.RemoteVideo = true, true, true
	})
	r := w.window()
	if len(spy.made) != 2 {
		t.Fatalf("textures made: %+v", spy.made)
	}
	pv, ok := r.Find(ui.RoleGroup, "You")
	if !ok {
		t.Fatal("no preview")
	}
	if pw, ph := float32(pv.Bounds[2]-pv.Bounds[0]), float32(pv.Bounds[3]-pv.Bounds[1]); pw/ph < 1.7 || pw/ph > 1.85 {
		t.Errorf("preview %v × %v, not the camera's 16:9", pw, ph)
	}
	shot(t, r.Rig, "video-preview")

	w = newWorld(t)
	spy = &spyYUV{}
	w.h.vid.wrap = func(in yuvSink) yuvSink { spy.inner = in; return spy }
	w.f.SetFrame(core.StreamLocal, colourFrame(64, 36, 1))
	w.f.SetFrame(core.StreamMember, colourFrame(64, 36, 1))
	w.f.SetFrame(core.StreamMember+1, colourFrame(48, 36, 1))
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup, u.Video, u.CameraOn = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true, true, true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true, Video: true}, {ID: []byte(alan), Joined: true, Video: true}, {ID: []byte(ada), Joined: true}}
	})
	r = w.window()
	if len(spy.made) != 3 {
		t.Fatalf("group textures made: %+v", spy.made)
	}
	shot(t, r.Rig, "video-group")
	has(t, r.Rig, ui.RoleLabel, "Grace Hopper") // the name plate over the camera
	has(t, r.Rig, ui.RoleLabel, "Alan Turing")
	has(t, r.Rig, ui.RoleImage, "Ada Lovelace") // no camera: the picture
}

// A video window asks for its next frame itself while it shows, and an idle
// one asks for nothing.
func TestAVideoCallDrivesItsOwnFrames(t *testing.T) {
	_, r, _ := liveVideo(t, colourFrame(64, 36, 1))
	r.Frames(3)
	if r.Context().WakeAfter() <= 0 {
		t.Error("a showing video asks for no frame")
	}
}

// Allocations: steady frames with a new picture each frame (copied into the
// texture) allocate nothing.
func TestVideoFlowingAllocatesNothing(t *testing.T) {
	w := newWorld(t)
	a, b := colourFrame(64, 36, 1), colourFrame(64, 36, 2)
	la, lb := colourFrame(32, 18, 1), colourFrame(32, 18, 2)
	w.f.SetFrame(core.StreamRemote, a)
	w.f.SetFrame(core.StreamLocal, la)
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.CameraOn, u.RemoteVideo, u.ActiveForMS = true, true, true, 61_000
	})
	r := w.window()
	r.Frames(6)
	n := 0
	run := func() {
		n++
		if n%2 == 0 {
			w.f.SetFrame(core.StreamRemote, a)
			w.f.SetFrame(core.StreamLocal, la)
		} else {
			w.f.SetFrame(core.StreamRemote, b)
			w.f.SetFrame(core.StreamLocal, lb)
		}
		r.Frame()
	}
	run()
	if got := testing.AllocsPerRun(20, run); got != 0 {
		t.Errorf("%v allocations per frame with video flowing", got)
	}
}
