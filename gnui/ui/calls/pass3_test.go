package calls

import (
	"os"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// CL-001: the Calls tab's toolbar says "Calls" and offers New call (Hide the
// chat list has no place here); over the new-call list it says "New call"
// with Back, which goes back to the history.
func TestTheCallsTabsToolbar(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history()
	r.Frames(2)
	has(t, r.Rig, ui.RoleLabel, "Calls")
	has(t, r.Rig, ui.RoleButton, "New call")
	hasNot(t, r.Rig, ui.RoleButton, "Back to the calls")
	r.Play(widgettest.Click(ui.RoleButton, "New call"), widgettest.Idle(2))
	has(t, r.Rig, ui.RoleLabel, "New call")
	has(t, r.Rig, ui.RoleButton, "Back to the calls")
	hasNot(t, r.Rig, ui.RoleButton, "New call")
	// The list opens ready for a name.
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleSearchField {
		t.Errorf("focus is %+v, want the search field", n)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Back to the calls"), widgettest.Idle(2))
	has(t, r.Rig, ui.RoleLabel, "Calls")
	has(t, r.Rig, ui.RoleButton, "New call")
}

// CA-022: a second click on Missed returns to every call.
func TestASecondClickOnMissedReturnsToEveryCall(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history(callMsg(ada, "a1", true, w.at(2*time.Hour), abi.CallStatusNoAnswer))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleCheckbox, "Missed"), widgettest.Idle(2))
	if string(w.a.Settings.CallFilter) != "missed" {
		t.Fatalf("filter %q", w.a.Settings.CallFilter)
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Missed"), widgettest.Idle(2))
	if len(w.a.Settings.CallFilter) != 0 {
		t.Errorf("filter %q after the second click", w.a.Settings.CallFilter)
	}
	// All while it is the chosen one stays All.
	r.Play(widgettest.Click(ui.RoleCheckbox, "All"), widgettest.Idle(2))
	if len(w.a.Settings.CallFilter) != 0 {
		t.Errorf("filter %q after All", w.a.Settings.CallFilter)
	}
}

// SH-016: Tab walks the Calls page in upstream's order: New call, the search
// field, All, Missed; Shift+Tab goes back.
func TestTabWalksTheCallsPageInOrder(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history()
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleSearchField, "Search name or number"), widgettest.Idle(1))
	focus := func(step widgettest.Step, role ui.Role, name string) {
		t.Helper()
		r.Play(step, widgettest.Idle(2))
		n, ok := r.Focused()
		if !ok || n.Role != role || string(r.Tree().Text(n.Name)) != name {
			t.Errorf("focus is %v %q, want %v %q", n.Role, string(r.Tree().Text(n.Name)), role, name)
		}
	}
	focus(widgettest.Key(ui.KeyTab, 0), ui.RoleCheckbox, "All")
	focus(widgettest.Key(ui.KeyTab, 0), ui.RoleCheckbox, "Missed")
	focus(widgettest.Key(ui.KeyTab, ui.ModShift), ui.RoleCheckbox, "All")
	focus(widgettest.Key(ui.KeyTab, ui.ModShift), ui.RoleSearchField, "Search name or number")
	focus(widgettest.Key(ui.KeyTab, ui.ModShift), ui.RoleButton, "New call")
}

// blurred makes the window not the key one, as a ringing call's window shown
// alone always is.
type blurred struct{}

func (blurred) Apply(k *widget.Kit, _ *state.State) { k.Context().Input().Focused = false }

// fillOf is the colour of the largest shape drawn inside the node's bounds:
// the round control's fill.
func fillOf(r *winRig, n ui.SemNode) uint32 {
	var best float32
	var col uint32
	for _, q := range r.Quads() {
		if q.Mode != gpu.ModeShape || q.Rect[0] < n.Bounds[0]-1 || q.Rect[2] > n.Bounds[2]+1 || q.Rect[1] < n.Bounds[1]-1 || q.Rect[3] > n.Bounds[3]+1 {
			continue
		}
		if a := (q.Rect[2] - q.Rect[0]) * (q.Rect[3] - q.Rect[1]); a > best {
			best, col = a, q.Color[0]
		}
	}
	return col
}

// The call's controls are solid whether the window is the key one or not:
// Accept the accent, Decline and Hang up the danger colour (a Rex alert tint
// and a grey Accept in a window that is not key were the bug).
func TestTheCallsControlsAreSolidInAnInactiveWindow(t *testing.T) {
	solid := func(what string, col uint32, want func(r, g, b uint32) bool) {
		t.Helper()
		if col&0xFF != 0xFF || !want(col>>24, col>>16&0xFF, col>>8&0xFF) {
			t.Errorf("%s: fill %08X", what, col)
		}
	}
	blue := func(r, g, b uint32) bool { return b > 200 && r < 100 }
	red := func(r, g, b uint32) bool { return r > 180 && g < 90 && b < 110 }

	var ringing [2]map[string]uint32
	for i, th := range []screen.Theme{nil, blurred{}} {
		w := newWorld(t)
		w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
		r := w.windowWith(th)
		r.Frames(3)
		ringing[i] = map[string]uint32{
			"Accept":  fillOf(r, r.Must(ui.RoleButton, "Accept")),
			"Decline": fillOf(r, r.Must(ui.RoleButton, "Decline")),
		}
		if th != nil {
			shot(t, r.Rig, "ringing-inactive")
		}
	}
	for _, n := range []string{"Accept", "Decline"} {
		if ringing[0][n] != ringing[1][n] {
			t.Errorf("%s: %08X in a key window, %08X in one that is not", n, ringing[0][n], ringing[1][n])
		}
	}
	solid("Accept", ringing[1]["Accept"], blue)
	solid("Decline", ringing[1]["Decline"], red)

	w := newWorld(t)
	w.update(incoming(1, abi.CallPhaseActive))
	r := w.windowWith(blurred{})
	r.Frames(3)
	solid("Hang up", fillOf(r, r.Must(ui.RoleButton, "Hang up")), red)
	if g := fillOf(r, r.Must(ui.RoleButton, "Mute")); g&0xFF != 0xFF {
		t.Errorf("Mute: fill %08X, not solid", g)
	}
	shot(t, r.Rig, "active-inactive")
}

// blurTheme is a theme with the window not the key one.
type blurTheme struct{ screen.Theme }

func (b blurTheme) Apply(k *widget.Kit, s *state.State) {
	b.Theme.Apply(k, s)
	k.Context().Input().Focused = false
}

// The ringing window and an active call in ZapFast's light and dark themes,
// in a window that is not the key one, for looking at: written to
// $CALLS_SHOTS only.
func TestCaptureTheCallControlsInBothThemes(t *testing.T) {
	if os.Getenv("CALLS_SHOTS") == "" {
		t.Skip("CALLS_SHOTS is not set")
	}
	for _, mode := range []struct {
		name string
		th   state.Theme
	}{{"light", state.ThemeLight}, {"dark", state.ThemeDark}} {
		for _, c := range []struct {
			name string
			f    func(w *world)
		}{
			{"ringing", func(w *world) { w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming)) }},
			{"ringing-group", func(w *world) {
				w.update(func(u *abi.CallUpdate) {
					u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = w.f.CallGeneration(), []byte(group), abi.CallDirectionIncoming, abi.CallPhaseIncoming, true
					u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada), Joined: true}}
				})
			}},
			{"active", func(w *world) {
				w.update(func(u *abi.CallUpdate) {
					incoming(1, abi.CallPhaseActive)(u)
					u.ActiveForMS, u.Muted = 83_000, true
				})
			}},
		} {
			w := newWorld(t)
			w.s.Settings.Theme = mode.th
			c.f(w)
			r := w.windowWith(blurTheme{ztheme.NewIn("", false)})
			r.Frames(6)
			shot(t, r.Rig, c.name+"-"+mode.name)
		}
	}
}
