package shell

import (
	"strings"
	"testing"

	"gonativeui/app"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// LK-006: closing the main window hides the locked chats again
// (zf:src/app.rs window_gone): the window that opens next shows neither
// the folder nor its chat, nor the prompt that opens it.
func TestClosingTheWindowClosesTheLockedFolder(t *testing.T) {
	s := state.New()
	f := fake.New(nil)
	link := &abi.Event{Tag: abi.EventTagLink}
	link.Link.Status.Tag = abi.LinkStatusTagConnected
	f.Emit(link,
		&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
			{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 5},
			{ID: []byte("4@s.whatsapp.net"), Name: []byte("Vault Keeper"), LastActivity: 9, Locked: true},
		}}})
	sh, err := New(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := s.AddAccount("1", nil, f)
	r := widgettest.Open(t, Config(s), 1100, 720, 1, sh)
	r.Frames(3)
	vault := a.Model.Chats.Find([]byte("4@s.whatsapp.net"))
	a.Mode = state.ListLocked
	a.OpenChat(vault)
	s.Touch()
	r.Frames(3)
	if a.Open != vault || !strings.Contains(widgettest.Dump(r.Tree()), `Label "Conversation"`) {
		t.Fatalf("the locked chat did not open in its folder:\n%s", widgettest.Dump(r.Tree()))
	}
	// The main window closes, by the app's own route.
	h := NewApp(sh)
	h.main.Store(&app.Win{})
	h.Event(nil, &app.Event{Kind: app.EvWindowClosed})
	// and a new one opens
	r = widgettest.Open(t, Config(s), 1100, 720, 1, sh)
	r.Frames(3)
	if a.Mode == state.ListLocked || a.Open == vault {
		t.Errorf("the reopened window is in the locked folder: mode %v, open %v", a.Mode, a.Open)
	}
	if d := widgettest.Dump(r.Tree()); strings.Contains(d, `Label "Conversation"`) || strings.Contains(d, `"Locked" [selected]`) {
		t.Errorf("the locked chat or folder shows in the reopened window:\n%s", d)
	}

	// The prompt for the folder's code closes with the window too.
	s.OpenDialog(state.Dialog{Kind: state.DialogUnlockLockedChats})
	h.Event(nil, &app.Event{Kind: app.EvWindowClosed})
	if s.Dialog.Kind == state.DialogUnlockLockedChats {
		t.Error("the unlock prompt outlived the window")
	}
}
