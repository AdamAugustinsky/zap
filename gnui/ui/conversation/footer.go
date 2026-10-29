package conversation

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/text"
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

// The footer: a bubble's bottom line (zf:src/ui/conversation.rs footer):
// the star, "Not sent" on a failed message of ours, "edited", the pin, the
// time and our ticks; a click on our time opens Message info.

const (
	// notSent explains a failed message, on hover and to VoiceOver
	// (NOT_SENT_HINT; upstream does not translate it, here both are).
	notSent     = "Not sent"
	notSentHint = "This message could not be sent, and Zap will not retry it. Send it again yourself."
)

func notSentText() string     { return i18n.T(notSent) }
func notSentHintText() string { return i18n.T(notSentHint) }

// footWhere is where a footer goes.
type footWhere uint8

const (
	footInline footWhere = iota // the bubble's last line, at its right
	footLine                    // the right of a line of the block (a voice note's duration)
	footOver                    // on a scrim over a picture's bottom corner
)

// footerMarks are what a footer shows of m.
type footerMarks struct {
	starred, pinned, edited, failed bool
	stamp                           []byte
	tick                            tickMark
	read                            bool
}

// tickMark is our message's delivery mark in its footer.
type tickMark uint8

const (
	tickNone tickMark = iota
	tickPending
	tickFailed
	tickSent
	tickBoth // delivered, read or played: two checks
)

func tickOf(d abi.Delivery) tickMark {
	switch d {
	case abi.DeliveryPending:
		return tickPending
	case abi.DeliveryFailed:
		return tickFailed
	case abi.DeliveryDelivered, abi.DeliveryRead, abi.DeliveryPlayed:
		return tickBoth
	}
	return tickSent
}

// tickStep is how far right of the first check the second of a pair sits.
func tickStep(e *screen.Env) float32 { return theme.Snap(e.Pt(4)) }

// tickIcons are a kit's icons for the delivery marks: the chat list's
// (chatlist/rows.go), a check, and a pair of them one overlapping the other.
type tickIcons struct{ tick, wait icon.ID }

func addTickIcons(k interface{ AddIcon(icon.Spec) icon.ID }) tickIcons {
	return tickIcons{
		tick: k.AddIcon(icon.Spec{Symbol: "checkmark", Fallback: '✓', Pt: 11, Weight: 0.3}),
		wait: k.AddIcon(icon.Spec{Symbol: "clock", Fallback: '◷', Pt: 11}),
	}
}

// width is the width of mark t as drawn; a failed message's "!" is in font.
func (ic tickIcons) width(e *screen.Env, font text.FontKey, t tickMark) float32 {
	k := e.K.Kit
	switch t {
	case tickNone:
		return 0
	case tickFailed:
		return k.TextWidthBytes(font, base.StrBytes("!"))
	case tickPending:
		w, _ := k.IconSize(ic.wait)
		return w
	}
	w, _ := k.IconSize(ic.tick)
	if t == tickBoth {
		w += tickStep(e)
	}
	return w
}

// draw draws mark t in the row being built: icons rather than text checks,
// whose pair the font sets apart.
func (ic tickIcons) draw(e *screen.Env, font text.FontKey, t tickMark, ink uint32, key ui.Key) {
	c, k := e.C(), e.K.Kit
	if t == tickFailed {
		c.NextFont(font)
		c.NextTextColor(ink)
		c.NextSize(ui.TextSize(0), ui.TextSize(0))
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 20), base.StrBytes("!"))
		return
	}
	id := ic.tick
	if t == tickPending {
		id = ic.wait
	}
	iw, ih := k.IconSize(id)
	c.NextSize(ui.Px(ic.width(e, font, t)), ui.Px(ih))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 21), nil))
	one := func(n uint64, x float32) {
		theme.At(c, x, 0, iw, ih)
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(key, 22+n), nil))
		k.Icon(id, ink)
		c.Pop()
	}
	one(0, 0)
	if t == tickBoth {
		one(1, tickStep(e))
	}
	c.Pop()
}

func (s *Screen) marks(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out bool) footerMarks {
	f := footerMarks{
		starred: a.Model.Starred(ch.ID(), m.ID()),
		pinned:  a.Model.PinnedMessage(ch.ID(), m.ID()),
		edited:  m.Edited(),
		failed:  out && m.Status() == abi.DeliveryFailed,
		stamp:   clock(e.C().Fmt(), m.Time()).Bytes(),
	}
	if out {
		f.tick = tickOf(m.Status())
		// display_delivery: privacy masks only Read on outgoing DMs, not
		// delivery, group reads or played receipts. The archive stays intact.
		f.read = m.Status() == abi.DeliveryPlayed || m.Status() == abi.DeliveryRead &&
			!(ch.Kind() == abi.ChatKindDirect && a.Model.ReceiptsOff())
	}
	return f
}

// width is the footer's width in pixels.
func (s *Screen) footWidth(e *screen.Env, f *footerMarks, over bool) float32 {
	k := e.K.Kit
	font, gap := s.look.small, e.Pt(4)
	w := k.TextWidthBytes(s.look.time, f.stamp)
	if f.starred {
		w += k.TextWidthBytes(font, base.StrBytes("★")) + gap
	}
	if f.pinned {
		w += e.Pt(footMarkPt) + gap
	}
	if f.edited && !over {
		w += k.TextWidthBytes(font, base.StrBytes(i18n.T("edited"))) + gap
	}
	if f.failed && !over {
		w += k.TextWidthBytes(s.look.smallMed, base.StrBytes(notSentText())) + gap
	}
	if f.tick != tickNone {
		w += s.icons.ticks.width(e, font, f.tick) + gap
	}
	return theme.Snap(w) + 1
}

const footMarkPt = 10 // FOOTER_MARK: the pin's size

// A bubble's time (ztheme.T.BubbleTimePt) is smaller than the list's
// (MetaPt), as the text it ends is the message's, not a row's.

// footer builds m's footer in the bubble's flow (footInline), as wide as it
// is so a short text's bubble makes room for it.
func (s *Screen) footer(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out bool, key ui.Key) {
	c := e.C()
	f := s.marks(e, a, ch, m, out)
	c.NextSize(ui.Px(s.footWidth(e, &f, false)), ui.Px(0))
	c.Box(0, "footer width")
	c.NextSize(ui.Pct(1), ui.Children(1))
	s.footRow(e, m, &f, out, key, footInline)
}

// footerAt builds m's footer at the right of the line {0, y, w, h} of the
// block being built (a voice note's duration line), if it fits beside left
// pixels of the line's own text; it reports whether it did.
func (s *Screen) footerAt(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out bool, key ui.Key, left, y, w, h float32) bool {
	f := s.marks(e, a, ch, m, out)
	fw := s.footWidth(e, &f, false)
	if left+e.Pt(8)+fw > w {
		return false
	}
	theme.At(e.C(), w-fw, y, fw, h)
	s.footRow(e, m, &f, out, key, footLine)
	return true
}

// footerOver is the footer of a picture without a caption: the star, the
// time and our ticks in white on a dark scrim over its bottom corner
// (zf:src/ui/conversation.rs footer_over_picture).
func (s *Screen) footerOver(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out bool, key ui.Key, w, h float32) {
	c := e.C()
	f := s.marks(e, a, ch, m, out)
	rowW := s.footWidth(e, &f, true)
	padX, padY, lineH := e.Pt(6), e.Pt(2), e.Pt(15)
	bw, bh := min(rowW+2*padX, w), min(lineH+2*padY, h)
	x := max(w-e.Pt(overInsetX)-rowW-padX, 0)
	y := max(h-e.Pt(overInsetY)-lineH-padY, 0)
	theme.At(c, x, y, bw, bh)
	c.NextBG(scrim)
	c.NextRadius(bh / 2)
	s.footRow(e, m, &f, out, key, footOver)
}

// footRow builds the footer's row (its size and place already set) and
// handles a click on it: secondary text on either side's bubble, white on
// a picture's scrim.
func (s *Screen) footRow(e *screen.Env, m model.Message, f *footerMarks, out bool, key ui.Key, where footWhere) {
	c, k := e.C(), e.K.Kit
	col := k.Theme().Colors[theme.Label2]
	if where == footOver {
		col = 0xFFFFFFFF
	}
	font, gap := s.look.small, e.Pt(4)
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyEnd)
	if where == footOver {
		c.NextJustify(ui.JustifyCenter)
	}
	c.NextCross(ui.CrossCenter)
	if where == footInline {
		c.NextCross(ui.CrossEnd) // its parts on one baseline, the time's
	}
	c.NextGap(gap)
	flags := ui.Flags(0)
	switch where {
	case footOver:
		flags = theme.Floating | ui.DrawBackground
	case footLine:
		flags = theme.Floating
	}
	info := out && !f.failed && m.Status() != abi.DeliveryPending
	if info {
		flags |= ui.Clickable
		c.NextRole(ui.RoleButton)
		c.NextSemName(i18n.T("Message info"))
		c.NextCursor(ui.CursorPointer)
	}
	row := c.BoxKeyed(flags, ui.KeyN(key, 7), nil)
	c.Push(row)
	small := func(n uint64, b []byte, fk text.FontKey, cl uint32) {
		c.NextFont(fk)
		c.NextTextColor(cl)
		c.NextSize(ui.TextSize(0), ui.TextSize(0))
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 8+n), b)
	}
	if f.starred {
		small(0, base.StrBytes("★"), font, footerStar)
	}
	if f.failed && where != footOver {
		// the word and its explanation: the red mark alone does not say
		// what went wrong or what to do
		c.NextFont(s.look.smallMed)
		c.NextTextColor(col | 0xFF)
		c.NextSize(ui.TextSize(0), ui.TextSize(0))
		c.NextRole(ui.RoleLabel)
		c.NextSemName(notSentHintText())
		id := c.BoxKeyed(ui.DrawText|ui.Hoverable, ui.KeyN(key, 14), base.StrBytes(notSentText()))
		rexui.Tooltip(k, id, notSentHintText())
	}
	if f.edited && where != footOver {
		small(1, base.StrBytes(i18n.T("edited")), font, col)
	}
	if f.pinned {
		c.NextSize(ui.Px(e.Pt(footMarkPt)), ui.Px(e.Pt(footMarkPt)))
		k.Icon(s.icons.pinSmall, col)
	}
	small(2, f.stamp, s.look.time, col)
	if f.tick != tickNone {
		ink := col
		if f.read {
			ink = ztheme.OnBubble(out).Read
		}
		s.icons.ticks.draw(e, font, f.tick, ink, key)
	}
	c.Pop()
	if info && c.Signal(row).Clicked && !e.S.Panes.Selecting {
		s.messageInfo(e, m)
	}
}

// footerStar is FOOTER_STAR, the star's gold.
const footerStar = uint32(0xF5B400FF)
