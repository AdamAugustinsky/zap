// Package spotlight is ⌘K: one field that finds a chat, a person, a message
// or a file across every linked account, and an action of the app's (the
// space's colours), over the main window, as macOS's Spotlight does. Chats and people match in the model as the text changes;
// messages match first in the conversations already loaded (at once), then
// in each account's archive (Command SearchMessages, 150 ms after the typing
// rests). A message that is a document, a picture, a video or a link goes
// under Files and links. It is rexui's command palette: ↑ ↓, Page Up and
// Page Down move (the selection kept in view), Enter opens, Escape or a
// click outside closes.
package spotlight

import (
	"bytes"
	gformat "gonativeui/format"
	"strings"
	"time"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

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
	secRecent  section = iota // the empty field: the chats used last
	secActions                // what the app does, by its name or a word for it
	secChats
	secPeople
	secMessages
	secFiles
	numSections
)

// caps are how many results a section shows.
var caps = [numSections]int{secRecent: 6, secActions: 2, secChats: 5, secPeople: 4, secMessages: 8, secFiles: 6}

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
	act    action // an action's row: what Enter does
}

// action is what an action's row does.
type action uint8

const (
	actNone        action = iota
	actSpaceColour        // the theme creator on the space on screen (View → Space colour…)
)

// spaceColourWords are words that find Space colour besides its translated
// name, which finds it in every language.
const spaceColourWords = "colour color theme tint grain appearance"

// Screen is the overlay.
type Screen struct {
	pal     rexui.PaletteState
	query   string
	needle  string
	changed time.Time // when the text last changed
	sent    string    // the text the accounts were asked for
	gens    []uint32  // each account's SearchHits answer, as last read
	rows    []result
	// items are the palette's rows: a section's header (-1 - its section)
	// or a result's index in rows
	items []int
	ic    struct{ doc, photo, video, link, msg, palette icon.ID }
}

// New is the overlay, closed.
func New() *Screen { return &Screen{} }

// Init readies its icons.
func (s *Screen) Init(e *screen.Env) error {
	k := e.K
	s.ic.doc = k.AddIcon(ztheme.ToolIcon("file-text"))
	s.ic.photo = k.AddIcon(ztheme.ToolIcon("image"))
	s.ic.video = k.AddIcon(ztheme.ToolIcon("video"))
	s.ic.link = k.AddIcon(ztheme.ToolIcon("link"))
	s.ic.msg = k.AddIcon(ztheme.ToolIcon("message-square-text"))
	s.ic.palette = k.AddIcon(ztheme.ToolIcon("palette"))
	return nil
}

// debounce is how long the typing rests before the archives are asked.
const debounce = 150 * time.Millisecond

// rowPt is a result's height.
const rowPt = 44

// Build builds the overlay while State.Spotlight.
func (s *Screen) Build(e *screen.Env) {
	st := e.S
	if !st.Spotlight || st.Lock.Locked {
		if s.pal.Open {
			s.reset()
		}
		return
	}
	k := e.K
	s.pal.Show(string(st.Req.SpotlightText)) // opens it, and adds what a key typed
	st.Req.SpotlightText = st.Req.SpotlightText[:0]
	p, ok := rexui.BeginPalette(k.Kit, i18n.T("Search everything")+"###spotlight", &s.pal, rexui.PaletteOpts{Placeholder: i18n.T("Chats, people, messages, files")})
	if !ok { // a click outside closed it
		s.close(e)
		return
	}
	if p.QueryChanged {
		s.query = strings.TrimSpace(p.Query)
		s.needle = format.Fold(s.query)
		s.changed = time.Now()
		s.collect(e)
	}
	s.ask(e)
	if s.hitsChanged(e) {
		s.collect(e)
	}
	p.Results(len(s.items))
	for i, it := range s.items {
		if it < 0 {
			p.Header(i, sectionName(section(-1-it)))
			continue
		}
		s.row(e, &p, i, &s.rows[it])
	}
	sig := rexui.EndPalette(k.Kit, &p)
	switch {
	case sig.Activated && sig.Row >= 0 && sig.Row < len(s.items) && s.items[sig.Row] >= 0:
		s.open(e, s.rows[s.items[sig.Row]])
	case sig.Closed:
		s.close(e)
	}
}

// row builds result rw as the palette's row i.
func (s *Screen) row(e *screen.Env, p *rexui.Palette, i int, rw *result) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	row := p.Row(i, c.Fmt().Str(rw.title).Str("###result").Int(i).String(), rexui.ListRowOpts{Height: rowPt, Gap: theme.Space3})
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
	c.NextSize(ui.Grow(1), ui.Children(1))
	c.Push(c.Box(0, "text"))
	k.NextWidth(ui.Pct(1))
	rexui.Label(k, rw.title, rexui.LabelOpts{Color: row.Text()})
	if rw.sub != "" {
		k.NextWidth(ui.Pct(1))
		rexui.Label(k, rw.sub, rexui.LabelOpts{Style: rexui.TextCaption, Color: row.Text2()})
	}
	c.Pop()
	if meta := s.meta(e, rw); meta != "" {
		rexui.Label(k, meta, rexui.LabelOpts{Color: row.Text2(), MetaPt: ztheme.T.MetaPt})
	}
	p.EndRow()
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
	return gformat.Date(t)
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
	case secActions:
		return i18n.T("Actions")
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
	if s.query != "" && e.S.Acct() != nil && wordsMatch(i18n.T("Space colour")+" "+spaceColourWords, s.needle) {
		add(result{sec: secActions, acct: e.S.Active, act: actSpaceColour, title: i18n.T("Space colour…"), ic: s.ic.palette})
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
	// the sections in their order, stable within each, each under its
	// header
	sortSections(s.rows)
	s.items = s.items[:0]
	for i := range s.rows {
		if i == 0 || s.rows[i].sec != s.rows[i-1].sec {
			s.items = append(s.items, -1-int(s.rows[i].sec))
		}
		s.items = append(s.items, i)
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

func title(a *state.Account, ch model.Chat) []byte { return a.Model.ChatName(ch) }

func preview(ch model.Chat) string {
	if !ch.HasLast() {
		return ""
	}
	return model.S(ch.Preview())
}

// wordsMatch reports whether needle (folded) starts one of words' words.
func wordsMatch(words, needle string) bool {
	for _, w := range strings.Fields(format.Fold(words)) {
		if strings.HasPrefix(w, needle) {
			return true
		}
	}
	return strings.HasPrefix(format.Fold(words), needle) // more than one word typed
}

// open opens a result, in its account.
func (s *Screen) open(e *screen.Env, rw result) {
	st := e.S
	if rw.act == actSpaceColour {
		s.close(e)
		st.Req.SpaceColour = true // the shell opens it (ui/shell/menu.go)
		st.Touch()
		return
	}
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
	// The palette is how one goes to a chat to write in it.
	st.Req.FocusComposer = true
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
	s.pal.Hide()
	s.query, s.needle, s.sent = "", "", ""
	s.rows, s.items = s.rows[:0], s.items[:0]
}
