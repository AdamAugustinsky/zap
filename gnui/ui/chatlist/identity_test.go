package chatlist

import (
	"strings"
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// treeHas reports whether any node's name or value holds sub.
func (r *rig) treeHas(sub string) bool {
	t := r.Tree()
	for _, n := range t.Nodes {
		if strings.Contains(string(t.Text(n.Name)), sub) || strings.Contains(string(t.Text(n.Value)), sub) {
			return true
		}
	}
	return false
}

// addAccount links another account on a fake core of its own with chats cs.
func (r *rig) addAccount(id string, cs ...abi.Chat) (*state.Account, *fake.Core) {
	f := fake.New(nil)
	a := r.s.AddAccount(id, nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: cs} }))
	r.Frames(3)
	return a, f
}

// A switch to an account whose chat, contacts and pictures are at the same
// generations as the one left shows its own rows: the rows' cache is the
// account's, not the ref's alone.
func TestSwitchWithEqualGenerationsShowsTheOtherAccountsRows(t *testing.T) {
	r := newRig(t)
	ada := chat("1", "Ada", 30)
	ada.Unread = 3
	ada.HasLast, ada.Last = true, abi.LastMessage{Summary: []byte("the secret of account one")}
	r.chats(ada)
	r.f.Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
		e.Avatar = abi.EventAvatar{ID: jid("1"), Path: []byte("/tmp/one/ada.jpg"), HasPath: true}
	}))
	r.Frames(3)
	if !r.has(ui.RoleListItem, "Ada") || r.l.info[0].avatar == "" {
		t.Fatalf("account one's row is not up: %v, avatar %q", r.rows(), r.l.info[0].avatar)
	}
	b, fb := r.addAccount("2", chat("1", "Bob", 30))
	fb.Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
		e.Avatar = abi.EventAvatar{ID: jid("7"), Path: []byte("/tmp/two/other.jpg"), HasPath: true}
	}))
	r.Frames(2)
	a := r.acct()
	ca, _ := a.Model.Chats.Get(1)
	cb, _ := b.Model.Chats.Get(1)
	if ca.Gen() != cb.Gen() || a.Model.Contacts.Gen() != b.Model.Contacts.Gen() || a.Model.Avatars.Gen() != b.Model.Avatars.Gen() {
		t.Fatalf("the generations differ; the test proves nothing: chat %d/%d contacts %d/%d avatars %d/%d",
			ca.Gen(), cb.Gen(), a.Model.Contacts.Gen(), b.Model.Contacts.Gen(), a.Model.Avatars.Gen(), b.Model.Avatars.Gen())
	}
	r.s.SwitchAccount(b.Index)
	r.Frames(3)
	if r.has(ui.RoleListItem, "Ada") || !r.has(ui.RoleListItem, "Bob") {
		t.Errorf("account two lists %v, want Bob", r.rows())
	}
	if r.treeHas("secret of account one") || r.treeHas("unread") {
		t.Errorf("account one's preview or unread state shows on account two")
	}
	if r.l.info[0].avatar != "" {
		t.Errorf("account two's row keeps account one's picture %q", r.l.info[0].avatar)
	}
}

// With accounts one, two and three, removing one moves three to two's
// index; three's starred list is its own, not two's.
func TestRemovingAnEarlierAccountKeepsAnswersWithTheirAccount(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5))
	starred := func(f *fake.Core, text string) {
		f.Emit(ev(abi.EventTagStarredList, func(e *abi.Event) {
			e.StarredList = abi.EventStarredList{Batch: 1, Last: true, Starred: []abi.Starred{{StarredAt: 9, Message: abi.Message{
				Chat: jid("1"), ID: []byte("m1"), Timestamp: 100,
				Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte(text)}}}}}}
		}))
	}
	two, f2 := r.addAccount("2", chat("1", "Ada", 5))
	three, f3 := r.addAccount("3", chat("1", "Ada", 5))
	two.Mode, three.Mode = state.ListStarred, state.ListStarred
	starred(f2, "two's starred note")
	starred(f3, "three's starred note")
	r.s.SwitchAccount(two.Index)
	r.Frames(3)
	if !r.treeHas("two's starred note") {
		t.Fatalf("account two's starred list is not up")
	}
	r.s.SwitchAccount(0)
	r.Frames(2)
	r.s.RemoveAccount(0)
	r.f.Emit(ev(abi.EventTagAccountRemoved, func(e *abi.Event) {}))
	r.Frames(3)
	if len(r.s.Accounts) != 2 || three.Index != 1 {
		t.Fatalf("account one was not removed: %d accounts, three at %d", len(r.s.Accounts), three.Index)
	}
	r.s.SwitchAccount(three.Index)
	r.Frames(3)
	if r.treeHas("two's starred note") || !r.treeHas("three's starred note") {
		t.Errorf("account three shows another account's starred list")
	}
	for _, m := range r.l.mirrors {
		if m.serial != two.Serial && m.serial != three.Serial {
			t.Errorf("a removed account's mirror is kept (serial %d)", m.serial)
		}
	}
}
