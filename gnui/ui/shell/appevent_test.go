package shell

import (
	"testing"

	"gonativeui/app"

	"zapfast/state"
	"zapfast/ui/screen"
)

// MB-029: with no main window (closed, in the tray) the menu's edit commands
// are dropped: they neither open the window nor reach a text field (there is
// none), where New Chat and the like open it first. The app has no *app.App
// here, so a command that went on to open the window would fail the test by
// dereferencing it.
func TestEditCommandsWithNoMainWindowAreDropped(t *testing.T) {
	s := state.New()
	sh := &Shell{S: s, Set: &screen.Set{}}
	h := NewApp(sh)
	for _, id := range []uint64{uint64(CmdUndo), uint64(CmdRedo), uint64(CmdCut), uint64(CmdCopy), uint64(CmdPaste), uint64(CmdSelectAll)} {
		h.Event(nil, &app.Event{Kind: app.EvCommand, ID: id})
		if h.main.Load() != nil || s.Win.Want[state.WinSettings] || s.Dialog.Kind != state.DialogNone {
			t.Fatalf("command %d changed the app's windows", id)
		}
	}
}
