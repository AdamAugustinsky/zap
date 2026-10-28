package composer

import (
	"bytes"
	"testing"
	"time"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// LG-007: unlinking a number drops what the composer kept of its chats, the
// open one's and the parked ones', unstored (zf:src/app.rs:4160-4172): a
// number linked next into the account, sharing those contacts, finds no
// text, attachment or quote of the first one.
func TestUnlinkingDropsTheComposersDraftsAndAttachments(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(grace)
	r.focus()
	r.typeText("grace secret")
	r.dropAt(ui.DropFiles, []byte("/tmp/grace.txt\x00"), 1)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hello"))
	r.focus()
	r.typeText("ada secret")
	r.s.Panes.Reply.Add([]byte("m1"))
	r.dropAt(ui.DropFiles, []byte("/tmp/ada.txt\x00"), 1)
	if r.text() != "ada secret" || len(r.chips()) != 1 {
		t.Fatalf("set-up: %q, %d chips", r.text(), len(r.chips()))
	}
	saved := len(r.sent(abi.CommandTagSaveDraft))

	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
	r.Frames(3)
	if r.text() != "" || len(r.s.Staged) != 0 || r.s.Panes.Reply.Len() != 0 {
		t.Errorf("after the unlink the composer holds %q, %d attachments, %d replies", r.text(), len(r.s.Staged), r.s.Panes.Reply.Len())
	}
	for _, c := range r.sent(abi.CommandTagSaveDraft)[saved:] {
		if bytes.Contains(c.SaveDraft.Text, []byte("secret")) {
			t.Errorf("the unlink stored the old number's text %q", c.SaveDraft.Text)
		}
	}

	r.f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 7, Last: true, Chats: defaultChats()} }),
	)
	r.Frames(3)
	for _, jid := range []string{grace, ada} {
		r.open(jid)
		r.Frames(2)
		if r.text() != "" || len(r.chips()) != 0 || r.s.Panes.Reply.Len() != 0 {
			t.Errorf("the new number's chat %s shows %q, chips %v, %d replies", jid, r.text(), r.chips(), r.s.Panes.Reply.Len())
		}
	}
}

// A refused text for another account's chat of the same JID is not taken
// into the open chat's composer.
func TestARefusedTextForAnotherAccountIsNotRestored(t *testing.T) {
	r := newRig(t, nil, nil)
	f2 := fake.New(nil)
	f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: defaultChats()} }))
	b := r.s.AddAccount("2", nil, f2)
	r.Frames(2)
	r.open(ada)
	r.s.Req.Restore = state.Prefill{Acct: b.Serial, Chat: []byte(ada), Text: []byte("b's refused text")}
	r.Frames(3)
	if r.text() != "" {
		t.Errorf("the composer took another account's text %q", r.text())
	}
	r.s.Req.Restore = state.Prefill{Acct: r.a.Serial, Chat: []byte(ada), Text: []byte("own text")}
	r.Frames(3)
	if r.text() != "own text" {
		t.Errorf("the composer did not take its own account's text: %q", r.text())
	}
}

// A removed chat's text, attachments and quote go unstored, the open one's
// and a parked one's (zf:src/app.rs:2244-2246, :2299-2315), and a chat of
// the same JID that comes back finds none of them.
func TestARemovedChatsComposerGoesUnstored(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(grace)
	r.focus()
	r.typeText("grace secret")
	r.dropAt(ui.DropFiles, []byte("/tmp/grace.txt\x00"), 1)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hello"))
	r.focus()
	r.typeText("ada secret")
	r.s.Panes.Reply.Add([]byte("m1"))
	r.dropAt(ui.DropFiles, []byte("/tmp/ada.txt\x00"), 1)
	saved := len(r.sent(abi.CommandTagSaveDraft))

	for _, jid := range []string{ada, grace} {
		r.f.Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(jid) }))
	}
	r.Frames(3)
	if r.text() != "" || len(r.s.Staged) != 0 || r.s.Panes.Reply.Len() != 0 || r.a.Open != 0 {
		t.Errorf("after the removal: %q, %d attachments, %d replies, open %d", r.text(), len(r.s.Staged), r.s.Panes.Reply.Len(), r.a.Open)
	}
	for _, c := range r.sent(abi.CommandTagSaveDraft)[saved:] {
		if bytes.Contains(c.SaveDraft.Text, []byte("secret")) {
			t.Errorf("the removal stored %q", c.SaveDraft.Text)
		}
	}
	r.f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 8, Last: true, Chats: defaultChats()} }))
	r.Frames(3)
	for _, jid := range []string{grace, ada} {
		r.open(jid)
		r.Frames(2)
		if r.text() != "" || len(r.chips()) != 0 || r.s.Panes.Reply.Len() != 0 {
			t.Errorf("the returning chat %s shows %q, chips %v, %d replies", jid, r.text(), r.chips(), r.s.Panes.Reply.Len())
		}
	}
}

// The core's real logout (zf:src/backend/worker.rs:3719, :3763-3777): it
// clears its archive, then sends an empty chat list, no drafts and
// LoggedOut. The open chat goes with the list, unstored, as upstream clears
// open_chat (zf:src/app.rs:3233): no SaveDraft, nor any other write, reaches
// the cleared archive, whatever the composer, a parked chat or a refused
// send held, and nothing shows once a number links again.
func TestTheCoresLogoutSequenceStoresNoDraft(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(grace)
	r.focus()
	r.typeText("grace secret")
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hello"))
	r.focus()
	r.typeText("private secret")
	r.s.Panes.Reply.Add([]byte("m1"))
	r.dropAt(ui.DropFiles, []byte("/tmp/ada.txt\x00"), 1)
	before := len(r.f.Sent())

	r.f.Emit(
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 99, Last: true} }),
		ev(abi.EventTagDrafts, func(e *abi.Event) {}),
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }),
	)
	r.Frames(3)
	r.now = r.now.Add(time.Minute) // past every pause that stores a draft
	r.Frames(3)
	for _, c := range r.f.Sent()[before:] {
		switch c.Tag {
		case abi.CommandTagSaveDraft, abi.CommandTagSendText, abi.CommandTagSendFiles:
			t.Errorf("the logout wrote %v to the archive: %q", c.Tag, c.SaveDraft.Text)
		}
	}
	if r.text() != "" || len(r.s.Staged) != 0 || r.s.Panes.Reply.Len() != 0 {
		t.Errorf("after the logout: %q, %d attachments, %d replies", r.text(), len(r.s.Staged), r.s.Panes.Reply.Len())
	}
	r.f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 100, Last: true, Chats: defaultChats()} }),
	)
	r.Frames(3)
	for _, jid := range []string{ada, grace} {
		r.open(jid)
		if r.text() != "" || len(r.chips()) != 0 || r.s.Panes.Reply.Len() != 0 {
			t.Errorf("the relinked chat %s shows %q, chips %v", jid, r.text(), r.chips())
		}
	}
}

// A reload of the list without the open chat closes it unstored too.
func TestAChatMissingFromAReloadIsNotStored(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.typeText("private secret")
	before := len(r.f.Sent())
	r.f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 5, Last: true, Chats: defaultChats()[1:]} }))
	r.Frames(3)
	for _, c := range r.f.Sent()[before:] {
		if c.Tag == abi.CommandTagSaveDraft {
			t.Errorf("the vanished chat's draft was stored: %q", c.SaveDraft.Text)
		}
	}
	if r.a.Open != 0 || r.text() != "" {
		t.Errorf("open %d, composer %q", r.a.Open, r.text())
	}
}
