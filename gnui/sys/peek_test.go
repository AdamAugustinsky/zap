package sys

import (
	"testing"

	"gonativeui/app"
	"gonativeui/app/hotkey"
	"gonativeui/menu"
	"gonativeui/platform"
)

// peekRig is a rig with the global shortcut and the Dock menu caught.
func peekRig(t *testing.T) (*rig, *[]hotkey.Shortcut, **menu.Model) {
	var keys []hotkey.Shortcut
	var dock *menu.Model
	r := newRig(t, func(r *rig) {
		r.y.p.hotkey = func(_ hotkey.ID, s hotkey.Shortcut) error { keys = append(keys, s); return nil }
		r.y.p.setMenu = func(m *menu.Model) { dock = m }
	})
	return r, &keys, &dock
}

func press(r *rig) bool {
	return r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvService, Source: platform.SourceHotkey, ID: uint64(peekKey)})
}

func titles(m *menu.Model) []string {
	var out []string
	for _, c := range m.Commands() {
		out = append(out, c.Title)
	}
	return out
}

// ⌃⌥Z peeks at the newest unread chat from anywhere, and closes the peek
// when it is up.
func TestTheShortcutPeeksAtTheNewestUnread(t *testing.T) {
	r, keys, _ := peekRig(t)
	if len(*keys) != 1 || (*keys)[0] != peekChord {
		t.Fatalf("registered %v", *keys)
	}
	r.incoming(grace, grace, "hi", 1)
	if !press(r) {
		t.Fatal("the press was not handled")
	}
	if !r.s.FloatPeek.Open() || string(r.a.Model.Chats.At(r.s.FloatPeek.Ref).ID()) != grace {
		t.Fatalf("peek %+v", r.s.FloatPeek)
	}
	press(r)
	if r.s.FloatPeek.Open() {
		t.Error("a second press did not close it")
	}
	// behind the lock it shows the lock screen
	r.s.Lock.Locked = true
	press(r)
	if r.s.FloatPeek.Open() || !r.s.Req.RaiseMain {
		t.Errorf("locked: peek %v, raise %v", r.s.FloatPeek.Open(), r.s.Req.RaiseMain)
	}
}

// The Dock's menu names the unread chats while notifications may, and
// peeks at the one chosen; with their content hidden it names none.
func TestTheDockMenuPeeksAtTheUnreadChats(t *testing.T) {
	r, _, dock := peekRig(t)
	if got := titles(*dock); len(got) != 2 || got[0] != "Peek at Ada Lovelace" || got[1] != "Peek at Engines" {
		t.Fatalf("menu %q", got)
	}
	r.incoming(grace, grace, "hi", 1) // the newest comes first
	got := titles(*dock)
	if len(got) != 3 || got[0] != "Peek at Grace Hopper" {
		t.Fatalf("menu %q", got)
	}
	if !r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvCommand, ID: uint64(cmdDockPeek)}) {
		t.Fatal("the command was not handled")
	}
	if string(r.a.Model.Chats.At(r.s.FloatPeek.Ref).ID()) != grace {
		t.Errorf("peek %+v", r.s.FloatPeek)
	}
	r.s.CloseFloatPeek()
	r.s.Settings.NotificationText = false
	r.frame()
	if got := titles(*dock); len(got) != 1 || got[0] != "Peek at the newest unread chat" {
		t.Errorf("hidden content: menu %q", got)
	}
}
