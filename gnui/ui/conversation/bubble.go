package conversation

import (
	"bytes"
	"math"

	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/markup"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/conversation/av"
	"zapfast/ui/conversation/kinds"
	"zapfast/ui/screen"
)

// sameRun reports whether b continues a's run of bubbles: the same sender,
// the same day.
func sameRun(a, b model.Message) bool {
	if a.FromMe() != b.FromMe() || dayOf(a.Time()) != dayOf(b.Time()) {
		return false
	}
	return a.FromMe() || sameID(a.Sender(), b.Sender())
}

// sender is who sent m: address book first, with the message's profile
// name only a hint, as upstream's display_name_or (nil for our own). It is
// valid until the next name.
func (s *Screen) sender(a *state.Account, m model.Message) []byte {
	if m.FromMe() {
		return nil
	}
	return s.personName(a, m.Sender(), m.SenderName())
}

// personName is a.Model.PersonName in s's scratch, valid until the next.
func (s *Screen) personName(a *state.Account, jid, hint []byte) []byte {
	s.name = a.Model.AppendPersonName(s.name[:0], jid, hint)
	return s.name
}

func (s *Screen) senderStr(a *state.Account, m model.Message) string {
	if m.FromMe() {
		return i18n.T("You")
	}
	return model.S(s.sender(a, m))
}

// hasText reports whether m's text is a body the bubble shows (a text, or
// the caption of a picture, a video or a document); other kinds' text is
// their block's.
func hasText(m model.Message) bool {
	switch m.Kind() {
	case abi.ContentTagText, abi.ContentTagInteractive, abi.ContentTagImage, abi.ContentTagVideo,
		abi.ContentTagDocument, abi.ContentTagStickerPack:
		return len(m.Text()) > 0
	}
	return false
}

// shown fills s.body and s.rich with m's text as the bubble shows it
// (markup parsed, mentions named) and s.spans with its spans.
func (s *Screen) shown(a *state.Account, m model.Message) {
	text := m.Text()
	s.ments, s.mentID = s.ments[:0], s.mentID[:0]
	if hasAt(text) && m.Decode(&s.dec) == nil {
		for i := range s.dec.Mentions {
			mr := &s.dec.Mentions[i]
			s.ments = append(s.ments, markup.Mention{User: mr.User, Name: a.Model.DisplayName(mr.ID)})
			s.mentID = append(s.mentID, mr.ID)
		}
	}
	s.spans = markup.ParseMentions(s.spans[:0], text, s.ments)
	s.body, s.rich = markup.ToRich(s.body[:0], s.rich[:0], text, s.spans, s.ments)
}

// shownForBubble reuses only the current bubble's scratch. Copy paths use
// shown directly, since their mention names may have changed since drawing.
func (s *Screen) shownForBubble(a *state.Account, m model.Message) {
	if s.shownReady && s.shownMessage == m {
		return
	}
	s.shown(a, m)
	s.shownMessage, s.shownReady = m, true
}

func hasAt(b []byte) bool {
	for _, c := range b {
		if c == '@' {
			return true
		}
	}
	return false
}

// message builds message mi of v (item id): the dividers above it, its
// bubble, its reactions, and the typing bubble after the newest.
func (s *Screen) message(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation, mi int, id uint64) {
	s.shownReady = false
	c, k := e.C(), e.K.Kit
	m := v.At(mi)
	n := v.Len()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	bottom := float32(0)
	if mi == n-1 {
		bottom = e.Pt(endPt)
	}
	c.NextPadding(e.Pt(sidePt), 0, e.Pt(sidePt), bottom)
	c.Push(c.Box(0, "m"))
	if mi == 0 {
		s.topOfHistory(e, v)
	}
	// a message being deleted for us is hidden until the core confirms
	// (Panes.PendingDelete), and an album's items after its first are drawn
	// in the first's grid; the neighbours run on past them
	if s.skip(e, v, mi) {
		if mi == n-1 {
			s.typing(e, a, ch)
		}
		c.Pop()
		return
	}
	var prev model.Message
	pi := s.shownNext(e, v, mi, -1)
	newDay := pi < 0
	if pi >= 0 {
		prev = v.At(pi)
		newDay = dayOf(prev.Time()) != dayOf(m.Time())
	}
	if newDay {
		rexui.DateDivider(k, dayLabel(c.Fmt(), m.Time(), s.now).String(), rexui.DateDividerOpts{Pill: true})
	}
	// the pins made since the message before (above the oldest loaded one
	// only once the history is all here)
	if s.noteN > 0 {
		from := int64(math.MinInt64)
		switch {
		case pi >= 0:
			from = prev.Time()
		case !v.Complete():
			from = m.Time()
		}
		s.notices(e, a, ch, from, m.Time())
	}
	divider := id == s.unreadAt && s.unreadAt != 0
	if divider {
		f := fillInt(c.Fmt(), i18n.N("{} unread message", "{} unread messages", s.unread), "{}", s.unread)
		rexui.DateDivider(k, f.String(), rexui.DateDividerOpts{})
	}
	first := pi < 0 || newDay || divider || !sameRun(prev, m)
	ni := s.shownNext(e, v, mi, 1)
	last := ni < 0
	if !last {
		last = !sameRun(m, v.At(ni)) || (s.unreadAt != 0 && ni < len(s.t.ids) && s.t.ids[ni] == s.unreadAt)
	}
	s.albV, s.albI, s.albN = v, mi, s.albumLen(v, mi)
	s.bubble(e, a, ch, m, id, first, last)
	s.shownMessage, s.shownReady = model.Message{}, false
	s.albN = 0
	if ni < 0 && s.noteN > 0 {
		s.notices(e, a, ch, m.Time(), math.MaxInt64)
	}
	if mi == n-1 {
		s.typing(e, a, ch)
	}
	c.Pop()
}

// hidden reports whether m is being deleted for us (Panes.PendingDelete).
func (s *Screen) hidden(e *screen.Env, m model.Message) bool {
	pd := &e.S.Panes.PendingDelete
	return pd.Len() > 0 && pd.Has(m.ID())
}

// revoking reports whether m is being deleted for everyone
// (Panes.Revoking): it shows as deleted until the core's update.
func (s *Screen) revoking(e *screen.Env, m model.Message) bool {
	rv := &e.S.Panes.Revoking
	return rv.Len() > 0 && rv.Has(m.ID())
}

// skip reports whether message i of v has no row of its own: hidden, or
// in an album its first draws.
func (s *Screen) skip(e *screen.Env, v *model.Conversation, i int) bool {
	return s.hidden(e, v.At(i)) || s.albumMember(v, i)
}

// shownNext is the index of the message next to mi in direction step (-1:
// older, 1: newer) that has its own row, -1 for none.
func (s *Screen) shownNext(e *screen.Env, v *model.Conversation, mi, step int) int {
	for i := mi + step; i >= 0 && i < v.Len(); i += step {
		if !s.skip(e, v, i) {
			return i
		}
	}
	return -1
}

// topOfHistory is what sits above the oldest loaded message
// (zf:src/ui/conversation.rs top_of_history).
func (s *Screen) topOfHistory(e *screen.Env, v *model.Conversation) {
	c, k := e.C(), e.K.Kit
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	c.NextPadding(0, e.Pt(6), 0, e.Pt(6))
	c.Push(c.Box(0, "top"))
	switch {
	case v.LoadingOlder():
		rexui.Spinner(k, i18n.T("Loading…"), rexui.SpinnerOpts{Size: theme.SizeSmall})
	case phoneFetching(v):
		rexui.Spinner(k, i18n.T("Loading older messages from your phone…"), rexui.SpinnerOpts{Size: theme.SizeSmall})
		rexui.Label(k, i18n.T("Loading older messages from your phone…"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	default:
		c.NextSize(ui.Px(1), ui.Px(e.Pt(6)))
		c.Box(0, "gap")
	}
	c.Pop()
}

// typing is the bubble with the moving dots while someone types.
func (s *Screen) typing(e *screen.Env, a *state.Account, ch model.Chat) {
	if s.typers = a.Model.Typers(ch.Ref(), s.typers[:0]); len(s.typers) == 0 {
		return
	}
	c, k := e.C(), e.K.Kit
	name := s.typingLine(c.Fmt(), a, ch).String()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossEnd)
	c.NextGap(e.Pt(2))
	c.NextPadding(0, e.Pt(2), 0, e.Pt(2))
	c.Push(c.Box(0, "typing"))
	if ch.Group() {
		s.typingPics(e, a)
	}
	rexui.TypingIndicator(k, name, rexui.TypingOpts{})
	c.Pop()
}

// bubble builds one message's bubble and the reactions under it.
func (s *Screen) bubble(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, id uint64, first, last bool) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	out := m.FromMe()
	p := &e.S.Panes
	selecting := p.Selecting
	picked := selecting && p.Selection.Has(m.ID())

	if selecting {
		s.pickRow(e, a, ch, m, picked)
	}
	// a group's senders show their pictures (PR-008)
	pic := ch.Group() && !out
	if pic {
		s.senderColumn(e, a, ch, m, first)
	}
	o := rexui.BubbleOpts{Tail: last, ColumnW: s.colPt}
	if out {
		o.Variant = rexui.BubbleOutgoing
		switch m.Status() {
		case abi.DeliveryPending:
			o.Status = rexui.BubbleSending
		case abi.DeliveryFailed:
			o.Status = rexui.BubbleFailed
		}
	}
	o.Selected = picked || (s.menu.open && s.menu.msg.Len() > 0 && sameID(s.menu.msg.At(0), m.ID()))
	// the flash fades (state.FlashStrength): the bubble's fill moves toward
	// the find colour by its share of FindMix
	o.Highlighted = id == s.flash && s.flash != 0 && s.strength > 0
	o.FindMix = rexui.BubbleLook.FindMix * s.strength
	o.Name = clock(c.Fmt().Str(s.senderStr(a, m)).Str(", "), m.Time()).String()

	key := ui.KeyN(c.ParentKey(), 1)
	brect := c.RectOf(ui.KeyN(key, 1))
	s.reactBeside(e, ch, m, brect)
	if !selecting {
		s.rightClick(e, m, brect)
		if s.doubleBeside(e, c.RectOf(ui.KeyN(key, 0)), brect) && m.Kind() != abi.ContentTagRevoked && !s.revoking(e, m) {
			// a double click beside the bubble or on its edge replies; one
			// on its text selects a word (MM-005)
			pn := &e.S.Panes
			pn.Edit.Reset()
			pn.Reply.Reset()
			pn.Reply.Add(m.ID())
			e.S.Req.FocusComposer = true
			e.S.Touch()
		}
	} else if at, ok := rightAt(e, brect); ok && picked {
		// a selected message's menu is the selection's delete choices
		s.delMenu, s.delAt = true, at
	}
	note := av.NoBubble(m) // a round video note is its own circle
	bare := kinds.NoBubble(m) || note
	var b rexui.Bubble
	if bare {
		s.beginBare(e, key, o, m.Kind() != abi.ContentTagImage && !note)
	} else {
		b = rexui.BeginBubble(k, key, o)
	}
	own := bare // our boxes, not rexui's bubble
	if first && ch.Group() && !out {
		// the sender's name in their hue
		name := s.sender(a, m)
		st := &r.Styles[rexui.TextStyles[rexui.TextSemibold]]
		c.NextFont(st.Font)
		c.NextTextColor(ztheme.Sender(hueOf(m.Sender())))
		c.NextSize(ui.Px(min(theme.Snap(k.TextWidthBytes(st.Font, name))+base.LabelSlack, s.maxW)), ui.Px(st.Line))
		c.NextRole(ui.RoleLabel)
		c.NextSemNameBytes(name)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(key, 15), name)
	}
	if s.revoking(e, m) {
		// deleted for everyone: drawn as the core will have it
		rexui.Label(k, i18n.T("This message was deleted"), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		s.footer(e, a, ch, m, out, key)
		s.endBubble(e, own, &b)
		closeRows(c, pic, selecting)
		return
	}
	if m.Forwarded() {
		rexui.Label(k, i18n.T("Forwarded"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	}
	qid, qs, qsum, quoted := m.Quote()
	s.span = s.settle(e, a, ch, m, out, quoted)
	if quoted {
		s.quote(e, a, ch, m, qid, qs, qsum)
	}
	// The text goes where kinds says, relative to the block: above a poll
	// or a card, nowhere for a location or a contact (their block draws it),
	// below the rest. Content no block draws says what it is in a line.
	if s.albN > 1 {
		s.albumGrid(e, a, ch, s.albV, out, key)
		s.endBubble(e, own, &b)
		if m.Reactions() > 0 {
			s.reactions(e, a, ch, m, out)
		}
		closeRows(c, pic, selecting)
		return
	}
	isKinds, isAV := kinds.Has(m), av.Has(m)
	place := kinds.Below
	if isKinds {
		place = kinds.Caption(m)
	}
	text := place != kinds.Nowhere && len(m.Text()) > 0 && (isKinds || isAV || hasText(m))
	if text && place == kinds.Above {
		s.text(e, a, m, id, key, false)
	}
	over := false
	switch {
	case isKinds || isAV:
		over = s.block(e, a, ch, m, isKinds, out, key)
	case !plain(m):
		b := s.kindLine(c.Fmt(), m).Bytes()
		rexui.LabelBytes(k, b, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	}
	alone := !isKinds && !isAV && plain(m)
	switch {
	case text && place == kinds.Below && !over && s.inlineFoot(e, a, ch, m, out, alone):
		s.textAndFooter(e, a, ch, m, id, key, out, alone)
	default:
		if text && place == kinds.Below {
			s.text(e, a, m, id, key, alone)
		}
		if !over {
			s.footer(e, a, ch, m, out, key)
		}
	}
	s.endBubble(e, own, &b)
	if m.Reactions() > 0 {
		s.reactions(e, a, ch, m, out)
	}
	closeRows(c, pic, selecting)
	if !selecting {
		s.affordances(e, a, ch, m, c.ParentKey(), brect)
	}
}

// endBubble closes the bubble bubble() opened, own for one of ours
// (beginBare).
func (s *Screen) endBubble(e *screen.Env, own bool, b *rexui.Bubble) {
	c := e.C()
	if own {
		c.Pop() // the column
		c.Pop() // the row
	} else {
		rexui.EndBubble(e.K.Kit, b)
	}
}

// closeRows closes the rows round a bubble: the sender's picture's (pic)
// and selection mode's (picking).
func closeRows(c *ui.Context, pic, picking bool) {
	if pic {
		c.Pop() // the column
		c.Pop() // the sender row
	}
	if picking {
		c.Pop() // the column
		c.Pop() // the pick row
	}
}

// plain reports whether m is a text (with no block, it is its text alone).
func plain(m model.Message) bool {
	k := m.Kind()
	return k == abi.ContentTagText || k == abi.ContentTagInteractive
}

// text builds m's text in the bubble: markup parsed, links and mentions
// live, selectable as item id. A poll's question is semibold, as upstream's
// ballot draws it; a text alone of a few emojis is large.
func (s *Screen) text(e *screen.Env, a *state.Account, m model.Message, id uint64, key ui.Key, alone bool) {
	c := e.C()
	r := e.K.Kit.Theme()
	s.shownForBubble(a, m)
	ro := ui.RichOpts{MaxW: s.maxW, Item: id}
	if s.span > 0 {
		ro.MaxW = s.span
	}
	if alone && markup.Big(m.Text()) {
		ro.EmojiLarge, ro.JumboFont = true, s.look.jumbo
	}
	font := r.Styles[theme.StyleBody].Font
	if m.Kind() == abi.ContentTagPoll {
		font = r.Styles[rexui.TextStyles[rexui.TextSemibold]].Font
	}
	c.NextFont(font)
	c.NextTextColor(rexui.BubbleLook.TextIn.RGBA(r))
	sig := c.RichText(ui.KeyN(key, 3), s.body, s.rich, &s.look.rich, ro)
	var phoneAnchor [4]float32
	for i, sp := range s.rich {
		if sp.Tag == 0 || s.spans[sp.Tag-1].Link != markup.LinkTel {
			continue
		}
		// VList includes overscan: only the phone ranges actually in the
		// transcript join the primary cycle, in message/span order.
		s.phoneRects = c.RichRangeRects(sig.ID, int(sp.Off), int(sp.Off+sp.Len), s.phoneRects[:0])
		for _, rr := range s.phoneRects {
			if rr[0] < s.listR[2] && rr[2] > s.listR[0] && rr[1] < s.listR[3] && rr[3] > s.listR[1] {
				// Equal stops keep build order, even beyond the reserved range.
				e.TabStopKey(screen.StopPhoneLink+screen.TabStop(min(s.phoneStop, 99)), ui.RichLinkKey(c.KeyOf(sig.ID), i))
				s.phoneStop++
				if sig.ClickedSpan == i && !sig.Box.Clicked && c.FocusKey() == ui.RichLinkKey(c.KeyOf(sig.ID), i) {
					for n := 0; n < c.KeyCount(); n++ {
						key, _ := c.KeyAt(n) // RichText already claimed activation.
						if key.Down && !key.Repeat && (key.Code == ui.KeyEnter || key.Code == ui.KeySpace) {
							phoneAnchor = rr // upstream's Popup::menu anchors to the link
						}
					}
				}
				break
			}
		}
	}
	if sig.Clicked != 0 {
		s.follow(e, a, m, int(sig.Clicked)-1)
		if phoneAnchor[2] > phoneAnchor[0] {
			s.phone.at = phoneAnchor
		}
	}
}

// block builds m's content block from package kinds (isKinds) or av, in a
// box of the size it asks for. The block's rect is in that box's
// coordinates, {0, 0, w, h}: kinds and av place it with NextPos, relative to
// the box being built. A picture without a caption carries the footer over
// its corner (CV-021); block reports whether it did.
func (s *Screen) block(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, isKinds, out bool, key ui.Key) (over bool) {
	var w, h float32
	if isKinds {
		maxW := s.maxW
		if m.Kind() == abi.ContentTagInteractive && kinds.NoBubble(m) {
			// a carousel takes most of the column (zf:src/ui/conversation.rs
			// bubble_row: 0.95 of it, at most 920 points)
			maxW = max(min(e.Pt(s.colPt)*0.95, e.Pt(920))-2*e.Pt(rexui.BubbleLook.PadX), s.maxW)
		}
		w, h = kinds.SizeIn(e, m, maxW, s.span)
	} else {
		w, h = av.Size(e, m, s.maxW)
	}
	if w < 1 || h < 1 {
		return false
	}
	c := e.C()
	c.NextSize(ui.Px(w), ui.Px(h))
	c.Push(c.Box(0, "block"))
	rr := screen.Rect{W: w, H: h}
	if isKinds {
		kinds.Build(e, m, rr)
	} else {
		av.Build(e, m, rr)
	}
	over = isKinds && m.Kind() == abi.ContentTagImage && len(m.Text()) == 0
	if over {
		s.footerOver(e, a, ch, m, out, key, w, h)
	} else if left, y, lh, ok := av.FooterLine(e, m, rr); ok && len(m.Text()) == 0 {
		// a voice note's time and ticks share its duration's line
		over = s.footerAt(e, a, ch, m, out, key, left, y, w, lh)
	}
	c.Pop()
	return over
}

const (
	overInsetX, overInsetY = 10, 6              // OVER_PICTURE_INSET, points
	scrim                  = uint32(0x0000006E) // black at 110/255
)

// beginBare opens the bubble of a message drawn without one
// (kinds.NoBubble): the row and the column its content goes in, keyed as
// rexui's bubble is so its rect and accessible name are where a bubble's
// would be. pad keeps the bubble's padding (a sticker sits where a
// bubble's content would); a lone picture has none. The caller pops both.
func (s *Screen) beginBare(e *screen.Env, key ui.Key, o rexui.BubbleOpts, pad bool) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	rc := &rexui.BubbleLook
	just := ui.JustifyStart
	if o.Variant == rexui.BubbleOutgoing {
		just = ui.JustifyEnd
	}
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossEnd)
	c.NextJustify(just)
	c.NextGap(e.Pt(6))
	c.NextPadding(0, e.Pt(rc.RowGap/2), 0, e.Pt(rc.RowGap/2))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 0), nil))
	if o.Variant == rexui.BubbleOutgoing && o.Status == rexui.BubbleFailed {
		rexui.Badge(k, "!", rexui.BadgeOpts{Variant: rexui.BadgeCount, Name: i18n.T("Not sent")})
	}
	flags := ui.Flags(0)
	if o.Selected || o.Highlighted {
		c.NextRadius(e.Pt(rc.Radius))
	}
	if o.Selected {
		c.NextBorder(rc.Ring.RGBA(r), e.Pt(rc.RingW))
		c.NextSemState(ui.SemSelected)
		flags |= ui.DrawBorder
	}
	if o.Highlighted {
		// no fill to move toward the find colour: the flash alone
		c.NextBG(theme.WithAlpha(rc.Find.RGBA(r), rc.FindMix*s.strength))
		flags |= ui.DrawBackground
	}
	if pad {
		c.NextPadding(e.Pt(rc.PadX), e.Pt(rc.PadY), e.Pt(rc.PadX), e.Pt(rc.PadY))
	}
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(o.Name)
	c.Push(c.BoxKeyed(flags, ui.KeyN(key, 1), nil))
}

// follow opens span i of the message's text: a link in the browser, a
// mention's info.
func (s *Screen) follow(e *screen.Env, a *state.Account, m model.Message, i int) {
	if i < 0 || i >= len(s.spans) {
		return
	}
	sp := s.spans[i]
	if sp.Style&markup.Mentioned != 0 {
		if int(sp.Arg) < len(s.mentID) {
			e.S.Panes.SetInfo(s.mentID[sp.Arg])
			e.S.Touch()
		}
		return
	}
	if sp.Link == markup.LinkTel {
		// a number offers a chat with it or a copy as written (MK-005)
		in := e.C().Input()
		ph := &s.phone
		ph.open, ph.at = true, [4]float32{in.MouseX, in.MouseY, in.MouseX + 1, in.MouseY + 1}
		if t := m.Text(); int(sp.End) <= len(t) && sp.Start <= sp.End {
			ph.written = append(ph.written[:0], t[sp.Start:sp.End]...)
		}
		ph.digits = ph.digits[:0]
		for _, c := range ph.written {
			if c >= '0' && c <= '9' {
				ph.digits = append(ph.digits, c)
			}
		}
		s.menu.open, s.more = false, false
		return
	}
	s.link = markup.Target(s.link[:0], m.Text(), sp)
	if len(s.link) == 0 {
		return
	}
	kinds.OpenLink(e.S, a, string(s.link))
}

// phoneMenu is a number's menu in a message: Message (a chat with it) and
// Copy number, as written.
func (s *Screen) phoneMenu(e *screen.Env, a *state.Account) {
	k := e.K.Kit
	ph := &s.phone
	p := rexui.BeginPopover(k, "phone menu", ph.at, rexui.PopoverOpts{Width: 180, Label: model.S(ph.written)})
	done := false
	if item(k, s.icons.reply, i18n.T("Message")) && len(ph.digits) > 0 {
		s.jid = append(append(s.jid[:0], ph.digits...), "@s.whatsapp.net"...)
		a.StartChat(s.jid, nil, nil)
		done = true
	}
	if item(k, s.icons.copy, i18n.T("Copy number")) {
		s.copied = append(s.copied[:0], ph.written...)
		setClipboard(e, s.copied)
		done = true
	}
	rexui.EndPopover(k, &p)
	if done || p.Dismissed {
		ph.open = false
	}
}

// quote is the quoted message at the top of a reply: clicking it jumps to
// the original (CV-017).
func (s *Screen) quote(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, qid, qsender, qsum []byte) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	name := qsender
	// the quoted sender's JID: the model keeps their name when it came
	jid := qsender
	decoded := m.Decode(&s.dec) == nil && s.dec.HasQuoted
	if decoded && len(s.dec.Quoted.Sender) > 0 {
		jid = s.dec.Quoted.Sender
	}
	mine := len(jid) > 0 && (bytes.Equal(jid, a.Model.Me.ID()) || bytes.Equal(jid, a.Model.Me.LID()))
	switch {
	case mine:
		name = base.StrBytes(i18n.T("You"))
	case decoded:
		var hint []byte // the scratch keeps another message's name when this has none
		if s.dec.Quoted.HasSenderName {
			hint = s.dec.Quoted.SenderName
		}
		name = s.personName(a, jid, hint)
	case len(name) > 0 && isJID(name):
		name = a.Model.DisplayName(name)
	}
	// As in WhatsApp, the bar and the name take the quoted sender's colour,
	// the one their name has in groups, kept readable on the bubble's fill
	// as it shows over the pane (zf:src/ui/conversation.rs quote_colours).
	// Both sides' bubbles are quiet (Rex's tint on yours), so the rest of the
	// quote is drawn alike on both.
	hue := r.Colors[theme.Accent]
	if !mine {
		hue = ztheme.Sender(hueOf(jid))
	}
	fill := rexui.BubbleLook.Incoming.RGBA(r)
	if m.FromMe() {
		fill = rexui.BubbleLook.Outgoing.RGBA(r)
	}
	bar := readableOn(theme.Over(fill, r.Colors[theme.Pane]|0xFF), hue, rexui.BubbleLook.TextIn.RGBA(r), 3)
	nameCol, text := bar, r.Colors[theme.Label2]
	bg := theme.WithAlpha(r.Colors[theme.Label], 0.06)
	// It spans the bubble's settled width, its padding upstream's.
	c.NextSize(ui.Children(1), ui.Children(1))
	inner := s.maxW - e.Pt(quoteBarPt+17)
	if s.span > 0 {
		c.NextSize(ui.Px(s.span), ui.Children(1))
		inner = s.span - e.Pt(quoteBarPt+17)
	}
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(1))
	c.NextPadding(e.Pt(quoteBarPt+7), e.Pt(5), e.Pt(10), e.Pt(6))
	c.NextBG(bg)
	c.NextRadius(e.Pt(6))
	c.NextBorder(bar, e.Pt(quoteBarPt))
	c.NextBorderSides(ui.SideLeft)
	c.NextRole(ui.RoleButton)
	c.NextSemName(c.Fmt().Str(model.S(name)).Str(": ").Str(model.S(qsum)).String())
	c.NextCursor(ui.CursorPointer)
	id := c.Box(ui.Clickable|ui.DrawBackground|ui.DrawBorder, "quote")
	c.Push(id)
	key := c.ParentKey()
	quoteLine(c, k, ui.KeyN(key, 1), &r.Styles[rexui.TextStyles[rexui.TextSemibold]], nameCol, name, inner)
	quoteLine(c, k, ui.KeyN(key, 2), &r.Styles[rexui.TextStyles[rexui.TextFootnote]], text, qsum, inner)
	c.Pop()
	if c.Signal(id).Clicked && len(qid) > 0 {
		// a private reply's quote is in its group
		chat := ch.ID()
		if m.Decode(&s.dec) == nil && s.dec.HasQuoted && s.dec.Quoted.HasChat && len(s.dec.Quoted.Chat) > 0 {
			chat = s.dec.Quoted.Chat
		}
		a.OpenMessage(chat, qid)
	}
}

// inlineFoot reports whether m's text has room after its last line for the
// footer, which then sits there, at the bubble's right, as upstream's
// rich_body places it: the time drops to a row of its own only when it does
// not fit. The text is laid out as its RichText will be (the same
// paragraph, which the text system's cache then has), so its lines, bold,
// code and mentions included, are the ones drawn. A few large emojis alone
// keep the footer's row, and so does text read right to left that wraps
// (upstream's rule).
func (s *Screen) inlineFoot(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out, alone bool) bool {
	if alone && markup.Big(m.Text()) {
		return false
	}
	s.shownForBubble(a, m)
	limit := s.maxW
	if s.span > 0 {
		limit = s.span
	}
	// maxW and span are content widths (the bubble padding is already removed).
	// Dir, MaxLines and Ellipsis match text's RichOpts defaults; jumbo text
	// returned above.
	pv := e.C().LayoutText(s.body, s.rich, &s.look.rich, limit, ui.RichOpts{})
	ln := len(pv.Lines)
	if ln == 0 || ln > 1 && pv.Lines[0].Flags&text.LineRTL != 0 {
		return false
	}
	s.textW, s.lineW = float32(math.Ceil(float64(pv.Width))), pv.Lines[ln-1].Width
	f := s.marks(e, a, ch, m, out)
	return s.lineW+e.Pt(inlineGapPt)+s.footWidth(e, &f, false) <= limit+0.5
}

// inlineGapPt is the room between a line and the footer beside it.
const inlineGapPt = 8

// textAndFooter builds m's text with the footer beside its last line
// (inlineFoot): a row as wide as the bubble with the footer at its end, the
// text over it from its start (floating across, so the footer does not
// narrow it), their bottoms lined up, and under it a box as wide as both
// need.
func (s *Screen) textAndFooter(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, id uint64, key ui.Key, out, alone bool) {
	c := e.C()
	f := s.marks(e, a, ch, m, out)
	need := max(s.textW, theme.Snap(s.lineW+e.Pt(inlineGapPt)+s.footWidth(e, &f, false))+1)
	c.NextSize(ui.Px(need), ui.Px(0))
	c.Box(0, "inline width")
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyEnd)
	c.NextCross(ui.CrossEnd)
	c.Push(c.Box(0, "inline"))
	c.NextFlags(ui.FloatingX)
	c.NextPos(0, 0)
	s.text(e, a, m, id, key, alone)
	c.NextSize(ui.Children(1), ui.Children(1))
	s.footRow(e, m, &f, out, key, footInline)
	c.Pop()
}

// quoteLine is one line of a quote, b in st's font and col, as wide as it
// is up to w, its end faded.
func quoteLine(c *ui.Context, k *base.Kit, key ui.Key, st *theme.StyleFont, col uint32, b []byte, w float32) {
	c.NextFont(st.Font)
	c.NextTextColor(col)
	c.NextSize(ui.Px(max(min(theme.Snap(k.TextWidthBytes(st.Font, b))+base.LabelSlack, w), 0)), ui.Px(st.Line))
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(b)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, key, b)
}

// quoteBarPt is a quote's bar (QUOTE_BAR).
const quoteBarPt = 4

// settle is the width a bubble with a card or a quote settles at
// (zf:src/ui/conversation.rs settled_width): its text's natural width with
// the footer on its last line, at least the card's width (CARD_WIDTH) and
// at most the column's. 0 for other bubbles: they take their text's width.
func (s *Screen) settle(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out, quoted bool) float32 {
	if !quoted && !kinds.Card(m) {
		return 0
	}
	floor := min(e.Pt(kindsCardPt), s.maxW)
	if len(m.Text()) == 0 || (kinds.Has(m) && kinds.Caption(m) == kinds.Nowhere) || (!kinds.Has(m) && !av.Has(m) && !hasText(m)) {
		return floor
	}
	r := e.K.Kit.Theme()
	font := r.Styles[theme.StyleBody].Font
	if m.Kind() == abi.ContentTagPoll {
		font = r.Styles[rexui.TextStyles[rexui.TextSemibold]].Font
	}
	s.shownForBubble(a, m)
	var w, last float32
	for rest := s.body; ; {
		line := rest
		i := bytes.IndexByte(rest, '\n')
		if i >= 0 {
			line = rest[:i]
		}
		last = e.K.Kit.TextWidthBytes(font, line)
		w = max(w, last)
		if i < 0 {
			break
		}
		rest = rest[i+1:]
	}
	f := s.marks(e, a, ch, m, out)
	w = max(w, last+e.Pt(8)+s.footWidth(e, &f, false))
	return min(max(float32(math.Ceil(float64(w))), floor), s.maxW)
}

// kindsCardPt is CARD_WIDTH, a card's width at least.
const kindsCardPt = 320

// readableOn is c, blended toward toward only as far as it must be to
// reach the target contrast on bg (zf:src/theme.rs readable_on).
func readableOn(bg, c, toward uint32, target float64) uint32 {
	var step float32
	for {
		cand := lerpRGB(c, toward, step)
		if step >= 1 || theme.Contrast(cand|0xFF, bg|0xFF) >= target {
			return cand
		}
		step = min(step+0.05, 1)
	}
}

// lerpRGB is the colour t of the way from a to b, channel by channel.
func lerpRGB(a, b uint32, t float32) uint32 {
	var out uint32
	for sh := uint(0); sh < 32; sh += 8 {
		x, y := float32(a>>sh&0xFF), float32(b>>sh&0xFF)
		out |= min(uint32(x+(y-x)*t+0.5), 255) << sh
	}
	return out
}

func isJID(b []byte) bool {
	for _, c := range b {
		if c == '@' {
			return true
		}
	}
	return false
}

// messageInfo opens Message info on our message m.
func (s *Screen) messageInfo(e *screen.Env, m model.Message) {
	p := &e.S.Panes
	p.MessageInfo.Reset()
	p.MessageInfo.Add(m.ID())
	p.Pane = state.PaneMessageInfo
	e.S.Touch()
}

// ticks are the delivery marks of our message.
func ticks(d abi.Delivery) string {
	switch d {
	case abi.DeliveryPending:
		return "🕓"
	case abi.DeliveryFailed:
		return "!"
	case abi.DeliveryDelivered, abi.DeliveryRead, abi.DeliveryPlayed:
		return "✓✓"
	}
	return "✓"
}

// reactions is the row of reaction pills under the bubble: a click on one
// reacts with its emoji, or takes ours back (zf:src/ui/conversation.rs
// reactions).
func (s *Screen) reactions(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, out bool) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	align := ui.FlowStart
	if out {
		align = ui.FlowEnd
	}
	c.NextWidth(ui.Pct(1))
	c.NextPadding(e.Pt(6), 0, e.Pt(6), e.Pt(2))
	c.BeginFlow("reactions", ui.FlowOpts{GapX: e.Pt(3), GapY: e.Pt(3), Align: align, Cross: ui.CrossCenter})
	font := r.Styles[theme.StyleBody].Font
	for j := 0; j < m.Reactions(); j++ {
		emoji, count, mine := m.Reaction(j)
		f := c.Fmt().Str(model.S(emoji))
		if count > 1 {
			f.Byte(' ').Int(count)
		}
		label := f.Bytes()
		edge := r.Colors[theme.Separator]
		if mine {
			edge = r.Colors[theme.Accent]
		}
		c.NextSize(ui.TextSize(0), ui.TextSize(0))
		c.NextPadding(e.Pt(6), e.Pt(3), e.Pt(6), e.Pt(3))
		c.NextBG(theme.Over(r.Colors[theme.Fill1], r.Colors[theme.Raised]|0xFF))
		c.NextBorder(edge, e.Pt(1))
		c.NextRadius(e.Pt(11))
		c.NextFont(font)
		c.NextTextColor(r.Colors[theme.Label])
		c.NextRole(ui.RoleButton)
		c.NextSemNameBytes(label)
		if mine {
			c.NextSemState(ui.SemChecked)
		}
		c.NextCursor(ui.CursorPointer)
		id := c.BoxKeyed(ui.Clickable|ui.DrawBackground|ui.DrawBorder|ui.DrawText|ui.DrawHotEffects, ui.KeyN(c.ParentKey(), uint64(j)), label)
		sig := c.Signal(id)
		if sig.Hovering {
			// who added it (CV-028)
			rexui.Tooltip(k, id, s.reactors(e, a, ch, m, emoji))
		}
		if sig.Clicked {
			// a pill reacts with its emoji, or takes ours back
			if mine {
				emoji = nil
			}
			a.React(ch.ID(), m.ID(), emoji)
		}
	}
	c.EndFlow()
}

// pickRow opens a selection-mode row: the round check before the bubble,
// the whole row a toggle, and the column the bubble goes in. The caller
// pops both.
func (s *Screen) pickRow(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, picked bool) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	row := c.Box(ui.Clickable, "pick")
	if sig := c.Signal(row); sig.Pressed {
		s.pickShift = c.Input().Mods&ui.ModShift != 0
	} else if sig.Clicked {
		picked = s.pick(e, a, m)
	}
	c.Push(row)
	side := e.Pt(20)
	fill, edge := uint32(0), r.Colors[theme.Label3]
	if picked {
		fill, edge = r.Colors[theme.Accent], r.Colors[theme.Accent]
	}
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextBG(fill)
	c.NextBorder(edge, e.Pt(1.5))
	c.NextRadius(side / 2)
	c.NextRole(ui.RoleCheckbox)
	f := c.Fmt()
	s.fill3(f, i18n.T("Select message from {sender}, {time}: {summary}"), s.senderStr(a, m), m)
	c.NextSemName(f.String())
	if picked {
		c.NextSemState(ui.SemChecked)
	}
	id := c.Box(ui.Clickable|ui.DrawBackground|ui.DrawBorder, "check")
	if picked {
		c.Push(id)
		c.NextSize(ui.Px(side), ui.Px(side))
		c.NextAlign(ui.AlignCenter)
		c.NextFont(s.look.small)
		c.NextTextColor(r.Colors[theme.OnAccent])
		c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 1), base.StrBytes("✓"))
		c.Pop()
	}
	if sig := c.Signal(id); sig.Pressed {
		s.pickShift = c.Input().Mods&ui.ModShift != 0
	} else if sig.Clicked {
		s.pick(e, a, m)
	}
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(0, "picked"))
}

// fill3 is upstream's selection name: the template with the sender, the
// time and the message's summary.
func (s *Screen) fill3(f *ui.Fmt, tmpl, who string, m model.Message) {
	for len(tmpl) > 0 {
		i := indexOf(tmpl, '{')
		if i < 0 {
			f.Str(tmpl)
			return
		}
		f.Str(tmpl[:i])
		tmpl = tmpl[i:]
		switch {
		case hasPrefix(tmpl, "{sender}"):
			f.Str(who)
			tmpl = tmpl[len("{sender}"):]
		case hasPrefix(tmpl, "{time}"):
			clock(f, m.Time())
			tmpl = tmpl[len("{time}"):]
		case hasPrefix(tmpl, "{summary}"):
			if hasText(m) {
				f.Str(model.S(m.Text()))
			} else {
				s.summary(f, m)
			}
			tmpl = tmpl[len("{summary}"):]
		default:
			f.Byte('{')
			tmpl = tmpl[1:]
		}
	}
}

// pick ticks m or takes it out; with Shift, held at the press (as AppKit's
// tables read it) or at the release, it selects from the last one clicked
// (logic's reducers). It reports whether m is selected after.
func (s *Screen) pick(e *screen.Env, a *state.Account, m model.Message) bool {
	shift := s.pickShift || e.C().Input().Mods&ui.ModShift != 0
	s.pickShift = false
	if shift {
		logic.SelectRange(e.S, a, m.ID())
	} else {
		logic.ToggleSelected(e.S, a, m.ID())
	}
	return e.S.Panes.Selection.Has(m.ID())
}

// canSelect is upstream's app::can_select: what selection mode can pick.
func canSelect(m model.Message) bool { return logic.CanSelect(m.Kind()) }

// rightClick opens the message's menu on a right click over its bubble (the
// last frame's rect: only the hot box gets RightClicked, and the bubble
// holds others).
func (s *Screen) rightClick(e *screen.Env, m model.Message, rect [4]float32) {
	if at, ok := rightAt(e, rect); ok {
		s.openMenu(m.ID(), at)
	}
}

// doubleBeside reports a double click this frame in the message's row
// (row) beside its bubble (b), or on the bubble's padding round its content.
func (s *Screen) doubleBeside(e *screen.Env, row, b [4]float32) bool {
	in := e.C().Input()
	if !in.Pressed[0] || in.Clicks[0] != 2 || row[2] <= row[0] {
		return false
	}
	x, y := in.MouseX, in.MouseY
	if x < row[0] || x >= row[2] || y < row[1] || y >= row[3] {
		return false
	}
	px, py := e.Pt(rexui.BubbleLook.PadX), e.Pt(rexui.BubbleLook.PadY)
	inner := x >= b[0]+px && x < b[2]-px && y >= b[1]+py && y < b[3]-py
	return !inner
}

// rightAt reports a right click released in rect this frame, and where.
func rightAt(e *screen.Env, rect [4]float32) ([4]float32, bool) {
	in := e.C().Input()
	if !in.Released[1] || rect[2] <= rect[0] {
		return [4]float32{}, false
	}
	x, y := in.MouseX, in.MouseY
	if x < rect[0] || x >= rect[2] || y < rect[1] || y >= rect[3] {
		return [4]float32{}, false
	}
	return [4]float32{x, y, x + 1, y + 1}, true
}

func (s *Screen) openMenu(id []byte, anchor [4]float32) {
	s.menu.open = true
	s.menu.msg.Reset()
	s.menu.msg.Add(id)
	s.menu.anchor = anchor
	s.more = false
}
