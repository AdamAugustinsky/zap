package av_test

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/img"
	"gonativeui/media"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/ui/screen"
)

func firstFrame(t *testing.T) (string, *image.RGBA) {
	t.Helper()
	path, err := filepath.Abs("../../../../assets/demo/videos/launch.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var frame *image.RGBA
	done := make(chan error, 1)
	onMain(func() {
		err = media.LoadVideoPoster(path, 320, func(p []byte, w, h int, err error) {
			frame = &image.RGBA{Pix: p, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
			done <- err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first frame timed out")
	}
	if frame.Rect.Dx() != 320 || frame.Rect.Dy() != 180 {
		t.Fatalf("fixture dimensions: %v", frame.Rect)
	}
	return path, frame
}

// Match the first frame beneath the normal 40/255 black scrim, excluding
// controls. A square message box must contain, rather than stretch, the clip.
func frameMatches(r *rig, want *image.RGBA, top int) bool {
	bright := false
	for y := 16; y < 65; y += 12 {
		for x := 16; x < 305; x += 24 {
			w, g := want.RGBAAt(x, y), r.at("vid", float32(x), float32(y+top))
			for _, pair := range [][2]uint8{{w.R, g.R}, {w.G, g.G}, {w.B, g.B}} {
				expected := int(pair[0]) * 215 / 255
				bright = bright || expected > 30
				d := int(pair[1]) - expected
				if d < -3 || d > 3 {
					return false
				}
			}
		}
	}
	return bright
}

func TestLocalVideoFilePoster(t *testing.T) {
	path, expected := firstFrame(t)
	r := newRig(t)
	m := video("vid", 10, path, false, false)
	m.Content.Video.Media.Width, m.Content.Video.Media.Height = 320, 320
	r.load(m)
	r.until("first frame from local video", func() bool { return frameMatches(r, expected, 70) })
	if c := r.at("vid", 20, 20); c.R != 0 || c.G != 0 || c.B != 0 {
		t.Fatalf("letterbox is not black: %v", c)
	}
	if len(r.videos) != 0 {
		t.Fatal("poster created a player")
	}
	r.shot("file-poster")
	if dir := os.Getenv("AV_SHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "file-poster-expected.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, expected); err != nil {
			t.Fatal(err)
		}
	}
	r.p.quiet = true
	r.Frames(10)
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("poster frame allocations: %v", n)
	}
}

type heldImage struct{ entered, release chan struct{} }

func TestGIFUsesFilePosterWhilePlayerLoads(t *testing.T) {
	path, expected := firstFrame(t)
	r := newRig(t)
	r.loadVideos = true
	m := video("gif", 10, path, false, true)
	r.poster(&m) // the thumbnail must not permanently hide the file's poster
	r.load(m)
	matches := func() bool {
		for y := 16; y < 65; y += 12 {
			for x := 16; x < 305; x += 24 {
				w, g := expected.RGBAAt(x, y), r.at("gif", float32(x), float32(y))
				for _, p := range [][2]uint8{{w.R, g.R}, {w.G, g.G}, {w.B, g.B}} {
					if d := int(p[1]) - int(p[0]); d < -3 || d > 3 {
						return false
					}
				}
			}
		}
		return true
	}
	r.until("GIF's first frame, not its thumbnail", matches)
	if len(r.videos) != 0 || r.has(ui.RoleLabel, "GIF · 0:12") {
		t.Fatal("decoded GIF poster starts a player or retains the length chip")
	}
	r.shot("gif-poster")
	r.Play(widgettest.Move(ui.RoleButton, "GIF"))
	v := r.video(0)
	if !matches() || v.state != media.VideoLoading || !v.opts.Loop || !v.opts.Muted {
		t.Fatal("loading GIF does not retain its poster and looping/muted options")
	}
	v.state = media.VideoReady
	r.until("GIF frame replaces poster", func() bool { return magenta(r.at("gif", 20, 20)) })
	r.shot("gif-playing")
	r.Play(away)
	r.p.quiet = true
	r.Frames(10)
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("GIF file poster allocates %v times", n)
	}
}

func (h *heldImage) Bytes() []byte { close(h.entered); <-h.release; return nil }
func (*heldImage) Release()        {}

func TestFilePosterLoadingSwitchAndFailure(t *testing.T) {
	path, expected := firstFrame(t)
	r := newRig(t)
	e := &r.p.env
	e.K.Images().Close(time.Second)
	cache := screen.NewImages(e.A, e.K, func(o *img.Options) { o.Workers = 1 })
	t.Cleanup(func() { cache.Close(time.Second) })
	h := &heldImage{make(chan struct{}), make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-h.release:
		default:
			close(h.release)
		}
	})
	cache.Request(img.Key{Hash: 1, W: 1}, img.Leased(h))
	select {
	case <-h.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	m := video("vid", 10, path, false, false)
	r.load(m)
	if n := cache.Stats().Pending; n != 2 {
		t.Fatalf("pending images = %d, want blocker and poster", n)
	}
	if c := r.at("vid", 20, 20); c.R != 0 || c.G != 0 || c.B != 0 {
		t.Fatalf("loading panel: %v", c)
	}
	r.must(ui.RoleButton, "Video")
	f := fake.New(nil)
	a := r.s.AddAccount("2", nil, f)
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Last: true, Chats: []abi.Chat{{ID: []byte(chatID)}}} }),
		ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(chatID), Last: true, Messages: []abi.Message{m}}
		}))
	r.s.SwitchAccount(a.Index)
	r.Frames(2)
	a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
	r.a, r.f = a, f
	r.Frames(3)
	if n := cache.Stats().Pending; n != 3 {
		t.Fatalf("accounts shared a poster key: %d pending", n)
	}
	close(h.release)
	r.until("new account's poster", func() bool { return frameMatches(r, expected, 0) })
	m.Content.Video.Media.Path = []byte(filepath.Join(t.TempDir(), "missing.mp4"))
	r.load(m)
	r.until("failed file poster", func() bool { return cache.Stats().Failed == 2 })
	if c := r.at("vid", 20, 20); c.R != 0 || c.G != 0 || c.B != 0 {
		t.Fatalf("failed panel: %v", c)
	}
	n := cache.Stats().Decodes
	r.Frames(10)
	if cache.Stats().Decodes != n {
		t.Fatal("failed poster retried")
	}
}

func TestFilePosterDoesNotDownloadOrReplaceRoundNotes(t *testing.T) {
	r := newRig(t, video("vid", 10, "", false, false))
	r.Frames(3)
	if r.p.rects["vid"].H != 68 || len(r.sent(abi.CommandTagDownload)) != 0 {
		t.Fatal("non-local video did not remain an idle file card")
	}
	r.load(video("vid", 10, "/not-opened.mp4", true, false))
	if r.p.env.K.Images().Stats().Entries != 0 {
		t.Fatal("round note requested a file poster")
	}
}
