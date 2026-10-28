package conversation

import (
	"bytes"
	"time"
	"unicode/utf8"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/markup"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// searchState is the in-chat search (zf:src/app.rs chat_search …): the
// query, the hits the core answered for it, and the hit picked.
type searchState struct {
	buf   ui.TextBuffer
	focus bool // the field takes the keyboard at the next frame

	acct    uint64
	chat    model.ChatRef
	rev     uint32 // the buffer's revision the last search was sent for
	sent    []byte // the query sent, trimmed
	pending bool

	// the day filter: the day picked (none: every day), the month the
	// calendar shows and whether it is open; the day the last search was
	// sent for and its range
	day, month  day
	calendar    bool
	sentDay     day
	from, until int64

	b         []byte // the hits' bytes
	raw       []byte
	spans     []markup.Span
	hits      []searchHit
	batch     uint32
	more      bool // a batch is arriving
	truncated bool
	gen       uint32
}

type searchHit struct {
	id, line, who model.Str
	ts            int64
	fromMe        bool
	status        abi.Delivery
}

func (st *searchState) at(s model.Str) []byte { return st.b[s.Off : s.Off+s.Len] }

func (st *searchState) put(b []byte) model.Str {
	off := len(st.b)
	st.b = append(st.b, b...)
	return model.Str{Off: uint32(off), Len: uint32(len(b))}
}

func (st *searchState) clearHits() {
	st.b, st.hits, st.truncated, st.more = st.b[:0], st.hits[:0], false, false
	st.gen++
}

// leave forgets the search when another chat opens.
func (st *searchState) leave(acct uint64, ref model.ChatRef) {
	if st.acct == acct && st.chat == ref {
		return
	}
	st.acct, st.chat = acct, ref
	st.buf.Reset()
	st.rev = st.buf.Rev()
	st.sent, st.pending = st.sent[:0], false
	st.day, st.sentDay, st.calendar = day{}, day{}, false
	st.clearHits()
}

// close closes the pane and clears the search (zf:src/app.rs
// close_chat_search).
func (st *searchState) close(s *state.State) {
	if s.Panes.Pane == state.PaneSearch {
		s.Panes.Pane = state.PaneNone
	}
	st.buf.Reset()
	st.rev = st.buf.Rev()
	st.sent, st.pending, st.focus = st.sent[:0], false, false
	st.day, st.sentDay, st.calendar = day{}, day{}, false
	st.clearHits()
	s.Touch()
}

// fold puts the pane away and keeps the search: the header's Search brings
// it back as it was.
func fold(s *state.State) {
	if s.Panes.Pane == state.PaneSearch {
		s.Panes.Pane = state.PaneNone
		s.Touch()
	}
}

// request asks the core for the open chat's matches of the query, on the
// day picked (zf:src/app.rs request_chat_search); the earlier hits stay
// until the answer replaces them. With neither, nothing is asked.
func (st *searchState) request(a *state.Account, ch model.Chat) {
	q := bytes.TrimSpace(st.buf.Bytes())
	st.rev = st.buf.Rev()
	if bytes.Equal(q, st.sent) && st.day == st.sentDay && (st.pending || len(st.hits) > 0) {
		return
	}
	st.sent = append(st.sent[:0], q...)
	st.sentDay = st.day
	if len(q) == 0 && st.day.none() {
		st.pending = false
		st.clearHits()
		return
	}
	cmd := a.Cmd(abi.CommandTagSearchChatMessages)
	sc := &cmd.SearchChatMessages
	sc.Chat, sc.Query = ch.ID(), st.sent
	if !st.day.none() {
		st.from, st.until = format.DayBounds(st.day.y, st.day.m, st.day.d, time.Local)
		sc.From, sc.HasFrom, sc.Until, sc.HasUntil = st.from, true, st.until, true
	}
	if a.Send(cmd) == nil {
		st.pending, st.acct = true, a.Serial
	}
}

// hits keeps an answer for the query in force; late ones are dropped.
func (st *searchState) answer(a *state.Account, e *abi.EventChatHits) {
	if a.Serial != st.acct || !bytes.Equal(bytes.TrimSpace(e.Query), st.sent) {
		return
	}
	if ranged := !st.sentDay.none(); e.HasFrom != ranged || e.HasUntil != ranged || (ranged && (e.From != st.from || e.Until != st.until)) {
		return
	}
	if !st.more || e.Batch != st.batch {
		st.clearHits()
		st.batch, st.more = e.Batch, true
	}
	for i := range e.Messages {
		m := &e.Messages[i]
		h := searchHit{id: st.put(m.ID), ts: m.Timestamp, fromMe: m.FromMe, status: m.Status}
		st.raw = hitLine(st.raw[:0], m, st.sent)
		st.spans = markup.Parse(st.spans[:0], st.raw)
		off := len(st.b)
		st.b = markup.AppendText(st.b, st.raw, st.spans, nil)
		h.line = model.Str{Off: uint32(off), Len: uint32(len(st.b) - off)}
		if !m.FromMe && bytes.HasSuffix(m.Chat, []byte("@g.us")) {
			name := m.SenderName
			if len(name) == 0 {
				name = a.Model.DisplayName(m.Sender)
			}
			h.who = st.put(firstName(name))
		}
		st.hits = append(st.hits, h)
	}
	if e.Truncated {
		st.truncated = true
	}
	if e.Last {
		st.more, st.pending = false, false
	}
	st.gen++
}

// lead is how many characters a snippet keeps before the match (pane.rs
// LEAD).
const lead = 18

// hitLine appends a hit's preview: the line of its text with the query,
// started a little before the match, or its summary.
func hitLine(dst []byte, m *abi.Message, q []byte) []byte {
	text := hitText(m)
	if len(text) == 0 {
		return append(dst, hitKind(m)...)
	}
	line := text
	at := -1
	for len(line) > 0 {
		end := bytes.IndexByte(line, '\n')
		cur := line
		if end >= 0 {
			cur = line[:end]
		}
		if i := foldIndex(cur, q); i >= 0 {
			line, at = cur, i
			break
		}
		if end < 0 {
			line = nil
			break
		}
		line = line[end+1:]
	}
	if at < 0 {
		return append(dst, firstLine(text)...)
	}
	if utf8.RuneCount(line[:at]) <= lead {
		return append(dst, line...)
	}
	skip := utf8.RuneCount(line[:at]) - lead
	start := 0
	for skip > 0 {
		_, n := utf8.DecodeRune(line[start:])
		start += n
		skip--
	}
	if sp := bytes.IndexAny(line[start:at], " \t"); sp >= 0 {
		start += sp
	}
	dst = append(dst, "…"...)
	return append(dst, bytes.TrimLeft(line[start:], " \t")...)
}

func hitText(m *abi.Message) []byte {
	c := &m.Content
	switch c.Tag {
	case abi.ContentTagText:
		return c.Text.Text
	case abi.ContentTagInteractive:
		return c.Interactive.Text
	case abi.ContentTagImage:
		return c.Image.Caption
	case abi.ContentTagVideo:
		return c.Video.Caption
	case abi.ContentTagDocument:
		return c.Document.Caption
	}
	return nil
}

func hitKind(m *abi.Message) string {
	switch m.Content.Tag {
	case abi.ContentTagImage:
		return i18n.T("Photo")
	case abi.ContentTagVideo:
		return i18n.T("Video")
	case abi.ContentTagAudio:
		if m.Content.Audio.VoiceNote {
			return i18n.T("Voice message")
		}
		return i18n.T("Audio")
	case abi.ContentTagDocument:
		return i18n.T("Document")
	case abi.ContentTagSticker:
		return i18n.T("Sticker")
	case abi.ContentTagLocation:
		return i18n.T("Location")
	case abi.ContentTagContact:
		return i18n.T("Contact")
	case abi.ContentTagPoll:
		return i18n.T("Poll")
	}
	return i18n.T("Message")
}

// foldIndex is the byte offset of q in b ignoring case, -1 when it is not
// there.
// hitLabel is a hit's line with the query's first match in it, past pre
// bytes, in bold (CS-002).
func hitLabel(e *screen.Env, line []byte, pre int, q []byte, tint rexui.Tint) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	col := tint.RGBA(r)
	at, end := foldMatch(line[pre:], q)
	if at < 0 {
		rexui.LabelBytes(k, line, rexui.LabelOpts{Color: tint})
		return
	}
	at, end = at+pre, end+pre
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(line)
	c.Push(c.Box(0, "hit line"))
	part := func(n uint64, b []byte, bold, last bool) {
		if len(b) == 0 {
			return
		}
		st := &r.Styles[theme.StyleBody]
		font := st.Font
		if bold {
			font = r.Styles[rexui.TextStyles[rexui.TextSemibold]].Font
		}
		c.NextFont(font)
		c.NextTextColor(col)
		w := ui.TextSize(0)
		flags := ui.DrawText
		if last {
			w = ui.TextSize(0).Strict(0)
			flags |= ui.FadeOverflowX
		}
		c.NextSize(w, ui.Px(st.Line))
		c.BoxKeyed(flags, ui.KeyN(c.ParentKey(), n), b)
	}
	part(1, line[:at], false, false)
	part(2, line[at:end], true, false)
	part(3, line[end:], false, true)
	c.Pop()
}

// foldMatch is where q first matches in b, ignoring case: [at, end), at
// -1 for none.
func foldMatch(b, q []byte) (at, end int) {
	at = foldIndex(b, q)
	if at < 0 {
		return -1, -1
	}
	end = at
	for rest := q; len(rest) > 0 && end < len(b); {
		_, n1 := utf8.DecodeRune(b[end:])
		_, n2 := utf8.DecodeRune(rest)
		end, rest = end+n1, rest[n2:]
	}
	return at, end
}

func foldIndex(b, q []byte) int {
	if len(q) == 0 {
		return -1
	}
	for i := 0; i < len(b); {
		if hasFoldPrefix(b[i:], q) {
			return i
		}
		_, n := utf8.DecodeRune(b[i:])
		i += n
	}
	return -1
}

func hasFoldPrefix(b, q []byte) bool {
	for len(q) > 0 {
		if len(b) == 0 {
			return false
		}
		r1, n1 := utf8.DecodeRune(b)
		r2, n2 := utf8.DecodeRune(q)
		if !bytes.EqualFold(b[:n1], q[:n2]) && r1 != r2 {
			return false
		}
		b, q = b[n1:], q[n2:]
	}
	return true
}

// Search is the in-chat search pane (zf:src/ui/pane.rs).
type Search struct {
	h       *hub
	closeIc icon.ID
	backIc  icon.ID
	nextIc  icon.ID
	calIc   icon.ID
	findIc  icon.ID
	calAt   [4]float32 // the calendar button, where the calendar hangs
	// the weekday headings, for headsLoc
	heads    [7]string
	headsLoc i18n.Locale
	headsSet bool
	list     base.ListState
	gen      uint32
	keyMoved bool
	// the field's ↑, ↓ and Enter this frame (FieldCommand), with n hits
	n               int
	up, down, enter bool
}

// FieldCommand takes ↑, ↓ and Enter from the search field while there are
// hits: they walk the hits and jump to one (zf:src/ui/pane.rs keyboard).
func (p *Search) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	if p.n == 0 || p.h.search.calendar {
		return false
	}
	switch cmd {
	case ui.FieldMoveUp:
		p.up = true
	case ui.FieldMoveDown:
		p.down = true
	case ui.FieldInsertNewline:
		p.enter = true
	default:
		return false
	}
	return true
}

// walk applies the field's keys to the hits; it returns the hit Enter
// picked, -1 for none.
func (p *Search) walk() int {
	if p.n == 0 {
		return -1
	}
	cur, has := p.list.Sel.Cursor()
	i := int(cur)
	switch {
	case p.down && has:
		i = min(i+1, p.n-1)
	case p.down:
		i, has = 0, true
	case p.up && has:
		i = max(i-1, 0)
	}
	if p.enter && !has {
		i, has = 0, true
	}
	if !has || !(p.up || p.down || p.enter) {
		return -1
	}
	i = min(i, p.n-1)
	p.list.MoveTo(i, false)
	p.list.Sel.Only(uint32(i))
	p.list.Sel.SetCursor(uint32(i))
	p.list.Reveal(i)
	p.keyMoved = true
	if p.enter {
		return i
	}
	return -1
}

var _ screen.SidePane = (*Search)(nil)

// NewSearch returns the search pane.
func NewSearch() *Search { return &Search{} }

// Init readies the pane for a window.
func (p *Search) Init(e *screen.Env) error {
	p.h = hubOf(e.S)
	p.h.listen()
	p.closeIc = e.K.AddIcon(icon.Spec{Symbol: "xmark", Fallback: '×', Pt: 15})
	p.backIc = e.K.AddIcon(icon.Spec{Symbol: "chevron.left", Fallback: '‹', Pt: 15})
	p.nextIc = e.K.AddIcon(icon.Spec{Symbol: "chevron.right", Fallback: '›', Pt: 15})
	p.calIc = e.K.AddIcon(icon.Spec{Symbol: "calendar", Fallback: '▦', Pt: 15})
	p.findIc = e.K.AddIcon(icon.Spec{Symbol: "magnifyingglass", Fallback: '⌕', Pt: 28})
	return nil
}

// Width is the pane's width: the setting's.
func (p *Search) Width(e *screen.Env) float32 { return e.Pt(e.S.Settings.SearchPaneWidth) }

const fieldLabel = "chat search"

// Build builds the pane at r.
func (p *Search) Build(e *screen.Env, r screen.Rect) {
	if p.h == nil {
		p.Init(e)
	}
	a := e.Acct()
	if a == nil {
		return
	}
	ch, ok := a.OpenChatView()
	if !ok {
		return
	}
	c, k := e.C(), e.K.Kit
	st := &p.h.search
	st.leave(a.Serial, ch.Ref())
	if st.gen != p.gen {
		p.gen = st.gen
		p.list.Sel.Reset()
	}
	e.Begin(r, "search")
	c.NextSize(ui.Px(r.W), ui.Px(r.H))
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(0, "column"))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(14), e.Pt(10), e.Pt(14), e.Pt(10))
	c.Push(c.Box(0, "top"))
	c.NextSize(ui.Pct(1), ui.Px(e.Pt(32)))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(6))
	c.Push(c.Box(0, "title row"))
	if e.S.Panes.Covering {
		// Over a narrow window the pane has the whole window: Back folds it
		// away, the query kept for the header's Search.
		back := rexui.IconButton(k, p.backIc, i18n.T("Back"), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
		e.TabStop(screen.StopChatSearchClose, back.ID)
		if back.Clicked {
			fold(e.S)
		}
	} else {
		cl := rexui.IconButton(k, p.closeIc, i18n.T("Close search"), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
		e.TabStop(screen.StopChatSearchClose, cl.ID)
		if cl.Clicked {
			st.close(e.S)
		}
	}
	rexui.Label(k, i18n.T("Search messages"), rexui.LabelOpts{Style: rexui.TextTitle3})
	c.Pop()
	p.n, p.up, p.down, p.enter = len(st.hits), false, false, false
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(6))
	c.Push(c.Box(0, "field row"))
	p.calendarButton(e, st)
	if st.focus {
		st.focus = false
		k.Focus(base.FieldKey(c.ParentKey(), fieldLabel))
	}
	c.NextFieldCommander(p, "")
	k.NextWidth(ui.Pct(1))
	// named as its placeholder, keyed as fieldLabel
	fs := rexui.SearchField(k, c.Fmt().Str(i18n.T("Search")).Str("###").Str(fieldLabel).String(), &st.buf, i18n.T("Search"), rexui.FieldOpts{})
	e.TabStopKey(screen.StopChatSearchField, fs.Key)
	c.Pop()
	if !st.day.none() {
		p.dayChip(e, st)
	}
	picked := p.walk()
	if st.buf.Rev() != st.rev || st.day != st.sentDay {
		st.request(a, ch)
	}
	if fs.Cancelled && st.buf.Len() == 0 && !st.calendar {
		st.close(e.S)
	}
	c.Pop()
	if st.calendar {
		p.calendar(e, st)
		e.TabInBuildOrder() // Tab goes among its days, as upstream's
	}
	rexui.Separator(k, rexui.SeparatorOpts{})

	switch {
	case len(bytes.TrimSpace(st.buf.Bytes())) == 0 && st.day.none():
		name := ch.Name()
		if len(name) == 0 {
			name = a.Model.DisplayName(ch.ID())
		}
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextPadding(e.Pt(14), e.Pt(24), e.Pt(14), 0)
		c.NextAxis(ui.AxisY)
		c.NextCross(ui.CrossCenter)
		c.Push(c.Box(0, "hint"))
		k.NextAlign(ui.AlignCenter)
		rexui.LabelBytes(k, fillBytes(c.Fmt(), i18n.T("Search messages with {}"), "{}", name).Bytes(),
			rexui.LabelOpts{Color: rexui.Of(theme.Label3), Wrap: true})
		c.Pop()
	case len(st.hits) == 0 && st.pending:
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextPadding(0, e.Pt(32), 0, 0)
		c.NextAxis(ui.AxisY)
		c.NextCross(ui.CrossCenter)
		c.Push(c.Box(0, "pending"))
		rexui.Spinner(k, i18n.T("Search"), rexui.SpinnerOpts{})
		c.Pop()
	case len(st.hits) == 0:
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.Push(c.Box(0, "none"))
		rexui.EmptyState(k, i18n.T("No messages found"), rexui.EmptyStateOpts{Icon: p.findIc, HasIcon: true, Message: i18n.T("Try another word or pick a different day.")})
		c.Pop()
	default:
		p.results(e, a, ch, st, picked)
	}
	c.Pop()
	e.End()
}

// results is the list of hits, newest first, and the notice when the core
// cut it.
func (p *Search) results(e *screen.Env, a *state.Account, ch model.Chat, st *searchState, picked int) {
	c, k := e.C(), e.K.Kit
	n := len(st.hits)
	k.NextRowHeight(54)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	l := rexui.BeginList(k, i18n.T("Search results"), &p.list, n, rexui.ListOpts{})
	for row := l.First; row < l.Last; row++ {
		h := &st.hits[row]
		f := c.Fmt()
		chatStamp(f, h.ts, e.S.Now()).Str(", ")
		if h.who.Len > 0 {
			f.Str(model.S(st.at(h.who))).Str(": ")
		}
		f.Str(model.S(st.at(h.line)))
		l.Row(row, uint32(row), f.Bytes())
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextAxis(ui.AxisY)
		c.NextGap(e.Pt(2))
		c.Push(c.Box(0, "hit"))
		rexui.LabelBytes(k, chatStamp(c.Fmt(), h.ts, e.S.Now()).Bytes(), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
		lf := c.Fmt()
		if h.fromMe {
			lf.Str(ticks(h.status)).Byte(' ')
		} else if h.who.Len > 0 {
			lf.Str(model.S(st.at(h.who))).Str(": ")
		}
		pre := len(lf.Bytes())
		lf.Str(model.S(st.at(h.line)))
		hitLabel(e, lf.Bytes(), pre, st.sent, l.Text())
		c.Pop()
	}
	rexui.EndList(k, &l)
	if l.Changed && !p.keyMoved {
		if cur, ok := p.list.Sel.Cursor(); ok && int(cur) < n {
			picked = int(cur)
		}
	}
	p.keyMoved = false
	if picked >= 0 && picked < n {
		a.OpenMessage(ch.ID(), st.at(st.hits[picked].id))
		if e.S.Panes.Covering {
			// Over a narrow conversation the pane folds away so the message
			// shows; the query, the day and the hits wait for the header's
			// Search (zf:src/ui/pane.rs search).
			st.calendar = false
			fold(e.S)
		}
	}
	if st.truncated {
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextPadding(e.Pt(14), e.Pt(10), e.Pt(14), e.Pt(8))
		c.Push(c.Box(0, "notice"))
		rexui.LabelBytes(k, fillInt(c.Fmt(), i18n.T("Only the newest {} matches are listed. Narrow the search or pick a day."), "{}", n).Bytes(),
			rexui.LabelOpts{Style: rexui.TextFootnote, Color: rexui.Of(theme.Label3), Wrap: true})
		c.Pop()
	}
}
