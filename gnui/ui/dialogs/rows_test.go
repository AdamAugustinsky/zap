package dialogs

import (
	"strconv"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

func manyChats(n int) []abi.Chat {
	var out []abi.Chat
	for i := 1; i <= n; i++ {
		out = append(out, chat(strconv.Itoa(i)+"@s.whatsapp.net", "Chat "+strconv.Itoa(i), int64(100-i)))
	}
	return out
}

func oneID(id string) *model.IDList {
	var l model.IDList
	l.Add([]byte(id))
	return &l
}

func (r *rig) toast() string {
	if n := len(r.s.Toasts); n > 0 {
		return string(r.s.Toasts[n-1].Text)
	}
	return ""
}

// DL-001: the forward picker lists writable chats and stops at five, saying so.
func TestForwardStopsAtTheLimitAndSaysSo(t *testing.T) {
	r := newRig(t, linkedEvent(), chatsEvent(manyChats(7)...))
	r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1"))
	for i := 1; i <= 5; i++ {
		r.click(ui.RoleCheckbox, "Chat "+strconv.Itoa(i))
	}
	if !r.has(ui.RoleLabel, "You can forward to up to 5 chats") || !r.has(ui.RoleLabel, "5 recipients selected") {
		t.Fatalf("no limit notice\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleCheckbox, "Chat 6")
	if !r.has(ui.RoleLabel, "5 recipients selected") {
		t.Error("a sixth chat was chosen")
	}
}

// DL-001: chats that cannot be written to are not offered, and none says so.
func TestForwardWithNoWritableChats(t *testing.T) {
	ro := chat("1@s.whatsapp.net", "Ada", 3)
	ro.ReadOnly = true
	gone := chat("2@s.whatsapp.net", "Grace", 2)
	gone.Left = true
	r := newRig(t, linkedEvent(), chatsEvent(ro, gone))
	r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1"))
	if !r.has(ui.RoleLabel, "No writable chats found") || r.has(ui.RoleCheckbox, "Ada") || r.has(ui.RoleCheckbox, "Grace") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
}

// DL-001: the search narrows the picker.
func TestForwardSearch(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1"))
	r.typeInto("Search chats", "grace")
	if !r.has(ui.RoleCheckbox, "Grace Hopper") || r.has(ui.RoleCheckbox, "Ada Lovelace") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
}

// DL-002: a message forwarded many times goes to one chat; the picker says so
// and a second choice replaces the first.
func TestForwardOneChatOnlyWhenTheCoreSaysSo(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.openForward(ada, oneID("m1"))
	r.f.Emit(ev(abi.EventTagForwardLimit, func(e *abi.Event) {
		e.ForwardLimit = abi.EventForwardLimit{FromChat: []byte("1@s.whatsapp.net"), Messages: [][]byte{[]byte("m1")}, Limit: 1}
	}))
	r.Frames(3)
	if !r.has(ui.RoleLabel, "Messages forwarded many times can be sent to one chat at a time") {
		t.Fatalf("no notice\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleCheckbox, "Grace Hopper")
	r.click(ui.RoleCheckbox, "Analytical Engine")
	if !r.has(ui.RoleLabel, "1 recipient selected") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Forward")
	got := r.sent(abi.CommandTagForward)
	if len(got) != 1 || len(got[0].Forward.ToChats) != 1 || string(got[0].Forward.ToChats[0]) != "12345@g.us" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-003: several chats are reviewed first; Back keeps the choice; Forward
// sends one batch.
func TestForwardReviewsSeveralRecipients(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1"))
	r.click(ui.RoleCheckbox, "Grace Hopper")
	r.click(ui.RoleCheckbox, "Analytical Engine")
	r.click(ui.RoleButton, "Review recipients")
	if !r.has(ui.RoleLabel, "Forward to these chats?") || !r.has(ui.RoleLabel, "Grace Hopper") || !r.has(ui.RoleLabel, "Analytical Engine") {
		t.Fatalf("no review\n%s", widgettest.Dump(r.Tree()))
	}
	if len(r.sent(abi.CommandTagForward)) != 0 {
		t.Fatal("sent before the review was confirmed")
	}
	r.click(ui.RoleButton, "Back")
	if !r.has(ui.RoleLabel, "2 recipients selected") {
		t.Fatalf("Back lost the choice\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Review recipients")
	r.click(ui.RoleButton, "Forward")
	got := r.sent(abi.CommandTagForward)
	if len(got) != 1 || len(got[0].Forward.ToChats) != 2 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-004 (the dialog's half): a destination that became read-only during the
// review sends nothing and returns to the choice.
func TestForwardReturnsToChoosingWhenADestinationClosed(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1"))
	r.click(ui.RoleCheckbox, "Grace Hopper")
	r.click(ui.RoleCheckbox, "Analytical Engine")
	r.click(ui.RoleButton, "Review recipients")
	grace := chat("2@s.whatsapp.net", "Grace Hopper", 10)
	grace.ReadOnly = true
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = grace }))
	r.Frames(2)
	r.click(ui.RoleButton, "Forward")
	if len(r.sent(abi.CommandTagForward)) != 0 {
		t.Fatalf("part of the batch was sent: %v", r.f.SentTags())
	}
	if r.toast() != "A selected chat is no longer writable" || !r.s.Dialog.Open() || !r.has(ui.RoleSearchField, "Search chats") {
		t.Fatalf("toast %q, open %v\n%s", r.toast(), r.s.Dialog.Open(), widgettest.Dump(r.Tree()))
	}
}

// DL-005: New chat offers Message yourself, Add contact and a contact search.
func TestNewChatOffersYourselfAndContacts(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewChat})
	if !r.has(ui.RoleButton, "Add contact") || !r.has(ui.RoleSearchField, "Search contacts") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.typeInto("Search contacts", "zzzz")
	if !r.has(ui.RoleLabel, "No matching contacts") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Add contact")
	if r.s.Dialog.Kind != state.DialogNewContact {
		t.Errorf("Add contact opened %v", r.s.Dialog.Kind)
	}
}

// DL-005: choosing a person opens their chat.
func TestNewChatOpensTheChosenChat(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewChat})
	r.typeInto("Search contacts", "grace")
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper"))
	r.Frames(3)
	if r.s.Dialog.Open() || r.a.Open != r.ref("2@s.whatsapp.net") {
		t.Fatalf("open %v chat %v\n%s", r.s.Dialog.Open(), r.a.Open, widgettest.Dump(r.Tree()))
	}
}

// DL-006: a number is formatted with its country code; anything else is
// refused with the reason; the phone checkbox needs a name and is remembered.
func TestNewContactNumberAndPhoneChoice(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "555-ABC")
	if !r.has(ui.RoleLabel, invalidNumber) {
		t.Fatalf("no reason\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Phone number"), widgettest.Key('A', ui.ModSuper), widgettest.Type("+1 (555) 123-4567"))
	r.Frames(2)
	if r.has(ui.RoleLabel, invalidNumber) {
		t.Error("a bracketed number was refused")
	}
	if n, ok := r.Find(ui.RoleCheckbox, "Also save to your phone's contacts"); !ok || n.State&ui.SemDisabled == 0 {
		t.Errorf("the phone choice is open without a name: %v %v", n.State, ok)
	}
	r.typeInto("First name", "Ada")
	before := r.a.Settings.SaveContactsToPhone
	r.click(ui.RoleCheckbox, "Also save to your phone's contacts")
	r.click(ui.RoleButton, "Save contact")
	if got := r.sent(abi.CommandTagNewContact); len(got) != 1 || got[0].NewContact.ToPhone == before {
		t.Fatalf("the choice did not reach the core: %v", r.f.SentTags())
	}
	if r.a.Settings.SaveContactsToPhone == before {
		t.Error("the choice is not remembered for the next contact")
	}
}

// DL-007: Message checks the number first, and says why when it fails.
func TestNewContactChecksTheNumber(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "+442079460958")
	r.click(ui.RoleButton, "Message")
	if !r.has(ui.RoleLabel, "Checking the number…") {
		t.Fatalf("no checking line\n%s", widgettest.Dump(r.Tree()))
	}
	req := r.sent(abi.CommandTagNewContact)[0].NewContact.Request
	r.f.Emit(ev(abi.EventTagContactFailed, func(e *abi.Event) {
		e.ContactFailed = abi.EventContactFailed{Request: req, Error: []byte("This number is not on WhatsApp")}
	}))
	r.Frames(3)
	if r.toast() != "This number is not on WhatsApp" || !r.s.Dialog.Open() || r.has(ui.RoleLabel, "Checking the number…") {
		t.Fatalf("toast %q open %v\n%s", r.toast(), r.s.Dialog.Open(), widgettest.Dump(r.Tree()))
	}
	// Another try is allowed.
	r.click(ui.RoleButton, "Message")
	if len(r.sent(abi.CommandTagNewContact)) != 2 {
		t.Errorf("no second try: %v", r.f.SentTags())
	}
}

// DL-008: Save contact with a name sends the composed name.
func TestSaveContactSendsTheName(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "+442079460958")
	r.typeInto("First name", "Ada")
	r.click(ui.RoleButton, "Save contact")
	got := r.sent(abi.CommandTagNewContact)
	if len(got) != 1 || string(got[0].NewContact.FullName) != "Ada" || string(got[0].NewContact.FirstName) != "Ada" || got[0].NewContact.ToPhone != r.a.Settings.SaveContactsToPhone {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-009..012, MM-010, SL-008: each question says what happens.
func TestConfirmationsExplainThemselves(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	for _, c := range []struct {
		d     state.Dialog
		title string
		text  string
	}{
		{state.Dialog{Kind: state.DialogConfirmClearChat, Chat: ada}, "Clear chat?", "This clears every message in your chat with Ada Lovelace, including downloaded media, on this computer and on your phone. The chat itself stays. It cannot be undone."},
		{state.Dialog{Kind: state.DialogConfirmUnlink}, "Unlink this computer?", "This removes the device from WhatsApp and deletes the chats stored here. You can link again with a new code."},
		{state.Dialog{Kind: state.DialogConfirmLockChat, Chat: ada}, "Lock this chat?", ""},
		{state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ada, IDs: *oneID("m1")}, "Delete for me?", "This removes the message from your phone and linked devices. Other people keep their copy. Connect to WhatsApp to delete it. This cannot be undone."},
		{state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ada, IDs: *oneID("m1"), ForEveryone: true}, "Delete for everyone?", "Everyone in this chat will see \"This message was deleted\" instead. It cannot be undone."},
		{state.Dialog{Kind: state.DialogConfirmRemoveAccount, Account: 0}, "Remove this account?", "This unlinks the number on this computer and deletes its local chats. Other accounts stay."},
	} {
		r.open(c.d)
		if _, ok := r.Find(ui.RoleDialog, c.title); !ok {
			t.Errorf("no dialog %q\n%s", c.title, widgettest.Dump(r.Tree()))
		}
		if c.text != "" && !r.has(ui.RoleLabel, c.text) {
			t.Errorf("%q: no text\n%s", c.title, widgettest.Dump(r.Tree()))
		}
		r.Key(ui.KeyEscape, 0)
		r.Frames(2)
	}
	if len(r.quiet()) != 0 {
		t.Errorf("cancelling sent %v", r.quiet())
	}
}

// DL-013: the media cache question names the account; the toast is the state's.
func TestClearMediaCacheNamesTheAccount(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogConfirmClearMediaCache, Account: 0})
	if !r.has(ui.RoleLabel, "Files in a download folder you chose are kept.") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
}

// LK-001: Lock chat asks, then sets the lock, and without a code goes on to
// set one.
func TestLockChat(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.a.OpenChat(ada)
	r.open(state.Dialog{Kind: state.DialogConfirmLockChat, Chat: ada})
	r.click(ui.RoleButton, "Lock chat")
	got := r.sent(abi.CommandTagSetLocked)
	if len(got) != 1 || !got[0].SetLocked.Locked || string(got[0].SetLocked.Chat) != "1@s.whatsapp.net" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.a.Open != 0 {
		t.Error("the locked chat stays open")
	}
	if r.s.Dialog.Kind != state.DialogUnlockLockedChats {
		t.Errorf("no code yet, so set one: %v", r.s.Dialog.Kind)
	}
}

// LK-003: the first time, the form offers to set a code (the code fields and
// their checks are in TestUnlock*, flows_test.go).
func TestUnlockOffersToSetACode(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogUnlockLockedChats})
	if _, ok := r.Find(ui.RoleDialog, "Set up locked chats"); !ok || !r.has(ui.RoleButton, "Set code and open") || !r.has(ui.RoleTextField, "Confirm code") {
		t.Fatalf("no set-up form\n%s", widgettest.Dump(r.Tree()))
	}
}

// DL-014: a group invite is looked up, shown, and joined or asked for.
func TestInviteLookupAndJoin(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC")})
	if !r.has(ui.RoleLabel, "Looking up the group…") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	preview := func(approval bool, id string) {
		r.f.Emit(ev(abi.EventTagInvitePreview, func(e *abi.Event) {
			e.InvitePreview = abi.EventInvitePreview{Code: []byte("AbC"), Result: abi.InviteResult{Tag: abi.InviteResultTagOk,
				Ok: abi.InviteResultOk{Info: abi.InviteInfo{ID: []byte(id), Subject: []byte("Go devs"), Members: 12, Approval: approval}}}}
		}))
		r.Frames(3)
	}
	preview(false, "777@g.us")
	if !r.has(ui.RoleLabel, "12 members") || !r.has(ui.RoleButton, "Join group") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Join group")
	if got := r.sent(abi.CommandTagJoinInvite); len(got) != 1 || string(got[0].JoinInvite.Code) != "AbC" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if !r.has(ui.RoleButton, "Joining…") {
		t.Errorf("no Joining…\n%s", widgettest.Dump(r.Tree()))
	}
	r.f.Emit(ev(abi.EventTagInviteJoined, func(e *abi.Event) {
		e.InviteJoined = abi.EventInviteJoined{Code: []byte("AbC"), Result: abi.JoinResult{Tag: abi.JoinResultTagOk, Ok: abi.JoinResultOk{Chat: []byte("777@g.us")}}}
	}))
	r.Frames(3)
	if r.s.Dialog.Open() || len(r.sent(abi.CommandTagEnsureChat)) != 1 {
		t.Errorf("open %v sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
}

// DL-014: a group that wants approval says so and sends a request.
func TestInviteNeedingApproval(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC")})
	r.f.Emit(ev(abi.EventTagInvitePreview, func(e *abi.Event) {
		e.InvitePreview = abi.EventInvitePreview{Code: []byte("AbC"), Result: abi.InviteResult{Tag: abi.InviteResultTagOk,
			Ok: abi.InviteResultOk{Info: abi.InviteInfo{ID: []byte("777@g.us"), Subject: []byte("Go devs"), Members: 1, Approval: true}}}}
	}))
	r.Frames(3)
	if !r.has(ui.RoleButton, "Request to join") || !r.has(ui.RoleLabel, "An admin must approve your request before you join.") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Request to join")
	r.f.Emit(ev(abi.EventTagInviteJoined, func(e *abi.Event) {
		e.InviteJoined = abi.EventInviteJoined{Code: []byte("AbC"), Result: abi.JoinResult{Tag: abi.JoinResultTagOk, Ok: abi.JoinResultOk{Chat: []byte("777@g.us"), Pending: true}}}
	}))
	r.Frames(3)
	if r.toast() != "Request sent. An admin must approve it before you join." || r.s.Dialog.Open() {
		t.Errorf("toast %q open %v", r.toast(), r.s.Dialog.Open())
	}
}

// DL-014: a group already joined opens its chat.
func TestInviteForAMemberOpensTheChat(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC")})
	r.f.Emit(ev(abi.EventTagInvitePreview, func(e *abi.Event) {
		e.InvitePreview = abi.EventInvitePreview{Code: []byte("AbC"), Result: abi.InviteResult{Tag: abi.InviteResultTagOk,
			Ok: abi.InviteResultOk{Info: abi.InviteInfo{ID: []byte("12345@g.us"), Subject: []byte("Analytical Engine"), Members: 4}}}}
	}))
	r.Frames(3)
	r.click(ui.RoleButton, "Open chat")
	if r.a.Open != r.ref("12345@g.us") {
		t.Errorf("chat %v", r.a.Open)
	}
}

// PK-018: a shared sticker pack is shown and added.
func TestStickerPackIsViewedAndAdded(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.open(state.Dialog{Kind: state.DialogStickerPack, Chat: ada, IDs: *oneID("m1")})
	if got := r.sent(abi.CommandTagViewStickerPack); len(got) != 1 || string(got[0].ViewStickerPack.Message) != "m1" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if !r.has(ui.RoleLabel, "Downloading the pack…") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.f.Emit(ev(abi.EventTagStickerPackPreview, func(e *abi.Event) {
		e.StickerPackPreview.Result = abi.StickerPackResult{Tag: abi.StickerPackResultTagOk, Ok: abi.StickerPackResultOk{
			Pack: abi.StickerPackInfo{Name: []byte("Ducks"), Dir: []byte("/packs/ducks")}, Publisher: []byte("Duck Co")}}
	}))
	r.Frames(3)
	r.click(ui.RoleButton, "Add to my stickers")
	if got := r.sent(abi.CommandTagAddStickerPack); len(got) != 1 || string(got[0].AddStickerPack.Dir) != "/packs/ducks" || string(got[0].AddStickerPack.Name) != "Ducks" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-015: the shortcuts list shows every one with its keys, and the switch
// for the hint bar writes the setting.
func TestShortcutsListAndHintSwitch(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.s.Settings.ShowShortcutHints = false
	r.open(state.Dialog{Kind: state.DialogShortcuts})
	if len(screen.Shortcuts) != 29 {
		t.Fatalf("%d shortcuts", len(screen.Shortcuts))
	}
	for _, name := range []string{"⌘K / ⌘Shift+F", "⌥↑ / ⌥↓"} {
		if !r.has(ui.RoleLabel, name) {
			t.Errorf("no %q", name)
		}
	}
	r.click(ui.RoleCheckbox, "Show shortcut hints under the message box")
	if !r.s.Settings.ShowShortcutHints {
		t.Error("the switch did not bring the hints back")
	}
}

// DL-016: About says what ZapFast is and warns it is unofficial.
func TestAboutSaysWhatItIs(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogAbout})
	for _, s := range []string{
		"A native WhatsApp client written in Go with gonativeui. It connects through whatsapp-rust. Messages are end-to-end encrypted on this device.",
		"This is an unofficial client. Using it may be against WhatsApp's terms of service and could get an account suspended.",
	} {
		if !r.has(ui.RoleLabel, s) {
			t.Errorf("no %q\n%s", s, widgettest.Dump(r.Tree()))
		}
	}
}

// PL-001: Send poll shows Sending… while the core makes it, and the answers
// can be added and removed within 2 to 12.
func TestPollAnswersAndSending(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.open(state.Dialog{Kind: state.DialogCreatePoll, Chat: ada})
	for i := 0; i < 10; i++ {
		r.click(ui.RoleButton, "Add answer")
	}
	if r.has(ui.RoleButton, "Add answer") || !r.has(ui.RoleTextField, "Answer 12") {
		t.Fatalf("more than 12 answers, or fewer\n%s", widgettest.Dump(r.Tree()))
	}
	for i := 0; i < 10; i++ {
		r.Play(widgettest.Click(ui.RoleButton, "Remove answer").Nth(0))
		r.Frames(2)
	}
	if r.has(ui.RoleButton, "Remove answer") || !r.has(ui.RoleTextField, "Answer 2") || r.has(ui.RoleTextField, "Answer 3") {
		t.Fatalf("fewer than 2 answers\n%s", widgettest.Dump(r.Tree()))
	}
	r.typeInto("Ask a question", "Lunch?")
	r.typeInto("Answer 1", "Soup")
	r.typeInto("Answer 2", "Salad")
	r.click(ui.RoleSwitch, "Allow multiple answers")
	r.click(ui.RoleButton, "Send poll")
	got := r.sent(abi.CommandTagCreatePoll)
	if len(got) != 1 || !got[0].CreatePoll.Draft.Multiple {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if !r.has(ui.RoleButton, "Sending…") || !r.s.Dialog.Open() {
		t.Fatalf("no Sending…\n%s", widgettest.Dump(r.Tree()))
	}
}

// PL-002: a poll the core refuses (a chat with disappearing messages) says
// why, and the dialog stays for another try.
func TestPollRefusedByTheCore(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.a.OpenChat(ada)
	r.open(state.Dialog{Kind: state.DialogCreatePoll, Chat: ada})
	r.typeInto("Ask a question", "Lunch?")
	r.typeInto("Answer 1", "Soup")
	r.typeInto("Answer 2", "Salad")
	r.click(ui.RoleButton, "Send poll")
	r.f.Emit(ev(abi.EventTagPollCreated, func(e *abi.Event) {
		e.PollCreated = abi.EventPollCreated{Chat: []byte("1@s.whatsapp.net"), HasError: true, Error: []byte("Poll creation in disappearing-message chats is not supported yet.")}
	}))
	r.Frames(3)
	if r.toast() != "Poll creation in disappearing-message chats is not supported yet." || !r.s.Dialog.Open() || !r.has(ui.RoleButton, "Send poll") {
		t.Fatalf("toast %q open %v\n%s", r.toast(), r.s.Dialog.Open(), widgettest.Dump(r.Tree()))
	}
	// A poll that was made closes the sheet.
	r.click(ui.RoleButton, "Send poll")
	r.f.Emit(ev(abi.EventTagPollCreated, func(e *abi.Event) { e.PollCreated = abi.EventPollCreated{Chat: []byte("1@s.whatsapp.net")} }))
	r.Frames(3)
	if r.s.Dialog.Open() {
		t.Error("the sheet stays after the poll was made")
	}
}

func pollMessage(votes ...abi.PollVoter) abi.Message {
	m := abi.Message{ID: []byte("p1"), Chat: []byte("1@s.whatsapp.net"), Sender: []byte("1@s.whatsapp.net"), Timestamp: 10,
		Content: abi.Content{Tag: abi.ContentTagPoll}}
	m.Content.Poll = abi.ContentPoll{Question: []byte("Lunch?"), Options: [][]byte{[]byte("Soup"), []byte("Salad")},
		State: abi.PollState{Counts: []uint64{2, 0}, Votes: votes, HistoryComplete: false}}
	return m
}

// PL-007: the results list who voted for what, and says when history is
// incomplete or the poll is gone.
func TestPollResults(t *testing.T) {
	r := newRig(t, append(baseEvents(), ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte("1@s.whatsapp.net"), Batch: 1, Last: true, Messages: []abi.Message{
			pollMessage(abi.PollVoter{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada"), Timestamp: 5, Choices: []uint64{0}}),
		}}
	}))...)
	ada := r.ref("1@s.whatsapp.net")
	r.a.OpenChat(ada)
	r.Frames(2)
	r.open(state.Dialog{Kind: state.DialogPollResults, Chat: ada, IDs: *oneID("p1")})
	for _, s := range []string{"Lunch?", "Soup", "2 votes", "0 votes", "Ada", "Earlier votes may still be missing. Results update as they arrive.", "Participant details are not available yet"} {
		if !r.has(ui.RoleLabel, s) {
			t.Errorf("no %q\n%s", s, widgettest.Dump(r.Tree()))
		}
	}
	r.open(state.Dialog{Kind: state.DialogPollResults, Chat: ada, IDs: *oneID("gone")})
	if !r.has(ui.RoleLabel, "This poll is no longer available.") {
		t.Errorf("\n%s", widgettest.Dump(r.Tree()))
	}
}

// IA-004: a list opens under its button's label with sections and
// descriptions; choosing sends the choice and closes; Escape sends nothing.
func TestInteractiveListDialog(t *testing.T) {
	list := abi.InteractiveButton{Label: []byte("Menu"), Action: abi.InteractiveAction{Tag: abi.InteractiveActionTagSelect}}
	list.Action.Select.Options = []abi.InteractiveOption{
		{Section: []byte("Starters"), Title: []byte("Soup"), Description: []byte("Hot")},
		{Section: []byte("Mains"), Title: []byte("Pasta")},
	}
	m := abi.Message{ID: []byte("i1"), Chat: []byte("1@s.whatsapp.net"), Sender: []byte("1@s.whatsapp.net"), Timestamp: 10, Content: abi.Content{Tag: abi.ContentTagInteractive}}
	m.Content.Interactive = abi.ContentInteractive{Text: []byte("Pick"), HasCard: true, Card: abi.InteractiveCard{Buttons: []abi.InteractiveButton{list}}}
	r := newRig(t, append(baseEvents(), ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte("1@s.whatsapp.net"), Batch: 1, Last: true, Messages: []abi.Message{m}}
	}))...)
	ada := r.ref("1@s.whatsapp.net")
	r.a.OpenChat(ada)
	r.Frames(2)
	r.open(state.Dialog{Kind: state.DialogInteractiveList, Chat: ada, IDs: *oneID("i1"), N: 0})
	for _, s := range []string{"Starters", "Mains", "Hot"} {
		if !r.has(ui.RoleLabel, s) {
			t.Errorf("no %q\n%s", s, widgettest.Dump(r.Tree()))
		}
	}
	if _, ok := r.Find(ui.RoleDialog, "Menu"); !ok {
		t.Errorf("not titled by the button\n%s", widgettest.Dump(r.Tree()))
	}
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.s.Dialog.Open() || len(r.sent(abi.CommandTagReplyInteractive)) != 0 {
		t.Fatal("Escape sent or stayed")
	}
	r.open(state.Dialog{Kind: state.DialogInteractiveList, Chat: ada, IDs: *oneID("i1"), N: 0})
	r.click(ui.RoleButton, "Pasta")
	got := r.sent(abi.CommandTagReplyInteractive)
	if len(got) != 1 || got[0].ReplyInteractive.Choice != 1 || r.s.Dialog.Open() {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// LO-001..004: coordinates in either order of separator, links and geo: are
// read; a short link asks for the full one; it sends a bare spot.
func TestLocationInputs(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: ada})
	r.typeInto("Paste a map link or coordinates", "https://maps.app.goo.gl/abc")
	if !r.has(ui.RoleLabel, "Short links only open online. Paste the full map link or the coordinates.") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Paste a map link or coordinates"), widgettest.Key('A', ui.ModSuper), widgettest.Type("40,7128 -74,0060"))
	r.Frames(2)
	if !r.has(ui.RoleLabel, "40.71280, -74.00600") || !r.has(ui.RoleLink, "Open in a map") {
		t.Fatalf("\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Send")
	got := r.sent(abi.CommandTagSendLocation)
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// AC-009's dialog: Remove this account asks first and sends nothing on Cancel.
func TestRemoveAccountCancel(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogConfirmRemoveAccount, Account: 0})
	r.click(ui.RoleButton, "Cancel")
	if len(r.quiet()) != 0 || r.s.Dialog.Open() {
		t.Fatalf("sent %v", r.quiet())
	}
}

// DL-017: with a dialog open, nothing beneath it takes keys or presses.
func TestDialogIsModal(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogAbout})
	if !r.Context().ModalOpen() {
		t.Error("an open dialog is not modal")
	}
}

// Clicking the wrapped hint label toggles the box, as a checkbox's label does
// on macOS; the box stays the one accessible control.
func TestShortcutHintLabelTogglesTheBox(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.s.Settings.ShowShortcutHints = false
	r.open(state.Dialog{Kind: state.DialogShortcuts})
	const name = "Show shortcut hints under the message box"
	// The label is no node, so click the dialog at the label's text, a little
	// right of the box and level with it.
	clickLabel := func() {
		box, dlg := r.Must(ui.RoleCheckbox, name), r.Must(ui.RoleDialog, "Keyboard shortcuts")
		dx := box.Bounds[0] + 56 - dlg.Bounds[0]
		dy := (box.Bounds[1]+box.Bounds[3])/2 - dlg.Bounds[1]
		r.Play(widgettest.ClickAt(ui.RoleDialog, "Keyboard shortcuts", dx, dy).Through())
		r.Frames(3)
	}
	clickLabel()
	if !r.s.Settings.ShowShortcutHints {
		t.Fatalf("a click on the label did not turn the hints on\n%s", widgettest.Dump(r.Tree()))
	}
	if n, _ := r.Find(ui.RoleCheckbox, name); n.State&ui.SemChecked == 0 {
		t.Error("the box does not show the hints on")
	}
	clickLabel()
	if r.s.Settings.ShowShortcutHints {
		t.Fatal("a second click did not turn them off")
	}
	// One control: the box, named by the label; the label's text is no node.
	if r.has(ui.RoleLabel, name) {
		t.Errorf("the label is a node of its own\n%s", widgettest.Dump(r.Tree()))
	}
}
