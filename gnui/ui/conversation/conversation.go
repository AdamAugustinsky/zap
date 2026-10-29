package conversation

import (
	"bytes"
	"gonativeui/img"
	"time"

	"gonativeui/icon"
	"gonativeui/menu"
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
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Geometry, in points (zf:src/ui/conversation.rs).
const (
	sidePt     = 16               // the transcript's inset from the pane's sides
	endPt      = 10               // the room under the newest message, over the composer
	estimatePt = 56               // a message never measured: a two-line bubble
	pinBarPt   = 46               // the pinned-message bar
	selBarPt   = 52               // the selection-mode bar
	olderAt    = 6                // ask for older history when the first built item is this close to the top
	editWindow = 15 * 60          // seconds a sent text can be edited (zf:src/app.rs EDIT_WINDOW)
	revokeWin  = 2 * 24 * 60 * 60 // seconds a message can be deleted for everyone (REVOKE_WINDOW)
)

// Screen is the conversation: the header and the transcript.
type Screen struct {
	h  *hub
	t  transcript
	sc ui.SelectionScope
	vl base.VList // the last frame's list (First, Last, Len)

	// the open chat, as the last frame had it
	acct uint64
	ref  model.ChatRef
	// the chat whose messages are awaited, since when (spinnerDue)
	loadRef  model.ChatRef
	loadFrom int64
	// unread is how many were unread when it opened; unreadAt the item of
	// the first of them once the transcript has it (0: none).
	unread    int
	unreadAt  uint64
	unreadSet bool
	// dropped is how many message records the core could not send when the
	// chat opened (droppedMessages).
	dropped uint64

	// flash is the message a jump showed (Panes.Jump), flashing from
	// flashAt (Input.Time) for state.JumpFlash; strength is this frame's.
	flash    uint64
	flashAt  int64
	strength float32

	menu struct {
		open   bool
		msg    model.IDList
		anchor [4]float32
	}
	one      model.IDList // one message's id, for the reducers that take a list
	quickBuf [][]byte     // the menu's quick reactions
	more     bool         // the header's More menu
	delMenu  bool         // selection mode's Delete… menu
	delAt    [4]float32   // where a right click on a selected message opened it
	narrow   bool         // the conversation has the window's whole width
	lastNew  uint64       // the newest message's item, as announce last saw it
	// a number's menu, opened from a message's text
	phone struct {
		open            bool
		at              [4]float32
		written, digits []byte
	}
	jid        []byte
	phoneStop  int
	phoneRects [][4]float32

	pinShown int // the pin the bar shows, newest first
	// the open chat's live pins this frame (livePins)
	pin     abi.Pinned
	pinIdx  [8]int
	pinAt   [8]int64
	pinN    int
	pinSoon int64
	// the chat's pins oldest first, for their notices (collectNotices)
	noteIdx [8]int
	noteAt  [8]int64
	noteN   int
	sticky  stickyState
	pics    map[uint64]senderPicEnt // group senders' pictures
	// a reaction picker asked for beside a message not on screen yet
	// (takeReaction)
	reactWait  []byte
	reactAt    int64
	reactLast  [4]float32
	typers     [][]byte
	typerNames [3]string // typingPics's, kept to build without allocating
	typerPics  [3]img.Handle
	span       float32 // the bubble being built's settled width (settle)
	// topInset is how far below the transcript's top its content starts at
	// rest: the pane's title row, the pinned message, the notice over it
	topInset float32
	// flush: the bubble being built is a picture alone, which fills it to
	// its edges at its radius (flushLook: the bubble's look without padding)
	flush     bool
	flushLook rexui.BubbleRecipe
	// its text's widest line and last line, laid out (inlineFoot)
	textW, lineW float32
	// frame is the window's frame the transcript was last built in; gap
	// says frames went by without it
	frame uint32
	gap   bool
	// pickShift: Shift was held when a selection row was pressed
	pickShift bool
	// albums: each message's album, and the one being built
	albums  [512]albumEnt
	adec    abi.Message
	albV    *model.Conversation
	albI    int
	albN    int
	av      avatarState
	avPaths avatarPaths
	mem     memberNames

	look  look
	icons icons

	// scratch, reused for every message
	shownMessage model.Message
	shownReady   bool
	spans        []markup.Span
	body         []byte
	rich         []ui.Span
	ments        []markup.Mention
	mentID       [][]byte
	dec          abi.Message
	name         []byte // a person's name (personName)
	buf          []byte
	link         []byte
	copied       []byte

	colPt   float32 // the transcript's column width, in points
	maxW    float32 // the widest a bubble's text may be, in pixels
	listR   [4]float32
	now     time.Time
	unix    int64
	copyID  menu.ID
	hasCopy bool
}

var _ screen.ContentPane = (*Screen)(nil)

// look is what the bubbles are drawn with, rebuilt when the theme or the
// scale changes.
type look struct {
	tune     uint32 // the tunables' Gen the fonts were made at (ztheme.T.BubbleTimePt)
	scale    float32
	colors   [3]uint32     // secondary, link and mention
	rich     ui.RichStyles // the text's styles, alike on both sides' bubbles
	small    text.FontKey  // the footer's
	time     text.FontKey  // its time, in Rex's metadata face (monospaced)
	smallMed text.FontKey  // its "Not sent"
	badge    text.FontKey  // a count badge's (semibold 11)
	jumbo    text.FontKey
	ok       bool
}

type icons struct {
	call, video, hangup, search, more icon.ID
	down, reply, close, pin, star     icon.ID
	forward, trash, copy, clock       icon.ID
	info, check, archive, open        icon.ID
	folder, photo, edit, eraser, plus icon.ID
	back, pinSmall, smile             icon.ID
	ticks                             tickIcons // the footer's delivery marks
	ok                                bool
}

// New returns the conversation.
func New() *Screen {
	s := &Screen{}
	s.t.reserve(256)
	s.spans = make([]markup.Span, 0, 256)
	s.body = make([]byte, 0, 4096)
	s.rich = make([]ui.Span, 0, 256)
	s.ments = make([]markup.Mention, 0, 16)
	s.mentID = make([][]byte, 0, 16)
	s.buf = make([]byte, 0, 512)
	s.quickBuf = make([][]byte, 0, len(quickReactions)+1)
	return s
}

// Init readies the conversation for a window: its icons and the events it
// keeps.
func (s *Screen) Init(e *screen.Env) error {
	s.h = hubOf(e.S)
	s.h.listen()
	s.addIcons(e)
	if m := e.K.Kit.Menu(); m != nil {
		s.copyID, s.hasCopy = m.RoleID(menu.RoleCopy)
	}
	return nil
}

func (s *Screen) addIcons(e *screen.Env) {
	if s.icons.ok {
		return
	}
	k := e.K
	ic := &s.icons
	// Keep registration order: IDs are positions in the window's atlas.
	for _, it := range [...]struct {
		dst *icon.ID
		sym string
		fb  rune
	}{
		{&ic.call, "phone", '☎'}, {&ic.video, "video", '▶'}, {&ic.hangup, "phone.down.fill", '✆'},
		{&ic.search, "magnifyingglass", '⌕'}, {&ic.more, "ellipsis", '…'},
	} {
		*it.dst = k.AddIcon(rexui.ToolbarSymbol(it.sym, it.fb))
	}
	ic.reply = k.AddIcon(ztheme.ToolIcon("arrowshape.turn.up.left", '↩'))
	ic.close = k.AddIcon(ztheme.ToolIcon("xmark", '×'))
	// The Newest message button, 0.46 of its 40 pt.
	ic.down = k.AddIcon(icon.Spec{Symbol: "arrow.down", Fallback: '↓', Pt: newestPt * 0.46})
	for _, it := range [...]struct {
		dst *icon.ID
		sym string
		fb  rune
	}{
		{&ic.pin, "pin.fill", '⚲'}, {&ic.star, "star.fill", '★'}, {&ic.forward, "arrowshape.turn.up.right", '↪'},
		{&ic.trash, "trash", '⌫'}, {&ic.copy, "doc.on.doc", '⎘'}, {&ic.clock, "arrow.clockwise", '↻'},
		{&ic.info, "info.circle", 'ⓘ'}, {&ic.check, "checkmark.circle", '✓'}, {&ic.archive, "archivebox", '▤'},
		{&ic.open, "arrow.up.forward.app", '↗'}, {&ic.folder, "folder", '▭'}, {&ic.photo, "photo", '▣'},
		{&ic.edit, "pencil", '✎'}, {&ic.eraser, "eraser", '⌧'}, {&ic.plus, "plus", '+'},
		{&ic.back, "chevron.left", '‹'},
	} {
		*it.dst = k.AddIcon(ztheme.ToolIcon(it.sym, it.fb)) // the bars' items, as the header's
	}
	ic.pinSmall = k.AddIcon(icon.Spec{Symbol: "pin.fill", Fallback: '⚲', Pt: footMarkPt})
	ic.smile = k.AddIcon(icon.Spec{Symbol: "face.smiling", Fallback: '☺', Pt: 16})
	ic.ticks = addTickIcons(k)
	ic.ok = true
}

// styles rebuilds the bubbles' rich-text styles when the theme moved.
func (s *Screen) styles(e *screen.Env) {
	k := e.K.Kit
	r := k.Theme()
	lk := &s.look
	sc := k.Scale()
	colors := [3]uint32{r.Colors[theme.Label2], r.Colors[theme.Link], r.Colors[theme.Accent]}
	if lk.ok && lk.scale == sc && lk.colors == colors && lk.tune == ztheme.Gen {
		return
	}
	ts := k.TextSystem()
	font := func(w int, italic, mono bool, size float32) text.FontKey {
		return ts.Font(text.FontDesc{SizePt: size, Weight: w, Italic: italic, Mono: mono, Scale: sc})
	}
	// Rex's metadata: the UI face with tabular figures (ztheme.Meta); a
	// bubble's time a step under the list's, on its last line's baseline
	meta := func(size float32) text.FontKey {
		return ts.Font(text.FontDesc{SizePt: size, TabularDigits: true, Scale: sc})
	}
	f := markup.Fonts{
		Regular: r.Styles[theme.StyleBody].Font, Bold: font(700, false, false, 13), Italic: font(400, true, false, 13),
		BoldItalic: font(700, true, false, 13), Mono: font(400, false, true, 13*0.95),
	}
	lk.rich = markup.RichStyles(f, markup.Colors{Secondary: colors[0], Link: colors[1], Mention: colors[2]})
	lk.small = font(400, false, false, 11)
	lk.time = meta(ztheme.T.BubbleTimePt)
	lk.smallMed = font(500, false, false, 11)
	lk.badge = font(600, false, false, 11)
	lk.jumbo = font(400, false, false, 13*text.JumboScale)
	lk.scale, lk.colors, lk.tune, lk.ok = sc, colors, ztheme.Gen, true
}

// Build builds the transcript at r.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	c := e.C()
	a := e.Acct()
	mkey.Enter(a) // the caches of the messages are the account's and the open chat's
	ch, ok := model.Chat{}, false
	if a != nil {
		ch, ok = a.OpenChatView()
	}
	if !ok {
		e.S.Req.FindInChat, e.S.Req.JumpToBottom = false, false
		return
	}
	if s.h == nil {
		s.Init(e)
	}
	s.styles(e)
	fr := e.C().Frame()
	s.gap, s.frame = s.frame != 0 && fr != s.frame+1, fr
	s.now = e.S.Now()
	s.unix = s.now.Unix()
	s.enter(e, a, ch)
	v := a.Model.Conversation(ch.ID())
	s.t.sync(v)
	s.requests(e, a)
	s.older(e, a, v)
	// the unread divider's scroll first: a jump that comes with the history
	// is the one to keep
	s.findUnread(e, v)
	s.jumpTo(e, v)
	s.anchor(e, v)
	s.flashing(e)
	s.takeReaction(e, ch)
	s.announce(e, a, ch, v)
	s.keys(e, a, ch, v)

	// beside no list or rail: a narrow window (the header's Back)
	s.narrow = r.X <= 0 || e.Narrow()
	s.colPt = max(r.W/e.K.Scale()-2*ztheme.T.TranscriptSide, 120)
	s.maxW = max(theme.Snap(e.Pt(s.colPt)*rexui.BubbleLook.MaxShare)-2*e.Pt(rexui.BubbleLook.PadX), e.Pt(24))

	// The transcript draws no background: the pane behind it shows through.
	// It runs from the pane's top, under its title row (ContentInset) and the
	// pinned message and the notice under that, which build after it, over
	// it: at rest its content starts below them (topInset, the history's top
	// block's), and what scrolls up under them is blurred, easing off below
	// them (the soft scroll edge).
	e.Begin(r, "conversation")
	inset := e.ContentInset()
	pins := s.livePins(a, ch.ID()) > 0
	// EventDropped names no chat: a message record dropped since the chat
	// opened, its pages' or a live one's, shows a notice over the
	// transcript, never a message attributed to the chat.
	dropped := droppedMessages(a) > s.dropped
	top := inset
	if pins {
		top += e.Pt(pinBarPt)
	}
	if dropped {
		top += e.Pt(28)
	}
	s.topInset = top
	theme.At(c, 0, 0, r.W, r.H)
	c.NextSize(ui.Px(r.W), ui.Px(r.H))
	c.Push(c.Box(base.Floating, "transcript"))
	under := false
	if v == nil || v.Len() == 0 {
		s.empty(e, ch.Ref(), v, r.W, r.H)
	} else {
		s.loadRef = 0
		s.list(e, a, ch, v, r.W, r.H)
		_, sy := c.Scroll(s.vl.Body)
		under = sy > 0.5
	}
	c.Pop()
	rexui.SoftScrollEdge(e.K.Kit, ui.HashString(c.ParentKey(), "soft edge"), r.W, top, e.Pt(ztheme.T.SoftFade), e.K.Kit.Theme().Colors[theme.Pane], under && ztheme.T.SoftBlur > 0, 0)
	y := inset
	if pins {
		s.pinBar(e, a, ch, r.W, y)
		y += e.Pt(pinBarPt)
	}
	if dropped {
		theme.At(c, 0, y, r.W, e.Pt(28))
		c.Push(c.Box(base.Floating, "dropped records"))
		rexui.DateDivider(e.K.Kit, i18n.T("Some records could not be shown"), rexui.DateDividerOpts{Pill: true})
		c.Pop()
	}
	if s.menu.open {
		s.contextMenu(e, a, ch, v)
	}
	if s.phone.open {
		s.phoneMenu(e, a)
	}
	e.End()
}

// droppedMessages is how many message records the core could not send.
func droppedMessages(a *state.Account) uint64 {
	return a.Model.DroppedOf(abi.EventTagMessages) + a.Model.DroppedOf(abi.EventTagMessageUpdated)
}

// enter resets what belongs to one open chat when another opens.
func (s *Screen) enter(e *screen.Env, a *state.Account, ch model.Chat) {
	if a.Serial == s.acct && ch.Ref() == s.ref {
		return
	}
	s.acct, s.ref = a.Serial, ch.Ref()
	s.t.conv = nil
	s.unread, s.unreadAt, s.unreadSet = a.UnreadAtOpen(ch.Ref()), 0, false
	s.dropped = droppedMessages(a)
	s.flash, s.flashAt = 0, 0
	s.lastNew = 0
	s.sticky = stickyState{}
	s.reactWait = s.reactWait[:0]
	s.menu.open, s.more = false, false
	s.sc.Clear()
	h := s.h
	h.hist = history{acct: a.Serial, chat: ch.Ref(), first: true}
	h.search.leave(a.Serial, ch.Ref())
}

// requests acts on the shell's one-shot asks.
func (s *Screen) requests(e *screen.Env, a *state.Account) {
	rq := &e.S.Req
	if rq.FindInChat {
		rq.FindInChat = false
		s.openSearch(e)
	}
	if rq.JumpToBottom {
		rq.JumpToBottom = false
		s.toBottom()
	}
}

func (s *Screen) openSearch(e *screen.Env) {
	p := &e.S.Panes
	if p.Pane != state.PaneSearch {
		p.Pane = state.PaneSearch
		e.S.Touch()
	}
	s.h.search.focus = true
}

func (s *Screen) toBottom() {
	if n := s.t.st.Len(); n > 0 {
		s.t.st.ScrollTo(n-1, base.AlignEnd)
	}
}

// older asks for history before the oldest loaded message when the top of
// the transcript is near (zf:src/app.rs, load_older and fetch_older):
// Account.LoadOlder pages the archive while it has more, else asks the
// phone, connected, at most every state.PhoneCooldown·2^misses. The state
// asks for a bare chat's history itself.
func (s *Screen) older(e *screen.Env, a *state.Account, v *model.Conversation) {
	if v == nil || v.Len() == 0 || s.t.st.Pending() || e.S.Panes.Jump.Msg.Len() > 0 {
		return
	}
	if s.vl.Len == 0 || s.vl.First > olderAt {
		return
	}
	// Only a transcript the reader scrolled up through is their ask; a
	// short one asks by itself.
	view := float64(s.listR[3] - s.listR[1])
	a.LoadOlder(s.t.st.Total() > view+1)
}

// phoneFetching reports whether the phone was asked for v's older history
// and has not answered.
func phoneFetching(v *model.Conversation) bool {
	if v == nil {
		return false
	}
	on, _ := v.FetchingPhone()
	return on
}

// jumpTo brings the message Panes.Jump names into view once the transcript
// has it, flashes it (CV-017) and clears the request; the state pages
// toward one not loaded yet.
func (s *Screen) jumpTo(e *screen.Env, v *model.Conversation) {
	j := &e.S.Panes.Jump
	if j.Msg.Len() == 0 || j.Chat != s.ref || v == nil {
		return
	}
	i := v.Find(j.Msg.At(0))
	if i < 0 || i >= len(s.t.ids) {
		return
	}
	item := s.t.ids[i]
	s.t.st.ScrollToID(item, base.AlignCenter)
	s.flash, s.flashAt = item, e.C().Input().Time
	j.Msg.Reset()
	e.S.Touch()
}

// anchor clears Panes.Anchor once its message is in and no page is on its
// way: the transcript kept it in place as the page came in (VList.Splice),
// and a jump's anchor is the jump's to scroll to.
func (s *Screen) anchor(e *screen.Env, v *model.Conversation) {
	p := &e.S.Panes
	if p.Anchor.Len() == 0 || v == nil || v.LoadingOlder() || phoneFetching(v) || v.Find(p.Anchor.At(0)) < 0 {
		return
	}
	p.Anchor.Reset()
}

// findUnread places the unread divider before the first of the messages
// that were unread when the chat opened, and scrolls there once.
func (s *Screen) findUnread(e *screen.Env, v *model.Conversation) {
	if s.unreadSet || v == nil || v.Len() == 0 {
		return
	}
	s.unreadSet = true
	if s.unread <= 0 {
		return
	}
	n := 0
	for i := v.Len() - 1; i >= 0; i-- {
		m := v.At(i)
		if m.FromMe() {
			continue
		}
		n++
		if n == s.unread {
			s.unreadAt = s.t.ids[i]
			if j := &e.S.Panes.Jump; j.Msg.Len() == 0 || j.Chat != s.ref {
				s.t.st.ScrollToID(s.unreadAt, base.AlignStart)
			}
			return
		}
	}
}

// keys takes the transcript's own keys: Escape leaves selection mode and
// closes the menus, PgUp and PgDn scroll a page, ⌘C copies the selected
// messages (KB-019, KB-020).
func (s *Screen) keys(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation) {
	c := e.C()
	p := &e.S.Panes
	if s.hasCopy && e.K.Kit.Command(s.copyID) {
		// Edit ▸ Copy that no field took: selection mode's messages, or the
		// transcript's text selection
		if p.Selecting {
			s.copySelected(e, a, ch, v)
		} else {
			s.copyText(e, a, ch, v)
		}
	}
	// PgUp and PgDn no text field takes (KB-020)
	page := float64(s.listR[3]-s.listR[1]) * 0.9
	if c.TakeKey(ui.KeyPageUp, 0) {
		s.t.st.ScrollBy(-page)
	}
	if c.TakeKey(ui.KeyPageDown, 0) {
		s.t.st.ScrollBy(page)
	}
	// Built before the composer: close its search calendar before Escape
	// reaches suggestions, attachments, edit or reply, even with editor focus.
	if s.h.search.calendar && p.Pane == state.PaneSearch && !c.ModalOpen() &&
		!e.S.Dialog.Open() && !e.S.InUse() && p.Picker == state.PickerNone && !p.ReactTarget.Open() &&
		!p.Selecting && a.Mode != state.ListStarred && !s.menu.open && !s.more && !s.delMenu && c.Input().NPreedit == 0 {
		for i := 0; i < c.KeyCount(); i++ {
			key, claimed := c.KeyAt(i)
			if key.Down && key.Code == ui.KeyEscape && key.Mods == 0 && (!claimed || c.TextFieldFocused()) {
				c.ClaimKey(i)
				// Drawn fields preclaim Escape at Begin and still read it
				// after ClaimKey. Hosted editors instead observe the claim.
				if c.TextFieldFocused() {
					c.Input().Keys[i].Down = false
				}
				s.h.search.calendar = false
				e.S.Touch()
				break
			}
		}
	}
	if c.TextFieldFocused() {
		return
	}
	if (s.menu.open || s.more || s.delMenu) && c.TakeKey(ui.KeyEscape, 0) {
		s.menu.open, s.more, s.delMenu = false, false, false
	}
	if p.Selecting && c.TakeKey(ui.KeyEscape, 0) {
		logic.CancelSelection(e.S)
	}
	// Home and End, with no field focused: the top and the bottom (KB-021)
	if c.TakeKey(ui.KeyHome, 0) && s.t.st.Len() > 0 {
		s.t.st.ScrollTo(0, base.AlignStart)
	}
	if c.TakeKey(ui.KeyEnd, 0) {
		s.toBottom()
	}
}

// empty is a conversation with no messages loaded: upstream's
// top_of_history alone.
func (s *Screen) empty(e *screen.Env, ref model.ChatRef, v *model.Conversation, w, h float32) {
	c, k := e.C(), e.K.Kit
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(0, s.topInset+e.Pt(24), 0, 0)
	c.NextGap(e.Pt(8))
	c.Push(c.Box(0, "empty"))
	switch {
	case phoneFetching(v):
		rexui.Spinner(k, i18n.T("Loading messages from your phone…"), rexui.SpinnerOpts{})
		rexui.Label(k, i18n.T("Loading messages from your phone…"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	case v == nil || (!v.Complete() && s.h.hist.first):
		if s.spinnerDue(e, ref) {
			rexui.Spinner(k, i18n.T("Loading…"), rexui.SpinnerOpts{})
		}
	default:
		rexui.DateDivider(k, i18n.T("No messages here yet"), rexui.DateDividerOpts{Pill: true})
	}
	c.Pop()
}

// spinnerDelayNs is how long a chat's messages may take to come before
// "Loading…" spins: the archive's answer usually comes sooner, and a spinner
// that flashes for a frame or two reads as a stutter.
const spinnerDelayNs = 250_000_000

// spinnerDue reports whether chat ref has been loading for spinnerDelayNs.
func (s *Screen) spinnerDue(e *screen.Env, ref model.ChatRef) bool {
	c := e.C()
	now := c.Input().Time
	if now == 0 {
		return true // no clock
	}
	if s.loadRef != ref {
		s.loadRef, s.loadFrom = ref, now
	}
	if left := s.loadFrom + spinnerDelayNs - now; left > 0 {
		c.WakeIn(left)
		return false
	}
	return true
}

// list is the transcript: one VList item per loaded message, in a selection
// scope (C1, C4).
func (s *Screen) list(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation, w, h float32) {
	c := e.C()
	s.phoneStop = 0
	o := base.VListOpts{Estimate: estimatePt, StickToBottom: true, BottomAlign: true, W: ui.Px(w), H: ui.Px(h)}
	s.collectNotices(a, ch.ID())
	vl := base.BeginVList(c, "Messages", &s.t.st, o)
	s.listR = c.RectOf(c.KeyOf(vl.Outer))
	// In selection mode a click anywhere on a row ticks it (SL-002): the
	// text is not selectable then, so it takes no press.
	picking := e.S.Panes.Selecting
	var sig ui.ScopeSignal
	if !picking {
		sig = c.BeginSelection(&s.sc, ui.ScopeOpts{Items: &s.t.st, View: vl.Outer})
	}
	for i := vl.First; i < vl.Last; i++ {
		id := vl.Item(i)
		mi := s.t.index(id)
		if mi < 0 {
			continue
		}
		s.message(e, a, ch, v, mi, id)
	}
	if !picking {
		c.EndSelection()
	}
	base.EndVList(&vl)
	s.vl = vl
	if sig.Copy {
		s.copyText(e, a, ch, v)
	}
	s.stickyDay(e, v, w)
	if !vl.AtEnd && vl.Len > 0 {
		s.newest(e, ch, w, h)
	}
}

// announce has VoiceOver read a message that arrives from someone else at
// the end of the open chat (AX-003): "Ada: hello". The chat's first page and
// older history are not news.
func (s *Screen) announce(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation) {
	if v == nil || v.Len() == 0 || len(s.t.ids) == 0 {
		return
	}
	newest := s.t.ids[len(s.t.ids)-1]
	if newest == s.lastNew {
		return
	}
	// What came while the transcript was not built (the lock screen, the
	// login, the Calls page) is not news once it is again.
	known := s.lastNew != 0 && !s.h.hist.first && !s.gap
	s.lastNew = newest
	m := v.At(v.Len() - 1)
	if !known || m.FromMe() || s.hidden(e, m) {
		return
	}
	f := e.C().Fmt().Str(s.senderStr(a, m)).Str(": ")
	s.summary(f, m)
	// the state's queue: it waits behind errors, goes stale, and stays
	// silent while the app is locked
	e.S.Announce(state.AnnounceMessage, f.String())
}

// flashing moves the jump flash on: state.FlashStrength over
// state.JumpFlash, a frame each while it lasts.
func (s *Screen) flashing(e *screen.Env) {
	s.strength = 0
	if s.flash == 0 {
		return
	}
	c := e.C()
	elapsed := time.Duration(c.Input().Time - s.flashAt)
	if elapsed >= state.JumpFlash {
		s.flash = 0
		return
	}
	s.strength = state.FlashStrength(elapsed)
	c.RequestFrame()
}

// newest is the button that scrolls to the newest message, while the
// transcript (w×h) is scrolled up (zf:src/ui/conversation.rs messages): a
// 40 pt circle of the overlay colour, centred 34 pt in from the
// transcript's bottom right, lifted off the messages by a soft shadow, the
// arrow down in it, growing a little and lighter under the pointer; the
// chat's unread count on a badge at its top right.
func (s *Screen) newest(e *screen.Env, ch model.Chat, w, h float32) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	d := e.Pt(newestPt)
	cx, cy := w-e.Pt(34), h-e.Pt(34)
	c.NextPos(cx-d/2, cy-d/2)
	c.NextFlags(base.Floating)
	pr := k.PressState(c.Fmt().Str(i18n.T("Newest message")).Str("###newest").String(), base.HoverOn)
	id := k.BeginPress(&pr, ui.RoleButton, ui.Px(d), ui.Px(d), 0, nil)
	// a raised surface of the theme, lighter (or darker) under the pointer
	fill, side := r.Colors[theme.Raised]|0xFF, d
	if pr.Hot > 0.5 {
		fill, side = theme.MixRGBA(fill, r.Colors[theme.Label]|0xFF, 0.06), theme.Snap(d*1.05)
	}
	c.NextCenter()
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextBG(fill)
	c.NextRadius(side / 2)
	c.NextShadow(ui.Shadow{OffY: e.Pt(3), Sigma: e.Pt(5), Color: theme.ColA(0x000000, theme.Lerp(0.16, 0.45, r.T))})
	c.BoxKeyed(base.Floating|ui.DrawBackground|ui.DrawShadow, ui.KeyN(c.ParentKey(), 1), nil)
	c.NextCenter()
	k.Icon(s.icons.down, r.Colors[theme.Label])
	k.FocusRing(pr.ID, pr.Visual, d/2)
	if k.EndPress(&pr).Clicked {
		s.toBottom()
	}
	rexui.Tooltip(k, id, i18n.T("Newest message"))
	// the chat's unread count (widgets::badge): 20 pt high, at least round
	n := ch.Unread()
	if n <= 0 {
		return
	}
	// the name first: the count's formatter is the one it takes too
	c.NextRole(ui.RoleLabel)
	c.NextSemName(fillInt(c.Fmt(), i18n.N("{} unread message", "{} unread messages", n), "{}", n).String())
	f := c.Fmt()
	if n > 99 {
		f.Str("99+")
	} else {
		f.Int(n)
	}
	bh := e.Pt(20)
	bw := max(bh, theme.Snap(k.TextWidthBytes(s.look.badge, f.Bytes())+e.Pt(12)))
	theme.At(c, theme.Snap(cx+e.Pt(14)-bw/2), theme.Snap(cy-e.Pt(16)-bh/2), bw, bh)
	c.NextBG(r.Colors[theme.Accent])
	c.NextRadius(bh / 2)
	c.NextFont(s.look.badge)
	c.NextTextColor(r.Colors[theme.OnAccent])
	c.NextAlign(ui.AlignCenter)
	c.NextPadding(0, theme.Snap((bh-e.Pt(13))/2), 0, 0) // grid:ok centres a 13-pt line, not a space
	// it lies on the button's corner on purpose
	c.NextAuditExempt(ui.AuditCovered.Mask())
	c.BoxKeyed(base.Floating|ui.DrawBackground|ui.DrawText, ui.KeyN(c.ParentKey(), 7600), f.Bytes())
}

// newestPt is the Newest message button's diameter.
const newestPt = 40

func sameID(a, b []byte) bool { return bytes.Equal(a, b) }
