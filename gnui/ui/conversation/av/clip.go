package av

import (
	"math"
	"strings"
	"sync"
	"time"

	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Inline videos and round video notes (zf:src/ui/conversation.rs video,
// video_note, video_controls, attachment, video_clicked): the poster (the
// message's inline thumbnail) or the frame playing, the badge, the length,
// the controls and the progress ring, and what a click does. Playing is
// video.go's.

// Geometry, in points.
const (
	pictureWidth  = 320.0 // PICTURE_WIDTH
	pictureHeight = 440.0 // PICTURE_HEIGHT
	noteSide      = 220.0 // NOTE_SIDE
	discSide      = 48.0
	cardHeight    = 52.0 + 16.0 // the attachment row and its margins
)

// Black at upstream's alphas, over a poster: the light scrim and the disc.
const (
	scrimLight = uint32(0x00000028) // 40/255
	scrimDisc  = uint32(0x0000008C) // 140/255
	white      = uint32(0xFFFFFFFF)
)

func (b *blk) videoSize(maxW float32) (w, h float32) {
	if b.md.VideoNote {
		s := min(b.pt(noteSide), maxW)
		return s, s
	}
	if b.fileCard() {
		return min(b.pt(cardWidth), maxW), b.pt(cardHeight)
	}
	// Without its size, a widescreen frame that fills the bubble. Playing,
	// the bubble takes the frames' own shape (the message's size can be
	// another's: the poster crops to it until then).
	vw, vh := float32(b.md.Width), float32(b.md.Height)
	if b.md.Width == 0 || b.md.Height == 0 {
		vw, vh = 1280, 720
	}
	limit := min(maxW, b.pt(pictureWidth))
	maxH := min(b.pt(pictureHeight), limit*1.3)
	w, h = b.fitPicture(b.pt(vw), b.pt(vh), limit, maxH)
	if vc.is(b.a, b.m.ID()) && vc.fw > 0 && vc.fh > 0 && !b.md.Gif {
		// the frames' shape at the bubble's width (never their pixels: a
		// small clip keeps the bubble's size)
		fw, fh := float32(vc.fw), float32(vc.fh)
		h = theme.Snap(w * fh / fw)
		if h > maxH {
			h = maxH
			w = theme.Snap(h * fw / fh)
		}
		h = max(h, b.pt(90))
	}
	return w, h
}

// fileCard reports a video drawn as a file row: no poster and no file to
// take a frame from (one sent before ZapFast made thumbnails).
func (b *blk) fileCard() bool {
	_, thumb := b.m.Thumbnail()
	return !thumb && len(b.md.Path) == 0
}

// fitPicture is upstream's fit_picture in pixels: within the bounds, never
// enlarged, at least 120 points wide and 90 high.
func (b *blk) fitPicture(w, h, maxW, maxH float32) (float32, float32) {
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	maxW, maxH = max(maxW, 0), max(maxH, 0)
	scale := float32(math.Min(float64(maxW/w), float64(maxH/h)))
	scale = min(max(scale, 0), 1)
	if w*scale < b.pt(120) {
		scale = max(min(b.pt(120)/w, maxW/w), 0)
	}
	return theme.Snap(max(w*scale, 0)), theme.Snap(max(max(h*scale, b.pt(90)), 0))
}

func (b *blk) videoBuild(r screen.Rect) {
	switch {
	case b.md.VideoNote:
		b.noteBuild(r)
	case b.fileCard():
		b.cardBuild(r)
	default:
		b.frameBuild(r)
	}
}

// frameBuild is video: the clip's frame while it plays in place (its
// poster before, at the clip's shape, under a light scrim with the badge in
// a dark disc), the controls while it is paused or under the pointer, and
// before it plays the GIF mark, the length and the size along the bottom. A
// downloaded GIF is its poster, looping muted under the pointer.
func (b *blk) frameBuild(r screen.Rect) {
	k, c := b.k, b.c
	gif := b.md.Gif
	hasPath := len(b.md.Path) > 0
	_, hasThumb := b.m.Thumbnail()
	name := i18n.T("Video")
	if gif {
		name = i18n.T("GIF")
	}
	radius := b.pt(ztheme.Nested())
	c.NextPos(0, 0)
	c.NextFlags(base.Floating)
	c.NextCursor(ui.CursorPointer)
	pr := k.PressState(b.lbl(name, 40), base.HoverOn)
	over := b.pointerOver(pr.Key)
	vis := b.shown(pr.Key)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(r.W), ui.Px(r.H), 0, nil)
	var (
		tex    gpu.TextureID
		tw, th int
		st     clipStatus
	)
	switch {
	case gif && hasPath:
		tex, tw, th = b.gifFrame(vis, over)
	case hasPath && vc.is(b.a, b.m.ID()):
		tex, tw, th = b.shownFrame(vis)
		st = b.clipStatus()
	}
	// A decoded GIF shows its first frame even before hover starts playback.
	// While that poster loads, keep the message's thumbnail as the fallback.
	gifPoster := tex == 0 && gif && hasPath && vis && b.filePoster(r.W, r.H, radius)
	switch {
	case gifPoster:
	case tex != 0 && gif:
		b.frame(41, tex, tw, th, 0, 0, r.W, r.H, radius, ui.FitFill)
	case tex != 0:
		// the bubble has the frames' shape by now (videoSize); fill it
		b.fillBox(42, 0, 0, r.W, r.H, radius, 0x000000FF)
		b.frame(41, tex, tw, th, 0, 0, r.W, r.H, radius, ui.FitFill)
	case hasPath && !hasThumb:
		b.fillBox(42, 0, 0, r.W, r.H, radius, 0x000000FF)
		if vis {
			b.filePoster(r.W, r.H, radius)
		}
	default:
		if hd := b.poster(r.W, r.H); hd != 0 {
			b.image(41, hd, 0, 0, r.W, r.H, radius, "")
		} else {
			b.fillBox(42, 0, 0, r.W, r.H, radius, 0x000000FF)
		}
	}
	// A downloaded GIF is a picture without the badge (upstream draws its
	// frame and nothing else); its chip stays only until a frame is ready. One
	// the system cannot play keeps the badge.
	still := gif && hasPath && !b.gifFailed()
	if !still && st.ph != playing {
		b.fillBox(43, 0, 0, r.W, r.H, radius, scrimLight)
		b.badge(r.W/2, r.H/2, gif, st.ph == loading)
	}
	switch {
	case st.loaded && (st.ph == paused || (st.ph == playing && over)):
		b.controls(r.W, r.H, st)
	case !st.loaded && !(still && (tex != 0 || gifPoster)):
		b.lengthChip(r)
	}
	k.FocusRing(pr.ID, pr.Visual, radius)
	sig := k.EndPress(&pr)
	if !hasPath {
		b.autoDownload()
	}
	if sig.Clicked {
		b.videoClicked(gif)
	}
	// The two clicks of a double click play and pause between them; then the
	// clip plays in the viewer window.
	if sig.DoubleClicked && !gif && hasPath && b.a != nil {
		b.expandVideo()
	}
}

// lengthChip is the line along the bottom of a clip not loaded: GIF · 0:12
// · 1.2 MB.
func (b *blk) lengthChip(r screen.Rect) {
	f := b.c.Fmt()
	sep := false
	if b.md.Gif {
		f.Str(i18n.T("GIF"))
		sep = true
	}
	if b.md.HasSeconds {
		if sep {
			f.Str(" · ")
		}
		durationTo(f, secs(b.md.Seconds))
		sep = true
	}
	if len(b.md.Path) == 0 {
		if sep {
			f.Str(" · ")
		}
		byteSize(f, b.md.Size)
	}
	if s := f.Bytes(); len(s) > 0 {
		b.pill(44, s, b.pt(8), r.H-b.pt(14), r.W-b.pt(16), false)
	}
}

// noteBuild is video_note: the clip's frame in a circle while it plays in
// place (its poster before), the progress ring round it once loaded, a light
// scrim and the badge unless it plays (a pause sign under the pointer while
// it does), and the time, or before it plays the length, on a pill near the
// bottom.
func (b *blk) noteBuild(r screen.Rect) {
	k, c := b.k, b.c
	side := min(r.W, r.H)
	inset := b.pt(5) // room for the progress ring
	pic := side - 2*inset
	hasPath := len(b.md.Path) > 0
	c.NextPos(0, 0)
	c.NextFlags(base.Floating)
	c.NextCursor(ui.CursorPointer)
	pr := k.PressState(b.lbl(i18n.T("Video message"), 50), base.HoverOn)
	over := b.pointerOver(pr.Key)
	vis := b.shown(pr.Key)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(side), ui.Px(side), 0, nil)
	var (
		tex    gpu.TextureID
		tw, th int
		st     clipStatus
	)
	if hasPath && vc.is(b.a, b.m.ID()) {
		tex, tw, th = b.shownFrame(vis)
		st = b.clipStatus()
	}
	switch {
	case tex != 0:
		b.frame(51, tex, tw, th, inset, inset, pic, pic, pic/2, ui.FitFill) // the centred square
	default:
		if hd := b.poster(pic, pic); hd != 0 {
			b.image(51, hd, inset, inset, pic, pic, pic/2, "")
		} else {
			b.fillBox(52, inset, inset, pic, pic, pic/2, b.col(theme.Fill2))
		}
	}
	if st.loaded {
		b.noteRing(side, st.frac())
	}
	if st.ph != playing {
		b.fillBox(53, inset, inset, pic, pic, pic/2, scrimLight)
	}
	switch {
	case (hasPath && st.ph == loading) || (!hasPath && b.md.State == abi.MediaStateTagDownloading):
		b.disc(side/2, side/2, 0, 0, true, hasPath)
	case st.ph == playing:
		if over { // only a pause sign under the pointer covers a playing clip
			b.disc(side/2, side/2, icPauseBig, white, false, hasPath)
		}
	case !hasPath && b.md.State == abi.MediaStateTagFailed:
		b.disc(side/2, side/2, icAlertBig, b.col(theme.Red), false, hasPath)
	default:
		b.disc(side/2, side/2, icPlayBig, white, false, hasPath)
	}
	f := c.Fmt()
	switch {
	case st.loaded:
		durationTo(f, st.pos)
	case b.md.HasSeconds:
		durationTo(f, secs(b.md.Seconds))
	case !hasPath:
		byteSize(f, b.md.Size)
	}
	if s := f.Bytes(); len(s) > 0 {
		lh := b.lineH()
		cy := inset + pic - lh/2 - b.pt(16)
		b.pill(54, s, side/2, cy, pic, true)
	}
	k.FocusRing(pr.ID, pr.Visual, side/2)
	sig := k.EndPress(&pr)
	if !hasPath {
		b.autoDownload()
	}
	if sig.Clicked {
		b.videoClicked(false)
	}
}

// badge is the dark disc at (cx, cy) with what a click does: a spinner while
// the file downloads or the clip loads, an alert after a failure, the
// external mark of a GIF the system's player has to play, else play.
func (b *blk) badge(cx, cy float32, gif, loadingClip bool) {
	hasPath := len(b.md.Path) > 0
	switch {
	case loadingClip || (!hasPath && b.md.State == abi.MediaStateTagDownloading):
		b.disc(cx, cy, 0, 0, true, hasPath)
	case !hasPath && b.md.State == abi.MediaStateTagFailed:
		b.disc(cx, cy, icAlertBig, b.col(theme.Red), false, hasPath)
	case hasPath && gif:
		b.disc(cx, cy, icExternal, white, false, hasPath)
	default:
		b.disc(cx, cy, icPlayBig, white, false, hasPath)
	}
}

// disc is the dark disc at (cx, cy) with icon ic in col, or a spinner (for
// the clip loading once its file is here, else for the download).
func (b *blk) disc(cx, cy float32, ic icon.ID, col uint32, spin, hasPath bool) {
	d := b.pt(discSide)
	x, y := theme.Snap(cx-d/2), theme.Snap(cy-d/2)
	b.fillBox(60, x, y, d, d, d/2, scrimDisc)
	if !spin {
		b.icon(ic, col, x, y, d, d)
		return
	}
	name := i18n.T("Downloading")
	if hasPath {
		name = i18n.T("Loading…")
	}
	s := theme.Snap(rexui.SpinnerLook.Size[1] * b.sc)
	b.c.NextPos(x+(d-s)/2, y+(d-s)/2)
	b.c.NextFlags(base.Floating)
	rexui.Spinner(b.k, b.lbl(name, 61), rexui.SpinnerOpts{Size: theme.SizeSmall, Backdrop: rexui.Of(theme.Label)})
}

// noteRing is the progress ring round a round note loaded in place: the
// track, and the part played from twelve o'clock clockwise (upstream's
// video::arc). The part played is the whole ring seen through up to four
// clip boxes, one per quarter: a whole quarter, or the part of one before
// the arc's end, cut across the ring there.
func (b *blk) noteRing(side, frac float32) {
	c := b.c
	lw := b.pt(3)
	rc := side/2 - b.pt(2) // the ring's centre line (NOTE_SIDE / 2 - 2)
	o := side/2 - rc - lw/2
	d := side - 2*o
	ring := func(n uint64, x, y float32, col uint32) {
		theme.At(c, x, y, d, d)
		c.NextBorder(col, lw)
		c.NextRadius(d / 2)
		c.BoxKeyed(base.Floating|ui.DrawBorder, b.kn(n), nil)
	}
	ring(100, o, o, theme.WithAlpha(b.col(theme.Label2), 0.35))
	acc := b.col(theme.Accent)
	m := side / 2
	for q := 0; q < 4 && frac > float32(q)/4; q++ {
		part := min(frac-float32(q)/4, 0.25) * 4 // of this quarter, 0 to 1
		sin, cos := math.Sincos(float64(part) * math.Pi / 2)
		s, k := rc*float32(sin), rc*float32(cos)
		early := part < 0.5 // the arc's end runs more across the cut x than y
		var x0, y0, x1, y1 float32
		switch q {
		case 0: // top right, from twelve o'clock
			x0, y0, x1, y1 = m, 0, side, m
			if part < 1 && early {
				x1 = m + s
			} else if part < 1 {
				y1 = m - k
			}
		case 1: // bottom right, from three
			x0, y0, x1, y1 = m, m, side, side
			if part < 1 && early {
				y1 = m + s
			} else if part < 1 {
				x0 = m + k
			}
		case 2: // bottom left, from six
			x0, y0, x1, y1 = 0, m, m, side
			if part < 1 && early {
				x0 = m - s
			} else if part < 1 {
				y0 = m + k
			}
		default: // top left, from nine
			x0, y0, x1, y1 = 0, 0, m, m
			if part < 1 && early {
				y0 = m - s
			} else if part < 1 {
				x1 = m - k
			}
		}
		if x1-x0 < 0.5 || y1-y0 < 0.5 {
			break
		}
		theme.At(c, x0, y0, x1-x0, y1-y0)
		c.Push(c.BoxKeyed(base.Floating|ui.Clip, b.kn(101+uint64(q)), nil))
		ring(1, o-x0, o-y0, acc)
		c.Pop()
	}
}

// Video controls' colours over the clip (video_controls): the bar, the
// speed chip and the track.
const (
	scrimBar   = uint32(0x00000096) // 150/255
	chipLight  = uint32(0xFFFFFF28) // white at 40/255
	trackLight = uint32(0xFFFFFF5A) // white at 90/255
)

// controls is video_controls along the bottom of a clip loaded in its
// bubble: play or pause, the time, the seek bar, the speed chip, the sound
// switch, and "Fill the window".
func (b *blk) controls(w, h float32, st clipStatus) {
	c := b.c
	barH := b.pt(32)
	radius := b.pt(ztheme.Nested())
	theme.At(c, 0, h-barH, w, barH)
	c.NextBG(scrimBar)
	c.NextRadii(0, 0, radius, radius)
	c.Push(c.BoxKeyed(base.Floating|ui.DrawBackground, b.kn(80), nil))
	cy := barH / 2
	hit := b.pt(26)
	half := hit / 2
	name, ic := i18n.T("Play"), icPlay
	if st.ph == playing {
		name, ic = i18n.T("Pause"), icPause
	}
	toggleX := b.pt(18) - half
	if b.ctl(81, name, ic, toggleX, cy-half, hit) {
		playVideo(b.e.S, b.a, b.m.ID(), b.md.Path, b.md.VideoNote)
	}
	winX := w - b.pt(18) - half
	if b.ctl(82, i18n.T("Fill the window"), icMaximize, winX, cy-half, hit) {
		b.expandVideo()
	}
	soundX := winX - b.pt(15) - half
	name, ic = i18n.T("Mute"), icVolume
	if b.e.S.Video.Muted {
		name, ic = i18n.T("Unmute"), icMuted
	}
	if b.ctl(83, name, ic, soundX, cy-half, hit) {
		toggleVideoSound(b.e.S)
	}
	label := SpeedLabel(b.e.S.Video.Speed())
	cw, ch := theme.Snap(b.textW(base.StrBytes(label)))+1+b.pt(10), b.pt(18)
	chipX := soundX - b.pt(4) - cw
	if b.speedPill(84, label, chipX, theme.Snap(cy-ch/2), cw, ch, chipLight, white) {
		cycleVideoSpeed(b.e.S)
	}
	f := c.Fmt()
	durationTo(f, st.pos).Str(" / ")
	durationTo(f, st.total)
	t := f.Bytes()
	tx := toggleX + hit + b.pt(4)
	tw := theme.Snap(b.textW(t)) + 1
	b.text(85, t, white, tx, theme.Snap(cy-b.lineH()/2), tw)
	if x0, x1 := tx+tw+b.pt(10), chipX-b.pt(6); x1-x0 >= b.pt(12) {
		b.seekBar(86, x0, theme.Snap(cy-b.pt(8)), x1-x0, b.pt(16), st)
	}
	c.Pop()
}

// ctl is a control's button: icon ic in white on nothing, d×d at (x, y),
// named name, which its tooltip says too. It reports a click.
func (b *blk) ctl(n uint64, name string, ic icon.ID, x, y, d float32) bool {
	k, c := b.k, b.c
	c.NextPos(x, y)
	c.NextFlags(base.Floating)
	c.NextCursor(ui.CursorPointer)
	pr := k.PressState(b.lbl(name, n), base.HoverOn)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(d), ui.Px(d), 0, nil)
	b.icon(ic, white, 0, 0, d, d)
	k.FocusRing(pr.ID, pr.Visual, d/2)
	sig := k.EndPress(&pr)
	rexui.Tooltip(k, pr.ID, name)
	if !sig.Clicked || b.a == nil {
		return false
	}
	c.WakeIn(1) // a frame that shows what the click did
	return true
}

// seekBar is the controls' seek bar: the track, the part played in the
// accent and the knob. A click seeks; a drag shows where it goes and seeks
// once, where it ends (as upstream's, which restarts its decoder on a seek).
func (b *blk) seekBar(n uint64, x, y, w, h float32, st clipStatus) {
	c := b.c
	theme.At(c, x, y, w, h)
	c.NextRole(ui.RoleSlider)
	c.NextSemName(i18n.T("Video"))
	c.NextSemRange(0, st.total.Seconds(), st.pos.Seconds(), 5)
	c.NextSemActions(ui.ActIncrement | ui.ActDecrement | ui.ActSetValue)
	c.NextCursor(ui.CursorPointer)
	id := c.BoxKeyed(base.Floating|ui.Clickable, b.kn(n), nil)
	sig := c.Signal(id)
	frac := st.frac()
	pointed := min(max(sig.Mouse[0]/w, 0), 1)
	switch {
	case sig.Released || sig.Clicked:
		seekVideo(pointed)
		frac = pointed
		c.WakeIn(1)
	case sig.Dragging:
		frac = pointed
	case sig.Action&ui.ActIncrement != 0:
		seekVideoBy(5 * time.Second)
	case sig.Action&ui.ActDecrement != 0:
		seekVideoBy(-5 * time.Second)
	case sig.Action&ui.ActSetValue != 0 && st.total > 0:
		seekVideo(float32(sig.ActionValue / st.total.Seconds()))
	}
	acc := b.col(theme.Accent)
	c.Push(id)
	line := b.pt(3)
	ly := theme.Snap((h - line) / 2)
	b.fillBox(0, 0, ly, w, line, line/2, trackLight)
	px := theme.Snap(frac * w)
	if px > 0 {
		b.fillBox(1, 0, ly, px, line, line/2, acc)
	}
	kr := b.pt(knobRadius)
	b.fillBox(2, theme.Snap(px-kr), theme.Snap(h/2-kr), 2*kr, 2*kr, kr, acc)
	c.Pop()
}

// speedPill is a speed chip w×h at (x, y): label in ink on fill, named
// "Playback speed {label}". It reports a click.
func (b *blk) speedPill(n uint64, label string, x, y, w, h float32, fill, ink uint32) bool {
	k, c := b.k, b.c
	c.NextPos(x, y)
	c.NextFlags(base.Floating)
	c.NextBG(fill)
	c.NextRadius(h / 2)
	c.NextCursor(ui.CursorPointer)
	pr := k.PressState(speedName(c, label, n), base.HoverOn)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(w), ui.Px(h), ui.DrawBackground, nil)
	st := &b.r.Styles[theme.StyleSubheadline]
	tw := min(theme.Snap(b.textW(base.StrBytes(label)))+1, w)
	theme.At(c, theme.Snap((w-tw)/2), theme.Snap((h-st.Line)/2), tw, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(ink)
	c.BoxKeyed(base.Floating|ui.DrawText, b.kn(1), base.StrBytes(label))
	k.FocusRing(pr.ID, pr.Visual, h/2)
	if !k.EndPress(&pr).Clicked {
		return false
	}
	c.WakeIn(1)
	return true
}

// speedName is a speed chip's press label: "Playback speed {label}###n".
func speedName(c *ui.Context, label string, n uint64) string {
	tmpl := i18n.T("Playback speed {label}")
	f := c.Fmt()
	if i := strings.Index(tmpl, "{label}"); i >= 0 {
		f.Str(tmpl[:i]).Str(label).Str(tmpl[i+len("{label}"):])
	} else {
		f.Str(tmpl)
	}
	return f.Str("###").Uint64(n).String()
}

// frame draws texture tex (tw×th texels) in the box (x, y, w, h) as image
// box n of the current parent.
func (b *blk) frame(n uint64, tex gpu.TextureID, tw, th int, x, y, w, h, radius float32, fit ui.ImageFit) {
	c := b.c
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextFlags(base.Floating)
	b.k.ExternalImage(n, tex, tw, th, fit, radius, "")
}

// pointerOver reports the pointer over the box keyed key as the last frame
// laid it out (and as much of it as showed): rect_contains_pointer, so a
// control over the clip does not take it away.
func (b *blk) pointerOver(key ui.Key) bool {
	c := b.c
	in := c.Input()
	if !in.Inside || in.Hidden {
		return false
	}
	id, ok := c.BoxOf(key)
	if !ok {
		return false
	}
	r := c.ShownRect(id)
	return in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3]
}

// pill is a line of white text on a dark pill: from x (or centred on x) at
// height y.
func (b *blk) pill(n uint64, s []byte, x, y, maxW float32, centred bool) {
	c := b.c
	st := &b.r.Styles[theme.StyleSubheadline]
	tw := min(theme.Snap(b.textW(s))+1, max(maxW-b.pt(12), 0))
	w, h := tw+b.pt(12), st.Line+b.pt(6)
	if centred {
		x -= w / 2
	} else {
		y -= h
	}
	x, y = theme.Snap(x), theme.Snap(y)
	b.fillBox(n, x, y, w, h, h/2, scrimDisc)
	theme.At(c, x+b.pt(6), y+b.pt(3), tw, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(white)
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(s)
	c.BoxKeyed(base.Floating|ui.DrawText|ui.FadeOverflowX, b.kn(n+1), s)
}

// cardBuild is the file row of a video with no poster and no file yet
// (attachment): the video sign on a disc, its name and details, and the
// download sign; a click downloads it.
func (b *blk) cardBuild(r screen.Rect) {
	k, c := b.k, b.c
	gif := b.md.Gif
	title := i18n.T("Video")
	if gif {
		title = i18n.T("GIF")
	}
	downloading := b.md.State == abi.MediaStateTagDownloading
	name := i18n.T("Download")
	if downloading {
		name = i18n.T("Downloading")
	}
	c.NextPos(0, 0)
	c.NextFlags(base.Floating)
	c.NextBG(theme.WithAlpha(b.col(theme.Window), 0.35))
	c.NextRadius(b.pt(ztheme.Nested()))
	c.NextCursor(ui.CursorPointer)
	pr := k.PressState(b.lbl(name, 70), base.HoverOn)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(r.W), ui.Px(r.H), ui.DrawBackground, nil)
	padX := b.pt(10)
	d := b.pt(36)
	y := theme.Snap((r.H - d) / 2)
	b.fillBox(71, padX, y, d, d, d/2, theme.WithAlpha(b.accent(), 0.25))
	b.icon(icVideo, b.accent(), padX, y, d, d)
	x := padX + d + b.pt(10)
	textW := max(r.W-x-padX-b.pt(34), 0)
	body := &b.r.Styles[theme.StyleBodySemibold]
	lh := b.lineH()
	ty := theme.Snap((r.H - body.Line - lh - b.pt(1)) / 2)
	theme.At(c, x, ty, textW, body.Line)
	c.NextFont(body.Font)
	c.NextTextColor(b.ink())
	c.NextRole(ui.RoleLabel)
	c.NextSemName(title)
	c.BoxKeyed(base.Floating|ui.DrawText|ui.FadeOverflowX, b.kn(72), base.StrBytes(title))
	f := c.Fmt()
	if b.md.State == abi.MediaStateTagFailed {
		tmpl := i18n.T("{error}. Click to retry.")
		if i := strings.Index(tmpl, "{error}"); i >= 0 {
			f.Str(tmpl[:i]).Str(model.S(b.failure())).Str(tmpl[i+len("{error}"):])
		}
	} else {
		if b.md.HasSeconds {
			durationTo(f, secs(b.md.Seconds)).Str(" · ")
		}
		byteSize(f, b.md.Size)
	}
	b.text(73, f.Bytes(), b.dim(), x, ty+body.Line+b.pt(1), textW)
	ax := r.W - padX - b.pt(24)
	if downloading {
		s := theme.Snap(rexui.SpinnerLook.Size[1] * b.sc)
		c.NextPos(ax+(b.pt(24)-s)/2, (r.H-s)/2)
		c.NextFlags(base.Floating)
		rexui.Spinner(k, b.lbl(i18n.T("Downloading"), 74), rexui.SpinnerOpts{Size: theme.SizeSmall})
	} else {
		b.icon(icDownload, b.dim(), ax, 0, b.pt(24), r.H)
	}
	k.FocusRing(pr.ID, pr.Visual, b.pt(8))
	sig := k.EndPress(&pr)
	b.autoDownload()
	if sig.Clicked && !downloading {
		b.download(true)
	}
}

// videoClicked is video_clicked: a downloaded clip plays or pauses in its
// bubble, a GIF opens in the system's player, and a clip still on
// WhatsApp's servers downloads and plays once it is here.
func (b *blk) videoClicked(gif bool) {
	if b.a == nil {
		return
	}
	b.c.WakeIn(1) // a frame that shows what the click did
	hasPath := len(b.md.Path) > 0
	switch {
	case hasPath && gif:
		openFile(b.e.S, string(b.md.Path))
	case hasPath:
		playVideo(b.e.S, b.a, b.m.ID(), b.md.Path, b.md.VideoNote)
	default:
		if b.md.State != abi.MediaStateTagDownloading {
			b.download(true)
		}
		if !gif {
			playWhenDownloaded(b.e.S, b.a, b.m.ID())
		}
	}
}

// chatRef is the message's chat.
func (b *blk) chatRef() model.ChatRef {
	if ref := b.a.Model.Chats.Find(b.decoded().Chat); ref != 0 {
		return ref
	}
	return b.a.Open
}

// openFile hands a file to the system (Action::OpenFile), through the
// state's one checked route: a "video" that is not one is not launched.
func openFile(s *state.State, path string) { s.OpenFile(path) }

// later holds a toast from a worker for the main thread (Frame shows it):
// the state is the main thread's.
var later toastSlot

type toastSlot struct {
	sync.Mutex
	text string
}

func (t *toastSlot) put(s string) {
	t.Lock()
	t.text = s
	t.Unlock()
}

func (t *toastSlot) take() string {
	t.Lock()
	defer t.Unlock()
	s := t.text
	t.text = ""
	return s
}
