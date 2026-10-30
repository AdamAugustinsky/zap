// Package peek is a chat looked at without being read (state.Peek), as
// Arc's peek and Zen's glance: its last messages in a panel beside the row
// it came from, and a line to answer in. Nothing here marks the chat read:
// the messages come as a hovered row's do (Account.Prefetch), and a reply
// is a plain send. Open opens the chat, which reads it as opening does.
// Escape, the close button or a click outside close the panel.
package peek

import (
	"math"

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
	"zapfast/ui/chatview"
	"zapfast/ui/screen"
)

// Sizes in points.
const (
	panelW    = 420 // the panel's least width; it takes more of a wide window
	panelWMax = 640
	panelMax  = 560 // the floating window's height (window.go)
	sideD     = 32  // the round buttons beside the panel
	shown     = 40  // the messages it shows, the newest
)

// How the overlay looks and moves, from Zen's glance (zap-notes/ZEN-REFERENCE.md).
const (
	recede  = 0.6  // the window under it fades this far toward its field
	share   = 0.55 // of the window's width the panel takes
	backC1  = 0.6  // easeOutBack's overshoot as it lands (about 1 %)
	arcMax  = 30   // points: the bow of its path at the most
	askSecs = 3    // a first Escape over a draft asks this long
)

// Screen is the overlay in the main window (State.Peek).
type Screen struct {
	v   view
	out bool // flying back to its row; it closes when it is there
}

// view is a peek's panel: the head, the messages, the line to answer in.
// The overlay and the floating window (Window) each have one.
type view struct {
	reply  ui.TextBuffer
	scroll base.ScrollState
	age    int // frames since it opened: the field's focus, the scroll to the end
	count  int // the messages the chat had at the last frame: a new one scrolls to it
	key    state.Peek
	asked  float32 // seconds left of a first Escape's question (a draft would go)
	close  icon.ID
	open   icon.ID
	split  icon.ID
	float  icon.ID
}

// act is what the user did in a view this frame.
type act uint8

const (
	actNone  act = iota
	actClose     // close it: the close button, Escape in the field
	actOpen      // open the chat (the view has closed nothing yet)
	actSplit     // open it beside the open chat, as Zen's glance splits
	actFloat     // keep it floating over other apps
)

// New is the overlay, closed.
func New() *Screen { return &Screen{} }

// Init readies its icon.
func (s *Screen) Init(e *screen.Env) error {
	s.v.icons(e)
	return nil
}

func (v *view) icons(e *screen.Env) {
	v.close = e.K.AddIcon(ztheme.ToolIcon("xmark", '×'))
	v.open = e.K.AddIcon(ztheme.ToolIcon("arrow.up.left.and.arrow.down.right", '⤢'))
	v.split = e.K.AddIcon(ztheme.ToolIcon("rectangle.split.2x1", '◫'))
	v.float = e.K.AddIcon(ztheme.ToolIcon("pip", '▣'))
}

// Build builds the panel while a peek is open, as Zen's glance: the window
// recedes under it, the panel flies out of the row on a shallow arc and
// lands with a little overshoot, and its buttons come out beside it. Closing
// flies it back.
func (s *Screen) Build(e *screen.Env) {
	st := e.S
	p := st.Peek
	a, ch, ok := s.v.start(st, p)
	if !ok {
		s.out = false
		if p.Open() {
			st.ClosePeek()
		}
		return
	}
	c, k := e.C(), e.K
	r := k.Theme()
	w, h := c.Size()
	W, H := float32(w), float32(h)

	// t is how far it is out: toward 1 arriving, toward 0 flying back
	goal, rate := float32(1), ztheme.Enter(r, ztheme.T.DurLong)
	if s.out {
		goal, rate = 0, ztheme.Exit(r, ztheme.T.DurLong)
	}
	t := c.AnimateValueFrom(ui.HashString(0, "peek in"), 0, goal, rate)
	if s.out && t < 0.02 {
		s.out = false
		st.ClosePeek()
		return
	}

	// A modal over the whole window: the window under it is inert, and the
	// composer's native text view, which lies above everything drawn, hides
	// under it (ui.HostRegions). The window fades back toward its field, with
	// no dim and no blur; a click outside, or Escape, closes it.
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleGroup)
	ask := c.BeginOverlay(ui.HashString(0, "peek"), ui.OverlayModal, [4]float32{0, 0, W, H}, ui.PlaceCenter).Dismissed
	defer c.EndOverlay()
	theme.At(c, 0, 0, W, H)
	c.NextBG(theme.WithAlpha(r.Colors[theme.Field]|0xFF, recede*min(t, 1)))
	if c.Signal(c.Box(base.Floating|ui.Clickable|ui.DrawBackground, "peek scrim")).Clicked {
		ask = true
	}

	// where it lands: centred with its buttons, the window's height
	d, gap := e.Pt(sideD), e.Pt(ztheme.Space3)
	pw := min(max(W*share, e.Pt(panelW)), e.Pt(panelWMax), W-2*e.Pt(ztheme.Space6)-gap-d)
	ph := H - 2*e.Pt(ztheme.Space6)
	x, y := (W-pw-gap-d)/2, (H-ph)/2
	fx, fy, fw, fh := s.flight(e, p.From, x, y, pw, ph, t, H)

	c.NextOpacity(min(t*4, 1))
	theme.At(c, fx, fy, fw, fh)
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextRadius(e.Pt(ztheme.WindowRadius))
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextShadow(ui.Shadow{OffY: e.Pt(16), Sigma: e.Pt(32), Color: 0x00000048})
	c.NextRole(ui.RoleDialog)
	c.NextSemName(i18n.Format(i18n.T("Peek: {chat}"), "{chat}", model.S(chatview.Title(a, ch))))
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.Clickable|ui.Clip, "peek"))
	// the content keeps the landed size while the panel grows round it (the
	// motion rules' rule 4: never animate what reflows text), and shows as
	// it nears its place
	c.NextCenter()
	c.NextSize(ui.Px(pw), ui.Px(ph))
	c.NextAxis(ui.AxisY)
	c.NextOpacity(min(max((t-0.4)/0.5, 0), 1))
	c.Push(c.Box(0, "peek content"))
	did := s.v.build(e, a, ch, pw, false)
	c.Pop()
	c.Pop()

	if s.v.asked > 0 {
		s.v.asked -= c.DT()
		if s.v.asked > 0 {
			c.WakeIn(int64(s.v.asked * 1e9))
		}
	}
	if b := s.v.side(e, fx+fw+gap, fy+e.Pt(ztheme.Space4), d, t); b != actNone {
		did = b
	}
	if c.TakeKey(ui.KeyEscape, 0) || did == actClose {
		ask, did = true, actNone
	}
	if s.out {
		return // flying back: nothing more it does
	}
	if ask && s.v.mayClose() {
		s.out = true
		c.RequestFrame()
		return
	}
	switch did {
	case actOpen:
		st.ClosePeek()
		openChat(st, a, ch)
	case actSplit:
		st.ClosePeek()
		splitChat(st, a, ch)
	case actFloat:
		st.ClosePeek()
		st.FloatChat(a, ch.Ref())
	}
}

// flight is the panel's rectangle t of the way out of from (a window
// rectangle, x0 y0 x1 y1) to its place x y w h: easeOutBack on a path that
// bows toward the side with more room, as Zen's glance. From nowhere (a
// shortcut, a notification) it grows a little where it lands.
func (s *Screen) flight(e *screen.Env, from [4]float32, x, y, w, h, t, H float32) (float32, float32, float32, float32) {
	if from[2] <= from[0] || from[3] <= from[1] {
		k := 0.96 + 0.04*t
		return x + w*(1-k)/2, y + h*(1-k)/2, w * k, h * k
	}
	b := t
	if !s.out {
		b = back(t)
	}
	fx := from[0] + (x-from[0])*b
	fy := from[1] + (y-from[1])*b
	fw := (from[2] - from[0]) + (w-(from[2]-from[0]))*b
	fh := (from[3] - from[1]) + (h-(from[3]-from[1]))*b
	// the bow: the most at the middle of the path, toward the roomier side
	dx, dy := x+w/2-(from[0]+from[2])/2, y+h/2-(from[1]+from[3])/2
	bow := min(float32(math.Hypot(float64(dx), float64(dy)))*0.4, e.Pt(arcMax))
	if (from[1]+from[3])/2 < H/2 {
		bow = -bow // more room below: it rises over the path, then comes down
	}
	fy += bow * float32(math.Sin(math.Pi*float64(min(max(b, 0), 1))))
	return fx, fy, fw, fh
}

// back is easeOutBack (c1 = backC1): it passes 1 by about 1 % and settles.
func back(t float32) float32 {
	u := t - 1
	return 1 + (backC1+1)*u*u*u + backC1*u*u
}

// mayClose reports whether a close may go ahead: at once with nothing typed,
// and with a draft only once asked, as Zen's glance asks before Escape closes
// a page (the close button turns red and says Close for askSecs).
func (v *view) mayClose() bool {
	if v.reply.Len() == 0 || v.asked > 0 {
		return true
	}
	v.asked = askSecs
	return false
}

// side is the column of round buttons beside the panel's right edge, at x
// y, each d across: close, open, open beside, float. They come out of the
// panel once it is nearly in place.
func (v *view) side(e *screen.Env, x, y, d, t float32) act {
	k := e.K
	c := k.Context()
	r := k.Theme()
	in := c.AnimateValueFrom(ui.HashString(0, "peek side"), 0, b2f(t > 0.85), ztheme.Enter(r, ztheme.T.DurStandard))
	if in <= 0 {
		return actNone
	}
	c.NextOpacity(in)
	c.NextPos(x-(1-in)*e.Pt(20), y)
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(ztheme.Space3))
	c.Push(c.Box(base.Floating, "peek side"))
	did := actNone
	if v.asked > 0 {
		// asked: the close button is the question, red, with its word
		k.NextHeight(ui.Px(d))
		if rexui.Button(k.Kit, i18n.T("Close")+"###peek close", rexui.ButtonOpts{Variant: rexui.ButtonDestructive, Size: theme.SizeSmall}).Clicked {
			did = actClose
		}
	} else if v.round(e, v.close, i18n.T("Close"), d) {
		did = actClose
	}
	if v.round(e, v.open, i18n.T("Open"), d) {
		did = actOpen
	}
	if v.round(e, v.split, i18n.T("Open beside the open chat"), d) {
		did = actSplit
	}
	if v.round(e, v.float, i18n.T("Float over other apps"), d) {
		did = actFloat
	}
	c.Pop()
	return did
}

// round is one of the side buttons: a raised circle d across with a symbol.
func (v *view) round(e *screen.Env, id icon.ID, label string, d float32) bool {
	k := e.K
	c := k.Context()
	r := k.Theme()
	c.NextSize(ui.Px(d), ui.Px(d))
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextRadius(d / 2)
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextShadow(ui.Shadow{OffY: e.Pt(2), Sigma: e.Pt(6), Color: 0x00000026})
	c.Push(c.Box(ui.DrawBackground|ui.DrawBorder, c.Fmt().Str("peek side ").Str(label).String()))
	k.NextSize(ui.Px(d), ui.Px(d))
	hit := rexui.IconButton(k.Kit, id, label, rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked
	c.Pop()
	return hit
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// splitChat opens ch beside the open chat in the main window (or opens it,
// with none open).
func splitChat(st *state.State, a *state.Account, ch model.Chat) {
	if a.Index != st.Active {
		openChat(st, a, ch) // a split is of one account's chats
		return
	}
	st.Page = state.PageChats
	a.OpenSplit(ch.Ref())
}

// start readies the view for peek p: the chat it looks at, and whether it
// shows (a chat that is there, with the app unlocked). A new chat starts the
// view afresh.
func (v *view) start(st *state.State, p state.Peek) (*state.Account, model.Chat, bool) {
	if !p.Open() || st.Lock.Locked {
		if v.age != 0 {
			v.reset()
		}
		return nil, model.Chat{}, false
	}
	a := st.AccountByID(p.Acct)
	if a == nil {
		return nil, model.Chat{}, false
	}
	ch, ok := a.Model.Chats.Get(p.Ref)
	if !ok {
		return nil, model.Chat{}, false
	}
	if v.key.Acct != p.Acct || v.key.Ref != p.Ref {
		v.reset()
		v.key = p
	}
	v.age++
	return a, ch, true
}

// build builds the panel's content in the current box, pw pixels wide;
// drag makes its head move the window (the floating panel's).
func (v *view) build(e *screen.Env, a *state.Account, ch model.Chat, pw float32, drag bool) act {
	did := v.header(e, a, ch, drag)
	if did != actNone {
		return did
	}
	v.messages(e, a, ch, pw)
	return v.footer(e, a, ch)
}

// openChat opens ch in the main window, which reads it as opening does.
func openChat(st *state.State, a *state.Account, ch model.Chat) {
	if a.Index != st.Active {
		st.SwitchAccount(a.Index)
	}
	st.Page = state.PageChats
	a.OpenChat(ch.Ref())
}

// header is the chat's picture and name, what a peek is, Open and close.
func (v *view) header(e *screen.Env, a *state.Account, ch model.Chat, drag bool) act {
	k := e.K
	c := k.Context()
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(ztheme.Space3))
	c.NextPadding(e.Pt(ztheme.Space3), e.Pt(ztheme.Space3), e.Pt(ztheme.Space2), e.Pt(ztheme.Space2))
	c.NextSize(ui.Pct(1), ui.Children(1))
	var fl ui.Flags
	if drag {
		fl = ui.WindowDrag
	}
	c.Push(c.Box(fl, "head"))
	name := model.S(chatview.Title(a, ch))
	side := e.Pt(28)
	rexui.Avatar(k.Kit, name, rexui.AvatarOpts{Size: 28, Image: screen.Avatar(k.Images(), a, ch.ID(), false, int32(side))})
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "titles"))
	k.NextWidth(ui.Pct(1))
	rexui.Label(k.Kit, name, rexui.LabelOpts{Style: rexui.TextHeadline})
	k.NextWidth(ui.Pct(1))
	rexui.Label(k.Kit, i18n.T("Peeking: it stays unread"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	c.Pop()
	did := actNone
	if !drag {
		// the overlay's buttons are beside the panel (side)
		c.Pop()
		v.headLine(e)
		return did
	}
	hit := ui.Px(e.Pt(ztheme.T.HitTool))
	k.NextSize(hit, hit)
	if rexui.IconButton(k.Kit, v.split, i18n.T("Open beside the open chat"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		did = actSplit
	}
	k.NextSize(hit, hit)
	if rexui.IconButton(k.Kit, v.float, i18n.T("Float over other apps"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		did = actFloat
	}
	if rexui.Button(k.Kit, i18n.T("Open"), rexui.ButtonOpts{Size: theme.SizeSmall}).Clicked {
		did = actOpen
	}
	k.NextSize(hit, hit)
	if rexui.IconButton(k.Kit, v.close, i18n.T("Close"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		did = actClose
	}
	c.Pop()
	v.headLine(e)
	return did
}

// headLine is the line under the head.
func (v *view) headLine(e *screen.Env) {
	c := e.C()
	c.NextSize(ui.Pct(1), ui.Px(e.K.Theme().Lines.Hairline))
	c.NextBG(e.K.Theme().Colors[theme.Separator])
	c.Box(ui.DrawBackground, "head line")
}

// messages are the newest messages, quiet bubbles, the newest at the foot.
func (v *view) messages(e *screen.Env, a *state.Account, ch model.Chat, pw float32) {
	k := e.K
	c := k.Context()
	r := k.Theme()
	cv := a.Model.Conversation(ch.ID())
	k.NextHeight(ui.Grow(1)) // what the head and the reply leave
	k.NextWidth(ui.Pct(1))
	sv := k.BeginScroll("peek messages", &v.scroll, base.ScrollVertical)
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(ztheme.T.RowGap))
	c.NextPadding(e.Pt(ztheme.Space3), e.Pt(ztheme.Space3), e.Pt(ztheme.Space3), e.Pt(ztheme.Space3))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "list"))
	if cv == nil || cv.Len() == 0 {
		rexui.Label(k.Kit, i18n.T("Loading…"), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	} else {
		maxW := (pw - 2*e.Pt(ztheme.Space3)) * 0.8
		first := max(cv.Len()-shown, 0)
		var prev model.Message
		for i := first; i < cv.Len(); i++ {
			m := cv.At(i)
			if m.Kind() == abi.ContentTagCall {
				continue
			}
			run := i > first && prev.FromMe() == m.FromMe() && string(prev.Sender()) == string(m.Sender())
			if !run && i > first {
				c.NextSize(ui.Px(1), ui.Px(e.Pt(max(ztheme.T.RunGap-ztheme.T.RowGap, 0))))
				c.Box(0, c.Fmt().Str("gap ").Int(i).String())
			}
			chatview.Bubble(e, a, ch, m, i, !run, maxW, r)
			prev = m
		}
	}
	c.Pop()
	k.CloseScrollContent(&sv)
	n := 0
	if cv != nil {
		n = cv.Len()
	}
	if v.age <= 3 || n != v.count {
		// the newest in view, as the chat opens at its end and as a message
		// comes in (the reply sent from here among them)
		c.SetScroll(sv.Inner, 0, 1e9)
	}
	v.count = n
	rexui.ScrollBars(k.Kit, &sv)
	k.CloseScroll(&sv)
}

// footer is the line to answer in: Enter sends, and the chat stays unread.
func (v *view) footer(e *screen.Env, a *state.Account, ch model.Chat) act {
	k := e.K
	c := k.Context()
	c.NextSize(ui.Pct(1), ui.Px(k.Theme().Lines.Hairline))
	c.NextBG(k.Theme().Colors[theme.Separator])
	c.Box(ui.DrawBackground, "foot line")
	c.NextPadding(e.Pt(ztheme.Space3), e.Pt(ztheme.Space2), e.Pt(ztheme.Space3), e.Pt(ztheme.Space3))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "reply"))
	k.NextWidth(ui.Pct(1))
	hint := i18n.Format(i18n.T("Reply to {chat}"), "{chat}", model.S(chatview.Title(a, ch)))
	f := rexui.TextField(k.Kit, i18n.T("Quick reply")+"###peek reply", &v.reply, hint, rexui.FieldOpts{})
	if v.age <= 2 && canSend(ch) {
		c.SetFocus(f.Key)
	}
	did := actNone
	if f.Cancelled {
		did = actClose
	}
	if f.Submitted && canSend(ch) {
		if text := v.reply.String(); len(text) > 0 {
			if err := a.SendText(ch.Ref(), []byte(text)); err == nil {
				v.reply.SetString("")
			}
		}
	}
	c.Pop()
	return did
}

// canSend reports whether a peek can answer in ch: not a channel one only
// reads, not a group that has left one.
func canSend(ch model.Chat) bool { return !ch.ReadOnly() }

func (v *view) reset() {
	v.reply.SetString("")
	v.scroll = base.ScrollState{}
	v.age, v.count, v.asked = 0, 0, 0
	v.key = state.Peek{}
}
