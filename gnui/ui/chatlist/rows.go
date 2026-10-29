package chatlist

import (
	"bytes"
	"math"
	"strconv"
	"strings"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// icons are the symbols the list draws.
type icons struct {
	pin, muted, mention, clock, failed, timer, tick icon.ID
	newChat, sidebar, back, plus, pencil, trash     icon.ID
	more                                            icon.ID // the More chip's chevron
	// empty-state glyphs (36 pt)
	eChat, eStar, eLock, eSearch, eArchive icon.ID
}

func (ic *icons) add(k *base.Kit, tb float32) {
	big := func(sym string, fb rune) icon.Spec { return icon.Spec{Symbol: sym, Fallback: fb, Pt: 38} }
	// the row's indicators and small buttons at Zap's small size, the
	// toolbar's items as Rex's (theme/icons.go)
	for _, it := range [...]struct {
		dst  *icon.ID
		spec icon.Spec
	}{
		{&ic.pin, ztheme.SmallIcon("pin.fill", '⚲')},
		{&ic.muted, ztheme.SmallIcon("bell.slash.fill", '⊘')},
		{&ic.mention, ztheme.SmallIcon("at", '@')},
		{&ic.clock, ztheme.SmallIcon("clock", '◷')},
		{&ic.failed, ztheme.SmallIcon("exclamationmark.circle.fill", '!')},
		{&ic.timer, icon.Spec{Symbol: "timer", Fallback: '◔', Pt: 9, Weight: 0.3}}, // on the avatar's edge
		{&ic.tick, ztheme.SmallIcon("checkmark", '✓')},
		{&ic.newChat, ztheme.ToolIcon("square.and.pencil", '✎')},
		{&ic.sidebar, ztheme.ToolIcon("sidebar.left", '▤')},
		{&ic.back, ztheme.ToolIcon("chevron.left", '‹')},
		{&ic.plus, ztheme.SmallIcon("plus", '+')},
		{&ic.pencil, ztheme.SmallIcon("pencil", '✎')},
		{&ic.trash, ztheme.SmallIcon("trash", '⌫')},
		{&ic.more, icon.Spec{Symbol: "chevron.down", Fallback: '▾', Pt: chevPt, Weight: 0.3}},
		{&ic.eChat, big("bubble.left.and.bubble.right", '✉')},
		{&ic.eStar, big("star", '☆')},
		{&ic.eLock, big("lock.open", '⚿')},
		{&ic.eSearch, big("magnifyingglass", '⌕')},
		{&ic.eArchive, big("archivebox", '▤')},
	} {
		*it.dst = k.AddIcon(it.spec)
	}
}

// Row geometry, in points: Rex's chat row with a one-line preview.
const (
	rowH     = 56
	avatarPt = 36
)

// rowInfo is the text of one chat row, kept while the chat, the contacts
// and the day stay as they were: a row is only formatted when it changes,
// into buffers it keeps, so a new message re-formats it without allocating.
type rowInfo struct {
	valid                bool
	gen, cgen, agen      uint32
	day                  int
	tick                 uint32 // the clock's serial (12/24 hours)
	title                string
	when, preview        []byte
	announce             []byte // the unread state, for assistive technologies
	draftOf              string // the draft's text this row was made with
	typingWho, typingFor string
	value                []byte
	tvalue               string
	tGroup, multiline    bool
	draftSrc             []byte
	avatar               string
	key                  img.Key
	handle               img.Handle
	hasAvatar            bool
	selfRow              bool
	unread               int
	marked, mention      bool
	muted, pinned, timer bool
	ticks                tickKind
	hasLast, fromMe      bool
	prefix               []byte // "Ada: " before a group's last message
	previewFull          []byte
	previewP             string
}

// tickKind is the delivery marks before our own last message.
type tickKind uint8

const (
	tickNone tickKind = iota
	tickPending
	tickSent
	tickDelivered
	tickRead
	tickFailed
)

// Row builds one chat row (rexui.ChatListRow with the extras of upstream's
// row: ticks, the draft, the muted and pinned icons, the @ marker, the
// timer on the avatar). It reports the press.
func (s *Screen) chatRow(e *screen.Env, in *rowInfo, selected, typing bool, typingText string) base.Signal {
	value := in.value
	if typing {
		value = base.StrBytes(in.tvalue)
	}
	k := e.K
	c, r := k.Context(), k.Theme()
	rc := &rexui.ChatRowLook
	sc := k.Scale()
	p := k.PressState("row", base.HoverOn)
	h := theme.Snap(rowH * sc)
	radius := theme.Snap(rc.Radius * sc)
	inset := theme.Snap(rc.Inset * sc)
	pad, gap := theme.Snap(rc.Pad*sc), theme.Snap(rc.Gap*sc)

	unread := in.unread > 0 || in.marked
	cut := false
	var fill uint32
	titleC, previewC, timeC := rc.Title.RGBA(r), rc.Preview.RGBA(r), rc.Time.RGBA(r)
	accent := r.Colors[theme.Accent]
	active := !p.Inactive
	on := selected && active
	switch {
	case p.Disabled:
		titleC, previewC, timeC = r.Colors[theme.TextDisabled], r.Colors[theme.TextDisabled], r.Colors[theme.TextDisabled]
	case on:
		fill = rc.Selected.RGBA(r)
		titleC, previewC, timeC = rc.TitleSelected.RGBA(r), rc.PreviewSelected.RGBA(r), rc.PreviewSelected.RGBA(r)
	case selected:
		fill = rc.SelectedInactive.RGBA(r)
	default:
		// a row under the pointer, and one pressed, lift a little off the
		// pane, easing in and out (Visual.Hot and Down), so the list
		// answers before the click does
		if t := ztheme.T.RowHover*p.Hot + ztheme.T.RowPress*p.Down; t > 0 { // how much of the label colour (tune.go)
			fill = theme.WithAlpha(r.Colors[theme.Label], t)
		}
	}
	if unread && !in.muted && !on {
		timeC = accent
	}

	var state ui.SemState
	if selected {
		state = ui.SemSelected
	}
	c.NextSemState(state)
	c.NextSemValueBytes(value)
	c.NextAxis(ui.AxisX)
	c.NextPadding(inset, 0, inset, 0)
	p.Name = in.title
	k.BeginPress(&p, ui.RoleListItem, ui.Pct(1), ui.Px(h), 0, nil)
	id := p.ID
	c.NextBG(fill)
	c.NextRadius(radius)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(gap)
	c.NextPadding(pad, 0, pad, 0)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	block := c.BoxKeyed(ui.DrawBackground, ui.KeyN(p.Key, 19), nil)
	c.Push(block)

	// the avatar, with the disappearing-messages timer at its corner
	side := theme.Snap(avatarPt * sc)
	c.NextSize(ui.Px(side), ui.Px(side))
	wrap := c.BoxKeyed(0, ui.KeyN(p.Key, 17), nil)
	c.Push(wrap)
	rexui.Avatar(k.Kit, in.title, rexui.AvatarOpts{Size: avatarPt, Image: in.handle, Backdrop: rexui.Of(theme.Window)})
	if in.timer {
		d := theme.Snap(14 * sc)
		theme.At(c, side-d+theme.Snap(2*sc), side-d+theme.Snap(2*sc), d, d)
		c.NextBG(r.Colors[theme.Window])
		c.NextRadius(d / 2)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
		b := c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 18), nil)
		c.Push(b)
		k.Icon(s.ic.timer, previewC)
		c.Pop()
	}
	c.Pop()

	// the text column: title and time, then the preview and its marks
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextGap(theme.Snap(sc))
	col := c.BoxKeyed(0, ui.KeyN(p.Key, 20), nil)
	c.Push(col)
	ts := &r.Styles[theme.StyleHeadline]
	if !unread {
		ts = &r.Styles[theme.StyleBodySemibold]
	}
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(gap)
	c.NextSize(ui.Pct(1), ui.Px(ts.Line))
	line := c.BoxKeyed(0, ui.KeyN(p.Key, 21), nil)
	c.Push(line)
	c.NextSize(ui.Pct(1), ui.Px(ts.Line))
	c.NextFont(ts.Font)
	c.NextTextColor(titleC)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 22), base.StrBytes(in.title))
	if len(in.when) > 0 {
		tm := &r.Styles[theme.StyleCaption1]
		c.NextSize(ui.TextSize(0), ui.Px(tm.Line))
		c.NextFont(ztheme.Meta(k, ztheme.T.MetaPt)) // Rex's metadata
		c.NextTextColor(timeC)
		c.BoxKeyed(ui.DrawText, ui.KeyN(p.Key, 23), in.when)
	}
	c.Pop()

	ps := &r.Styles[theme.StyleSubheadline]
	lineH := max(ps.Line, theme.Snap(18*sc))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(theme.Snap(4 * sc))
	c.NextSize(ui.Pct(1), ui.Px(lineH))
	l2 := c.BoxKeyed(0, ui.KeyN(p.Key, 24), nil)
	c.Push(l2)
	lead := false
	switch {
	case typing:
		c.NextSize(ui.Pct(1), ui.Px(ps.Line))
		c.NextFont(ps.Font)
		c.NextTextColor(rc.Typing.RGBA(r))
		if on {
			c.NextTextColor(previewC)
		}
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(p.Key, 25), base.StrBytes(typingText))
	default:
		if in.draftOf != "" {
			c.NextSize(ui.TextSize(0), ui.Px(ps.Line))
			c.NextFont(ps.Font)
			c.NextTextColor(accent)
			if on {
				c.NextTextColor(previewC)
			}
			c.BoxKeyed(ui.DrawText, ui.KeyN(p.Key, 26), base.StrBytes(draftLabel()))
		} else if in.ticks != tickNone {
			// the marks and the preview are one unit, so that the marks being
			// wider than 16 pt takes from the gap after them: the text stays
			// where upstream puts it, 20 pt along
			w, h := s.tickSize(k.Kit, in.ticks, ps.Line)
			c.NextAxis(ui.AxisX)
			c.NextCross(ui.CrossCenter)
			c.NextGap(max(theme.Snap(4*sc)-(w-theme.Snap(16*sc)), 0))
			c.NextSize(ui.Pct(1), ui.Px(h))
			c.Push(c.BoxKeyed(0, ui.KeyN(p.Key, 28), nil))
			lead = true
			s.ticks(e, in.ticks, ps.Line, p.Key, on)
		}
		c.NextSize(ui.Pct(1), ui.Px(ps.Line))
		c.NextFont(ps.Font)
		c.NextTextColor(previewC)
		txt := in.preview
		if in.draftOf != "" {
			txt = base.StrBytes(in.draftOf)
		}
		pb := c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(p.Key, 27), txt)
		if lead {
			c.Pop()
		}
		if rect := c.Rect(pb); len(in.previewFull) > 0 && in.draftOf == "" && (in.multiline || k.TextWidthBytes(ps.Font, txt) > rect[2]-rect[0]+1) {
			cut = true
		}
	}
	dim := previewC
	if in.pinned {
		k.Icon(s.ic.pin, dim)
	}
	if in.muted {
		k.Icon(s.ic.muted, dim)
	}
	if in.mention {
		bo := rexui.BadgeOpts{Color: rexui.Of(theme.Accent), Name: i18n.T("Mentioned you")}
		if on {
			bo.Color, bo.TextColor = rexui.Of(theme.OnAccent), rexui.Of(theme.Accent)
		}
		rexui.Badge(k.Kit, "@", bo)
	}
	if unread {
		bo := rexui.BadgeOpts{Color: rexui.Of(theme.Accent)}
		if in.muted {
			bo.Color = rexui.Of(theme.Gray)
		}
		if on {
			bo.Color, bo.TextColor = rexui.Of(theme.OnAccent), rexui.Of(theme.Accent)
		}
		if in.unread > 0 {
			rexui.BadgeCountN(k.Kit, in.unread, bo)
		} else {
			bo.Variant, bo.Name = rexui.BadgeStatus, i18n.T("Unread")
			rexui.Badge(k.Kit, "", bo)
		}
	}
	c.Pop() // line 2
	c.Pop() // the column
	c.Pop() // the block
	k.FocusRing(id, p.Visual, radius)
	sig := k.EndPress(&p)
	if cut {
		rexui.Tooltip(k.Kit, id, model.S(in.previewFull))
	}
	return sig
}

// draftLabel is "Draft: " (the trailing space is part of the label).
func draftLabel() string { return i18n.T("Draft:") + " " }

// tickSize is the size of the delivery marks as drawn: the second tick of a
// pair sits 5 pt right of the first, so a pair is wider than the 16 pt a
// single mark has, and an icon can be taller than the preview's line.
func (s *Screen) tickSize(k *base.Kit, t tickKind, line float32) (w, h float32) {
	sc := k.Scale()
	var iw, ih float32
	switch t {
	case tickPending:
		iw, ih = k.IconSize(s.ic.clock)
	case tickFailed:
		iw, ih = k.IconSize(s.ic.failed)
	case tickSent:
		iw, ih = k.IconSize(s.ic.tick)
	default:
		iw, ih = k.IconSize(s.ic.tick)
		iw += theme.Snap(5 * sc)
	}
	return max(theme.Snap(16*sc), float32(math.Ceil(float64(iw)))), max(line, float32(math.Ceil(float64(ih))))
}

// ticks draws the delivery marks of our own last message in a box as wide as
// they are (at least 16 pt).
func (s *Screen) ticks(e *screen.Env, t tickKind, line float32, key ui.Key, on bool) {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	w, line := s.tickSize(k.Kit, t, line)
	col := r.Colors[theme.Label2]
	switch t {
	case tickRead:
		col = r.Colors[theme.Blue]
	case tickFailed:
		col = rexui.DangerText.RGBA(r)
	}
	if on && t != tickFailed { // the open chat's row is a tint: its preview's colour
		col = rexui.ChatRowLook.PreviewSelected.RGBA(r)
	}
	c.NextSize(ui.Px(w), ui.Px(line))
	box := c.BoxKeyed(0, ui.KeyN(key, 30), nil)
	c.Push(box)
	one := func(n uint64, x float32, ic icon.ID) {
		iw, ih := k.IconSize(ic)
		theme.At(c, x, theme.Snap((line-ih)/2), iw, ih)
		wb := c.BoxKeyed(base.Floating, ui.KeyN(key, 31+n), nil)
		c.Push(wb)
		k.Icon(ic, col)
		c.Pop()
	}
	switch t {
	case tickPending:
		one(0, 0, s.ic.clock)
	case tickFailed:
		one(0, 0, s.ic.failed)
	case tickSent:
		one(0, 0, s.ic.tick)
	default:
		one(0, 0, s.ic.tick)
		one(1, theme.Snap(5*sc), s.ic.tick)
	}
	c.Pop()
}

// refresh formats the row of chat ch if anything it shows changed.
func (s *Screen) refresh(e *screen.Env, in *rowInfo, ch model.Chat) {
	a := e.Acct()
	m := a.Model
	day := dayNumber(e.S.Now())
	before := in.draftOf
	s.draftFor(a, in, ch.ID())
	draft := in.draftOf
	if in.valid && in.gen == ch.Gen() && in.cgen == m.Contacts.Gen() && in.day == day && in.tick == s.clockSerial && before == draft {
		s.refreshAvatar(e, in, ch)
		return
	}
	in.valid, in.gen, in.cgen, in.day, in.tick = true, ch.Gen(), m.Contacts.Gen(), day, s.clockSerial
	if t := s.titleBytes(a, ch); string(t) != in.title {
		in.title = string(t)
	}
	in.when = appendStamp(in.when[:0], e.S.Now(), ch.Time())
	in.unread, in.marked = ch.Unread(), ch.MarkedUnread()
	_, in.mention = ch.UnreadMention()
	in.mention = in.mention && in.unread > 0
	in.pinned = ch.Pinned()
	_, in.timer = ch.Ephemeral()
	if sec, ok := ch.Ephemeral(); ok {
		in.timer = sec > 0
	}
	in.hasLast, in.fromMe = ch.HasLast(), ch.LastFromMe()
	in.ticks, in.prefix = tickNone, in.prefix[:0]
	in.preview = in.preview[:0]
	if in.hasLast {
		if in.fromMe {
			in.ticks = ticksOf(ch.LastStatus(), ch.Kind(), m.ReceiptsOff())
		} else if ch.Group() {
			if who := s.shortNameBytes(a, ch.PreviewSender()); len(who) > 0 {
				in.prefix = append(append(in.prefix, who...), ": "...)
			}
		}
		in.preview = s.appendPlain(append(in.preview, in.prefix...), m, ch.Preview())
	}
	in.announce = appendAnnounce(in.announce[:0], in)
	in.previewFull, in.multiline = in.previewFull[:0], false
	if in.hasLast {
		full := ch.PreviewFull()
		if len(full) > 2000 {
			full = full[:2000]
		}
		in.multiline = len(bytes.TrimRight(full, " \n\t")) > len(bytes.TrimSpace(ch.Preview())) && bytes.IndexByte(bytes.TrimSpace(full), '\n') >= 0
		in.previewFull = bytes.TrimRight(s.appendPlain(append(in.previewFull, in.prefix...), m, full), " \n\t")
	}
	// the accessible value: the unread state, then the line the row shows
	in.value = append(in.value[:0], in.announce...)
	if in.draftOf != "" || len(in.preview) > 0 {
		if len(in.value) > 0 {
			in.value = append(in.value, ". "...)
		}
		if in.draftOf != "" {
			in.value = append(append(append(in.value, i18n.T("Draft:")...), ' '), in.draftOf...) // draftLabel()
		} else {
			in.value = append(in.value, in.preview...)
		}
	}
	in.tvalue = ""
	in.valid = true
	s.refreshAvatar(e, in, ch)
}

// refreshMuted keeps the muted flag current: it depends on the clock.
func (s *Screen) mutedNow(e *screen.Env, ch model.Chat) bool { return ch.Muted(e.S.Now().Unix()) }

// refreshAvatar asks for the picture once, then follows its file.
func (s *Screen) refreshAvatar(e *screen.Env, in *rowInfo, ch model.Chat) {
	a := e.Acct()
	g := a.Model.Avatars.Gen()
	if in.hasAvatar && in.agen == g {
		return
	}
	if !in.hasAvatar {
		a.FetchAvatar(ch.ID(), false)
	}
	in.hasAvatar, in.agen = true, g
	path, id, ok := a.Model.Avatars.Picture(ch.ID(), false)
	// The key is the picture's identity, not its path: a new picture is
	// written over the last one's file.
	if ok && (id != in.key.Hash || string(path) != in.avatar) {
		if string(path) != in.avatar {
			in.avatar = string(path)
		}
		in.key = img.Key{Hash: id}
		in.handle = 0
	}
	if !ok {
		in.avatar, in.handle = "", 0
	}
}

// avatarHandle requests the picture from the image cache.
func (s *Screen) avatarHandle(e *screen.Env, in *rowInfo, px int32) {
	if in.avatar == "" {
		return
	}
	cache := e.K.Images()
	if cache == nil {
		return
	}
	in.key.W, in.key.H, in.key.Flags = px, px, img.Cover
	in.handle, _ = cache.Request(in.key, img.File(in.avatar))
}

func ticksOf(d abi.Delivery, kind abi.ChatKind, receiptsOff bool) tickKind {
	// display_delivery (zf:src/ui/conversation.rs:5079): with read receipts
	// off, a direct chat's Read shows as Delivered.
	if receiptsOff && kind == abi.ChatKindDirect && d == abi.DeliveryRead {
		d = abi.DeliveryDelivered
	}
	switch d {
	case abi.DeliveryPending:
		return tickPending
	case abi.DeliverySent:
		return tickSent
	case abi.DeliveryDelivered:
		return tickDelivered
	case abi.DeliveryRead, abi.DeliveryPlayed:
		return tickRead
	case abi.DeliveryFailed:
		return tickFailed
	}
	return tickNone
}

// appendAnnounce appends what assistive technologies read after a row's
// name (zf:src/ui/chats.rs:2110).
func appendAnnounce(dst []byte, in *rowInfo) []byte {
	switch {
	case in.marked && in.unread == 0:
		return append(dst, i18n.T("unread")...)
	case in.unread > 0 && in.mention:
		return appendCount(dst, i18n.T("{} unread messages, mentioned you"), in.unread)
	case in.unread > 0:
		return appendCount(dst, i18n.T("{} unread messages"), in.unread)
	}
	return dst
}

// appendCount appends tpl with every "{}" in it replaced by n: i18n.Format
// into a buffer.
func appendCount(dst []byte, tpl string, n int) []byte {
	for {
		i := strings.Index(tpl, "{}")
		if i < 0 {
			return append(dst, tpl...)
		}
		dst = strconv.AppendInt(append(dst, tpl[:i]...), int64(n), 10)
		tpl = tpl[i+2:]
	}
}

// joinValue is a row's accessible value: its unread state, then the line it
// shows.
func joinValue(announce, shown string) string {
	if announce == "" {
		return shown
	}
	if shown == "" {
		return announce
	}
	return announce + ". " + shown
}
