//go:build darwin

package av_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/logic"
	"zapfast/state"
	"zapfast/ui/conversation/av"
)

// onMain runs f on the main thread, where AVPlayer plays (its clock moves
// with the main run loop), as a window's frames run.
func onMain(f func()) { widgettest.OnMain(f) }

// A real MP4 GIF uses AVPlayer muted and looping, and the hidden-window
// ticker pauses it even when no transcript block is built.
func TestLiveGIFPausesWhenHidden(t *testing.T) {
	if os.Getenv("AV_LIVE") == "" {
		t.Skip("AV_LIVE=1 plays a muted demo GIF")
	}
	path, err := filepath.Abs("../../../../assets/demo/videos/launch.mp4")
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t)
	r.p.realClock = true
	var v *media.Video
	restore := av.SetHooks(av.Hooks{OpenVideo: func(path string, o media.VideoOptions) (av.Video, error) {
		if !o.Muted || !o.Loop {
			t.Error("GIF is not muted and looping")
		}
		v, err = media.OpenVideo(path, o)
		return v, err
	}})
	t.Cleanup(func() { onMain(av.Forget); restore() })
	r.load(video("gif", 10, path, false, true))
	r.mainPlay(widgettest.Move(ui.RoleButton, "GIF"))
	r.mainUntil("GIF frames arrive", func() bool {
		ready := false
		onMain(func() { _, pixels := media.VideoCounts(); ready = v != nil && v.Playing() && pixels > 0 })
		return ready
	})
	r.mainFrames(20)
	onMain(func() { r.shot("real-gif-playing"); r.p.quiet = true })
	var allocs float64
	onMain(func() { allocs = testing.AllocsPerRun(20, func() { r.Frames(1) }) })
	if allocs != 0 {
		t.Errorf("real GIF frames allocate %v times", allocs)
	}
	onMain(func() { r.p.hidden = true; r.p.skip = map[string]bool{"gif": true} })
	r.mainFrames(3)
	onMain(func() {
		if v.Playing() {
			t.Error("hidden GIF still plays")
		}
		if c := r.Context(); c.Animating() || c.WakeAfter() != 0 {
			t.Errorf("hidden GIF requests frames: %v, %d", c.Animating(), c.WakeAfter())
		}
	})
}

// mainFrames runs n frames on the main thread, the run loop turning
// between them as a window's does.
func (r *rig) mainFrames(n int) {
	onMain(func() {
		for i := 0; i < n; i++ {
			r.Frames(1)
			widgettest.RunLoop(10 * time.Millisecond)
		}
	})
}

// mainUntil runs main-thread frames until cond, for at most five seconds.
func (r *rig) mainUntil(what string, cond func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			r.t.Fatalf("never: %s\n%s", what, widgettest.Dump(r.Tree()))
		}
		r.mainFrames(1)
	}
}

// mainPlay plays steps on the main thread; a step that fails ends the test
// back on the test's goroutine.
func (r *rig) mainPlay(steps ...widgettest.Step) {
	r.t.Helper()
	var err error
	onMain(func() {
		p := r.Player()
		p.Load(steps...)
		for i := 0; i < 600 && !p.Done() && p.Err() == nil; i++ {
			r.Frames(1)
			widgettest.RunLoop(5 * time.Millisecond)
		}
		err = p.Err()
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// Live, with AV_LIVE=1 on a Mac (the demo core and the real media.Video, on
// the main thread): Margaret's round video note plays in its circle, its
// frames arrive (a pixel buffer held, its time moving) without a frame
// allocating, and scrolled out of
// the transcript it lets its player go (media.VideoCounts back to nothing).
// Ada's clip then plays inline with its controls under the pointer, and the
// lock stops it. With AV_SHOT set, the frames are written there.
func TestLiveDemoVideos(t *testing.T) {
	if os.Getenv("AV_LIVE") == "" {
		t.Skip("AV_LIVE=1 plays video and sound")
	}
	if err := demo.Ready(""); err != nil {
		t.Skipf("no core library: %v", err)
	}
	c, err := demo.OpenChosen(core.Config{Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	av.Forget()
	t.Cleanup(func() { onMain(av.Forget) })
	r := &rig{t: t, sh: &opener{}}
	s := state.New()
	s.Shell, s.Demo = r.sh, true
	a := s.AddAccount("1", nil, c)
	r.s, r.a = s, a
	for deadline := time.Now().Add(10 * time.Second); !a.Model.Chats.Loaded(); {
		if time.Now().After(deadline) {
			t.Fatal("no chats within 10 s")
		}
		s.Pump(5 * time.Millisecond)
		time.Sleep(5 * time.Millisecond)
	}
	l := logic.New()
	l.Init(s)
	r.p = &page{s: s, l: l, maxW: 330, realClock: true}
	cfg := app.Config{Title: "av", Width: 420, Height: 700, Clear: [4]float32{1, 1, 1, 1}}
	r.Rig = widgettest.Open(t, cfg, 840, 1400, 2, r.p)
	r.mainFrames(2)
	// find opens chat jid, waits for a video of the kind asked with its
	// file, and puts only it on the page.
	find := func(jid string, note bool) string {
		onMain(func() { a.OpenChat(a.Model.Chats.Find([]byte(jid))) })
		var id string
		r.mainUntil("a demo video with its file", func() bool {
			v := a.Conversation()
			for i := 0; v != nil && i < v.Len(); i++ {
				m := v.At(i)
				if md, ok := m.Media(); ok && m.Kind() == abi.ContentTagVideo && md.VideoNote == note && !md.Gif && len(md.Path) > 0 {
					id = string(m.ID())
					t.Logf("%s: %s, %d s, %s", jid, id, md.Seconds, md.Path)
					return true
				}
			}
			return false
		})
		skip := map[string]bool{}
		v := a.Conversation()
		for i := 0; i < v.Len(); i++ {
			if m := v.At(i); string(m.ID()) != id {
				skip[string(m.ID())] = true
			}
		}
		onMain(func() { r.p.skip = skip })
		r.mainFrames(2)
		return id
	}
	counts := func() (v, pb int) {
		onMain(func() { v, pb = media.VideoCounts() })
		return v, pb
	}
	framed := func() bool { v, pb := counts(); return v == 1 && pb == 1 }

	note := find("14155550199@s.whatsapp.net", true) // Margaret
	start := time.Now()
	r.mainPlay(widgettest.Click(ui.RoleButton, "Video message"))
	r.mainUntil("its frames arrive", framed)
	t.Logf("first frame after %v", time.Since(start))
	r.mainPlay(away)
	r.mainFrames(3)
	var allocs float64
	onMain(func() {
		r.p.quiet = true
		allocs = testing.AllocsPerRun(20, func() {
			time.Sleep(8 * time.Millisecond) // a new frame of the clip is due
			r.Frames(1)
		})
		r.p.quiet = false
	})
	if allocs != 0 {
		t.Errorf("a frame of the playing note allocates %v times", allocs)
	}
	for t0 := time.Now(); time.Since(t0) < 1200*time.Millisecond; {
		r.mainFrames(1)
	}
	var label string
	onMain(func() {
		tr := r.Tree()
		for i := range tr.Nodes {
			if n := &tr.Nodes[i]; n.Role == ui.RoleLabel {
				label = string(tr.Text(n.Name))
			}
		}
	})
	t.Logf("after 1.2 s the pill says %q", label)
	if label == "0:00" || label == "" {
		t.Errorf("its time did not move in 1.2 s: %q", label)
	}
	onMain(func() { r.shot("live-note") })
	onMain(func() { r.p.skip[note] = true }) // scrolled out of the transcript
	for t0 := time.Now(); time.Since(t0) < 1700*time.Millisecond; {
		r.mainFrames(1)
	}
	if v, pb := counts(); v != 0 || pb != 0 {
		t.Errorf("scrolled away, %s are kept", fmt.Sprint(v, " videos and ", pb, " pixel buffers"))
	}

	find("393331234567@s.whatsapp.net", false) // Ada's clip
	r.mainPlay(widgettest.Click(ui.RoleButton, "Video"))
	r.mainUntil("its frames arrive", framed)
	for t0 := time.Now(); time.Since(t0) < 800*time.Millisecond; {
		r.mainFrames(1)
	}
	r.mainPlay(widgettest.Move(ui.RoleButton, "Video"))
	r.mainFrames(3)
	r.must(ui.RoleButton, "Fill the window")
	onMain(func() { r.shot("live-clip") })
	onMain(func() { s.Locked() })
	r.mainFrames(2)
	if v, pb := counts(); v != 0 || pb != 0 {
		t.Errorf("locked, %d videos and %d pixel buffers are kept", v, pb)
	}
}
