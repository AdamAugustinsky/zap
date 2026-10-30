package shell

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/state"
)

// A space with grain lays its tile across the window; one without lays
// none.
func TestASpacesGrain(t *testing.T) {
	p := newPaneRig(t, 1100)
	sp := state.Space{Acct: p.a.Index}
	tiles := func() bool {
		r := p.r.Context().RectOf(ui.KeyN(grainKey, 1))
		return r[2] > r[0]
	}
	p.frames()
	if tiles() {
		t.Fatal("grain without asking for it")
	}
	p.a.SetTheme(sp, state.SpaceTheme{Tint: "teal", Grain: 1})
	p.r.Play(widgettest.Settle())
	p.frames()
	if !tiles() {
		t.Fatal("a space with grain shows none")
	}
	p.a.SetTheme(sp, state.SpaceTheme{Tint: "teal"})
	p.r.Play(widgettest.Settle())
	p.frames()
	if tiles() {
		t.Error("the grain stayed once taken away")
	}
}

// Hiding the list (⌘B) with motion on, it glides away: a frame later it is
// still drawn where it was, its right edge part of the way toward the rail
// (never over it), and once it has gone the conversation has its room; it
// glides back the same way.
func TestTheListGlides(t *testing.T) {
	p := newPaneRig(t, 1100)
	p.sh.Slides = true
	p.open("1@s.whatsapp.net")
	p.r.Play(widgettest.Settle())
	listKey, chatKey := p.sh.panes.PaneKey(paneList), p.sh.panes.PaneKey(paneChat)
	rectOf := func(k ui.Key) [4]float32 { return p.r.Context().RectOf(k) }
	at := rectOf(listKey)
	chatAt := rectOf(chatKey)
	if at[2] <= at[0] {
		t.Fatal("no list to hide")
	}
	p.s.Panes.ListHidden = true
	p.s.Touch()
	p.r.Frames(2)
	mid := rectOf(listKey)
	if mid[2] <= mid[0] || mid[0] != at[0] || mid[2] >= at[2] {
		t.Fatalf("a frame after hiding, the list is at %v (it was at %v): no glide", mid, at)
	}
	p.r.Play(widgettest.Settle())
	if r := rectOf(listKey); r[2] > r[0] && r[2] > at[0] {
		t.Errorf("the list is still drawn at %v once it has gone", r)
	}
	if c := rectOf(chatKey); c[0] >= chatAt[0] {
		t.Errorf("the conversation did not take the list's room: %v, was %v", c, chatAt)
	}
	p.s.Panes.ListHidden = false
	p.s.Touch()
	p.r.Play(widgettest.Settle())
	if r := rectOf(listKey); r != at {
		t.Errorf("the list came back to %v, not %v", r, at)
	}
}
