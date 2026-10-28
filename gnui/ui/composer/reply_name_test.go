package composer

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
)

func TestReplyStripUsesPersonNamePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, saved, profile, hint, want string
	}{
		{"message hint", "", "", "Public hint", "~Public hint"},
		{"saved name", "Saved Ada", "Profile Ada", "Public hint", "Saved Ada"},
		{"contact profile", "", "Profile Ada", "Public hint", "~Profile Ada"},
		{"missing optional fields", "", "", "", "Ada Lovelace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
				e.Contacts.Contacts = []abi.Contact{{ID: []byte(ada), FullName: []byte(tc.saved), HasFullName: tc.saved != "",
					PushName: []byte(tc.profile), HasPushName: tc.profile != ""}}
			}))
			r.open(group)
			m := textMsg("reply", ada, false, 100, "Synthetic reply")
			m.SenderName, m.HasSenderName = []byte(tc.hint), tc.hint != ""
			r.messages(group, m)
			r.s.Panes.Reply.Add([]byte("reply"))
			r.Frames(10)
			if !r.has(ui.RoleLabel, "Replying to "+tc.want) {
				t.Fatalf("reply label = %q, want %q", r.scr.replyWho, tc.want)
			}
			if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
				t.Errorf("steady reply strip: %v allocations per frame", n)
			}
		})
	}
}

func TestReplyStripNamesStayWithTheirAccount(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.messages(group, textMsg("reply", ada, false, 100, "First account"))
	r.s.Panes.Reply.Add([]byte("reply"))
	r.Frames(3)
	f := fake.New(nil)
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: defaultChats()}
	}))
	b := r.s.AddAccount("2", nil, f)
	r.Frames(2)
	b.OpenChat(b.Model.Chats.Find([]byte(group)))
	r.s.SwitchAccount(1)
	r.Frames(3)
	m := textMsg("reply", ada, false, 100, "Second account")
	m.SenderName, m.HasSenderName = []byte("Other hint"), true
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) { e.Messages.Chat, e.Messages.Messages = []byte(group), []abi.Message{m} }))
	r.Frames(2)
	r.s.Panes.Reply.Add([]byte("reply"))
	r.Frames(10)
	if !r.has(ui.RoleLabel, "Replying to ~Other hint") || r.has(ui.RoleLabel, "Replying to Ada Lovelace") {
		t.Fatalf("second account reply label = %q", r.scr.replyWho)
	}
}
