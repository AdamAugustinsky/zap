package av_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/conversation/av"
)

// poster puts a small green PNG in the fake core's blobs, as the core keeps
// a message's inline thumbnail, and gives the message it.
func (r *rig) poster(m *abi.Message) {
	r.t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 32, 18))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = 30, 160, 60, 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		r.t.Fatal(err)
	}
	ref, err := r.f.PutBlob(b.Bytes())
	if err != nil {
		r.t.Fatal(err)
	}
	m.Thumbnail, m.HasThumbnail = ref, true
}

// load delivers more messages of the chat.
func (r *rig) load(msgs ...abi.Message) {
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 2, Last: true, Messages: msgs}
	}))
	r.Frames(3)
}

func (r *rig) viewerOn(id string) bool {
	w := &r.s.Win
	return w.Want[state.WinViewer] && w.ViewerMsg.Len() == 1 && string(w.ViewerMsg.At(0)) == id &&
		w.ViewerChat == r.a.Open && !w.ViewerHasCard
}

// at is the colour of the last frame at (x, y) of message id's block.
func (r *rig) at(id string, x, y float32) color.RGBA {
	b := r.p.rects[id]
	return r.Image().RGBAAt(int(b.X+x), int(b.Y+y))
}

func green(c color.RGBA) bool   { return int(c.G) > int(c.R)+60 && int(c.G) > int(c.B)+60 }
func magenta(c color.RGBA) bool { return int(c.R) > int(c.G)+80 && int(c.B) > int(c.G)+80 }

func TestVideoColourChecksRejectNeutralFrames(t *testing.T) {
	for v := 0; v <= 255; v++ {
		c := color.RGBA{uint8(v), uint8(v), uint8(v), 255}
		if green(c) || magenta(c) {
			t.Errorf("neutral frame %v accepted as green or magenta", c)
		}
	}
	if !green(color.RGBA{30, 160, 60, 255}) || !magenta(color.RGBA{200, 30, 200, 255}) {
		t.Error("fixture colours must be recognized")
	}
}

// away moves the pointer off every block.
var away = widgettest.MoveTo(400, 1390)

func (r *rig) has(role ui.Role, name string) bool { _, ok := r.Find(role, name); return ok }

// A downloaded video plays in its bubble when clicked (MK-014): its frames
// over the poster, no viewer, the controls while the pointer is over it or
// while it is paused, and a click on it pauses and plays.
func TestAVideoPlaysInItsBubble(t *testing.T) {
	r := newRig(t)
	m := video("vid", 10, "/m/clip.mp4", false, false)
	r.poster(&m)
	r.load(m)
	if got := r.p.rects["vid"]; got.W != 320 || got.H != 180 {
		t.Fatalf("size %v × %v, want 320 × 180", got.W, got.H)
	}
	r.until("the poster is drawn", func() bool { return green(r.at("vid", 20, 20)) })
	r.must(ui.RoleLabel, "0:12")
	if len(r.videos) != 0 {
		t.Fatal("a video opened before a click")
	}
	r.Click(ui.RoleButton, "Video")
	v := r.video(0)
	if v.path != "/m/clip.mp4" || !v.playing || v.rate != 1 || v.muted || v.opts.Loop {
		t.Fatalf("opened %+v", *v)
	}
	r.until("its frame shows", func() bool { return magenta(r.at("vid", 20, 20)) })
	if r.s.Win.Want[state.WinViewer] || len(r.sent(abi.CommandTagDownload)) != 0 {
		t.Error("the viewer opened, or a download was asked for")
	}
	r.must(ui.RoleButton, "Fill the window") // the pointer is over it
	r.Play(away)
	r.Frames(2)
	if r.has(ui.RoleButton, "Fill the window") || r.has(ui.RoleLabel, "0:12") {
		t.Error("controls or the length over a playing clip the pointer is not on")
	}
	r.Click(ui.RoleButton, "Video")
	if v.playing {
		t.Fatal("a click did not pause it")
	}
	r.Play(away)
	v.pos = 3 * time.Second
	r.Frames(2)
	r.must(ui.RoleLabel, "0:03 / 0:12") // paused: the controls stay
	r.must(ui.RoleButton, "Fill the window")
	r.Click(ui.RoleButton, "Play")
	if !v.playing || len(r.videos) != 1 {
		t.Errorf("the controls' Play: playing %v, %d videos", v.playing, len(r.videos))
	}
}

// The controls' speed chip cycles 1x, 1.5x and 2x and the sound switch
// mutes; both carry over to the next clip (VO-004). The seek bar seeks, and
// "Fill the window" plays it in the viewer, stopping it in the bubble.
func TestVideoControls(t *testing.T) {
	r := newRig(t)
	a, b := video("a", 10, "/m/a.mp4", false, false), video("b", 11, "/m/b.mp4", false, false)
	r.poster(&a)
	r.poster(&b)
	r.load(a, b)
	r.Play(widgettest.Click(ui.RoleButton, "Video").Nth(0))
	v := r.video(0)
	r.Click(ui.RoleButton, "Pause")
	if v.playing {
		t.Fatal("Pause did not pause it")
	}
	r.Click(ui.RoleButton, "Playback speed 1x")
	if v.rate != 1.5 {
		t.Errorf("rate %v after one click", v.rate)
	}
	r.Frames(1)
	r.Click(ui.RoleButton, "Playback speed 1.5x")
	r.Click(ui.RoleButton, "Mute")
	r.Frames(1)
	if v.rate != 2 || !v.muted {
		t.Errorf("rate %v, muted %v", v.rate, v.muted)
	}
	r.must(ui.RoleButton, "Unmute")
	r.Click(ui.RoleSlider, "Video") // its middle
	if v.seeks != 1 || v.seekTo < 5*time.Second || v.seekTo > 7*time.Second || v.playing {
		t.Errorf("seek: %d to %v, playing %v", v.seeks, v.seekTo, v.playing)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Video").Nth(1))
	w := r.video(1)
	if !v.closed || w.rate != 2 || !w.muted || !w.playing {
		t.Errorf("the next clip: first closed %v; rate %v, muted %v, playing %v", v.closed, w.rate, w.muted, w.playing)
	}
	if got := av.VideoSpeed(r.s); got != 2 || r.s.Settings.VoiceSpeed != 0 && r.s.Settings.VoiceSpeed != 1 {
		t.Errorf("video speed %v, voice speed %v", got, r.s.Settings.VoiceSpeed)
	}
	w.pos = 3 * time.Second
	r.Click(ui.RoleButton, "Fill the window")
	r.Frames(2)
	if !w.closed || !r.viewerOn("b") || !r.s.Win.ViewerPlay || r.s.Win.ViewerAt != 3*time.Second {
		t.Errorf("Fill the window: closed %v, viewer %v, playing %v at %v", w.closed, r.viewerOn("b"), r.s.Win.ViewerPlay, r.s.Win.ViewerAt)
	}
}

// A double click on a clip plays and pauses it with its two clicks, then
// plays it in the viewer window from where it stood, stopping it in the
// bubble (MK-016). A clip that was not loaded (another one was) plays there
// from its start.
func TestADoubleClickPlaysTheClipInTheViewer(t *testing.T) {
	r := newRig(t)
	a, b := video("a", 10, "/m/a.mp4", false, false), video("b", 11, "/m/b.mp4", false, false)
	r.poster(&a)
	r.poster(&b)
	r.load(a, b)
	r.Play(widgettest.Click(ui.RoleButton, "Video").Nth(0))
	v := r.video(0)
	v.pos = 5 * time.Second
	r.Play(widgettest.DoubleClick(ui.RoleButton, "Video").Nth(0))
	r.Frames(2)
	if len(r.videos) != 1 || !v.closed || v.plays != 2 || v.pauses != 1 {
		t.Fatalf("%d videos; closed %v, %d plays, %d pauses", len(r.videos), v.closed, v.plays, v.pauses)
	}
	if !r.viewerOn("a") || !r.s.Win.ViewerPlay || r.s.Win.ViewerAt != 5*time.Second {
		t.Errorf("viewer on a %v, playing %v at %v", r.viewerOn("a"), r.s.Win.ViewerPlay, r.s.Win.ViewerAt)
	}

	r.s.Win.Want[state.WinViewer] = false
	r.Play(away, widgettest.Click(ui.RoleButton, "Video").Nth(0)) // a plays again
	v = r.video(1)
	v.pos = 7 * time.Second
	r.Play(widgettest.DoubleClick(ui.RoleButton, "Video").Nth(1))
	r.Frames(2)
	if !r.viewerOn("b") || !r.s.Win.ViewerPlay || r.s.Win.ViewerAt != 0 || !v.closed {
		t.Errorf("viewer on b %v, playing %v at %v; a closed %v", r.viewerOn("b"), r.s.Win.ViewerPlay, r.s.Win.ViewerAt, v.closed)
	}
}

// A round video note plays inside its circle with the progress ring round
// it and its time on the pill (MK-017); its first play sends the played
// receipt.
func TestARoundNotePlaysInItsCircle(t *testing.T) {
	r := newRig(t)
	m := video("note", 10, "/m/note.mp4", true, false)
	r.poster(&m)
	r.load(m)
	if got := r.p.rects["note"]; got.W != 220 || got.H != 220 {
		t.Fatalf("size %v × %v", got.W, got.H)
	}
	r.must(ui.RoleLabel, "0:12")
	r.Click(ui.RoleButton, "Video message")
	v := r.video(0)
	if !v.playing || len(r.sent(abi.CommandTagMarkPlayed)) != 1 {
		t.Fatalf("playing %v, receipts %d", v.playing, len(r.sent(abi.CommandTagMarkPlayed)))
	}
	r.Play(away)
	v.pos = 6 * time.Second
	r.Frames(2)
	r.must(ui.RoleLabel, "0:06")
	if !magenta(r.at("note", 110, 50)) {
		t.Errorf("no frame in the circle: %v", r.at("note", 110, 50))
	}
	acc := r.p.env.K.Theme().Colors[theme.Accent]
	isAcc := func(c color.RGBA) bool {
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		return d(c.R, uint8(acc>>24)) < 40 && d(c.G, uint8(acc>>16)) < 40 && d(c.B, uint8(acc>>8)) < 40
	}
	// half played: the ring is lit from twelve o'clock to six, clockwise
	for _, p := range []struct {
		name string
		x, y float32
		lit  bool
	}{{"one o'clock", 110 + 54, 110 - 93.5, true}, {"three", 218, 110, true}, {"half past four", 110 + 76.4, 110 + 76.4, true},
		{"half past seven", 110 - 76.4, 110 + 76.4, false}, {"nine", 2, 110, false}, {"eleven", 110 - 54, 110 - 93.5, false}} {
		if c := r.at("note", p.x, p.y); isAcc(c) != p.lit {
			t.Errorf("the ring at %s: %v, lit %v", p.name, c, p.lit)
		}
	}
	r.shot("note-playing")
	r.Click(ui.RoleButton, "Video message")
	r.Click(ui.RoleButton, "Video message")
	if !v.playing || len(r.sent(abi.CommandTagMarkPlayed)) != 1 {
		t.Errorf("pause and play: playing %v, receipts %d", v.playing, len(r.sent(abi.CommandTagMarkPlayed)))
	}
}

// A clip not downloaded says its size; a click downloads it and plays it
// once it is here.
func TestAClipNotDownloadedPlaysOnceItIsHere(t *testing.T) {
	r := newRig(t)
	m := video("vid", 10, "", false, false)
	r.poster(&m)
	r.load(m)
	r.must(ui.RoleLabel, "0:12 · 2.3 MB")
	r.Click(ui.RoleButton, "Video")
	r.Frames(2)
	if dl := r.sent(abi.CommandTagDownload); len(dl) != 1 || !dl[0].Download.Manual || string(dl[0].Download.Message) != "vid" {
		t.Errorf("downloads %+v", dl)
	}
	if len(r.videos) != 0 || r.s.Win.Want[state.WinViewer] {
		t.Fatal("something played before the file was here")
	}
	r.f.Emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(chatID), Message: []byte("vid"),
			Result: abi.PathResult{Tag: abi.PathResultTagOk, Ok: abi.PathResultOk{Path: []byte("/m/landed.mp4")}}}
	}))
	v := r.video(0)
	r.until("it plays", func() bool { return v.playing })
	if v.path != "/m/landed.mp4" {
		t.Errorf("played %q", v.path)
	}
}

// A downloaded GIF is its poster; under the pointer it loops muted, and a
// click opens it in the system's player (MK-013). Left alone it lets its
// player go.
func TestAGIFLoopsMutedUnderThePointer(t *testing.T) {
	r := newRig(t)
	loop := mediaFile(t, "loop.mp4") // opened only as a regular file of a known format
	m := video("gif", 10, loop, false, true)
	r.poster(&m)
	r.load(m)
	r.until("the poster is drawn", func() bool { return green(r.at("gif", 20, 20)) })
	r.must(ui.RoleLabel, "GIF · 0:12")
	if len(r.videos) != 0 || !green(r.at("gif", 160, 90)) { // no badge in its middle
		t.Fatalf("a downloaded GIF is a picture until the pointer is over it: %d videos, %v", len(r.videos), r.at("gif", 160, 90))
	}
	r.Play(widgettest.Move(ui.RoleButton, "GIF"))
	v := r.video(0)
	r.until("it plays", func() bool { return v.playing && magenta(r.at("gif", 20, 20)) })
	if !v.opts.Loop || !v.opts.Muted || r.has(ui.RoleLabel, "GIF · 0:12") {
		t.Errorf("options %+v; the chip over a looping GIF %v", v.opts, r.has(ui.RoleLabel, "GIF · 0:12"))
	}
	r.Play(away)
	r.Frames(2)
	if v.playing || !green(r.at("gif", 20, 20)) {
		t.Errorf("the pointer left: playing %v, %v", v.playing, r.at("gif", 20, 20))
	}
	r.now = r.now.Add(2 * time.Second)
	r.Frames(2)
	if !v.closed {
		t.Error("a GIF left alone keeps its player")
	}
	r.Click(ui.RoleButton, "GIF")
	r.until("the GIF opens", func() bool { return len(r.sh.opened()) == 1 })
	if got := r.sh.opened(); got[0] != loop || r.s.Win.Want[state.WinViewer] {
		t.Errorf("opened %v, viewer %v", got, r.s.Win.Want[state.WinViewer])
	}
}

func TestGIFPausesWithoutDrawingAndResumesOnHover(t *testing.T) {
	for _, mode := range []string{"hidden", "clipped", "not built", "unfocused"} {
		t.Run(mode, func(t *testing.T) {
			r := newRig(t)
			m := video("gif", 10, "/m/loop.mp4", false, true)
			r.poster(&m)
			r.load(m)
			r.Play(widgettest.Move(ui.RoleButton, "GIF"))
			v := r.video(0)
			if !v.playing || v.frames == 0 {
				t.Fatal("GIF never played")
			}
			switch mode {
			case "hidden":
				r.p.hidden = true
				r.p.skip = map[string]bool{"gif": true}
			case "clipped":
				r.p.shift = 3000
			case "not built":
				r.p.skip = map[string]bool{"gif": true}
			case "unfocused":
				r.p.unfocused = true
			}
			r.Frames(3)
			if v.playing {
				t.Fatal("unseen/inactive GIF is still playing")
			}
			frames := v.frames
			r.p.quiet = true
			if n := testing.AllocsPerRun(20, func() { r.Frames(1) }); n != 0 {
				t.Errorf("paused GIF allocates %v times", n)
			}
			if v.frames != frames {
				t.Error("paused GIF drew frames")
			}
			r.now = r.now.Add(2 * time.Second)
			r.Frames(3)
			if c := r.Context(); c.Animating() || c.WakeAfter() != 0 {
				t.Errorf("idle GIF requests frames: %v, %d", c.Animating(), c.WakeAfter())
			}
			r.p.hidden, r.p.unfocused, r.p.shift, r.p.skip = false, false, 0, nil
			r.Frames(3)
			r.Play(widgettest.Move(ui.RoleButton, "GIF"))
			r.until("GIF resumes on hover", func() bool {
				last := r.videos[len(r.videos)-1]
				return last.playing && last.frames > 0 && last.seekTo == 0
			})
		})
	}
}

func TestGIFSteadyPlaybackAllocatesNothing(t *testing.T) {
	r := newRig(t)
	m := video("gif", 10, "/m/loop.mp4", false, true)
	r.poster(&m)
	r.load(m)
	r.Play(widgettest.Move(ui.RoleButton, "GIF"))
	v := r.video(0)
	r.p.quiet = true
	r.Frames(20)
	before := v.frames
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("playing GIF allocates %v times", n)
	}
	if !v.playing || v.frames <= before || !v.muted || !v.opts.Loop {
		t.Fatal("allocation check did not exercise muted looping playback")
	}
}

func TestGIFRemovedFromTheTranscriptSchedulesItsPause(t *testing.T) {
	r := newRig(t)
	m := video("gif", 10, "/m/loop.mp4", false, true)
	r.poster(&m)
	r.load(m)
	r.Play(widgettest.Move(ui.RoleButton, "GIF"))
	v := r.video(0)
	r.Frames(30) // settle the hover before removing the block
	r.p.skip = map[string]bool{"gif": true}
	r.Frames(1)
	// The ticker runs before Build: one missed frame is needed to know the
	// block disappeared. Without an owed wake it never gets to observe that.
	if c := r.Context(); v.playing && !c.Animating() && c.WakeAfter() == 0 {
		t.Fatal("unbuilt GIF keeps playing with no frame owed to pause it")
	}
	r.Frames(1)
	if v.playing {
		t.Fatal("the recovery frame did not pause the GIF")
	}
}

func TestInlineVideoHiddenOrRemovedPausesWithoutFurtherInput(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "hidden"}[hidden], func(t *testing.T) {
			r := newRig(t)
			m := video("vid", 10, "/m/clip.mp4", false, false)
			r.poster(&m)
			r.load(m)
			r.Click(ui.RoleButton, "Video")
			v := r.video(0)
			r.Frames(30)
			r.p.hidden, r.p.skip = hidden, map[string]bool{"vid": true}
			r.Frames(1)
			if c := r.Context(); v.playing && (hidden || !c.Animating() && c.WakeAfter() == 0) {
				t.Fatal("unbuilt video keeps playing without a recovery wake")
			}
			r.Frames(1)
			if v.playing {
				t.Fatal("video did not pause")
			}
		})
	}
}

func TestGIFDownloadFollowsTheAccountSetting(t *testing.T) {
	r := newRig(t)
	m := video("gif", 10, "", false, true)
	r.poster(&m)
	r.load(m)
	r.Play(widgettest.Move(ui.RoleButton, "GIF"))
	if len(r.sent(abi.CommandTagDownload)) != 0 || len(r.videos) != 0 {
		t.Fatal("hover downloads or plays with automatic downloads disabled")
	}
	r.a.Settings.AutoDownload = true
	r.Frames(3)
	dl := r.sent(abi.CommandTagDownload)
	if len(dl) != 1 || dl[0].Download.Manual || string(dl[0].Download.Message) != "gif" {
		t.Fatalf("automatic GIF download: %+v", dl)
	}
	if len(r.videos) != 0 {
		t.Fatal("GIF played before its file arrived")
	}
}

// A clip the system cannot play opens in its player, and says so (MK-015).
func TestAClipTheSystemCannotPlayOpensInItsPlayer(t *testing.T) {
	r := newRig(t)
	odd := mediaFile(t, "odd.webm") // opened only as a regular file of a known format
	r.badVideos = map[string]bool{odd: true}
	m := video("odd", 10, odd, false, false)
	r.poster(&m)
	r.load(m)
	r.Click(ui.RoleButton, "Video")
	r.until("it opens in the system", func() bool { return len(r.sh.opened()) == 1 })
	if got := r.sh.opened()[0]; got != odd {
		t.Errorf("opened %q", got)
	}
	if n := len(r.s.Toasts); n == 0 || string(r.s.Toasts[n-1].Text) != "This video opens in your system player" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
	if r.openVideos() != 0 {
		t.Error("its player was kept")
	}
}

// While its clip loads, a block shows a spinner; it plays once loaded.
func TestALoadingClipShowsASpinner(t *testing.T) {
	r := newRig(t)
	r.loadVideos = true
	m := video("note", 10, "/m/note.mp4", true, false)
	r.poster(&m)
	r.load(m)
	r.Click(ui.RoleButton, "Video message")
	v := r.video(0)
	r.Frames(2)
	r.must(ui.RoleProgressBar, "Loading…")
	v.state = media.VideoReady
	r.Frames(2)
	if r.has(ui.RoleProgressBar, "Loading…") || !v.playing {
		t.Errorf("loaded: spinner %v, playing %v", r.has(ui.RoleProgressBar, "Loading…"), v.playing)
	}
}

// A clip whose box does not show pauses at once; one scrolled out of the
// transcript stops after a while and lets its player go.
func TestAClipThatDoesNotShowPausesThenStops(t *testing.T) {
	r := newRig(t)
	m := video("vid", 10, "/m/clip.mp4", false, false)
	r.poster(&m)
	r.load(m)
	r.Click(ui.RoleButton, "Video")
	v := r.video(0)
	r.p.shift = 3000 // built, below the window
	r.Frames(3)
	if v.playing || v.closed {
		t.Fatalf("out of the window: playing %v, closed %v", v.playing, v.closed)
	}
	r.p.shift = 0
	r.Frames(2)
	if v.playing || v.closed {
		t.Fatalf("back: playing %v, closed %v (it waits for a click)", v.playing, v.closed)
	}
	r.Click(ui.RoleButton, "Video")
	r.p.skip = map[string]bool{"vid": true} // scrolled out of the transcript
	r.Frames(2)
	if v.playing || v.closed {
		t.Fatalf("not built: playing %v, closed %v", v.playing, v.closed)
	}
	if ui := r.Offscreen().UI; ui.WakeAfter() == 0 {
		t.Error("no frame asked for to let it go")
	}
	r.now = r.now.Add(2 * time.Second)
	r.Frames(2)
	if r.openVideos() != 0 {
		t.Fatal("a clip out of view kept its player")
	}
	r.p.skip = nil
	r.Frames(2)
	r.must(ui.RoleLabel, "0:12")
}

// A clip played to its end goes back to its poster, and plays again from a
// new player; the framework's Ended event does the same at once.
func TestAClipThatEndsGoesBackToItsPoster(t *testing.T) {
	r := newRig(t)
	m := video("vid", 10, "/m/clip.mp4", false, false)
	r.poster(&m)
	r.load(m)
	r.Click(ui.RoleButton, "Video")
	v := r.video(0)
	v.end()
	r.Frames(2)
	if !v.closed {
		t.Fatal("an ended clip kept its player")
	}
	r.Play(away)
	r.Frames(1)
	r.must(ui.RoleLabel, "0:12")
	r.Click(ui.RoleButton, "Video")
	w := r.video(1)
	if !w.playing {
		t.Fatal("it did not play again")
	}
	ended := &app.Event{Kind: app.EvService, Source: platform.SourceMedia, A: int64(media.Ended), B: int64(w.id)}
	if !av.Event(r.s, ended) || !w.closed {
		t.Error("the Ended event did not end it")
	}
}

// One sound at a time: a clip stops the voice note and its run, and a voice
// note stops the clip. Locking, switching account and opening the viewer
// stop it too.
func TestOneSoundAtATime(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/30.ogg", 30))
	m := video("vid", 11, "/m/clip.mp4", false, false)
	r.poster(&m)
	r.load(m)
	r.Play(clickPlay(0))
	p := r.player(0)
	r.until("the note plays", func() bool { return p.playing })
	r.Click(ui.RoleButton, "Video")
	v := r.video(0)
	if !p.closed || !v.playing || r.s.Voice.Chat != 0 {
		t.Fatalf("the clip: note closed %v, clip playing %v, run %v", p.closed, v.playing, r.s.Voice.Chat)
	}
	r.Play(away, clickPlay(0))
	q := r.player(1)
	r.until("the note plays again", func() bool { return q.playing })
	if !v.closed {
		t.Fatal("the voice note did not stop the clip")
	}
	stops := []struct {
		name string
		do   func()
	}{
		{"lock", func() { r.s.Locked() }},
		{"account", func() { r.s.AddAccount("2", nil, fake.New(nil)); r.s.SwitchAccount(1) }},
		{"viewer", func() { r.s.OpenViewer(r.a.Open, []byte("v1"), -1) }},
	}
	for i, s := range stops {
		r.s.SwitchAccount(0)
		r.s.Win.Want[state.WinViewer] = false
		r.Frames(2)
		r.Play(away, widgettest.Click(ui.RoleButton, "Video"))
		v := r.video(1 + i)
		s.do()
		r.Frames(2)
		if !v.closed {
			t.Errorf("%s did not stop the clip", s.name)
		}
	}
}

// Steady frames of a clip allocate nothing: playing under the pointer (the
// controls) and away from it, and paused. Paused, it asks for no frames.
func TestVideoFramesAllocateNothing(t *testing.T) {
	r := newRig(t)
	vid, note := video("vid", 11, "/m/clip.mp4", false, false), video("note", 12, "/m/note.mp4", true, false)
	r.poster(&vid)
	r.poster(&note)
	r.load(vid, note, video("gif", 13, "/m/g.mp4", false, true))
	r.Click(ui.RoleButton, "Video")
	v := r.video(0)
	r.Frames(10)
	time.Sleep(50 * time.Millisecond) // the posters decode
	r.Frames(10)
	r.p.quiet = true
	step := func() {
		v.pos += 33 * time.Millisecond
		r.Frames(1)
	}
	if n := testing.AllocsPerRun(30, step); n != 0 {
		t.Errorf("a frame playing under the pointer allocates %v times", n)
	}
	r.Play(away)
	r.Frames(3)
	if n := testing.AllocsPerRun(30, step); n != 0 {
		t.Errorf("a frame playing allocates %v times", n)
	}
	if ui := r.Offscreen().UI; ui.WakeAfter() == 0 && !ui.Animating() {
		t.Error("no frame asked for while playing")
	}
	r.Click(ui.RoleButton, "Video")
	r.Play(away)
	r.Frames(5)
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("a paused frame allocates %v times", n)
	}
	if ui := r.Offscreen().UI; ui.WakeAfter() != 0 || ui.Animating() {
		t.Errorf("a paused clip asks for frames (wake in %d ns, animating %v)", ui.WakeAfter(), ui.Animating())
	}
	r.Click(ui.RoleButton, "Video message")
	r.Play(away)
	r.Frames(3)
	w := r.video(1)
	if n := testing.AllocsPerRun(30, func() { w.pos += 33 * time.Millisecond; r.Frames(1) }); n != 0 {
		t.Errorf("a round note's frame allocates %v times", n)
	}
}

// A video with neither a poster nor its file is a file row: a click
// downloads it.
func TestVideoWithoutPosterIsAFileRow(t *testing.T) {
	r := newRig(t, video("old", 10, "", false, false))
	if got := r.p.rects["old"]; got.W != 320 || got.H != 68 {
		t.Fatalf("size %v × %v", got.W, got.H)
	}
	r.must(ui.RoleLabel, "0:12 · 2.3 MB")
	r.Click(ui.RoleButton, "Download")
	r.Frames(2)
	if dl := r.sent(abi.CommandTagDownload); len(dl) != 1 || !dl[0].Download.Manual {
		t.Errorf("downloads %+v", dl)
	}
}

// Steady frames allocate nothing: a voice note playing, then paused, and the
// videos beside it. Has and Size allocate nothing either.
func TestFramesAllocateNothing(t *testing.T) {
	vid, note := video("vid", 11, "/m/clip.mp4", false, false), video("note", 12, "", true, false)
	song := voice("song", 13, false, "/v/9.ogg", 9)
	song.Content.Audio.VoiceNote = false
	r := newRig(t)
	r.poster(&vid)
	r.poster(&note)
	r.load(voice("v1", 10, false, "/v/30.ogg", 30), vid, note, song, voice("v2", 14, false, "", 4))
	r.Play(clickPlay(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	r.Frames(10)
	time.Sleep(50 * time.Millisecond) // the posters decode
	r.Frames(10)
	r.p.quiet = true
	r.Frames(5)
	step := func() {
		p.pos += 40 * time.Millisecond
		r.Frames(1)
	}
	if n := testing.AllocsPerRun(30, step); n != 0 {
		t.Errorf("a frame while playing allocates %v times", n)
	}
	if ui := r.Offscreen().UI; ui.WakeAfter() == 0 && !ui.Animating() {
		t.Error("no frame asked for while playing")
	}
	r.Click(ui.RoleButton, "Pause")
	r.Frames(5)
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("a frame while paused allocates %v times", n)
	}
	if ui := r.Offscreen().UI; ui.WakeAfter() != 0 || ui.Animating() {
		t.Errorf("a paused note asks for frames (wake in %d ns, animating %v)", ui.WakeAfter(), ui.Animating())
	}
}

func clickPlay(n int) widgettest.Step { return widgettest.Click(ui.RoleButton, "Play").Nth(n) }

// The blocks side by side, theirs and ours, for a look by eye ($AV_SHOT):
// a voice note playing (gallery), then an inline clip paused with its
// controls (gallery-video).
func TestGallery(t *testing.T) {
	vid, note, gif := video("vid", 11, "/m/clip.mp4", false, false), video("note", 12, "", true, false), video("gif", 15, "/m/g.mp4", false, true)
	song := voice("song", 13, true, "/v/9.ogg", 9)
	song.Content.Audio.VoiceNote = false
	mine := voice("mine", 14, true, "/v/5.ogg", 5)
	failed := voice("failed", 16, false, "", 3)
	failed.Content.Audio.Media.State = abi.MediaState{Tag: abi.MediaStateTagFailed, Failed: abi.MediaStateFailed{Error: []byte("Expired")}}
	r := newRig(t)
	r.poster(&vid)
	r.poster(&note)
	r.poster(&gif)
	r.load(voice("v1", 10, false, "/v/30.ogg", 30), vid, note, song, mine, gif, failed, video("old", 17, "", false, false))
	r.s.Settings.VoiceSpeed = 1.5
	r.Play(clickPlay(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	p.pos = 11 * time.Second
	r.Frames(5)
	time.Sleep(50 * time.Millisecond)
	r.Frames(5)
	r.shot("gallery")
	r.Click(ui.RoleButton, "Video")
	v := r.video(0)
	r.Click(ui.RoleButton, "Pause")
	v.pos = 4 * time.Second
	r.Click(ui.RoleButton, "Playback speed 1x")
	r.Play(away)
	r.Frames(3)
	r.shot("gallery-video")
}
