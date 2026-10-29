package picker

import (
	"bytes"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/emoji"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/picker.rs:20): the picker is WIDTH × HEIGHT
// inside a frame of FRAME_MARGIN.
const (
	widthPt   = 420
	heightPt  = 400
	marginPt  = 12
	maxFreq   = 36
	tabsGapPt = 8
)

// Screen is the picker.
type Screen struct {
	ic struct {
		clock, star, starOff, bubble, plus, trash, send, photo, link, doc, check, xmark, sticker icon.ID
	}
	fonts struct {
		scale      float32
		emoji, cat text.FontKey
	}

	tab  state.Picker // the tab shown, PickerNone for none
	acct uint64       // Account.Serial of the session the picker belongs to
	sel  int          // the tab strip's choice

	// react is the reaction picker this screen shows, a copy of
	// Panes.ReactTarget as it opened; frequentIdx are the table rows of
	// Settings.ReactionEmoji, most used first.
	react struct {
		on        bool
		chat      model.ChatRef
		msg       string
		anchor    screen.Rect
		has       bool
		restarted bool // opened or moved this frame
	}
	frequentIdx []int32

	em  emojiState
	gif gifState
	st  stickerState

	wantClose bool
	recentTmp []int32
}

var (
	_ screen.Overlay = (*Screen)(nil)
	_ state.Listener = (*Screen)(nil)
)

// New returns the picker.
func New() *Screen { return &Screen{} }

// Init readies it for a window: its icons, its buffers and the events it
// listens for.
func (s *Screen) Init(e *screen.Env) error {
	k := e.K
	emoji.Warm()
	pt := float32(15)
	add := func(sym string, fb rune) icon.ID { return k.AddIcon(icon.Spec{Symbol: sym, Fallback: fb, Pt: pt}) }
	s.ic.clock = add("clock", '◷')
	s.ic.star = add("star", '☆')
	s.ic.starOff = add("star.slash", '☆')
	s.ic.bubble = add("bubble.left", '✉')
	s.ic.plus = icon.Plus
	s.ic.trash = add("trash", '✕')
	s.ic.send = add("arrowshape.turn.up.right", '➦')
	s.ic.photo = add("photo", '▣')
	s.ic.link = add("arrow.up.right.square", '↗')
	s.ic.doc = add("doc.text", '▤')
	s.ic.check = icon.Check
	s.ic.xmark = icon.Close
	s.ic.sticker = add("face.smiling", '☺')
	s.em.init()
	s.gif.init()
	s.st.init()
	e.S.Listen(s, abi.EventTagGifs, abi.EventTagStickers)
	e.S.OnUnlinked(s, func(_ *state.State, a *state.Account) {
		if s.acct == a.Serial {
			s.switched(0) // the hook runs before the account's Serial rotates
		}
	})
	return nil
}

// Event takes the events the model does not keep: GIF results and the
// sticker lists. A hidden account's are dropped; the lists are asked for
// again when its picker opens.
func (s *Screen) Event(a *state.Account, ev *abi.Event) {
	if !a.Active() {
		return
	}
	if a.Serial != s.acct {
		s.switched(a.Serial)
	}
	switch ev.Tag {
	case abi.EventTagGifs:
		s.gif.apply(&ev.Gifs)
	case abi.EventTagStickers:
		s.st.apply(&ev.Stickers)
	}
}

// tabs are the strip's choices, in the order of state.Picker.
var tabNames = [3]string{"Emoji", "GIF", "Stickers"}

// Build builds the picker over the window while one is open; nothing
// otherwise.
func (s *Screen) Build(e *screen.Env) {
	a := e.Acct()
	if a == nil {
		return
	}
	p := &e.S.Panes
	if a.Serial != s.acct {
		s.switched(a.Serial)
	}
	s.react.restarted = false
	if t := &p.ReactTarget; t.Open() {
		if !s.react.on || s.reactMoved(t) {
			s.startReaction(e)
		}
		if !s.reactionAlive(a) {
			t.Reset()
		}
	}
	if s.react.on && !p.ReactTarget.Open() {
		s.react.on = false
		s.tab = state.PickerNone
	}
	if !s.react.on {
		if p.Picker != s.tab {
			s.opened(e, p.Picker)
		}
		if p.Picker == state.PickerNone {
			return
		}
	}
	k := e.K
	s.ensureFonts(e)

	anchor := s.anchor(e)
	// A press outside closes it and reaches what it landed on, as upstream's
	// does; the window losing the focus does not. A dialog the picker opened
	// (removing a pack) keeps it open.
	beh := rexui.PopoverSemitransient
	if e.S.Dialog.Open() {
		beh = rexui.PopoverApplicationDefined
	}
	name := i18n.T("Emoji picker") + "###picker"
	switch {
	case s.react.on:
		name = i18n.T("React to message") + "###picker"
	case s.tab == state.PickerGifs:
		name = i18n.T("GIF picker") + "###picker"
	case s.tab == state.PickerStickers:
		name = i18n.T("Sticker picker") + "###picker"
	}
	edge := rexui.PopoverAbove
	if s.react.on {
		edge = rexui.PopoverBelow
	}
	pop := rexui.BeginPopover(k.Kit, name, anchor, rexui.PopoverOpts{
		Edge: edge, Behavior: beh, Width: widthPt + 2*marginPt, NoPad: true,
	})
	s.wantClose = false
	s.panel(e, s.fit(e, anchor))
	rexui.EndPopover(k.Kit, &pop)
	if (pop.Dismissed && !s.react.restarted) || s.wantClose {
		s.closePicker(e, s.react.on)
	}
}

// switched forgets the previous session, including search caches, partial
// batches and import drafts. Only the window's icons and fonts survive.
func (s *Screen) switched(serial uint64) {
	for _, b := range [...]*ui.TextBuffer{&s.em.query, &s.gif.query, &s.gif.key, &s.st.search, &s.st.link, &s.st.packName} {
		b.Wipe()
	}
	*s = Screen{ic: s.ic, fonts: s.fonts, acct: serial}
	s.em.init()
	s.gif.init()
	s.st.init()
}

// anchor is the rectangle the picker opens against: the reaction target's
// control, or the composer's emoji button, in window pixels.
func (s *Screen) anchor(e *screen.Env) [4]float32 {
	if s.react.on && s.react.has {
		r := s.react.anchor
		return [4]float32{r.X, r.Y, r.X + r.W, r.Y + r.H}
	}
	if r := e.S.Panes.PickerAnchor; !s.react.on && !r.Empty() {
		return [4]float32{r.X, r.Y, r.X + r.W, r.Y + r.H}
	}
	// no anchor given: the foot of the conversation, where the composer's
	// button is
	c := e.K.Context()
	w, h := c.Size()
	W, H := float32(w), float32(h)
	if s.react.on {
		return [4]float32{W/2 - e.Pt(17), H/3 - e.Pt(17), W/2 + e.Pt(17), H/3 + e.Pt(17)}
	}
	x := e.Pt(52) + e.Pt(max(e.S.Settings.SidebarWidth, 240))
	if e.S.Panes.ListHidden {
		x = e.Pt(52)
	}
	x += e.Pt(24)
	y := H - e.Pt(52) + e.Pt(9)
	return [4]float32{x, y, x + e.Pt(34), y + e.Pt(34)}
}

// ensureFonts makes the emoji grid's fonts for the window's scale.
func (s *Screen) ensureFonts(e *screen.Env) {
	sc := e.K.Scale()
	if s.fonts.scale == sc {
		return
	}
	ts := e.K.TextSystem()
	s.fonts.scale = sc
	s.fonts.emoji = ts.Font(text.FontDesc{SizePt: 24, Weight: theme.WeightRegular, Scale: sc})
	s.fonts.cat = ts.Font(text.FontDesc{SizePt: 16, Weight: theme.WeightRegular, Scale: sc})
}

// opened is a tab opening (Action::TogglePicker): the search clears, the
// emoji tab takes the keyboard, and the lists a tab shows are asked for.
func (s *Screen) opened(e *screen.Env, tab state.Picker) {
	s.tab = tab
	switch tab {
	case state.PickerEmoji:
		s.em.reset()
		s.snapshotRecent(e)
	case state.PickerGifs:
		s.gif.reset()
		if len(s.gif.results) == 0 && bytes.TrimSpace(e.S.Settings.GiphyKey) != nil && len(bytes.TrimSpace(e.S.Settings.GiphyKey)) > 0 {
			s.searchGifs(e, "")
		}
	case state.PickerStickers:
		s.stickersOpened(e)
	}
	s.sel = int(tab) - 1
	e.S.Touch()
}

// snapshotRecent keeps the recent emoji as they stand as the emoji picker
// opens: the row keeps its order while the picker is open, so the same
// emoji can be sent again where it was.
func (s *Screen) snapshotRecent(e *screen.Env) {
	s.em.recent = recentIndexes(s.em.recent, e.S.Settings.RecentEmoji)
}

// reactMoved reports whether the reaction target is another message or
// place than the picker opened for.
func (s *Screen) reactMoved(t *state.ReactTarget) bool {
	return t.Chat != s.react.chat || string(t.Msg.At(0)) != s.react.msg || t.Anchor != s.react.anchor
}

// startReaction opens the picker as the reaction picker for
// Panes.ReactTarget (Action::OpenReactionPicker).
func (s *Screen) startReaction(e *screen.Env) {
	t := &e.S.Panes.ReactTarget
	e.S.Panes.Picker = state.PickerNone
	s.react.on, s.react.chat, s.react.msg = true, t.Chat, string(t.Msg.At(0))
	s.react.anchor, s.react.has = t.Anchor, !t.Anchor.Empty()
	s.react.restarted = true
	s.tab = state.PickerEmoji
	s.em.reset()
	s.frequentIdx = s.frequentIdx[:0]
	tab := emojis()
	for _, f := range e.S.Settings.ReactionEmoji {
		if i := tab.Get(string(f.Emoji)); i >= 0 {
			s.frequentIdx = append(s.frequentIdx, i)
		}
	}
	e.S.Touch()
}

// reactionAlive reports whether the reaction target is still there: its
// chat is open and its message loaded.
func (s *Screen) reactionAlive(a *state.Account) bool {
	if a.Open != s.react.chat {
		return false
	}
	return s.reactMessage(a) >= 0
}

// reactMessage is the index of the target in its conversation, -1 for none.
func (s *Screen) reactMessage(a *state.Account) int {
	ch, ok := a.Model.Chats.Get(s.react.chat)
	if !ok {
		return -1
	}
	v := a.Model.Conversation(ch.ID())
	if v == nil {
		return -1
	}
	return v.Find([]byte(s.react.msg))
}

// closePicker closes the picker (Action::ClosePicker); the composer takes
// the keyboard back unless it was the reaction picker.
func (s *Screen) closePicker(e *screen.Env, wasReaction bool) {
	e.S.Panes.Picker = state.PickerNone
	s.tab = state.PickerNone
	s.react.on = false
	if wasReaction {
		e.S.Panes.ReactTarget.Reset()
	}
	s.em.jump = -1
	if !wasReaction {
		e.S.Req.FocusComposer = true
	}
	e.S.Touch()
}

// fit is the picker's height in pixels: its own, or less in a short window,
// the room on the anchor's roomier side less the popover's arrow and a
// margin, so the tabs stay inside and the body scrolls in what is left
// (at the 600 × 400 minimum the panel would run off the window).
func (s *Screen) fit(e *screen.Env, anchor [4]float32) float32 {
	_, h := e.K.Context().Size()
	room := max(anchor[1], float32(h)-anchor[3]) - e.Pt(popRoomPt)
	return max(min(e.Pt(heightPt+2*marginPt), room), e.Pt(minHeightPt))
}

// The popover's arrow and gap with a margin from the window's edge, and the
// least height the picker takes, in points.
const popRoomPt, minHeightPt = 28, 160

// panel builds the picker's content, height pixels tall: the tab's body
// over the tab strip.
func (s *Screen) panel(e *screen.Env, height float32) {
	k := e.K
	c := k.Context()
	k.NextPad(marginPt)
	k.NextGap(tabsGapPt)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "picker", ui.Px(e.Pt(widthPt+2*marginPt)), ui.Px(height), 0)

	k.NextGap(tabsGapPt)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "body", ui.Pct(1), ui.Pct(1), 0)
	var picked string
	switch {
	case s.react.on:
		s.reactionHeader(e)
		picked = s.emojiBody(e, true)
	case s.tab == state.PickerEmoji:
		picked = s.emojiBody(e, false)
	case s.tab == state.PickerGifs:
		s.gifBody(e)
	case s.tab == state.PickerStickers:
		s.stickerBody(e)
	}
	k.EndContainer()

	if !s.react.on {
		// the tabs stay at the bottom
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextAxis(ui.AxisX)
		c.NextJustify(ui.JustifyCenter)
		c.Push(c.Box(0, "tabs"))
		s.sel = int(s.tab) - 1
		if rexui.SegmentedControl(k.Kit, i18n.T("Picker tab"), &s.sel, s.tabLabels(), rexui.SegmentedOpts{Role: rexui.SegmentedTabs}).Changed {
			s.opened(e, state.Picker(s.sel+1))
			e.S.Panes.Picker = s.tab
		}
		c.Pop()
	}
	k.EndContainer()
	if picked != "" {
		s.choose(e, picked)
	}
}

var tabLabelsBuf [3]string

func (s *Screen) tabLabels() []string {
	for i, n := range tabNames {
		tabLabelsBuf[i] = i18n.T(n)
	}
	return tabLabelsBuf[:]
}

// reactionHeader is the reaction picker's title, its close button and a
// line naming the message.
func (s *Screen) reactionHeader(e *screen.Env) {
	k := e.K
	c := k.Context()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(0, "reaction title"))
	k.NextWidth(ui.Pct(1))
	rexui.Label(k.Kit, i18n.T("React to message"), rexui.LabelOpts{Style: rexui.TextSemibold})
	if rexui.IconButton(k.Kit, s.ic.xmark, i18n.T("Close reactions"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		s.wantClose = true
	}
	c.Pop()
	if line := s.reactionPreview(e); len(line) > 0 {
		rexui.LabelBytes(k.Kit, line, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	}
}

// reactionPreview is "name: first line of the message", built in the
// frame's arena.
func (s *Screen) reactionPreview(e *screen.Env) []byte {
	a := e.Acct()
	i := s.reactMessage(a)
	if i < 0 {
		return nil
	}
	ch, _ := a.Model.Chats.Get(s.react.chat)
	v := a.Model.Conversation(ch.ID())
	m := v.At(i)
	t := m.Text()
	if n := bytes.IndexByte(t, '\n'); n >= 0 {
		t = t[:n]
	}
	if len(t) == 0 {
		return nil
	}
	name := m.SenderName()
	if len(name) == 0 {
		name = a.Model.DisplayName(m.Sender())
	}
	f := e.C().Fmt()
	f.Str(model.S(name)).Str(": ").Str(model.S(t))
	return f.Bytes()
}

// choose is an emoji picked: it goes to the composer, or is the reaction.
func (s *Screen) choose(e *screen.Env, glyph string) {
	if s.react.on {
		s.react1(e, glyph)
		return
	}
	// Action::InsertEmoji: the composer inserts it, and it is a recent one
	e.S.InsertEmoji([]byte(glyph))
}

// react1 sends the reaction: choosing the emoji we already reacted with
// takes it back (conversation::reaction_choice).
func (s *Screen) react1(e *screen.Env, glyph string) {
	a := e.Acct()
	ch, ok := a.Model.Chats.Get(s.react.chat)
	if !ok {
		return
	}
	v := a.Model.Conversation(ch.ID())
	i := s.reactMessage(a)
	if v == nil || i < 0 {
		return
	}
	m := v.At(i)
	own := ""
	for j := 0; j < m.Reactions(); j++ {
		if em, _, mine := m.Reaction(j); mine {
			own = string(em)
			break
		}
	}
	send := glyph
	if own == glyph {
		send = ""
	}
	a.React(ch.ID(), m.ID(), []byte(send))
	s.closePicker(e, true)
}

// quote fills the reply a send carries from the composer's reply, and takes
// it (App::reply_to.take()).
func (s *Screen) quote(e *screen.Env, q *abi.ReplyTarget, has *bool) {
	p := &e.S.Panes
	if p.Reply.Len() == 0 {
		return
	}
	q.ID, *has = append(q.ID[:0], p.Reply.At(0)...), true
	p.Reply.Reset()
}

// afterSend closes the picker and brings the new message into view
// (follow_outgoing, refocus_composer).
func (s *Screen) afterSend(e *screen.Env) {
	s.closePicker(e, false)
	e.S.Req.JumpToBottom = true
}

// openURL opens a link in the browser through the state's Shell, off the
// main thread (opening can take seconds).
func (s *Screen) openURL(e *screen.Env, url string) {
	if sh := e.S.Shell; sh != nil {
		go sh.Open(url)
	}
}
