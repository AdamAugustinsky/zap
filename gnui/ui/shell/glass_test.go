package shell

import (
	"testing"

	"gonativeui/platform"

	"zapfast/state"
)

// The window gets the system glass only with Transparency on: off, it has
// none, so windows moving behind it cost the system no blurring.
func TestTheGlassFollowsTransparency(t *testing.T) {
	s := state.New()
	if got := Config(s).Backdrop; got != platform.BackdropGlass {
		t.Errorf("with Transparency on the window's backdrop is %v", got)
	}
	s.Settings.Transparency = false
	if got := Config(s).Backdrop; got != platform.BackdropNone {
		t.Errorf("with Transparency off the window's backdrop is %v", got)
	}
}
