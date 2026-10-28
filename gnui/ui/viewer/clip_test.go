package viewer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/media"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// fakeClip is a video that plays by counting frames: it moves 40 ms a frame
// while playing, draws a texture of its own, and records what it was told.
type fakeClip struct {
	path     string
	id       uint64
	state    media.VideoState
	w, h     int
	dur, pos time.Duration
	playing  bool
	ended    bool
	closed   bool
	muted    bool
	rate     float32
	frames   int
	seeks    []time.Duration
	tex      gpu.TextureID
	texR     *gpu.Renderer
}

func (f *fakeClip) ID() uint64              { return f.id }
func (f *fakeClip) State() media.VideoState { return f.state }
func (f *fakeClip) Size() (int, int)        { return f.w, f.h }
func (f *fakeClip) Duration() time.Duration { return f.dur }
func (f *fakeClip) Position() time.Duration { return f.pos }
func (f *fakeClip) Playing() bool           { return f.playing }
func (f *fakeClip) Ended() bool             { return f.ended }
func (f *fakeClip) Pause()                  { f.playing = false }
func (f *fakeClip) Close()                  { f.closed = true }
func (f *fakeClip) SetMuted(m bool)         { f.muted = m }
func (f *fakeClip) SetRate(r float32)       { f.rate = r }
func (f *fakeClip) Play() {
	if f.ended {
		f.pos, f.ended = 0, false
	}
	f.playing = true
}
func (f *fakeClip) Seek(d time.Duration) {
	f.seeks = append(f.seeks, d)
	f.pos, f.ended = d, false
}

func (f *fakeClip) Frame(r *gpu.Renderer, now int64) (gpu.TextureID, int, int, int64) {
	if f.state != media.VideoReady || r == nil {
		return 0, 0, 0, 0
	}
	if f.tex == 0 || f.texR != r {
		f.tex, f.texR = r.NewTexture(gpu.TexRGBA8, f.w, f.h), r
		px := make([]byte, f.w*f.h*4)
		for i := range px {
			px[i] = 0x80
		}
		r.Upload(f.tex, 0, 0, f.w, f.h, px, f.w*4)
	}
	var next int64
	if f.playing {
		f.frames++
		f.pos += 40 * time.Millisecond
		if f.pos >= f.dur {
			f.pos, f.playing, f.ended = f.dur, false, true
		} else {
			next = int64(40 * time.Millisecond)
		}
	}
	return f.tex, f.w, f.h, next
}

// clips is what the viewer opened, in order, and what the next open does.
type clips struct {
	opened []*fakeClip
	fail   error
	ready  media.VideoState
}

// fakeClips makes every clip the viewer opens in the test a fakeClip, 640×360
// and 30 s long.
func fakeClips(t *testing.T) *clips {
	t.Helper()
	cs := &clips{}
	prev := openClip
	openClip = func(path string) (clip, error) {
		if cs.fail != nil {
			return nil, cs.fail
		}
		f := &fakeClip{path: path, id: uint64(len(cs.opened) + 1), state: cs.ready, w: 640, h: 360, dur: 30 * time.Second}
		cs.opened = append(cs.opened, f)
		return f, nil
	}
	t.Cleanup(func() { openClip = prev })
	return cs
}

func (c *clips) last() *fakeClip {
	if len(c.opened) == 0 {
		return nil
	}
	return c.opened[len(c.opened)-1]
}

func clipFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.mp4")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func openClipRig(t *testing.T) (*rig, *clips) {
	t.Helper()
	cs := fakeClips(t)
	cs.ready = media.VideoReady
	r := open(t, album{mediaItem("m1", clipFile(t), true), mediaItem("m2", clipFile(t), true)}, "m1")
	r.wait(ui.RoleButton, "Play or pause the video")
	return r, cs
}

// VW-009: the play button plays the clip in the window, with the controls.
func TestAClipPlaysInTheWindowWithItsControls(t *testing.T) {
	r, cs := openClipRig(t)
	if len(cs.opened) != 0 {
		t.Fatal("a clip does not play until asked")
	}
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(4)
	v := cs.last()
	if v == nil || !v.playing || filepath.Base(v.path) != "c.mp4" {
		t.Fatalf("Play opens and plays the file: %+v", v)
	}
	if len(r.shell().opened()) != 0 {
		t.Error("the system player is not used")
	}
	for _, n := range []struct {
		role ui.Role
		name string
	}{{ui.RoleButton, "Pause"}, {ui.RoleButton, "Mute"}, {ui.RoleButton, "1x"}, {ui.RoleSlider, "Seek"}, {ui.RoleButton, "Back to the message (Esc)"}, {ui.RoleImage, "Video"}} {
		if !r.has(n.role, n.name) {
			t.Errorf("no %s %q", n.name, n.name)
		}
	}
	if r.has(ui.RoleButton, "Play or pause the video") {
		t.Error("the disc goes while it plays")
	}
	pos := v.pos
	r.Frames(5)
	if v.pos <= pos || v.frames < 5 {
		t.Errorf("frames arrive: %v after %v, %d frames", v.pos, pos, v.frames)
	}
	// A click on the frame pauses, and again plays.
	r.Click(ui.RoleImage, "Video")
	r.Frames(2)
	if v.playing {
		t.Error("a click on the frame pauses")
	}
	r.Must(ui.RoleButton, "Play")
	r.Click(ui.RoleButton, "Play")
	r.Frames(2)
	if !v.playing {
		t.Error("the Play button plays")
	}
	r.Click(ui.RoleButton, "Pause")
	r.Frames(2)
	if v.playing {
		t.Error("the Pause button pauses")
	}
}

func TestTheClipsKeys(t *testing.T) {
	r, cs := openClipRig(t)
	r.Key(ui.KeySpace, 0)
	r.Frames(3)
	v := cs.last()
	if v == nil || !v.playing {
		t.Fatal("Space starts the clip")
	}
	r.Key(ui.KeySpace, 0)
	r.Frames(2)
	if v.playing {
		t.Error("Space pauses")
	}
	r.Key(ui.KeySpace, 0)
	r.Frames(2)
	if !v.playing {
		t.Error("Space plays")
	}
	r.Key('M', 0)
	r.Frames(2)
	if !v.muted || !r.has(ui.RoleButton, "Unmute") {
		t.Error("M mutes")
	}
	for _, want := range []float32{1.5, 2, 1} {
		r.Key('S', 0)
		r.Frames(2)
		if v.rate != want {
			t.Errorf("S cycles the speed: %v, want %v", v.rate, want)
		}
	}
	r.Play(widgettest.Pause(1))
	pos := v.pos
	r.Key(ui.KeyRight, 0)
	r.Frames(2)
	if len(v.seeks) != 1 || v.seeks[0] < pos+5*time.Second || v.seeks[0] > pos+5*time.Second+200*time.Millisecond {
		t.Errorf("→ jumps five seconds: %v from %v", v.seeks, pos)
	}
	if r.shown() != "m1" {
		t.Error("the arrows do not step the album while the clip is up")
	}
	r.Key(ui.KeyLeft, 0)
	r.Frames(2)
	if len(v.seeks) != 2 || v.seeks[1] >= v.seeks[0] {
		t.Errorf("← jumps back: %v", v.seeks)
	}
}

func TestTheArrowsStepAClipNotStartedYet(t *testing.T) {
	r, cs := openClipRig(t)
	r.Key(ui.KeyRight, 0)
	r.Frames(3)
	if r.shown() != "m2" || len(cs.opened) != 0 {
		t.Errorf("an unstarted clip steps with the arrows: %q", r.shown())
	}
}

func TestTheSeekBarSeeksWhereItIsClicked(t *testing.T) {
	r, cs := openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(4)
	v := cs.last()
	r.Click(ui.RoleSlider, "Seek")
	r.Frames(2)
	if len(v.seeks) != 1 || v.seeks[0] < 13*time.Second || v.seeks[0] > 17*time.Second {
		t.Errorf("a click in the middle seeks to about half: %v", v.seeks)
	}
}

func TestSoundAndSpeedStayFromClipToClip(t *testing.T) {
	r, cs := openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	r.Key('M', 0)
	r.Key('S', 0)
	r.Frames(2)
	r.Click(ui.RoleButton, "Video 2")
	r.Frames(3)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v := cs.last()
	if len(cs.opened) != 2 || !v.muted || v.rate != 1.5 {
		t.Errorf("a new clip keeps the sound and the speed: muted %v, rate %v", v.muted, v.rate)
	}
}

// The end of the clip arrives as the framework's media event; the disc comes
// back and Play starts again.
func TestTheEndOfTheClipOffersPlayAgain(t *testing.T) {
	r, cs := openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v := cs.last()
	v.pos = v.dur - 80*time.Millisecond
	r.Frames(4)
	if !v.ended || v.playing {
		t.Fatalf("it ended: %+v", v)
	}
	ev := &app.Event{Kind: app.EvService, Source: platform.SourceMedia, A: int64(media.Ended), B: int64(v.id)}
	if !r.st.AppEvent(ev) {
		t.Error("the viewer takes the end of its clip")
	}
	if r.st.AppEvent(&app.Event{Kind: app.EvService, Source: platform.SourceMedia, A: int64(media.Ended), B: int64(v.id + 7)}) {
		t.Error("the end of another video is not its own")
	}
	r.Frames(2)
	r.Must(ui.RoleButton, "Play or pause the video")
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	if !v.playing || v.ended || len(cs.opened) != 1 {
		t.Errorf("Play plays it again from the start: %+v", v)
	}
}

// Stepping to another item, closing, and the window's end close the video.
func TestTheClipClosesWhenTheViewerMovesOrCloses(t *testing.T) {
	r, cs := openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v := cs.last()
	r.Click(ui.RoleButton, "Video 2")
	r.Frames(3)
	if !v.closed || v.playing {
		t.Error("stepping closes the clip")
	}
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v = cs.last()
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if !v.closed || !r.sc.Closing() {
		t.Error("Escape closes the viewer and the clip")
	}

	r, cs = openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v = cs.last()
	r.Click(ui.RoleButton, "Back to the message (Esc)")
	r.Frames(2)
	if !v.closed || !r.sc.Closing() {
		t.Error("Back to the message closes the viewer and the clip")
	}

	r, cs = openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v = cs.last()
	r.sc.Closed(r.sc.e)
	if !v.closed {
		t.Error("the end of the window closes the clip")
	}
}

func TestAClipThatCannotPlayGoesToTheSystemPlayer(t *testing.T) {
	cs := fakeClips(t)
	cs.fail = media.ErrVideoUnsupported
	p := clipFile(t)
	r := open(t, album{mediaItem("m1", p, true)}, "m1")
	r.wait(ui.RoleButton, "Play or pause the video")
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(2)
	for i := 0; i < 100 && len(r.shell().opened()) == 0; i++ {
		r.Play(widgettest.Pause(5))
	}
	if got := r.shell().opened(); len(got) != 1 || got[0] != p {
		t.Errorf("the system player gets the file: %v", got)
	}
}

// VW-010: a downloaded motion clip plays over its photo; the button goes back.
func TestAMotionClipPlaysOverItsPhoto(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoReady
	dir := t.TempDir()
	p := writePNG(t, dir, "p.png", 400, 300)
	mp := filepath.Join(dir, "m.mp4")
	os.WriteFile(mp, []byte("x"), 0o644)
	r := openWith(t, func(f *fake.Core) album { return album{mediaItem("m1", p, false)} }, "m1")
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		m := abi.Message{ID: []byte("m1"), Chat: []byte(chatJID), Sender: []byte(chatJID), Timestamp: 5}
		m.Content.Tag = abi.ContentTagImage
		m.Content.Image.HasMotion = true
		m.Content.Image.Motion.Path, m.Content.Image.Motion.HasPath = []byte(mp), true
		m.Content.Image.Media.Path, m.Content.Image.Media.HasPath = []byte(p), true
		e.Messages = abi.EventMessages{Chat: []byte(chatJID), Batch: 5, Last: true, Messages: []abi.Message{m}}
	}))
	r.Frames(3)
	r.photo()
	r.Click(ui.RoleButton, "Play motion photo")
	r.Frames(4)
	v := cs.last()
	if v == nil || v.path != mp || !v.playing || len(r.sent(abi.CommandTagDownloadMotion)) != 0 {
		t.Fatalf("the downloaded clip plays: %+v", v)
	}
	r.Must(ui.RoleImage, "Video")
	r.Must(ui.RoleButton, "Pause")
	if r.has(ui.RoleImage, "Photo") || r.has(ui.RoleButton, "Zoom in") {
		t.Error("the clip covers the photo")
	}
	r.Click(ui.RoleButton, "Show the photo")
	r.Frames(3)
	if !v.closed {
		t.Error("the photo button closes the clip")
	}
	r.photo()
	r.Must(ui.RoleButton, "Play motion photo")
}

// VW-009: a double click on the clip in its bubble opens the viewer playing
// (State.OpenViewerPlaying): the clip plays as soon as it is here, once.
func TestAClipOpenedPlayingStartsAtOnce(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoReady
	p := clipFile(t)
	r := openPre(t, nil, func(st *state.State) { st.Win.ViewerPlay = true },
		func(*fake.Core) album { return album{mediaItem("m1", p, true), mediaItem("m2", p, true)} }, "m1")
	r.wait(ui.RoleButton, "Pause")
	v := cs.last()
	if v == nil || !v.playing || len(cs.opened) != 1 || filepath.Base(v.path) != "c.mp4" {
		t.Fatalf("the clip plays without a click: %+v", v)
	}
	if r.has(ui.RoleButton, "Play or pause the video") {
		t.Error("no play disc while it plays")
	}
	if r.st.Win.ViewerPlay {
		t.Error("the request is read once")
	}
	// Another item does not play by itself.
	r.Click(ui.RoleButton, "Video 2")
	r.Frames(3)
	if !v.closed || len(cs.opened) != 1 {
		t.Errorf("the next clip waits for its own click: %d opened", len(cs.opened))
	}
	r.Must(ui.RoleButton, "Play or pause the video")
}

// A plain open shows the poster with the disc, and a request for the item
// already on screen plays it.
func TestAPlainOpenWaitsAndANewRequestPlays(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoReady
	p := clipFile(t)
	r := open(t, album{mediaItem("m1", p, true)}, "m1")
	r.wait(ui.RoleButton, "Play or pause the video")
	if len(cs.opened) != 0 {
		t.Fatal("a plain open does not play")
	}
	r.st.OpenViewerPlaying(r.st.Win.ViewerChat, []byte("m1"))
	r.Frames(4)
	if v := cs.last(); v == nil || !v.playing {
		t.Error("the viewer asked to play the clip on screen plays it")
	}
}

// Asked to play a photo, the viewer does not keep the request for the next item.
func TestAPlayRequestOnAPhotoIsDropped(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoReady
	dir := t.TempDir()
	r := openPre(t, nil, func(st *state.State) { st.Win.ViewerPlay = true },
		func(*fake.Core) album {
			return album{mediaItem("m1", writePNG(t, dir, "a.png", 300, 200), false), mediaItem("m2", clipFile(t), true)}
		}, "m1")
	r.photo()
	r.Click(ui.RoleButton, "Video 2")
	r.Frames(3)
	r.Must(ui.RoleButton, "Play or pause the video")
	if len(cs.opened) != 0 {
		t.Error("the request died with the photo")
	}
}

// Opened playing from where the bubble's clip was (OpenViewerPlayingAt): the
// clip waits until it is ready, seeks there and plays.
func TestAClipResumesFromTheBubblesPosition(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoLoading
	p := clipFile(t)
	r := openPre(t, nil, func(st *state.State) { st.Win.ViewerPlay, st.Win.ViewerAt = true, 12*time.Second },
		func(*fake.Core) album { return album{mediaItem("m1", p, true)} }, "m1")
	r.Frames(4)
	v := cs.last()
	if v == nil || v.playing || len(v.seeks) != 0 {
		t.Fatalf("it waits for the clip to load: %+v", v)
	}
	if r.st.Win.ViewerAt != 0 {
		t.Error("the position is read once")
	}
	v.state = media.VideoReady
	r.Frames(4)
	if !v.playing || len(v.seeks) != 1 || v.seeks[0] != 12*time.Second {
		t.Errorf("it seeks to the bubble's position, then plays: playing %v, seeks %v", v.playing, v.seeks)
	}
}

func TestAPositionPastTheEndStartsFromTheStart(t *testing.T) {
	for _, at := range []time.Duration{45 * time.Second, 30 * time.Second, -3 * time.Second} {
		cs := fakeClips(t)
		cs.ready = media.VideoReady
		p := clipFile(t)
		r := openPre(t, nil, func(st *state.State) { st.Win.ViewerPlay, st.Win.ViewerAt = true, at },
			func(*fake.Core) album { return album{mediaItem("m1", p, true)} }, "m1")
		r.Frames(4)
		v := cs.last()
		if v == nil || !v.playing || len(v.seeks) != 0 || v.pos > time.Second {
			t.Errorf("at %v: plays from the start: %+v", at, v)
		}
	}
}

// A hand on the controls while the clip loads cancels the resume: it is not
// seeked and started over what the person did.
func TestAPauseBeforeTheResumeIsKept(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoLoading
	p := clipFile(t)
	r := openPre(t, nil, func(st *state.State) { st.Win.ViewerPlay, st.Win.ViewerAt = true, 5*time.Second },
		func(*fake.Core) album { return album{mediaItem("m1", p, true)} }, "m1")
	r.Frames(3)
	v := cs.last()
	r.Key(ui.KeySpace, 0) // plays by hand while it loads
	r.Frames(2)
	v.state = media.VideoReady
	r.Frames(4)
	if len(v.seeks) != 0 {
		t.Errorf("a hand on the controls cancels the resume: %v", v.seeks)
	}
}

// The speed and the sound are the state's, shared with the bubble's clips: what
// the bubble set plays here, and what is switched here is there for the next.
func TestTheSpeedAndSoundAreSharedWithTheBubble(t *testing.T) {
	cs := fakeClips(t)
	cs.ready = media.VideoReady
	p := clipFile(t)
	r := openPre(t, nil, func(st *state.State) { st.Video.Muted, st.Video.Rate = true, 2 },
		func(*fake.Core) album { return album{mediaItem("m1", p, true), mediaItem("m2", p, true)} }, "m1")
	r.wait(ui.RoleButton, "Play or pause the video")
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(3)
	v := cs.last()
	if !v.muted || v.rate != 2 || !r.has(ui.RoleButton, "Unmute") || !r.has(ui.RoleButton, "2x") {
		t.Fatalf("the bubble's settings play here: muted %v, rate %v", v.muted, v.rate)
	}
	// Switched here: the state, for the bubble's next clip.
	r.Key('M', 0)
	r.Key('S', 0)
	r.Frames(3)
	if r.st.Video.Muted || r.st.Video.Speed() != 1 || v.muted || v.rate != 1 {
		t.Errorf("a switch here reaches the state and the clip: %+v, clip %v %v", r.st.Video, v.muted, v.rate)
	}
	// Switched elsewhere (the bubble) while this clip is up: it follows.
	r.st.Video.Muted, r.st.Video.Rate = true, 1.5
	r.Frames(3)
	if !v.muted || v.rate != 1.5 || !r.has(ui.RoleButton, "Unmute") {
		t.Errorf("a switch elsewhere reaches the clip: muted %v, rate %v", v.muted, v.rate)
	}
}
