package calls

import (
	"bytes"
	"sort"
	"time"

	"gonativeui/icon"
	"gonativeui/rexui"
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

// The Calls page (zf:src/ui/calls.rs, zf:src/ui/chats.rs's Calls header):
// the account's call history in the list column, or the new-call list over
// it.

// Layout, in points.
const (
	rowPt       = 64 // a history or contact row
	rowAvatarPt = 44
)

// Page is the Calls page: the call history in the list column.
type Page struct {
	h  *hub
	ic pageIcons

	search, find ui.TextBuffer // the history's search, the new-call list's
	hist, people base.ListState

	// The history's rows for the account on screen, rebuilt when the log,
	// the names, the day, the chip or the search change.
	rows  []row
	shown []uint32 // indexes into rows, as the list shows them
	rb    []byte   // the rows' words
	built struct {
		acct            uint64 // the account's Serial
		mode            state.ListMode
		log             uint32
		contacts, chats uint32
		day             int
		missed          bool
		needle          []byte
		ok              bool
	}

	// The new-call list: the frequent people, then every contact.
	persons []person
	listed  []uint32 // items: personFrequent+i, personAll+j, or the headers
	pbuf    []byte
	pbuilt  struct {
		acct            uint64 // the account's Serial
		log             uint32
		contacts, chats uint32
		needle          []byte
		ok              bool
	}
	nfreq int

	pics      pics
	lastFrame uint32
	lastAcct  uint64 // the account's Serial
	// wasNew: the new-call list showed last frame (its search starts empty).
	wasNew bool
	// focusFind: the new-call list just opened, and its search takes the
	// keyboard as soon as it is built.
	focusFind bool
	needle    []byte
	buf, buf2 []byte
	key       []byte
}

var (
	_ screen.Pane        = (*Page)(nil)
	_ screen.ToolbarPart = (*Page)(nil)
)

// person is one row of the new-call list.
type person struct {
	jid, name, key, phone span
}

// The new-call list's items.
const (
	itemFrequentHeader = 1 << 30
	itemAllHeader      = 1<<30 + 1
	itemFrequent       = 1 << 29 // + the frequent person's index
)

type pageIcons struct {
	voice, video, newCall, back, out, in, missed, link, big icon.ID
}

// NewPage returns the Calls page.
func NewPage() *Page {
	p := &Page{}
	p.search.Max, p.find.Max = 256, 256
	p.search.Reserve(256)
	p.find.Reserve(256)
	p.hist.Sel.Reserve(1024)
	p.people.Sel.Reserve(4096)
	return p
}

// Init readies the page for a window.
func (p *Page) Init(e *screen.Env) error {
	p.h = hubOf(e.S)
	p.pics.reset()
	add := func(sym string, fb rune, pt float32) icon.ID {
		return e.K.AddIcon(icon.Spec{Symbol: sym, Fallback: fb, Pt: pt})
	}
	tbPt := theme.MacWindow.Capsule.Glyph + 2
	p.ic = pageIcons{
		voice: add("phone", '☎', 16), video: add("video", '▶', 16), newCall: add("phone.badge.plus", '+', tbPt),
		back: add("chevron.left", '‹', tbPt), out: add("phone.arrow.up.right", '↗', 13), in: add("phone.arrow.down.left", '↙', 13),
		missed: add("phone.fill.arrow.down.left", '↙', 13), link: add("link", '∞', 18), big: add("phone", '☎', 40),
	}
	return nil
}

// Build builds the page at r.
func (p *Page) Build(e *screen.Env, r screen.Rect) {
	e.Begin(r, "Calls")
	defer e.End()
	a := e.Acct()
	if a == nil || p.h == nil {
		return
	}
	h, c, k := p.h, e.C(), e.K
	now := e.S.Now()
	h.tick(now)
	ac := h.acct(a)
	if !ac.read {
		ac.read, ac.missed = true, string(a.Settings.CallFilter) == "missed"
	}
	// The page reads the account's calls as it opens, and opens on the
	// history; switching numbers keeps it open on the other number's.
	if f := c.Frame(); f != p.lastFrame+1 || a.Serial != p.lastAcct {
		if a.Serial == p.lastAcct || f != p.lastFrame+1 {
			ac.newCall = false
			ac.picked.Reset()
		}
		h.loadLog(a)
	}
	p.lastFrame, p.lastAcct = c.Frame(), a.Serial
	if ac.newCall != p.wasNew {
		p.wasNew = ac.newCall
		p.find.SetString("")
	}
	p.takeKeys(e, ac)

	k.NextPadding(0, 0, 0, 0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "calls page", ui.Px(r.W), ui.Px(r.H), 0)
	k.NextPadding(12, 8, 12, 6)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "search", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(ui.Pct(1))
	hint := i18n.T("Search name or number")
	if ac.newCall {
		fs := rexui.SearchField(k.Kit, c.Fmt().Str(hint).Str("###find").String(), &p.find, hint, rexui.FieldOpts{})
		e.TabStopKey(screen.StopSearch, fs.Key)
		if p.focusFind {
			// The list opens ready for a name, as New chat does.
			p.focusFind = false
			c.SetFocus(fs.Key)
			c.SetFocusVisible(true)
		}
		if fs.Cancelled {
			p.showNewCall(ac, false) // Escape leaves the list from its search too
		}
	} else {
		fs := rexui.SearchField(k.Kit, c.Fmt().Str(hint).Str("###search").String(), &p.search, hint, rexui.FieldOpts{})
		e.TabStopKey(screen.StopSearch, fs.Key)
		p.chips(e, a, ac)
	}
	k.EndContainer()
	k.NextHeight(ui.Pct(1))
	k.BeginContainer(ui.AxisY, ui.CrossStart, "body", ui.Pct(1), ui.Pct(1), 0)
	if ac.newCall {
		p.newCallList(e, a, ac)
	} else {
		p.history(e, a, ac, now)
	}
	k.EndContainer()
	k.EndContainer()
}

// takeKeys: Escape leaves the new-call list for the history; the shell's
// Escape leaves the page.
func (p *Page) takeKeys(e *screen.Env, ac *acct) {
	c := e.C()
	if ac.newCall && !c.ModalOpen() && c.TakeKey(ui.KeyEscape, 0) {
		p.showNewCall(ac, false)
	}
}

func (p *Page) showNewCall(ac *acct, on bool) {
	ac.newCall = on
	p.focusFind = on
	p.h.gen++
}

// Toolbar is the page's share of the window's toolbar (CL-001): over the
// history the title "Calls" and New call; over the new-call list Back and the
// title "New call" (zf:src/ui/chats.rs new_call_header). The Calls tab has no
// Hide the chat list: the history needs the list.
func (p *Page) Toolbar(e *screen.Env, tb *rexui.Toolbar) {
	a := e.Acct()
	if a == nil || p.h == nil {
		return
	}
	ac := p.h.acct(a)
	k := e.K
	c := k.Context()
	title := i18n.T("Calls")
	if ac.newCall {
		title = i18n.T("New call")
		it := tb.Item(p.ic.back, i18n.T("Back to the calls"))
		e.TabStop(screen.StopBack, it.ID)
		if it.Clicked {
			p.showNewCall(ac, false)
		}
	}
	tb.BeginCustom("list title", ui.Pct(1))
	r := k.Theme()
	st := &r.Styles[theme.StyleHeadline]
	theme.At(c, 0, theme.Snap((tb.Height()-st.Line)/2), 0, st.Line)
	c.NextSize(ui.Pct(1), ui.Px(st.Line))
	c.NextFont(st.Font)
	c.NextTextColor(rexui.ToolbarLook.Title.RGBA(r))
	c.NextRole(ui.RoleLabel)
	c.NextSemName(title)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.HashString(c.ParentKey(), "title"), base.StrBytes(title))
	tb.EndCustom()
	if ac.newCall {
		return
	}
	nc := tb.Item(p.ic.newCall, i18n.T("New call"))
	e.TabStop(screen.StopNewChat, nc.ID)
	if nc.Clicked {
		p.showNewCall(ac, true)
	}
}

// chips are All and Missed, remembered per number (CA-022): a second click
// on Missed returns to every call, as the chat list's chips do.
func (p *Page) chips(e *screen.Env, a *state.Account, ac *acct) {
	k := e.K
	k.Space(6)
	k.NextGap(4)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "chips", ui.Pct(1), ui.Children(1), 0)
	missed := ac.missed
	for i, name := range [2]string{i18n.T("All"), i18n.T("Missed")} {
		on := ac.missed == (i == 1)
		sig := rexui.ToggleButton(k.Kit, name, &on, rexui.ToggleOpts{Size: theme.SizeSmall})
		e.TabStop([2]screen.TabStop{screen.StopAll, screen.StopMissed}[i], sig.ID)
		if sig.Changed {
			missed = i == 1 && on
		}
	}
	k.EndContainer()
	if missed != ac.missed {
		ac.missed = missed
		if ac.missed {
			a.Settings.CallFilter = append(a.Settings.CallFilter[:0], "missed"...)
		} else {
			a.Settings.CallFilter = nil
		}
		p.h.gen++
		e.S.Touch()
	}
}

// hidden is whether a chat's calls stay out of the history: a locked chat's
// while its folder is closed.
func hiddenChat(a *state.Account, chat []byte) bool {
	ref := a.Model.Chats.Find(chat)
	return ref != 0 && a.Model.Chats.At(ref).Locked() && a.Mode != state.ListLocked
}

// rebuild folds the history and words its rows when anything they show
// changed.
func (p *Page) rebuild(a *state.Account, ac *acct, now time.Time) {
	b := &p.built
	p.needle = appendKey(p.needle[:0], bytes.TrimSpace(p.search.Bytes()))
	day := now.YearDay() + now.Year()*400
	// The mode is in the key: a locked chat's calls show only while its
	// folder is open (hiddenChat).
	if b.ok && b.acct == a.Serial && b.mode == a.Mode && b.log == ac.log.gen && b.contacts == a.Model.Contacts.Gen() && b.chats == a.Model.Chats.Gen() &&
		b.day == day && b.missed == ac.missed && bytes.Equal(b.needle, p.needle) {
		return
	}
	b.ok, b.acct, b.mode, b.log, b.contacts, b.chats, b.day, b.missed = true, a.Serial, a.Mode, ac.log.gen, a.Model.Contacts.Gen(), a.Model.Chats.Gen(), day, ac.missed
	b.needle = append(b.needle[:0], p.needle...)
	l := &ac.log
	p.rows = fold(p.rows[:0], l, func(chat []byte) bool { return hiddenChat(a, chat) })
	p.rb = p.rb[:0]
	for i := range p.rows {
		rw := &p.rows[i]
		e := &l.e[rw.first]
		chat := l.chat(rw.first)
		off := len(p.rb)
		if string(chat) == LinkChat {
			p.rb = append(p.rb, i18n.T("Call link")...)
		} else {
			p.rb = append(p.rb, chatTitle(a, chat)...)
		}
		name := span{uint32(off), uint32(len(p.rb) - off)}
		rw.name = name
		if rw.n > 1 {
			p.rb = append(p.rb, " ("...)
			p.rb = appendInt(p.rb, int64(rw.n))
			p.rb = append(p.rb, ')')
		}
		rw.label = span{uint32(off), uint32(len(p.rb) - off)}
		off = len(p.rb)
		p.rb = appendKey(p.rb, name.of(p.rb))
		rw.key = span{uint32(off), uint32(len(p.rb) - off)}
		off = len(p.rb)
		p.rb = appendSummary(p.rb, e)
		rw.summary = span{uint32(off), uint32(len(p.rb) - off)}
		off = len(p.rb)
		p.rb = appendStamp(p.rb, e.ts, now)
		rw.stamp = span{uint32(off), uint32(len(p.rb) - off)}
	}
	p.shown = p.shown[:0]
	for i := range p.rows {
		rw := &p.rows[i]
		if ac.missed && !l.e[rw.first].missed() {
			continue
		}
		if len(p.needle) > 0 && !bytes.Contains(rw.key.of(p.rb), p.needle) {
			phone := model.Phone(l.chat(rw.first))
			if phone == nil || !bytes.Contains(phone, p.needle) {
				continue
			}
		}
		p.shown = append(p.shown, uint32(i))
	}
}

// history is every call of the account, newest first, a row per run of
// alike calls (CA-021, CA-026).
func (p *Page) history(e *screen.Env, a *state.Account, ac *acct, now time.Time) {
	k := e.K
	l := &ac.log
	if len(l.e) == 0 {
		title, msg := i18n.T("Loading…"), i18n.T("Reading this account's calls.")
		if l.loaded {
			title, msg = i18n.T("No calls yet"), i18n.T("Calls from your phone's call log and the ones you make here appear here.")
		}
		p.empty(e, title, msg)
		return
	}
	p.rebuild(a, ac, now)
	if len(p.shown) == 0 && ac.missed && len(p.needle) == 0 {
		p.empty(e, i18n.T("No missed calls"), i18n.T("Choose All to see every call."))
		return
	}
	// The selection is the picked call while its chat is open beside the list.
	p.hist.Sel.Clear()
	if ac.picked.Len() == 2 {
		if ref := a.Model.Chats.Find(ac.picked.At(0)); ref != 0 && ref == a.Open {
			for i, ri := range p.shown {
				if bytes.Equal(l.id(p.rows[ri].first), ac.picked.At(1)) {
					p.hist.Sel.Only(uint32(i))
				}
			}
		}
	}
	if len(p.shown) == 0 {
		k.Space(12)
		k.NextWidth(ui.Pct(1))
		rexui.Label(k.Kit, i18n.T("No matching calls"), rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)})
		return
	}
	k.NextRowHeight(rowPt)
	lst := k.Kit.BeginList(i18n.T("Call history"), &p.hist, len(p.shown), base.ListOpts{Pad: [2]float32{6, 0}, OwnPad: true})
	for r := lst.First; r < lst.Last; r++ {
		p.historyRow(e, a, ac, &lst, r)
	}
	if lst.Changed {
		if it, ok := p.hist.Sel.Cursor(); ok && int(it) < len(p.shown) {
			p.pick(e, a, ac, int(p.shown[it]))
		}
	}
	lst.CloseRows()
	v := lst.ScrollView()
	rexui.ScrollBars(k.Kit, &v)
	k.Kit.CloseList(&lst)
}

// historyRow is one row: the person, the count when calls fold, the way the
// newest went and what it was, when, and a button that calls back the same
// way (zf:src/ui/calls.rs:314).
func (p *Page) historyRow(e *screen.Env, a *state.Account, ac *acct, lst *base.List, r int) {
	k, c, th := e.K, e.C(), e.K.Theme()
	l := &ac.log
	rw := &p.rows[p.shown[r]]
	en := &l.e[rw.first]
	chat := l.chat(rw.first)
	link := string(chat) == LinkChat
	rs := lst.RowState(r, uint32(r))
	// The row's spoken name: "Ada (2), Missed voice call, 12:30".
	p.buf = append(append(append(append(append(p.buf[:0], rw.label.of(p.rb)...), ", "...), rw.summary.of(p.rb)...), ", "...), rw.stamp.of(p.rb)...)
	fill, fg, fg2 := p.rowColors(e, &rs)
	if fill != 0 {
		c.NextBG(fill)
		c.NextRadius(e.Pt(8))
	}
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(10), 0, e.Pt(6), 0)
	flags := ui.Flags(0)
	if fill != 0 {
		flags = ui.DrawBackground
	}
	lst.BeginRow(&rs, p.buf, flags)
	name := rw.label.of(p.rb)
	if link {
		p.linkAvatar(e, rowAvatarPt, p.ic.link)
	} else {
		rexui.Avatar(k.Kit, model.S(rw.name.of(p.rb)), rexui.AvatarOpts{Size: rowAvatarPt, Image: p.pics.of(e, a, chat, rowAvatarPt)})
	}
	// the text column
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextGap(e.Pt(3))
	c.Push(c.Box(0, "text"))
	nameC := fg
	if en.missed() && !(rs.Selected && rs.Emphasised) {
		nameC = rexui.DangerText.RGBA(th) // a missed call's name is red, as on the phone, in a red that reads
	}
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "line1"))
	hs := &th.Styles[theme.StyleBodySemibold]
	c.NextSize(ui.Pct(1), ui.Px(hs.Line))
	c.NextFont(hs.Font)
	c.NextTextColor(nameC)
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(rs.Key, 1), name)
	cs := &th.Styles[theme.StyleCaption1]
	c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(cs.Line))
	c.NextFont(ztheme.Mono(k, 10.5)) // Rex's metadata, as the chat list's times
	c.NextTextColor(fg2)
	c.BoxKeyed(ui.DrawText, ui.KeyN(rs.Key, 2), rw.stamp.of(p.rb))
	c.Pop()
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(5))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "line2"))
	glyph, gc := p.ic.in, th.Colors[theme.Accent]
	switch {
	case en.missed():
		glyph = p.ic.missed
	case en.fromMe:
		glyph = p.ic.out
	}
	if en.bad() {
		gc = th.Colors[theme.Red]
	}
	if rs.Selected && rs.Emphasised {
		gc = fg
	}
	k.Kit.Icon(glyph, gc)
	ss := &th.Styles[theme.StyleSubheadline]
	c.NextSize(ui.Pct(1), ui.Px(ss.Line))
	c.NextFont(ss.Font)
	c.NextTextColor(fg2)
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(rs.Key, 3), rw.summary.of(p.rb))
	c.Pop()
	c.Pop()
	// One-to-one calls only: a group call, or a link's, has no way back
	// from here.
	if !en.group && !link {
		ic, tip, named := p.ic.voice, i18n.T("Voice call"), i18n.T("Voice call {name}")
		if en.video {
			ic, tip, named = p.ic.video, i18n.T("Video call"), i18n.T("Video call {name}")
		}
		// The button's name tells the rows' buttons apart: "Voice call Ada".
		p.buf2 = appendFill(p.buf2[:0], named, "{name}", rw.name.of(p.rb))
		k.NextSize(ui.Px(e.Pt(30)), ui.Px(e.Pt(30)))
		s := rexui.IconButton(k.Kit, ic, model.S(p.buf2), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
		rexui.Tooltip(k.Kit, s.ID, tip)
		if s.Clicked && !hiddenChat(a, chat) {
			p.h.place(a, chat, en.video)
		}
	} else {
		// the button's room, empty, so every row's time ends at one edge
		c.NextSize(ui.Px(e.Pt(30)), ui.Px(e.Pt(30)))
		c.Box(0, "noback")
	}
}

// rowColors are a row's fill and text colours: the sidebar's selection.
func (p *Page) rowColors(e *screen.Env, rs *base.RowState) (fill, fg, fg2 uint32) {
	th := e.K.Theme()
	fg, fg2 = th.Colors[theme.Label], th.Colors[theme.Label2]
	switch {
	case rs.Selected && !e.K.Inactive():
		fill = rexui.ChatRowLook.Selected.RGBA(th)
		// the chat list's: Rex's selection is a light tint, under which the
		// text keeps its own colours
		fg, fg2 = rexui.ChatRowLook.TitleSelected.RGBA(th), rexui.ChatRowLook.PreviewSelected.RGBA(th)
		rs.Emphasised = true
	case rs.Selected:
		fill = rexui.ChatRowLook.SelectedInactive.RGBA(th)
		rs.Emphasised = false
	default:
		rs.Emphasised = false
	}
	return fill, fg, fg2
}

// pick opens the picked call's chat beside the list at the call (CA-023,
// zf:src/app.rs PickCall). A call from the phone's log with nobody we chat
// with makes the chat first; a call link's opens nothing.
func (p *Page) pick(e *screen.Env, a *state.Account, ac *acct, ri int) {
	l := &ac.log
	chat := l.chat(p.rows[ri].first)
	if string(chat) == LinkChat || hiddenChat(a, chat) {
		return
	}
	ac.newCall = false
	ac.picked.Reset()
	ac.picked.Add(chat)
	id := l.id(p.rows[ri].first)
	ac.picked.Add(id)
	if ref := a.Model.Chats.Find(chat); ref == 0 {
		a.StartChat(chat, a.Model.DisplayName(chat), nil)
	} else if !a.OpenMessage(chat, id) {
		a.OpenChat(ref)
	}
	p.h.gen++
	e.S.Touch()
}

// empty is an empty state in the body.
func (p *Page) empty(e *screen.Env, title, msg string) {
	k := e.K
	k.NextWidth(ui.Pct(1))
	k.NextHeight(ui.Pct(1))
	rexui.EmptyState(k.Kit, title, rexui.EmptyStateOpts{Message: msg, Icon: p.ic.big, HasIcon: true})
}

// linkAvatar is a call link's picture: a link on a plain disc.
func (p *Page) linkAvatar(e *screen.Env, sizePt float32, ic icon.ID) { linkAvatar(e, sizePt, ic) }

func linkAvatar(e *screen.Env, sizePt float32, ic icon.ID) {
	c, th := e.C(), e.K.Theme()
	side := e.Pt(sizePt)
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextBG(th.Colors[theme.Fill2])
	c.NextRadius(side / 2)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextRole(ui.RoleImage)
	c.NextSemName(i18n.T("Call link"))
	c.Push(c.Box(ui.DrawBackground, "link avatar"))
	e.K.Kit.Icon(ic, th.Colors[theme.Label])
	c.Pop()
}

// rebuildPeople lists the new-call list's people: the most called, then
// every contact with a number, searchable (zf:src/ui/calls.rs:511).
func (p *Page) rebuildPeople(a *state.Account, ac *acct) {
	b := &p.pbuilt
	p.needle = appendKey(p.needle[:0], bytes.TrimSpace(p.find.Bytes()))
	if b.ok && b.acct == a.Serial && b.log == ac.log.gen && b.contacts == a.Model.Contacts.Gen() && b.chats == a.Model.Chats.Gen() && bytes.Equal(b.needle, p.needle) {
		return
	}
	b.ok, b.acct, b.log, b.contacts, b.chats = true, a.Serial, ac.log.gen, a.Model.Contacts.Gen(), a.Model.Chats.Gen()
	b.needle = append(b.needle[:0], p.needle...)
	p.persons, p.pbuf = p.persons[:0], p.pbuf[:0]
	me := a.Model.Me.ID()
	locked := func(jid []byte) bool {
		ref := a.Model.Chats.Find(jid)
		return ref != 0 && a.Model.Chats.At(ref).Locked()
	}
	from := 0 // the section's first person: the frequent show again among all
	addPerson := func(jid []byte) {
		phone := model.Phone(jid)
		if phone == nil || bytes.Equal(jid, me) || locked(jid) {
			return
		}
		for i := from; i < len(p.persons); i++ {
			if bytes.Equal(p.persons[i].jid.of(p.pbuf), jid) {
				return
			}
		}
		var ps person
		ps.jid = put(&p.pbuf, jid)
		ps.name = put(&p.pbuf, chatTitle(a, jid))
		off := len(p.pbuf)
		p.pbuf = appendKey(p.pbuf, ps.name.of(p.pbuf))
		ps.key = span{uint32(off), uint32(len(p.pbuf) - off)}
		off = len(p.pbuf)
		p.pbuf = appendPhone(p.pbuf, phone)
		ps.phone = span{uint32(off), uint32(len(p.pbuf) - off)}
		if len(p.needle) > 0 && !bytes.Contains(ps.key.of(p.pbuf), p.needle) && !bytes.Contains(phone, p.needle) {
			p.pbuf = p.pbuf[:ps.jid.off]
			return
		}
		p.persons = append(p.persons, ps)
	}
	// The frequent first, while nothing is typed.
	var freq [frequentCalls]int
	fq := freq[:0]
	if len(p.needle) == 0 {
		fq = frequent(fq, &ac.log, me, func(chat []byte) bool { return locked(chat) || hiddenChat(a, chat) })
	}
	for _, i := range fq {
		addPerson(ac.log.chat(i))
	}
	p.nfreq = len(p.persons)
	from = p.nfreq
	for i := 0; i < a.Model.Contacts.Len(); i++ {
		addPerson(a.Model.Contacts.At(i).ID())
	}
	for _, ref := range a.Model.Chats.Order() {
		ch := a.Model.Chats.At(ref)
		if ch.Kind() == abi.ChatKindDirect {
			addPerson(ch.ID())
		}
	}
	all := p.persons[p.nfreq:]
	sort.SliceStable(all, func(i, j int) bool { return bytes.Compare(all[i].key.of(p.pbuf), all[j].key.of(p.pbuf)) < 0 })
	// The frequent appear again among every contact, as upstream lists
	// them (its contact list does not leave them out).
	p.listed = p.listed[:0]
	if p.nfreq > 0 {
		p.listed = append(p.listed, itemFrequentHeader)
		for i := 0; i < p.nfreq; i++ {
			p.listed = append(p.listed, itemFrequent+uint32(i))
		}
	}
	if len(all) > 0 {
		p.listed = append(p.listed, itemAllHeader)
		for i := range all {
			p.listed = append(p.listed, uint32(p.nfreq+i))
		}
	}
}

// newCallList is WhatsApp's new-call list: the people called most often,
// then every contact, each with a voice and a video button; a row alone
// starts nothing, so a stray click never rings anyone (CA-024).
func (p *Page) newCallList(e *screen.Env, a *state.Account, ac *acct) {
	k := e.K
	p.rebuildPeople(a, ac)
	if len(p.persons) == 0 {
		k.Space(12)
		k.NextWidth(ui.Pct(1))
		rexui.Label(k.Kit, i18n.T("No matching contacts"), rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)})
		return
	}
	p.people.Sel.Clear()
	k.NextRowHeight(rowPt)
	lst := k.Kit.BeginList(i18n.T("Contacts"), &p.people, len(p.listed), base.ListOpts{Pad: [2]float32{6, 0}, OwnPad: true})
	for r := lst.First; r < lst.Last; r++ {
		if p.personRow(e, a, ac, &lst, r) {
			break
		}
	}
	p.people.Sel.Clear()
	lst.CloseRows()
	v := lst.ScrollView()
	rexui.ScrollBars(k.Kit, &v)
	k.Kit.CloseList(&lst)
}

// personRow is one row of the new-call list, or a section's header; it
// reports whether a call started (the list goes back to the history).
func (p *Page) personRow(e *screen.Env, a *state.Account, ac *acct, lst *base.List, r int) bool {
	k, c, th := e.K, e.C(), e.K.Theme()
	it := p.listed[r]
	rs := lst.RowState(r, uint32(r))
	if it == itemFrequentHeader || it == itemAllHeader {
		title := i18n.T("All contacts")
		if it == itemFrequentHeader {
			title = i18n.T("Frequently contacted")
		}
		c.NextCross(ui.CrossEnd)
		c.NextPadding(e.Pt(10), 0, e.Pt(10), e.Pt(8))
		lst.BeginRow(&rs, nil, 0)
		rexui.Label(k.Kit, title, rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Accent)})
		return false
	}
	i := int(it)
	if it >= itemFrequent {
		i = int(it - itemFrequent)
	}
	ps := &p.persons[i]
	name := ps.name.of(p.pbuf)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(10), 0, e.Pt(6), 0)
	lst.BeginRow(&rs, name, 0)
	rexui.Avatar(k.Kit, model.S(name), rexui.AvatarOpts{Size: rowAvatarPt, Image: p.pics.of(e, a, ps.jid.of(p.pbuf), rowAvatarPt)})
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextGap(e.Pt(3))
	c.Push(c.Box(0, "text"))
	hs := &th.Styles[theme.StyleBodySemibold]
	c.NextSize(ui.Pct(1), ui.Px(hs.Line))
	c.NextFont(hs.Font)
	c.NextTextColor(th.Colors[theme.Label])
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(rs.Key, 1), name)
	ss := &th.Styles[theme.StyleSubheadline]
	c.NextSize(ui.Pct(1), ui.Px(ss.Line))
	c.NextFont(ss.Font)
	c.NextTextColor(th.Colors[theme.Label3])
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(rs.Key, 2), ps.phone.of(p.pbuf))
	c.Pop()
	jid := ps.jid.of(p.pbuf)
	started := false
	p.buf = appendFill(p.buf[:0], i18n.T("Voice call {name}"), "{name}", name)
	k.NextSize(ui.Px(e.Pt(30)), ui.Px(e.Pt(30)))
	s := rexui.IconButton(k.Kit, p.ic.voice, model.S(p.buf), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	rexui.Tooltip(k.Kit, s.ID, model.S(p.buf))
	if s.Clicked {
		p.h.place(a, jid, false)
		started = true
	}
	p.buf = appendFill(p.buf[:0], i18n.T("Video call {name}"), "{name}", name)
	k.NextSize(ui.Px(e.Pt(30)), ui.Px(e.Pt(30)))
	s = rexui.IconButton(k.Kit, p.ic.video, model.S(p.buf), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	rexui.Tooltip(k.Kit, s.ID, model.S(p.buf))
	if s.Clicked && !started {
		p.h.place(a, jid, true)
		started = true
	}
	if started {
		p.showNewCall(ac, false)
	}
	return started
}
