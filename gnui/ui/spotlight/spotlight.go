// Package spotlight is ⌘K: one field that finds a chat, a person, a message
// or a file across every linked account, over the main window, as macOS's
// Spotlight does. Chats and people match in the model as the text changes;
// messages match first in the conversations already loaded (at once), then
// in each account's archive (Command SearchMessages, 150 ms after the typing
// rests). A message that is a document, a picture, a video or a link goes
// under Files and links. ↑ and ↓ move, Enter opens, Escape or a click
// outside closes.
package spotlight

import (
	"bytes"
	"strings"
	"time"

	"gonativeui/icon"
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

// section is a group of results, in the order they show.
type section uint8

const (
	secRecent section = iota // the empty field: the chats used last
	secChats
	secPeople
	secMessages
	secFiles
	numSections
)

// caps are how many results a section shows.
var caps = [numSections]int{secRecent: 6, secChats: 5, secPeople: 4, secMessages: 8, secFiles: 6}

// result is one row.
type result struct {
	sec    section
	acct   int // its account's index in State.Accounts
	ref    model.ChatRef
	jid    []byte // the chat's (or the person's) id
	msg    []byte // a message's id
	title  string
	sub    string
	ts     int64
	ic     icon.ID // a file's or a link's kind; 0: the chat's picture
	person bool
}

// Screen is the overlay.
type Screen struct {
	field   ui.TextBuffer
	rev     uint32
	query   string
	needle  string
	changed time.Time // when the text last changed
	sent    string    // the text the accounts were asked for
	gens    []uint32  // each account's SearchHits answer, as last read
	rows    []result
	sel     int
	age     int        // frames since it opened
	mouse   [2]float32 // the pointer, last frame: a row is picked by moving over it
	ic      struct{ doc, photo, video, link, msg icon.ID }
}

// New is the overlay, closed.
func New() *Screen { return &Screen{} }

// Init readies its icons.
func (s *Screen) Init(e *screen.Env) error {
	k := e.K
	s.ic.doc = k.AddIcon(ztheme.ToolIcon("doc.text", '▤'))
	s.ic.photo = k.AddIcon(ztheme.ToolIcon("photo", '▣'))
	s.ic.video = k.AddIcon(ztheme.ToolIcon("video", '▶'))
	s.ic.link = k.AddIcon(ztheme.ToolIcon("link", '↗'))
	s.ic.msg = k.AddIcon(ztheme.ToolIcon("text.bubble", '✉'))
	return nil
}

// debounce is how long the typing rests before the archives are asked.
const debounce = 150 * time.Millisecond

// Pt sizes.
const (
	panelW   = 640
	panelTop = 88
	rowPt    = 44
	listMax  = 420
)

// Build builds the overlay while State.Spotlight.
func (s *Screen) Build(e *screen.Env) {
	st := e.S
	if !st.Spotlight || st.Lock.Locked {
		if s.age != 0 {
			s.reset()
		}
		return
	}
	s.age++
	c, k := e.C(), e.K
	r := k.Theme()
	w, h := c.Size()
	W, H := float32(w), float32(h)

	// a click outside closes it
	theme.At(c, 0, 0, W, H)
	c.NextRole(ui.RoleGroup)
	scrim := c.Box(base.Floating|ui.Clickable, "spotlight scrim")
	if c.Signal(scrim).Clicked {
		s.close(e)
		return
	}

	// it comes down from where Spotlight lives, a little, and fades in
	// (motion.go: Short, from its place)
	t := c.AnimateValueFrom(ui.HashString(0, "spotlight in"), 0, 1, ztheme.Enter(r, ztheme.T.DurShort))
	pw := min(e.Pt(panelW), W-e.Pt(ztheme.Space8))
	x := theme.Snap((W - pw) / 2)
	y := e.Pt(panelTop) - (1-t)*e.Pt(ztheme.Space2)
	c.NextOpacity(t)
	theme.At(c, x, y, pw, 0)
	c.NextSize(ui.Px(pw), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextPadding(e.Pt(ztheme.Space2), e.Pt(ztheme.Space2), e.Pt(ztheme.Space2), e.Pt(ztheme.Space2))
	c.NextGap(e.Pt(ztheme.Space1))
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextRadius(e.Pt(ztheme.WindowRadius))
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextShadow(ui.Shadow{OffY: e.Pt(12), Sigma: e.Pt(24), Color: 0x00000040})
	c.NextRole(ui.RoleDialog)
	c.NextSemName(i18n.T("Search everything"))
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.Clickable, "spotlight"))

	sig := rexui.SearchField(k.Kit, i18n.T("Search everything")+"###spotlight", &s.field, i18n.T("Chats, people, messages, files"), rexui.FieldOpts{Size: theme.SizeLarge})
	if s.age <= 2 {
		c.SetFocus(sig.Key)
	}
	if t := st.Req.SpotlightText; len(t) > 0 {
		s.field.SetString(s.field.String() + string(t))
		s.field.SetCaret(len(s.field.String()))
		st.Req.SpotlightText = t[:0]
	}
	if s.field.Rev() != s.rev {
		s.rev = s.field.Rev()
		s.query = strings.TrimSpace(s.field.String())
		s.needle = format.Fold(s.query)
		s.changed = time.Now()
		s.sel = 0
		s.collect(e)
	}
	s.ask(e)
	if s.hitsChanged(e) {
		s.collect(e)
	}

	// the keys: ↑ ↓ move, Enter opens, Escape closes
	switch {
	case c.TakeKey(ui.KeyDown, 0):
		s.sel = min(s.sel+1, len(s.rows)-1)
	case c.TakeKey(ui.KeyUp, 0):
		s.sel = max(s.sel-1, 0)
	}
	if sig.Cancelled || c.TakeKey(ui.KeyEscape, 0) {
		c.Pop()
		s.close(e)
		return
	}
	open := -1
	defer func() { in := c.Input(); s.mouse = [2]float32{in.MouseX, in.MouseY} }()
	if sig.Submitted && len(s.rows) > 0 {
		open = s.sel
	}
	if i := s.list(e, pw-2*e.Pt(ztheme.Space2)); i >= 0 {
		open = i
	}
	c.Pop()
	if open >= 0 && open < len(s.rows) {
		s.open(e, s.rows[open])
	}
}

// list builds the rows under their sections and reports a clicked one.
func (s *Screen) list(e *screen.Env, w float32) int {
	c, k := e.C(), e.K
	if len(s.rows) == 0 {
		if s.query != "" {
			c.NextPadding(e.Pt(ztheme.Space2), e.Pt(ztheme.Space3), e.Pt(ztheme.Space2), e.Pt(ztheme.Space3))
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.Push(c.Box(0, "nothing"))
			rexui.Label(k.Kit, i18n.T("Nothing found"), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
			c.Pop()
		}
		return -1
	}
	k.NextScrollMaxHeight(listMax) // as tall as the results, up to listMax
	v := k.BeginScroll("spotlight results", &scrollState, base.ScrollVertical)
	clicked := -1
	last := section(255)
	for i := range s.rows {
		rw := &s.rows[i]
		if rw.sec != last {
			last = rw.sec
			c.NextPadding(e.Pt(ztheme.Space2), e.Pt(ztheme.Space2), e.Pt(ztheme.Space2), e.Pt(ztheme.Space1))
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.Push(c.Box(0, c.Fmt().Str("section ").Int(int(rw.sec)).String()))
			rexui.Label(k.Kit, sectionName(rw.sec), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
			c.Pop()
		}
		if s.row(e, i, w) {
			clicked = i
		}
	}
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k.Kit, &v)
	k.CloseScroll(&v)
	return clicked
}

// scrollState is the results' scroll (one overlay, one window).
var scrollState base.ScrollState

// row builds result i and reports a click.
func (s *Screen) row(e *screen.Env, i int, w float32) bool {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	rw := &s.rows[i]
	p := k.PressState(c.Fmt().Str("result").Int(i).String(), base.HoverOn)
	p.Name = rw.title
	if in := c.Input(); p.Hot > 0.5 && (in.MouseX != s.mouse[0] || in.MouseY != s.mouse[1]) {
		s.sel = i // the pointer moving over a row picks it, as the arrows do
	}
	fill := uint32(0)
	switch {
	case i == s.sel:
		fill = rexui.ChatRowLook.Selected.RGBA(r)
	case p.Hot > 0:
		fill = theme.WithAlpha(r.Colors[theme.Label], ztheme.T.RowHover*p.Hot)
	}
	c.NextBG(fill)
	c.NextRadius(e.Pt(ztheme.Nested()))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(ztheme.Space3))
	c.NextPadding(e.Pt(ztheme.Space2), 0, e.Pt(ztheme.Space2), 0)
	k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(e.Pt(rowPt)), ui.DrawBackground, nil)
	side := e.Pt(28)
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.Push(c.Box(0, "lead"))
	if rw.ic != 0 {
		k.Icon(rw.ic, r.Colors[theme.Label2])
	} else if a := s.acct(e, rw.acct); a != nil {
		rexui.Avatar(k, rw.title, rexui.AvatarOpts{Size: 28, Image: screen.Avatar(e.K.Images(), a, rw.jid, false, int32(side))})
	}
	c.Pop()
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "text"))
	k.NextWidth(ui.Pct(1))
	rexui.Label(k, rw.title, rexui.LabelOpts{})
	if rw.sub != "" {
		k.NextWidth(ui.Pct(1))
		rexui.Label(k, rw.sub, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	}
	c.Pop()
	if meta := s.meta(e, rw); meta != "" {
		rexui.Label(k, meta, rexui.LabelOpts{Color: rexui.Of(theme.Label2), MetaPt: ztheme.T.MetaPt})
	}
	return k.EndPress(&p).Clicked
}

// meta is what a row says at its end: the time of a message, and the
// account when there are several.
func (s *Screen) meta(e *screen.Env, rw *result) string {
	var b strings.Builder
	if rw.ts > 0 {
		b.WriteString(stamp(rw.ts, e.S.Now()))
	}
	if len(e.S.Accounts) > 1 {
		if a := s.acct(e, rw.acct); a != nil {
			if b.Len() > 0 {
				b.WriteString("  ")
			}
			b.WriteString(model.S(accountName(a)))
		}
	}
	return b.String()
}

// stamp is a message's time: the hour today, Yesterday, then the date.
func stamp(ts int64, now time.Time) string {
	t := time.Unix(ts, 0)
	switch format.DaysSince(t, now) {
	case 0:
		return string(format.AppendTimeOfDay(nil, t.Hour(), t.Minute(), format.Hour12()))
	case 1:
		return i18n.T("Yesterday")
	}
	return t.Format("02/01/06")
}

func accountName(a *state.Account) []byte {
	if n := a.Model.Me.Name(); len(n) > 0 {
		return n
	}
	return model.Phone(a.Model.Me.ID())
}

func sectionName(sec section) string {
	switch sec {
	case secRecent:
		return i18n.T("Recent")
	case secChats:
		return i18n.T("Chats")
	case secPeople:
		return i18n.T("People")
	case secMessages:
		return i18n.T("Messages")
	}
	return i18n.T("Files and links")
}

func (s *Screen) acct(e *screen.Env, i int) *state.Account {
	if i < 0 || i >= len(e.S.Accounts) {
		return nil
	}
	return e.S.Accounts[i]
}

// ask sends the text to every account's archive once the typing rests.
func (s *Screen) ask(e *screen.Env) {
	if s.query == s.sent {
		return
	}
	if wait := debounce - time.Since(s.changed); wait > 0 {
		e.C().WakeIn(int64(wait))
		return
	}
	s.sent = s.query
	for _, a := range e.S.Accounts {
		a.Model.Answer(abi.EventTagSearchHits).Clear()
		if s.query == "" || !a.Model.Linked() {
			continue
		}
		cmd := a.Cmd(abi.CommandTagSearchMessages)
		cmd.SearchMessages.Query = []byte(s.query)
		a.Send(cmd)
	}
}

// hitsChanged reports whether an account's archive has answered since.
func (s *Screen) hitsChanged(e *screen.Env) bool {
	n := len(e.S.Accounts)
	if len(s.gens) != n {
		s.gens = make([]uint32, n)
	}
	changed := false
	for i, a := range e.S.Accounts {
		if g := a.Model.Answer(abi.EventTagSearchHits).Gen(); g != s.gens[i] {
			s.gens[i], changed = g, true
		}
	}
	return changed
}

// collect builds the rows for the text.
func (s *Screen) collect(e *screen.Env) {
	s.rows = s.rows[:0]
	var count [numSections]int
	add := func(rw result) {
		if count[rw.sec] < caps[rw.sec] {
			count[rw.sec]++
			s.rows = append(s.rows, rw)
		}
	}
	seen := map[string]bool{} // messages already listed: account and id
	for ai, a := range e.S.Accounts {
		m := a.Model
		if !m.Linked() {
			continue
		}
		order := m.Chats.Order()
		if s.query == "" {
			for _, ref := range order {
				ch := m.Chats.At(ref)
				if ch.Locked() || ch.Archived() {
					continue
				}
				add(result{sec: secRecent, acct: ai, ref: ref, jid: ch.ID(), title: model.S(title(a, ch)), sub: preview(ch)})
			}
			continue
		}
		// chats: the name first, its start before its middle
		var later []result
		for _, ref := range order {
			ch := m.Chats.At(ref)
			if ch.Locked() {
				continue
			}
			t := title(a, ch)
			folded := format.Fold(model.S(t))
			switch {
			case strings.HasPrefix(folded, s.needle):
				add(result{sec: secChats, acct: ai, ref: ref, jid: ch.ID(), title: model.S(t), sub: preview(ch)})
			case strings.Contains(folded, s.needle):
				later = append(later, result{sec: secChats, acct: ai, ref: ref, jid: ch.ID(), title: model.S(t), sub: preview(ch)})
			}
		}
		for _, rw := range later {
			add(rw)
		}
		// people with no chat yet
		me := m.Me.ID()
		for i := 0; i < m.Contacts.Len(); i++ {
			ct := m.Contacts.At(i)
			id := ct.ID()
			if bytes.Equal(id, me) || m.Chats.Find(id) != 0 || !strings.Contains(format.Fold(model.S(ct.DisplayName())), s.needle) {
				continue
			}
			add(result{sec: secPeople, acct: ai, jid: id, title: model.S(ct.DisplayName()), sub: model.S(format.AppendPhone(nil, model.S(model.Phone(id)))), person: true})
		}
		// messages in the conversations already loaded: at once
		for _, ref := range order {
			ch := m.Chats.At(ref)
			if ch.Locked() {
				continue
			}
			v := m.Conversation(ch.ID())
			if v == nil {
				continue
			}
			for i := v.Len() - 1; i >= 0; i-- {
				msg := v.At(i)
				if txt := msg.Text(); len(txt) > 0 && strings.Contains(format.Fold(model.S(txt)), s.needle) {
					key := a.ID + "/" + string(msg.ID())
					if !seen[key] {
						seen[key] = true
						s.addMessage(add, ai, a, ch, msg.ID(), msg.Kind(), txt, msg.Time())
					}
				}
			}
		}
		// and the archive's answer, once it has come
		ans := m.Answer(abi.EventTagSearchHits)
		if ev := ans.Event(); ans.Has() && ev != nil && string(ev.SearchHits.Query) == s.sent {
			for j := 0; j < ans.Len(); j++ {
				ev, x := ans.Item(j)
				if ev == nil {
					break
				}
				hm := &ev.SearchHits.Messages[x]
				key := a.ID + "/" + string(hm.ID)
				if seen[key] {
					continue
				}
				ref := m.Chats.Find(hm.Chat)
				ch, ok := m.Chats.Get(ref)
				if !ok || ch.Locked() {
					continue
				}
				seen[key] = true
				s.addMessage(add, ai, a, ch, hm.ID, hm.Content.Tag, hitText(hm), hm.Timestamp)
			}
		}
	}
	// the sections in their order, stable within each
	sortSections(s.rows)
	if s.sel >= len(s.rows) {
		s.sel = max(len(s.rows)-1, 0)
	}
}

// addMessage adds a message hit: a file or a link under Files and links,
// the rest under Messages, with the chat and the matched text.
func (s *Screen) addMessage(add func(result), ai int, a *state.Account, ch model.Chat, id []byte, kind abi.ContentTag, txt []byte, ts int64) {
	rw := result{sec: secMessages, acct: ai, ref: ch.Ref(), jid: ch.ID(), msg: append([]byte(nil), id...), ts: ts,
		title: model.S(title(a, ch)), sub: snippet(model.S(txt), s.needle)}
	switch kind {
	case abi.ContentTagDocument:
		rw.sec, rw.ic = secFiles, s.ic.doc
	case abi.ContentTagImage:
		rw.sec, rw.ic = secFiles, s.ic.photo
	case abi.ContentTagVideo:
		rw.sec, rw.ic = secFiles, s.ic.video
	default:
		if bytes.Contains(txt, []byte("http://")) || bytes.Contains(txt, []byte("https://")) {
			rw.sec, rw.ic = secFiles, s.ic.link
		}
	}
	if rw.sec == secFiles {
		// a file is named by itself (a link by its address), its chat under it
		rw.title, rw.sub = rw.sub, model.S(title(a, ch))
		if rw.ic == s.ic.link {
			rw.title = link(model.S(txt))
		}
	}
	add(rw)
}

// hitText is the text a hit matched in: its text, a caption or a file name.
func hitText(m *abi.Message) []byte {
	c := &m.Content
	switch c.Tag {
	case abi.ContentTagText:
		return c.Text.Text
	case abi.ContentTagDocument:
		if len(c.Document.Caption) > 0 {
			return c.Document.Caption
		}
		return c.Document.FileName
	case abi.ContentTagImage:
		return c.Image.Caption
	case abi.ContentTagVideo:
		return c.Video.Caption
	}
	return nil
}

// snippet is a line of txt round the needle's first match: from the start
// of a word a little before it, so the match is in sight.
func snippet(txt, needle string) string {
	txt = strings.Join(strings.Fields(txt), " ")
	i := strings.Index(format.Fold(txt), needle)
	if i > 24 {
		// the folded text keeps the original's byte offsets for ASCII;
		// elsewhere this only moves the start of the line
		start := i - 18
		if sp := strings.LastIndexByte(txt[:start], ' '); sp >= 0 && start-sp < 12 {
			start = sp + 1
		}
		for start > 0 && txt[start]&0xC0 == 0x80 {
			start--
		}
		txt = "…" + txt[start:]
	}
	return txt
}

// link is the first address in txt, without its scheme: what a link row is
// named by.
func link(txt string) string {
	for _, f := range strings.Fields(txt) {
		for _, sch := range [...]string{"https://", "http://"} {
			if i := strings.Index(f, sch); i >= 0 {
				return strings.TrimSuffix(f[i+len(sch):], "/")
			}
		}
	}
	return txt
}

func sortSections(rows []result) {
	// insertion sort: the rows are few and nearly in order
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].sec < rows[j-1].sec; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func title(a *state.Account, ch model.Chat) []byte {
	if n := ch.Name(); len(n) > 0 {
		return n
	}
	return a.Model.DisplayName(ch.ID())
}

func preview(ch model.Chat) string {
	if !ch.HasLast() {
		return ""
	}
	return model.S(ch.Preview())
}

// open opens a result, in its account.
func (s *Screen) open(e *screen.Env, rw result) {
	st := e.S
	a := s.acct(e, rw.acct)
	if a == nil {
		return
	}
	s.close(e)
	if rw.acct != st.Active {
		st.SwitchAccount(rw.acct)
	}
	st.Page = state.PageChats
	switch {
	case rw.person:
		a.StartChat(rw.jid, []byte(rw.title), nil)
	case rw.msg != nil:
		st.OpenMessageIn(a.ID, rw.jid, rw.msg)
	default:
		a.SetMode(state.ListChats)
		a.OpenChat(rw.ref)
	}
	st.Touch()
}

// close closes the overlay.
func (s *Screen) close(e *screen.Env) {
	e.S.Spotlight = false
	e.C().SetFocus(0)
	e.S.Touch()
	s.reset()
}

func (s *Screen) reset() {
	s.field.SetString("")
	s.rev, s.query, s.needle, s.sent = 0, "", "", ""
	s.rows, s.sel, s.age = s.rows[:0], 0, 0
}
