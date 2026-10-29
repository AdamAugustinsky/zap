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
// composer. Its title row moves it; its buttons shrink it to the floating
// Zap icon, open the chat in the main window, or close it. The icon shows
// the chat's unread count; a click brings the chat back, and the × it
// shows under the pointer closes it. The chat is read while the window has
// the focus (logic, and here when it takes the focus).
type Pip struct {
	v                        *View
	mini, open, close, glyph icon.ID
	shrunk                   bool // the window's size is the icon's
	focused                  bool // the window had the focus last frame
}

// Sizes in points.
const (
	pipW, pipH = 360, 520
	bubblePt   = 64 // the icon's window
	discPt     = 52 // the icon's disc, inside it: room for its ×
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
		win.SetFrameRect(x+w-nw, y, nw, nh)
	})
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

// bubble is the window shrunk: the Zap icon, the chat's unread count on
// it, and a × under the pointer that closes the chat.
func (p *Pip) bubble(e *screen.Env, a *state.Account, ch model.Chat) {
	st := e.S
	k := e.K
	c := k.Context()
	r := k.Theme()
	w, _ := c.Size()
	W := float32(w)
	disc := e.Pt(discPt)
	off := theme.Snap((W - disc) / 2)
	ps := k.PressState("pip icon", base.HoverOn)
	ps.Name = i18n.Format(i18n.T("Bring back {chat}"), "{chat}", model.S(Title(a, ch)))
	theme.At(c, off, off, disc, disc)
	c.NextBG(r.Colors[theme.Accent] | 0xFF)
	c.NextRadius(disc / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	k.BeginPress(&ps, ui.RoleButton, ui.Px(disc), ui.Px(disc), ui.DrawBackground|base.Floating, nil)
	k.Icon(p.glyph, 0xFFFFFFFF)
	sig := k.EndPress(&ps)
	rexui.Tooltip(k.Kit, sig.ID, model.S(Title(a, ch)))
	if sig.Clicked {
		st.SetPipMini(false)
	}
	if n := ch.Unread(); n > 0 {
		theme.At(c, off+disc-e.Pt(20), off-e.Pt(2), e.Pt(24), e.Pt(18))
		c.NextAxis(ui.AxisX)
		c.NextJustify(ui.JustifyCenter)
		c.Push(c.Box(base.Floating, "count"))
		rexui.Badge(k.Kit, count(n), rexui.BadgeOpts{Variant: rexui.BadgeCount, Name: i18n.T("Unread messages")})
		c.Pop()
	}
	if ps.Hot > 0 || c.Input().MouseX >= 0 && c.Input().MouseX < W && c.Input().MouseY >= 0 && c.Input().MouseY < W {
		x := e.Pt(18)
		theme.At(c, 0, 0, x, x)
		c.NextSize(ui.Px(x), ui.Px(x))
		if rexui.IconButton(k.Kit, p.close, i18n.T("Close"), rexui.IconButtonOpts{Variant: rexui.IconBordered}).Clicked {
			st.ClosePip()
		}
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
