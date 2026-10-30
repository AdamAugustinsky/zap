package chatview

import (
	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Pip is the chat floating over other apps (state.Pip, WinPip), as a
// video's picture in picture: a small window on every Space that stays
// over the apps the person works in, with the chat's transcript and a
// composer. Its title row moves it anywhere on the screen; its buttons
// shrink it to the floating Zap icon, open the chat in the main window, or
// close it. The icon shows the chat's unread count and moves anywhere
// too; a click on it brings the chat back, and under the pointer a × at
// its top right closes it. The chat is read while the window has
// the focus (logic, and here when it takes the focus).
type Pip struct {
	v                        *View
	mini, open, close, glyph icon.ID
	xsmall                   icon.ID    // the close bubble's ×
	shrunk                   bool       // the window's size is the icon's
	drag                     bubbleDrag // a press on the icon (dragBubble)
	focused                  bool       // the window had the focus last frame
}

// Sizes in points.
const (
	pipW, pipH = 360, 520
	bubblePt   = 78 // the icon's window: the disc, and room for its close bubble
	discPt     = 52 // the icon's disc, at the window's bottom left
	closePt    = 24 // the close bubble beside it, at the top right
	pipMargin  = 16 // off the screen's edges
)

// NewPip is the floating chat's window.
func NewPip() *Pip { return &Pip{v: New("pip")} }

// Init readies its icons and its composer, in its window.
func (p *Pip) Init(e *screen.Env) error {
	k := e.K
	p.mini = k.AddIcon(ztheme.ToolIcon("minus", '–'))
	p.open = k.AddIcon(ztheme.ToolIcon("macwindow", '□'))
	p.close = k.AddIcon(ztheme.ToolIcon("xmark", '×'))
	p.glyph = k.AddIcon(ztheme.LargeIcon("bubble.left.fill", '●'))
	p.xsmall = k.AddIcon(ztheme.SmallIcon("xmark", '×'))
	p.shrunk, p.focused = false, false
	return p.v.Init(e)
}

// Config is a floating panel at the bottom right of the main screen. It
// draws its own shape (opaque: a chat stays legible over any app).
func (p *Pip) Config(*state.State) app.WindowConfig {
	return app.WindowConfig{
		Title: "Zap", Width: pipW, Height: pipH,
		Panel: &platform.Panel{Floating: true, AllSpaces: true, Shadow: true, Radius: ztheme.WindowRadius},
		Place: placePip,
	}
}

func placePip(win *platform.Window) {
	scr := app.Screens(nil)
	if len(scr) == 0 {
		return
	}
	v := scr[0]
	win.SetFrameRect(v.VisibleX+v.VisibleW-pipW-pipMargin, v.VisibleY+v.VisibleH-pipH-pipMargin, pipW, pipH)
}

// Build builds the chat, or the icon; it closes the window when the chat
// has gone.
func (p *Pip) Build(e *screen.Env) {
	st := e.S
	pp := &st.Pip
	a := st.AccountByID(pp.Acct)
	var ch model.Chat
	ok := a != nil && pp.Open() && !st.Lock.Locked
	if ok {
		ch, ok = a.Model.Chats.Get(pp.Ref)
	}
	if !ok || ch.Locked() {
		st.ClosePip()
		return
	}
	p.v.Bind(a, ch)
	p.resize(e, pp.Mini)
	c := e.C()
	focused := c.Input().Focused && !pp.Mini
	if focused != pp.Focused {
		pp.Focused = focused
		st.Touch()
	}
	if focused && !p.focused && (ch.Unread() > 0 || ch.MarkedUnread()) {
		a.MarkRead(ch.Ref()) // the person came to it: read
	}
	p.focused = focused
	if pp.Mini {
		p.bubble(e, a, ch)
		return
	}
	p.chat(e, a, ch)
}

// resize gives the window the icon's size or the chat's, keeping its top
// right corner where it is, between frames.
func (p *Pip) resize(e *screen.Env, mini bool) {
	if mini == p.shrunk || e.A == nil || e.A.Win == nil {
		return
	}
	p.shrunk = mini
	win := e.A.Win
	e.A.AtSafePoint(func() {
		x, y, w, _ := win.FrameRect()
		nw, nh := float64(pipW), float64(pipH)
		if mini {
			nw, nh = bubblePt, bubblePt
		}
		nx, ny := onScreen(x+w-nw, y, nw, nh)
		win.SetFrameRect(nx, ny, nw, nh)
	})
}

// onScreen moves a w×h frame at x, y into the visible part of the screen
// it is on (the chat growing back from an icon near an edge).
func onScreen(x, y, w, h float64) (float64, float64) {
	for _, v := range app.Screens(nil) {
		if x+w/2 < v.X || x+w/2 >= v.X+v.W || y+h/2 < v.Y-h || y >= v.Y+v.H {
			continue
		}
		m := float64(pipMargin)
		x = min(max(x, v.VisibleX+m), v.VisibleX+v.VisibleW-w-m)
		y = min(max(y, v.VisibleY+m), v.VisibleY+v.VisibleH-h-m)
		return x, y
	}
	return x, y
}

// chat is the full window: the title row, the transcript, the composer.
func (p *Pip) chat(e *screen.Env, a *state.Account, ch model.Chat) {
	st := e.S
	k := e.K
	c := k.Context()
	r := k.Theme()
	w, h := c.Size()
	W, H := float32(w), float32(h)
	theme.At(c, 0, 0, W, H)
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextRadius(e.Pt(ztheme.WindowRadius))
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleDialog)
	c.NextSemName(i18n.Format(i18n.T("Floating: {chat}"), "{chat}", model.S(Title(a, ch))))
	c.Push(c.Box(ui.DrawBackground|ui.DrawBorder, "pip"))

	// the title row: the chat, and what the window does; it moves the window
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(ztheme.Space2))
	c.NextPadding(e.Pt(ztheme.Space3), e.Pt(ztheme.Space2), e.Pt(ztheme.Space2), e.Pt(ztheme.Space2))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(ui.WindowDrag, "head"))
	name := model.S(Title(a, ch))
	side := e.Pt(24)
	rexui.Avatar(k.Kit, name, rexui.AvatarOpts{Size: 24, Image: screen.Avatar(k.Images(), a, ch.ID(), false, int32(side))})
	k.NextWidth(ui.Pct(1))
	rexui.Label(k.Kit, name, rexui.LabelOpts{Style: rexui.TextHeadline})
	hit := ui.Px(e.Pt(ztheme.T.HitTool))
	k.NextSize(hit, hit)
	if rexui.IconButton(k.Kit, p.mini, i18n.T("Shrink to the floating icon"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		st.SetPipMini(true)
	}
	k.NextSize(hit, hit)
	if rexui.IconButton(k.Kit, p.open, i18n.T("Open in Zap"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		st.ClosePip()
		if a.Index != st.Active {
			st.SwitchAccount(a.Index)
		}
		st.Page = state.PageChats
		a.OpenChat(ch.Ref())
		st.RaiseMain()
	}
	k.NextSize(hit, hit)
	if rexui.IconButton(k.Kit, p.close, i18n.T("Close"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		st.ClosePip()
	}
	c.Pop()
	c.NextSize(ui.Pct(1), ui.Px(r.Lines.Hairline))
	c.NextBG(r.Colors[theme.Separator])
	c.Box(ui.DrawBackground, "head line")

	p.v.Transcript(e, a, ch, W)
	p.v.Composer(e, a, ch)
	c.Pop()
}

// bubble is the window shrunk: the Zap icon with the chat's unread count.
// A click on it brings the chat back; dragged, it moves the window anywhere
// on the screen (it moves the window itself: a system drag area would keep
// the click); under the pointer a × at its top right closes the chat.
func (p *Pip) bubble(e *screen.Env, a *state.Account, ch model.Chat) {
	st := e.S
	k := e.K
	c := k.Context()
	r := k.Theme()
	in := c.Input()
	w, _ := c.Size()
	W := float32(w)
	disc := e.Pt(discPt)
	// the disc at the bottom left, clear of the close bubble's corner
	dx, dy := e.Pt(4), W-disc-e.Pt(4)
	ps := k.PressState("pip icon", base.HoverOn)
	ps.Name = i18n.Format(i18n.T("Bring back {chat}"), "{chat}", model.S(Title(a, ch)))
	theme.At(c, dx, dy, disc, disc)
	c.NextBG(r.Colors[theme.Accent] | 0xFF)
	c.NextRadius(disc / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	k.BeginPress(&ps, ui.RoleButton, ui.Px(disc), ui.Px(disc), ui.DrawBackground|base.Floating, nil)
	k.Icon(p.glyph, 0xFFFFFFFF)
	sig := k.EndPress(&ps)
	p.dragBubble(e, c.Signal(sig.ID))
	if sig.Clicked && !p.drag.moved {
		st.SetPipMini(false)
	}
	if n := ch.Unread(); n > 0 && !in.Inside {
		theme.At(c, dx+disc-e.Pt(20), dy-e.Pt(2), e.Pt(24), e.Pt(18))
		c.NextAxis(ui.AxisX)
		c.NextJustify(ui.JustifyCenter)
		c.Push(c.Box(base.Floating, "count"))
		rexui.Badge(k.Kit, count(n), rexui.BadgeOpts{Variant: rexui.BadgeCount, Name: i18n.T("Unread messages")})
		c.Pop()
	}
	if in.Inside && !p.drag.moved {
		// the close bubble: a small one of its own at the top right, over
		// nothing it could be mistaken for, while the pointer is on the window
		x := e.Pt(closePt)
		cs := k.PressState("pip close", base.HoverOn)
		cs.Name = i18n.T("Close")
		theme.At(c, W-x-e.Pt(2), e.Pt(2), x, x)
		fill := r.Colors[theme.Raised] | 0xFF
		if cs.Hot > 0 {
			fill = theme.MixRGBA(fill, r.Colors[theme.Label]|0xFF, 0.12*cs.Hot)
		}
		c.NextBG(fill)
		c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
		c.NextRadius(x / 2)
		c.NextShadow(ui.Shadow{OffY: e.Pt(1), Sigma: e.Pt(3), Color: 0x00000040})
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		k.BeginPress(&cs, ui.RoleButton, ui.Px(x), ui.Px(x), ui.DrawBackground|ui.DrawBorder|base.Floating, nil)
		k.Icon(p.xsmall, r.Colors[theme.Label])
		if k.EndPress(&cs).Clicked {
			st.ClosePip()
		}
	}
}

// bubbleDrag is a press on the icon: where the window and the pointer were
// on the screen, and whether it has moved past a click.
type bubbleDrag struct {
	on, moved bool
	frame     [4]float64 // the window's frame at the press, points
	at        [2]float64 // the pointer on the screen at the press, points
}

// dragBubble moves the window with the pointer while the icon is dragged
// (sig, its box's signal), from where both were at the press.
func (p *Pip) dragBubble(e *screen.Env, sig ui.Signal) {
	d := &p.drag
	if e.A == nil || e.A.Win == nil {
		return
	}
	win := e.A.Win
	in := e.C().Input()
	sc := float64(e.K.Scale())
	x, y, w, h := win.FrameRect()
	pointer := [2]float64{x + float64(in.MouseX)/sc, y + float64(in.MouseY)/sc}
	switch {
	case sig.Pressed:
		d.on, d.moved = true, false
		d.frame, d.at = [4]float64{x, y, w, h}, pointer
	case sig.Dragging && d.on:
		dx, dy := pointer[0]-d.at[0], pointer[1]-d.at[1]
		if !d.moved && dx*dx+dy*dy < 9 {
			return // still a click
		}
		d.moved = true
		nx, ny := d.frame[0]+dx, d.frame[1]+dy
		if nx != x || ny != y {
			e.A.AtSafePoint(func() { win.SetFrameRect(nx, ny, w, h) })
		}
		e.C().RequestFrame()
	case !sig.Dragging && !sig.Released:
		d.on = false
	}
}

func count(n int) string {
	if n > 99 {
		return "99+"
	}
	b := make([]byte, 0, 3)
	if n >= 10 {
		b = append(b, byte('0'+n/10))
	}
	return string(append(b, byte('0'+n%10)))
}

// Closed keeps the draft and forgets the floating chat once its window
// has gone (closed from it, by the app, or by the system).
func (p *Pip) Closed(e *screen.Env) {
	p.v.Release()
	if e.S.Pip.Open() {
		e.S.Pip = state.Pip{}
		e.S.Touch()
	}
}
