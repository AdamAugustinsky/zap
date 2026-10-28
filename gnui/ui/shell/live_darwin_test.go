//go:build darwin

package shell

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The live test needs the main thread (a real window).
func TestMain(m *testing.M) { widgettest.RunMain(m) }

// mallocCounter counts the allocations made while the shell builds, on
// the main thread, while on.
type mallocCounter struct {
	*Shell
	on              bool
	frames, mallocs uint64
}

func (b *mallocCounter) Build(c *ui.Context) {
	if !b.on {
		b.Shell.Build(c)
		return
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	b.Shell.Build(c)
	runtime.ReadMemStats(&m1)
	b.frames++
	b.mallocs += m1.Mallocs - m0.Mallocs
}

// A real window's steady frames ask the platform nothing that allocates:
// the window's frame (where it is, for the next start) is read at the
// first frame and then only when it may have moved, never per frame (it
// cost 16 B a frame). It opens a window, so it runs only with
// ZAPFAST_LIVE=1.
func TestLiveSteadyFramesDoNotAllocate(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	f := fake.New(nil)
	f.Emit(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: abi.LinkStatus{Tag: abi.LinkStatusTagConnected}}},
		&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
			{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 20},
		}}})
	s := state.New()
	sh, err := New(s, &screen.Set{})
	if err != nil {
		t.Fatal(err)
	}
	a := s.AddAccount("1", nil, f)
	b := &mallocCounter{Shell: sh}
	l := widgettest.StartLive(t, Config(s), b)
	l.Until("the chats", 300, func() bool { return a.Model.Chats.Loaded() })
	l.Frames(30) // warm: the first frames place the window and fill caches
	l.Do(func(*app.App) {
		if !sh.placed {
			t.Error("the window was never placed")
		}
		b.on = true
	})
	l.Frames(120)
	var frames, mallocs uint64
	l.Do(func(*app.App) { b.on, frames, mallocs = false, b.frames, b.mallocs })
	if frames < 100 || mallocs > frames/10 {
		t.Errorf("%d allocations in %d frames of a real window", mallocs, frames)
	}
}

// SH-014: a zoomed (maximized) window is remembered as zoomed with the
// user size it unzooms to, and a window that closed zoomed zooms again at
// launch from that size. It opens windows, so it runs only with
// ZAPFAST_LIVE=1.
func TestLiveTheZoomedStateIsRemembered(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	var first *firstFrame
	open := func(maximized bool) (*state.State, *Shell, *widgettest.LiveApp) {
		s := state.New()
		s.Settings.WindowW, s.Settings.WindowH, s.Settings.WindowMaximized = 820, 600, maximized
		sh, err := New(s, &screen.Set{})
		if err != nil {
			t.Fatal(err)
		}
		first = &firstFrame{Shell: sh}
		l := widgettest.StartLive(t, Config(s), first)
		l.Frames(30)
		return s, sh, l
	}
	s, sh, l := open(false)
	zoom := func(on bool) {
		l.Do(func(*app.App) { sh.mainWin().SetZoomed(on) })
		l.Frames(60)
	}
	zoom(true)
	var zoomed bool
	l.Do(func(*app.App) { zoomed = sh.mainWin().Zoomed() })
	if st := s.Settings; !zoomed || !st.WindowMaximized || st.WindowW != 820 || st.WindowH != 600 {
		t.Errorf("zoomed %v: maximized %v, size %v×%v", zoomed, st.WindowMaximized, st.WindowW, st.WindowH)
	}
	zoom(false)
	if st := s.Settings; st.WindowMaximized || st.WindowW != 820 || st.WindowH != 600 {
		t.Errorf("unzoomed: maximized %v, size %v×%v", st.WindowMaximized, st.WindowW, st.WindowH)
	}
	l.Close()
	s, sh, l = open(true)
	l.Frames(30)
	l.Do(func(*app.App) { zoomed = sh.mainWin().Zoomed() })
	if !first.zoomed {
		t.Error("the relaunched window was not zoomed at its first frame: it showed, then zoomed")
	}
	if st := s.Settings; !zoomed || !st.WindowMaximized || st.WindowW != 820 || st.WindowH != 600 {
		t.Errorf("relaunched zoomed %v: maximized %v, size %v×%v", zoomed, st.WindowMaximized, st.WindowW, st.WindowH)
	}
	l.Close()
}

// firstFrame is the shell, noting whether its window was zoomed when it built
// its first frame.
type firstFrame struct {
	*Shell
	built, zoomed bool
}

func (f *firstFrame) Build(c *ui.Context) {
	if !f.built {
		f.built = true
		f.zoomed = f.a != nil && f.a.Win != nil && f.a.Win.Zoomed()
	}
	f.Shell.Build(c)
}
