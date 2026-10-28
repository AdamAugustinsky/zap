package shell

import (
	"testing"

	"gonativeui/app"

	"zapfast/state"
	"zapfast/ui/screen"
)

type fakeOpener struct{ titles []string }

func (f *fakeOpener) OpenWindow(cfg app.WindowConfig, _ app.Builder) (*app.Win, error) {
	f.titles = append(f.titles, cfg.Title)
	return &app.Win{}, nil
}

type stubWindow struct{ title string }

func (w *stubWindow) Init(*screen.Env) error               { return nil }
func (w *stubWindow) Config(*state.State) app.WindowConfig { return app.WindowConfig{Title: w.title} }
func (w *stubWindow) Build(*screen.Env)                    {}

// CA-004: with no main window, a window asked for alone (a ringing call)
// opens by itself and is to come forward once open; one asked for the usual
// way (Settings) waits for the main window.
func TestAWindowOpensAloneWithNoMainWindow(t *testing.T) {
	s := state.New()
	sh := &Shell{S: s, Set: &screen.Set{Call: &stubWindow{"Call"}, Settings: &stubWindow{"Settings"}}}
	s.OpenWindow(state.WinSettings)
	s.OpenWindowAlone(state.WinCall)
	f := &fakeOpener{}
	if !sh.manage(f, true) {
		t.Error("the call window is not to come forward")
	}
	if len(f.titles) != 1 || f.titles[0] != "Call" || !s.Win.Open[state.WinCall] || s.Win.Want[state.WinCall] || s.Win.Alone[state.WinCall] {
		t.Errorf("opened %v, open %v want %v", f.titles, s.Win.Open, s.Win.Want)
	}
	if !s.Win.Want[state.WinSettings] || s.Win.Open[state.WinSettings] {
		t.Error("Settings did not wait for the main window")
	}
	// The main window's frame opens the rest.
	if sh.manage(f, false); len(f.titles) != 2 || f.titles[1] != "Settings" {
		t.Errorf("opened %v", f.titles)
	}
}
