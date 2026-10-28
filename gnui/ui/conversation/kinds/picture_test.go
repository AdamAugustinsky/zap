package kinds_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/conversation/kinds"
)

// solid writes a w×h picture of one colour and returns its path.
func solid(t *testing.T, name string, w, h int, c color.RGBA) string {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func photo(id string, from bool, path string, w, h uint32, st abi.MediaStateTag) abi.Message {
	m := msg(id, 10, from, abi.Content{Tag: abi.ContentTagImage})
	md := &m.Content.Image.Media
	md.Mime, md.Size, md.Width, md.HasWidth, md.Height, md.HasHeight = []byte("image/png"), 5000, w, true, h, true
	if path != "" {
		md.Path, md.HasPath = []byte(path), true
	}
	md.State.Tag = st
	return m
}

// update replaces a message the way the core does.
func (r *rig) update(m abi.Message) {
	r.t.Helper()
	r.f.Emit(ev(abi.EventTagMessageUpdated, func(e *abi.Event) { e.MessageUpdated.Message = m }))
	r.Frames(3)
}

// pixel is the colour of the window at (x, y).
func (r *rig) pixel(x, y int) color.RGBA {
	c := r.Image().RGBAAt(x, y)
	return c
}

func near(a, b color.RGBA, tol int) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol && d(a.B, b.B) <= tol
}

// until runs frames until cond, for at most a second (decodes are on workers).
func (r *rig) until(what string, cond func() bool) {
	r.t.Helper()
	for i := 0; i < 200; i++ {
		r.Frames(2)
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("never: %s", what)
}

func (r *rig) centre(id string) (int, int) {
	b := r.p.rects[id]
	return int(b.X + b.W/2), int(b.Y + b.H/2)
}

func commands(r *rig, tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

var red = color.RGBA{220, 30, 30, 255}

// A downloaded photo is shown at its shape, within the bubble's width and
// upstream's 320 × 440 points; it opens the viewer window.
func TestPhotoShowsItsFileAndOpensTheViewer(t *testing.T) {
	path := solid(t, "p.png", 800, 600, red)
	r := newRig(t, photo("p", false, path, 800, 600, abi.MediaStateTagIdle))
	w, h := r.p.sized["p"][0], r.p.sized["p"][1]
	if w != 320 || h != 240 {
		t.Fatalf("size %v × %v, want 320 × 240", w, h)
	}
	r.until("the photo is drawn", func() bool { x, y := r.centre("p"); return near(r.pixel(x, y), red, 6) })
	if _, ok := r.Find(ui.RoleButton, "Photo"); !ok {
		t.Error("no Photo button")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Photo"))
	r.Frames(2)
	if !r.s.Win.Want[state.WinViewer] || r.s.Win.ViewerMsg.Len() != 1 || string(r.s.Win.ViewerMsg.At(0)) != "p" {
		t.Errorf("viewer want %v msg %d", r.s.Win.Want[state.WinViewer], r.s.Win.ViewerMsg.Len())
	}
	if r.s.Win.ViewerChat != r.a.Open {
		t.Error("the viewer shows another chat")
	}
}

// Small photos are not enlarged below upstream's floor; unknown sizes fall
// back to 4:3 at the floor.
func TestPhotoSizeFollowsFitPicture(t *testing.T) {
	r := newRig(t,
		photo("tall", false, "", 300, 1200, abi.MediaStateTagIdle),
		photo("small", false, "", 200, 100, abi.MediaStateTagIdle),
		photo("unknown", false, "", 0, 0, abi.MediaStateTagIdle),
	)
	// narrow ones are widened to 120 points, as upstream's fit_picture does
	if s := r.p.sized["tall"]; s[0] != 120 || s[1] != 480 {
		t.Errorf("tall %v", s)
	}
	if s := r.p.sized["small"]; s[0] != 200 || s[1] != 100 {
		t.Errorf("small %v", s)
	}
	if s := r.p.sized["unknown"]; s[0] != 120 || s[1] != 90 {
		t.Errorf("unknown %v", s)
	}
}

// Not downloaded: the inline thumbnail stands in under a scrim, with the
// size and a download button; a click asks for the file.
func TestUndownloadedPhotoShowsItsThumbnailAndDownloadsOnClick(t *testing.T) {
	thumb := solid(t, "t.png", 40, 30, color.RGBA{30, 30, 220, 255})
	data, _ := os.ReadFile(thumb)
	f := newRig(t) // for its core, to put the blob in
	ref, err := f.f.PutBlob(data)
	if err != nil {
		t.Fatal(err)
	}
	m := photo("p", false, "", 800, 600, abi.MediaStateTagIdle)
	m.Thumbnail, m.HasThumbnail = ref, true
	f.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 2, Last: true, Messages: []abi.Message{m}}
	}))
	f.Frames(2)
	f.until("the thumbnail is drawn", func() bool {
		x, y := f.centre("p")
		c := f.pixel(x-60, y-60)        // away from the badge at the centre
		return c.B > c.R+40 && c.B > 60 // blue, darkened by the scrim
	})
	if _, ok := f.Find(ui.RoleButton, "Download photo"); !ok {
		t.Fatal("no Download photo button")
	}
	f.Play(widgettest.Click(ui.RoleButton, "Download photo"))
	f.Frames(2)
	got := commands(f, abi.CommandTagDownload)
	if len(got) != 1 || !got[0].Download.Manual || string(got[0].Download.Message) != "p" || string(got[0].Download.Chat) != chatID {
		t.Fatalf("download commands %+v", got)
	}
}

// With automatic downloads on, a photo in view is asked for once, not on
// every frame; large ones wait to be clicked.
func TestVisiblePhotosDownloadAutomaticallyOnceWithinTheLimit(t *testing.T) {
	big := photo("big", false, "", 800, 600, abi.MediaStateTagIdle)
	big.Content.Image.Media.Size = 65 << 20
	r := newRig(t, photo("p", false, "", 800, 600, abi.MediaStateTagIdle), big)
	r.a.Settings.AutoDownload = true
	r.Frames(6)
	got := commands(r, abi.CommandTagDownload)
	if len(got) != 1 || got[0].Download.Manual || string(got[0].Download.Message) != "p" {
		t.Fatalf("download commands %+v", got)
	}
	r.a.Settings.AutoDownload = false
	r.update(photo("q", false, "", 800, 600, abi.MediaStateTagIdle))
	r.Frames(4)
	if n := len(commands(r, abi.CommandTagDownload)); n != 1 {
		t.Errorf("%d downloads with the setting off", n)
	}
}

// While it downloads there is nothing to click for; after a failure the
// reason shows and a click retries.
func TestDownloadingAndFailedPhotos(t *testing.T) {
	r := newRig(t, photo("p", false, "", 800, 600, abi.MediaStateTagDownloading))
	if _, ok := r.Find(ui.RoleButton, "Downloading"); !ok {
		t.Fatal("no Downloading button")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Downloading"))
	if n := len(commands(r, abi.CommandTagDownload)); n != 0 {
		t.Errorf("a click while downloading asked again (%d)", n)
	}
	// The core reports a download's end in a Media event (an update of the
	// message keeps the state it had).
	r.f.Emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(chatID), Message: []byte("p"),
			Result: abi.PathResult{Tag: abi.PathResultTagErr, Err: abi.PathResultErr{Error: []byte("expired")}}}
	}))
	r.Frames(3)
	if _, ok := r.Find(ui.RoleButton, "Retry download"); !ok {
		t.Fatal("no Retry download button")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Retry download"))
	r.Frames(2)
	if got := commands(r, abi.CommandTagDownload); len(got) != 1 || !got[0].Download.Manual {
		t.Errorf("retry %+v", got)
	}
}

// A file ZapFast cannot decode says so, and a click opens it in the system.
func TestPictureThatCannotBeDecodedOpensInTheSystem(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.jpg")
	if err := os.WriteFile(p, []byte("not a picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened := make(chan string, 1)
	restore := kinds.SetOpeners(func(s string) error { opened <- s; return nil }, nil, nil)
	defer restore()
	r := newRig(t, photo("p", false, p, 400, 300, abi.MediaStateTagIdle))
	r.until("the failure is drawn", func() bool {
		_, ok := r.Find(ui.RoleButton, "Photo")
		r.Frames(1)
		return ok && r.pixelsDiffer("p")
	})
	r.Play(widgettest.Click(ui.RoleButton, "Photo"))
	select {
	case got := <-opened:
		if got != p {
			t.Errorf("opened %q", got)
		}
	case <-time.After(time.Second):
		t.Error("the file was not opened")
	}
}

// pixelsDiffer reports whether the block shows more than a flat fill.
func (r *rig) pixelsDiffer(id string) bool {
	b := r.p.rects[id]
	first := r.pixel(int(b.X)+2, int(b.Y)+2)
	for y := int(b.Y); y < int(b.Y+b.H); y += 6 {
		for x := int(b.X); x < int(b.X+b.W); x += 6 {
			if !near(r.pixel(x, y), first, 3) {
				return true
			}
		}
	}
	return false
}

// A sticker is drawn at 180 points and opens the viewer; an animated one
// plays, a frame at a time.
func TestAnimatedStickerPlaysAndOpensTheViewer(t *testing.T) {
	data, err := os.ReadFile("testdata/anim.webp")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "s.webp")
	os.WriteFile(p, data, 0o644)
	m := msg("s", 10, false, abi.Content{Tag: abi.ContentTagSticker})
	md := &m.Content.Sticker.Media
	md.Mime, md.Size, md.Width, md.HasWidth, md.Height, md.HasHeight = []byte("image/webp"), 900, 96, true, 96, true
	md.Path, md.HasPath = []byte(p), true
	m.Content.Sticker.Animated = true
	r := newRig(t, m)
	if s := r.p.sized["s"]; s[0] != 180 || s[1] != 180 {
		t.Fatalf("sticker size %v", s)
	}
	seen := map[[3]uint8]bool{}
	r.until("the sticker shows two of its frames", func() bool {
		x, y := r.centre("s")
		c := r.pixel(x-60, y-60) // the frame's colour outside its white disc
		if c.R > 150 || c.G > 150 || c.B > 150 {
			seen[[3]uint8{c.R / 64, c.G / 64, c.B / 64}] = true
		}
		return len(seen) >= 2
	})
	r.Play(widgettest.Click(ui.RoleButton, "Sticker"))
	r.Frames(2)
	if !r.s.Win.Want[state.WinViewer] {
		t.Error("a sticker did not open the viewer")
	}
}

// An animated GIF that arrives as a picture is played by the framework's
// animated images and opens in the system's viewer (the viewer's album leaves
// GIFs out, as upstream's open_target does).
func TestGIFPictureAnimatesAndOpensInTheSystem(t *testing.T) {
	data, err := os.ReadFile("testdata/anim.gif")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gif.DecodeAll(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "g.gif")
	os.WriteFile(p, data, 0o644)
	m := photo("g", false, p, 96, 96, abi.MediaStateTagIdle)
	m.Content.Image.Media.Mime = []byte("image/gif")
	opened := make(chan string, 1)
	restore := kinds.SetOpeners(func(s string) error { opened <- s; return nil }, nil, nil)
	defer restore()
	r := newRig(t, m)
	seen := map[[3]uint8]bool{}
	r.until("the GIF shows two of its frames", func() bool {
		x, y := r.centre("g")
		c := r.pixel(x-30, y-30)
		seen[[3]uint8{c.R / 64, c.G / 64, c.B / 64}] = true
		return len(seen) >= 2
	})
	r.Play(widgettest.Click(ui.RoleButton, "Photo"))
	select {
	case got := <-opened:
		if got != p {
			t.Errorf("opened %q", got)
		}
	case <-time.After(time.Second):
		t.Error("the GIF did not open in the system")
	}
	if r.s.Win.Want[state.WinViewer] {
		t.Error("a GIF opened the viewer")
	}
}
