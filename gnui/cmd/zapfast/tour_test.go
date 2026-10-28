//go:build !gnui_release

package main

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"

	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/state"
	"zapfast/ui/shell"
)

// demoCore opens the demo core, or skips without the library.
func demoCore(t *testing.T) core.Conn {
	t.Helper()
	if err := demo.Ready(""); err != nil {
		if os.Getenv("ZAPFAST_CORE") != "" {
			t.Fatal(err)
		}
		t.Skip("no core library:", err)
	}
	c, err := demo.OpenChosen(core.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(3 * time.Second) })
	return c
}

// DM-006: the tour's first played frame is its opening shot (dark, Ada's
// chat open and connected, settled), not the window as it was before:
// the frames after it, in the opening hold, are the same picture.
func TestTheTourOpensOnItsShot(t *testing.T) {
	c := demoCore(t)
	s := state.New()
	s.Demo = true
	sh, err := shell.New(s, testScreens(true))
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	s.AddAccount("1", nil, c)
	tr := newTour(s, sh, true)
	tr.once = true
	cfg := shell.Config(s)
	off, err := app.NewOffscreen(cfg, int(cfg.Width), int(cfg.Height), 1, tr)
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	off.UI.SetSystemAppearance(ui.Appearance{Dark: true, Known: true})
	var first *image.RGBA
	for i := 0; i < 2000 && first == nil; i++ {
		img, _ := off.Frame()
		if tr.phase == tourPlaying {
			first = clone(img)
		} else {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if first == nil {
		t.Fatal("the tour never started")
	}
	if a := s.Acct(); s.Settings.Theme != state.ThemeDark || a.Open != a.Model.Chats.Find([]byte(demoAda)) {
		t.Fatalf("the opening shot is not set: theme %v", s.Settings.Theme)
	}
	for range 10 {
		off.Frame()
	}
	img, _ := off.Frame()
	if d := differing(first, img); d > len(first.Pix)/4/200 {
		t.Errorf("the first frame differs from the opening hold in %d pixels", d)
	}
}

func clone(img *image.RGBA) *image.RGBA {
	c := *img
	c.Pix = append([]byte(nil), img.Pix...)
	return &c
}

// differing counts the pixels that differ between a and b.
func differing(a, b *image.RGBA) int {
	if len(a.Pix) != len(b.Pix) {
		return len(a.Pix) / 4
	}
	n := 0
	for i := 0; i < len(a.Pix); i += 4 {
		if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] {
			n++
		}
	}
	return n
}

// DM-004, DM-005: both scripts play to their end on the demo core, every
// step finding its target through the real screens.
func TestTheToursPlayToTheirEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("plays about 95 s of virtual time")
	}
	for _, script := range []string{"launch", "whats-new"} {
		t.Run(script, func(t *testing.T) {
			c := demoCore(t)
			s := state.New()
			s.Demo = true
			sh, err := shell.New(s, testScreens(true))
			if err != nil {
				t.Fatal(err)
			}
			s.AddAccount("1", nil, c)
			tr := newTour(s, sh, true)
			tr.script, tr.once = script, true
			tr.trace = filepath.Join(t.TempDir(), "tour.json")
			cfg := shell.Config(s)
			off, err := app.NewOffscreen(cfg, int(cfg.Width), int(cfg.Height), 1, tr)
			if err != nil {
				t.Fatal(err)
			}
			defer off.Close()
			for i := 0; i < 60*120 && tr.phase != tourHolding; i++ {
				off.Frame()
				if tr.phase == tourWaiting {
					time.Sleep(2 * time.Millisecond)
				}
			}
			if tr.phase != tourHolding {
				t.Fatal("the tour did not end")
			}
			if err := tr.p.Err(); err != nil {
				t.Error(err)
			}
			// DM-006: the trace for scripts/render-demo.py
			b, err := os.ReadFile(tr.trace)
			if err != nil {
				t.Fatal(err)
			}
			var trace struct {
				Width, Duration float64
				Complete        bool
				Events          []tourEvent
			}
			if err := json.Unmarshal(b, &trace); err != nil {
				t.Fatal(err)
			}
			kinds := map[string]int{}
			for _, e := range trace.Events {
				kinds[e.Kind]++
			}
			if !trace.Complete || trace.Width != float64(cfg.Width) || trace.Duration < 30 || kinds["pointer"] == 0 || kinds["click"] == 0 || kinds["keys"] < 8 {
				t.Errorf("trace: complete %v, width %v, %v s, %v", trace.Complete, trace.Width, trace.Duration, kinds)
			}
			// The times are the recorded frames': a 60th of a second each
			// from the first (frames mode samples them at -demo-fps).
			if want := float64(tr.frames) / 60; trace.Duration < want-0.02 || trace.Duration > want+0.02 {
				t.Errorf("the trace lasts %.3f s, the %d played frames %.3f s", trace.Duration, tr.frames, want)
			}
			if e := trace.Events[0]; e.At < 0 || e.At > 0.05 {
				t.Errorf("the first event at %v s", e.At)
			}
		})
	}
}

// DM-002 on the demo core's data: each page opens the sample it is about
// and points at its message (a jump to it, its info).
func TestDemoPagesOnTheCoresData(t *testing.T) {
	for _, c := range []struct {
		page, chat string
		check      func(s *state.State) bool
	}{
		{"poll-results", demoMargaret, func(s *state.State) bool { return s.Panes.Jump.Seq > 0 }},
		{"video", demoAda, func(s *state.State) bool { return s.Panes.Jump.Seq > 0 }},
		{"message-info-unknown", demoFamily, func(s *state.State) bool {
			return s.Panes.Pane == state.PaneMessageInfo && s.Panes.MessageInfo.Len() == 1
		}},
		{"message-info-partial", demoGroup, func(s *state.State) bool { return s.Panes.Pane == state.PaneMessageInfo }},
		{"receipts-played", demoGrace, func(*state.State) bool { return true }},
		{"gifs", demoAda, func(s *state.State) bool {
			return s.Panes.Picker == state.PickerGifs && string(s.Settings.GiphyKey) == "zf-demo-key"
		}},
		{"sticker-pack-view", demoGroup, func(s *state.State) bool {
			return s.Dialog.Kind == state.DialogStickerPack && s.Dialog.IDs.Len() == 1
		}},
		// the locked chat stays locked after its reading updates it
		{"locked-open", demoDorothy, func(s *state.State) bool {
			a := s.Accounts[0]
			c, _ := a.OpenChatView()
			return a.Mode == state.ListLocked && c.Locked()
		}},
		{"locked-prompt", "", func(s *state.State) bool {
			c, _ := s.Accounts[0].Model.Chats.Get(s.Accounts[0].Model.Chats.Find([]byte(demoDorothy)))
			return c.Locked() && s.Dialog.Kind == state.DialogUnlockLockedChats
		}},
	} {
		t.Run(c.page, func(t *testing.T) {
			r := newRig(t, demoCore(t))
			page := newDemoPage(c.page)
			r.sh.Set.Tickers = append(r.sh.Set.Tickers, page)
			for i := 0; i < 600 && (!page.done || page.late); i++ {
				time.Sleep(2 * time.Millisecond)
				r.Frames(1)
			}
			r.Frames(20) // the core's own updates after the page
			a := r.s.Accounts[0]
			if ch, ok := a.OpenChatView(); (c.chat != "" || ok) && (!ok || string(ch.ID()) != c.chat) {
				t.Fatalf("the page opened %q", ch.ID())
			}
			if !c.check(r.s) {
				t.Errorf("the page did not show its part: pane %v jumps %d", r.s.Panes.Pane, r.s.Panes.Jump.Seq)
			}
		})
	}
}
