package viewer

import (
	"time"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/media"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// A clip plays in the window (image_preview.rs video): the frame the
// framework's media.Video gives, the bubble's controls along its bottom
// (play or pause, the time, a seek bar, the speed, the sound, and back to the
// message), a click on the frame to play or pause, Space, M, S and the arrows
// (±5 s, while the clip is up). A motion photo's clip plays the same way over
// its photo, with a button to show the photo again. Leaving the item or
// closing the window stops and closes the video.

// clip is the part of media.Video the viewer uses, so a test plays a fake.
type clip interface {
	ID() uint64
	State() media.VideoState
	Size() (w, h int)
	Duration() time.Duration
	Position() time.Duration
	Play()
	Pause()
	Playing() bool
	Ended() bool
	Seek(d time.Duration)
	SetMuted(m bool)
	SetRate(r float32)
	Frame(r *gpu.Renderer, now int64) (tex gpu.TextureID, w, h int, next int64)
	Close()
}

// openClip opens the file for playing; tests replace it.
var openClip = func(path string) (clip, error) {
	v, err := media.OpenVideo(path, media.VideoOptions{})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// playback is the clip open in the window.
type playback struct {
	v      clip
	path   string
	motion bool // a motion photo's clip, over its photo
	scrub  bool // the seek bar is being dragged

	// at is where a clip opened playing resumes (the bubble's position):
	// seekWait holds the play until the clip is ready and its length known,
	// which says whether at is inside it.
	at       time.Duration
	seekWait bool
	// muted and rate are what the video was last given of the shared
	// settings (State.Video).
	muted bool
	rate  float32
}

// The speeds the chip cycles through, as the phone's (audio.rs CYCLED_SPEEDS).
var cycledSpeeds = [...]float32{1, 1.5, 2}

func speedLabel(r float32) string {
	switch r {
	case 1.5:
		return "1.5x"
	case 2:
		return "2x"
	}
	return "1x"
}

// showsClip reports whether the stage plays a clip: a video item, or a motion
// photo's clip over its photo.
func (s *Screen) showsClip() bool { return s.isVideo() || s.pb.motion }

// stopClip pauses and closes the open clip.
func (s *Screen) stopClip() {
	if v := s.pb.v; v != nil {
		v.Pause()
		v.Close()
	}
	s.pb = playback{}
}

// startClip opens the file and plays it. A file the system cannot play goes
// to the system's player, as upstream's does for what it cannot decode.
func (s *Screen) startClip(e *screen.Env, path string, motion bool) {
	s.startClipAt(e, path, motion, 0)
}

// startClipAt is startClip from at: the clip waits, paused, until it is ready,
// seeks there when at is inside it (from 0 when it is not) and plays.
func (s *Screen) startClipAt(e *screen.Env, path string, motion bool, at time.Duration) {
	s.stopClip()
	if path == "" {
		return
	}
	v, err := openClip(path)
	if err != nil {
		s.openPath(e, path)
		return
	}
	vs := &s.e.S.Video
	v.SetMuted(vs.Muted)
	if vs.Rate != 0 {
		v.SetRate(vs.Rate)
	}
	s.pb = playback{v: v, path: path, motion: motion, muted: vs.Muted, rate: vs.Speed()}
	if at > 0 {
		s.pb.at, s.pb.seekWait = at, true
	} else {
		v.Play()
	}
	s.wake()
}

// onAppEvent takes the end of the clip: the window draws again, which is
// where the video notices it (its frame call).
func (s *Screen) onAppEvent(_ *state.State, ev *app.Event) bool {
	m, ok := media.EventOf(ev)
	if !ok || m.Kind != media.Ended || s.pb.v == nil || m.ID != s.pb.v.ID() {
		return false
	}
	s.endedSeen++
	s.wake()
	return true
}

// clipPath is the file of the clip on stage.
func (s *Screen) clipPath() string {
	if s.pb.motion {
		return s.pb.path
	}
	return s.r.path
}

// videoStage draws the clip: its frame, or its poster before it plays, the
// play disc until it has, and the controls once it is open.
func (s *Screen) videoStage(e *screen.Env, r screen.Rect, sig ui.Signal) {
	c, k := e.C(), e.K
	th := k.Theme()
	v := s.pb.v
	if v != nil && v.State() == media.VideoFailed {
		path := s.pb.path
		s.stopClip()
		s.openPath(e, path)
		v = nil
	}
	if v != nil {
		s.syncClip(v)
	}
	// The frame, asked for only while its box shows.
	var tex gpu.TextureID
	var tw, thh int
	if v != nil && k.ImageShown(1) {
		var next int64
		tex, tw, thh, next = v.Frame(e.A.R, c.Input().Time)
		if next > 0 {
			c.WakeIn(next)
		}
	}
	// The clip keeps its own shape: the frame knows it, then the poster; 16:9
	// is only what a clip with neither falls back to.
	aspect := float32(16.0 / 9.0)
	var poster img.Handle
	if i := s.r.thumb; i >= 0 && i < len(s.album) && s.album[i].thumb != nil {
		it := &s.album[i]
		poster, _ = s.cache.Request(img.Key{Hash: it.thumbHash}, img.Bytes(it.thumb))
		if info, ok := s.cache.Info(poster); ok && info.W > 0 && info.H > 0 {
			aspect = float32(info.W) / float32(info.H)
		}
	}
	if v != nil {
		if vw, vh := v.Size(); vw > 0 && vh > 0 {
			aspect = float32(vw) / float32(vh)
		}
	}
	w, h := r.W, r.H
	if w/h > aspect {
		w = h * aspect
	} else {
		h = w / aspect
	}
	w, h = theme.Snap(w), theme.Snap(h)
	x, y := theme.Snap((r.W-w)/2), theme.Snap((r.H-h)/2)
	s.mediaRect = [4]float32{r.X + x, r.Y + y, r.X + x + w, r.Y + y + h}
	switch {
	case tex != 0 && tw > 0 && thh > 0:
		c.NextPos(x, y)
		k.NextSize(ui.Px(w), ui.Px(h))
		c.NextFlags(base.Floating)
		k.ExternalImage(1, tex, tw, thh, ui.FitContain, e.Pt(8), i18n.T("Video"))
	case poster != 0:
		c.NextPos(x, y)
		k.NextSize(ui.Px(w), ui.Px(h))
		c.NextFlags(base.Floating)
		k.Image(1, poster, ui.FitContain, e.Pt(8), i18n.T("Video"))
	default:
		theme.At(c, x, y, w, h)
		c.NextBG(th.Colors[theme.Fill2])
		c.NextRadius(e.Pt(8))
		c.NextRole(ui.RoleImage)
		c.NextSemName(i18n.T("Video"))
		c.Box(base.Floating|ui.DrawBackground, "poster")
	}
	toggle := false
	switch {
	case v == nil || v.Ended():
		disc := e.Pt(64)
		theme.At(c, x+(w-disc)/2, y+(h-disc)/2, disc, disc)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextBG(0x0000008C) // a scrim: the symbol stays light in either theme
		c.NextRadius(disc / 2)
		p := k.PressState(i18n.T("Play or pause the video"), base.HoverOn)
		k.BeginPress(&p, ui.RoleButton, ui.Px(disc), ui.Px(disc), base.Floating|ui.DrawBackground, nil)
		k.Icon(s.ic.bigPlay, 0xFFFFFFFF)
		toggle = k.EndPress(&p).Clicked
	case tex == 0:
		side := e.Pt(48)
		place(e, x+(w-side)/2, y+(h-side)/2, side, side, "loading")
		rexui.Spinner(k.Kit, i18n.T("Loading"), rexui.SpinnerOpts{})
		c.Pop()
	}
	if v != nil {
		s.controls(e, v, r.X+x, r.Y+y, x, y, w, h)
	}
	in := c.Input()
	if toggle || (sig.Clicked && !s.drag.moved && s.inMedia(in.MouseX, in.MouseY)) {
		s.playVideo(e)
	}
	// A double click puts the clip back in its message.
	if sig.DoubleClicked && v != nil && s.inMedia(in.MouseX, in.MouseY) {
		s.close(e)
	}
}

// syncClip gives the video what the shared settings say (a speed or mute
// switched in the bubble carries to this clip, and the other way: the
// controls only change State.Video), and starts a resumed clip once it is
// ready.
func (s *Screen) syncClip(v clip) {
	vs := &s.e.S.Video
	if vs.Muted != s.pb.muted {
		s.pb.muted = vs.Muted
		v.SetMuted(vs.Muted)
	}
	if r := vs.Speed(); r != s.pb.rate {
		s.pb.rate = r
		v.SetRate(r)
	}
	if s.pb.seekWait && v.State() == media.VideoReady {
		s.pb.seekWait = false
		if dur := v.Duration(); s.pb.at > 0 && dur > 0 && s.pb.at < dur {
			v.Seek(s.pb.at)
		}
		v.Play()
	}
}

// controls is the bar along the bottom of the frame: play or pause, the time,
// the seek bar, the speed, the sound, and back to the message
// (conversation.rs video_controls). wx, wy is the frame's corner in the
// window, x, y in the stage.
func (s *Screen) controls(e *screen.Env, v clip, wx, wy, x, y, w, h float32) {
	c, k := e.C(), e.K
	th := k.Theme()
	bh := e.Pt(32)
	by := y + h - bh
	theme.At(c, x, by, w, bh)
	c.NextBG(0x00000096)
	c.NextRadius(e.Pt(6))
	c.Push(c.Box(base.Floating|ui.DrawBackground, "controls"))
	defer c.Pop()
	side := e.Pt(26)
	cy := (bh - side) / 2
	btn := func(label string, ic icon.ID, bx float32) bool {
		theme.At(c, bx, cy, side, side)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		p := k.PressState(label, base.HoverOn)
		k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), base.Floating, nil)
		k.Icon(ic, 0xFFFFFFFF)
		return k.EndPress(&p).Clicked
	}
	pad := e.Pt(5)
	playing := v.Playing()
	pl, plName := s.ic.playSm, i18n.T("Play")
	if playing {
		pl, plName = s.ic.pauseSm, i18n.T("Pause")
	}
	if btn(plName, pl, pad) {
		s.playVideo(e)
	}
	back := w - pad - side
	if btn(i18n.T("Back to the message (Esc)"), s.ic.exitFull, back) {
		s.close(e)
		return
	}
	sound := back - e.Pt(2) - side
	vo, voName := s.ic.vol, i18n.T("Mute")
	if s.e.S.Video.Muted {
		vo, voName = s.ic.volOff, i18n.T("Unmute")
	}
	if btn(voName, vo, sound) {
		s.e.S.Video.Muted = !s.e.S.Video.Muted
	}
	// The speed chip cycles 1x, 1.5x, 2x.
	chipW := e.Pt(38)
	chipX := sound - e.Pt(4) - chipW
	theme.At(c, chipX, (bh-e.Pt(18))/2, chipW, e.Pt(18))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextBG(0xFFFFFF28)
	c.NextRadius(e.Pt(9))
	rate := s.e.S.Video.Speed()
	p := k.PressState(speedLabel(rate), base.HoverOn)
	k.BeginPress(&p, ui.RoleButton, ui.Px(chipW), ui.Px(e.Pt(18)), base.Floating|ui.DrawBackground, nil)
	rexui.Label(k.Kit, speedLabel(rate), rexui.LabelOpts{Color: rexui.OfRGBA(0xFFFFFFFF)})
	if k.EndPress(&p).Clicked {
		s.cycleSpeed(v)
	}
	// The time, "0:07 / 0:30", then the seek bar in what is left.
	pos, dur := v.Position(), v.Duration()
	if s.pb.scrub {
		pos = time.Duration(s.scrubTo * float64(dur))
	}
	timeW := e.Pt(78)
	timeX := pad + side + e.Pt(4)
	theme.At(c, timeX, 0, timeW, bh)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(base.Floating, "time"))
	f := c.Fmt()
	appendClock(f, pos)
	f.Str(" / ")
	appendClock(f, dur)
	rexui.LabelBytes(k.Kit, f.Bytes(), rexui.LabelOpts{Color: rexui.OfRGBA(0xFFFFFFFF)})
	c.Pop()
	tx := timeX + timeW + e.Pt(6)
	tw := chipX - e.Pt(6) - tx
	if tw < e.Pt(12) || dur <= 0 {
		return
	}
	th2 := e.Pt(16)
	theme.At(c, tx, (bh-th2)/2, tw, th2)
	c.NextRole(ui.RoleSlider)
	c.NextSemName(i18n.T("Seek"))
	id := c.Box(base.Floating|ui.Clickable|ui.Hoverable, "seek")
	c.Push(id)
	sg := c.Signal(id)
	in := c.Input()
	pointed := float64((in.MouseX - (wx + tx)) / tw)
	pointed = min(max(pointed, 0), 1)
	switch {
	case sg.Pressed:
		s.pb.scrub, s.scrubTo = true, pointed
	case sg.Dragging && s.pb.scrub:
		s.scrubTo = pointed
	}
	if sg.Released && s.pb.scrub {
		// Seeking restarts the decoder: a drag seeks once, where it ends.
		s.pb.scrub = false
		v.Seek(time.Duration(pointed * float64(dur)))
		c.RequestFrame()
	}
	frac := float32(pos) / float32(dur)
	frac = min(max(frac, 0), 1)
	lh := e.Pt(3)
	ly := (th2 - lh) / 2
	theme.At(c, 0, ly, tw, lh)
	c.NextBG(0xFFFFFF5A)
	c.NextRadius(lh / 2)
	c.Box(base.Floating|ui.DrawBackground, "line")
	theme.At(c, 0, ly, tw*frac, lh)
	c.NextBG(th.Colors[theme.Accent])
	c.NextRadius(lh / 2)
	c.Box(base.Floating|ui.DrawBackground, "played")
	knob := e.Pt(10)
	theme.At(c, tw*frac-knob/2, (th2-knob)/2, knob, knob)
	c.NextBG(th.Colors[theme.Accent])
	c.NextRadius(knob / 2)
	c.Box(base.Floating|ui.DrawBackground, "knob")
	c.Pop()
}

// appendClock writes d as minutes and seconds ("0:07", "12:05").
func appendClock(f *ui.Fmt, d time.Duration) {
	sec := int(d / time.Second)
	f.Int(sec / 60).Str(":")
	if sec%60 < 10 {
		f.Str("0")
	}
	f.Int(sec % 60)
}

// cycleSpeed takes the next speed of the chip.
func (s *Screen) cycleSpeed(v clip) {
	cur := s.e.S.Video.Speed()
	next := cycledSpeeds[0]
	for _, r := range cycledSpeeds {
		if r > cur {
			next = r
			break
		}
	}
	s.e.S.Video.Rate = next
}

// playVideo plays or pauses the clip on stage, opening it the first time
// (VW-009).
func (s *Screen) playVideo(e *screen.Env) {
	if v := s.pb.v; v != nil {
		s.pb.seekWait = false
		if v.Playing() {
			v.Pause()
		} else {
			v.Play()
		}
		s.e.C().RequestFrame()
		return
	}
	s.startClip(e, s.clipPath(), false)
}

// autoPlay starts the clip the viewer was opened on playing (OpenViewerPlaying),
// once the item is known: a clip not here yet waits for its download.
func (s *Screen) autoPlay(e *screen.Env) {
	if !s.wantPlay || !s.r.have {
		return
	}
	switch {
	case !s.isVideo():
		s.wantPlay = false
	case s.r.path != "":
		s.wantPlay = false
		if v := s.pb.v; v != nil {
			if !v.Playing() {
				v.Play()
			}
			return
		}
		s.startClipAt(e, s.r.path, false, s.wantAt)
	case !s.dlOf().downloading:
		s.wantPlay = false
	}
}

// playMotion plays a motion photo's clip over its photo (VW-010).
func (s *Screen) playMotion(e *screen.Env, path string) {
	s.startClip(e, path, true)
}

// videoKeys takes the clip's keys: Space plays or pauses, M mutes, S changes
// the speed, and the arrows jump five seconds while the clip is up (they step
// through the album otherwise: keys takes them).
func (s *Screen) videoKeys(e *screen.Env) {
	c := e.C()
	if !s.showsClip() || s.clipPath() == "" {
		return
	}
	if c.TakeKey(ui.KeySpace, 0) {
		s.playVideo(e)
	}
	v := s.pb.v
	if v == nil {
		return
	}
	switch {
	case c.TakeKey('M', 0):
		s.e.S.Video.Muted = !s.e.S.Video.Muted
	case c.TakeKey('S', 0):
		s.cycleSpeed(v)
	}
}

// jump moves the clip by d, within its length; false when no clip is up.
func (s *Screen) jump(d time.Duration) bool {
	v := s.pb.v
	if v == nil {
		return false
	}
	to := v.Position() + d
	if dur := v.Duration(); dur > 0 {
		to = min(to, dur)
	}
	v.Seek(max(to, 0))
	s.e.C().RequestFrame()
	return true
}

// ---- motion photos ----

// motionState is a photo's motion clip, from its message.
type motionState struct {
	has         bool
	path        string
	err         string
	downloading bool
}

// read takes the clip of message i of conv, when it is a picture with one.
func (m *motionState) read(conv *model.Conversation, i int, msg *abi.Message) {
	*m = motionState{}
	if conv.At(i).Kind() != abi.ContentTagImage || conv.Decode(i, msg) != nil {
		return
	}
	img := &msg.Content.Image
	if !img.HasMotion {
		return
	}
	m.has = true
	if img.Motion.HasPath {
		m.path = string(img.Motion.Path)
	}
	switch img.Motion.State.Tag {
	case abi.MediaStateTagDownloading:
		m.downloading = true
	case abi.MediaStateTagFailed:
		m.err = string(img.Motion.State.Failed.Error)
	}
}

// onMotion takes the result of the clip's download.
func (s *Screen) onMotion(res *abi.PathResult) {
	m := &s.motion
	m.downloading = false
	if res.Tag == abi.PathResultTagOk {
		m.path, m.err = string(res.Ok.Path), ""
	} else {
		m.err = string(res.Err.Error)
	}
	s.wake()
}

// motionItem is the toolbar's play button for a motion photo: it fetches the
// clip when it is not here (a failure says why and a click tries again) and
// plays it.
func (s *Screen) motionItem(e *screen.Env, tb *rexui.Toolbar) {
	m := &s.motion
	if !m.has || s.isVideo() {
		return
	}
	if s.pb.motion {
		if tb.Item(s.ic.photo, i18n.T("Show the photo")).Clicked {
			s.stopClip()
		}
		return
	}
	if m.downloading {
		tb.Item(s.ic.download, i18n.T("Downloading"))
		return
	}
	ic, name := s.ic.play, i18n.T("Play motion photo")
	if m.err != "" && m.path == "" {
		ic, name = s.ic.failed, m.err
	}
	if !tb.Item(ic, name).Clicked {
		return
	}
	a := s.account()
	if m.path == "" {
		if a == nil {
			return
		}
		m.downloading, m.err = true, ""
		cmd := a.Cmd(abi.CommandTagDownloadMotion)
		cmd.DownloadMotion.Chat, cmd.DownloadMotion.Message = s.chatID, s.cur.id
		a.Send(cmd)
		return
	}
	s.playMotion(e, m.path)
}
