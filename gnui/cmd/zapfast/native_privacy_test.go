package main

import (
	"slices"
	"testing"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// Use the full shell: unlike a dialog-only host it does not build Dialogs
// behind the lock or login. The native ticket, not the semantic tree, is
// the privacy boundary. No system sheet or account is used.
func TestNativeConfirmationsWithdrawAtPrivacyTransitions(t *testing.T) {
	for _, q := range []struct {
		name string
		kind state.DialogKind
	}{{"delete", state.DialogConfirmDeleteChat}, {"clear", state.DialogConfirmClearChat}} {
		for _, transition := range []string{"app lock", "remote lock", "snapshot lock", "logged out", "unlinked", "removed"} {
			t.Run(transition+"/"+q.name, func(t *testing.T) {
				f := fake.New(nil)
				linked(f)
				r := newRig(t, f)
				a := r.s.Accounts[0]
				fd := r.FakeDialogs()
				ref := a.Model.Chats.Find([]byte("1@s.whatsapp.net"))
				r.s.OpenDialog(state.Dialog{Kind: q.kind, Chat: ref})
				r.Frames(3)
				open := fd.Open()
				if len(open) != 1 {
					t.Fatalf("expected native confirmation: %+v", open)
				}
				ticket := open[0].Ticket
				switch transition {
				case "app lock":
					r.s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$c2FsdA$aGFzaA")
					r.s.Lock.Locked = true
					r.s.Touch()
					r.Frames(3) // real guard -> State.Locked; Dialogs is not built
				case "remote lock", "snapshot lock":
					c := abi.Chat{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 20, Locked: true}
					if transition == "remote lock" {
						f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = c }))
					} else {
						f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
							e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{c}}
						}))
					}
					r.s.Pump(0) // must cancel before any subsequent Build
				case "logged out", "unlinked":
					status := abi.LinkStatusTagLoggedOut
					if transition == "unlinked" {
						status = abi.LinkStatusTagUnlinked
					}
					f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = status }))
					r.s.Pump(0)
				case "removed":
					f.Emit(&abi.Event{Tag: abi.EventTagAccountRemoved})
					r.s.Pump(0)
				}
				if len(fd.Open()) != 0 || !slices.Contains(fd.Withdrawn, ticket) {
					t.Fatalf("private native ticket %v survives transition: open %+v, withdrawn %v", ticket, fd.Open(), fd.Withdrawn)
				}
				r.Frames(3)
				if len(fd.Open()) != 0 {
					t.Fatal("confirmation reappeared while inaccessible")
				}
				if transition == "app lock" {
					r.s.Lock.Locked = false
					r.s.Touch()
					r.Frames(3)
					if open = fd.Open(); len(open) != 1 || open[0].Ticket == ticket {
						t.Fatal("unlock must ask afresh, not reuse the cancelled ticket")
					}
				}
				for _, cmd := range f.Sent() {
					if cmd.Tag == abi.CommandTagDeleteChat || cmd.Tag == abi.CommandTagClearChat {
						t.Fatal("privacy transition confirmed a destructive action")
					}
				}
			})
		}
	}
}

func TestNativeConfirmationSurvivesUnrelatedPrivacyEvents(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	a := r.s.Accounts[0]
	other := fake.New(nil)
	linked(other)
	r.s.AddAccount("2", nil, other)
	r.s.Pump(0)
	fd := r.FakeDialogs()
	ref := a.Model.Chats.Find([]byte("1@s.whatsapp.net"))
	r.s.OpenDialog(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ref})
	r.Frames(3)
	if len(fd.Open()) != 1 {
		t.Fatal("no native confirmation")
	}
	ticket := fd.Open()[0].Ticket
	other.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
	f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace Hopper"), Locked: true}
	}))
	r.s.Pump(0)
	// A lock inside the authenticated folder still allows the dialog.
	a.Mode = state.ListLocked
	f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), Locked: true}
	}))
	r.s.Pump(0)
	r.Frames(3)
	if open := fd.Open(); len(open) != 1 || open[0].Ticket != ticket || len(fd.Withdrawn) != 0 {
		t.Fatal("an unrelated event or authorized chat cancelled the confirmation")
	}
	a.CloseLockedFolder()
	r.Frames(3)
	if len(fd.Open()) != 0 || !slices.Contains(fd.Withdrawn, ticket) {
		t.Fatal("leaving the folder kept its native confirmation")
	}
}
