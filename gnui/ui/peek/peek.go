// Package peek is a chat looked at without being read (state.Peek), as
// Arc's peek and Zen's glance: its last messages in a panel beside the row
// it came from, and a line to answer in. Nothing here marks the chat read:
// the messages come as a hovered row's do (Account.Prefetch), and a reply
// is a plain send. Open opens the chat, which reads it as opening does.
// Escape, the close button or a click outside close the panel.
package peek

import (
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
	panelW   = 420
	panelMax = 560
	shown    = 40 // the messages it shows, the newest
)

// Screen is the overlay in the main window (State.Peek).
type Screen struct{ v view }

// view is a peek's panel: the head, the messages, the line to answer in.
// The overlay and the floating window (Window) each have one.
type view struct {
	reply  ui.TextBuffer
	scroll base.ScrollState
	age    int // frames since it opened: the field's focus, the scroll to the end
	count  int // the messages the chat had at the last frame: a new one scrolls to it
	key    state.Peek
	close  icon.ID
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
	v.split = e.K.AddIcon(ztheme.ToolIcon("rectangle.split.2x1", '◫'))
	v.float = e.K.AddIcon(ztheme.ToolIcon("pip", '▣'))
}

// Build builds the panel while a peek is open.
func (s *Screen) Build(e *screen.Env) {
	st := e.S
	p := st.Peek
	a, ch, ok := s.v.start(st, p)
	if !ok {
		if p.Open() {
			st.ClosePeek()
		}
		return
	}
	c, k := e.C(), e.K
	r := k.Theme()
	w, h := c.Size()
	W, H := float32(w), float32(h)

	// A modal over the whole window: the window under it is inert, and the
	// composer's native text view, which lies above everything drawn, hides
	// under it (ui.HostRegions). A click outside, or Escape, closes it.
	t := c.AnimateValueFrom(ui.HashString(0, "peek in"), 0, 1, ztheme.Enter(r, ztheme.T.DurShort))
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleGroup)
	if c.BeginOverlay(ui.HashString(0, "peek"), ui.OverlayModal, [4]float32{0, 0, W, H}, ui.PlaceCenter).Dismissed {
		c.EndOverlay()
		st.ClosePeek()
		return
	}
	defer c.EndOverlay()
	theme.At(c, 0, 0, W, H)
	c.NextBG(theme.WithAlpha(0x000000FF, ztheme.T.ScrimDim*t))
	if c.Signal(c.Box(base.Floating|ui.Clickable|ui.DrawBackground, "peek scrim")).Clicked {
		st.ClosePeek()
		return
	}

	// beside what it came from, inside the window; it comes from there
	// (motion.go: Short, from its place)
	pw := min(e.Pt(panelW), W-e.Pt(ztheme.Space8))
	ph := min(e.Pt(panelMax), H-e.Pt(2*ztheme.Space8))
	x := p.From[2] + e.Pt(ztheme.Space2)
	if x+pw > W-e.Pt(ztheme.Space4) {
		x = max(p.From[0]-pw-e.Pt(ztheme.Space2), e.Pt(ztheme.Space4))
	}
	y := min(max(p.From[1]-e.Pt(ztheme.Space6), e.Pt(ztheme.Space8)), H-ph-e.Pt(ztheme.Space4))
	x -= (1 - t) * e.Pt(ztheme.Space2)
	c.NextOpacity(t)
	theme.At(c, x, y, pw, ph)
	c.NextAxis(ui.AxisY)
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextRadius(e.Pt(ztheme.WindowRadius))
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextShadow(ui.Shadow{OffY: e.Pt(12), Sigma: e.Pt(24), Color: 0x00000040})
	c.NextRole(ui.RoleDialog)
	c.NextSemName(i18n.Format(i18n.T("Peek: {chat}"), "{chat}", model.S(chatview.Title(a, ch))))
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.Clickable, "peek"))

	did := s.v.build(e, a, ch, pw, false)
	c.Pop()
	if c.TakeKey(ui.KeyEscape, 0) {
		did = actClose
	}
	switch did {
	case actClose:
		st.ClosePeek()
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
	// the line under the head
	c.NextSize(ui.Pct(1), ui.Px(k.Theme().Lines.Hairline))
	c.NextBG(k.Theme().Colors[theme.Separator])
	c.Box(ui.DrawBackground, "head line")
	return did
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
	v.age, v.count = 0, 0
	v.key = state.Peek{}
}
