package shell

import (
	"testing"

	"gonativeui/app"

	"zapfast/state"
	"zapfast/ui/screen"
)

// MB-029: Find must survive the turn that reopens a closed main window.
// A pending window (not open, no ID yet) lets this test inspect the request
// without opening a native window. openMain must not duplicate that window.
func TestFindRequestsSearchWhileTheMainWindowReopens(t *testing.T) {
	s := state.New()
	h := NewApp(&Shell{S: s, Set: &screen.Set{}})
	pending := &app.Win{}
	h.main.Store(pending)
	h.Event(nil, &app.Event{Kind: app.EvCommand, ID: uint64(CmdFind)})
	if !s.Req.FocusSearch || h.main.Load() != pending {
		t.Fatal("Find was lost while the main window was reopening")
	}
}
