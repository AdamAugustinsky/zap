package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/gocore/demo"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// rig is the main window offscreen at 1100×720 points, scale 1, with every
// screen wired, over conn.
type rig struct {
	*widgettest.Rig
	s  *state.State
	sh *shell.Shell
}

func newRig(t *testing.T, conn core.Conn, with ...func(*screen.Set)) *rig {
	t.Helper()
	return newRigSized(t, conn, 0, 0, with...)
}

// newRigSized is newRig with the window w×h points (0: the default size).
// with replaces screens before the shell reads their roles (shell.New).
func newRigSized(t *testing.T, conn core.Conn, w, h float32, with ...func(*screen.Set)) *rig {
	t.Helper()
	s := state.New()
	s.Settings.WindowW, s.Settings.WindowH = w, h
	set := testScreens(true)
	for _, f := range with {
		f(set)
	}
	sh, err := shell.New(s, set)
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic, sh.Slides = true, true
	s.AddAccount("1", nil, conn)
	cfg := shell.Config(s)
	r := &rig{Rig: widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, sh), s: s, sh: sh}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func linked(f *fake.Core) {
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 20},
				{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace Hopper"), LastActivity: 10},
				{ID: []byte("3@s.whatsapp.net"), Name: []byte("Old Friend"), LastActivity: 5, Archived: true},
			}}
		}))
}

// Unlinked, the window is the login screen; linked, the rail and the chats.
func TestRoutesFromLoginToTheChats(t *testing.T) {
	f := fake.New(nil)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) {
		e.Link.Status.Tag = abi.LinkStatusTagUnlinked
		e.Link.Status.Unlinked.QR, e.Link.Status.Unlinked.HasQR = []byte("qr"), true
	}))
	r := newRig(t, f)
	if _, ok := r.Find(ui.RoleButton, "Chats"); ok {
		t.Error("the rail shows while unlinked")
	}
	linked(f)
	r.Frames(3)
	for _, name := range []string{"Chats", "Calls", "Starred messages", "Archived", "Settings", "Switch account"} {
		if _, ok := r.Find(ui.RoleButton, name); !ok {
			t.Errorf("the rail has no %q", name)
		}
	}
	if _, ok := r.Find(ui.RoleButton, "Locked"); ok {
		t.Error("the rail offers Locked with none locked")
	}
	for _, name := range []string{"Ada Lovelace", "Grace Hopper"} {
		if _, ok := r.Find(ui.RoleListItem, name); !ok {
			t.Errorf("no chat row %q", name)
		}
	}
	if !r.s.Accounts[0].Started {
		t.Error("the core was not given its startup permit after the first frame")
	}
}

// A click on a row opens the chat (LoadChat); ⌥↓ steps to the next one;
// Escape closes it; the rail's Calls and ⌘B change the window.
func TestOpensChatsAndRunsTheShortcuts(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	a := r.s.Accounts[0]
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper"), widgettest.Idle(2))
	if c, ok := a.OpenChatView(); !ok || string(c.Name()) != "Grace Hopper" {
		t.Fatalf("open chat %v", a.Open)
	}
	loaded := false
	for _, c := range f.Sent() {
		loaded = loaded || (c.Tag == abi.CommandTagLoadChat && string(c.LoadChat.Chat) == "2@s.whatsapp.net")
	}
	if !loaded {
		t.Errorf("sent %v", f.SentTags())
	}
	r.Play(widgettest.Key(ui.KeyUp, ui.ModAlt), widgettest.Idle(2))
	if c, _ := a.OpenChatView(); string(c.Name()) != "Ada Lovelace" {
		t.Errorf("⌥↑ opened %q", c.Name())
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if a.Open != 0 {
		t.Error("Escape left the chat open")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Calls"), widgettest.Idle(2))
	if r.s.Page != state.PageCalls {
		t.Error("Calls is not on screen")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2), widgettest.Key('B', ui.PrimaryMod()), widgettest.Idle(2))
	if r.s.Page != state.PageChats || !r.s.Panes.ListHidden {
		t.Errorf("page %v, list hidden %v", r.s.Page, r.s.Panes.ListHidden)
	}
	r.Play(widgettest.Key('/', ui.PrimaryMod()), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogShortcuts {
		t.Errorf("⌘/ opened dialog %v", r.s.Dialog.Kind)
	}
}

// The demo core (the real library): the shell builds over it and its chat
// list shows the demo's chats by name.
func TestShellOnTheDemoCore(t *testing.T) {
	if err := demo.Ready(""); err != nil {
		if os.Getenv("ZAPFAST_CORE") != "" {
			t.Fatal(err)
		}
		t.Skip("no core library:", err)
	}
	c, err := demo.OpenChosen(core.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(3 * time.Second)
	r := newRig(t, c)
	a := r.s.Accounts[0]
	for i := 0; i < 400 && !a.Model.Chats.Loaded(); i++ {
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
	}
	r.Frames(3)
	if !a.Model.Chats.Loaded() || !a.Model.Linked() {
		t.Fatalf("the demo's chats did not arrive (linked %v)", a.Model.Linked())
	}
	if _, ok := r.Find(ui.RoleButton, "Chats"); !ok {
		t.Error("no rail")
	}
	rows := 0
	for _, n := range r.Tree().Nodes {
		if n.Role == ui.RoleListItem {
			rows++
		}
	}
	if _, ok := r.Find(ui.RoleListItem, "Ada Lovelace"); !ok || rows < 3 {
		t.Errorf("%d chat rows, Ada Lovelace's missing", rows)
	}
	// Opening a chat loads its history from the demo.
	r.Play(widgettest.Click(ui.RoleListItem, "Ada Lovelace"))
	for i := 0; i < 400 && (a.Conversation() == nil || a.Conversation().Len() == 0); i++ {
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
	}
	if v := a.Conversation(); v == nil || v.Len() == 0 {
		t.Error("the demo sent no history for Ada")
	}
}

// With the list hidden, the rail shows the chats' pictures in the list's
// order (pinned first, then the latest), one cell each, under the
// destinations: the strip's size once went to its first cell, which then
// filled the rail with a pinned chat's picture in its middle.
func TestTheHiddenListsStripKeepsTheListsOrder(t *testing.T) {
	f := fake.New(nil)
	var chats []abi.Chat
	for i := range 6 {
		chats = append(chats, abi.Chat{ID: fmt.Appendf(nil, "%d@s.whatsapp.net", i+1), Name: fmt.Appendf(nil, "Chat %c", 'A'+i), LastActivity: int64(10 + i), Pinned: i == 0})
	}
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: chats} }))
	r := newRig(t, f)
	r.s.Panes.ListHidden = true
	r.s.Touch()
	r.Frames(6)
	tr := r.Tree()
	var got []string
	var top float32
	for i := range tr.Nodes {
		n := &tr.Nodes[i]
		if n.Role != ui.RoleListItem || n.Bounds[0] != 0 || len(tr.Text(n.Name)) == 0 {
			continue
		}
		if h := n.Bounds[3] - n.Bounds[1]; h != 52 {
			t.Errorf("%s is %v points high, want one cell (52)", tr.Text(n.Name), h)
		}
		if len(got) == 0 {
			top = n.Bounds[1]
		}
		got = append(got, string(tr.Text(n.Name)))
	}
	// pinned A, then the latest first
	if want := "[Chat A Chat F Chat E Chat D Chat C Chat B]"; fmt.Sprint(got) != want {
		t.Errorf("the strip shows %v, want %v", got, want)
	}
	if dests := float32(46 + 3*48); top != dests {
		t.Errorf("the strip starts at %v, want under the destinations (%v)", top, dests)
	}
}
