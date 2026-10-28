package viewer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"weak"

	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

const (
	chatJID = "1@s.whatsapp.net"
	winW    = 960
	winH    = 700
)

// writePNG writes a w×h picture and returns its path.
func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	p := writePNG(t, t.TempDir(), "x.png", w, h)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

// pumped drains the account's core before each frame, as the main window's
// frame does.
type pumped struct {
	app.Builder
	st *state.State
	mr *menuAnswer
}

// menuAnswer delivers the outcome of a native context menu in the next frame,
// as the app's loop does: the rig has no window to pop one up.
type menuAnswer struct {
	owner ui.Key
	item  int
	out   ui.MenuOutcome
}

func (p pumped) Init(a *app.App) error { return p.Builder.(app.Initer).Init(a) }
func (p pumped) Build(c *ui.Context) {
	p.st.Pump(0)
	if p.mr != nil && p.mr.out != ui.MenuPending {
		c.Input().SetMenuResult(p.mr.owner, p.mr.item, p.mr.out)
		p.mr.out = ui.MenuPending
	}
	p.Builder.Build(c)
}

type rig struct {
	*widgettest.Rig
	t  *testing.T
	st *state.State
	f  *fake.Core
	a  *state.Account
	sc *Screen
	mr *menuAnswer
}

// recShell records the files the viewer opens; a test never opens one.
type recShell struct {
	mu    sync.Mutex
	paths []string
}

func (s *recShell) Open(path string) error {
	s.mu.Lock()
	s.paths = append(s.paths, path)
	s.mu.Unlock()
	return nil
}

func (s *recShell) opened() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

type album []abi.MediaItem

// open makes the viewer window over a chat, opened on message open (and a
// card), over the album the core will answer with.
func open(t *testing.T, items album, id string, card ...string) *rig {
	t.Helper()
	return openWith(t, func(*fake.Core) album { return items }, id, card...)
}

func mediaItem(id, path string, video bool) abi.MediaItem {
	it := abi.MediaItem{ID: []byte(id), Video: video}
	if path != "" {
		it.Path, it.HasPath = []byte(path), true
	}
	return it
}

// settle plays frames until the node exists.
func (r *rig) wait(role ui.Role, name string) ui.SemNode {
	r.t.Helper()
	r.Play(widgettest.WaitFor(role, name))
	return r.Must(role, name)
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// photo is the picture's node once it has decoded.
func (r *rig) photo() ui.SemNode { return r.wait(ui.RoleImage, "Photo") }

func width(n ui.SemNode) int  { return int(n.Bounds[2] - n.Bounds[0] + 0.5) }
func height(n ui.SemNode) int { return int(n.Bounds[3] - n.Bounds[1] + 0.5) }

func (r *rig) shown() string { return string(r.st.Win.ViewerMsg.At(0)) }

// ---- VW-001, VW-002: the photo, fit and zoom ----

func TestAPhotoOpensFittedAndNeverEnlarged(t *testing.T) {
	dir := t.TempDir()
	small := writePNG(t, dir, "small.png", 400, 300)
	r := open(t, album{mediaItem("m1", small, false)}, "m1")
	if sent := r.sent(abi.CommandTagLoadChatMedia); len(sent) == 0 || string(sent[0].LoadChatMedia.Around) != "m1" || string(sent[0].LoadChatMedia.Chat) != chatJID {
		t.Fatalf("the album was not asked for around the photo: %+v", sent)
	}
	n := r.photo()
	if width(n) != 400 || height(n) != 300 {
		t.Errorf("a picture smaller than the stage is shown at its own size, got %dx%d", width(n), height(n))
	}
	if !r.has(ui.RoleButton, "Fit") {
		t.Error("the zoom control reads Fit while fitted")
	}
	// A big one is shrunk to the stage, keeping its shape.
	big := writePNG(t, dir, "big.png", 3000, 1500)
	r2 := open(t, album{mediaItem("m1", big, false)}, "m1")
	n = r2.photo()
	if width(n) != winW || height(n) != winW/2 {
		t.Errorf("a wide picture fits the stage's width: got %dx%d", width(n), height(n))
	}
}

func TestZoomKeysAndHeaderButtons(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 400, 300)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	r.Key('=', 0)
	r.Frames(2)
	if n := r.photo(); width(n) != 500 || height(n) != 375 {
		t.Errorf("+ zooms in a step: %dx%d", width(n), height(n))
	}
	if !r.has(ui.RoleButton, "125%") {
		t.Error("the control shows the scale while zoomed")
	}
	r.Key('-', 0)
	r.Key('-', 0)
	r.Frames(2)
	if n := r.photo(); width(n) != 320 || height(n) != 240 {
		t.Errorf("− zooms out a step: %dx%d", width(n), height(n))
	}
	// 0 and the control go back to the window.
	r.Key('0', 0)
	r.Frames(2)
	if !r.has(ui.RoleButton, "Fit") {
		t.Error("0 fits the picture to the window")
	}
	r.Click(ui.RoleButton, "Zoom in")
	r.Frames(2)
	if n := r.photo(); width(n) != 500 {
		t.Errorf("Zoom in: %d", width(n))
	}
	r.Click(ui.RoleButton, "Zoom out")
	r.Click(ui.RoleButton, "Zoom out")
	r.Frames(2)
	r.Click(ui.RoleButton, "80%")
	r.Frames(2)
	if !r.has(ui.RoleButton, "Fit") {
		t.Error("clicking the scale fits the picture")
	}
	r.Click(ui.RoleButton, "Fit")
	r.Frames(2)
	if !r.has(ui.RoleButton, "100%") {
		t.Error("clicking Fit shows the original size")
	}
}

// The viewer window's View menu zooms the picture (⌘+ ⌘- ⌘0).
func TestTheMenuZoomsThePicture(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 400, 300)
	r := openChrome(t, newViewerChrome(t), func(*fake.Core) album { return album{mediaItem("m1", p, false)} }, "m1")
	r.photo()
	r.Play(widgettest.Command(uint16(screen.CmdPictureZoomIn)), widgettest.Idle(2))
	if n := r.photo(); width(n) != 500 {
		t.Errorf("Zoom In: %d wide", width(n))
	}
	r.Play(widgettest.Command(uint16(screen.CmdPictureZoomOut)), widgettest.Command(uint16(screen.CmdPictureZoomOut)), widgettest.Idle(2))
	if n := r.photo(); width(n) != 320 {
		t.Errorf("Zoom Out: %d wide", width(n))
	}
	r.Play(widgettest.Command(uint16(screen.CmdPictureActualSize)), widgettest.Idle(2))
	if n := r.photo(); width(n) != 400 || !r.has(ui.RoleButton, "100%") {
		t.Errorf("Actual Size: %d wide", width(n))
	}
}

func TestZoomIsLimited(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 400, 300)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	for i := 0; i < 12; i++ {
		r.Key('=', 0)
		r.Frames(1)
	}
	if !r.has(ui.RoleButton, "400%") {
		t.Error("zoom stops at 400%")
	}
	for i := 0; i < 20; i++ {
		r.Key('-', 0)
		r.Frames(1)
	}
	if !r.has(ui.RoleButton, "25%") {
		t.Error("zoom stops at 25%")
	}
}

// The pointer's gestures, delivered as the window delivers them (the rig's
// wheel, pinch, smart-zoom and drag steps) over a 4000×2000 picture that fits
// at about a quarter.
func gestures(t *testing.T) *rig {
	t.Helper()
	p := writePNG(t, t.TempDir(), "p.png", 4000, 2000)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	return r
}

func TestAPinchZoomsAroundTheFingers(t *testing.T) {
	r := gestures(t)
	fit := r.sc.v.fitScale
	r.Play(widgettest.Magnify(ui.RoleImage, "Photo", 0.5, 4), widgettest.Idle(2))
	if r.sc.v.fit || r.sc.v.zoom <= fit*1.3 {
		t.Errorf("a pinch out zooms in: %+v (fit %v)", r.sc.v, fit)
	}
	z := r.sc.v.zoom
	r.Play(widgettest.Magnify(ui.RoleImage, "Photo", -0.4, 4), widgettest.Idle(2))
	if r.sc.v.zoom >= z {
		t.Errorf("a pinch in zooms out: %v -> %v", z, r.sc.v.zoom)
	}
}

func TestSmartZoomTogglesTheOriginalSize(t *testing.T) {
	r := gestures(t)
	r.Play(widgettest.SmartMagnify(ui.RoleImage, "Photo"), widgettest.Idle(2))
	if r.sc.v.fit || r.sc.v.zoom != 1 || !r.has(ui.RoleButton, "100%") {
		t.Errorf("smart zoom shows the original size: %+v", r.sc.v)
	}
	r.Play(widgettest.SmartMagnify(ui.RoleImage, "Photo"), widgettest.Idle(2))
	if !r.sc.v.fit {
		t.Error("smart zoom again fits")
	}
}

func TestADoubleClickTogglesTheOriginalSize(t *testing.T) {
	r := gestures(t)
	r.Play(widgettest.DoubleClick(ui.RoleImage, "Photo"), widgettest.Idle(2))
	if r.sc.v.fit || r.sc.v.zoom != 1 {
		t.Fatalf("a double click shows the original size: %+v", r.sc.v)
	}
	if r.sc.Closing() {
		t.Error("a double click on the picture never closes it")
	}
	r.Play(widgettest.DoubleClick(ui.RoleImage, "Photo"), widgettest.Idle(2))
	if !r.sc.v.fit {
		t.Error("and again fits")
	}
}

func TestAClickOnThePictureNeverCloses(t *testing.T) {
	r := gestures(t)
	r.Click(ui.RoleImage, "Photo")
	r.Frames(3)
	if r.sc.Closing() {
		t.Error("a click on the picture keeps the viewer open")
	}
}

func TestTheWheelZoomsAndTheTrackpadPans(t *testing.T) {
	r := gestures(t)
	// Two fingers on a fitted picture do nothing; ⌘ with them zooms.
	r.Play(widgettest.Wheel(ui.RoleImage, "Photo", -20, 3), widgettest.Idle(2))
	if !r.sc.v.fit {
		t.Errorf("two fingers do not zoom: %+v", r.sc.v)
	}
	r.Play(widgettest.Wheel(ui.RoleImage, "Photo", 20, 3).With(ui.PrimaryMod()), widgettest.Idle(2))
	if r.sc.v.fit {
		t.Fatal("⌘ and the wheel zoom")
	}
	z := r.sc.v.zoom
	r.Play(widgettest.Wheel(ui.RoleImage, "Photo", -20, 3).With(ui.PrimaryMod()), widgettest.Idle(2))
	if r.sc.v.zoom >= z {
		t.Errorf("⌘ and the wheel the other way zoom out: %v -> %v", z, r.sc.v.zoom)
	}
	// Once zoomed, two fingers pan.
	r.Key('0', 0)
	r.Frames(2)
	r.Play(widgettest.SmartMagnify(ui.RoleImage, "Photo"), widgettest.Idle(2))
	zoom, pan := r.sc.v.zoom, r.sc.v.pan
	r.Play(widgettest.Wheel(ui.RoleImage, "Photo", 30, 2), widgettest.Idle(2))
	if r.sc.v.zoom != zoom || r.sc.v.pan == pan {
		t.Errorf("two fingers pan a zoomed picture: %v %v -> %v", zoom, pan, r.sc.v.pan)
	}
}

func TestADragPansAZoomedPictureOnly(t *testing.T) {
	r := gestures(t)
	r.Play(widgettest.Drag(ui.RoleImage, "Photo", -10, 3), widgettest.Idle(2))
	if r.sc.v.pan != ([2]float32{}) || r.sc.Closing() {
		t.Errorf("a fitted picture does not pan: %v", r.sc.v.pan)
	}
	r.Play(widgettest.SmartMagnify(ui.RoleImage, "Photo"), widgettest.Idle(2))
	pan := r.sc.v.pan
	r.Play(widgettest.Drag(ui.RoleImage, "Photo", -10, 3), widgettest.Idle(2))
	if r.sc.v.pan[0] >= pan[0] || r.sc.Closing() {
		t.Errorf("a drag pans: %v -> %v", pan, r.sc.v.pan)
	}
}

// A mouse wheel's notch zooms a step; over the empty field it does nothing.
func TestAWheelNotchZoomsAStep(t *testing.T) {
	r := gestures(t)
	fit := r.sc.v.fitScale
	r.Play(widgettest.WheelNotch(ui.RoleImage, "Photo", 1, 1), widgettest.Idle(2))
	if r.sc.v.fit || r.sc.v.zoom < fit*1.2 || r.sc.v.zoom > fit*1.3 {
		t.Fatalf("a notch zooms in a step: %+v (fit %v)", r.sc.v, fit)
	}
	z := r.sc.v.zoom
	r.Play(widgettest.WheelNotch(ui.RoleImage, "Photo", -1, 1), widgettest.Idle(2))
	if r.sc.v.zoom >= z {
		t.Errorf("the other way zooms out: %v -> %v", z, r.sc.v.zoom)
	}
	// Over the toolbar it does nothing.
	r = gestures(t)
	r.Play(widgettest.WheelNotch(ui.RoleButton, "Fit", 1, 2), widgettest.Idle(2))
	if !r.sc.v.fit {
		t.Errorf("the wheel zooms only over the stage: %+v", r.sc.v)
	}
}

// writeJPEG writes a JPEG whose EXIF orientation is o.
func writeJPEG(t *testing.T, dir, name string, w, h, o int) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, m, nil); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	// APP1 "Exif": a big-endian TIFF with one tag, 0x0112 (Orientation).
	app := []byte{0xFF, 0xE1, 0, 34, 'E', 'x', 'i', 'f', 0, 0, 'M', 'M', 0, 42, 0, 0, 0, 8,
		0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, byte(o), 0, 0, 0, 0, 0, 0}
	out := append(append(append([]byte{}, raw[:2]...), app...), raw[2:]...)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A JPEG turned by its EXIF orientation has its sides the other way round at
// its original size, and the zoom limits follow the turned picture.
func TestATurnedJPEGShowsAtItsTurnedSize(t *testing.T) {
	p := writeJPEG(t, t.TempDir(), "turned.jpg", 400, 200, 6)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	r.Key('0', 0)
	r.Key('=', 0)
	r.Key('-', 0)
	r.Frames(3)
	r.Play(widgettest.SmartMagnify(ui.RoleImage, "Photo"), widgettest.Idle(2))
	if n := r.photo(); width(n) != 200 || height(n) != 400 {
		t.Errorf("a quarter turn swaps the sides: %dx%d", width(n), height(n))
	}
}

func TestZoomMath(t *testing.T) {
	v := newView()
	v.fitScale = 0.4
	v.zoomAt(zoomStep, [2]float32{}, 1000, 800, 400, 320)
	if v.fit || v.zoom != 0.5 {
		t.Errorf("zooming from fit starts at the fitted scale: %v %v", v.fit, v.zoom)
	}
	// The limits never reverse the direction.
	w := newView()
	w.fitScale = 0.1
	w.zoomAt(0.8, [2]float32{}, 4000, 3000, 400, 300)
	if !w.fit || w.scale() != 0.1 {
		t.Error("a tiny fit must not jump up when zoomed out")
	}
	w.zoomAt(1.1, [2]float32{}, 4000, 3000, 400, 300)
	if w.zoom < 0.109 || w.zoom > 0.111 {
		t.Errorf("zoom in from a tiny fit: %v", w.zoom)
	}
	a := newView()
	a.actualAt([2]float32{}, 1000, 1000, 500, 500)
	a.zoomAt(3.9, [2]float32{}, 1000, 1000, 500, 500)
	a.zoomAt(1.1, [2]float32{}, 1000, 1000, 500, 500)
	if a.zoom != maxZoom {
		t.Errorf("max zoom: %v", a.zoom)
	}
	a.zoomAt(0.001, [2]float32{}, 1000, 1000, 500, 500)
	if a.zoom != minZoom {
		t.Errorf("min zoom: %v", a.zoom)
	}
	// The point under the pointer stays under it while zooming.
	z := newView()
	z.fitScale = 1
	z.actualAt([2]float32{}, 2000, 2000, 400, 400)
	q := [2]float32{100, -50}
	before := (q[0] - z.pan[0]) / z.scale()
	z.zoomAt(zoomStep, q, 2000, 2000, 400, 400)
	after := (q[0] - z.pan[0]) / z.scale()
	if d := before - after; d > 0.01 || d < -0.01 {
		t.Errorf("the pixel under the pointer moved by %v", d)
	}
	// A picture smaller than the stage stays centred.
	s := newView()
	s.pan = [2]float32{500, 400}
	s.zoom, s.fit = 0.25, false
	s.clamp(1600, 1200, 800, 600)
	if s.pan != ([2]float32{}) {
		t.Errorf("a small picture is centred: %v", s.pan)
	}
	// The fit never enlarges.
	if got := fitScaleOf(320, 240, 800, 700); got != 1 {
		t.Errorf("fit of a small picture: %v", got)
	}
	if got := fitScaleOf(1600, 1200, 800, 700); got != 0.5 {
		t.Errorf("fit of a big picture: %v", got)
	}
	if bucket(700, 4000) != 1024 || bucket(300, 4000) != 512 || bucket(2000, 1500) != 0 {
		t.Errorf("decode buckets: %d %d %d", bucket(700, 4000), bucket(300, 4000), bucket(2000, 1500))
	}
}

// ---- VW-003: copy image ----

func TestCopyImageAsksTheCoreForAPhotoNeverAClip(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "p.png", 200, 100)
	clip := filepath.Join(dir, "clip.mp4")
	os.WriteFile(clip, []byte("not really"), 0o644)
	r := open(t, album{mediaItem("m1", p, false), mediaItem("m2", clip, true)}, "m1")
	r.photo()
	r.Click(ui.RoleButton, "Copy image (⌘C)")
	r.Frames(2)
	cmds := r.sent(abi.CommandTagPrepareClipboardImage)
	if len(cmds) != 1 || string(cmds[0].PrepareClipboardImage.Path) != p {
		t.Fatalf("copy sent %+v", cmds)
	}
	// The answer is a toast: a failed decode says so.
	r.f.Emit(ev(abi.EventTagClipboardImage, func(e *abi.Event) {
		e.ClipboardImage.Result.Tag = abi.ClipboardImageResultTagErr
		e.ClipboardImage.Result.Err.Error = []byte("bad")
	}))
	r.Play(widgettest.Idle(3))
	if len(r.st.Toasts) == 0 || string(r.st.Toasts[len(r.st.Toasts)-1].Text) != "Failed to copy image" || !r.st.Toasts[len(r.st.Toasts)-1].Error {
		t.Fatalf("toasts %v", r.st.Toasts)
	}
	// A clip has no Copy image, nor does ⌘C copy it.
	r.Key(ui.KeyRight, 0)
	r.Frames(3)
	if r.has(ui.RoleButton, "Copy image (⌘C)") {
		t.Error("a clip offers Copy image")
	}
	r.Key('C', ui.PrimaryMod())
	r.Frames(2)
	if n := len(r.sent(abi.CommandTagPrepareClipboardImage)); n != 1 {
		t.Errorf("⌘C on a clip sent %d copies", n)
	}
}

func TestCopyImageWritesThePictureAndSaysSo(t *testing.T) {
	var written []byte
	was := setClipboardImage
	setClipboardImage = func(b []byte) error { written = b; return nil }
	defer func() { setClipboardImage = was }()
	p := writePNG(t, t.TempDir(), "p.png", 20, 10)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	r.Key('C', ui.PrimaryMod())
	r.Frames(2)
	if len(r.sent(abi.CommandTagPrepareClipboardImage)) != 1 {
		t.Fatal("⌘C did not ask the core")
	}
	pix := make([]byte, 20*10*4)
	for i := range pix {
		pix[i] = 200
	}
	ref, _ := r.f.PutBlob(pix)
	r.f.Emit(ev(abi.EventTagClipboardImage, func(e *abi.Event) {
		e.ClipboardImage.Result.Tag = abi.ClipboardImageResultTagOk
		e.ClipboardImage.Result.Ok.Image = abi.DecodedImage{Width: 20, Height: 10, RGBA: ref}
	}))
	for i := 0; i < 200 && written == nil; i++ {
		r.Play(widgettest.Pause(5))
	}
	m, err := png.Decode(bytes.NewReader(written))
	if err != nil || m.Bounds().Dx() != 20 || m.Bounds().Dy() != 10 {
		t.Fatalf("clipboard picture: %v %v", m, err)
	}
	r.Frames(2)
	if n := len(r.st.Toasts); n == 0 || string(r.st.Toasts[n-1].Text) != "Copied image" {
		t.Errorf("toasts %v", r.st.Toasts)
	}
}

// ---- VW-004, VW-005: the album ----

func threePhotos(t *testing.T) (album, string) {
	dir := t.TempDir()
	var a album
	for i, id := range []string{"m1", "m2", "m3"} {
		a = append(a, mediaItem(id, writePNG(t, dir, id+".png", 300+100*i, 200), false))
	}
	return a, dir
}

func TestLeftAndRightStepThroughTheAlbum(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m2")
	r.photo()
	r.Key(ui.KeyRight, 0)
	r.Frames(2)
	if r.shown() != "m3" {
		t.Fatalf("→ goes to the next, on %q", r.shown())
	}
	if n := r.photo(); width(n) != 500 {
		t.Errorf("the next picture shows: %d wide", width(n))
	}
	r.Key(ui.KeyRight, 0)
	r.Frames(2)
	if r.shown() != "m3" {
		t.Error("stepping stops at the end")
	}
	r.Key(ui.KeyLeft, 0)
	r.Key(ui.KeyLeft, 0)
	r.Key(ui.KeyLeft, 0)
	r.Frames(2)
	if r.shown() != "m1" {
		t.Errorf("← goes back and stops at the start, on %q", r.shown())
	}
	// Stepping returns to fitting the window.
	r.Key('=', 0)
	r.Frames(2)
	r.Key(ui.KeyRight, 0)
	r.Frames(2)
	if !r.has(ui.RoleButton, "Fit") {
		t.Error("a new picture starts fitted")
	}
}

func TestTheStripRunsAlongTheBottomAndClicksShowAnItem(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m1")
	r.photo()
	for _, name := range []string{"Photo 1", "Photo 2", "Photo 3"} {
		if !r.has(ui.RoleButton, name) {
			t.Fatalf("the strip has no %q", name)
		}
	}
	cur := r.Must(ui.RoleButton, "Photo 1")
	if cur.State&ui.SemSelected == 0 {
		t.Error("the current item is marked")
	}
	if r.Must(ui.RoleButton, "Photo 2").State&ui.SemSelected != 0 {
		t.Error("only the current item is marked")
	}
	r.Click(ui.RoleButton, "Photo 3")
	r.Frames(2)
	if r.shown() != "m3" {
		t.Fatalf("clicking a tile shows it: %q", r.shown())
	}
	if r.Must(ui.RoleButton, "Photo 3").State&ui.SemSelected == 0 {
		t.Error("the tile clicked is now the current one")
	}
	// A single item has no strip.
	one := open(t, a[:1], "m1")
	one.photo()
	if one.has(ui.RoleButton, "Photo 1") {
		t.Error("a lone item has no strip")
	}
}

func TestTheStripNamesVideosAndShowsStoredThumbnails(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "p.png", 300, 200)
	clip := filepath.Join(dir, "c.mp4")
	os.WriteFile(clip, []byte("x"), 0o644)
	th := pngBytes(t, 56, 56)
	r := openWith(t, func(f *fake.Core) album {
		ref, _ := f.PutBlob(th)
		a := album{mediaItem("m1", p, false), mediaItem("m2", clip, true)}
		a[1].Thumbnail, a[1].HasThumbnail = ref, true
		return a
	}, "m2")
	r.wait(ui.RoleButton, "Video 2")
	if !r.has(ui.RoleButton, "Photo 1") {
		t.Error("photos are named Photo N")
	}
	for i := 0; i < 200 && r.sc.album[1].thumb == nil; i++ {
		r.Play(widgettest.Pause(5))
	}
	if r.sc.album[1].thumb == nil || r.sc.album[0].thumb != nil {
		t.Fatal("the thumbnail's blob was read for the item that has one")
	}
	// The clip's poster is that thumbnail, and the blob went back to the core.
	r.wait(ui.RoleImage, "Video")
	if _, err := r.f.ReadBlob(1, 0, make([]byte, 1)); err == nil {
		t.Error("the thumbnail's blob was not freed")
	}
}

// openWith is open with an album made after the fake exists (blobs).
func openWith(t *testing.T, make func(f *fake.Core) album, id string, card ...string) *rig {
	t.Helper()
	return openChrome(t, nil, make, id, card...)
}

func openChrome(t *testing.T, ch screen.Chrome, make func(f *fake.Core) album, id string, card ...string) *rig {
	t.Helper()
	return openPre(t, ch, nil, make, id, card...)
}

// openPre is openChrome with pre run on the state before the window opens.
func openPre(t *testing.T, ch screen.Chrome, pre func(st *state.State), make func(f *fake.Core) album, id string, card ...string) *rig {
	t.Helper()
	st, f, a, sc, b, mr := setup(t, ch, make, id, card...)
	if pre != nil {
		pre(st)
	}
	r := &rig{Rig: widgettest.Open(t, sc.Config(st), winW, winH, 1, b), t: t, st: st, f: f, a: a, sc: sc, mr: mr}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Offscreen().UI.SetNativeMenus(true)
	r.Frames(4)
	return r
}

// setup makes the state, the fake core and the window's builder for a viewer
// on message id of the chat, over the album make builds.
func setup(t *testing.T, ch screen.Chrome, make func(f *fake.Core) album, id string, card ...string) (*state.State, *fake.Core, *state.Account, *Screen, pumped, *menuAnswer) {
	t.Helper()
	st := state.New()
	st.Shell = &recShell{}
	f := fake.New(nil)
	a := st.AddAccount("1", nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chatJID), Name: []byte("Ada Lovelace"), LastActivity: 20}}}
		}))
	st.Pump(0)
	ref := a.Model.Chats.Find([]byte(chatJID))
	a.OpenChat(ref)
	items := make(f)
	f.Reply(abi.CommandTagLoadChatMedia, ev(abi.EventTagChatMedia, func(e *abi.Event) {
		e.ChatMedia = abi.EventChatMedia{Chat: []byte(chatJID), Batch: 7, Last: true, Items: items}
	}))
	st.Win.ViewerChat = ref
	st.Win.ViewerMsg.Add([]byte(id))
	for _, c := range card {
		n, _ := strconv.Atoi(c)
		st.Win.ViewerCard, st.Win.ViewerHasCard = n, true
	}
	st.Win.Open[state.WinViewer] = true
	sc := New()
	sc.Traffic = true
	mr := &menuAnswer{}
	b := pumped{screen.NewWindow(st, state.WinViewer, sc, nil, ch), st, mr}
	return st, f, a, sc, b, mr
}

// viewerChrome gives the window the View menu's picture-zoom commands, as the
// shell's viewer menu model does.
type viewerChrome struct{ m *menu.Model }

func newViewerChrome(t *testing.T) viewerChrome {
	t.Helper()
	cmds := []menu.Command{
		{ID: screen.CmdPictureZoomIn, Title: "Zoom In"},
		{ID: screen.CmdPictureZoomOut, Title: "Zoom Out"},
		{ID: screen.CmdPictureActualSize, Title: "Actual Size"},
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	menus := []menu.Menu{{Title: "View", Items: []menu.Item{item(screen.CmdPictureZoomIn), item(screen.CmdPictureZoomOut), item(screen.CmdPictureActualSize)}}}
	m, err := menu.NewModel(cmds, menus, nil)
	if err != nil {
		t.Fatal(err)
	}
	return viewerChrome{m}
}

func (c viewerChrome) Menu() *menu.Model               { return c.m }
func (c viewerChrome) Commands(*screen.Env)            {}
func (c viewerChrome) MenuOf(state.Window) *menu.Model { return c.m }

func (r *rig) shell() *recShell { return r.st.Shell.(*recShell) }

func TestTheArrowsShowOnHoverAndStep(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m1")
	r.photo()
	if r.has(ui.RoleButton, "Next (Right arrow)") {
		t.Error("the arrows show only on hover")
	}
	r.Play(widgettest.Move(ui.RoleImage, "Photo"), widgettest.Idle(2))
	r.Must(ui.RoleButton, "Next (Right arrow)")
	r.Must(ui.RoleButton, "Previous (Left arrow)")
	r.Click(ui.RoleButton, "Next (Right arrow)")
	r.Frames(2)
	if r.shown() != "m2" {
		t.Errorf("the next arrow steps: %q", r.shown())
	}
}

// A card of an interactive message opens among the card's pictures (VW-014).
func TestACardPictureOpensAmongTheCards(t *testing.T) {
	dir := t.TempDir()
	var a album
	for c := uint64(0); c < 3; c++ {
		it := mediaItem("card", writePNG(t, dir, "c"+strconv.Itoa(int(c))+".png", 300+int(c)*50, 200), false)
		it.Card, it.HasCard = c, true
		a = append(a, it)
	}
	r := open(t, a, "card", "1")
	if n := r.photo(); width(n) != 350 {
		t.Errorf("card 1 shows first: %d wide", width(n))
	}
	r.Key(ui.KeyRight, 0)
	r.Frames(2)
	if w := r.st.Win; !w.ViewerHasCard || w.ViewerCard != 2 || w.ViewerMsg.Len() != 1 {
		t.Errorf("the card steps along: %d %v", w.ViewerCard, w.ViewerHasCard)
	}
	if n := r.photo(); width(n) != 400 {
		t.Errorf("card 2: %d wide", width(n))
	}
}

// ---- VW-006: the header ----

func TestTheHeaderActsOnTheMessage(t *testing.T) {
	a, _ := threePhotos(t)

	r := open(t, a, "m2")
	r.photo()
	r.parityShot("viewer-header")
	r.Click(ui.RoleButton, "Forward")
	r.Frames(2)
	d := r.st.Dialog
	if d.Kind != state.DialogForward || d.IDs.Len() != 1 || string(d.IDs.At(0)) != "m2" || d.Chat != r.st.Win.ViewerChat {
		t.Errorf("Forward opens the forward dialog on the message: %+v", d)
	}
	if !r.sc.Closing() || !r.st.Req.RaiseMain {
		t.Error("Forward leaves the viewer for the main window, as upstream")
	}

	r = open(t, a, "m2")
	r.photo()
	r.Click(ui.RoleButton, "Reply")
	r.Frames(2)
	if r.st.Panes.Reply.Len() != 1 || string(r.st.Panes.Reply.At(0)) != "m2" || !r.st.Req.FocusComposer || r.a.Open == 0 || !r.sc.Closing() || !r.st.Req.RaiseMain {
		t.Errorf("Reply quotes the message in the composer: %d %v %v", r.st.Panes.Reply.Len(), r.st.Req.FocusComposer, r.sc.Closing())
	}

	r = open(t, a, "m2")
	r.photo()
	r.Click(ui.RoleButton, "Save as…")
	r.Frames(2)
	sv := r.sent(abi.CommandTagSaveAttachmentAs)
	if len(sv) != 1 || string(sv[0].SaveAttachmentAs.Name) != "m2.png" || filepath.Base(string(sv[0].SaveAttachmentAs.Source)) != "m2.png" || !r.sc.Closing() {
		t.Errorf("Save as… hands the file to the core: %+v", sv)
	}

	r = open(t, a, "m2")
	r.photo()
	r.a.OpenChat(0)
	r.Click(ui.RoleButton, "Show in the chat")
	r.Frames(2)
	if j := r.st.Panes.Jump; r.a.Open != r.st.Win.ViewerChat || !r.sc.Closing() || !r.st.Req.RaiseMain || j.Msg.Len() != 1 || string(j.Msg.At(0)) != "m2" {
		t.Errorf("Show in the chat jumps to the message and leaves the viewer: %+v", j)
	}

	r = open(t, a, "m2")
	r.photo()
	r.Click(ui.RoleButton, "React")
	r.Frames(2)
	rt := r.st.Panes.ReactTarget
	if !rt.Open() || rt.Chat != r.st.Win.ViewerChat || string(rt.Msg.At(0)) != "m2" || !rt.Anchor.Empty() {
		t.Errorf("React opens the picker on the message, beside its bubble: %+v", rt)
	}
	if j := r.st.Panes.Jump; j.Msg.Len() != 1 || string(j.Msg.At(0)) != "m2" || r.a.Open != r.st.Win.ViewerChat {
		t.Errorf("the message is shown in the main window: %+v", j)
	}
	if !r.st.Req.RaiseMain || !r.sc.Closing() {
		t.Error("React raises the main window and closes the viewer")
	}
	if len(r.sent(abi.CommandTagReact)) != 0 {
		t.Error("the viewer reacts with no emoji of its own")
	}

	r = open(t, a, "m2")
	r.photo()
	for _, name := range []string{"Open in another app", "Enter Full Screen", "Zoom in", "Zoom out", "Copy image (⌘C)", "Close preview (Esc)"} {
		if !r.has(ui.RoleButton, name) {
			t.Errorf("the header has no %q", name)
		}
	}
	r.Click(ui.RoleButton, "Open in another app")
	for i := 0; i < 100 && len(r.shell().opened()) == 0; i++ {
		r.Play(widgettest.Pause(5))
	}
	if got := r.shell().opened(); len(got) != 1 || filepath.Base(got[0]) != "m2.png" {
		t.Errorf("Open in another app opens the file: %v", got)
	}
	r.Click(ui.RoleButton, "Close preview (Esc)")
	if !r.sc.Closing() {
		t.Error("the close button closes")
	}
}

// ---- VW-007: leaving ----

func TestEscapeAndTheEmptyFieldClose(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "p.png", 400, 300)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	r.Click(ui.RoleImage, "Photo")
	r.Frames(2)
	if r.sc.Closing() {
		t.Error("a click on the picture keeps the viewer")
	}
	// Where nothing is drawn, a click closes it (a picture that failed
	// leaves the whole field empty).
	bad := filepath.Join(dir, "bad.jpg")
	os.WriteFile(bad, []byte("not a jpeg"), 0o644)
	r = open(t, album{mediaItem("m1", bad, false)}, "m1")
	r.wait(ui.RoleButton, "Open externally")
	r.Click(ui.RoleLabel, "This image could not be displayed in Zap.")
	r.Frames(2)
	if !r.sc.Closing() {
		t.Error("a click on the empty field closes the viewer")
	}
	// Beside a fitted photo, the field is empty too.
	r = open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	r.Play(widgettest.ClickAt(ui.RoleGroup, "Image", 6, 6), widgettest.Idle(2))
	if !r.sc.Closing() {
		t.Error("a click beside the picture closes the viewer")
	}
	r = open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	r.Key(ui.KeyEscape, 0)
	r.Frames(1)
	if !r.sc.Closing() {
		t.Error("Escape closes the viewer")
	}
	// The rule itself: outside the picture's rectangle.
	r.sc.prevRect = [4]float32{100, 100, 200, 200}
	if r.sc.inMedia(99, 150) || !r.sc.inMedia(100, 150) || r.sc.inMedia(150, 200) {
		t.Error("the picture's rectangle")
	}
}

// ---- VW-008: closing with the chat ----

func TestTheViewerClosesWithItsChat(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 400, 300)
	items := album{mediaItem("m1", p, false)}

	r := open(t, items, "m1")
	r.photo()
	r.f.Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(chatJID) }))
	r.Frames(3)
	if !r.sc.Closing() {
		t.Error("a removed chat closes the viewer")
	}

	locked := func(r *rig) {
		r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
			e.ChatUpdated.Chat = abi.Chat{ID: []byte(chatJID), Name: []byte("Ada Lovelace"), Locked: true, LastActivity: 20}
		}))
	}
	r = open(t, items, "m1")
	r.photo()
	locked(r)
	r.Frames(3)
	if !r.sc.Closing() {
		t.Error("a chat locked outside the open folder closes the viewer")
	}

	r = open(t, items, "m1")
	r.photo()
	r.a.Mode = state.ListLocked
	locked(r)
	r.Frames(3)
	if r.sc.Closing() {
		t.Error("a locked chat stays open while its folder is")
	}
	r.a.Mode = state.ListChats
	r.Frames(3)
	if !r.sc.Closing() {
		t.Error("closing the folder closes the viewer")
	}
}

// ---- VW-011: not downloaded ----

func TestAnItemNotDownloadedOffersItsOwnDownload(t *testing.T) {
	dir := t.TempDir()
	have := writePNG(t, dir, "have.png", 300, 200)
	r := open(t, album{mediaItem("m1", have, false), mediaItem("m2", "", false)}, "m2")
	r.wait(ui.RoleButton, "Download")
	if r.has(ui.RoleImage, "Photo") && width(r.Must(ui.RoleImage, "Photo")) == 300 {
		t.Error("another picture stands in for the missing one")
	}
	r.Click(ui.RoleButton, "Download")
	r.Frames(2)
	d := r.sent(abi.CommandTagDownload)
	if len(d) != 1 || !d[0].Download.Manual || string(d[0].Download.Message) != "m2" || string(d[0].Download.Chat) != chatJID || d[0].Download.HasCard {
		t.Fatalf("Download asks for the item: %+v", d)
	}
	if !r.sc.dlOf().downloading {
		t.Error("a running download is remembered")
	}
	// A failure says why and a click tries again.
	r.f.Emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(chatJID), Message: []byte("m2")}
		e.Media.Result.Tag = abi.PathResultTagErr
		e.Media.Result.Err.Error = []byte("no network")
	}))
	r.Frames(3)
	if r.sc.dlOf().err != "no network" {
		t.Errorf("the failure is kept: %+v", r.sc.dlOf())
	}
	r.Click(ui.RoleButton, "Download")
	r.Frames(2)
	if len(r.sent(abi.CommandTagDownload)) < 2 {
		t.Error("a click retries")
	}
	// The file arrives: the viewer shows it.
	fresh := writePNG(t, dir, "m2.png", 350, 250)
	r.f.Emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(chatJID), Message: []byte("m2")}
		e.Media.Result.Tag = abi.PathResultTagOk
		e.Media.Result.Ok.Path = []byte(fresh)
	}))
	r.Frames(3)
	if n := r.photo(); width(n) != 350 {
		t.Errorf("the downloaded file shows: %d wide", width(n))
	}
}

// ---- VW-012: a picture that cannot be shown ----

func TestAPictureThatCannotBeShownSaysSo(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.jpg")
	os.WriteFile(bad, []byte("this is not a jpeg"), 0o644)
	r := open(t, album{mediaItem("m1", bad, false)}, "m1")
	r.wait(ui.RoleButton, "Open externally")
	if !r.has(ui.RoleLabel, "This image could not be displayed in Zap.") {
		t.Error("the failure is stated")
	}
	r.Click(ui.RoleButton, "Open externally")
	for i := 0; i < 100 && len(r.shell().opened()) == 0; i++ {
		r.Play(widgettest.Pause(5))
	}
	if got := r.shell().opened(); len(got) != 1 || got[0] != bad {
		t.Errorf("Open externally opens the file: %v", got)
	}
}

// ---- VW-013: nothing reaches the chat ----

func TestTypingInTheViewerReachesNothing(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 400, 300)
	r := open(t, album{mediaItem("m1", p, false)}, "m1")
	r.photo()
	before := len(r.f.Sent())
	r.Type("hello there")
	r.Key('V', ui.PrimaryMod())
	r.Key(ui.KeyEnter, 0)
	r.Frames(3)
	for _, c := range r.f.Sent()[before:] {
		switch c.Tag {
		case abi.CommandTagSendText, abi.CommandTagComposing, abi.CommandTagSaveDraft:
			t.Errorf("the chat saw %v", c.Tag)
		}
	}
	if r.st.Req.FocusComposer || r.st.Panes.Reply.Len() > 0 {
		t.Error("the composer was asked for")
	}
}

// ---- VW-009, VW-010: clips ----

func TestAClipShowsItsPosterWithAPlayButton(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "c.mp4")
	os.WriteFile(clip, []byte("x"), 0o644)
	r := open(t, album{mediaItem("m1", clip, true)}, "m1")
	r.wait(ui.RoleButton, "Play or pause the video")
	for _, name := range []string{"Zoom in", "Zoom out", "Copy image (⌘C)", "Fit"} {
		if r.has(ui.RoleButton, name) {
			t.Errorf("a clip offers %q", name)
		}
	}
	// Not downloaded: its Download button, as for a photo.
	r2 := open(t, album{mediaItem("m1", "", true)}, "m1")
	r2.wait(ui.RoleButton, "Download")
	if r2.has(ui.RoleButton, "Play or pause the video") {
		t.Error("an undownloaded clip cannot play")
	}
}

func TestAMotionPhotoFetchesItsClip(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 400, 300)
	r := openWith(t, func(f *fake.Core) album { return album{mediaItem("m1", p, false)} }, "m1")
	// The message carries a motion clip that is not downloaded.
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		m := abi.Message{ID: []byte("m1"), Chat: []byte(chatJID), Sender: []byte(chatJID), Timestamp: 5}
		m.Content.Tag = abi.ContentTagImage
		m.Content.Image.HasMotion = true
		m.Content.Image.Media.Path, m.Content.Image.Media.HasPath = []byte(p), true
		e.Messages = abi.EventMessages{Chat: []byte(chatJID), Batch: 5, Last: true, Messages: []abi.Message{m}}
	}))
	r.Frames(3)
	r.wait(ui.RoleButton, "Play motion photo")
	r.Click(ui.RoleButton, "Play motion photo")
	r.Frames(2)
	d := r.sent(abi.CommandTagDownloadMotion)
	if len(d) != 1 || string(d[0].DownloadMotion.Message) != "m1" {
		t.Fatalf("the clip was not asked for: %+v", d)
	}
	// A failed download says why and a click tries again.
	r.f.Emit(ev(abi.EventTagMotion, func(e *abi.Event) {
		e.Motion = abi.EventMotion{Chat: []byte(chatJID), Message: []byte("m1")}
		e.Motion.Result.Tag = abi.PathResultTagErr
		e.Motion.Result.Err.Error = []byte("clip gone")
	}))
	r.Frames(3)
	r.Click(ui.RoleButton, "clip gone")
	r.Frames(2)
	if len(r.sent(abi.CommandTagDownloadMotion)) != 2 {
		t.Error("a click on the failed clip retries")
	}
}

// ---- other windows' requests ----

func TestAnotherRequestMovesTheOpenViewer(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m1")
	r.photo()
	r.st.Win.ViewerMsg.Reset()
	r.st.Win.ViewerMsg.Add([]byte("m3"))
	r.Frames(3)
	if n := r.photo(); width(n) != 500 {
		t.Errorf("asking again moves to the item: %d wide", width(n))
	}
}

func TestADeletedItemLeavesTheAlbum(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m1")
	r.photo()
	r.f.Reply(abi.CommandTagLoadChatMedia) // the refreshed album, in the end
	r.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(chatJID), ID: []byte("m2")}
	}))
	r.Frames(3)
	if r.has(ui.RoleButton, "Photo 3") || !r.has(ui.RoleButton, "Photo 2") {
		t.Error("the deleted item left the strip")
	}
}

func TestTheShownItemDeletedMovesToItsNeighbour(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m2")
	r.photo()
	r.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(chatJID), ID: []byte("m2")}
	}))
	r.Frames(3)
	if r.shown() != "m3" || r.sc.Closing() {
		t.Errorf("the next item takes its place: %q", r.shown())
	}
	one := open(t, a[:1], "m1")
	one.photo()
	one.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(chatJID), ID: []byte("m1")}
	}))
	one.Frames(3)
	if !one.sc.Closing() {
		t.Error("with nothing left the viewer closes")
	}
}

func TestALongAlbumBuildsOnlyTheTilesInView(t *testing.T) {
	p := writePNG(t, t.TempDir(), "p.png", 100, 100)
	var a album
	for i := 1; i <= 120; i++ {
		a = append(a, mediaItem("m"+strconv.Itoa(i), p, false))
	}
	r := open(t, a, "m100")
	r.photo()
	r.Frames(4)
	if !r.has(ui.RoleButton, "Photo 100") {
		t.Error("the strip is centred on the item on screen")
	}
	if r.has(ui.RoleButton, "Photo 1") || r.has(ui.RoleButton, "Photo 60") {
		t.Error("tiles far out of view are not built")
	}
	r.Click(ui.RoleButton, "Photo 103")
	r.Frames(3)
	if r.shown() != "m103" {
		t.Errorf("a tile of a long album shows its item: %q", r.shown())
	}
}

// A window that has closed frees its album and ignores what comes after.
func TestClosedFreesTheWindowsState(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m2")
	r.photo()
	if len(r.sc.album) != 3 {
		t.Fatalf("album: %d", len(r.sc.album))
	}
	r.sc.Closed(r.sc.e)
	if len(r.sc.album) != 0 || r.sc.cache != nil {
		t.Error("Closed drops the album and the cache")
	}
	r.f.Emit(ev(abi.EventTagChatMedia, func(e *abi.Event) {
		e.ChatMedia = abi.EventChatMedia{Chat: []byte(chatJID), Batch: 8, Last: true, Items: a}
	}))
	r.Frames(3)
	if len(r.sc.album) != 0 {
		t.Error("a closed window ignores the album")
	}
}

// A worker's thumbnail is kept only by the item that takes it: a hand-off
// no item took, and one that comes after the window closed, can be
// collected.
func TestThumbnailHandoffsDoNotOutliveTheirAlbum(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m2")
	r.photo()
	gen := r.sc.resetGen
	stray := make([]byte, 64<<10)
	strayGone := weak.Make(&stray[0])
	if !r.sc.th.put(thumbDone{gen: gen, id: []byte("gone"), data: stray}) {
		t.Fatal("a thumbnail of the album on screen was refused")
	}
	stray = nil
	r.Frames(2)
	r.sc.Closed(r.sc.e)
	late := make([]byte, 64<<10)
	lateGone := weak.Make(&late[0])
	if r.sc.th.put(thumbDone{gen: gen, id: []byte("m1"), data: late}) {
		t.Error("a thumbnail that came after the window closed was taken")
	}
	late = nil
	runtime.GC()
	runtime.GC()
	if strayGone.Value() != nil {
		t.Error("a thumbnail no item took is still held")
	}
	if lateGone.Value() != nil {
		t.Error("a thumbnail that came after the window closed is still held")
	}
	runtime.KeepAlive(r.sc)
}

// rightClick right-clicks the picture and returns the native menu it asked
// for (none: nil).
func (r *rig) rightClick() (owner ui.Key, items []ui.MenuItem) {
	r.t.Helper()
	r.Play(widgettest.RClick(ui.RoleImage, "Photo"), widgettest.Idle(2))
	req, ok := r.Offscreen().UI.TakeMenuRequest()
	if !ok || req.Kind != ui.MenuItems {
		return 0, nil
	}
	return req.Owner, append([]ui.MenuItem(nil), req.Items...)
}

func (r *rig) choose(owner ui.Key, items []ui.MenuItem, title string) {
	r.t.Helper()
	for _, it := range items {
		if it.Title == title {
			r.mr.owner, r.mr.item, r.mr.out = owner, it.ID, ui.MenuChosen
			r.Frames(3)
			return
		}
	}
	r.t.Fatalf("no item %q in %+v", title, items)
}

// Right-clicking the picture offers the header's copy, save and open, as a
// native menu; the empty field offers nothing.
func TestThePictureHasANativeMenu(t *testing.T) {
	a, _ := threePhotos(t)
	r := open(t, a, "m2")
	r.photo()
	owner, items := r.rightClick()
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	if want := []string{"Copy image", "Save as…", "Open in another app"}; !slices.Equal(titles, want) {
		t.Fatalf("the picture's menu: %v", titles)
	}
	r.choose(owner, items, "Open in another app")
	for i := 0; i < 100 && len(r.shell().opened()) == 0; i++ {
		r.Play(widgettest.Pause(5))
	}
	if got := r.shell().opened(); len(got) != 1 || filepath.Base(got[0]) != "m2.png" {
		t.Errorf("Open in another app opens the file: %v", got)
	}

	r = open(t, a, "m2")
	r.photo()
	owner, items = r.rightClick()
	r.choose(owner, items, "Copy image")
	if cs := r.sent(abi.CommandTagPrepareClipboardImage); len(cs) != 1 {
		t.Errorf("Copy image asks the core for the picture: %d", len(cs))
	}

	r = open(t, a, "m2")
	r.photo()
	owner, items = r.rightClick()
	r.choose(owner, items, "Save as…")
	if sv := r.sent(abi.CommandTagSaveAttachmentAs); len(sv) != 1 || !r.sc.Closing() {
		t.Errorf("Save as… hands the file to the core: %d", len(sv))
	}
}

// ---- the sticker view ----

// openSticker opens the viewer on a sticker message of the chat whose file is
// path, with the core listing the favorites when asked.
func openSticker(t *testing.T, path string, favorites ...string) *rig {
	t.Helper()
	r := openWith(t, func(*fake.Core) album { return nil }, "s1")
	r.f.Reply(abi.CommandTagRecentStickers, ev(abi.EventTagStickers, func(e *abi.Event) {
		var en []abi.StickerEntry
		for _, f := range favorites {
			en = append(en, abi.StickerEntry{Tag: abi.StickerEntryTagFavorite, Favorite: abi.StickerEntryFavorite{Path: []byte(f)}})
		}
		e.Stickers = abi.EventStickers{Batch: 3, Last: true, Entries: en}
	}))
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		m := abi.Message{ID: []byte("s1"), Chat: []byte(chatJID), Sender: []byte(chatJID), Timestamp: 5}
		m.Content.Tag = abi.ContentTagSticker
		m.Content.Sticker.Media.Path, m.Content.Sticker.Media.HasPath = []byte(path), true
		e.Messages = abi.EventMessages{Chat: []byte(chatJID), Batch: 5, Last: true, Messages: []abi.Message{m}}
	}))
	r.Frames(4)
	return r
}

func contentHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestAStickerShowsOnACardWithAddToFavorites(t *testing.T) {
	p := writePNG(t, t.TempDir(), "s.png", 256, 256)
	r := openSticker(t, p, "/saved/0000.webp")
	n := r.wait(ui.RoleImage, "Sticker")
	if width(n) > 300 || width(n) != height(n) {
		t.Errorf("the sticker is a square card of at most 300: %dx%d", width(n), height(n))
	}
	r.Must(ui.RoleButton, "Add to favorites")
	r.parityShot("sticker-add")
	if r.has(ui.RoleButton, "Zoom in") || r.has(ui.RoleButton, "Download") {
		t.Error("a sticker is not a photograph: no zoom")
	}
	r.Click(ui.RoleButton, "Add to favorites")
	r.Frames(3)
	sv := r.sent(abi.CommandTagSaveSticker)
	if len(sv) != 1 || string(sv[0].SaveSticker.Path) != p {
		t.Fatalf("Add to favorites saves the sticker: %+v", sv)
	}
	if r.sc.Closing() {
		t.Error("the button does not close the view")
	}
	if r.has(ui.RoleButton, "Remove from favorites") {
		t.Fatal("Remove offered before the saved copy's path is known")
	}
	saved := "/saved/" + contentHash(t, p) + ".webp"
	r.f.Emit(ev(abi.EventTagStickers, func(e *abi.Event) {
		e.Stickers = abi.EventStickers{Batch: 4, Last: true, Entries: []abi.StickerEntry{
			{Tag: abi.StickerEntryTagFavorite, Favorite: abi.StickerEntryFavorite{Path: []byte(saved)}},
		}}
	}))
	r.wait(ui.RoleButton, "Remove from favorites")
	r.Must(ui.RoleButton, "Remove from favorites")
	r.parityShot("sticker-remove")
	r.Click(ui.RoleButton, "Remove from favorites")
	if fg := r.sent(abi.CommandTagForgetSticker); len(fg) != 1 || string(fg[0].ForgetSticker.Path) != saved {
		t.Fatalf("new favorite's removal: %+v", fg)
	}
}

func (r *rig) parityShot(name string) {
	if dir := os.Getenv("VIEWER_SHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			r.t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, r.Image()); err != nil {
			r.t.Fatal(err)
		}
	}
}

func TestAFavoriteStickerOffersRemoveFromFavorites(t *testing.T) {
	p := writePNG(t, t.TempDir(), "s.png", 256, 256)
	saved := "/stickers/saved/" + contentHash(t, p) + ".webp"
	r := openSticker(t, p, "/stickers/saved/other.webp", saved)
	for i := 0; i < 200 && !r.has(ui.RoleButton, "Remove from favorites"); i++ {
		r.Play(widgettest.Pause(5))
	}
	r.Must(ui.RoleButton, "Remove from favorites")
	r.Click(ui.RoleButton, "Remove from favorites")
	r.Frames(3)
	fg := r.sent(abi.CommandTagForgetSticker)
	if len(fg) != 1 || string(fg[0].ForgetSticker.Path) != saved {
		t.Fatalf("Remove from favorites forgets the saved copy: %+v", fg)
	}
	r.Must(ui.RoleButton, "Add to favorites")
}

func TestAStickerClosesOnAClickOutsideIt(t *testing.T) {
	p := writePNG(t, t.TempDir(), "s.png", 256, 256)
	r := openSticker(t, p)
	r.wait(ui.RoleImage, "Sticker")
	r.Play(widgettest.ClickAt(ui.RoleGroup, "Sticker", 8, 8), widgettest.Idle(2))
	if !r.sc.Closing() {
		t.Error("a click on the backdrop closes")
	}
}

func TestAStickerClosesOnAClickOnItAndEscape(t *testing.T) {
	p := writePNG(t, t.TempDir(), "s.png", 256, 256)
	r := openSticker(t, p)
	r.wait(ui.RoleImage, "Sticker")
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if !r.sc.Closing() {
		t.Error("Escape closes")
	}
	r = openSticker(t, p)
	r.wait(ui.RoleImage, "Sticker")
	r.Click(ui.RoleImage, "Sticker")
	r.Frames(2)
	if !r.sc.Closing() {
		t.Error("a click on the sticker closes")
	}
}

// The open hand shows while the picture can be dragged (zoomed past the
// stage), the closed one while it is.
func TestTheGrabCursorsShowWhileThePictureCanBeDragged(t *testing.T) {
	r := gestures(t)
	cursor := func() ui.Cursor {
		var cr []ui.CursorRegion
		cr = r.Offscreen().UI.CursorRegions(cr)
		for _, c := range cr {
			if c.Cursor == ui.CursorGrab || c.Cursor == ui.CursorGrabbing {
				return c.Cursor
			}
		}
		return ui.CursorArrow
	}
	if c := cursor(); c != ui.CursorArrow {
		t.Errorf("a fitted picture cannot be dragged: %v", c)
	}
	r.Play(widgettest.SmartMagnify(ui.RoleImage, "Photo"), widgettest.Idle(3))
	if c := cursor(); c != ui.CursorGrab {
		t.Errorf("a zoomed picture shows the open hand: %v", c)
	}
	x, y := widgettest.Centre(r.Must(ui.RoleImage, "Photo"))
	r.Play(widgettest.Down(ui.RoleImage, "Photo"), widgettest.Idle(1))
	if c := cursor(); c != ui.CursorGrab {
		t.Errorf("a press alone does not close the hand: %v", c)
	}
	r.Play(widgettest.MoveTo(x-40, y+10), widgettest.Idle(2))
	if c := cursor(); c != ui.CursorGrabbing {
		t.Errorf("a drag shows the closed hand: %v", c)
	}
	r.Play(widgettest.Up(), widgettest.Idle(2))
	if c := cursor(); c != ui.CursorGrab {
		t.Errorf("the open hand is back after the drag: %v", c)
	}
}
