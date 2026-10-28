package composer

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/i18n"
	"zapfast/state"
)

// CP-022: the shortcut hints line fits the composer's own width, in the
// longest languages, at the narrowest window and beside a side pane: no
// hint runs past its row or under the pane, none is cut in a word, and the
// buttons stay in view.
func TestTheHintsLineFitsInTheLongestLanguages(t *testing.T) {
	for _, lang := range []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French} {
		for _, c := range []struct {
			name string
			w, h int
			pane state.Pane
		}{
			{"narrow", 600, 400, state.PaneNone},
			{"info pane", 1100, 720, state.PaneInfo},
			{"search pane", 1100, 720, state.PaneSearch},
		} {
			t.Run(lang.Tag()+"/"+c.name, func(t *testing.T) {
				old := i18n.Current()
				i18n.SetLocale(lang)
				t.Cleanup(func() { i18n.SetLocale(old) })
				r := newRigSized(t, c.w, c.h, nil, func(s *state.State) { s.Settings.ShowShortcutHints = true }, nil)
				r.open(ada)
				r.s.Panes.Pane = c.pane
				r.Frames(6)
				r.AuditLayout(lang.Tag()+"/"+c.name, widgettest.Allow{Check: ui.AuditCentre, Where: "Switch account",
					Reason: "the rail's account picture, the shell's, off by 0.6 px with a pane open"},
					widgettest.Allow{Check: ui.AuditCentre, Where: "Image \"99@s.whatsapp.net\"", Reason: "the same picture in the other languages"})
				if !r.has(ui.RoleButton, i18n.Format(i18n.T("All shortcuts ({})"), "{}", keyLabel("Ctrl+/"))) {
					t.Error("the shortcuts button is gone")
				}
			})
		}
	}
}

// At a width where everything fits, the line is whole.
func TestTheHintsLineIsWholeWhereItFits(t *testing.T) {
	r := newRig(t, nil, func(s *state.State) { s.Settings.ShowShortcutHints = true })
	r.open(ada)
	if !r.hasText(r.scr.hints.hint) {
		t.Errorf("the hints are cut: %q", r.scr.hints.shown)
	}
}

// Fitting the line costs nothing in a steady frame, fitted or whole.
func TestTheHintsLineAllocatesNothing(t *testing.T) {
	for _, w := range []int{600, 1100} {
		r := newRigSized(t, w, 600, nil, func(s *state.State) { s.Settings.ShowShortcutHints = true }, nil)
		r.open(ada)
		r.s.Panes.Pane = state.PaneInfo
		r.Frames(10)
		if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
			t.Errorf("%d wide: %v allocations per frame", w, n)
		}
	}
}
