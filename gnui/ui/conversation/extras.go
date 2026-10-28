package conversation

import (
	"bytes"
	"time"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/calls"
	"zapfast/ui/screen"
)

// ---- senders (PR-008, CV-018) ----

// hueOf is a sender's hue in degrees (zf:src/util.rs hue): FNV-1a of the
// JID, so a person keeps their colour.
func hueOf(id []byte) float32 {
	h := uint32(2_166_136_261)
	for _, b := range id {
		h ^= uint32(b)
		h *= 16_777_619
	}
	return float32(h % 360)
}

const senderPicPt = 28 // a group sender's picture

// senderColumn opens, for an incoming message in a group, the row with the
// sender's picture (on a run's first message; the room for it on the
// others) and the column the bubble goes in. The caller pops both.
func (s *Screen) senderColumn(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, first bool) {
	c := e.C()
	d := e.Pt(senderPicPt)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossStart)
	c.NextGap(e.Pt(6))
	c.Push(c.Box(0, "sender row"))
	if first {
		s.senderPic(e, a, ch, m, d)
	} else {
		c.NextSize(ui.Px(d), ui.Px(1))
		c.Box(0, "sender room")
	}
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(0, "sender column"))
}

// senderPic is a sender's round picture, or their initials on their hue
// until it comes: Rex's avatar.
func (s *Screen) senderPic(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, d float32) {
	jid := m.Sender()
	name := bytes.TrimLeft(s.sender(a, m), "~")
	rexui.Avatar(e.K.Kit, model.S(name), rexui.AvatarOpts{Size: senderPicPt, Image: s.senderImage(e, a, jid, d),
		Color: rexui.OfRGBA(ztheme.AvatarColor(hueOf(jid)))})
}

// typingLine is the header's "typing…" (zf:src/ui/conversation.rs
// subtitle): in a group "Ada is typing…" or "Ada, Bo and Cy are typing…",
// from s.typers.
func (s *Screen) typingLine(f *ui.Fmt, a *state.Account, ch model.Chat) *ui.Fmt {
	n := len(s.typers)
	switch {
	case !ch.Group() || n == 0:
		return f.Str(i18n.T("typing…"))
	case n == 1:
		return fillBytes(f, i18n.T("{one} is typing…"), "{one}", a.Model.DisplayName(s.typers[0]))
	}
	// "{names} and {last} are typing…" (upstream does not translate it)
	tmpl := i18n.T("{names} and {last} are typing…")
	for len(tmpl) > 0 {
		switch {
		case hasPrefix(tmpl, "{names}"):
			for i := 0; i < n-1; i++ {
				if i > 0 {
					f.Str(", ")
				}
				f.Str(model.S(a.Model.DisplayName(s.typers[i])))
			}
			tmpl = tmpl[len("{names}"):]
		case hasPrefix(tmpl, "{last}"):
			f.Str(model.S(a.Model.DisplayName(s.typers[n-1])))
			tmpl = tmpl[len("{last}"):]
		default:
			f.Byte(tmpl[0])
			tmpl = tmpl[1:]
		}
	}
	return f
}

// typingPics is the typing bubble's stack of the typers' pictures in a
// group, the first three (zf:src/ui/conversation.rs typing_bubble): Rex's
// avatar stack.
func (s *Screen) typingPics(e *screen.Env, a *state.Account) {
	d := e.Pt(senderPicPt)
	n := min(len(s.typers), len(s.typerNames))
	for i := 0; i < n; i++ {
		s.typerNames[i] = model.S(bytes.TrimLeft(a.Model.DisplayName(s.typers[i]), "~"))
		s.typerPics[i] = s.senderImage(e, a, s.typers[i], d)
	}
	rexui.AvatarStack(e.K.Kit, i18n.T("typing…"), s.typerNames[:n], n, rexui.AvatarOpts{Size: senderPicPt, Images: s.typerPics[:n]})
}

// senderImage is the image handle of a sender's picture, 0 for none (yet).
// The path is kept per sender, so a frame makes no string.
func (s *Screen) senderImage(e *screen.Env, a *state.Account, jid []byte, d float32) img.Handle {
	cache := e.K.Images()
	if cache == nil || len(jid) == 0 {
		return 0
	}
	if s.pics == nil {
		s.pics = map[uint64]senderPicEnt{}
	}
	h := img.HashBytes(jid) ^ a.Serial
	g := a.Model.Avatars.Gen()
	ent, ok := s.pics[h]
	if !ok || ent.gen != g {
		a.FetchAvatar(jid, false)
		path, id, has := a.Model.Avatars.Picture(jid, false)
		if !has {
			path = nil
		}
		if string(path) != ent.path {
			ent.path = string(path)
		}
		ent.id = id // a new picture reuses the last one's file
		ent.gen = g
		s.pics[h] = ent
	}
	if ent.path == "" {
		return 0
	}
	px := int32(d + 0.5)
	hd, _ := cache.Request(img.Key{Hash: ent.id, W: px, H: px, Flags: img.Cover}, img.File(ent.path))
	return hd
}

type senderPicEnt struct {
	gen  uint32
	id   uint64 // the picture's identity (Avatars.Picture), its key's hash
	path string
}

// ---- the day that floats over the transcript (CV-014) ----

const (
	stickyHold = time.Second            // STICKY_DATE_HOLD
	stickyFade = 250 * time.Millisecond // STICKY_DATE_FADE
)

// stickyState follows the view's scrolling for the floating day.
type stickyState struct {
	ok      bool
	anchor  uint64
	off     float64
	atEnd   bool
	movedAt int64
}

// stickyDay keeps the day of the first message on screen at the top of the
// transcript once its own divider has scrolled away, while the reader
// scrolls; it fades a moment after scrolling stops. Following new messages
// at the bottom does not count (zf:src/ui/conversation.rs sticky_date).
func (s *Screen) stickyDay(e *screen.Env, v *model.Conversation, w float32) {
	c := e.C()
	st := &s.sticky
	id, off := s.t.st.Anchor()
	now := c.Input().Time
	atEnd := s.vl.AtEnd
	if !st.ok {
		*st = stickyState{ok: true, anchor: id, off: off, atEnd: atEnd}
		return
	}
	moved := id != st.anchor || off-st.off > 0.5 || st.off-off > 0.5
	if moved && !(atEnd && st.atEnd) {
		st.movedAt = now
	}
	st.anchor, st.off, st.atEnd = id, off, atEnd
	if st.movedAt == 0 {
		return
	}
	since := time.Duration(now - st.movedAt)
	if since >= stickyHold+stickyFade {
		st.movedAt = 0
		return
	}
	opacity := float32(1)
	if since >= stickyHold {
		opacity = 1 - float32(since-stickyHold)/float32(stickyFade)
		c.RequestFrame()
	} else {
		c.WakeIn(int64(stickyHold - since))
	}
	mi := s.t.index(id)
	if mi < 0 {
		return
	}
	m := v.At(mi)
	// its own divider is still in view at the top
	if pi := s.shownNext(e, v, mi, -1); (pi < 0 || dayOf(v.At(pi).Time()) != dayOf(m.Time())) && off < dividerPt {
		return
	}
	s.chip(e, dayLabel(e.C().Fmt(), m.Time(), s.now).Bytes(), w, e.Pt(8), opacity)
}

const dividerPt = 36 // how far down a message its day divider reaches

// chip is a floating day chip at y, at opacity: the date divider's pill.
func (s *Screen) chip(e *screen.Env, label []byte, w, y, opacity float32) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	font := r.Styles[theme.StyleFootnote].Font
	padX, padY := e.Pt(10), e.Pt(3)
	lh := r.Styles[theme.StyleFootnote].Line
	cw := theme.Snap(k.TextWidthBytes(font, label)) + 1 + 2*padX
	ch := lh + 2*padY
	theme.At(c, theme.Snap((w-cw)/2), y, cw, ch)
	bg := theme.Over(r.Colors[theme.Fill2], r.Colors[theme.Window]|0xFF)
	flags := base.Floating | ui.DrawBackground | ui.DrawText
	c.NextBG(theme.WithAlpha(bg, opacity*float32(bg&0xFF)/255))
	c.NextRadius(ch / 2)
	c.NextFont(font)
	c.NextTextColor(theme.WithAlpha(r.Colors[theme.Label2], opacity*float32(r.Colors[theme.Label2]&0xFF)/255))
	c.NextAlign(ui.AlignCenter)
	c.NextPadding(padX, padY, padX, padY)
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(label)
	c.BoxKeyed(flags, ui.KeyN(c.ParentKey(), 7300), label)
}

// ---- pin notices (CV-030) ----

// collectNotices lists the open chat's pins oldest first (noteIdx: ChatPin
// indexes), for the notices they leave in the transcript.
func (s *Screen) collectNotices(a *state.Account, chat []byte) {
	s.noteN = 0
	n := a.Model.ChatPins(chat)
	for i := 0; i < n && s.noteN < len(s.noteIdx); i++ {
		p := &s.pin
		if a.Model.ChatPin(chat, i, p) != nil || p.PinnedAt == 0 {
			continue
		}
		j := s.noteN
		for ; j > 0 && s.noteAt[j-1] > p.PinnedAt; j-- {
			s.noteIdx[j], s.noteAt[j] = s.noteIdx[j-1], s.noteAt[j-1]
		}
		s.noteIdx[j], s.noteAt[j] = i, p.PinnedAt
		s.noteN++
	}
}

// notices builds the notices of the pins made after from and up to until
// (zf:src/ui/conversation.rs pin_notice): "You pinned a message", "Ada
// pinned a message", or "A message was pinned".
func (s *Screen) notices(e *screen.Env, a *state.Account, ch model.Chat, from, until int64) {
	c, k := e.C(), e.K.Kit
	for j := 0; j < s.noteN; j++ {
		if s.noteAt[j] <= from || s.noteAt[j] > until {
			continue
		}
		p := &s.pin
		if a.Model.ChatPin(ch.ID(), s.noteIdx[j], p) != nil {
			continue
		}
		f := c.Fmt()
		switch {
		case p.Pinner.ByMe:
			f.Str(i18n.T("You pinned a message"))
		case len(p.Pinner.Sender) == 0:
			f.Str(i18n.T("A message was pinned"))
		default:
			fillBytes(f, i18n.T("{} pinned a message"), "{}", a.Model.DisplayName(p.Pinner.Sender))
		}
		rexui.DateDivider(k, f.String(), rexui.DateDividerOpts{Pill: true})
	}
}

// ---- the react and reply controls beside a hovered message (CV-029) ----

const (
	reactPt    = 26 // REACTION_AFFORDANCE_SIZE
	reactGapPt = 6  // REACTION_AFFORDANCE_GAP
)

// affordances are the round React control beside a message, and Reply one
// step further out where the chat takes messages, shown while the pointer
// is on the bubble or them (or one has the focus); their place swaps sides
// near an edge (zf:src/ui/conversation.rs reaction_affordance). item is
// the message's box; b the bubble's rect last frame, in the window.
func (s *Screen) affordances(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, item ui.Key, b [4]float32) {
	if m.Kind() == abi.ContentTagCall || m.Kind() == abi.ContentTagRevoked || b[2] <= b[0] || s.menu.open {
		return
	}
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	ir := c.RectOf(item)
	bounds := s.listR
	d, gap := e.Pt(reactPt), e.Pt(reactGapPt)
	if ir[2] <= ir[0] || bounds[3]-bounds[1] < d {
		return
	}
	// only the part of the bubble on screen anchors them
	top, bot := max(b[1], bounds[1]), min(b[3], bounds[3])
	if bot-top < d {
		return
	}
	before, after := b[0]-gap-d, b[2]+gap
	pref, alt := after, before
	if m.FromMe() {
		pref, alt = before, after
	}
	fits := func(x float32) bool { return x >= bounds[0] && x+d <= bounds[2] }
	x := pref
	if !fits(x) {
		x = alt
	}
	if !fits(x) {
		return
	}
	y := theme.Snap((top+bot)/2 - d/2)
	step := d + e.Pt(4)
	if x < (b[0]+b[2])/2 {
		step = -step
	}
	canReply := !ch.ReadOnly() && !ch.Left() && m.Kind() != abi.ContentTagPhoneOnly && !s.revoking(e, m)
	rx := x + step
	canReply = canReply && fits(rx)
	// shown while the pointer is on the bubble or the controls
	in := c.Input()
	px, py := in.MouseX, in.MouseY
	lo, hi := min(b[0], x, rx), max(b[2], x+d, rx+d)
	if !canReply {
		lo, hi = min(b[0], x), max(b[2], x+d)
	}
	shown := in.Inside && px >= lo && px < hi && py >= top && py < bot
	name := c.Fmt()
	if m.FromMe() {
		name.Str(i18n.T("React to your message"))
	} else {
		fill(name, i18n.T("React to {name}'s message"), "{name}", s.senderStr(a, m))
	}
	if s.circle(e, ui.KeyN(item, 7400), s.icons.smile, name.String(), x-ir[0], y-ir[1], d, shown, r) {
		e.S.OpenReaction(ch.Ref(), m.ID(), state.Rect{X: x, Y: y, W: d, H: d})
	}
	if canReply && s.circle(e, ui.KeyN(item, 7401), s.icons.reply, i18n.T("Reply"), rx-ir[0], y-ir[1], d, shown, r) {
		pn := &e.S.Panes
		pn.Edit.Reset()
		pn.Reply.Reset()
		pn.Reply.Add(m.ID())
		e.S.Req.FocusComposer = true
		e.S.Touch()
	}
}

// circle is one of the round controls beside a message, at x, y in the
// message's box: a button that VoiceOver and the keyboard always reach,
// drawn only when shown or focused.
func (s *Screen) circle(e *screen.Env, key ui.Key, ic icon.ID, name string, x, y, d float32, shown bool, r *theme.Resolved) bool {
	c, k := e.C(), e.K.Kit
	focused := c.FocusKey() == key
	vis := shown || focused
	theme.At(c, x, y, d, d)
	c.NextRole(ui.RoleButton)
	c.NextSemName(name)
	c.NextCursor(ui.CursorPointer)
	// the glyph in the middle of the circle: its picture is larger than the
	// symbol, and a box of the circle's size would draw it from its corner
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	flags := base.Floating | ui.Clickable | ui.Focusable
	if vis {
		c.NextBG(theme.Over(r.Colors[theme.Fill1], r.Colors[theme.Raised]|0xFF))
		c.NextRadius(d / 2)
		c.NextShadow(ui.Shadow{OffY: e.Pt(2), Sigma: e.Pt(3), Color: ztheme.BubbleShadow()})
		flags |= ui.DrawBackground | ui.DrawShadow | ui.DrawHotEffects
	}
	id := c.BoxKeyed(flags, key, nil)
	if vis {
		c.Push(id)
		k.Icon(ic, r.Colors[theme.Label2])
		c.Pop()
		rexui.Tooltip(k, id, i18n.T("React"))
	}
	return c.Signal(id).Clicked
}

// ---- a call entry's menu (CV-033) ----

// callMenu is the menu of a call in the transcript: calling back, or in a
// group "Group calls happen on the phone"; it reports a choice.
func (s *Screen) callMenu(e *screen.Env, a *state.Account, ch model.Chat, m model.Message) bool {
	k := e.K.Kit
	group := ch.Kind() != abi.ChatKindDirect
	if !group && m.Decode(&s.dec) == nil {
		group = s.dec.Content.Call.Group
	}
	if group {
		itemOff(k, s.icons.call, i18n.T("Group calls happen on the phone"), false)
		return false
	}
	if item(k, s.icons.call, i18n.T("Voice call")) {
		calls.Place(e.S, a, ch.ID(), false)
		return true
	}
	if item(k, s.icons.video, i18n.T("Video call")) {
		calls.Place(e.S, a, ch.ID(), true)
		return true
	}
	return false
}

// ---- who reacted (CV-028) ----

// reactors is the tooltip of a reaction pill: who added emoji to m ("You,
// Ada"), worked out only while the pill is under the pointer.
func (s *Screen) reactors(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, emoji []byte) string {
	if m.Decode(&s.dec) != nil {
		return ""
	}
	f := e.C().Fmt()
	n := 0
	for i := range s.dec.Reactions {
		re := &s.dec.Reactions[i]
		if string(re.Emoji) != string(emoji) {
			continue
		}
		if n > 0 {
			f.Str(", ")
		}
		if re.FromMe {
			f.Str(i18n.T("You"))
		} else {
			f.Str(model.S(a.Model.DisplayName(re.Sender)))
		}
		n++
	}
	return f.String()
}

// ---- React from the viewer (VW-006) ----

// reactWaitFor is how long a reaction picker waits for its message to come
// into view.
const reactWaitFor = 5 * time.Second

// takeReaction takes a reaction picker opened with no anchor for a message
// of the open chat (the viewer's React: State.OpenReaction with a zero
// Rect, with the jump to the message): it opens beside the message's bubble
// once the jump has brought it on screen (reactBeside).
func (s *Screen) takeReaction(e *screen.Env, ch model.Chat) {
	t := &e.S.Panes.ReactTarget
	now := e.C().Input().Time
	if t.Open() && t.Anchor == (state.Rect{}) && t.Chat == ch.Ref() {
		s.reactWait = append(s.reactWait[:0], t.Msg.At(0)...)
		s.reactAt = now
		t.Reset()
	}
	if len(s.reactWait) > 0 && time.Duration(now-s.reactAt) > reactWaitFor {
		s.reactWait = s.reactWait[:0]
	}
}

// reactBeside opens the picker takeReaction holds beside m's bubble (b, in
// the window) when m is the message and its bubble is on screen.
func (s *Screen) reactBeside(e *screen.Env, ch model.Chat, m model.Message, b [4]float32) {
	if len(s.reactWait) == 0 || !sameID(s.reactWait, m.ID()) || b[2] <= b[0] {
		return
	}
	// on screen and settled: the jump has landed (a bubble still moving
	// changes the rect read from the last layout, and the framework runs
	// the frames it takes to settle)
	if mid := (b[1] + b[3]) / 2; mid < s.listR[1] || mid > s.listR[3] || b != s.reactLast {
		s.reactLast = b
		return
	}
	e.S.OpenReaction(ch.Ref(), m.ID(), state.Rect{X: b[0], Y: b[1], W: b[2] - b[0], H: b[3] - b[1]})
	s.reactWait = s.reactWait[:0]
}
