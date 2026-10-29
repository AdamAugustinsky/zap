package composer

import (
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

func TestAccountSwitchBeforeFirstComposer(t *testing.T) {
	r := newRig(t, nil, nil)
	f := fake.New(nil)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Last: true, Chats: defaultChats()} }))
	b := r.s.AddAccount("2", nil, f)
	r.Frames(3)
	r.a.Model.SetDraft([]byte(ada), []byte("first account"))
	b.Model.SetDraft([]byte(ada), []byte("second account"))
	r.s.SwitchAccount(1)
	b.OpenChat(b.Model.Chats.Find([]byte(ada)))
	r.Frames(3)
	if r.text() != "second account" {
		t.Fatalf("first composer holds %q", r.text())
	}
	r.s.SwitchAccount(0)
	r.open(ada)
	if r.text() != "first account" || string(b.Model.Draft([]byte(ada))) != "second account" {
		t.Fatal("switching accounts mixed their drafts")
	}
}

// The picker opens above the emoji button, which the composer reports every
// frame; the emoji chosen there goes in at the caret and counts as used.
func TestPickerAnchorAndInsert(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	btn := r.Must(ui.RoleButton, "Emoji, GIFs, and stickers")
	a := r.s.Panes.PickerAnchor
	if a.Empty() || a.X != btn.Bounds[0] || a.Y != btn.Bounds[1] || a.X+a.W != btn.Bounds[2] || a.Y+a.H != btn.Bounds[3] {
		t.Fatalf("the anchor is %+v, the button %v", a, btn.Bounds)
	}
	r.focus()
	r.typeText("so ")
	r.s.InsertEmoji([]byte("🎉"))
	r.Frames(2)
	if r.text() != "so 🎉" {
		t.Fatalf("the composer holds %q", r.text())
	}
	if len(r.s.Settings.RecentEmoji) == 0 || string(r.s.Settings.RecentEmoji[0]) != "🎉" {
		t.Errorf("recent emoji %q", r.s.Settings.RecentEmoji)
	}
	if len(r.s.Req.Insert) != 0 || r.s.Req.FocusComposer {
		t.Error("the insert was not taken")
	}
	if string(r.a.Model.Draft([]byte(ada))) != "so 🎉" {
		t.Errorf("the draft is %q", r.a.Model.Draft([]byte(ada)))
	}
}

// LN-003: a link's text goes into an empty composer of its chat, never over
// a draft, an edit or a reply; untouched, it is not kept as a draft, and an
// edit of it is the reader's.
func TestLinkPrefill(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(grace)
	r.a.StartChat([]byte(ada), []byte("Ada"), []byte("Hello from the site"))
	r.Frames(3)
	if r.text() != "Hello from the site" || len(r.s.Req.Prefill.Chat) != 0 {
		t.Fatalf("the composer holds %q (request %q)", r.text(), r.s.Req.Prefill.Chat)
	}
	if len(r.a.Model.Draft([]byte(ada))) != 0 {
		t.Error("the link's text became a draft")
	}
	r.open(grace)
	if d, ok := r.last(abi.CommandTagSaveDraft); ok && eq(d.SaveDraft.Chat, ada) {
		t.Errorf("leaving stored %q for Ada", d.SaveDraft.Text)
	}
	r.open(ada)
	if r.text() != "" {
		t.Fatalf("the untouched link text came back: %q", r.text())
	}

	// A draft stays.
	r.focus()
	r.typeText("my own words")
	r.open(grace)
	r.a.StartChat([]byte(ada), []byte("Ada"), []byte("Template"))
	r.Frames(3)
	if r.text() != "my own words" || len(r.s.Req.Prefill.Chat) != 0 {
		t.Fatalf("the draft became %q", r.text())
	}

	// An edit of the link's text is the reader's.
	r.focus()
	r.Play(widgettest.Key('A', ui.PrimaryMod()), widgettest.Key(ui.KeyBackspace, 0), widgettest.Idle(2))
	if r.text() != "" {
		t.Fatalf("could not empty the composer: %q", r.text())
	}
	r.open(grace)
	r.a.StartChat([]byte(ada), []byte("Ada"), []byte("Hi"))
	r.Frames(3)
	r.focus()
	r.typeText(" there")
	r.open(grace)
	if d, ok := r.last(abi.CommandTagSaveDraft); !ok || !eq(d.SaveDraft.Chat, ada) || !eq(d.SaveDraft.Text, "Hi there") {
		t.Errorf("the changed link text was stored as %q", d.SaveDraft.Text)
	}

	// Not over a reply.
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Question?"))
	r.focus()
	r.Play(widgettest.Key('A', ui.PrimaryMod()), widgettest.Key(ui.KeyBackspace, 0), widgettest.Idle(2))
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	r.s.PrefillComposer([]byte(ada), []byte("Template"))
	r.Frames(2)
	if r.text() != "" || len(r.s.Req.Prefill.Chat) != 0 {
		t.Errorf("a link's text went over a reply: %q", r.text())
	}
}

// A link's text for a chat the core is still making waits for it to open;
// one for a chat that exists and did not open is dropped.
func TestPrefillWaitsForANewChat(t *testing.T) {
	const bob = "3@s.whatsapp.net"
	r := newRig(t, nil, nil)
	r.open(grace)
	r.a.StartChat([]byte(bob), []byte("Bob"), []byte("Hey Bob"))
	if _, ok := r.last(abi.CommandTagEnsureChat); !ok {
		t.Fatal("no EnsureChat")
	}
	r.Frames(3)
	if r.text() != "" || len(r.s.Req.Prefill.Chat) == 0 {
		t.Fatalf("Grace's composer took Bob's text, or it was dropped: %q", r.text())
	}
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(bob), Name: []byte("Bob"), LastActivity: 40}
	}))
	r.Frames(4)
	if ch, _ := r.a.OpenChatView(); string(ch.ID()) != bob || r.text() != "Hey Bob" {
		t.Fatalf("Bob's chat is open: %v, holding %q", string(ch.ID()) == bob, r.text())
	}

	r.open(grace)
	r.s.PrefillComposer([]byte(ada), []byte("For Ada"))
	r.Frames(2)
	if len(r.s.Req.Prefill.Chat) != 0 {
		t.Fatal("text for a chat that did not open still waits")
	}
	r.open(ada)
	if r.text() != "" {
		t.Errorf("Ada's composer took stale text: %q", r.text())
	}
}

// A private reply opens the sender's chat quoting the group's message
// (Panes.ReplyChat): the strip names the sender and the send quotes the
// message in its chat; without the message the strip says so, and the send
// still quotes it.
func TestPrivateReply(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.messages(group, textMsg("g7", ada, false, 100, "Who has the notes?"))
	// As the conversation's "Reply privately" does it, in one frame.
	r.before(func(*ui.Context) {
		r.a.OpenChat(r.ref(ada))
		r.s.Panes.Reply.Add([]byte("g7"))
		r.s.Panes.ReplyChat.Add([]byte(group))
	})
	r.Frames(2)
	if !r.hasText("Replying to Ada Lovelace") || !r.hasText("Who has the notes?") {
		t.Fatal("no reply strip for the group's message")
	}
	r.focus()
	r.typeText("I do")
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagSendText)
	if !ok || !eq(c.SendText.Chat, ada) || !c.SendText.HasQuoting || !eq(c.SendText.Quoting.ID, "g7") ||
		!c.SendText.Quoting.HasChat || !eq(c.SendText.Quoting.Chat, group) {
		t.Fatalf("sent %+v quoting %+v", c.SendText.Chat, c.SendText.Quoting)
	}
	if r.s.Panes.Reply.Len() != 0 || r.s.Panes.ReplyChat.Len() != 0 {
		t.Error("the reply stayed after the send")
	}

	r.s.Panes.Reply.Add([]byte("gone"))
	r.s.Panes.ReplyChat.Add([]byte(group))
	r.Frames(2)
	if !r.hasText("Original group message unavailable") {
		t.Fatal("no strip for a message that cannot be shown")
	}
	r.typeText("still")
	r.key(ui.KeyEnter, 0)
	if c, _ := r.last(abi.CommandTagSendText); !eq(c.SendText.Quoting.ID, "gone") || !eq(c.SendText.Quoting.Chat, group) {
		t.Errorf("the unavailable reply went as %+v", c.SendText.Quoting)
	}
}

// The text is the model's draft as it is typed (the chat list shows it once
// the chat closes), and goes to the archive when the composer closes the
// chat; the lock stores it too, but not an edit's text.
func TestDraftsLiveInTheModel(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.typeText("on my way")
	if string(r.a.Model.Draft([]byte(ada))) != "on my way" {
		t.Fatalf("the model's draft is %q", r.a.Model.Draft([]byte(ada)))
	}
	r.s.Locked()
	if d, ok := r.last(abi.CommandTagSaveDraft); !ok || !eq(d.SaveDraft.Chat, ada) || !eq(d.SaveDraft.Text, "on my way") {
		t.Fatalf("the lock stored %q", d.SaveDraft.Text)
	}
	if r.s.InUse() {
		t.Error("in use without a recording")
	}
	r.typeText("!")
	// the first Escape lets go of the composer, the second closes the chat
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3), widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	if r.a.Open != 0 {
		t.Fatal("Escape did not close the chat")
	}
	if d, _ := r.last(abi.CommandTagSaveDraft); !eq(d.SaveDraft.Text, "on my way!") {
		t.Errorf("closing stored %q", d.SaveDraft.Text)
	}

	// An edit replaces the draft, and is never stored as one.
	r.open(ada)
	r.messages(ada, textMsg("m1", me, true, r.now.Unix()-60, "sent text"))
	r.s.Panes.Edit.Add([]byte("m1"))
	r.Frames(2)
	n := len(r.sent(abi.CommandTagSaveDraft))
	if len(r.a.Model.Draft([]byte(ada))) != 0 {
		t.Errorf("the draft stayed under the edit: %q", r.a.Model.Draft([]byte(ada)))
	}
	r.s.Locked()
	if len(r.sent(abi.CommandTagSaveDraft)) != n {
		t.Error("the lock stored the edit's text")
	}
}

// Staged attachments are the state's (the cropper finds them by ID): a
// click on a picture opens the cropper on its ID alone, the file's path
// being the attachment's, and the cropped file takes its place; a document
// has no crop.
func TestCroppingAStagedPicture(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.dropAt(ui.DropFiles, []byte("/tmp/notes.txt\x00/tmp/cat.png\x00"), 2)
	if len(r.s.Staged) != 2 || r.s.Staged[1].Kind != state.StagedFile || r.s.StagedByID(r.s.Staged[1].ID) == nil {
		t.Fatalf("staged %+v", r.s.Staged)
	}
	if r.has(ui.RoleButton, "notes.txt") {
		t.Error("a document can be cropped")
	}
	id := r.s.Staged[1].ID
	r.Play(widgettest.Settle(), widgettest.Click(ui.RoleButton, "cat.png"), widgettest.Idle(2))
	d := r.s.Dialog
	if d.Kind != state.DialogCropper || uint64(d.N) != id || len(d.Path) != 0 || len(d.Text) != 0 {
		t.Fatalf("dialog %+v", d)
	}
	if st := r.s.StagedByID(id); st == nil || string(st.Path) != "/tmp/cat.png" {
		t.Fatalf("the cropper would not find the file: %+v", st)
	}
	r.s.CloseDialog()
	r.f.Emit(ev(abi.EventTagPictureEdited, func(e *abi.Event) {
		e.PictureEdited.Target = id
		e.PictureEdited.Result = abi.PathResult{Tag: abi.PathResultTagOk, Ok: abi.PathResultOk{Path: []byte("/tmp/cat-cropped.jpg")}}
	}))
	r.Frames(2)
	if st := r.s.StagedByID(id); st == nil || string(st.Path) != "/tmp/cat-cropped.jpg" {
		t.Fatalf("the cropped file did not take its place: %+v", st)
	}
	r.focus()
	r.key(ui.KeyEnter, 0)
	f, _ := r.last(abi.CommandTagSendFiles)
	if len(f.SendFiles.Paths) != 2 || !eq(f.SendFiles.Paths[1], "/tmp/cat-cropped.jpg") {
		t.Errorf("sent %q", f.SendFiles.Paths)
	}
	if len(r.s.Staged) != 0 {
		t.Error("the attachments stayed after the send")
	}
}

// A pasted picture opens the cropper on its ID at once; the cropper reads
// its pixels from the attachment's blob, which holds them until the cropped
// file replaces it and the blob goes.
func TestCroppingAPastedPicture(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.Frames(30) // nothing animates any more
	r.before(func(*ui.Context) {
		r.scr.pasted(r.scr.env, app.Paste{Kind: platform.PasteImage, Data: pngOf(3, 2)})
		// The worker reads the picture before this frame's build takes it:
		// staged and ready in the same frame, which once left the crop
		// button where the chip had been.
		for i := 0; len(r.scr.paste.done) == 0 && i < 2000; i++ {
			time.Sleep(time.Millisecond)
		}
	})
	if !r.scr.picturesReady() {
		t.Fatal("the picture was not read in the frame it was staged")
	}
	st := r.s.Staged[0]
	if st.Kind != state.StagedPicture || !st.Ready || st.W != 3 || st.H != 2 {
		t.Fatalf("staged %+v", st)
	}
	// The band is laid out with the last frame's height and the crop button
	// placed from the last frame's layout: the window asks for frames until
	// both caught up, and rests only with the button over the chip (it once
	// rested with the band at its old height and no button).
	for i := 0; r.Context().Animating(); i++ {
		if i == 30 {
			t.Fatal("the window never rests")
		}
		r.Frame()
	}
	b, _ := r.Find(ui.RoleButton, "Photo")
	chip, _ := r.Find(ui.RoleListItem, "Photo")
	if b.Bounds != chip.Bounds && (b.Bounds[0] != chip.Bounds[0] || b.Bounds[1] != chip.Bounds[1] || b.Bounds[3] != chip.Bounds[3]) {
		t.Fatalf("at rest the crop button is at %v, its chip at %v", b.Bounds, chip.Bounds)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Photo"), widgettest.Idle(1))
	d := r.s.Dialog
	if d.Kind != state.DialogCropper || uint64(d.N) != st.ID || len(d.Path) != 0 || len(d.Text) != 0 {
		t.Fatalf("dialog %v %d %q %q", d.Kind, d.N, d.Path, d.Text)
	}
	got := r.s.StagedByID(st.ID)
	if got == nil || !got.Ready || got.W != 3 || got.H != 2 || got.Blob != st.Blob || got.Blob.Len != 3*2*4 {
		t.Fatalf("the cropper would not find the pixels: %+v", got)
	}
	pix := make([]byte, got.Blob.Len)
	if n, err := r.f.ReadBlob(got.Blob.ID, 0, pix); err != nil || n != len(pix) ||
		pix[0] != 255 || pix[1] != 0 || pix[2] != 0 || pix[3] != 255 || pix[4] != 0x80 {
		t.Fatalf("the blob holds %d bytes %v (%v)", n, pix, err)
	}
	r.s.CloseDialog()
	r.f.Emit(ev(abi.EventTagPictureEdited, func(e *abi.Event) {
		e.PictureEdited.Target = st.ID
		e.PictureEdited.Result = abi.PathResult{Tag: abi.PathResultTagOk, Ok: abi.PathResultOk{Path: []byte("/tmp/edit.jpg")}}
	}))
	r.Frames(2)
	if got := r.s.StagedByID(st.ID); got == nil || got.Kind != state.StagedFile || string(got.Path) != "/tmp/edit.jpg" {
		t.Fatalf("now staged %+v", got)
	}
	if n, _ := r.f.ReadBlob(st.Blob.ID, 0, make([]byte, 1)); n != 0 {
		t.Error("the pasted pixels were not freed")
	}
}

// selBar is a conversation slot with a selection bar.
type selBar struct{ bars int }

func (b *selBar) Init(*screen.Env) error                              { return nil }
func (b *selBar) Build(e *screen.Env, r screen.Rect)                  { e.Placeholder(r, "Transcript") }
func (b *selBar) Toolbar(*screen.Env, *rexui.Toolbar)                 {}
func (b *selBar) SelectionBarHeight(e *screen.Env, _ float32) float32 { return e.Pt(52) }
func (b *selBar) BuildSelectionBar(e *screen.Env, r screen.Rect) {
	b.bars++
	e.Placeholder(r, "Selection bar")
}

// SL-005: in selection mode the conversation's bar takes the composer's
// place; the composer comes back as it was.
func TestSelectionBarTakesThePlace(t *testing.T) {
	bar := &selBar{}
	r := newRigWith(t, nil, nil, func(set *screen.Set) { set.Conversation = bar })
	r.open(ada)
	r.focus()
	r.typeText("kept")
	r.s.Panes.Selecting = true
	r.Frames(3)
	if bar.bars == 0 || r.has(ui.RoleTextField, "Message") || r.has(ui.RoleButton, "Emoji, GIFs, and stickers") {
		t.Fatal("the composer shows beside the selection bar")
	}
	r.s.Panes.Selecting = false
	r.Frames(3)
	if !r.has(ui.RoleTextField, "Message") || r.text() != "kept" {
		t.Fatalf("the composer came back holding %q", r.text())
	}

	// Up as the bar goes is not the composer's (it had no keyboard then).
	r.messages(ada, textMsg("m1", me, true, r.now.Unix()-60, "sent text"))
	r.Play(widgettest.Key('A', ui.PrimaryMod()), widgettest.Key(ui.KeyBackspace, 0), widgettest.Idle(2))
	r.s.Panes.Selecting = true
	r.Frames(3)
	r.s.Panes.Selecting = false
	r.Play(widgettest.Key(ui.KeyUp, 0), widgettest.Idle(2))
	if r.s.Panes.Edit.Len() != 0 {
		t.Error("Up on the bar's last frame started an edit")
	}
}
