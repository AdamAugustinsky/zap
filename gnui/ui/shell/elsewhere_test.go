package shell

import (
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// AC-001: the account button's dot counts the unread chats of the accounts
// not on screen, again when their chats change.
func TestUnreadElsewhere(t *testing.T) {
	s := state.New()
	s.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	chats := func(f *fake.Core, unread uint32) {
		f.Emit(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
			{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada"), LastActivity: 5, Unread: unread},
			{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace"), LastActivity: 4, Unread: unread},
		}}})
		s.Pump(0)
	}
	f1, f2 := fake.New(nil), fake.New(nil)
	s.AddAccount("1", nil, f1)
	s.AddAccount("2", nil, f2)
	chats(f1, 3)
	chats(f2, 0)
	sh := &Shell{S: s}
	if n := sh.unreadElsewhere(); n != 0 {
		t.Errorf("%d elsewhere with the other account read", n)
	}
	chats(f2, 1)
	if n := sh.unreadElsewhere(); n != 2 {
		t.Errorf("%d elsewhere, want 2", n)
	}
	s.SwitchAccount(1)
	if n := sh.unreadElsewhere(); n != 2 {
		t.Errorf("%d elsewhere from the second account, want 2", n)
	}
}

// The rail's Archived and Locked counts are the account's on screen, also
// when another account's list is at the same generation.
func TestRailCountsFollowTheAccount(t *testing.T) {
	s := state.New()
	f1, f2 := fake.New(nil), fake.New(nil)
	s.AddAccount("1", nil, f1)
	s.AddAccount("2", nil, f2)
	f1.Emit(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
		{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada"), LastActivity: 5, Locked: true},
		{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace"), LastActivity: 4, Archived: true, Unread: 2},
	}}})
	f2.Emit(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
		{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada"), LastActivity: 5},
		{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace"), LastActivity: 4},
	}}})
	s.Pump(0)
	a1, a2 := s.Accounts[0], s.Accounts[1]
	if a1.Model.Chats.Gen() != a2.Model.Chats.Gen() {
		t.Fatalf("generations differ: %d, %d", a1.Model.Chats.Gen(), a2.Model.Chats.Gen())
	}
	sh := &Shell{S: s, builds: 2}
	sh.count(a1)
	if sh.locked != 1 || sh.archived != 1 || sh.archivedUnread != 1 {
		t.Fatalf("first account: locked %d, archived %d (%d unread)", sh.locked, sh.archived, sh.archivedUnread)
	}
	s.SwitchAccount(1)
	sh.count(a2)
	if sh.locked != 0 || sh.archived != 0 || sh.archivedUnread != 0 {
		t.Errorf("second account shows the first's counts: locked %d, archived %d (%d unread)", sh.locked, sh.archived, sh.archivedUnread)
	}
}
