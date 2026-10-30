package main

import (
	"math"
	"slices"
	"testing"
	"zapfast/model"
	ztheme "zapfast/theme"
	"zapfast/ui/shell"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// The split view (ui/shell/split.go): whole chats side by side, the one in
// use the open chat. ⌃⌥V puts the open chat beside its neighbour tab; each
// composer sends to its own chat; a press in another pane puts it in use;
// the dock's Expand gives the window to one; a tab dragged onto the
// conversation makes room for itself before the drop, and the open chat's
// own tab goes beside the chat the person was on before; a pane's grip
// drags it along the others, which slide aside while it is held.
func TestSplitView(t *testing.T) {
	r, f := keyRig(t)
	a := r.s.Accounts[0]
	r.s.Panes.ListHidden = true // room for two chats
	r.open(adaJID)
	r.open(graceJID)
	r.open(adaJID) // tabs: Ada, Grace
	ada, grace := a.Model.Chats.Find([]byte(adaJID)), a.Model.Chats.Find([]byte(graceJID))
	panes := func(want ...model.ChatRef) bool { return slices.Equal(a.Panes(), want) }

	r.Play(widgettest.Key('V', ui.ModCtrl|ui.ModAlt), widgettest.Idle(4))
	if a.Open != grace || !panes(ada, grace) {
		t.Fatalf("⌃⌥V: open %q, panes %v", r.openName(), a.Panes())
	}
	fields := allNodes(r, ui.RoleTextField, "Message")
	if len(fields) != 2 {
		t.Fatalf("%d composers, want one per pane", len(fields))
	}

	// the pane in use's composer takes ⌘L, and sends to its chat
	r.Play(widgettest.Key('L', prim), widgettest.Type("to grace"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if c, ok := lastSent(f, abi.CommandTagSendText); !ok || string(c.SendText.Chat) != graceJID {
		t.Fatalf("the second pane's message: %v", f.SentTags())
	}
	// a press in the first pane puts Ada in use; its composer sends to her
	left := fields[0].Bounds
	r.Play(widgettest.Down(ui.RoleTextField, "Message"), widgettest.Up(), widgettest.Idle(3))
	if a.Open != ada || !panes(ada, grace) {
		t.Fatalf("a press in the first pane: open %q, panes %v", r.openName(), a.Panes())
	}
	r.Play(widgettest.Key('L', prim), widgettest.Type("to ada"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if c, ok := lastSent(f, abi.CommandTagSendText); !ok || string(c.SendText.Chat) != adaJID || string(c.SendText.Text) != "to ada" {
		t.Fatalf("the first pane's message went to %q", c.SendText.Chat)
	}

	// a pane's grip, held over the other pane, slides that one aside; the
	// release moves it there
	r.Play(widgettest.MoveTo((left[0]+left[2])/2, left[1]-200), widgettest.Idle(8))
	right := fields[1].Bounds
	r.Play(widgettest.Down(ui.RoleButton, "Drag to move the pane"), widgettest.MoveTo((right[0]+right[2])/2, right[1]-200), widgettest.Idle(12))
	ah, gh := r.Must(ui.RoleButton, "Ada Lovelace").Bounds, r.Must(ui.RoleButton, "Grace Hopper").Bounds
	if ah[0] < gh[0] {
		t.Errorf("held over Grace's pane, Ada's did not take its place: Ada at %v, Grace at %v", ah[0], gh[0])
	}
	r.Play(widgettest.Up(), widgettest.Idle(4))
	if !panes(grace, ada) || a.Open != ada {
		t.Fatalf("moved: open %q, panes %v", r.openName(), a.Panes())
	}

	// the dock under the pointer: Expand gives the window to that chat
	r.Play(widgettest.MoveTo((left[0]+left[2])/2, left[1]-200), widgettest.Idle(8))
	r.Click(ui.RoleButton, "Fill the window with this chat")
	r.Frames(3)
	if a.SplitOn() || a.Open != grace {
		t.Fatalf("expand: open %q, panes %v", r.openName(), a.Panes())
	}

	// a tab dragged onto the conversation: the chat shown makes room, a
	// pane waits for the tab on the pointer's side, and the drop splits
	a.OpenChat(ada)
	r.Frames(3)
	field := r.Must(ui.RoleTextField, "Message")
	x := field.Bounds[2] - (field.Bounds[2]-field.Bounds[0])/6
	y := field.Bounds[1] - 200
	r.Play(widgettest.Down(ui.RoleTab, "Grace Hopper"), widgettest.MoveTo(x, y), widgettest.Idle(12))
	r.Must(ui.RoleGroup, "Drop to split")
	if narrow := r.Must(ui.RoleTextField, "Message"); narrow.Bounds[2] >= field.Bounds[2]-10 {
		t.Errorf("the chat shown did not make room: %v, was %v", narrow.Bounds, field.Bounds)
	}
	if a.Open != ada {
		t.Errorf("while the tab is held the chat it goes beside shows: %q", r.openName())
	}
	r.Play(widgettest.Up(), widgettest.Idle(4))
	if a.Open != grace || !panes(ada, grace) {
		t.Fatalf("drop: open %q, panes %v", r.openName(), a.Panes())
	}

	// the open chat's own tab dragged to the left goes beside the chat the
	// person was on before it
	a.Expand(grace)
	r.Frames(3)
	field = r.Must(ui.RoleTextField, "Message")
	x = field.Bounds[0] + (field.Bounds[2]-field.Bounds[0])/6
	r.Play(widgettest.Down(ui.RoleTab, "Grace Hopper"), widgettest.MoveTo(x, y), widgettest.Idle(12), widgettest.Up(), widgettest.Idle(4))
	if a.Open != grace || !panes(grace, ada) {
		t.Fatalf("the open tab dropped left: open %q, panes %v", r.openName(), a.Panes())
	}
}

// A wider window holds more chats side by side: as many panes as fit at
// their least width (one per pane's screens at most), and a narrower one
// fewer.
func TestTheSplitHoldsAsManyAsFit(t *testing.T) {
	const kathJID = "5@s.whatsapp.net"
	sized := func(w float32, presses int) (*rig, *state.Account) {
		f := fake.New(nil)
		linked(f)
		r := newRigSized(t, f, w, 800)
		r.Frames(3)
		f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
			e.ChatUpdated.Chat = abi.Chat{ID: []byte(kathJID), Name: []byte("Katherine Johnson"), LastActivity: 15}
		}))
		r.Frames(2)
		r.s.Panes.ListHidden = true
		r.open(kathJID)
		r.open(graceJID)
		r.open(adaJID)
		for range presses {
			r.Play(widgettest.Key('V', ui.ModCtrl|ui.ModAlt), widgettest.Idle(4))
		}
		return r, r.s.Accounts[0]
	}
	r, a := sized(1400, 2)
	if n := len(a.Panes()); n != 3 || a.MaxPanes() < 3 {
		t.Fatalf("a 1400 pt window: %d panes (room for %d), want 3", n, a.MaxPanes())
	}
	if n := len(allNodes(r, ui.RoleTextField, "Message")); n != 3 {
		t.Fatalf("%d composers, want 3", n)
	}
	// ⌃⌥V once: two panes; again, no room for a third: the split closes
	r, a = sized(900, 1)
	if a.MaxPanes() != 2 {
		t.Errorf("a 900 pt window has room for %d panes, want 2", a.MaxPanes())
	}
	if n := len(allNodes(r, ui.RoleTextField, "Message")); n != 2 {
		t.Errorf("%d composers in the narrower window, want the 2 that fit", n)
	}
	r.Play(widgettest.Key('V', ui.ModCtrl|ui.ModAlt), widgettest.Idle(4))
	if a.SplitOn() {
		t.Errorf("⌃⌥V with no room left kept the split: %v", a.Panes())
	}
}

// A split is one tab, as Zen groups split tabs: showing another chat puts
// the split aside as a group, the group's tab (or either of its chats)
// brings it back as it was, and its × closes both.
func TestSplitGroupTab(t *testing.T) {
	r, f := keyRig(t)
	a := r.s.Accounts[0]
	const kathJID = "5@s.whatsapp.net"
	f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(kathJID), Name: []byte("Katherine Johnson"), LastActivity: 15}
	}))
	r.Frames(2)
	r.s.Panes.ListHidden = true
	r.open(kathJID)
	r.open(graceJID)
	r.open(adaJID) // tabs: Katherine, Grace, Ada
	ada, grace := a.Model.Chats.Find([]byte(adaJID)), a.Model.Chats.Find([]byte(graceJID))
	a.OpenSplit(grace)
	r.Frames(3)
	group := "Ada Lovelace · Grace Hopper"
	r.Must(ui.RoleTab, group)
	if _, up := r.Find(ui.RoleTab, "Grace Hopper"); up {
		t.Error("the split's chats still have tabs of their own")
	}

	// another chat shows alone; the split waits as the group
	r.Click(ui.RoleTab, "Katherine Johnson")
	r.Frames(3)
	if a.SplitOn() || r.openName() != "Katherine Johnson" || len(a.Group().Chats) != 2 || a.Group().Chats[0] != ada {
		t.Fatalf("another chat: panes %v, open %q, group %+v", a.Panes(), r.openName(), a.Group())
	}
	r.Must(ui.RoleTab, group)
	r.Click(ui.RoleTab, group)
	r.Frames(3)
	if a.Open != grace || !slices.Equal(a.Panes(), []model.ChatRef{ada, grace}) {
		t.Fatalf("the group's tab: open %q, panes %v", r.openName(), a.Panes())
	}

	// either chat of the group brings it back, that chat in use
	r.Click(ui.RoleTab, "Katherine Johnson")
	r.Frames(2)
	a.OpenChat(ada)
	r.Frames(2)
	if a.Open != ada || !a.InSplit(grace) {
		t.Fatalf("Ada from the list: open %q, panes %v", r.openName(), a.Panes())
	}

	// its × closes both chats' tabs (the tab bar's Close, CloseGroupTabs)
	a.CloseGroupTabs()
	r.Frames(3)
	if a.SplitOn() || !a.Group().Empty() || len(a.ChatTabs()) != 1 {
		t.Fatalf("closed: panes %v, group %+v, tabs %v", a.Panes(), a.Group(), a.ChatTabs())
	}
}

// A double click on the sidebar's empty part hides the chat list, and on
// the rail's empty part shows it again.
func TestDoubleClickTheEmptySidebar(t *testing.T) {
	r, _ := keyRig(t)
	r.Play(widgettest.DoubleClick(ui.RoleGroup, "Double-click to hide the chat list"), widgettest.Idle(3))
	if !r.s.Panes.ListHidden {
		t.Fatal("a double click under the rows did not hide the list")
	}
	// under the rail's pictures of the hidden list
	r.Play(widgettest.DoubleClick(ui.RoleGroup, "Double-click to show the chat list"), widgettest.Idle(3))
	if r.s.Panes.ListHidden {
		t.Fatal("a double click on the rail's empty part did not show the list")
	}
}

// The divider follows the pointer, not steps: it stays where the drag
// leaves it, at any share the panes' least width allows.
func TestTheSplitDividerStaysWhereItIsLeft(t *testing.T) {
	r, _ := keyRig(t)
	a := r.s.Accounts[0]
	r.s.Panes.ListHidden = true
	r.open(adaJID)
	a.OpenSplit(a.Model.Chats.Find([]byte(graceJID)))
	r.Frames(3)
	fields := allNodes(r, ui.RoleTextField, "Message")
	if len(fields) != 2 {
		t.Fatalf("%d panes", len(fields))
	}
	total := fields[1].Bounds[2] - fields[0].Bounds[0]
	r.Play(widgettest.Drag(ui.RoleSplitter, "Resize the split", 10, 6), widgettest.Idle(2)) // 6 steps of 10 px
	got := r.s.Share()
	want := 0.5 + 60/total
	if got < want-0.03 || got > want+0.03 {
		t.Fatalf("share %.3f after a 60 px drag, want about %.3f", got, want)
	}
}

// allNodes are the frame's nodes with role and name, in build order.
func allNodes(r *rig, role ui.Role, name string) []ui.SemNode {
	tree := r.Tree()
	var out []ui.SemNode
	for _, n := range tree.Nodes {
		if n.Role == role && string(tree.Text(n.Name)) == name {
			out = append(out, n)
		}
	}
	return out
}

// ⌥⌘← and ⌥⌘→ put the pane beside in use, its composer taking the
// keyboard; with ⇧ they move the pane in use along the others.
func TestThePanesFromTheKeyboard(t *testing.T) {
	r, f := keyRig(t)
	a := r.s.Accounts[0]
	r.s.Panes.ListHidden = true
	r.open(adaJID)
	ada, grace := a.Model.Chats.Find([]byte(adaJID)), a.Model.Chats.Find([]byte(graceJID))
	a.OpenSplit(grace)
	r.Frames(3)
	r.Play(widgettest.Key(ui.KeyLeft, prim|ui.ModAlt), widgettest.Idle(3), widgettest.Type("hi"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if a.Open != ada {
		t.Fatalf("⌥⌘←: open %q", r.openName())
	}
	if c, ok := lastSent(f, abi.CommandTagSendText); !ok || string(c.SendText.Chat) != adaJID {
		t.Fatalf("typing after ⌥⌘← went to %q", c.SendText.Chat)
	}
	// (the test's drawn field takes ⌥⌘⇧→ as a selection; the app's native one
	// leaves it to the menu)
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2), widgettest.Key(ui.KeyRight, prim|ui.ModAlt|ui.ModShift), widgettest.Idle(3))
	if !slices.Equal(a.Panes(), []model.ChatRef{grace, ada}) || a.Open != ada {
		t.Fatalf("⌥⌘⇧→: open %q, panes %v", r.openName(), a.Panes())
	}
}

// A space has a colour, as Arc's: a right click on it in the strip shows
// the swatches, and the one chosen tints the window while it shows.
func TestASpacesColour(t *testing.T) {
	r, _ := keyRig(t)
	a := r.s.Accounts[0]
	work := a.FolderByID(a.NewFolder("Work", "briefcase"))
	a.EditFolder(work, func(f *state.Folder) { f.Space = true })
	r.s.GoToSpace(state.Space{Acct: a.Index, Folder: a.Settings.Folders[work].ID})
	r.Frames(3)
	r.Play(widgettest.RClick(ui.RoleButton, "Work"), widgettest.Idle(2))
	r.Click(ui.RoleButton, "Purple")
	r.Frames(2)
	if got := a.Settings.Folders[work].Tint; got != "purple" || r.s.SpaceTint() != "purple" {
		t.Fatalf("the folder's colour %q, the space's %q", got, r.s.SpaceTint())
	}
	// the accent takes the space's colour, once it has crossfaded (the
	// rig's appearance is dark)
	r.Play(widgettest.Settle())
	if want := ztheme.TintAccent(ztheme.TintColor("purple"), true) | 0xFF; r.sh.Accent()|0xFF != want {
		t.Errorf("in a purple space the accent is %08X, want %08X", r.sh.Accent(), want)
	}
	// the theme creator, in the same popover, which stays open: a colour
	// more makes the colours the space's own, free; a harmony then holds
	// them, the second opposite the first
	r.Click(ui.RoleButton, "Add a colour")
	r.Frames(2)
	got := r.s.SpaceTheme()
	if !got.Custom() || got.Dots[1] == 0 || got.Dots[2] != 0 || got.Dots[0]|0xFF != ztheme.TintColor("purple") {
		t.Fatalf("after Add a colour the space's theme is %+v", got)
	}
	r.Click(ui.RoleButton, "Harmony: Free")
	r.Frames(2)
	got = r.s.SpaceTheme()
	h0, _ := ztheme.FieldPlace(got.Dots[0])
	h1, _ := ztheme.FieldPlace(got.Dots[1])
	if d := math.Abs(math.Mod(h1-h0+360, 360) - 180); got.Mix != "complementary" || d > 2 {
		t.Fatalf("the harmony is %q, the hues %v and %v apart", got.Mix, h0, h1)
	}
	// dragging on the field moves the space's colour, its partner with it,
	// and the opacity is a slider
	r.Play(widgettest.Drag(ui.RoleGroup, "Colours", 10, 6), widgettest.Idle(2))
	moved := r.s.SpaceTheme()
	if moved.Dots[0] == got.Dots[0] {
		t.Fatal("a drag on the field moved no colour")
	}
	m0, _ := ztheme.FieldPlace(moved.Dots[0])
	m1, _ := ztheme.FieldPlace(moved.Dots[1])
	if d := math.Abs(math.Mod(m1-m0+360, 360) - 180); d > 2 {
		t.Errorf("after the drag the pair is %v° from opposite", d)
	}
	if _, ok := r.Find(ui.RoleSlider, "Opacity"); !ok {
		t.Error("there is no Opacity slider")
	}
	r.s.GoToSpace(state.Space{Acct: a.Index})
	r.Play(widgettest.Settle())
	if r.s.SpaceTint() != "" {
		t.Errorf("the account's space took the folder's colour: %q", r.s.SpaceTint())
	}
	if r.sh.Accent()|0xFF != ztheme.Accent(true)|0xFF {
		t.Errorf("back in a space with no colour the accent is %08X, not Zap's green", r.sh.Accent())
	}
}

// A window with one space has no strip to right-click: View → Space colour…
// opens the same colours, under the list's title row, for the space on
// screen.
func TestTheSpaceColourFromTheMenu(t *testing.T) {
	r, _ := keyRig(t)
	a := r.s.Accounts[0]
	if len(a.Settings.Folders) != 0 {
		t.Fatal("the rig has folders")
	}
	r.Play(widgettest.Command(uint16(shell.CmdSpaceColour)), widgettest.Idle(3))
	r.Click(ui.RoleButton, "Purple")
	r.Frames(2)
	if got := a.Settings.SpaceTint; got != "purple" || r.s.SpaceTint() != "purple" {
		t.Fatalf("the account's colour %q, the space's %q", got, r.s.SpaceTint())
	}
}

// A space can keep its own appearance, as Zen's: Light in the Work space
// lights the window while it shows, whatever the app's (the rig's is dark),
// and the account's space, which follows the app, is dark again.
func TestASpacesOwnAppearance(t *testing.T) {
	r, _ := keyRig(t)
	a := r.s.Accounts[0]
	work := a.FolderByID(a.NewFolder("Work", "briefcase"))
	a.EditFolder(work, func(f *state.Folder) { f.Space = true })
	r.s.GoToSpace(state.Space{Acct: a.Index, Folder: a.Settings.Folders[work].ID})
	r.Play(widgettest.Settle())
	if !ztheme.Dark() {
		t.Fatal("the rig is not dark")
	}
	r.Play(widgettest.RClick(ui.RoleButton, "Work"), widgettest.Idle(2))
	r.Click(ui.RoleButton, "Light")
	r.Play(widgettest.Settle())
	if got := a.Settings.Folders[work].TintMode; got != "light" || ztheme.Dark() {
		t.Fatalf("Work's mode %q, the window dark %v", got, ztheme.Dark())
	}
	r.s.GoToSpace(state.Space{Acct: a.Index})
	r.Play(widgettest.Settle())
	if !ztheme.Dark() {
		t.Error("the account's space did not go back to the app's dark")
	}
}
