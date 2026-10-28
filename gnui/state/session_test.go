package state_test

import (
	"bytes"
	"testing"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

func loggedOut() *abi.Event {
	return ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut })
}

// relinkWithAda is another number linked into the account: connected, and
// a chat list with Ada in it again (the contact both numbers share).
func relinkWithAda() []*abi.Event {
	return []*abi.Event{
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = []byte("5550002@s.whatsapp.net") }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 9, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 7}}}
		}),
	}
}

// LG-007: an unlinked number's session goes with it (zf:src/app.rs:4128-4174).
// Another number linked into the same account, sharing a contact, sees none
// of the first one's messages, drafts, answers, pictures, stars or open chat.
func TestUnlinkingForgetsTheNumbersSession(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	ref := a.Model.Chats.Find([]byte(ada))
	a.OpenChat(ref)
	m := text("old", 3)
	r.cores[0].Emit(
		ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(ada), Last: true, Messages: []abi.Message{m}}
		}),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts.Contacts = []abi.Contact{{ID: []byte(ada), FullName: []byte("Ada Lovelace")}}
		}),
		ev(abi.EventTagAvatar, func(e *abi.Event) {
			e.Avatar = abi.EventAvatar{ID: []byte(ada), Path: []byte("/old/ada.jpg"), HasPath: true}
		}),
		ev(abi.EventTagDrafts, func(e *abi.Event) {
			e.Drafts.Drafts = []abi.Draft{{Chat: []byte(ada), Text: []byte("old secret")}}
		}),
		ev(abi.EventTagStarChanged, func(e *abi.Event) {
			e.StarChanged = abi.EventStarChanged{Chat: []byte(ada), Message: []byte("old"), Starred: true}
		}),
		ev(abi.EventTagSearchHits, func(e *abi.Event) {
			e.SearchHits.Query = []byte("old")
			e.SearchHits.Last = true
			e.SearchHits.Messages = []abi.Message{m}
		}),
	)
	r.pump()
	if a.Conversation() == nil || a.Conversation().Len() != 1 || len(a.Model.Draft([]byte(ada))) == 0 ||
		!a.Model.Answer(abi.EventTagSearchHits).Has() || !a.Model.Starred([]byte(ada), []byte("old")) {
		t.Fatal("the first number's session is not set up")
	}
	r.s.Panes.Reply.Add([]byte("old"))
	r.s.Req.Restore = state.Prefill{Acct: a.Serial, Chat: []byte(ada), Text: []byte("refused secret")}
	r.s.Staged = append(r.s.Staged, state.Staged{ID: 1, Kind: state.StagedFile, Path: []byte("/old/file.pdf")})
	a.Mode = state.ListStarred
	serial, chatGen := a.Serial, a.Model.Chats.At(ref).Gen()
	unlinked := 0
	r.s.OnUnlinked(t, func(_ *state.State, b *state.Account) {
		if b == a {
			unlinked++
		}
	})
	r.cores[0].Emit(loggedOut())
	r.pump()

	if unlinked != 1 {
		t.Errorf("the OnUnlinked hooks ran %d times", unlinked)
	}
	if a.Serial == serial {
		t.Error("the unlinked session kept its Serial: caches keyed by it still match")
	}
	if a.Open != 0 || a.Mode != state.ListChats || len(a.Settings.LastChat) != 0 {
		t.Errorf("open %d, mode %d, last chat %q", a.Open, a.Mode, a.Settings.LastChat)
	}
	p := &r.s.Panes
	if p.Reply.Len() != 0 || len(r.s.Staged) != 0 || len(r.s.Req.Restore.Text) != 0 {
		t.Error("the window kept the old number's reply, attachments or refused text")
	}
	for _, c := range r.cores[0].Sent() {
		if c.Tag == abi.CommandTagSaveDraft && bytes.Contains(c.SaveDraft.Text, []byte("secret")) {
			t.Errorf("unlinking stored the old number's text %q", c.SaveDraft.Text)
		}
	}
	if !hasToast(r.s, "This device was unlinked from your phone") {
		t.Error("no toast says the device was unlinked")
	}

	r.cores[0].Emit(relinkWithAda()...)
	r.pump()
	ref = a.Model.Chats.Find([]byte(ada))
	if ref == 0 {
		t.Fatal("the new number's chat list is not in")
	}
	if g := a.Model.Chats.At(ref).Gen(); g <= chatGen {
		t.Errorf("the new session's row is at generation %d, the old one's reached %d", g, chatGen)
	}
	a.OpenChat(ref)
	if v := a.Conversation(); v == nil || v.Len() != 0 {
		t.Error("the new number's chat shows the old number's messages")
	}
	if d := a.Model.Draft([]byte(ada)); len(d) != 0 {
		t.Errorf("the new number's chat has the old draft %q", d)
	}
	if a.Model.Answer(abi.EventTagSearchHits).Has() {
		t.Error("the old number's search hits are still the answer")
	}
	if _, ok, known := a.Model.Avatars.Thumb([]byte(ada)); ok || known {
		t.Error("the old number's picture of Ada is still known")
	}
	if a.Model.Contacts.Find([]byte(ada)) >= 0 || bytes.Equal(a.Model.DisplayName([]byte(ada)), []byte("Ada Lovelace")) {
		t.Error("the old number's address book still names Ada")
	}
	if a.Model.Starred([]byte(ada), []byte("old")) {
		t.Error("the old number's star is still on")
	}
	if !a.Model.Avatars.Ask([]byte(ada), false) {
		t.Error("the new number may not ask for Ada's picture")
	}
}

// An account that is not on screen forgets its own session and leaves the
// window's alone.
func TestAHiddenAccountsUnlinkLeavesTheWindowAlone(t *testing.T) {
	r := newRig(t, 2)
	a, b := r.s.Accounts[0], r.s.Accounts[1]
	b.Open = b.Model.Chats.Find([]byte(ada))
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.s.Panes.Reply.Add([]byte("mine"))
	r.cores[1].Emit(loggedOut())
	r.pump()
	if b.Open != 0 || b.Model.Chats.Len() != 0 {
		t.Error("the hidden account kept its chats")
	}
	if a.Open == 0 || r.s.Panes.Reply.Len() != 1 || a.Model.Chats.Len() != 1 {
		t.Error("the hidden account's unlink touched the account on screen")
	}
}

func hasToast(s *state.State, text string) bool {
	for _, t := range s.Toasts {
		if string(t.Text) == text {
			return true
		}
	}
	return false
}

// A JID names a chat only inside one account: a refused send's text waiting
// for the open chat of the account on screen never goes into another
// account's chat of the same JID when that one closes.
func TestARefusedTextStaysWithItsAccount(t *testing.T) {
	r := newRig(t, 2)
	a, b := r.s.Accounts[0], r.s.Accounts[1]
	// Each account has its Ada open; b's is parked as b leaves the screen.
	r.s.SwitchAccount(1)
	b.OpenChat(b.Model.Chats.Find([]byte(ada)))
	r.s.SwitchAccount(0)
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	if b.Open == 0 {
		t.Fatal("the other account's chat is not parked open")
	}
	r.cores[0].Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
		e.SendRefused.Chat, e.SendRefused.Reason = []byte(ada), abi.RefusalOffline
		e.SendRefused.Unsent.Tag, e.SendRefused.Unsent.Text.Text = abi.UnsentTagText, []byte("a's refused text")
	}))
	r.pump()
	if string(r.s.Req.Restore.Text) != "a's refused text" {
		t.Fatalf("restore %q", r.s.Req.Restore.Text)
	}
	// The other account's Ada goes, closing its open chat.
	r.cores[1].Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(ada) }))
	r.pump()
	if b.Open != 0 {
		t.Fatal("the other account's chat did not close")
	}
	if d := b.Model.Draft([]byte(ada)); len(d) != 0 || r.sent(1, abi.CommandTagSaveDraft) != 0 {
		t.Errorf("the other account took the text: draft %q, %d SaveDraft", d, r.sent(1, abi.CommandTagSaveDraft))
	}
	if string(r.s.Req.Restore.Text) != "a's refused text" || r.s.Req.Restore.Acct != a.Serial {
		t.Errorf("the account on screen lost its text: %+v", r.s.Req.Restore)
	}
	// Its own chat's close keeps it, as a draft.
	a.OpenChat(0)
	if d := a.Model.Draft([]byte(ada)); string(d) != "a's refused text" {
		t.Errorf("the account's own close kept %q", d)
	}
}

// A removed chat leaves nothing reachable, as upstream's forget_chat
// (zf:src/app.rs:2235-2316): its row, messages, draft, stars, pins,
// downloads, typing, listings and search hits, its last-chat setting and the
// open chat go, nothing of it is stored, and a chat of the same JID that
// comes back later starts clean under a new ref.
func TestARemovedChatLeavesNothingBehind(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	const grace = "2@s.whatsapp.net"
	r.cores[0].Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4}
	}))
	r.pump()
	ref := a.Model.Chats.Find([]byte(ada))
	a.OpenChat(ref)
	m := text("old", 3)
	other := text("g1", 2)
	other.Chat = []byte(grace)
	r.cores[0].Emit(
		ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(ada), Last: true, Messages: []abi.Message{m}}
		}),
		ev(abi.EventTagDrafts, func(e *abi.Event) {
			e.Drafts.Drafts = []abi.Draft{{Chat: []byte(ada), Text: []byte("old secret")}}
		}),
		ev(abi.EventTagStarChanged, func(e *abi.Event) {
			e.StarChanged = abi.EventStarChanged{Chat: []byte(ada), Message: []byte("old"), Starred: true}
		}),
		ev(abi.EventTagPins, func(e *abi.Event) {
			e.Pins = abi.EventPins{Chat: []byte(ada), Last: true, Items: []abi.Pinned{{Chat: []byte(ada), ID: []byte("old"), Text: []byte("pinned secret")}}}
		}),
		ev(abi.EventTagMedia, func(e *abi.Event) {
			e.Media = abi.EventMedia{Chat: []byte(ada), Message: []byte("old")}
			e.Media.Result.Tag, e.Media.Result.Ok.Path = abi.PathResultTagOk, []byte("/cache/old.jpg")
		}),
		ev(abi.EventTagTyping, func(e *abi.Event) {
			e.Typing = abi.EventTyping{Chat: []byte(ada), Sender: []byte(ada), Composing: true}
		}),
		ev(abi.EventTagChatHits, func(e *abi.Event) {
			e.ChatHits = abi.EventChatHits{Chat: []byte(ada), Query: []byte("old"), Last: true, Messages: []abi.Message{m}}
		}),
		ev(abi.EventTagSearchHits, func(e *abi.Event) {
			e.SearchHits.Query, e.SearchHits.Last = []byte("m"), true
			e.SearchHits.Messages = []abi.Message{m, other}
		}),
	)
	r.pump()
	if a.Conversation() == nil || a.Model.ChatPins([]byte(ada)) != 1 || a.Model.Answer(abi.EventTagSearchHits).Len() != 2 {
		t.Fatal("the chat is not set up")
	}
	if _, n := a.Model.Typing(ref); n != 1 {
		t.Fatal("no one types")
	}
	r.s.Panes.Info.Add([]byte(ada))
	r.s.Panes.Pane = state.PaneInfo
	r.cores[0].Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(ada) }))
	r.pump()

	ms := a.Model
	if ms.Chats.Find([]byte(ada)) != 0 || a.Open != 0 || ms.Conversation([]byte(ada)) != nil {
		t.Error("the chat, its messages or the open chat stayed")
	}
	if _, ok := ms.Chats.Get(ref); ok {
		t.Error("the old ref still names a chat")
	}
	if len(ms.Draft([]byte(ada))) != 0 || ms.Starred([]byte(ada), []byte("old")) || ms.ChatPins([]byte(ada)) != 0 {
		t.Error("its draft, star or pin stayed")
	}
	if _, _, _, ok := ms.File([]byte(ada), []byte("old"), model.PartMain); ok {
		t.Error("its download stayed")
	}
	if _, n := ms.Typing(ref); n != 0 {
		t.Error("someone still types in it")
	}
	if ms.Answer(abi.EventTagChatHits).Has() {
		t.Error("its in-chat search stayed")
	}
	if h := ms.Answer(abi.EventTagSearchHits); h.Len() != 1 {
		t.Errorf("the search hits hold %d messages, want only the other chat's", h.Len())
	} else if ev, j := h.Item(0); string(ev.SearchHits.Messages[j].Chat) != grace {
		t.Errorf("the hit left is %q's", ev.SearchHits.Messages[j].Chat)
	}
	if len(a.Settings.LastChat) != 0 || r.s.Panes.Info.Len() != 0 || r.s.Panes.Pane != state.PaneNone {
		t.Errorf("last chat %q, info %d, pane %d", a.Settings.LastChat, r.s.Panes.Info.Len(), r.s.Panes.Pane)
	}
	cleared := false
	for _, c := range r.s.NoticeClears {
		cleared = cleared || (c.Account == a.ID && string(c.Chat) == ada)
	}
	if !cleared {
		t.Error("its notifications were not taken back")
	}
	for _, c := range r.cores[0].Sent() {
		if c.Tag == abi.CommandTagSaveDraft && len(c.SaveDraft.Text) > 0 {
			t.Errorf("the removal stored %q", c.SaveDraft.Text)
		}
	}

	r.cores[0].Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 9}
	}))
	r.pump()
	back := ms.Chats.Find([]byte(ada))
	if back == 0 || back == ref {
		t.Fatalf("the returning chat's ref is %d (the old one was %d)", back, ref)
	}
	a.OpenChat(back)
	if v := a.Conversation(); v == nil || v.Len() != 0 || len(ms.Draft([]byte(ada))) != 0 || ms.Starred([]byte(ada), []byte("old")) {
		t.Error("the returning chat is not clean")
	}
}

// The core's real logout (zf:src/backend/worker.rs:3763-3777): an empty
// chat list, no drafts, then LoggedOut. A refused text waiting for the open
// chat is not stored as it closes with the list, and the hooks see the chat
// go (OnChatGone) before it closes (OnChatClose).
func TestTheCoresLogoutSequenceStoresNothing(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.s.Req.Restore = state.Prefill{Acct: a.Serial, Chat: []byte(ada), Text: []byte("refused secret")}
	var order []string
	r.s.OnChatGone(t, func(_ *state.State, _ *state.Account, chat []byte) { order = append(order, "gone "+string(chat)) })
	r.s.OnChatClose(t, func(_ *state.State, _ *state.Account, chat []byte) { order = append(order, "close "+string(chat)) })
	before := len(r.cores[0].Sent())
	r.cores[0].Emit(
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 99, Last: true} }),
		ev(abi.EventTagDrafts, func(e *abi.Event) {}),
		loggedOut(),
	)
	r.pump()
	for _, c := range r.cores[0].Sent()[before:] {
		if c.Tag == abi.CommandTagSaveDraft {
			t.Errorf("the logout stored %q", c.SaveDraft.Text)
		}
	}
	if len(order) != 2 || order[0] != "gone "+ada || order[1] != "close "+ada {
		t.Errorf("hooks ran %q", order)
	}
	if a.Open != 0 || len(r.s.Req.Restore.Text) != 0 {
		t.Errorf("open %d, restore %q", a.Open, r.s.Req.Restore.Text)
	}
}
