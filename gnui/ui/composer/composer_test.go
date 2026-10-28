package composer

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// CP-001, KB-011: Enter sends the text to the open chat and empties the
// composer; "typing…" goes before and stops with the send; the archived
// draft is cleared.
func TestEnterSendsTheText(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.typeText("hello there")
	if c, ok := r.last(abi.CommandTagComposing); !ok || !c.Composing.Composing || !eq(c.Composing.Chat, ada) {
		t.Fatalf("typing sent no composing: %v", r.f.SentTags())
	}
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagSendText)
	if !ok || !eq(c.SendText.Chat, ada) || !eq(c.SendText.Text, "hello there") || c.SendText.HasQuoting {
		t.Fatalf("sent %v (%q)", r.f.SentTags(), c.SendText.Text)
	}
	if r.text() != "" {
		t.Errorf("the composer keeps %q after the send", r.text())
	}
	if c, _ := r.last(abi.CommandTagComposing); c.Composing.Composing {
		t.Error("typing did not stop with the send")
	}
	if d, ok := r.last(abi.CommandTagSaveDraft); !ok || len(d.SaveDraft.Text) != 0 {
		t.Error("the sent text stays the chat's draft")
	}
	// White space alone sends nothing.
	n := len(r.sent(abi.CommandTagSendText))
	r.typeText("   ")
	r.key(ui.KeyEnter, 0)
	if len(r.sent(abi.CommandTagSendText)) != n {
		t.Error("white space was sent")
	}
}

// CP-001: with Enter sends off, Enter does not send and ⌘Enter does.
func TestCommandEnterSendsWhenEnterDoesNot(t *testing.T) {
	r := newRig(t, nil, func(s *state.State) { s.Settings.EnterSends = false })
	r.open(ada)
	r.focus()
	r.typeText("one")
	r.key(ui.KeyEnter, 0)
	if _, ok := r.last(abi.CommandTagSendText); ok {
		t.Fatal("Enter sent with Enter sends off")
	}
	r.key(ui.KeyEnter, ui.PrimaryMod())
	if c, ok := r.last(abi.CommandTagSendText); !ok || !eq(c.SendText.Text, "one") {
		t.Fatalf("⌘Enter sent %v", r.f.SentTags())
	}
	// The hints bar says so.
	if !r.hasText("⌘+Enter sends") {
		t.Error("the hints do not name ⌘+Enter")
	}
}

// The send button sends too, and is the microphone while the composer is
// empty (CP-010): in the field, in its place.
func TestSendButtonAndMicrophone(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	mic, ok := r.Find(ui.RoleButton, "Record a voice message")
	if !ok {
		t.Fatal("an empty composer offers no microphone")
	}
	if r.has(ui.RoleButton, "Send") {
		t.Error("the send button shows beside the microphone")
	}
	if field, _ := r.Find(ui.RoleGroup, "Message"); mic.Bounds[0] < field.Bounds[0] || mic.Bounds[2] > field.Bounds[2] {
		t.Errorf("the microphone %v is outside the field %v", mic.Bounds, field.Bounds)
	}
	r.focus()
	r.typeText("hi")
	if r.has(ui.RoleButton, "Record a voice message") {
		t.Error("the microphone stays beside text")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	if c, ok := r.last(abi.CommandTagSendText); !ok || !eq(c.SendText.Text, "hi") {
		t.Fatalf("the send button sent %v", r.f.SentTags())
	}
	// CP-025: without a recorder the microphone says it cannot record.
	r.mic.accessErr = fmt.Errorf("perm: %w", errors.ErrUnsupported)
	r.Play(widgettest.Click(ui.RoleButton, "Record a voice message"), widgettest.Idle(2))
	if len(r.s.Toasts) == 0 || string(r.s.Toasts[0].Text) != "Could not record: no microphone" || !r.s.Toasts[0].Error {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

// MM-004: a reply shows "Replying to {who}" above the field; the send
// quotes it; Cancel reply drops it.
func TestReplyStripQuotes(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Shall we compute?"), textMsg("m2", me, true, 110, "Yes"))
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	if !r.hasText("Replying to Ada Lovelace") || !r.hasText("Shall we compute?") {
		t.Fatalf("no reply strip:\n%s", widgettest.Dump(r.Tree()))
	}
	r.typeText("Sure")
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagSendText)
	if !ok || !c.SendText.HasQuoting || !eq(c.SendText.Quoting.ID, "m1") {
		t.Fatalf("the send quotes %q (%v)", c.SendText.Quoting.ID, ok)
	}
	if r.s.Panes.Reply.Len() != 0 || r.hasText("Replying to") {
		t.Error("the reply stays after the send")
	}
	// Our own message is "You"; Cancel reply (Esc) drops it.
	r.s.Panes.Reply.Add([]byte("m2"))
	r.Frames(2)
	if !r.hasText("Replying to You") {
		t.Error("a reply to our message does not name You")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Cancel reply (Esc)"), widgettest.Idle(2))
	if r.s.Panes.Reply.Len() != 0 {
		t.Error("Cancel reply left the reply")
	}
	// Escape in the composer drops it too.
	r.s.Panes.Reply.Add([]byte("m2"))
	r.Frames(2)
	r.focus()
	r.key(ui.KeyEscape, 0)
	if r.s.Panes.Reply.Len() != 0 {
		t.Error("Escape left the reply")
	}
}

// MM-008: editing puts the message's text in the composer under "Editing
// message"; the send edits instead of sending; Stop editing empties it.
func TestEditingAMessage(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", me, true, r.now.Unix()-60, "Helo"))
	r.s.Panes.Edit.Add([]byte("m1"))
	r.Frames(2)
	if r.text() != "Helo" || !r.hasText("Editing message") {
		t.Fatalf("editing shows %q", r.text())
	}
	if r.has(ui.RoleButton, "Attach") || r.has(ui.RoleButton, "Record a voice message") {
		t.Error("the tools show while editing")
	}
	r.focus()
	r.key(ui.KeyEnd, 0)
	r.typeText(" world")
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagEditText)
	if !ok || !eq(c.EditText.ID, "m1") || !eq(c.EditText.Text, "Helo world") || !eq(c.EditText.Chat, ada) {
		t.Fatalf("the edit sent %v (%q)", r.f.SentTags(), c.EditText.Text)
	}
	if _, ok := r.last(abi.CommandTagSendText); ok {
		t.Error("an edit went out as a new message")
	}
	if r.s.Panes.Edit.Len() != 0 || r.text() != "" {
		t.Error("the edit stays after the send")
	}
	r.s.Panes.Edit.Add([]byte("m1"))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Stop editing (Esc)"), widgettest.Idle(2))
	if r.s.Panes.Edit.Len() != 0 || r.text() != "" {
		t.Errorf("Stop editing left %q", r.text())
	}
}

// KB-010: Up in an empty composer edits our latest message that can still
// be edited.
func TestUpEditsTheLastMessage(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada,
		textMsg("old", me, true, r.now.Unix()-3600, "too old"),
		textMsg("m1", me, true, r.now.Unix()-120, "mine"),
		textMsg("m2", ada, false, r.now.Unix()-60, "theirs"))
	r.focus()
	r.typeText(" ")
	r.key(ui.KeyUp, 0)
	if r.s.Panes.Edit.Len() != 0 {
		t.Fatal("Up edited with a space typed")
	}
	r.key('A', ui.PrimaryMod())
	r.key(ui.KeyBackspace, 0)
	r.key(ui.KeyUp, 0)
	r.Frames(2)
	if r.s.Panes.Edit.Len() != 1 || string(r.s.Panes.Edit.At(0)) != "m1" || r.text() != "mine" {
		t.Fatalf("Up edits %v, text %q", r.s.Panes.Edit.Len(), r.text())
	}
	if r.has(ui.RoleButton, "Record a voice message") {
		t.Error("an edit offers the microphone")
	}
}

// CP-004: each chat keeps its unsent text across chat switches (and in the
// archive, for restarts); the archive's drafts come back at start.
func TestDraftsFollowTheChats(t *testing.T) {
	r := newRig(t, nil, nil)
	r.f.Emit(ev(abi.EventTagDrafts, func(e *abi.Event) {
		e.Drafts.Last = true
		e.Drafts.Drafts = []abi.Draft{{Chat: []byte(grace), Text: []byte("from last time")}}
	}))
	r.Frames(2)
	r.open(ada)
	r.focus()
	r.typeText("half a thought")
	r.open(grace)
	if d, ok := r.last(abi.CommandTagSaveDraft); !ok || !eq(d.SaveDraft.Chat, ada) || !eq(d.SaveDraft.Text, "half a thought") {
		t.Fatalf("leaving the chat saved %q for %q", d.SaveDraft.Text, d.SaveDraft.Chat)
	}
	if r.text() != "from last time" {
		t.Fatalf("Grace's composer holds %q", r.text())
	}
	if c, _ := r.last(abi.CommandTagComposing); c.Composing.Composing {
		t.Error("typing did not stop when the chat changed")
	}
	r.open(ada)
	if r.text() != "half a thought" {
		t.Fatalf("Ada's draft came back as %q", r.text())
	}
	// A sent message leaves no draft behind.
	r.focus()
	r.key(ui.KeyEnter, 0)
	r.open(grace)
	r.open(ada)
	if r.text() != "" {
		t.Errorf("a sent message came back as a draft: %q", r.text())
	}
}

// An unfinished edit is dropped, not kept as the chat's draft.
func TestAnEditIsNotADraft(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", me, true, r.now.Unix()-60, "sent text"))
	r.s.Panes.Edit.Add([]byte("m1"))
	r.Frames(2)
	r.open(grace)
	r.open(ada)
	if r.text() != "" || r.s.Panes.Edit.Len() != 0 {
		t.Errorf("the edit came back: %q", r.text())
	}
}

// "Typing…" stops after a pause, and the draft is kept in the archive then.
func TestComposingStopsAfterAPause(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.typeText("abc")
	if c, _ := r.last(abi.CommandTagComposing); !c.Composing.Composing {
		t.Fatal("no composing")
	}
	n := len(r.sent(abi.CommandTagComposing))
	r.typeText("d")
	if len(r.sent(abi.CommandTagComposing)) != n {
		t.Error("composing was sent again for every key")
	}
	r.now = r.now.Add(5 * time.Second)
	r.Frames(2)
	if c, _ := r.last(abi.CommandTagComposing); c.Composing.Composing {
		t.Error("typing did not stop after the pause")
	}
	if d, ok := r.last(abi.CommandTagSaveDraft); !ok || !eq(d.SaveDraft.Text, "abcd") {
		t.Error("the draft was not kept after the pause")
	}
	// Without "Show when you are typing" nothing is sent.
	r.a.Settings.SendTyping = false
	r.typeText("e")
	if c, _ := r.last(abi.CommandTagComposing); c.Composing.Composing {
		t.Error("composing sent with typing off")
	}
}

// ⌘L (state.Req.FocusComposer) gives the composer the keyboard.
func TestFocusRequest(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.s.Req.FocusComposer = true
	r.Frames(3)
	if r.s.Req.FocusComposer {
		t.Error("the request was not taken")
	}
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleTextField {
		t.Fatal("the composer does not have the keyboard")
	}
	r.typeText("x")
	if r.text() != "x" {
		t.Errorf("typing reached %q", r.text())
	}
}

// CP-022: the hints bar under the composer; its × hides it and the
// keyboard opens the shortcuts.
func TestShortcutHints(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	if !r.hasText("Enter sends · Shift+Enter for a new line · *bold* _italic_ ~strike~ · ⌘+V pastes a picture") {
		t.Fatal("no hints")
	}
	r.Play(widgettest.Click(ui.RoleButton, "All shortcuts (⌘+/)"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogShortcuts {
		t.Error("the keyboard did not open the shortcuts")
	}
	r.s.CloseDialog()
	r.Frames(2)
	h := r.scr.Height(r.scr.env, 800)
	r.Play(widgettest.Click(ui.RoleButton, "Hide shortcut hints (bring them back from Keyboard shortcuts)"), widgettest.Idle(3))
	if r.s.Settings.ShowShortcutHints || r.hasText("Enter sends") {
		t.Error("the hints stay")
	}
	if h2 := r.scr.Height(r.scr.env, 800); h2 >= h {
		t.Errorf("the band kept its height without the hints: %v, %v", h, h2)
	}
}

// CV-032, LK-007: chats we cannot write in show why instead of a composer.
func TestReadOnlyChats(t *testing.T) {
	chats := []abi.Chat{
		{ID: []byte("c1@newsletter"), Name: []byte("News"), Kind: abi.ChatKindBroadcast, LastActivity: 5},
		{ID: []byte("c2@newsletter"), Name: []byte("Old news"), Kind: abi.ChatKindBroadcast, Left: true, LastActivity: 4},
		{ID: []byte("g2@g.us"), Name: []byte("Gone"), Kind: abi.ChatKindGroup, Left: true, LastActivity: 3},
		{ID: []byte("g3@g.us"), Name: []byte("Announcements"), Kind: abi.ChatKindGroup, ReadOnly: true, LastActivity: 2,
			Participants: [][]byte{[]byte(me), []byte(ada)}},
		{ID: []byte("g4@g.us"), Name: []byte("Removed"), Kind: abi.ChatKindGroup, ReadOnly: true, LastActivity: 2,
			Participants: [][]byte{[]byte(ada)}},
		{ID: []byte(ada), Name: []byte("Ada"), Locked: true, LastActivity: 1},
	}
	r := newRig(t, chats, nil)
	for _, tc := range []struct{ jid, says string }{
		{"c1@newsletter", "Channels are read-only in Zap"},
		{"c2@newsletter", "You left this channel"},
		{"g2@g.us", "You left this group"},
		{"g3@g.us", "admins"},
		{"g4@g.us", "You left this group"},
		{ada, "Locked chats are read-only in Zap"},
	} {
		if tc.jid == ada {
			r.a.Mode = state.ListLocked // a locked chat opens only in its folder
		}
		r.open(tc.jid)
		if !r.hasText(tc.says) {
			t.Errorf("%s does not say %q", tc.jid, tc.says)
		}
		if r.has(ui.RoleTextField, "Message") {
			t.Errorf("%s offers a composer", tc.jid)
		}
	}
}

// CP-012: the smiley opens the picker on emoji, and closes it.
func TestSmileyTogglesThePicker(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.Play(widgettest.Click(ui.RoleButton, "Emoji, GIFs, and stickers"), widgettest.Idle(2))
	if r.s.Panes.Picker != state.PickerEmoji {
		t.Fatalf("picker %v", r.s.Panes.Picker)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Emoji, GIFs, and stickers"), widgettest.Idle(2))
	if r.s.Panes.Picker != state.PickerNone {
		t.Errorf("picker %v after the second click", r.s.Panes.Picker)
	}
}
