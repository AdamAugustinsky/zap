package av

import (
	"strings"
	"time"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Voice notes and audio files (zf:src/ui/conversation.rs voice_player,
// speed_pill): play and pause, the waveform (a track for an audio file) with
// the progress over it, click or drag to seek, the time, and the speed chip.

// Geometry, in points (voice_player's).
const (
	cardWidth  = 320.0 // CARD_WIDTH: an audio message's bubble
	voiceBtn   = 36.0
	voiceBarH  = 30.0
	voiceGap   = 8.0
	chipW      = 44.0
	chipH      = 20.0
	barPitch   = 3.0
	barW       = 2.0
	knobRadius = 5.0
	flatBar    = 12 // a waveform not known yet: flat bars
)

func (b *blk) voiceSize(maxW float32) (w, h float32) {
	return min(b.pt(cardWidth), maxW), max(b.pt(voiceBtn), b.pt(voiceBarH)+b.pt(2)+b.lineH())
}

// voiceBuild is voice_player: the button, the waveform or track over the
// time, the music badge of an audio file, and the speed chip once the file
// is here.
func (b *blk) voiceBuild(r screen.Rect) {
	voice := b.md.Voice
	hasPath := len(b.md.Path) > 0
	ph, pos, total := status(b.a, b.m.ID())
	if total == 0 && b.md.HasSeconds {
		total = time.Duration(b.md.Seconds) * time.Second
	}
	btn, gap := b.pt(voiceBtn), b.pt(voiceGap)
	waveW := r.W - btn - gap
	if hasPath {
		waveW -= b.pt(chipW) + gap
	}
	if !voice {
		waveW -= btn + gap
	}
	waveW = max(waveW, 0)
	barH := b.pt(voiceBarH)
	colY := theme.Snap((r.H - (barH + b.pt(2) + b.lineH())) / 2)
	mid := colY + barH/2

	b.button(0, theme.Snap((r.H-btn)/2), btn, ph, hasPath, voice)
	x := btn + gap
	b.wave(x, colY, waveW, barH, ph, pos, total, voice, hasPath)
	b.duration(x, colY+barH+b.pt(2), waveW, ph, pos, total)
	x += waveW + gap
	if !voice {
		b.music(x, mid-btn/2, btn)
		x += btn + gap
	}
	if hasPath {
		b.chip(x, theme.Snap(mid-b.pt(chipH)/2), b.pt(chipW), b.pt(chipH))
	} else {
		b.autoDownload()
	}
	if ph == playing {
		b.c.WakeIn(int64(frameStep)) // the progress moves
	}
}

// button is the round button before the waveform: download, a spinner while
// the file downloads or decodes, play or pause.
func (b *blk) button(x, y, d float32, ph phase, hasPath, voice bool) {
	k, c := b.k, b.c
	fill := theme.WithAlpha(b.accent(), 0.22)
	if (!hasPath && b.md.State == abi.MediaStateTagDownloading) || (hasPath && ph == loading) {
		b.fillBox(10, x, y, d, d, d/2, fill)
		name := i18n.T("Loading…")
		if !hasPath {
			name = i18n.T("Downloading")
		}
		s := theme.Snap(rexui.SpinnerLook.Size[1] * b.sc)
		c.NextPos(x+(d-s)/2, y+(d-s)/2)
		c.NextFlags(base.Floating)
		rexui.Spinner(k, b.lbl(name, 11), rexui.SpinnerOpts{Size: theme.SizeSmall})
		return
	}
	name, ic := i18n.T("Download"), icDownload
	switch {
	case hasPath && ph == playing:
		name, ic = i18n.T("Pause"), icPause
	case hasPath:
		name, ic = i18n.T("Play"), icPlay
	}
	c.NextPos(x, y)
	c.NextFlags(base.Floating)
	pr := k.PressState(b.lbl(name, 12), base.HoverOn)
	if pr.Hot > 0 || pr.Down > 0 {
		fill = theme.WithAlpha(b.accent(), 0.38)
	}
	c.NextBG(fill)
	c.NextRadius(d / 2)
	c.NextCursor(ui.CursorPointer)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(d), ui.Px(d), ui.DrawBackground, nil)
	b.icon(ic, b.accent(), 0, 0, d, d)
	k.FocusRing(pr.ID, pr.Visual, d/2)
	if !k.EndPress(&pr).Clicked || b.a == nil {
		return
	}
	if hasPath {
		toggle(b.e, b.a, b.m.ID(), b.md.Path, voice)
	} else {
		b.download(true)
	}
}

// drag is the waveform being dragged: where it last sought.
var drag struct {
	id   mkey.K
	frac float32
}

// wave is the waveform of a voice note (the track of an audio file) with
// the part played in the accent and the knob; a click or a drag seeks.
func (b *blk) wave(x, y, w, h float32, ph phase, pos, total time.Duration, voice, hasPath bool) {
	c := b.c
	frac := float32(0)
	if total > 0 {
		frac = min(float32(float64(pos)/float64(total)), 1)
	}
	played := frac * w
	name := i18n.T("Voice message")
	if !voice {
		name = i18n.T("Audio")
	}
	theme.At(c, x, y, w, h)
	c.NextRole(ui.RoleSlider)
	c.NextSemName(name)
	c.NextSemRange(0, total.Seconds(), pos.Seconds(), 5)
	flags := ui.Flags(base.Floating)
	if hasPath {
		flags |= ui.Clickable
		c.NextCursor(ui.CursorPointer)
		c.NextSemActions(ui.ActIncrement | ui.ActDecrement | ui.ActSetValue)
	}
	id := c.BoxKeyed(flags, b.kn(20), nil)
	c.Push(id)
	acc, quiet := b.accent(), b.quiet()
	if voice {
		wave := b.waveform()
		pitch, bw := b.pt(barPitch), b.pt(barW)
		count := int(w / pitch)
		inner := h - b.pt(4)
		for i := 0; i < count; i++ {
			level := min(float32(wave[i*len(wave)/count])/100, 1)
			bh := theme.Snap(max(b.pt(2)+level*inner, b.pt(2)))
			bx := float32(i) * pitch
			col := quiet
			if ph != idle && ph != loading && bx+bw/2 <= played {
				col = acc
			}
			b.fillBox(uint64(i), bx, theme.Snap((h-bh)/2), bw, bh, bw/2, col)
		}
	} else {
		th := b.pt(4)
		b.fillBox(200, 0, theme.Snap((h-th)/2), w, th, th/2, quiet)
		if frac > 0 {
			b.fillBox(201, 0, theme.Snap((h-th)/2), theme.Snap(played), th, th/2, acc)
		}
	}
	kr := b.pt(knobRadius)
	if (!voice || ph == playing || ph == paused) && w >= 2*kr {
		kx := min(max(played, kr), w-kr)
		b.fillBox(202, theme.Snap(kx-kr), theme.Snap(h/2-kr), 2*kr, 2*kr, kr, acc)
	}
	c.Pop()
	if !hasPath || b.a == nil {
		return
	}
	sig := c.Signal(id)
	key := mkey.Of(b.a, b.m.ID())
	if (sig.Pressed || sig.Dragging) && w > 0 {
		f := min(max(sig.Mouse[0]/w, 0), 1)
		if sig.Pressed || drag.id != key || abs(f-drag.frac) > 0.002 {
			drag.id, drag.frac = key, f
			seek(b.e, b.a, b.m.ID(), b.md.Path, voice, f)
		}
	}
	switch {
	case sig.Action&ui.ActIncrement != 0:
		seekBy(b.m.ID(), b.a, 5*time.Second)
	case sig.Action&ui.ActDecrement != 0:
		seekBy(b.m.ID(), b.a, -5*time.Second)
	case sig.Action&ui.ActSetValue != 0 && total > 0:
		seek(b.e, b.a, b.m.ID(), b.md.Path, voice, float32(sig.ActionValue/total.Seconds()))
	}
}

// duration is the line under the waveform: the position while it plays or
// is paused, else the length (or the file's size), or why the download
// failed.
func (b *blk) duration(x, y, w float32, ph phase, pos, total time.Duration) {
	f := b.c.Fmt()
	switch {
	case len(b.md.Path) == 0 && b.md.State == abi.MediaStateTagFailed:
		tmpl := i18n.T("{error}. Click to retry.")
		if i := strings.Index(tmpl, "{error}"); i >= 0 {
			f.Str(tmpl[:i]).Str(model.S(b.failure())).Str(tmpl[i+len("{error}"):])
		} else {
			f.Str(tmpl)
		}
	case ph == playing || ph == paused:
		durationTo(f, pos)
	case total > 0:
		durationTo(f, total)
	default:
		byteSize(f, b.md.Size)
	}
	b.text(21, f.Bytes(), b.dim(), x, y, w)
}

var durBuf [24]byte

// secs is a length in seconds as a duration.
func secs(n uint32) time.Duration { return time.Duration(n) * time.Second }

// durationTo appends d as util::duration does: "0:07".
func durationTo(f *ui.Fmt, d time.Duration) *ui.Fmt {
	return f.Str(model.S(format.AppendDuration(durBuf[:0], uint32(d/time.Second))))
}

// music is an audio file's badge: a note on a disc, level with the track.
func (b *blk) music(x, y, d float32) {
	c := b.c
	theme.At(c, x, y, d, d)
	c.NextBG(theme.WithAlpha(b.accent(), 0.22))
	c.NextRadius(d / 2)
	c.NextRole(ui.RoleImage)
	c.NextSemName(i18n.T("Audio"))
	c.Push(c.BoxKeyed(base.Floating|ui.DrawBackground, b.kn(30), nil))
	b.icon(icMusic, b.accent(), 0, 0, d, d)
	c.Pop()
}

// chip is the speed chip (speed_pill): it cycles 1x, 1.5x and 2x, lit above
// 1x. The rate applies at once, so it never waits for a speed to be
// prepared. The message's menu lists every speed.
func (b *blk) chip(x, y, w, h float32) {
	k, c := b.k, b.c
	sp := Speed(b.e.S)
	active := sp > 1
	label := SpeedLabel(sp)
	name := speedName(c, label, 9)
	c.NextPos(x, y)
	c.NextFlags(base.Floating)
	pr := k.PressState(name, base.HoverOn)
	hot := pr.Hot > 0 || pr.Down > 0
	var fill, ink uint32
	switch {
	case active && hot:
		fill, ink = theme.WithAlpha(b.accent(), 0.42), b.accent()
	case active:
		fill, ink = theme.WithAlpha(b.accent(), 0.30), b.accent()
	case hot:
		fill, ink = b.col(theme.Fill2), b.dim()
	default:
		fill, ink = b.col(theme.Fill1), b.dim()
	}
	c.NextBG(fill)
	c.NextRadius(h / 2)
	c.NextCursor(ui.CursorPointer)
	if active {
		c.NextSemState(ui.SemSelected)
	}
	k.BeginPress(&pr, ui.RoleButton, ui.Px(w), ui.Px(h), ui.DrawBackground, nil)
	st := &b.r.Styles[theme.StyleSubheadline]
	tw := min(theme.Snap(b.textW(base.StrBytes(label)))+1, w)
	theme.At(c, theme.Snap((w-tw)/2), theme.Snap((h-st.Line)/2), tw, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(ink)
	c.BoxKeyed(base.Floating|ui.DrawText, b.kn(1), base.StrBytes(label))
	k.FocusRing(pr.ID, pr.Visual, h/2)
	sig := k.EndPress(&pr)
	rexui.Tooltip(k, pr.ID, i18n.T("Playback speed. Right-click for every speed."))
	if sig.Clicked {
		SetSpeed(b.e.S, nextCycled(sp))
	}
}

// ---- the waveform ----

// waveEnt is the waveform of one message, as its record carries it.
type waveEnt struct {
	id  mkey.K
	gen uint32
	ok  bool
	n   uint8 // bars in the record, 0: none
	v   [bars]byte
}

var waves [512]waveEnt

// heardEnt is a waveform the decoder measured for a message whose record
// had none (upstream's Player::bars).
type heardEnt struct {
	id mkey.K
	ok bool
	v  [bars]byte
}

var heard [64]heardEnt

var flat = func() (f [bars]byte) {
	for i := range f {
		f[i] = flatBar
	}
	return f
}()

func waveKey(a *state.Account, id []byte) mkey.K { return mkey.Of(a, id) }

// rememberBars keeps the waveform the decoder measured for message id of
// chat of account a.
func rememberBars(a *state.Account, chat model.ChatRef, id []byte, v *[bars]byte) {
	k := mkey.In(a, chat, id)
	heard[k.Slot(len(heard))] = heardEnt{id: k, ok: true, v: *v}
}

// waveform is the message's bars: its record's (decoded once per
// generation), else what the decoder measured, else flat ones.
func (b *blk) waveform() []byte {
	k := waveKey(b.a, b.m.ID())
	e := &waves[k.Slot(len(waves))]
	if !e.ok || e.id != k || e.gen != b.m.Gen() {
		*e = waveEnt{id: k, gen: b.m.Gen(), ok: true}
		if d := b.decoded(); d.Content.Tag == abi.ContentTagAudio {
			e.n = uint8(copy(e.v[:], d.Content.Audio.Waveform))
		}
	}
	if e.n > 0 {
		return e.v[:e.n]
	}
	if h := &heard[k.Slot(len(heard))]; h.ok && h.id == k {
		return h.v[:]
	}
	return flat[:]
}

// FooterLine is where the transcript puts an audio message's time and
// ticks (zf:src/ui/conversation.rs voice_player): the duration's line under
// the waveform, {0, y, r.W, h} in the block's coordinates, whose text
// reaches left pixels into it. ok is false for other content, and while the
// line says why a download failed (it is too long to share).
func FooterLine(e *screen.Env, m model.Message, r screen.Rect) (left, y, h float32, ok bool) {
	if m.Kind() != abi.ContentTagAudio || !Has(m) {
		return 0, 0, 0, false
	}
	b := use(e, m)
	if len(b.md.Path) == 0 && b.md.State == abi.MediaStateTagFailed {
		return 0, 0, 0, false
	}
	barH, lh := b.pt(voiceBarH), b.lineH()
	colY := theme.Snap((r.H - (barH + b.pt(2) + lh)) / 2)
	return b.pt(voiceBtn) + b.pt(voiceGap) + b.pt(durationPt), colY + barH + b.pt(2), lh, true
}

// durationPt is room for the duration's text ("12:34", "1.2 MB").
const durationPt = 48
