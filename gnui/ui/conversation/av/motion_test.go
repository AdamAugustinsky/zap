package av_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// photo is a photo of the chat whose file is a green PNG on disk, with its
// motion clip at motion ("": not downloaded yet).
func (r *rig) photo(id string, ts int64, motion string) abi.Message {
	r.t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = 30, 160, 60, 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(r.t.TempDir(), id+".png")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		r.t.Fatal(err)
	}
	m := abi.Message{ID: []byte(id), Chat: []byte(chatID), Sender: []byte(chatID), Timestamp: ts}
	m.Content.Tag = abi.ContentTagImage
	c := &m.Content.Image
	c.Media = abi.Media{Mime: []byte("image/png"), Size: uint64(b.Len()), Width: 640, HasWidth: true, Height: 480, HasHeight: true,
		Path: []byte(path), HasPath: true}
	c.HasMotion = true
	if motion != "" {
		c.Motion.Path, c.Motion.HasPath = []byte(motion), true
	}
	return m
}

// A motion photo's badge plays its clip over the photo, muted and looping,
// and a second click goes back to the photo (MK-018). Nothing opens before
// the click; one clip plays at a time.
func TestAMotionPhotoLoopsItsClipOverThePhoto(t *testing.T) {
	r := newRig(t)
	r.load(r.photo("mp", 10, "/m/motion.mov"), r.photo("other", 11, "/m/other.mov"))
	r.until("the photo shows", func() bool { return green(r.at("mp", 100, 100)) })
	if len(r.videos) != 0 {
		t.Fatal("a clip opened before a click")
	}
	r.Play(clickMotion(0))
	v := r.video(0)
	r.until("the clip plays over the photo", func() bool { return v.playing && magenta(r.at("mp", 100, 100)) })
	if v.path != "/m/motion.mov" || !v.opts.Loop || !v.opts.Muted {
		t.Errorf("opened %q with %+v", v.path, v.opts)
	}
	r.Frames(1)
	r.must(ui.RoleButton, "Pause")
	r.Play(clickMotion(0)) // the other's: one at a time
	w := r.video(1)
	r.until("the other plays", func() bool { return w.playing && magenta(r.at("other", 100, 100)) })
	if !v.closed || !green(r.at("mp", 100, 100)) {
		t.Errorf("the first: closed %v, %v", v.closed, r.at("mp", 100, 100))
	}
	r.Click(ui.RoleButton, "Pause")
	r.Frames(2)
	if !w.closed || !green(r.at("other", 100, 100)) || r.has(ui.RoleButton, "Pause") {
		t.Errorf("a second click: closed %v, %v", w.closed, r.at("other", 100, 100))
	}
	if n := len(r.sent(abi.CommandTagDownloadMotion)); n != 0 {
		t.Errorf("%d downloads of a clip that is here", n)
	}
}

func clickMotion(n int) widgettest.Step {
	return widgettest.Click(ui.RoleButton, "Play motion photo").Nth(n)
}

// A clip not downloaded yet downloads on the click, with a spinner, and plays
// once it is here; one whose download fails is let go, and the badge says
// so.
func TestAMotionClipDownloadsFirst(t *testing.T) {
	r := newRig(t)
	r.load(r.photo("mp", 10, ""), r.photo("bad", 11, ""))
	r.Play(clickMotion(0))
	r.Frames(2)
	if dl := r.sent(abi.CommandTagDownloadMotion); len(dl) != 1 || string(dl[0].DownloadMotion.Message) != "mp" {
		t.Fatalf("downloads %+v", dl)
	}
	r.must(ui.RoleButton, "Downloading motion photo")
	if len(r.videos) != 0 {
		t.Fatal("a clip opened before its file was here")
	}
	r.f.Emit(ev(abi.EventTagMotion, func(e *abi.Event) {
		e.Motion = abi.EventMotion{Chat: []byte(chatID), Message: []byte("mp"),
			Result: abi.PathResult{Tag: abi.PathResultTagOk, Ok: abi.PathResultOk{Path: []byte("/m/landed.mov")}}}
	}))
	v := r.video(0)
	r.until("it plays", func() bool { return v.playing && magenta(r.at("mp", 100, 100)) })
	if v.path != "/m/landed.mov" {
		t.Errorf("played %q", v.path)
	}

	r.Play(clickMotion(0)) // bad's
	r.Frames(2)
	r.f.Emit(ev(abi.EventTagMotion, func(e *abi.Event) {
		e.Motion = abi.EventMotion{Chat: []byte(chatID), Message: []byte("bad"),
			Result: abi.PathResult{Tag: abi.PathResultTagErr, Err: abi.PathResultErr{Error: []byte("expired")}}}
	}))
	r.Frames(3)
	if !v.closed || len(r.videos) != 1 || r.has(ui.RoleButton, "Downloading motion photo") {
		t.Errorf("after the failure: first closed %v, %d videos", v.closed, len(r.videos))
	}
	r.Play(clickMotion(1)) // a click again asks again
	r.Frames(2)
	if n := len(r.sent(abi.CommandTagDownloadMotion)); n != 3 {
		t.Errorf("%d motion downloads, want 3", n)
	}
}

// A clip whose photo does not show pauses; out of the transcript a while it
// lets its player go, and plays again once the photo is back. A clip the
// system cannot play opens in its player. The lock goes back to the photo.
func TestAMotionClipOutOfViewAndItsStops(t *testing.T) {
	r := newRig(t)
	odd := mediaFile(t, "odd.mov") // opened only as a regular file of a known format
	r.badVideos = map[string]bool{odd: true}
	r.load(r.photo("mp", 10, "/m/motion.mov"), r.photo("odd", 11, odd))
	r.Play(clickMotion(0))
	v := r.video(0)
	r.until("it plays", func() bool { return v.playing })
	r.p.shift = 3000
	r.Frames(3)
	if v.playing || v.closed {
		t.Fatalf("below the window: playing %v, closed %v", v.playing, v.closed)
	}
	r.p.shift = 0
	r.p.skip = map[string]bool{"mp": true, "odd": true}
	r.Frames(2)
	r.now = r.now.Add(2 * time.Second)
	r.Frames(2)
	if r.openVideos() != 0 {
		t.Fatal("a motion clip out of view kept its player")
	}
	r.p.skip = nil
	w := r.video(1)
	r.until("it plays again", func() bool { return w.playing && magenta(r.at("mp", 100, 100)) })

	r.Play(clickMotion(0)) // odd's
	r.until("it opens in the system", func() bool { return len(r.sh.opened()) == 1 })
	if got := r.sh.opened()[0]; got != odd || r.openVideos() != 0 {
		t.Errorf("opened %q; %d players kept", got, r.openVideos())
	}

	r.Play(clickMotion(0)) // mp's again
	x := r.video(3)
	r.until("it plays", func() bool { return x.playing })
	r.s.Locked()
	r.Frames(2)
	if !x.closed || r.has(ui.RoleButton, "Pause") {
		t.Errorf("the lock: closed %v", x.closed)
	}
}

// A motion clip's steady frames allocate nothing.
func TestMotionFramesAllocateNothing(t *testing.T) {
	r := newRig(t)
	r.load(r.photo("mp", 10, "/m/motion.mov"))
	r.Play(clickMotion(0))
	v := r.video(0)
	r.until("it plays", func() bool { return v.playing && magenta(r.at("mp", 100, 100)) })
	r.Play(away)
	r.Frames(5)
	r.p.quiet = true
	if n := testing.AllocsPerRun(30, func() { v.pos += 33 * time.Millisecond; r.Frames(1) }); n != 0 {
		t.Errorf("a frame of a motion clip allocates %v times", n)
	}
}
