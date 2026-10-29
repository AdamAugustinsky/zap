// Package chatview is a chat's light pane, bound to the chat it is given
// rather than to the account's open one: its transcript, as quiet bubbles
// with their day, sender, quote and time, and a composer of its own (the
// native text view, its draft the chat's). The chat floating over the
// desktop (Pip) is built of it; the peek's bubbles are its Bubble. It has
// none of the full conversation's menus, selection, search or players:
// media shows as what it is. (The split view's panes are the whole
// conversation: ui/shell/split.go.)
package chatview

import (
	"strings"
	"time"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// maxText is the most bytes a message's text takes, as the main composer's.
const maxText = 65536

// shownMax is the most messages a pane lays out, the newest.
const shownMax = 400

// View is one light pane's state: its chat, its scroll and its composer.
type View struct {
	name   string // its boxes' identity: two panes of one window differ
	comp   *rexui.ComposerState
	scroll base.ScrollState

	acct *state.Account
	ref  model.ChatRef
	chat []byte // the bound chat's ID: its draft

	age        int     // frames since the chat was bound: the scroll to its end
	count      int     // messages last frame: a new one at the foot follows
	first      []byte  // the oldest message last frame: older ones above keep the place
	fromBottom float32 // how far above its foot the reader was last frame, pixels
	keep       int     // frames to hold that place while older messages come in
	day        []byte  // a day's label, reused
	label      string  // the composer's: "Reply to" the chat, its identity the pane's
}

// New is a pane whose boxes are named name ("split", "pip").
func New(name string) *View { return &View{name: name} }

// Init makes the composer in the window (a native text view; the drawn
// field offscreen), at the window's Init.
func (v *View) Init(e *screen.Env) error {
	var w *platform.Window
	if e.A != nil {
		w = e.A.Win
	}
	st, err := rexui.NewComposer(w, rexui.ComposerSetup{Placeholder: i18n.T("Type a message"), MaxLen: maxText, Enter: enterPolicy(e.S)})
	v.comp = st
	if v.acct != nil && len(v.chat) > 0 {
		_ = st.SetText(v.acct.Model.Draft(v.chat))
	}
	return err
}

// Release keeps the draft and frees the composer's view: its window closed.
func (v *View) Release() {
	v.Unbind()
	if v.comp != nil {
		v.comp.Release()
		v.comp = nil
	}
}

func enterPolicy(st *state.State) platform.EnterPolicy {
	if st.Settings.EnterSends {
		return platform.EnterSends
	}
	return platform.CmdEnterSends
}

// Bind makes ch of a the pane's chat: the draft of the chat it had is kept,
// and ch's comes into the composer.
func (v *View) Bind(a *state.Account, ch model.Chat) {
	if v.acct == a && v.ref == ch.Ref() {
		return
	}
	v.Unbind()
	v.acct, v.ref = a, ch.Ref()
	v.chat = append(v.chat[:0], ch.ID()...)
	v.label = i18n.Format(i18n.T("Reply to {chat}"), "{chat}", model.S(Title(a, ch))) + "###" + v.name + " composer"
	v.age, v.count, v.keep, v.fromBottom = 0, 0, 0, 0
	v.first = v.first[:0]
	v.scroll = base.ScrollState{}
	if v.comp != nil {
		_ = v.comp.SetText(a.Model.Draft(ch.ID()))
	}
}

// Unbind leaves the chat: its draft goes to the archive, where the main
// composer and a restart find it.
func (v *View) Unbind() {
	a := v.acct
	if a == nil || len(v.chat) == 0 {
		v.acct, v.ref = nil, 0
		return
	}
	var text []byte
	if v.comp != nil {
		text = v.comp.Text()
	}
	if !hasContent(text) {
		text = nil
	}
	a.Model.SetDraft(v.chat, text)
	cmd := a.Cmd(abi.CommandTagSaveDraft)
	cmd.SaveDraft.Chat, cmd.SaveDraft.Text = v.chat, text
	a.Send(cmd)
	v.acct, v.ref = nil, 0
	v.chat = v.chat[:0]
}

// Chat is the bound chat's account and ref.
func (v *View) Chat() (*state.Account, model.ChatRef) { return v.acct, v.ref }

// Transcript builds the chat's messages in a scroll view that takes the
// height the parent leaves, w pixels wide.
func (v *View) Transcript(e *screen.Env, a *state.Account, ch model.Chat, w float32) {
	k := e.K
	c := k.Context()
	r := k.Theme()
	v.age++
	cv := a.Model.Conversation(ch.ID())
	k.NextHeight(ui.Grow(1))
	k.NextWidth(ui.Pct(1))
	sv := k.BeginScroll(v.name+" messages", &v.scroll, base.ScrollVertical)
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(ztheme.T.RowGap))
	pad := e.Pt(ztheme.Space4)
	c.NextPadding(pad, e.Pt(ztheme.Space3), pad, e.Pt(ztheme.Space3))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "list"))
	n := 0
	if cv == nil || cv.Len() == 0 {
		rexui.Label(k.Kit, i18n.T("Loading…"), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	} else {
		n = cv.Len()
		if cv.LoadingOlder() {
			rexui.Label(k.Kit, i18n.T("Loading…"), rexui.LabelOpts{Color: rexui.Of(theme.Label2), Style: rexui.TextCaption})
		}
		maxW := (w - 2*pad) * 0.78
		from := max(n-shownMax, 0)
		var prev model.Message
		now := e.S.Now()
		for i := from; i < n; i++ {
			m := cv.At(i)
			if m.Kind() == abi.ContentTagCall {
				continue
			}
			if i == from || format.DaysSince(time.Unix(prev.Time(), 0), time.Unix(m.Time(), 0)) != 0 {
				v.dayChip(e, m.Time(), now, i)
			}
			run := i > from && prev.FromMe() == m.FromMe() && string(prev.Sender()) == string(m.Sender())
			if !run && i > from {
				c.NextSize(ui.Px(1), ui.Px(e.Pt(max(ztheme.T.RunGap-ztheme.T.RowGap, 0))))
				c.Box(0, c.Fmt().Str("gap ").Int(i).String())
			}
			Bubble(e, a, ch, m, i, !run, maxW, r)
			prev = m
		}
	}
	c.Pop()
	k.CloseScrollContent(&sv)
	v.place(e, a, ch, cv, sv, n)
	rexui.ScrollBars(k.Kit, &sv)
	k.CloseScroll(&sv)
}

// place keeps the scroll where the reader wants it: at the foot when the
// chat opens and when a message comes in while they are there (their own
// always), where they were when older messages come in above, and it asks
// for older ones when they reach the top.
func (v *View) place(e *screen.Env, a *state.Account, ch model.Chat, cv *model.Conversation, sv base.ScrollView, n int) {
	c := e.C()
	content, view := c.ScrollExtent(sv.Inner)
	_, y := c.Scroll(sv.Inner)
	var oldest []byte
	newMine := false
	if cv != nil && n > 0 {
		oldest = cv.At(0).ID()
		newMine = n > v.count && cv.At(n-1).FromMe()
	}
	switch {
	case v.age <= 3:
		c.SetScroll(sv.Inner, 0, 1e9)
	case len(v.first) > 0 && string(oldest) != string(v.first) && n > v.count:
		v.keep = 3 // older messages came in above
	case n != v.count && (v.fromBottom < e.Pt(48) || newMine):
		c.SetScroll(sv.Inner, 0, 1e9)
	}
	if v.keep > 0 {
		v.keep--
		c.SetScroll(sv.Inner, 0, float64(max(content[1]-view[1]-v.fromBottom, 0)))
	} else {
		v.fromBottom = max(content[1]-view[1]-float32(y), 0)
	}
	if v.age > 3 && v.keep == 0 && y < float64(e.Pt(24)) && content[1] > view[1] && cv != nil && !cv.LoadingOlder() {
		a.LoadOlderIn(ch.ID(), true)
	}
	v.count = n
	v.first = append(v.first[:0], oldest...)
}

// dayChip is the day a run of messages was sent on: the transcript's pill.
func (v *View) dayChip(e *screen.Env, ts int64, now time.Time, i int) {
	k := e.K
	c := k.Context()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, c.Fmt().Str("day ").Int(i).String()))
	v.day = format.AppendDayLabel(v.day[:0], i18n.Current(), time.Unix(ts, 0), now.Local())
	rexui.DateDivider(k.Kit, string(v.day), rexui.DateDividerOpts{Pill: true})
	c.Pop()
}

// Composer builds the line to write in at the pane's foot; Enter (or the
// send button) sends to the bound chat. It reports whether it has the
// keyboard.
func (v *View) Composer(e *screen.Env, a *state.Account, ch model.Chat) bool {
	k := e.K
	c := k.Context()
	if v.comp == nil {
		return false
	}
	c.NextSize(ui.Pct(1), ui.Px(k.Theme().Lines.Hairline))
	c.NextBG(k.Theme().Colors[theme.Separator])
	c.Box(ui.DrawBackground, "composer line")
	pad := e.Pt(ztheme.Space3)
	c.NextPadding(pad, e.Pt(ztheme.Space2), pad, pad)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "compose"))
	k.NextWidth(ui.Pct(1))
	if why := noSend(ch); why != "" {
		rexui.Label(k.Kit, why, rexui.LabelOpts{Color: rexui.Of(theme.Label2), Style: rexui.TextCaption})
		c.Pop()
		return false
	}
	sig := rexui.Composer(k.Kit, v.label, v.comp, rexui.ComposerOpts{})
	if sig.Changed && !sig.Composing {
		text := v.comp.Text()
		if hasContent(text) {
			a.Model.SetDraft(ch.ID(), text)
		} else if len(a.Model.Draft(ch.ID())) > 0 {
			a.Model.SetDraft(ch.ID(), nil)
		}
	}
	if sig.Submitted {
		if text := v.comp.Text(); hasContent(text) {
			if err := a.SendText(ch.Ref(), text); err == nil {
				_ = v.comp.SetText(nil)
				a.Model.SetDraft(ch.ID(), nil)
			}
		}
	}
	c.Pop()
	return sig.Focused
}

// Focus gives the composer the keyboard.
func (v *View) Focus(e *screen.Env) {
	if v.comp != nil {
		v.comp.Focus(e.K.Kit)
	}
}

// noSend is why nothing can be sent in ch, as the main composer says it;
// "" when something can.
func noSend(ch model.Chat) string {
	switch {
	case ch.Kind() == abi.ChatKindBroadcast && ch.Left():
		return i18n.T("You left this channel")
	case ch.Kind() == abi.ChatKindBroadcast:
		return i18n.T("Channels are read-only in Zap")
	case ch.Locked():
		return i18n.T("Locked chats are read-only in Zap")
	case ch.Left():
		return i18n.T("You left this group")
	case ch.ReadOnly():
		return strings.Replace(i18n.T("Only {admins} can send messages"), "{admins}", i18n.T("admins"), 1)
	}
	return ""
}

func hasContent(b []byte) bool {
	for _, x := range b {
		if x != ' ' && x != '\n' && x != '\t' && x != '\r' {
			return true
		}
	}
	return false
}

// Bubble is message m of chat ch, the i-th of its conversation: its sender
// (in a group, first of a run), the message it quotes, its text or what it
// is, and its time; at most maxW pixels wide.
func Bubble(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, i int, first bool, maxW float32, r *theme.Resolved) {
	k := e.K
	c := k.Context()
	mine := m.FromMe()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	if mine {
		c.NextJustify(ui.JustifyEnd)
	}
	c.Push(c.Box(0, c.Fmt().Str("row ").Int(i).String()))
	fill := rexui.BubbleLook.Incoming.RGBA(r)
	if mine {
		fill = rexui.BubbleLook.Outgoing.RGBA(r)
	}
	c.NextBG(fill)
	c.NextRadius(e.Pt(ztheme.T.BubbleRadius))
	c.NextPadding(e.Pt(ztheme.T.BubblePadX), e.Pt(ztheme.T.BubblePadY), e.Pt(ztheme.T.BubblePadX), e.Pt(ztheme.T.BubblePadY))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(ztheme.T.NameGap))
	c.NextSize(ui.Children(1), ui.Children(1))
	c.Push(c.Box(ui.DrawBackground, "bubble"))
	if first && ch.Group() && !mine {
		who := m.SenderName()
		if len(who) == 0 {
			who = a.Model.DisplayName(m.Sender())
		}
		rexui.Label(k.Kit, model.S(who), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Label2)})
	}
	text := model.S(m.Text())
	if what := KindLabel(m.Kind()); what != "" {
		if text != "" {
			text = what + " · " + text
		} else {
			text = what
		}
	}
	textW := min(maxW, k.TextWidth(r.Styles[theme.StyleBody].Font, text)+e.Pt(2))
	if _, sender, summary, ok := m.Quote(); ok {
		textW = max(textW, quote(e, a, sender, summary, maxW, r))
	}
	k.NextWidth(ui.Px(textW))
	rexui.Label(k.Kit, text, rexui.LabelOpts{Wrap: true})
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyEnd)
	c.Push(c.Box(0, "foot"))
	rexui.Label(k.Kit, Stamp(m.Time(), e.S.Now()), rexui.LabelOpts{Color: rexui.Of(theme.Label2), MetaPt: ztheme.T.BubbleTimePt})
	c.Pop()
	c.Pop()
	c.Pop()
}

// quote is the message a bubble answers: a bar, who wrote it and a line of
// it, in a nested plate. It returns its width.
func quote(e *screen.Env, a *state.Account, sender, summary []byte, maxW float32, r *theme.Resolved) float32 {
	k := e.K
	c := k.Context()
	who := i18n.T("You")
	if len(sender) > 0 && string(sender) != string(a.Model.Me.ID()) {
		who = model.S(a.Model.DisplayName(sender))
	}
	line := model.S(summary)
	f := r.Styles[theme.StyleBody].Font
	inner := e.Pt(ztheme.Space2)
	w := min(maxW, max(k.TextWidth(f, who), k.TextWidth(f, line))+2*inner+e.Pt(ztheme.Space1))
	c.NextBG(theme.WithAlpha(r.Colors[theme.Label], 0.06))
	c.NextRadius(e.Pt(ztheme.Nested()))
	c.NextPadding(inner, e.Pt(ztheme.Space1), inner, e.Pt(ztheme.Space1))
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Px(w), ui.Children(1))
	c.Push(c.Box(ui.DrawBackground, "quote"))
	k.NextWidth(ui.Pct(1))
	rexui.Label(k.Kit, who, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Accent)})
	k.NextWidth(ui.Pct(1))
	rexui.Label(k.Kit, line, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	c.Pop()
	return w
}

// KindLabel is what a message is when it is not only text.
func KindLabel(k abi.ContentTag) string {
	switch k {
	case abi.ContentTagImage:
		return i18n.T("Photo")
	case abi.ContentTagVideo:
		return i18n.T("Video")
	case abi.ContentTagAudio:
		return i18n.T("Voice message")
	case abi.ContentTagDocument:
		return i18n.T("Document")
	case abi.ContentTagSticker:
		return i18n.T("Sticker")
	case abi.ContentTagLocation, abi.ContentTagLiveLocation:
		return i18n.T("Location")
	case abi.ContentTagContact:
		return i18n.T("Contact")
	case abi.ContentTagPoll:
		return i18n.T("Poll")
	case abi.ContentTagRevoked:
		return i18n.T("This message was deleted")
	}
	return ""
}

// Stamp is a message's time: the hour today, Yesterday, then the date.
func Stamp(ts int64, now time.Time) string {
	t := time.Unix(ts, 0)
	switch format.DaysSince(t, now) {
	case 0:
		return string(format.AppendTimeOfDay(nil, t.Hour(), t.Minute(), format.Hour12()))
	case 1:
		return i18n.T("Yesterday")
	}
	return t.Format("02/01/06")
}

// Title is a chat's name as the list writes it.
func Title(a *state.Account, ch model.Chat) []byte {
	if n := ch.Name(); len(n) > 0 {
		return n
	}
	return a.Model.DisplayName(ch.ID())
}
