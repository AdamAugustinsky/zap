package settings

import (
	"testing"

	"gonativeui/ui"
)

// Appearance's Space colour row opens the theme creator on the space on
// screen, in the main window, which it raises: the way in with no space strip
// (one space, so no strip to right-click).
func TestSpaceColourRowAsksTheMainWindow(t *testing.T) {
	r := newRig(t, 900, 700, nil)
	connect(r)
	if !r.text("Space colour") {
		t.Fatal("no Space colour row in Appearance")
	}
	r.click(ui.RoleButton, "Edit…")
	if !r.s.Req.SpaceColour || !r.s.Req.RaiseMain {
		t.Errorf("Req.SpaceColour %v, Req.RaiseMain %v: the main window was not asked", r.s.Req.SpaceColour, r.s.Req.RaiseMain)
	}
}
