package stickermaker

import (
	"math"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// viewPt is the height of the picture area, in points
// (zf:src/ui/dialogs.rs MAKER_VIEW).
const viewPt = 300

// draft is a picture on its way to becoming a sticker
// (zf:src/model.rs StickerDraft); the emojis are in the Screen's text field.
type draft struct {
	path        []byte
	file        string // path as a string, for the image cache
	w, h        uint32
	transparent bool
	// keep: see-through pixels stay see-through instead of turning white.
	keep bool
	crop Square
}

// Screen is the sticker maker.
type Screen struct {
	st   *state.State
	imgs *img.Cache

	d      draft
	has    bool
	emojis ui.TextBuffer
	open   bool
	shown  bool // the dialog was on screen in the last frame
	// grab: the square when a drag began, so a drag is measured from there.
	grab struct {
		on    bool
		start Square
	}
}

var _ screen.Overlay = (*Screen)(nil)

// New returns the sticker maker.
func New() *Screen { return &Screen{} }

// Init readies it for a window.
func (s *Screen) Init(e *screen.Env) error {
	s.st = e.S
	s.emojis.Reserve(256)
	e.S.Listen((*listener)(s), abi.EventTagStickerPicture)
	return nil
}

// listener is the Screen as a state.Listener: the core has chosen and read a
// picture (zf:src/app.rs, Event::StickerPicture).
type listener Screen

func (l *listener) Event(a *state.Account, e *abi.Event) {
	s := (*Screen)(l)
	p := &e.StickerPicture
	s.d = draft{
		path: append(s.d.path[:0], p.Path...), file: string(p.Path), w: p.Width, h: p.Height,
		transparent: p.Transparent, keep: p.Transparent,
		crop: Centered(p.Width, p.Height),
	}
	s.has = true
	s.emojis.Reset()
	s.grab.on = false
	if a.Active() {
		s.st.OpenDialog(state.Dialog{Kind: state.DialogStickerMaker})
	}
}

// Build draws the sticker maker while state.DialogStickerMaker is open.
func (s *Screen) Build(e *screen.Env) {
	s.imgs = e.K.Images() // the window's cache; the window polls it
	d := &e.S.Dialog
	if d.Kind != state.DialogStickerMaker {
		if s.shown {
			s.shown, s.has = false, false // the draft is spent or abandoned
		}
		return
	}
	a := e.Acct()
	if a == nil {
		return
	}
	if !s.has {
		// Opened without a picture: the system's picker chooses one, and the
		// core answers with StickerPicture, which opens this again.
		a.Send(a.Cmd(abi.CommandTagPickStickerPicture))
		e.S.CloseDialog()
		return
	}
	if !s.shown {
		s.shown, s.open = true, true
	}
	k := e.K
	winW, _ := winPt(e)
	dl := rexui.BeginDialog(k.Kit, "sticker-maker", &s.open, rexui.DialogOpts{Title: i18n.T("Make a sticker"), Width: min(420, max(winW-64, 220))})
	if dl.Visible {
		s.body(e, a, &dl)
	}
	rexui.EndDialog(k.Kit, &dl)
	if !s.open && e.S.Dialog.Kind == state.DialogStickerMaker {
		e.S.CloseDialog()
	}
}

func winPt(e *screen.Env) (w, h float32) {
	pw, ph := e.C().Size()
	sc := e.K.Scale()
	return float32(pw) / sc, float32(ph) / sc
}

func (s *Screen) body(e *screen.Env, a *state.Account, dl *rexui.Dialog) {
	k := e.K
	d := &s.d
	s.view(e)

	smallest, largest := Smallest(d.w, d.h), max(min(d.w, d.h), 1)
	side := float32(d.crop.Side)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "size", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, i18n.T("Size"), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	k.NextWidth(widget.Fill)
	if rexui.Slider(k.Kit, i18n.T("Size")+"###slider", &side, float32(smallest), float32(largest), 1, rexui.SliderOpts{}).Changed {
		d.crop = d.crop.Resized(uint32(side+0.5), d.w, d.h)
	}
	k.EndContainer()

	if d.transparent {
		rexui.Switch(k.Kit, i18n.T("Keep the transparent background"), &d.keep, rexui.SwitchOpts{})
	}
	hint := i18n.T("Emojis that describe it, like 😂 or ❤️")
	rexui.TextField(k.Kit, hint, &s.emojis, hint, rexui.FieldOpts{})

	chat, canSend := a.Model.Chats.Get(a.Open)
	canSend = canSend && a.Open != 0
	dl.BeginActions()
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	if dl.Action(i18n.T("Add to favorites"), variant(!canSend)) {
		s.makeSticker(e, a, nil)
	}
	if canSend && dl.Action(i18n.T("Send"), rexui.ButtonPrimary) {
		s.makeSticker(e, a, &chat)
	}
	dl.EndActions()
}

func variant(primary bool) rexui.ButtonVariant {
	if primary {
		return rexui.ButtonPrimary
	}
	return rexui.ButtonDefault
}

// makeSticker asks the core for the sticker (zf:src/app.rs, Action::MakeSticker):
// sent to the open chat, quoting what the composer quotes, or kept among the
// favourites. The sheet closes with the action.
func (s *Screen) makeSticker(e *screen.Env, a *state.Account, to *model.Chat) {
	d := &s.d
	cmd := a.Cmd(abi.CommandTagMakeSticker)
	m := &cmd.MakeSticker
	m.Source = d.path
	m.Crop = abi.StickerCrop{X: d.crop.X, Y: d.crop.Y, Side: d.crop.Side}
	m.Transparent = d.transparent && d.keep
	m.Emojis = CleanEmojis(s.emojis.Bytes())
	if to != nil {
		m.Chat, m.HasChat = to.ID(), true
		if reply := e.S.Panes.Reply; reply.Len() > 0 {
			m.Quoting.ID, m.HasQuoting = reply.At(0), true
			if rc := e.S.Panes.ReplyChat; rc.Len() > 0 { // a private reply to a group message
				m.Quoting.Chat, m.Quoting.HasChat = rc.At(0), true
			}
		}
	}
	a.Send(cmd)
	s.has = false
}

// ---- the picture ----

// view is the picture with the square over it. Dragging anywhere moves the
// square; what it leaves out is faded.
func (s *Screen) view(e *screen.Env) {
	k, c := e.K, e.C()
	d := &s.d
	vkey := ui.HashString(c.ParentKey(), "view")
	k.BeginContainer(ui.AxisY, ui.CrossStart, "view", ui.Pct(1), k.Fixed(viewPt), 0)
	defer k.EndContainer()
	r := c.RectOf(vkey)
	vw, vh := r[2]-r[0], r[3]-r[1]
	if vw <= 0 || vh <= 0 {
		c.RequestFrame() // sized in the next frame
		return
	}

	scale := min(vw/float32(d.w), vh/float32(d.h))
	pic := rect{w: theme.Snap(float32(d.w) * scale), h: theme.Snap(float32(d.h) * scale)}
	pic.x, pic.y = theme.Snap((vw-pic.w)/2), theme.Snap((vh-pic.h)/2)
	scale = pic.w / float32(d.w)

	dragKey := ui.KeyN(c.ParentKey(), 9301)
	if sg := c.PeekSignal(dragKey); scale > 0 {
		if sg.Pressed {
			s.grab.on, s.grab.start = true, d.crop
		}
		if s.grab.on && (sg.Dragging || sg.Released) {
			dx := int64(math.Round(float64(sg.DragDelta[0] / scale)))
			dy := int64(math.Round(float64(sg.DragDelta[1] / scale)))
			d.crop = s.grab.start.Moved(dx, dy, d.w, d.h)
		}
		if !sg.Pressed && !sg.Dragging {
			s.grab.on = false
		}
	}

	// The area, then the picture in it.
	at(c, rect{0, 0, vw, vh})
	c.NextBG(k.Theme().Colors[theme.Fill3])
	c.NextRadius(e.Pt(8))
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(c.ParentKey(), 9300), nil)
	s.picture(e, pic)

	sq := rect{
		pic.x + float32(d.crop.X)*scale, pic.y + float32(d.crop.Y)*scale,
		float32(d.crop.Side) * scale, float32(d.crop.Side) * scale,
	}
	shade := theme.WithAlpha(k.Theme().Colors[theme.Shadow], 0.6)
	for i, o := range [...]rect{
		{pic.x, pic.y, pic.w, sq.y - pic.y},
		{pic.x, sq.y + sq.h, pic.w, pic.y + pic.h - sq.y - sq.h},
		{pic.x, sq.y, sq.x - pic.x, sq.h},
		{sq.x + sq.w, sq.y, pic.x + pic.w - sq.x - sq.w, sq.h},
	} {
		if o.w <= 0 || o.h <= 0 {
			continue
		}
		at(c, o)
		c.NextBG(shade)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(c.ParentKey(), 9310+uint64(i)), nil)
	}
	at(c, sq)
	c.NextBorder(0xFFFFFFFF, e.Pt(2))
	c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(c.ParentKey(), 9320), nil)

	// The whole area takes the drag.
	at(c, rect{0, 0, vw, vh})
	c.NextCursor(ui.CursorPointer)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Sticker"))
	c.BoxKeyed(ui.Clickable|ui.Hoverable|base.Floating, dragKey, nil)
}

type rect struct{ x, y, w, h float32 }

func at(c *ui.Context, r rect) { theme.At(c, r.x, r.y, r.w, r.h) }

// picture draws the source picture, contained in pic.
func (s *Screen) picture(e *screen.Env, pic rect) {
	c := e.C()
	key := ui.KeyN(c.ParentKey(), 9302)
	at(c, pic)
	if s.imgs != nil {
		h, _ := s.imgs.Request(img.Key{Hash: img.HashBytes(s.d.path), W: 1024, H: 1024}, img.File(s.d.file))
		if tex, uv, st := s.imgs.Use(h); st == img.Ready {
			c.NextImageFit(tex, uv, ui.FitContain)
			c.NextTextColor(0xFFFFFFFF)
			c.BoxKeyed(ui.DrawImage|base.Floating, key, nil)
			return
		}
	}
	c.NextBG(e.K.Theme().Colors[theme.Fill2])
	c.BoxKeyed(ui.DrawBackground|base.Floating, key, nil)
}
