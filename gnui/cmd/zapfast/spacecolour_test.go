package main

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// ⌘K finds Space colour among its actions by a word for it, and Enter opens
// the theme creator on the space on screen: a way in with one space, when
// there is no space strip to right-click.
func TestSpotlightOpensSpaceColour(t *testing.T) {
	for _, word := range []string{"colo", "theme", "Space c"} {
		t.Run(word, func(t *testing.T) {
			r, _ := keyRig(t)
			if _, ok := r.Find(ui.RoleMenu, "Space colour"); ok {
				t.Fatal("the theme creator is open already")
			}
			r.Play(widgettest.Key('K', prim), widgettest.Idle(2), widgettest.Type(word), widgettest.Idle(3))
			if _, ok := r.Find(ui.RoleListItem, "Space colour…"); !ok {
				t.Fatalf("%q: no Space colour… action", word)
			}
			r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(4))
			if r.s.Spotlight {
				t.Error("Spotlight is still open")
			}
			if _, ok := r.Find(ui.RoleMenu, "Space colour"); !ok {
				t.Error("the theme creator did not open")
			}
		})
	}
}
