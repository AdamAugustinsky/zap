//go:build !gnui_release

package main

import (
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
	"zapfast/state"
)

const (
	vaultJID    = "4@s.whatsapp.net"
	vaultSecret = "the vault code is 4711"
)

// linkedWithLocked is linked with a locked chat that holds a message.
func linkedWithLocked(f *fake.Core) {
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 20},
				{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace Hopper"), LastActivity: 10},
				{ID: []byte(vaultJID), Name: []byte("Vault Keeper"), LastActivity: 30, Locked: true},
			}}
		}))
}

// openVault opens the locked folder and its locked chat, and checks that the
// protected message shows there.
func openVault(t *testing.T, r *rig, f *fake.Core) *state.Account {
	t.Helper()
	a := r.s.Accounts[0]
	a.Mode = state.ListLocked
	ref := a.Model.Chats.Find([]byte(vaultJID))
	a.OpenChat(ref)
	var m abi.Message
	m.ID, m.Chat, m.Sender, m.Timestamp = []byte("v1"), []byte(vaultJID), []byte(vaultJID), r.s.Now().Unix()-60
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(vaultSecret)
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(vaultJID), Batch: 1, Last: true, Complete: true, Messages: []abi.Message{m}}
	}))
	r.s.Touch()
	r.Frames(4)
	if a.Open != ref || !showsVault(r) {
		t.Fatalf("the locked chat does not show in its folder (open %v):\n%s", a.Open, widgettest.Dump(r.Tree()))
	}
	return a
}

func showsVault(r *rig) bool {
	d := widgettest.Dump(r.Tree())
	return strings.Contains(d, vaultSecret) || strings.Contains(d, "Vault Keeper")
}

// The locked folder's authorization ends by every way out of it, and its
// open locked chat goes with it (zf:src/app.rs close_locked_folder): the
// protected message is no longer in the window.
func TestLeavingTheLockedFolderClosesItsChat(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(t *testing.T, r *rig)
	}{
		{"the rail's Chats", func(t *testing.T, r *rig) { r.Click(ui.RoleButton, "Chats") }},
		{"Escape", func(t *testing.T, r *rig) {
			r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
			if r.s.Accounts[0].Mode == state.ListLocked {
				r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
			}
		}},
		{"⌘⇧1 (Chats)", func(t *testing.T, r *rig) {
			r.Play(widgettest.Key('1', prim|ui.ModShift), widgettest.Idle(2))
		}},
		{"⌘⇧F (Search chats)", func(t *testing.T, r *rig) {
			r.Play(widgettest.Key('F', prim|ui.ModShift), widgettest.Idle(2))
		}},
		{"the app lock and the unlock", func(t *testing.T, r *rig) {
			r.s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$c2FsdA$aGFzaA")
			r.s.Lock.Locked = true
			r.s.Touch()
			r.Frames(3)
			if showsVault(r) {
				t.Fatal("the locked chat shows behind the lock")
			}
			r.s.Lock.Locked = false
			r.s.Touch()
		}},
		{"another account and back", func(t *testing.T, r *rig) {
			f2 := fake.New(nil)
			linked(f2)
			r.s.AddAccount("2", nil, f2)
			r.Frames(3)
			r.s.SwitchAccount(1)
			r.Frames(3)
			r.s.SwitchAccount(0)
			r.s.Touch()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New(nil)
			linkedWithLocked(f)
			r := newRig(t, f)
			a := openVault(t, r, f)
			r.s.Panes.SetInfo([]byte(vaultJID))
			r.s.Touch()
			r.Frames(2)
			tc.leave(t, r)
			r.Frames(4)
			if showsVault(r) {
				t.Errorf("the protected chat still shows outside its folder:\n%s", widgettest.Dump(r.Tree()))
			}
			if a.Mode == state.ListLocked || a.Open == a.Model.Chats.Find([]byte(vaultJID)) || r.s.Panes.Pane != state.PaneNone {
				t.Errorf("mode %v, open %v, pane %v: the folder's chat or pane stays", a.Mode, a.Open, r.s.Panes.Pane)
			}
		})
	}
}

// Defence in depth: a locked chat left open outside the folder (by a way
// out not routed through CloseLockedFolder) shows nothing of itself.
func TestALockedChatOutsideItsFolderShowsNothing(t *testing.T) {
	f := fake.New(nil)
	linkedWithLocked(f)
	r := newRig(t, f)
	a := openVault(t, r, f)
	a.Mode = state.ListChats // as a stray path would leave it
	r.s.Panes.Pane = state.PaneSearch
	r.s.Touch()
	r.Frames(4)
	if _, ok := a.OpenChatView(); ok {
		t.Error("OpenChatView gives a locked chat outside its folder")
	}
	// The chat's shortcuts do not reach it: ⌘F searches the chats, ⌘E
	// archives nothing.
	r.s.Panes.Pane = state.PaneNone
	r.s.Touch()
	r.Frames(2)
	r.Play(widgettest.Key('E', prim), widgettest.Idle(2))
	for _, tag := range f.SentTags() {
		if tag == abi.CommandTagSetArchived {
			t.Error("⌘E archived the hidden locked chat")
		}
	}
	r.Play(widgettest.Key('F', prim), widgettest.Idle(3))
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleSearchField {
		t.Errorf("⌘F did not go to the chat list's search (focused %v)", n.Role)
	}
	if showsVault(r) {
		t.Errorf("the transcript or header renders a locked chat outside its folder:\n%s", widgettest.Dump(r.Tree()))
	}
}

// Leaving the folder empties the chat list's search, which in the folder
// searches the locked chats' titles (upstream's search.clear()).
func TestLeavingTheLockedFolderClearsTheSearch(t *testing.T) {
	f := fake.New(nil)
	linkedWithLocked(f)
	r := newRig(t, f)
	openVault(t, r, f)
	r.Play(widgettest.Click(ui.RoleSearchField, "Search"), widgettest.Type("Vault"), widgettest.Idle(3))
	if v := r.Tree().Text(r.Must(ui.RoleSearchField, "Search").Value); string(v) != "Vault" {
		t.Fatalf("the search holds %q", v)
	}
	r.Click(ui.RoleButton, "Chats")
	r.Frames(3)
	if v := r.Tree().Text(r.Must(ui.RoleSearchField, "Search").Value); len(v) != 0 {
		t.Errorf("the folder's search %q stays after leaving it", v)
	}
}

// A chat locked from the phone while it is open outside the folder closes,
// as upstream's hide_locked_chat: its draft is kept, its panel and its
// messages go. A chat unlocked meanwhile changes nothing.
func TestAChatLockedFromThePhoneWhileOpenCloses(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	a := r.s.Accounts[0]
	r.open(adaJID)
	var m abi.Message
	m.ID, m.Chat, m.Sender, m.Timestamp = []byte("a1"), []byte(adaJID), []byte(adaJID), r.s.Now().Unix()-60
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(vaultSecret)
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(adaJID), Batch: 1, Last: true, Complete: true, Messages: []abi.Message{m}}
	}))
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Type("half a thought"), widgettest.Idle(2))
	r.s.Panes.SetInfo([]byte(adaJID))
	r.s.Touch()
	r.Frames(3)
	ada := a.Model.Chats.Find([]byte(adaJID))
	if a.Open != ada || !strings.Contains(widgettest.Dump(r.Tree()), vaultSecret) {
		t.Fatal("Ada's chat does not show")
	}
	updated := func(locked bool, jid, name string, act int64) {
		f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
			e.ChatUpdated.Chat = abi.Chat{ID: []byte(jid), Name: []byte(name), LastActivity: act, Locked: locked}
		}))
		r.Frames(4)
	}
	// Grace, not open, is unlocked: nothing changes.
	updated(false, "2@s.whatsapp.net", "Grace Hopper", 10)
	if a.Open != ada || r.s.Panes.Pane != state.PaneInfo {
		t.Fatalf("an unlocked chat moved the open one: open %v, pane %v", a.Open, r.s.Panes.Pane)
	}
	// The open chat updated unlocked (it was not locked, or was unlocked on
	// the phone): it stays.
	updated(false, adaJID, "Ada Lovelace", 20)
	if a.Open != ada || r.s.Panes.Pane != state.PaneInfo {
		t.Fatalf("an unlocked update closed the open chat: open %v, pane %v", a.Open, r.s.Panes.Pane)
	}
	updated(true, adaJID, "Ada Lovelace", 20)
	if a.Open != 0 || r.s.Panes.Pane != state.PaneNone {
		t.Errorf("the chat locked from the phone stays open: open %v, pane %v", a.Open, r.s.Panes.Pane)
	}
	if strings.Contains(widgettest.Dump(r.Tree()), vaultSecret) {
		t.Error("its messages still show")
	}
	if d := string(a.Model.Draft([]byte(adaJID))); d != "half a thought" {
		t.Errorf("its draft is %q, want the text left in the composer", d)
	}
}

// Inside the open folder a locked chat's update leaves it open.
func TestALockedChatUpdatedInItsFolderStaysOpen(t *testing.T) {
	f := fake.New(nil)
	linkedWithLocked(f)
	r := newRig(t, f)
	a := openVault(t, r, f)
	ref := a.Open
	f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(vaultJID), Name: []byte("Vault Keeper"), LastActivity: 40, Locked: true}
	}))
	r.Frames(4)
	if a.Open != ref || !showsVault(r) {
		t.Error("the folder's open chat closed on its own update")
	}
}

// pollIn puts a poll message in chat jid.
func pollIn(f *fake.Core, r *rig, jid string) {
	m := abi.Message{ID: []byte("p1"), Chat: []byte(jid), Sender: []byte(jid), Timestamp: r.s.Now().Unix() - 30,
		Content: abi.Content{Tag: abi.ContentTagPoll}}
	m.Content.Poll = abi.ContentPoll{Question: []byte("Where is the vault key?"), Options: [][]byte{[]byte("Under the mat"), []byte("In the safe")},
		State: abi.PollState{Counts: []uint64{1, 0}, Votes: []abi.PollVoter{{ID: []byte(jid), Name: []byte("Keeper"), Timestamp: 5, Choices: []uint64{0}}}}}
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(jid), Batch: 1, Last: true, Complete: true, Messages: []abi.Message{m}}
	}))
}

// A message dialog of a chat that may no longer show goes with it
// (zf:src/app.rs leave_chat): the poll results' question, options and voters
// leave the window when the chat is locked from the phone, and when the app
// lock closes the folder it was open in.
func TestAPollResultsDialogGoesWithItsLockedChat(t *testing.T) {
	const question = "Where is the vault key?"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, r *rig, f *fake.Core) model.ChatRef
		lock  func(t *testing.T, r *rig, f *fake.Core)
	}{
		{"locked from the phone", func(t *testing.T, r *rig, f *fake.Core) model.ChatRef {
			r.open(adaJID)
			pollIn(f, r, adaJID)
			return r.s.Accounts[0].Open
		}, func(t *testing.T, r *rig, f *fake.Core) {
			f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(adaJID), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true}
			}))
		}},
		{"the app lock and the unlock", func(t *testing.T, r *rig, f *fake.Core) model.ChatRef {
			a := openVault(t, r, f)
			pollIn(f, r, vaultJID)
			return a.Open
		}, func(t *testing.T, r *rig, f *fake.Core) {
			r.s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$c2FsdA$aGFzaA")
			r.s.Lock.Locked = true
			r.s.Touch()
			r.Frames(3)
			if strings.Contains(widgettest.Dump(r.Tree()), question) {
				t.Error("the poll's question shows behind the lock")
			}
			r.s.Lock.Locked = false
			r.s.Touch()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New(nil)
			linkedWithLocked(f)
			r := newRig(t, f)
			ref := tc.setup(t, r, f)
			var ids model.IDList
			ids.Add([]byte("p1"))
			r.s.OpenDialog(state.Dialog{Kind: state.DialogPollResults, Chat: ref, IDs: ids})
			r.Frames(4)
			if _, ok := r.Find(ui.RoleLabel, "Keeper"); !ok {
				t.Fatalf("the poll results do not show:\n%s", widgettest.Dump(r.Tree()))
			}
			tc.lock(t, r, f)
			r.Frames(4)
			if d := widgettest.Dump(r.Tree()); strings.Contains(d, question) || strings.Contains(d, "Under the mat") || strings.Contains(d, "Keeper") {
				t.Errorf("the poll results outlive their chat:\n%s", d)
			}
			if r.s.Dialog.Open() {
				t.Errorf("dialog %v stays open", r.s.Dialog.Kind)
			}
		})
	}
}
