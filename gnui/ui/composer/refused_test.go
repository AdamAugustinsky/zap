package composer

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// refuse emits the core's refusal of a send to chat.
func (r *rig) refuse(chat string, set func(x *abi.EventSendRefused)) {
	r.t.Helper()
	r.f.Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
		e.SendRefused.Chat = []byte(chat)
		set(&e.SendRefused)
	}))
	r.Frames(3)
}

// CP-021: a refused text comes back into the open chat's empty composer and
// is kept as its draft; text typed since stays, and an edit is not replaced.
func TestRefusedTextComesBack(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.refuse(ada, func(x *abi.EventSendRefused) {
		x.Unsent.Tag = abi.UnsentTagText
		x.Unsent.Text.Text = []byte("see you at 5")
	})
	if r.text() != "see you at 5" || string(r.a.Model.Draft([]byte(ada))) != "see you at 5" {
		t.Fatalf("text %q, draft %q", r.text(), r.a.Model.Draft([]byte(ada)))
	}
	if d, ok := r.last(abi.CommandTagSaveDraft); !ok || !eq(d.SaveDraft.Text, "see you at 5") {
		t.Error("the restored text is not stored")
	}
	if len(r.s.Req.Restore.Chat) != 0 || !r.scr.focused {
		t.Error("the request stayed, or the composer has no keyboard")
	}
	if !r.toasted("Not sent") {
		t.Errorf("toasts %v", r.s.Toasts)
	}

	// Text typed since is kept.
	r.refuse(ada, func(x *abi.EventSendRefused) {
		x.Unsent.Tag = abi.UnsentTagText
		x.Unsent.Text.Text = []byte("older")
	})
	if r.text() != "see you at 5" || len(r.s.Req.Restore.Chat) != 0 {
		t.Errorf("a second refusal replaced the text: %q", r.text())
	}
}

// CP-021: a refused send's reply shows again, unless another reply or an
// edit took its place; a private reply's quote for a chat not on screen
// waits there.
func TestRefusedQuoteComesBack(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"), textMsg("m2", ada, false, 101, "Ho"))
	text := func(x *abi.EventSendRefused) {
		x.Unsent.Tag = abi.UnsentTagText
		x.Unsent.Text.Text = []byte("ok")
	}
	r.refuse(ada, func(x *abi.EventSendRefused) {
		text(x)
		x.Quoting, x.HasQuoting = abi.ReplyTarget{ID: []byte("m1")}, true
	})
	if r.s.Panes.Reply.Len() != 1 || string(r.s.Panes.Reply.At(0)) != "m1" || !r.hasText("Replying to") {
		t.Fatal("the reply did not come back")
	}
	r.s.Panes.Reply.Reset()
	r.s.Panes.Reply.Add([]byte("m2"))
	r.Frames(2)
	r.refuse(ada, func(x *abi.EventSendRefused) {
		text(x)
		x.Quoting, x.HasQuoting = abi.ReplyTarget{ID: []byte("m1")}, true
	})
	if string(r.s.Panes.Reply.At(0)) != "m2" {
		t.Error("a refusal replaced the reply set since")
	}

	// A private reply to Grace, refused while Ada is open, waits in Grace's
	// chat with its quote of the group's message.
	r.refuse(grace, func(x *abi.EventSendRefused) {
		text(x)
		x.Quoting, x.HasQuoting = abi.ReplyTarget{ID: []byte("g7"), Chat: []byte(group), HasChat: true}, true
	})
	r.open(grace)
	p := &r.s.Panes
	if p.Reply.Len() != 1 || string(p.Reply.At(0)) != "g7" || p.ReplyChat.Len() != 1 || string(p.ReplyChat.At(0)) != group {
		t.Errorf("Grace's reply: %d, %d", p.Reply.Len(), p.ReplyChat.Len())
	}
	if string(r.a.Model.Draft([]byte(grace))) != "ok" || r.text() != "ok" {
		t.Errorf("Grace's text %q", r.text())
	}
}

// CP-019: refused files come back as they were staged (documents stay
// documents), with their caption and the reply they quoted; for a chat not
// on screen they wait in it.
func TestRefusedFilesComeBack(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"))
	r.refuse(ada, func(x *abi.EventSendRefused) {
		x.Quoting, x.HasQuoting = abi.ReplyTarget{ID: []byte("m1")}, true
		x.Unsent.Tag = abi.UnsentTagFiles
		x.Unsent.Files = abi.UnsentFiles{Paths: [][]byte{[]byte("/tmp/a.txt"), []byte("/tmp/b.pdf")}, Documents: true,
			Caption: []byte("see these"), HasCaption: true}
	})
	if got := r.chips(); len(got) != 2 || got[0] != "a.txt" || got[1] != "b.pdf" {
		t.Fatalf("chips %q", got)
	}
	if r.s.Staged[0].Kind != state.StagedDocument || r.text() != "see these" {
		t.Errorf("kind %v, caption %q", r.s.Staged[0].Kind, r.text())
	}
	if r.s.Panes.Reply.Len() != 1 || !r.hasText("Replying to") {
		t.Error("the reply did not come back")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	if c, ok := r.last(abi.CommandTagSendFiles); !ok || !c.SendFiles.Documents || !c.SendFiles.HasQuoting || !eq(c.SendFiles.Caption, "see these") {
		t.Fatalf("sent again %+v", c.SendFiles)
	}

	// Refused for Grace while Ada is open: they wait in Grace's chat.
	r.refuse(grace, func(x *abi.EventSendRefused) {
		x.Unsent.Tag = abi.UnsentTagFiles
		x.Unsent.Files = abi.UnsentFiles{Paths: [][]byte{[]byte("/tmp/c.zip")}, Caption: []byte("zip"), HasCaption: true}
	})
	if len(r.chips()) != 0 {
		t.Fatal("Grace's files show in Ada's composer")
	}
	r.open(grace)
	if got := r.chips(); len(got) != 1 || got[0] != "c.zip" || r.text() != "zip" || r.s.Staged[0].Kind != state.StagedFile {
		t.Errorf("Grace's composer: chips %q, text %q", got, r.text())
	}
}

// CP-019: a refused picture comes back staged with its pixels; one for a
// chat that is gone gives them back to the core.
func TestRefusedPictureComesBack(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	put := func() abi.BlobRef {
		ref, err := r.f.PutBlob(make([]byte, 2*2*4))
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	ref := put()
	r.refuse(ada, func(x *abi.EventSendRefused) {
		x.Unsent.Tag = abi.UnsentTagImage
		x.Unsent.Image = abi.UnsentImage{Width: 2, Height: 2, RGBA: ref}
	})
	if got := r.chips(); len(got) != 1 || got[0] != "Photo" {
		t.Fatalf("chips %q", got)
	}
	if st := &r.s.Staged[0]; st.Kind != state.StagedPicture || !st.Ready || st.Blob.ID != ref.ID || st.W != 2 {
		t.Errorf("staged %+v", *st)
	}
	r.key(ui.KeyEscape, 0)
	if _, ok := r.blob(ref); ok {
		t.Error("Escape kept the picture's pixels")
	}

	gone := put()
	r.refuse("9@s.whatsapp.net", func(x *abi.EventSendRefused) {
		x.Unsent.Tag = abi.UnsentTagImage
		x.Unsent.Image = abi.UnsentImage{Width: 2, Height: 2, RGBA: gone}
	})
	if _, ok := r.blob(gone); ok {
		t.Error("the pixels for a chat that is gone stay")
	}
}

// CP-004: quitting stores the open chat's text; a chat closed by any route
// (here not the composer's Escape) stores it at once, once, and its
// attachments wait for it.
func TestQuitAndCloseStoreTheDraft(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.typeText("bye")
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	n := len(r.sent(abi.CommandTagSaveDraft))
	r.a.OpenChat(0)
	if d := r.sent(abi.CommandTagSaveDraft); len(d) != n+1 || !eq(d[n].SaveDraft.Chat, ada) || !eq(d[n].SaveDraft.Text, "bye") {
		t.Fatalf("closing stored %d drafts", len(d)-n)
	}
	r.Frames(3)
	r.open(grace)
	if len(r.sent(abi.CommandTagSaveDraft)) != n+1 {
		t.Errorf("Ada's text was stored %d times", len(r.sent(abi.CommandTagSaveDraft))-n)
	}
	r.open(ada)
	if r.text() != "bye" || len(r.chips()) != 1 {
		t.Errorf("Ada's composer came back with %q, %q", r.text(), r.chips())
	}

	r.typeText(" now")
	r.s.Quit()
	if d, _ := r.last(abi.CommandTagSaveDraft); !eq(d.SaveDraft.Chat, ada) || !eq(d.SaveDraft.Text, "bye now") {
		t.Errorf("quitting stored %q", d.SaveDraft.Text)
	}
}

// CP-003: the placeholder asks for a caption while something is staged.
func TestPlaceholderAsksForACaption(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	if r.scr.hint != "Type a message" {
		t.Fatalf("placeholder %q", r.scr.hint)
	}
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	if r.scr.hint != "Add a caption" {
		t.Errorf("staged: placeholder %q", r.scr.hint)
	}
	r.key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.scr.hint != "Type a message" {
		t.Errorf("unstaged: placeholder %q", r.scr.hint)
	}
}

// MM-007: a private reply quoting a locked group while the locked folder is
// closed hides its quote and sends nothing, text or voice; the reply stays,
// and sends again once the folder is open.
func TestHiddenQuoteSendsNothing(t *testing.T) {
	chats := append(defaultChats(), abi.Chat{ID: []byte("g9@g.us"), Name: []byte("Secret"), Kind: abi.ChatKindGroup,
		GroupSubjectKnown: true, Locked: true, LastActivity: 5})
	r := newRig(t, chats, nil)
	r.open(ada)
	p := &r.s.Panes
	p.Reply.Add([]byte("m9"))
	p.ReplyChat.Add([]byte("g9@g.us"))
	r.Frames(2)
	if !r.s.QuoteHidden(r.a) || !r.hasText("Original group message unavailable") || r.hasText("Replying to") {
		t.Fatal("the locked group's quote shows")
	}
	r.focus()
	r.typeText("hello")
	r.key(ui.KeyEnter, 0)
	if _, ok := r.last(abi.CommandTagSendText); ok {
		t.Fatal("a reply quoting a hidden chat went")
	}
	if r.text() != "hello" || p.Reply.Len() != 1 || !r.toasted("isn’t available on this computer") {
		t.Errorf("text %q, reply %d, toasts %v", r.text(), p.Reply.Len(), r.s.Toasts)
	}

	// A recording waits as a refused voice message, and Send keeps it.
	r.key('A', ui.PrimaryMod())
	r.key(ui.KeyBackspace, 0)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = 2 * time.Second
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	for i := 0; i < 200 && !r.hasText("not sent"); i++ {
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
	}
	if _, ok := r.last(abi.CommandTagSendVoice); ok || !r.hasText("Voice message (0:02) not sent") || p.Reply.Len() != 1 {
		t.Fatalf("the recording: sent %v, reply %d", r.f.SentTags(), p.Reply.Len())
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	if _, ok := r.last(abi.CommandTagSendVoice); ok {
		t.Fatal("the refused voice message went with its quote hidden")
	}

	// The folder opens: the reply sends, quoting the group.
	r.a.Mode = state.ListLocked
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	if c, ok := r.last(abi.CommandTagSendVoice); !ok || !c.SendVoice.HasQuoting || !eq(c.SendVoice.Quoting.Chat, "g9@g.us") {
		t.Fatalf("with the folder open the voice message sent %+v", c.SendVoice)
	}
}

// SH-017: Escape in the composer leaves Starred for the chats before it
// drops the attachments; the chat stays open.
func TestEscapeLeavesStarredFirst(t *testing.T) {
	r := newRig(t, nil, nil)
	r.a.Mode = state.ListStarred
	r.open(ada)
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	r.focus()
	r.key(ui.KeyEscape, 0)
	if r.a.Mode != state.ListChats || len(r.s.Staged) != 1 {
		t.Fatalf("Escape: mode %v, %d staged", r.a.Mode, len(r.s.Staged))
	}
	r.key(ui.KeyEscape, 0)
	if len(r.s.Staged) != 0 || r.a.Open == 0 {
		t.Errorf("the second Escape: %d staged, open %v", len(r.s.Staged), r.a.Open)
	}
}

// SH-017: with the composer focused, Escape takes back one thing at a time
// in upstream's order (zf:src/ui/keys.rs:164): the picker, Starred, the
// staged files, the edit or the reply (starting one drops the other, so they
// never wait together), the chat search's pane, then the chat. The selection
// bar replaces the composer, so the composer is never focused during a
// selection.
func TestEscapeOrderWithTheComposerFocused(t *testing.T) {
	for _, edit := range []bool{true, false} {
		r := newRig(t, nil, nil)
		r.a.Mode = state.ListStarred
		r.open(ada)
		r.messages(ada, textMsg("m1", me, true, r.now.Unix()-60, "mine"), textMsg("m2", ada, false, r.now.Unix()-30, "theirs"))
		r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
		r.focus()
		p := &r.s.Panes
		if edit {
			p.Edit.Add([]byte("m1"))
		} else {
			p.Reply.Add([]byte("m2"))
		}
		p.Pane = state.PaneSearch
		p.Picker = state.PickerEmoji
		r.Frames(2)
		steps := []struct {
			name string
			gone func() bool
		}{
			{"picker", func() bool { return p.Picker == state.PickerNone }},
			{"Starred", func() bool { return r.a.Mode == state.ListChats }},
			{"staged files", func() bool { return len(r.s.Staged) == 0 }},
			{"edit or reply", func() bool { return p.Edit.Len() == 0 && p.Reply.Len() == 0 }},
			{"pane", func() bool { return p.Pane == state.PaneNone }},
			{"chat", func() bool { return r.a.Open == 0 }},
		}
		for i, st := range steps {
			r.key(ui.KeyEscape, 0)
			for j, o := range steps {
				if got, want := o.gone(), j <= i; got != want {
					t.Fatalf("edit %v, after Escape %d (%s): %s gone %v, want %v", edit, i+1, st.name, o.name, got, want)
				}
			}
		}
	}
}
