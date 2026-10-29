package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/menu"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const (
	adaJID   = "1@s.whatsapp.net"
	graceJID = "2@s.whatsapp.net"
)

// keyRig is the wired main window, linked, with Ada's and Grace's chats.
func keyRig(t *testing.T) (*rig, *fake.Core) {
	t.Helper()
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.Frames(3)
	return r, f
}

func (r *rig) open(jid string) {
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte(jid)))
	r.s.Touch()
	r.Frames(3)
}

func (r *rig) openName() string {
	c, ok := r.s.Accounts[0].OpenChatView()
	if !ok {
		return ""
	}
	return string(c.Name())
}

func (r *rig) focusedName() string {
	n, ok := r.Focused()
	if !ok {
		return ""
	}
	return string(r.Tree().Text(n.Name))
}

func sentTag(f *fake.Core, tag abi.CommandTag) int {
	n := 0
	for _, c := range f.Sent() {
		if c.Tag == tag {
			n++
		}
	}
	return n
}

var prim = ui.PrimaryMod()

// The shell's shortcuts (zf:src/ui/keys.rs:622-663, KB-*), each pressed on
// the wired window and judged by what it did.
func TestTheShellsShortcuts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(r *rig)
		steps []widgettest.Step
		check func(t *testing.T, r *rig, f *fake.Core)
	}{
		// ⌘K is Zap's search over everything (ui/spotlight); ⌘⇧F keeps the list's
		{"⌘K opens Spotlight on its field", nil, []widgettest.Step{widgettest.Key('K', prim), widgettest.Idle(2)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.focusedName(); !r.s.Spotlight || got != "Search everything" {
				t.Errorf("Spotlight %v, focus on %q", r.s.Spotlight, got)
			}
		}},
		{"⌘⇧F focuses the chat search (KB-001)", nil, []widgettest.Step{widgettest.Key('F', prim|ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.focusedName(); got != "Search" {
				t.Errorf("focus on %q", got)
			}
		}},
		{"⌘F with no chat searches the list (KB-002)", nil, []widgettest.Step{widgettest.Key('F', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.focusedName(); got != "Search" {
				t.Errorf("focus on %q", got)
			}
		}},
		{"⌘F with a chat open searches it (KB-002)", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Key('F', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Panes.Pane != state.PaneSearch {
				t.Errorf("pane %v", r.s.Panes.Pane)
			}
		}},
		// from the list's search (⌘⇧F: ⌘K is Spotlight, a modal, under which the app's commands wait)
		{"⌘L focuses the message field (KB-004)", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Key('F', prim|ui.ModShift), widgettest.Idle(2), widgettest.Key('L', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.focusedName(); got != "Message" {
				t.Errorf("focus on %q", got)
			}
		}},
		{"⌥↓ opens the next chat (KB-005)", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Key(ui.KeyDown, ui.ModAlt)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.openName(); got != "Grace Hopper" {
				t.Errorf("open %q", got)
			}
		}},
		{"⌥↑ opens the previous chat (KB-005)", func(r *rig) { r.open(graceJID) }, []widgettest.Step{widgettest.Key(ui.KeyUp, ui.ModAlt)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.openName(); got != "Ada Lovelace" {
				t.Errorf("open %q", got)
			}
		}},
		{"⌘⇧] opens the next chat (KB-006)", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Key(']', prim|ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.openName(); got != "Grace Hopper" {
				t.Errorf("open %q", got)
			}
		}},
		{"⌘⇧[ opens the previous chat (KB-006)", func(r *rig) { r.open(graceJID) }, []widgettest.Step{widgettest.Key('[', prim|ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.openName(); got != "Ada Lovelace" {
				t.Errorf("open %q", got)
			}
		}},
		// Zap's ⌘1–9 are the titlebar's tabs, as a browser's (upstream's KB-007
		// opened the list's chats by place)
		{"⌘2 opens the second tab", func(r *rig) { r.open(adaJID); r.open(graceJID); r.open(adaJID) }, []widgettest.Step{widgettest.Key('2', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.openName(); got != "Grace Hopper" {
				t.Errorf("open %q", got)
			}
		}},
		{"⌘⇧2 shows Calls, ⌘⇧1 Chats (KB-008, KB-009)", nil, []widgettest.Step{widgettest.Key('2', prim|ui.ModShift), widgettest.Idle(2)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Page != state.PageCalls {
				t.Fatalf("page %v", r.s.Page)
			}
			r.Play(widgettest.Key('1', prim|ui.ModShift), widgettest.Idle(2))
			if r.s.Page != state.PageChats {
				t.Errorf("page %v", r.s.Page)
			}
		}},
		{"Escape closes the chat (KB-012)", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Key(ui.KeyEscape, 0)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Accounts[0].Open != 0 {
				t.Error("still open")
			}
		}},
		{"⌘N opens New chat (KB-013)", nil, []widgettest.Step{widgettest.Key('N', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind != state.DialogNewChat {
				t.Errorf("dialog %v", r.s.Dialog.Kind)
			}
		}},
		{"⌘E archives the open chat (KB-014)", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Key('E', prim)}, func(t *testing.T, r *rig, f *fake.Core) {
			if sentTag(f, abi.CommandTagSetArchived) != 1 {
				t.Error("SetArchived not sent")
			}
		}},
		{"⌘B hides the chat list (KB-018)", nil, []widgettest.Step{widgettest.Key('B', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if !r.s.Panes.ListHidden {
				t.Error("still shown")
			}
		}},
		{"⌘End asks the chat for its newest message", func(r *rig) { r.open(adaJID) }, []widgettest.Step{widgettest.Do(func() {}), widgettest.Key(ui.KeyEnd, prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Req.JumpToBottom {
				t.Error("the request was not taken")
			}
		}},
		{"⌘, opens Settings (KB-022)", nil, []widgettest.Step{widgettest.Key(',', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if !r.s.Win.Open[state.WinSettings] && !r.s.Win.Want[state.WinSettings] {
				t.Error("Settings not asked for")
			}
		}},
		{"⌘= and ⌘- zoom, ⌘0 resets (KB-023, KB-024)", nil, []widgettest.Step{widgettest.Key('=', prim), widgettest.Idle(1), widgettest.Key('=', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if z := r.s.Settings.Zoom; z < 1.19 || z > 1.21 {
				t.Fatalf("zoom %v after two ⌘=", z)
			}
			r.Play(widgettest.Key('-', prim), widgettest.Idle(2))
			if z := r.s.Settings.Zoom; z < 1.09 || z > 1.11 {
				t.Fatalf("zoom %v after ⌘-", z)
			}
			r.Play(widgettest.Key('0', prim), widgettest.Idle(2))
			if r.s.Settings.Zoom != 1 {
				t.Errorf("zoom %v after ⌘0", r.s.Settings.Zoom)
			}
		}},
		{"⌘/ lists the shortcuts (KB-026)", nil, []widgettest.Step{widgettest.Key('/', prim)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind != state.DialogShortcuts {
				t.Errorf("dialog %v", r.s.Dialog.Kind)
			}
		}},
		{"? lists the shortcuts when not typing (KB-026)", nil, []widgettest.Step{widgettest.Key('/', ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind != state.DialogShortcuts {
				t.Errorf("dialog %v", r.s.Dialog.Kind)
			}
		}},
		{"? types in a field (KB-026)", nil, []widgettest.Step{widgettest.Key('K', prim), widgettest.Idle(2), widgettest.Key('/', ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind == state.DialogShortcuts {
				t.Error("? opened the list while typing")
			}
		}},
		{"⌘⇧L locks with a password (KB-027)", func(r *rig) { r.s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$c2FsdA$aGFzaA") }, []widgettest.Step{widgettest.Key('L', prim|ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if !r.s.Lock.Locked {
				t.Error("not locked")
			}
		}},
		{"⌘⇧L does nothing without a password (KB-027)", nil, []widgettest.Step{widgettest.Key('L', prim|ui.ModShift)}, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Lock.Locked {
				t.Error("locked with no app-lock password")
			}
		}},
		{"chat shortcuts wait while a dialog is up (KB-032)", func(r *rig) { r.open(adaJID); r.s.OpenDialog(state.Dialog{Kind: state.DialogNewChat}) }, []widgettest.Step{widgettest.Key('E', prim)}, func(t *testing.T, r *rig, f *fake.Core) {
			if sentTag(f, abi.CommandTagSetArchived) != 0 {
				t.Error("⌘E archived under a dialog")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := keyRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			r.Play(append(tc.steps, widgettest.Idle(3))...)
			tc.check(t, r, f)
		})
	}
}

// ⌘⇧A switches to the next linked number (KB-015).
func TestNextAccountShortcut(t *testing.T) {
	r, _ := keyRig(t)
	g := fake.New(nil)
	linked(g)
	r.s.AddAccount("2", nil, g)
	r.Frames(3)
	r.Play(widgettest.Key('A', prim|ui.ModShift), widgettest.Idle(3))
	if r.s.Active != 1 {
		t.Errorf("active %d", r.s.Active)
	}
}

// openLog is a state.Shell that records what it was asked to open.
type openLog struct {
	mu     sync.Mutex
	opened []string
}

func (o *openLog) Open(path string) error {
	o.mu.Lock()
	o.opened = append(o.opened, path)
	o.mu.Unlock()
	return nil
}

func (o *openLog) wait(t *testing.T, n int) []string {
	t.Helper()
	for range 200 {
		o.mu.Lock()
		got := append([]string(nil), o.opened...)
		o.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("opened nothing")
	return nil
}

// The menu bar's commands (zf:src/macos.rs:47-110, MB-*), each chosen as
// the native menu delivers it.
func TestTheMenuBarsCommands(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cmd   uint16
		setup func(r *rig)
		check func(t *testing.T, r *rig, f *fake.Core)
	}{
		{"About Zap (MB-001)", uint16(shell.CmdAbout), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind != state.DialogAbout {
				t.Errorf("dialog %v", r.s.Dialog.Kind)
			}
		}},
		{"Settings… (MB-002)", uint16(shell.CmdSettings), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if !r.s.Win.Open[state.WinSettings] && !r.s.Win.Want[state.WinSettings] {
				t.Error("Settings not asked for")
			}
		}},
		{"New Chat… (MB-008)", uint16(shell.CmdNewChat), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind != state.DialogNewChat {
				t.Errorf("dialog %v", r.s.Dialog.Kind)
			}
		}},
		{"Find… with a chat open (MB-016)", uint16(shell.CmdFind), func(r *rig) { r.open(adaJID) }, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Panes.Pane != state.PaneSearch {
				t.Errorf("pane %v", r.s.Panes.Pane)
			}
		}},
		{"Find… with none (MB-016)", uint16(shell.CmdFind), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if got := r.focusedName(); got != "Search" {
				t.Errorf("focus on %q", got)
			}
		}},
		{"Toggle Sidebar (MB-017)", uint16(shell.CmdToggleSidebar), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if !r.s.Panes.ListHidden {
				t.Error("still shown")
			}
		}},
		{"Zoom In (MB-018)", uint16(shell.CmdZoomIn), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if z := r.s.Settings.Zoom; z < 1.09 || z > 1.11 {
				t.Errorf("zoom %v", z)
			}
		}},
		{"Zoom Out (MB-019)", uint16(shell.CmdZoomOut), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if z := r.s.Settings.Zoom; z < 0.89 || z > 0.91 {
				t.Errorf("zoom %v", z)
			}
		}},
		{"Actual Size (MB-020)", uint16(shell.CmdActualSize), func(r *rig) { r.s.Settings.Zoom = 1.5 }, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Settings.Zoom != 1 {
				t.Errorf("zoom %v", r.s.Settings.Zoom)
			}
		}},
		{"Show Zap (MB-024)", uint16(shell.CmdShowWindow), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Req.RaiseMain {
				t.Error("a request left behind")
			}
		}},
		{"Keyboard Shortcuts (MB-026)", uint16(shell.CmdShortcuts), nil, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind != state.DialogShortcuts {
				t.Errorf("dialog %v", r.s.Dialog.Kind)
			}
		}},
		{"Keyboard Shortcuts waits while locked (MB-029)", uint16(shell.CmdShortcuts), func(r *rig) { r.s.Lock.Locked = true; r.s.Settings.AppLockHash = []byte("x") }, func(t *testing.T, r *rig, _ *fake.Core) {
			if r.s.Dialog.Kind == state.DialogShortcuts {
				t.Error("opened behind the lock")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := keyRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			r.Play(widgettest.Command(tc.cmd), widgettest.Idle(3))
			tc.check(t, r, f)
		})
	}
}

// ZapFast Help opens the user guide through the state's Shell (MB-027);
// no browser opens in a test.
func TestHelpOpensTheGuide(t *testing.T) {
	r, _ := keyRig(t)
	o := &openLog{}
	r.s.Shell = o
	r.Play(widgettest.Command(uint16(shell.CmdHelp)), widgettest.Idle(2))
	if got := o.wait(t, 1); got[0] != "https://zapfast.rocks/using-zapfast/" {
		t.Errorf("opened %q", got)
	}
}

// The menu's titles and shortcuts are upstream's (zf:src/macos.rs:47-110):
// the bar's menus and every item in order, the shortcut each shows.
func TestTheMenuBarIsUpstreams(t *testing.T) {
	sh, err := shell.New(state.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := sh.Menu()
	want := []struct{ title, items string }{
		{"Zap", "About Zap|Settings…|Quit Zap"},
		{"File", "New Chat…|Close Tab|Close Window"},
		{"Edit", "Undo|Redo|Cut|Copy|Paste|Select All|Find…"},
		{"View", "Toggle Sidebar|Zoom In|Zoom Out|Actual Size|Enter Full Screen"},
		{"Window", "Minimize|Zoom|Show Zap"},
		{"Help", "Keyboard Shortcuts|Zap Help"},
	}
	got := menuTitles(m)
	if len(got) != len(want) {
		t.Fatalf("menus %v", got)
	}
	for i, w := range want {
		if got[i].title != w.title || got[i].items != w.items {
			t.Errorf("menu %d: %q %q, want %q %q", i, got[i].title, got[i].items, w.title, w.items)
		}
	}
}

type menuTitle struct{ title, items string }

// menuTitles are the application menu and the bar's, with their items' titles, "|"-joined (a
// command with a role and no title shows the system's: Minimize).
func menuTitles(m *menu.Model) []menuTitle {
	var out []menuTitle
	menus := m.Menus()
	// The application menu is menu 0, before the bar's.
	for _, i := range append([]int{0}, m.Bar()...) {
		mt := menuTitle{title: menus[i].Title}
		for _, it := range menus[i].Items {
			if it.Kind != menu.ItemCommand {
				continue
			}
			c, _ := m.Command(it.Command)
			name := c.Title
			if name == "" && c.Role == menu.RoleMinimize {
				name = "Minimize"
			}
			if mt.items != "" {
				mt.items += "|"
			}
			mt.items += name
		}
		out = append(out, mt)
	}
	return out
}

// KB-032: the chat's shortcuts wait while the picker, a reaction or a
// recording is up, and pass once it is gone.
func TestChatShortcutsWaitForThePickerAndARecording(t *testing.T) {
	r, f := keyRig(t)
	r.open(adaJID)
	r.s.Panes.Picker = state.PickerEmoji
	r.Play(widgettest.Key('E', prim), widgettest.Idle(2))
	if sentTag(f, abi.CommandTagSetArchived) != 0 {
		t.Error("⌘E archived under the picker")
	}
	r.s.Panes.Picker = state.PickerNone
	r.open(graceJID) // the tabs: Ada, Grace (⌘2)
	r.open(adaJID)
	recording := true
	r.s.OnInUse(t, func(*state.State) bool { return recording })
	r.Play(widgettest.Key('2', prim), widgettest.Idle(2))
	if got := r.openName(); got != "Ada Lovelace" {
		t.Errorf("⌘2 opened %q during a recording", got)
	}
	recording = false
	r.Play(widgettest.Key('2', prim), widgettest.Idle(2))
	if got := r.openName(); got != "Grace Hopper" {
		t.Errorf("⌘2 after the recording opened %q", got)
	}
}

// callsToolbar is a Calls page that gives the list's toolbar its items.
type callsToolbar struct{ built int }

func (c *callsToolbar) Init(*screen.Env) error         { return nil }
func (c *callsToolbar) Build(*screen.Env, screen.Rect) {}
func (c *callsToolbar) Toolbar(e *screen.Env, tb *rexui.Toolbar) {
	c.built++
	tb.Item(0, "New call")
}

// CL-001: on the Calls tab the Calls page's items take the list's toolbar.
func TestTheCallsTabHasItsOwnToolbarItems(t *testing.T) {
	cp := &callsToolbar{}
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f, func(set *screen.Set) { set.CallsPage = cp })
	r.Frames(5)
	if cp.built != 0 {
		t.Fatal("the Calls items on the Chats tab")
	}
	r.Play(widgettest.Key('2', prim|ui.ModShift), widgettest.Idle(3))
	if cp.built == 0 {
		t.Fatal("the Calls page's items were not asked for")
	}
	if _, ok := r.Find(ui.RoleButton, "New call"); !ok {
		t.Errorf("no New call:\n%s", widgettest.Dump(r.Tree()))
	}
	if _, ok := r.Find(ui.RoleButton, "New chat (⌘N)"); ok {
		t.Error("New chat stayed on the Calls tab")
	}
}

// CP-020: with no chat open the window takes dropped files (to say "Open
// a chat first"); with one open the composer's target takes them.
func TestADropWithNoChatOpenIsTaken(t *testing.T) {
	r, _ := keyRig(t)
	if _, accept, _ := r.Context().DropAt(900, 400, ui.DropFiles, ui.DropAllOps); accept&ui.DropFiles == 0 {
		t.Error("a drop with no chat open is refused")
	}
	r.open(adaJID)
	_, accept, target := r.Context().DropAt(900, 400, ui.DropFiles, ui.DropAllOps)
	if accept&ui.DropFiles == 0 || target == ui.HashString(0, "zapfast/no-chat-drop") {
		t.Errorf("the open chat's drop went to %v", target)
	}
}

// SH-017: Escape takes back the topmost thing, one press at a time, in
// upstream's order (zf:src/ui/keys.rs:164-247): a dialog, the picker, the
// selection, the search pane, then the chat itself. The composer-focused
// chain including Starred and the calendar is exercised below.
func TestEscapeTakesBackOneThingAtATime(t *testing.T) {
	r, _ := keyRig(t)
	r.open(adaJID)
	a := r.s.Accounts[0]
	r.s.Panes.Pane = state.PaneSearch
	r.s.Panes.Selecting = true
	r.s.Panes.Picker = state.PickerEmoji
	r.s.OpenDialog(state.Dialog{Kind: state.DialogNewChat})
	r.s.Touch()
	r.Frames(3)
	esc := func(what string, gone func() bool) {
		t.Helper()
		r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
		if !gone() {
			t.Fatalf("Escape did not take back %s", what)
		}
		if a.Open == 0 && what != "the chat" {
			t.Fatalf("Escape closed the chat before %s", what)
		}
	}
	esc("the dialog", func() bool { return !r.s.Dialog.Open() })
	if r.s.Panes.Picker == state.PickerNone || !r.s.Panes.Selecting {
		t.Fatal("the dialog's Escape took more")
	}
	esc("the picker", func() bool { return r.s.Panes.Picker == state.PickerNone })
	esc("the selection", func() bool { return !r.s.Panes.Selecting })
	esc("the search pane", func() bool { return r.s.Panes.Pane == state.PaneNone })
	if r.focusedName() == "Message" {
		// a focused composer lets go of the keyboard first (the single keys
		// work then: shell/keyboard.go), and the next Escape closes the chat
		esc("the composer's focus", func() bool { return r.focusedName() != "Message" })
	}
	esc("the chat", func() bool { return a.Open == 0 })
}

// tabWalk presses key n times from no focus and lists where the focus went.
func (r *rig) tabWalk(n int, mods ui.Mods) []string {
	r.Context().SetFocus(0)
	var got []string
	for range n {
		r.Play(widgettest.Key(ui.KeyTab, mods), widgettest.Idle(1))
		got = append(got, r.focusedName())
	}
	return got
}

// SH-016: the main window's Tab walks upstream's order with every screen's
// stops (zf:src/demo.rs main_tab_cycle): the composer, Send (the
// microphone while it is empty), Attach, Emoji, phone links, the header's Search, the
// rail top to bottom and our avatar, the list's New chat and Hide, its
// search field and the chips (the ones that fit, then More, which the rest
// fold into); it wraps, and Shift+Tab walks it back.
func TestTabWalksTheMainWindowInUpstreamsOrder(t *testing.T) {
	r, f := keyRig(t)
	r.open(adaJID)
	phoneMessages(r, f)
	want := []string{"Message", "Record a voice message", "Attach", "Emoji, GIFs, and stickers", "+1 555 0100", "+1 555 0101", "+1 555 0102", "Search messages",
		"Chats", "Calls", "Starred messages", "Archived", "Settings", "Switch account",
		"New chat (⌘N)", "Hide the chat list (⌘B)", "Search", "All", "Unread", "Private", "More"}
	got := r.tabWalk(len(want)+1, 0)
	if fmt.Sprint(got) != fmt.Sprint(append(want, want[0])) {
		t.Fatalf("Tab walked\n%q\nwant\n%q", got, want)
	}
	back := r.tabWalk(len(want), ui.ModShift)
	for i, g := range back {
		if w := want[len(want)-1-i]; g != w {
			t.Fatalf("Shift+Tab %d went to %q, want %q", i+1, g, w)
		}
	}
	r.tabWalk(5, 0)
	if !r.Context().FocusVisible() {
		t.Fatal("Tab did not show keyboard focus")
	}
	r.Frames(60)
	if n := testing.AllocsPerRun(60, r.Frame); n != 0 {
		t.Fatalf("phone links: %v steady allocations/frame", n)
	}
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	if _, ok := r.Find(ui.RoleButton, "Copy number"); !ok {
		t.Fatal("Enter did not open the phone link's menu")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Message"), widgettest.Idle(2))
	found := false
	for _, cmd := range f.Sent() {
		found = found || cmd.Tag == abi.CommandTagEnsureChat && string(cmd.EnsureChat.Chat) == "15550100@s.whatsapp.net"
	}
	if !found {
		t.Fatal("the keyboard-opened phone menu did not request the number's chat")
	}
	// Send takes the microphone's place once there is text.
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Type("hi"), widgettest.Idle(2))
	r.Play(widgettest.Key(ui.KeyTab, 0), widgettest.Idle(1))
	if got := r.focusedName(); got != "Send" {
		t.Errorf("Tab from the text went to %q, want Send", got)
	}
}

// Styled and web spans before the phone exercise full rich-span indices,
// which differ from markup Tags and ordinals among phone links.
func phoneMessages(r *rig, f *fake.Core) {
	r.s.Shell = &openLog{} // even a misrouted web-link activation stays local
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(adaJID), Batch: 1, Last: true, Complete: true,
			Messages: []abi.Message{
				{ID: []byte("phone1"), Chat: []byte(adaJID), Sender: []byte(adaJID), FromMe: true, Timestamp: r.s.Now().Unix() - 60,
					Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("*Call* example.com then +1 555 0100 or +1 555 0101")}}},
				{ID: []byte("phone2"), Chat: []byte(adaJID), Sender: []byte(adaJID), Timestamp: r.s.Now().Unix() - 30,
					Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("Or +1 555 0102")}}},
			}}
	}))
	r.Frames(4)
}

func TestTabSkipsClippedPhoneSpans(t *testing.T) {
	r, f := keyRig(t)
	r.open(adaJID)
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(adaJID), Batch: 1, Last: true, Complete: true,
			Messages: []abi.Message{{ID: []byte("long"), Chat: []byte(adaJID), Sender: []byte(adaJID), Timestamp: r.s.Now().Unix(),
				Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("+1 555 0100\n" + strings.Repeat("line\n", 80) + "+1 555 0101")}}}}}
	}))
	r.Frames(8)
	got := r.tabWalk(6, 0)
	want := []string{"Message", "Record a voice message", "Attach", "Emoji, GIFs, and stickers", "+1 555 0101", "Search messages"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Tab through clipped message: %q, want %q", got, want)
	}
}

// The composer-focused branch of upstream's Escape chain, with both
// Starred and the conversation's calendar in the same wired window.
func TestEscapeWholeOrderWithTheComposerFocused(t *testing.T) {
	for _, edit := range []bool{false, true} {
		r, f := keyRig(t)
		r.open(adaJID)
		phoneMessages(r, f)
		a, p := r.s.Accounts[0], &r.s.Panes
		if edit {
			p.Edit.Add([]byte("phone1"))
		} else {
			p.Reply.Add([]byte("phone1"))
		}
		r.s.Staged = append(r.s.Staged, state.Staged{ID: 1, Path: []byte("/synthetic/attachment.txt")})
		r.s.Req.FindInChat = true
		r.Frames(30) // the search pane slides in beside the chat, and lands
		r.Play(widgettest.Click(ui.RoleButton, "Filter by date"), widgettest.Idle(2))
		r.s.Req.FocusComposer = true
		r.Frames(3)
		r.Play(widgettest.Key('A', prim), widgettest.Key(ui.KeyBackspace, 0), widgettest.Idle(2), widgettest.Type(":"), widgettest.Idle(2))
		if _, ok := r.Find(ui.RoleList, "Suggestions"); !ok {
			t.Fatal("typing the trigger did not open suggestions")
		}
		a.Mode, p.Picker = state.ListStarred, state.PickerEmoji
		r.s.OpenDialog(state.Dialog{Kind: state.DialogAbout})
		r.Frames(3)
		absent := func(role ui.Role, name string) bool { _, ok := r.Find(role, name); return !ok }
		steps := []struct {
			name string
			gone func() bool
		}{
			{"dialog", func() bool { return !r.s.Dialog.Open() }},
			{"picker", func() bool { return p.Picker == state.PickerNone }},
			{"Starred", func() bool { return a.Mode == state.ListChats }},
			{"calendar", func() bool { return absent(ui.RoleButton, "Previous month") }},
			{"suggestions", func() bool { return absent(ui.RoleList, "Suggestions") }},
			{"staged files", func() bool { return len(r.s.Staged) == 0 }},
			{"edit or reply", func() bool { return p.Edit.Len() == 0 && p.Reply.Len() == 0 }},
			{"search pane", func() bool { return p.Pane == state.PaneNone }},
			// the composer lets go of the keyboard, then the chat closes
			{"composer's focus", func() bool { return p.Pane == state.PaneNone && r.focusedName() != "Message" }},
			{"chat", func() bool { return a.Open == 0 }},
		}
		for i, step := range steps {
			if i > 1 && step.name != "chat" && r.focusedName() != "Message" {
				t.Fatalf("before %s: composer lost focus to %q", step.name, r.focusedName())
			}
			r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
			for j, other := range steps {
				if i == 0 && other.name == "suggestions" {
					continue // the picker covers the completion list
				}
				if got, want := other.gone(), j <= i; got != want {
					t.Fatalf("edit %v, after %s: %s gone %v, want %v", edit, step.name, other.name, got, want)
				}
			}
		}
	}
}

// The in-chat search pane's controls come after the header's Search, in
// reading order (zf:src/ui/pane.rs).
func TestTabReachesTheSearchPane(t *testing.T) {
	r, _ := keyRig(t)
	r.open(adaJID)
	r.s.Panes.Pane = state.PaneSearch
	r.s.Touch()
	r.Frames(3)
	got := r.tabWalk(8, 0)
	want := []string{"Message", "Record a voice message", "Attach", "Emoji, GIFs, and stickers", "Search messages", "Close search", "Filter by date", "Search"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Tab walked\n%q\nwant\n%q", got, want)
	}
}

// The Calls page with a chat open walks upstream's order with ui/calls'
// stops: the composer's, the header's Search, the rail, then the page's New
// call, its search and the All and Missed chips.
func TestTabWalksTheCallsPageInUpstreamsOrder(t *testing.T) {
	r, _ := keyRig(t)
	r.open(adaJID)
	r.s.Page = state.PageCalls
	r.s.Touch()
	r.Frames(3)
	want := []string{"Message", "Record a voice message", "Attach", "Emoji, GIFs, and stickers", "Search messages",
		"Chats", "Calls", "Starred messages", "Archived", "Settings", "Switch account",
		"New call", "Search name or number", "All", "Missed"}
	if got := r.tabWalk(len(want)+1, 0); fmt.Sprint(got) != fmt.Sprint(append(want, want[0])) {
		t.Errorf("Tab walked\n%q\nwant\n%q", got, want)
	}
}

// stubPane is a side pane with a control and no Tab stop: a screen not on
// the order yet.
type stubPane struct{}

func (stubPane) Init(*screen.Env) error      { return nil }
func (stubPane) Width(e *screen.Env) float32 { return e.Pt(300) }
func (stubPane) Build(e *screen.Env, r screen.Rect) {
	e.Begin(r, "stub")
	rexui.Button(e.K.Kit, "Stub control", rexui.ButtonOpts{})
	e.End()
}

// A pane whose screen puts none of its controls in the order leaves the
// frame's Tab to build order, so that Tab still reaches them; the
// deliberate order comes back once it closes.
func TestTabStillReachesAPaneWithoutStops(t *testing.T) {
	r, _ := keyRig(t)
	r.sh.Set.MessageInfo = stubPane{}
	r.open(adaJID) // the composer's stops are there
	r.s.Panes.Pane = state.PaneMessageInfo
	r.s.Touch()
	r.Frames(3)
	if got := fmt.Sprintf("%q", r.tabWalk(40, 0)); !strings.Contains(got, `"Stub control"`) {
		t.Errorf("Tab never reached the pane's control: %s", got)
	}
	r.s.Panes.Pane = state.PaneNone
	r.s.Touch()
	r.Frames(3)
	if got := r.tabWalk(1, 0); got[0] != "Message" {
		t.Errorf("without the pane Tab went first to %q", got[0])
	}
}

// ⌥↓ and ⌥↑ switch chats from the composer too, keeping the keyboard in
// it (zf:src/ui/keys.rs:254-268).
func TestAltArrowsSwitchChatsFromTheComposer(t *testing.T) {
	r, _ := keyRig(t)
	r.open(adaJID)
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Type("hi"), widgettest.Idle(2))
	r.Play(widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(3))
	if got := r.openName(); got != "Grace Hopper" {
		t.Fatalf("⌥↓ from the composer opened %q", got)
	}
	r.Play(widgettest.Key(ui.KeyUp, ui.ModAlt), widgettest.Idle(3))
	if got := r.openName(); got != "Ada Lovelace" {
		t.Errorf("⌥↑ from the composer opened %q", got)
	}
}

// MB-021: View's last item is the system's Full Screen (menu.RoleFullScreen:
// AppKit carries it out and retitles it), ⌃⌘F, also under a modal.
func TestFullScreenIsTheSystemsItem(t *testing.T) {
	r, _ := keyRig(t)
	m := r.sh.Menu()
	id, ok := m.RoleID(menu.RoleFullScreen)
	if !ok || id != menu.ID(shell.CmdFullScreen) {
		t.Fatalf("the role is on %v (%v)", id, ok)
	}
	c, _ := m.Command(id)
	if c.Title != "Enter Full Screen" || !c.InModal || (ui.PrimaryMod() == ui.ModSuper && string(c.Shortcut.AppendLabel(nil)) != "⌃⌘F") {
		t.Errorf("%+v", c)
	}
	for _, mt := range menuTitles(m) {
		if mt.title == "View" && !strings.HasSuffix(mt.items, "|Enter Full Screen") {
			t.Errorf("View: %s", mt.items)
		}
	}
}
