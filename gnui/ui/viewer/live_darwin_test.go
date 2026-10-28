//go:build darwin

package viewer

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/widget/widgettest"

	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// watcher is the shell's app handler with two posts of the test's own: one
// reads what the viewer holds (on the main thread, where it lives), one ends
// the run.
type watcher struct {
	*shell.App
	sc   *Screen
	snap atomic.Pointer[snapshot]
}

type snapshot struct {
	pos            time.Duration
	state          media.VideoState
	w, h           int
	playing, ended bool
	endedSeen      int
	open           bool
}

const (
	postSnapshot = 98
	postQuit     = 99
)

func (w *watcher) Event(a *app.App, e *app.Event) {
	switch {
	case e.Kind == app.EvPost && e.ID == postSnapshot:
		sn := &snapshot{endedSeen: w.sc.endedSeen}
		if v := w.sc.pb.v; v != nil {
			sn.open, sn.pos, sn.state, sn.playing, sn.ended = true, v.Position(), v.State(), v.Playing(), v.Ended()
			sn.w, sn.h = v.Size()
		}
		w.snap.Store(sn)
	case e.Kind == app.EvPost && e.ID == postQuit:
		a.Quit()
	default:
		w.App.Event(a, e)
	}
}

// The framework's own media.Video plays the demo clip in a real window, from
// the viewer opened playing (State.OpenViewerPlaying) to its end: the frames
// arrive, the real Ended event reaches the viewer through the state's app
// hook, and the clip is then at rest on the play disc. Muted. It opens a
// window, so it runs only with ZAPFAST_LIVE=1 (and the screen unlocked).
func TestLiveARealClipPlaysToItsEnd(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a real window: set ZAPFAST_LIVE=1")
	}
	demo, err := filepath.Abs("../../../assets/demo/videos/launch.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(demo); err != nil {
		t.Skip("no demo clip:", err)
	}
	st, _, _, sc, _, _ := setup(t, nil, func(*fake.Core) album { return album{mediaItem("m1", demo, true)} }, "m1")
	st.Video.Muted = true // a test plays no sound
	st.Win.Open[state.WinViewer] = false
	st.OpenViewerPlaying(st.Win.ViewerChat, []byte("m1"))
	sh, err := shell.New(st, &screen.Set{Viewer: sc})
	if err != nil {
		t.Fatal(err)
	}
	h := &watcher{App: shell.NewApp(sh), sc: sc}
	st.Wake = h.Wake

	done := make(chan struct{})
	var mid, last *snapshot
	go func() {
		defer func() {
			app.Post(app.Event{ID: postQuit}, nil)
			close(done)
		}()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			app.Post(app.Event{ID: postSnapshot}, nil)
			time.Sleep(150 * time.Millisecond)
			sn := h.snap.Load()
			if sn == nil {
				continue
			}
			last = sn
			if sn.playing && sn.pos >= 500*time.Millisecond && mid == nil {
				mid = sn
			}
			if sn.endedSeen > 0 && sn.ended {
				return
			}
		}
	}()
	var runErr error
	widgettest.OnMain(func() {
		runErr = app.Main(app.AppConfig{ID: "ai.a3lab.zap.viewer-live"}, h)
	})
	<-done
	if runErr != nil {
		t.Skip("no window to open:", runErr)
	}
	if mid == nil {
		t.Fatalf("the clip did not play at once from OpenViewerPlaying: %+v", last)
	}
	if mid.state != media.VideoReady || mid.w != 640 || mid.h != 360 {
		t.Errorf("the clip: state %v, %dx%d", mid.state, mid.w, mid.h)
	}
	if last == nil || last.endedSeen == 0 || !last.ended || last.playing {
		t.Fatalf("the real end did not reach the viewer: %+v", last)
	}
}
