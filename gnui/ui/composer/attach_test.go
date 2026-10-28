package composer

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// dropAt drops data of kind on the conversation: the drop target takes
// it there, and what it was given is staged.
func (r *rig) dropAt(kind ui.DropKinds, data []byte, items int) {
	r.t.Helper()
	if _, accept, _ := r.Context().DropAt(700, 300, kind, ui.DropAllOps); accept&kind == 0 {
		r.t.Fatal("the conversation takes no drop there")
	}
	r.before(func(c *ui.Context) { r.scr.dropped(r.scr.env, ui.DropData{Kind: kind, Data: data, Items: items}) })
	r.Frames(2)
}

// chips are the attachment row's names.
func (r *rig) chips() []string {
	t := r.Tree()
	var out []string
	for _, n := range t.Nodes {
		if n.Role == ui.RoleListItem && n.Parent >= 0 && t.Nodes[n.Parent].Role == ui.RoleList && string(t.Text(t.Nodes[n.Parent].Name)) == "Attachments" {
			out = append(out, string(t.Text(n.Name)))
		}
	}
	return out
}

func pngOf(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// waitPictures runs frames until the staged pictures are read.
func (r *rig) waitPictures() {
	r.t.Helper()
	for i := 0; i < 400 && !r.scr.picturesReady(); i++ {
		time.Sleep(2 * time.Millisecond)
		r.Frames(1)
	}
	if !r.scr.picturesReady() {
		r.t.Fatal("the pictures were never read")
	}
	r.Frames(1)
}

// CP-016, CP-018: files dropped on the chat wait above the text with close
// buttons; the text is their caption; the send key sends them at once, the
// caption and the reply with the first; Escape drops them (CP-016).
func TestDroppedFilesAreStagedAndSent(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Send me the notes"))
	r.dropAt(ui.DropFiles, []byte("/tmp/notes.pdf\x00/tmp/plan.png\x00"), 2)
	if got := r.chips(); len(got) != 2 || got[0] != "notes.pdf" || got[1] != "plan.png" {
		t.Fatalf("chips %q", got)
	}
	if !r.has(ui.RoleButton, "Remove notes.pdf") {
		t.Error("a chip has no close button")
	}
	if r.has(ui.RoleButton, "Record a voice message") {
		t.Error("the microphone shows with attachments staged")
	}
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	r.focus()
	r.typeText("here")
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagSendFiles)
	if !ok || len(c.SendFiles.Paths) != 2 || !eq(c.SendFiles.Paths[0], "/tmp/notes.pdf") || c.SendFiles.Documents ||
		!c.SendFiles.HasCaption || !eq(c.SendFiles.Caption, "here") || !c.SendFiles.HasQuoting || !eq(c.SendFiles.Quoting.ID, "m1") {
		t.Fatalf("sent %v: %+v", r.f.SentTags(), c.SendFiles)
	}
	if _, ok := r.last(abi.CommandTagSendText); ok {
		t.Error("the caption went out as a text too")
	}
	if len(r.chips()) != 0 || r.text() != "" || r.s.Panes.Reply.Len() != 0 {
		t.Error("the composer kept what it sent")
	}
	if len(r.s.Toasts) == 0 || string(r.s.Toasts[0].Text) != "Sending 2 files…" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
	r.s.Toasts = r.s.Toasts[:0] // the toast floats over the composer
	r.Frames(2)
	// Escape drops staged files; a chip's close button drops one.
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00/tmp/b.txt\x00"), 2)
	r.Play(widgettest.Click(ui.RoleButton, "Remove a.txt"), widgettest.Idle(2))
	if got := r.chips(); len(got) != 1 || got[0] != "b.txt" {
		t.Fatalf("after remove: %q", got)
	}
	r.focus()
	r.key(ui.KeyEscape, 0)
	if len(r.chips()) != 0 {
		t.Error("Escape left the attachments")
	}
	if r.a.Open == 0 {
		t.Error("the Escape that dropped the attachments closed the chat")
	}
}

// CP-017, CP-018: a dropped or pasted picture goes as a photo, its pixels
// in a core blob, with the caption; files after it go without.
func TestPicturesGoAsPhotos(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.dropAt(ui.DropImage, pngOf(3, 2), 1)
	r.before(func(c *ui.Context) {
		r.scr.pasted(r.scr.env, app.Paste{Kind: platform.PasteFiles, Data: []byte("/tmp/x.zip\x00"), Files: 1})
	})
	r.Frames(1)
	if got := r.chips(); len(got) != 2 || got[0] != "Photo" || got[1] != "x.zip" {
		t.Fatalf("chips %q", got)
	}
	r.focus()
	r.typeText("look")
	r.key(ui.KeyEnter, 0) // straight away: the send waits for the picture's pixels
	r.waitPictures()
	r.Frames(2)
	im, ok := r.last(abi.CommandTagSendImage)
	if !ok || im.SendImage.Width != 3 || im.SendImage.Height != 2 || im.SendImage.RGBA.Len != 3*2*4 || !eq(im.SendImage.Caption, "look") {
		t.Fatalf("sent %v: %+v", r.f.SentTags(), im.SendImage)
	}
	px := make([]byte, 24)
	if n, _ := r.f.ReadBlob(im.SendImage.RGBA.ID, 0, px); n != 24 || px[0] != 255 || px[3] != 255 || px[4] != 0x80 {
		t.Errorf("the blob holds %v", px[:n])
	}
	f, ok := r.last(abi.CommandTagSendFiles)
	if !ok || f.SendFiles.HasCaption || len(f.SendFiles.Paths) != 1 || !eq(f.SendFiles.Paths[0], "/tmp/x.zip") {
		t.Fatalf("the file went %+v", f.SendFiles)
	}
}

// CP-014: nothing is staged while the picker is open; a paste that found
// nothing stages nothing and toasts nothing (CP-015 is a log line).
func TestNothingIsStagedWhileThePickerIsOpen(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.s.Panes.Picker = state.PickerEmoji
	r.Frames(1)
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	if len(r.chips()) != 0 {
		t.Error("a drop staged with the picker open")
	}
	r.s.Panes.Picker = state.PickerNone
	r.before(func(c *ui.Context) { r.scr.pasted(r.scr.env, app.Paste{Failed: true}) })
	r.Frames(1)
	if len(r.chips()) != 0 || len(r.s.Toasts) != 0 {
		t.Error("a failed paste staged or toasted")
	}
}

// CP-011, CP-013, CP-017: the plus menu's items; Send as document asks the
// core's file dialog for documents, whose answer is staged as documents.
func TestPlusMenu(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.Play(widgettest.Click(ui.RoleButton, "Attach"), widgettest.Idle(2))
	for _, n := range []string{"Send files", "Send as document", "Send location", "Create poll"} {
		if !r.has(ui.RoleButton, n) {
			t.Fatalf("the plus menu has no %q:\n%s", n, widgettest.Dump(r.Tree()))
		}
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send as document"), widgettest.Idle(2))
	c, ok := r.last(abi.CommandTagPickFiles)
	if !ok || !c.PickFiles.Documents || !eq(c.PickFiles.Chat, ada) {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.has(ui.RoleButton, "Send files") {
		t.Error("the menu stays open")
	}
	r.f.Emit(ev(abi.EventTagPicked, func(e *abi.Event) {
		e.Picked = abi.EventPicked{Chat: []byte(ada), Paths: [][]byte{[]byte("/tmp/photo.jpg")}, Documents: true}
	}))
	r.Frames(3)
	if got := r.chips(); len(got) != 1 || got[0] != "photo.jpg" {
		t.Fatalf("chips %q", got)
	}
	r.focus()
	r.key(ui.KeyEnter, 0)
	if f, ok := r.last(abi.CommandTagSendFiles); !ok || !f.SendFiles.Documents || f.SendFiles.HasCaption {
		t.Fatalf("the document went %+v", f.SendFiles)
	}
	r.s.Toasts = r.s.Toasts[:0]
	r.Frames(2) // while the toast fades
	r.Play(widgettest.Click(ui.RoleButton, "Attach"), widgettest.Idle(2), widgettest.Click(ui.RoleButton, "Create poll"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogCreatePoll || r.s.Dialog.Chat != r.ref(ada) {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
	r.s.CloseDialog()
	r.Play(widgettest.Click(ui.RoleButton, "Attach"), widgettest.Idle(2), widgettest.Click(ui.RoleButton, "Send location"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogSendLocation {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
}

// CP-019 (in part): files picked for a chat that is not open wait in its
// draft, and staged files stay with their chat across switches.
func TestStagedFilesStayWithTheirChat(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	r.open(grace)
	if len(r.chips()) != 0 {
		t.Fatal("Ada's file followed to Grace")
	}
	r.f.Emit(ev(abi.EventTagPicked, func(e *abi.Event) {
		e.Picked = abi.EventPicked{Chat: []byte(ada), Paths: [][]byte{[]byte("/tmp/b.txt")}}
	}))
	r.Frames(2)
	if len(r.chips()) != 0 {
		t.Fatal("a file picked for Ada was staged for Grace")
	}
	r.open(ada)
	if got := r.chips(); len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Errorf("Ada's chips %q", got)
	}
}

// The band grows with what it holds: a reply strip and attachments.
func TestTheBandGrows(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"))
	h0 := r.scr.Height(r.scr.env, 800)
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(3)
	h1 := r.scr.Height(r.scr.env, 800)
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	r.Frames(2)
	h2 := r.scr.Height(r.scr.env, 800)
	if !(h0 < h1 && h1 < h2) {
		t.Errorf("heights %v, %v, %v", h0, h1, h2)
	}
	// The field sits inside the window, above its foot.
	field, _ := r.Find(ui.RoleGroup, "Message")
	if field.Bounds[3] > 720 || field.Bounds[1] < 720-h2 {
		t.Errorf("the field %v is outside the band of %v", field.Bounds, h2)
	}
}
