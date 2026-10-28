package cropper

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// stagePasted stages a pasted picture as the composer does: its pixels are a
// blob the core holds.
func (r *rig) stagePasted(id uint64, w, h int) {
	r.t.Helper()
	px := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		px[i*4], px[i*4+1], px[i*4+2], px[i*4+3] = byte(i), byte(i>>8), 7, 255
	}
	ref, err := r.f.PutBlob(px)
	if err != nil {
		r.t.Fatal(err)
	}
	r.s.Staged = append(r.s.Staged, state.Staged{ID: id, Kind: state.StagedPicture, W: uint32(w), H: uint32(h), Blob: ref, Ready: true})
}

// stageFile stages a picture file.
func (r *rig) stageFile(id uint64, path string) {
	r.s.Staged = append(r.s.Staged, state.Staged{ID: id, Kind: state.StagedFile, Path: []byte(path), Name: path, Ready: true})
}

// waitFor plays frames until the control shows, for the work that finishes off
// the main thread.
func (r *rig) waitFor(name string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !r.has(ui.RoleButton, name) && time.Now().Before(deadline) {
		r.Frames(1)
		time.Sleep(2 * time.Millisecond)
	}
	if !r.has(ui.RoleButton, name) {
		r.t.Fatalf("no %q\n%s", name, widgettest.Dump(r.Tree()))
	}
}

// openPasted stages a pasted picture as attachment 3 and opens the cropper on
// it.
func (r *rig) openPasted(w, h int) {
	r.t.Helper()
	if r.s.StagedByID(3) == nil {
		r.stagePasted(3, w, h)
	}
	r.open(state.Dialog{Kind: state.DialogCropper, N: 3})
	r.waitFor("Keep this crop")
}

func (r *rig) applied() []abi.CommandApplyPictureEdit {
	var out []abi.CommandApplyPictureEdit
	for _, c := range r.sent(abi.CommandTagApplyPictureEdit) {
		out = append(out, c.ApplyPictureEdit)
	}
	return out
}

// CR-001: a staged picture opens over the window with the controls.
func TestOpensWithItsControls(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(800, 600)
	for _, c := range []struct {
		role ui.Role
		name string
	}{
		{ui.RoleButton, "Keep this crop"}, {ui.RoleButton, "Cancel (Esc)"}, {ui.RoleButton, "Turn left"},
		{ui.RoleButton, "Turn right"}, {ui.RoleButton, "Reset"},
	} {
		if !r.has(c.role, c.name) {
			t.Errorf("no %q\n%s", c.name, widgettest.Dump(r.Tree()))
		}
	}
}

// CR-005, CR-007: keeping a pasted picture sends its pixels, the crop and the
// turns; the dialog closes and the original is left to the core.
func TestKeepSendsThePastedPicture(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(100, 60)
	r.click(ui.RoleButton, "Keep this crop")
	got := r.applied()
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	ap := got[0]
	if ap.Target != 3 || ap.Width != 100 || ap.Height != 60 || ap.Turns != 0 ||
		ap.Crop != (abi.PictureCrop{X: 0, Y: 0, Width: 100, Height: 60}) {
		t.Errorf("sent %+v", ap)
	}
	if ap.Source.Tag != abi.PictureSourceTagPasted || ap.Source.Pasted.RGBA.Len != 100*60*4 {
		t.Fatalf("source %+v", ap.Source)
	}
	buf := make([]byte, 100*60*4)
	if n, err := r.f.ReadBlob(ap.Source.Pasted.RGBA.ID, 0, buf); err != nil || n != len(buf) || buf[4] != 1 {
		t.Errorf("the blob holds %d bytes (%v), pixel 1 = %d", n, err, buf[4])
	}
	if r.s.Dialog.Open() {
		t.Error("the cropper stays open after Keep")
	}
}

// CR-004: Enter keeps; Escape and the close button leave the picture as it was.
func TestEnterKeepsAndEscapeLeaves(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(100, 60)
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.s.Dialog.Open() || len(r.applied()) != 0 {
		t.Fatalf("Escape: open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
	r.openPasted(100, 60)
	r.click(ui.RoleButton, "Cancel (Esc)")
	if r.s.Dialog.Open() || len(r.applied()) != 0 {
		t.Fatalf("close: open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
	r.openPasted(100, 60)
	r.Key(ui.KeyEnter, 0)
	r.Frames(2)
	if r.s.Dialog.Open() || len(r.applied()) != 1 {
		t.Fatalf("Enter: open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
}

// CR-003: Turn right and Turn left turn the picture a quarter at a time, the
// crop with it; Reset restores it.
func TestTurnAndReset(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(100, 60)
	r.click(ui.RoleButton, "Turn right")
	r.click(ui.RoleButton, "Keep this crop")
	ap := r.applied()[0]
	if ap.Turns != 1 || ap.Width != 100 || ap.Height != 60 || ap.Crop != (abi.PictureCrop{Width: 60, Height: 100}) {
		t.Errorf("turned right: %+v", ap)
	}

	r.openPasted(100, 60)
	r.click(ui.RoleButton, "Turn left")
	r.click(ui.RoleButton, "Keep this crop")
	if ap := r.applied()[1]; ap.Turns != 3 {
		t.Errorf("turned left: %+v", ap)
	}

	r.openPasted(100, 60)
	r.click(ui.RoleButton, "Turn right")
	r.click(ui.RoleButton, "Reset")
	r.click(ui.RoleButton, "Keep this crop")
	if ap := r.applied()[2]; ap.Turns != 0 || ap.Crop != (abi.PictureCrop{Width: 100, Height: 60}) {
		t.Errorf("reset: %+v", ap)
	}
}

// CR-002: dragging an edge moves that side; dragging the inside moves the
// whole region.
func TestDragTheCrop(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(800, 600)
	r.Play(widgettest.Drag(ui.RoleGroup, "Right edge", -10, 10))
	r.Frames(2)
	r.click(ui.RoleButton, "Keep this crop")
	ap := r.applied()[0]
	if ap.Crop.X != 0 || ap.Crop.Y != 0 || ap.Crop.Height != 600 || ap.Crop.Width >= 760 || ap.Crop.Width < 600 {
		t.Errorf("dragged the right edge: %+v", ap.Crop)
	}

	r.openPasted(800, 600)
	r.Play(widgettest.Drag(ui.RoleGroup, "Left edge", 10, 10))
	r.Frames(2)
	r.Play(widgettest.Drag(ui.RoleGroup, "Crop", 3, 10))
	r.Frames(2)
	r.click(ui.RoleButton, "Keep this crop")
	ap = r.applied()[1]
	if ap.Crop.X == 0 || ap.Crop.X+ap.Crop.Width > 800 || ap.Crop.Width < 600 || ap.Crop.Width > 720 {
		t.Errorf("dragged the left edge, then the inside: %+v", ap.Crop)
	}
}

// CR-001, CR-005, CR-006: a staged file is inspected first; the cropper opens
// when the core has its size, keeps the file as the source, and reopening it
// after the core has written the crop starts from the original with the crop.
func TestFileIsInspectedThenOriginalIsKept(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.stageFile(7, "/tmp/photo.jpg")
	r.open(state.Dialog{Kind: state.DialogCropper, N: 7})
	insp := r.sent(abi.CommandTagInspectPicture)
	if len(insp) != 1 || insp[0].InspectPicture.Target != 7 || string(insp[0].InspectPicture.Path) != "/tmp/photo.jpg" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.has(ui.RoleButton, "Keep this crop") {
		t.Fatal("the cropper opened before the core answered")
	}
	// An answer for another picture does not open it.
	r.f.Emit(&abi.Event{Tag: abi.EventTagPictureInspected, PictureInspected: abi.EventPictureInspected{
		Target: 9, Result: abi.SizeResult{Tag: abi.SizeResultTagOk, Ok: abi.SizeResultOk{Width: 10, Height: 10}}}})
	r.Frames(2)
	if r.has(ui.RoleButton, "Keep this crop") {
		t.Fatal("an answer for another picture opened it")
	}
	r.f.Emit(&abi.Event{Tag: abi.EventTagPictureInspected, PictureInspected: abi.EventPictureInspected{
		Target: 7, Result: abi.SizeResult{Tag: abi.SizeResultTagOk, Ok: abi.SizeResultOk{Width: 800, Height: 600}}}})
	r.Frames(3)
	if !r.has(ui.RoleButton, "Keep this crop") {
		t.Fatalf("not open after the answer\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Drag(ui.RoleGroup, "Right edge", -10, 10))
	r.Frames(2)
	r.click(ui.RoleButton, "Keep this crop")
	ap := r.applied()[0]
	if ap.Source.Tag != abi.PictureSourceTagFile || string(ap.Source.File.Path) != "/tmp/photo.jpg" || ap.Width != 800 || ap.Height != 600 {
		t.Fatalf("sent %+v", ap)
	}
	crop := ap.Crop

	// The core writes the cropped copy; the composer now stages that file.
	r.f.Emit(&abi.Event{Tag: abi.EventTagPictureEdited, PictureEdited: abi.EventPictureEdited{
		Target: 7, Result: abi.PathResult{Tag: abi.PathResultTagOk, Ok: abi.PathResultOk{Path: []byte("/cache/cropped.png")}}}})
	r.Frames(2)
	r.s.Staged[0].Path = []byte("/cache/cropped.png") // the composer stages the cropped copy
	r.open(state.Dialog{Kind: state.DialogCropper, N: 7})
	if n := len(r.sent(abi.CommandTagInspectPicture)); n != 1 {
		t.Fatalf("the cropped copy was inspected again (%d inspections)", n)
	}
	r.click(ui.RoleButton, "Keep this crop")
	again := r.applied()[1]
	if string(again.Source.File.Path) != "/tmp/photo.jpg" || again.Crop != crop {
		t.Errorf("reopened on %q with %+v, want the original with %+v", again.Source.File.Path, again.Crop, crop)
	}
}

// CR-008: a picture the core cannot read says so and does not open.
func TestUnreadablePictureToasts(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.stageFile(7, "/tmp/broken.png")
	r.open(state.Dialog{Kind: state.DialogCropper, N: 7})
	r.f.Emit(&abi.Event{Tag: abi.EventTagPictureInspected, PictureInspected: abi.EventPictureInspected{
		Target: 7, Result: abi.SizeResult{Tag: abi.SizeResultTagErr, Err: abi.SizeResultErr{Error: []byte("bad header")}}}})
	r.Frames(3)
	if r.s.Dialog.Open() {
		t.Error("the cropper stays open")
	}
	if len(r.s.Toasts) == 0 || string(r.s.Toasts[len(r.s.Toasts)-1].Text) != "Could not use this image: bad header" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

// A file that is no picture is not opened.
func TestNonPictureDoesNotOpen(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.stageFile(7, "/tmp/notes.txt")
	r.open(state.Dialog{Kind: state.DialogCropper, N: 7})
	if r.s.Dialog.Open() || len(r.sent(abi.CommandTagInspectPicture)) != 0 {
		t.Errorf("open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
}

// Turning makes a preview off the UI thread; the cropper asks for it and
// takes the answer without blocking the frame.
func TestTurnedPreviewIsMade(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(100, 60)
	r.click(ui.RoleButton, "Turn right")
	deadline := time.Now().Add(5 * time.Second)
	for r.h.scr.pv.file == "" && time.Now().Before(deadline) {
		r.Frames(1)
		time.Sleep(5 * time.Millisecond)
	}
	f := r.h.scr.pv.file
	if f == "" {
		t.Fatal("no preview arrived")
	}
	im, err := readPNG(f)
	if err != nil || im.Bounds().Dx() != 60 || im.Bounds().Dy() != 100 {
		t.Fatalf("preview %v %v", im.Bounds(), err)
	}
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			return
		}
		r.Frames(1)
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("the preview file is left behind")
}

func readPNG(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return image.NewRGBA(image.Rectangle{}), err
	}
	return png.Decode(bytes.NewReader(b))
}

// The worker turns a picture a quarter clockwise: the top left pixel goes to
// the top right.
func TestRenderTurnsClockwise(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	src.Set(2, 1, color.NRGBA{0, 0, 255, 255})
	out := filepath.Join(t.TempDir(), "p.png")
	pix := append([]byte(nil), src.Pix...)
	got := render(job{rgba: pix, w: 3, h: 2, turns: 1, out: out}, func() bool { return false })
	if !got.ok || got.w != 2 || got.h != 3 {
		t.Fatalf("render = %+v", got)
	}
	im, err := readPNG(out)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := im.At(1, 0).RGBA()
	_, _, b, _ := im.At(0, 2).RGBA()
	if r>>8 != 255 || b>>8 != 255 {
		t.Errorf("top right %v, bottom left %v", im.At(1, 0), im.At(0, 2))
	}
}

// Removing the attachment while the cropper is open closes it.
func TestRemovedAttachmentClosesIt(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(100, 60)
	r.s.Staged = nil
	r.Frames(2)
	if r.s.Dialog.Open() {
		t.Error("the cropper stays open on a removed attachment")
	}
}

// An attachment that is not a picture is not opened.
func TestDocumentDoesNotOpen(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.s.Staged = append(r.s.Staged, state.Staged{ID: 5, Kind: state.StagedDocument, Path: []byte("/tmp/a.pdf"), Ready: true})
	r.open(state.Dialog{Kind: state.DialogCropper, N: 5})
	if r.s.Dialog.Open() || len(r.sent(abi.CommandTagInspectPicture)) != 0 {
		t.Errorf("open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
}

// jpegWithOrientation is a w × h JPEG whose EXIF says orientation o.
func jpegWithOrientation(t *testing.T, w, h, o int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00")
	exif[len(exif)-8], exif[len(exif)-7] = byte(o>>8), byte(o)
	seg := append([]byte{0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}, exif...)
	b = append(append(append([]byte(nil), b[:2]...), seg...), b[2:]...)
	path := filepath.Join(t.TempDir(), "p.jpg")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A turned preview of a photo starts from the upright picture, as the core
// and the system's decoder see it.
func TestRenderUprightsByExif(t *testing.T) {
	never := func() bool { return false }
	for _, c := range []struct {
		orientation, turns int
		w, h               int32
	}{
		{1, 0, 6, 4}, {6, 0, 4, 6}, {8, 0, 4, 6}, {3, 0, 6, 4}, {5, 0, 4, 6},
		{6, 1, 6, 4}, {1, 1, 4, 6},
	} {
		out := filepath.Join(t.TempDir(), "o.png")
		got := render(job{path: jpegWithOrientation(t, 6, 4, c.orientation), turns: c.turns, out: out}, never)
		if !got.ok || got.w != c.w || got.h != c.h {
			t.Errorf("orientation %d, %d turns: %+v, want %dx%d", c.orientation, c.turns, got, c.w, c.h)
		}
	}
}

// A steady frame of the cropper builds without allocating, while its preview
// is a turned one too.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.openPasted(160, 100)
	check := func(what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for r.h.scr.pv.pending && time.Now().Before(deadline) {
			r.Frames(1)
			time.Sleep(2 * time.Millisecond)
		}
		r.Play(widgettest.Settle())
		r.Frames(5)
		if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
			t.Errorf("%s: %v allocations per frame", what, n)
		}
	}
	check("whole")
	r.click(ui.RoleButton, "Turn right")
	check("turned")
}
