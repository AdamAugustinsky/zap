//go:build darwin

package chatlist

import (
	"os"
	"slices"
	"sync/atomic"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// A right-click on a chat row in a real window pops up the native menu, and
// choosing an item in it does what it says. It opens a window, so it runs
// only with ZAPFAST_LIVE=1 (and the screen unlocked).
func TestLiveNativeContextMenu(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a real window: set ZAPFAST_LIVE=1")
	}
	f := fake.New(nil)
	s := state.New()
	sh, err := shell.New(s, &screen.Set{ChatList: New()})
	if err != nil {
		t.Fatal(err)
	}
	s.AddAccount("1", nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat("1", "Ada Lovelace", 20)}}
	}))
	var opened atomic.Int32
	widgettest.OnMain(func() {
		platform.DebugOnMenuOpen(func(m platform.DebugMenu) {
			opened.Add(1)
			m.Choose(1) // "Pin to top" (the chat has nothing unread: Mark as unread is first)
		})
	})
	t.Cleanup(func() { widgettest.OnMain(func() { platform.DebugOnMenuOpen(nil) }) }) // after the app ends
	l := widgettest.StartLive(t, shell.Config(s), sh)
	for i := 0; i < 60; i++ { // the cond of Until runs in the frame: look from here
		if _, ok := l.Find(ui.RoleListItem, "Ada Lovelace"); ok {
			break
		}
		l.Frames(2)
	}

	n := l.Must(ui.RoleListItem, "Ada Lovelace")
	x, y := widgettest.Centre(n)
	l.Native(func(w widgettest.Win) { widgettest.Post(w.MouseEvent(widgettest.MouseMove, x, y, 0, 0)) })
	l.Frames(2)
	l.Native(func(w widgettest.Win) { widgettest.Post(w.MouseEvent(widgettest.RightDown, x, y, 0, 1)) })
	l.Frames(2)
	l.Native(func(w widgettest.Win) { widgettest.Post(w.MouseEvent(widgettest.RightUp, x, y, 0, 1)) })
	for i := 0; i < 100 && !slices.Contains(f.SentTags(), abi.CommandTagSetPinned); i++ {
		l.Frames(2)
	}
	if opened.Load() != 1 {
		t.Errorf("the native menu opened %d times", opened.Load())
	}
	for _, c := range f.Sent() {
		if c.Tag == abi.CommandTagSetPinned && !c.SetPinned.Pinned {
			t.Errorf("Pin to top sent an unpin")
		}
	}
}

// A sideways scroll (a touchpad swipe, or Shift and the wheel, which macOS
// reports as the same) over the filter chips brings the chips that did not
// fit into view.
func TestLiveChipsScroll(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a real window: set ZAPFAST_LIVE=1")
	}
	f := fake.New(nil)
	s := state.New()
	s.Settings.SidebarWidth = 240
	sh, err := shell.New(s, &screen.Set{ChatList: New()})
	if err != nil {
		t.Fatal(err)
	}
	s.AddAccount("1", nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat("1", "Ada Lovelace", 20)}}
	}))
	l := widgettest.StartLive(t, shell.Config(s), sh)
	for i := 0; i < 60; i++ {
		if _, ok := l.Find(ui.RoleScrollArea, "chip-filters"); ok {
			break
		}
		l.Frames(2)
	}
	shown := func() bool {
		area, chip := l.Must(ui.RoleScrollArea, "chip-filters"), l.Must(ui.RoleCheckbox, "Channels")
		return chip.Bounds[0] >= area.Bounds[0] && chip.Bounds[2] <= area.Bounds[2]+0.5
	}
	if shown() {
		t.Fatalf("Channels fits at 240 pt: nothing to scroll")
	}
	area := l.Must(ui.RoleScrollArea, "chip-filters")
	x, y := area.Bounds[0]+40, (area.Bounds[1]+area.Bounds[3])/2
	for i := 0; i < 10 && !shown(); i++ {
		l.ScrollWith(x, y, -150, 0, 0)
	}
	if !shown() {
		t.Errorf("a sideways scroll did not bring Channels into view")
	}
}

// Shift and a line-based mouse wheel (a vertical wheel) over the filter chips
// scrolls them sideways; a plain wheel does too (upstream CL-034).
func TestLiveShiftWheelScrollsTheChips(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a real window: set ZAPFAST_LIVE=1")
	}
	f := fake.New(nil)
	s := state.New()
	s.Settings.SidebarWidth = 240
	sh, err := shell.New(s, &screen.Set{ChatList: New()})
	if err != nil {
		t.Fatal(err)
	}
	s.AddAccount("1", nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat("1", "Ada Lovelace", 20)}}
	}))
	l := widgettest.StartLive(t, shell.Config(s), sh)
	for i := 0; i < 60; i++ {
		if _, ok := l.Find(ui.RoleScrollArea, "chip-filters"); ok {
			break
		}
		l.Frames(2)
	}
	chipX := func() float32 { return l.Must(ui.RoleCheckbox, "Channels").Bounds[0] }
	area := l.Must(ui.RoleScrollArea, "chip-filters")
	x, y := area.Bounds[0]+40, (area.Bounds[1]+area.Bounds[3])/2
	wheel := func(mods ui.Mods) {
		for i := 0; i < 6; i++ {
			l.Native(func(w widgettest.Win) {
				if ev, err := w.ScrollLinesEvent(x, y, 0, -3, mods); err == nil {
					widgettest.Dispatch(ev)
				}
			})
			l.Frames(2)
		}
		l.Frames(30) // the scroll eases
	}
	start := chipX()
	wheel(0)
	if chipX() >= start {
		t.Fatalf("a plain wheel did not scroll the chips: Channels %v -> %v", start, chipX())
	}
	l.ScrollWith(x, y, 10000, 0, 0) // return to the left before testing Shift
	l.Frames(30)
	start = chipX()
	wheel(ui.ModShift)
	if chipX() >= start {
		t.Errorf("Shift and the wheel did not scroll the chips: Channels %v -> %v", start, chipX())
	}
}
