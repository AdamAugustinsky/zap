package conversation

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

func namedReply(chat, person, hint string) abi.Message {
	m := msg(chat, "named-reply", base0, false, "reply body")
	m.Sender, m.SenderName, m.HasSenderName = []byte(person), []byte(hint), hint != ""
	m.HasQuoted = true
	m.Quoted = abi.Quoted{ID: []byte("original"), Sender: []byte(person), SenderName: []byte(hint),
		HasSenderName: hint != "", Summary: []byte("quoted body")}
	return m
}

func TestSenderAndQuoteUsePersonNamePrecedence(t *testing.T) {
	for _, tc := range []struct{ name, saved, profile, hint, fallback, want string }{
		{"saved", "Address book", "Profile", "Message hint", "Chat name", "Address book"},
		{"profile", "", "Profile", "Message hint", "Chat name", "~Profile"},
		{"missing profile", "", "", "Message hint", "Chat name", "~Message hint"},
		{"chat fallback", "", "", "", "Chat name", "Chat name"},
		{"number fallback", "", "", "", "+1", "+1"},
	} {
		for _, chat := range []string{ada, group} {
			t.Run(tc.name+"/"+chat, func(t *testing.T) {
				r := newRig(t)
				r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
					e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte(tc.fallback)}
				}), ev(abi.EventTagContacts, func(e *abi.Event) {
					e.Contacts = abi.EventContacts{Last: true, Contacts: []abi.Contact{
						{ID: []byte("9@s.whatsapp.net"), PushName: []byte("Unrelated profile"), HasPushName: true},
						{ID: []byte(ada), FullName: []byte(tc.saved), HasFullName: tc.saved != "", PushName: []byte(tc.profile), HasPushName: tc.profile != ""},
					}}
				}))
				r.Frames(2)
				r.open(chat, namedReply(chat, ada, tc.hint))
				r.must(ui.RoleGroup, bubble(tc.want, base0))
				if chat == group {
					r.must(ui.RoleLabel, tc.want)
				}
				r.must(ui.RoleButton, tc.want+": quoted body")
				if tc.name == "missing profile" {
					if n := testing.AllocsPerRun(20, r.Frame); n != 0 {
						t.Errorf("steady frames naming a message-only profile: %v allocations", n)
					}
					r.f.Emit(&abi.Event{Tag: abi.EventTagMessageUpdated, MessageUpdated: abi.EventMessageUpdated{
						Message: namedReply(chat, ada, "Changed hint"),
					}})
					r.Frames(2)
					r.must(ui.RoleGroup, bubble("~Changed hint", base0))
					r.must(ui.RoleButton, "~Changed hint: quoted body")
					r.f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
						e.Contacts = abi.EventContacts{Last: true, Contacts: []abi.Contact{{ID: []byte(ada), PushName: []byte("New profile"), HasPushName: true}}}
					}))
					r.Frames(2)
					r.must(ui.RoleGroup, bubble("~New profile", base0))
					r.must(ui.RoleButton, "~New profile: quoted body")
				}
			})
		}
	}
}

func TestSenderAndQuoteNamesStayWithTheirAccount(t *testing.T) {
	r := newRig(t)
	contact := func(name string) *abi.Event {
		return &abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{Last: true,
			Contacts: []abi.Contact{{ID: []byte(ada), FullName: []byte(name), HasFullName: true}}}}
	}
	r.f.Emit(contact("First account"))
	r.Frames(2)
	m := namedReply(group, ada, "Public hint")
	r.open(group, m)
	second := r.s.AddAccount("2", nil, fake.New(nil))
	second.Model.Event(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Last: true,
		Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada Lovelace")}, {ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup}}}})
	second.Model.Event(contact("Second account"))
	second.OpenChat(second.Model.Chats.Find([]byte(group)))
	second.Model.Event(&abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{Chat: []byte(group), Last: true, Messages: []abi.Message{m}}})
	check := func(a *state.Account, want string) {
		t.Helper()
		ch, _ := a.OpenChatView()
		if got := r.scr.senderStr(a, a.Model.Conversation(ch.ID()).At(0)); got != want {
			t.Errorf("account %d sender %q, want %q", a.Serial, got, want)
		}
	}
	r.s.SwitchAccount(1)
	r.Frames(3)
	r.must(ui.RoleGroup, bubble("Second account", base0))
	r.must(ui.RoleButton, "Second account: quoted body")
	r.f.Emit(contact("First renamed while hidden"))
	r.Frames(3)
	check(r.a, "First renamed while hidden")
	check(second, "Second account")
	r.must(ui.RoleGroup, bubble("Second account", base0))
	r.must(ui.RoleButton, "Second account: quoted body")
	r.s.SwitchAccount(0)
	r.Frames(3)
	r.must(ui.RoleGroup, bubble("First renamed while hidden", base0))
	r.must(ui.RoleButton, "First renamed while hidden: quoted body")
}

// A quote that carries no profile name is not named with the last one the
// decoder's scratch held.
func TestAQuoteWithoutAHintKeepsNoOtherName(t *testing.T) {
	r := newRig(t)
	hinted := namedReply(group, "2@s.whatsapp.net", "Stale")
	bare := namedReply(group, ada, "")
	bare.ID, bare.Timestamp = []byte("bare-reply"), base0+60
	r.open(group, hinted, bare)
	r.must(ui.RoleButton, "~Stale: quoted body")
	r.must(ui.RoleButton, "Ada Lovelace: quoted body")
}
