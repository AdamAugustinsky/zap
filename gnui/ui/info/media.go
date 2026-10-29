package info

import (
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// thumbCache is the scratch a thumbnail's bytes are read into.
type thumbCache struct{ buf []byte }

func mediaTitle() string { return i18n.T("Media, links and docs") }

// thumb is the image of a listed picture: the archived preview, else the
// downloaded file. 0 while there is none; the cache decodes at the size
// shown.
func (s *Screen) thumb(e *screen.Env, a *state.Account, m *mediaItem, px int32) img.Handle {
	cache := e.K.Images()
	if cache == nil || px <= 0 {
		return 0
	}
	key := img.Key{Hash: img.HashBytes(s.sh.bytes(m.msg)) ^ uint64(px)<<48, W: px, H: px, Flags: img.Cover}
	if _, _, ok := cache.Get(key); ok {
		h, _ := cache.Request(key, img.Source{})
		return h
	}
	if m.hasThumb && !m.thumbTried && a.Conn != nil {
		n := int(m.thumb.Len)
		if n > 0 && n <= 256<<10 {
			if cap(s.thumbs.buf) < n {
				s.thumbs.buf = make([]byte, n)
			}
			b := s.thumbs.buf[:n]
			got, err := a.Conn.ReadBlob(m.thumb.ID, 0, b)
			if err == nil && got > 0 {
				h, _ := cache.Request(key, img.Bytes(b[:got]))
				return h
			}
		}
		m.thumbTried = true
	}
	if !m.video && m.path.n > 0 {
		h, _ := cache.Request(key, img.File(s.sh.str(m.path)))
		return h
	}
	return 0
}

// mediaRow is the "Media, links and docs" row, its count and chevron, and the
// newest pictures under it.
func (s *Screen) mediaRow(e *screen.Env, a *state.Account, contentPt float32) {
	k := e.K
	c := e.C()
	r := k.Theme()
	p := k.PressState(mediaTitle(), base.HoverOn)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(4))
	c.NextRadius(k.Pt(6))
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), k.Fixed(36), 0, nil)
	k.NextWidth(widget.Fill)
	c.NextSize(ui.Pct(1), ui.Px(k.Pt(18)))
	c.NextFont(s.font(e, 13.5, 400))
	c.NextTextColor(r.Colors[theme.Label2])
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 1), base.StrBytes(p.Name))
	if s.sh.loaded {
		total := s.sh.counts[0] + s.sh.counts[1] + s.sh.counts[2]
		f := c.Fmt().Int(total)
		if s.sh.cut[0] || s.sh.cut[1] || s.sh.cut[2] {
			f.Str("+")
		}
		k.Text(2, f.String(), s.font(e, 13, 400), r.Colors[theme.Label3], ui.AlignRight, k.Pt(18))
	}
	k.Icon(s.ic.chevronRight, r.Colors[theme.Label3])
	sig := k.EndPress(&p)
	e.TabStop(screen.StopInfoControl+mediaRowStop, sig.ID)
	if p.Hot > 0 {
		_ = sig
	}
	if sig.Clicked {
		e.S.Panes.InfoView = state.InfoMedia
		e.S.Touch()
	}
	if s.sh.failed {
		s.failure(e, a)
	}
	if s.sh.loaded && len(s.sh.media) > 0 {
		cell := min((contentPt-2*gapPt)/3, 110)
		k.NextGap(gapPt)
		k.NextJustify(ui.JustifyStart)
		k.BeginContainer(ui.AxisX, ui.CrossStart, "strip", ui.Pct(1), ui.Children(1), 0)
		for i := 0; i < min(3, len(s.sh.media)); i++ {
			s.mediaCell(e, a, i, cell)
		}
		k.EndContainer()
	}
}

// failure says the listing could not be made, with a button that asks again.
func (s *Screen) failure(e *screen.Env, a *state.Account) {
	k := e.K
	k.NextGap(8)
	k.NextJustify(ui.JustifyCenter)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "failure", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, i18n.T("Could not list the media."), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	if rexui.ButtonIcon(k.Kit, s.ic.refresh, i18n.T("Try again"), rexui.ButtonOpts{}).Clicked {
		s.request(a)
	}
	k.EndContainer()
}

// mediaCell is one square picture or video: its preview cropped to the
// square, with a play mark and the length on a video. It goes to the message.
func (s *Screen) mediaCell(e *screen.Env, a *state.Account, i int, side float32) {
	k := e.K
	c := e.C()
	r := k.Theme()
	m := &s.sh.media[i]
	px := k.Pt(side)
	p := k.PressState(s.sh.str(m.label), base.HoverOn)
	c.NextRadius(k.Pt(6))
	k.BeginPress(&p, ui.RoleButton, ui.Px(px), ui.Px(px), ui.Clip, nil)
	h := s.thumb(e, a, m, int32(px))
	if h != 0 {
		k.NextSize(ui.Px(px), ui.Px(px))
		k.Image(1, h, ui.FitFill, k.Pt(6), "")
	} else {
		theme.At(c, 0, 0, px, px)
		c.NextBG(r.Colors[theme.Fill2])
		c.NextRadius(k.Pt(6))
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 2), nil)
		ic := s.ic.photo
		if m.video {
			ic = s.ic.video
		}
		c.NextCenter()
		c.NextAuditExempt(ui.AuditOverflow.Mask())
		k.Icon(ic, r.Colors[theme.Label3])
	}
	if m.video {
		d := k.Pt(30)
		c.NextCenter()
		c.NextSize(ui.Px(d), ui.Px(d))
		c.NextBG(0x0000008C)
		c.NextRadius(d / 2)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.Push(c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 4), nil))
		k.Icon(s.ic.play, 0xFFFFFFFF)
		c.Pop()
		if m.durationTxt.n > 0 {
			t := s.sh.bytes(m.durationTxt)
			w := k.Pt(float32(len(t))*6.5 + 8)
			hh := k.Pt(15)
			theme.At(c, px-w-k.Pt(4), px-hh-k.Pt(4), w, hh)
			c.NextBG(0x0000008C)
			c.NextRadius(k.Pt(3))
			c.NextAxis(ui.AxisX)
			c.NextCross(ui.CrossCenter)
			c.NextJustify(ui.JustifyCenter)
			c.Push(c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 5), nil))
			c.NextSize(ui.TextSize(0), ui.Px(k.Pt(14)))
			c.NextFont(s.font(e, 11, 500))
			c.NextTextColor(0xFFFFFFFF)
			c.BoxKeyed(ui.DrawText, ui.KeyN(p.Key, 6), t)
			c.Pop()
		}
	}
	if p.Hot > 0 {
		theme.At(c, 0, 0, px, px)
		c.NextBG(theme.WithAlpha(0xFFFFFFFF, 0.1*p.Hot))
		c.NextRadius(k.Pt(6))
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 7), nil)
	}
	if k.EndPress(&p).Clicked {
		s.goTo(e, a, s.sh.bytes(m.chat), s.sh.bytes(m.msg))
	}
}

// goTo jumps to a listed message, which opens its chat and flashes it. Over a
// narrow chat the panel would hide it, so the panel closes.
func (s *Screen) goTo(e *screen.Env, a *state.Account, chat, msg []byte) {
	if !a.OpenMessage(chat, msg) {
		return
	}
	if e.S.Panes.Covering {
		s.closePanel(e)
	}
}

// window is the rows of a fixed-height-row list in view, with a row of slack
// on either side.
func (s *Screen) window(e *screen.Env, v base.ScrollView, rows int, rowPx, viewPx float32) (first, last int) {
	_, sy := e.C().Scroll(v.Inner)
	first = max(int(float32(sy)/rowPx)-1, 0)
	last = min(first+int(viewPx/rowPx)+3, rows)
	first = min(first, rows)
	return first, last
}

// gap is a box of h pixels along the column.
func gap(e *screen.Env, label string, h float32) {
	if h <= 0 {
		return
	}
	c := e.C()
	c.NextSize(ui.Px(0), ui.Px(h))
	c.Box(0, label)
}

// members is the group's members, each opening their own panel.
func (s *Screen) members(e *screen.Env, a *state.Account, r screen.Rect) {
	k := e.K
	c := e.C()
	k.NextGap(4)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "members", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, c.Fmt().Str(i18n.T("Members")).Str(" (").Int(len(s.v.list)).Str(")").String(), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Label2)})
	_, wh := c.Size()
	winPt := float32(wh) / k.Scale()
	rows := min(max(int((winPt-200-300)/memberPt), 2), 8)
	rowPx := k.Pt(memberPt)
	k.NextSize(ui.Pct(1), ui.Px(rowPx*float32(min(rows, len(s.v.list)))))
	v := k.BeginScroll(i18n.T("Members list"), &s.memberSc, base.ScrollVertical)
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "rows", ui.Pct(1), ui.Children(1), 0)
	first, last := s.window(e, v, len(s.v.list), rowPx, rowPx*float32(rows))
	gap(e, "above", float32(first)*rowPx)
	for i := first; i < last; i++ {
		s.memberRow(e, a, &s.v.list[i], rowPx)
	}
	gap(e, "below", float32(len(s.v.list)-last)*rowPx)
	k.EndContainer()
	k.EndScroll(v)
	k.EndContainer()
}

func (s *Screen) memberRow(e *screen.Env, a *state.Account, m *member, rowPx float32) {
	k := e.K
	c := e.C()
	th := k.Theme()
	jid := s.v.text(m.jid)
	p := k.PressState(model.S(s.v.text(m.label)), base.HoverOn)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(8))
	c.NextPadding(k.Pt(8), 0, k.Pt(8), 0)
	c.NextRadius(k.Pt(6))
	c.NextBG(theme.WithAlpha(th.Colors[theme.Label], 0.08*p.Hot))
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), ui.Px(rowPx), 0, nil)
	opts := rexui.AvatarOpts{Size: 24, Image: screen.Avatar(e.K.Images(), a, jid, false, int32(k.Pt(24)))}
	name := model.S(s.v.text(m.name))
	rexui.Avatar(k.Kit, trimTilde(name), opts)
	c.NextSize(ui.Pct(1), ui.Px(k.Pt(18)))
	c.NextFont(s.font(e, 13, 400))
	c.NextTextColor(th.Colors[theme.Label])
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 1), base.StrBytes(name))
	if k.EndPress(&p).Clicked && !m.me {
		e.S.Panes.SetInfo(jid)
		e.S.Touch()
	}
}

func trimTilde(s string) string {
	if len(s) > 0 && s[0] == '~' {
		return s[1:]
	}
	return s
}

// options are the tabs' labels with their counts.
func (s *Screen) tabOptions() []string {
	if s.optionGen != s.sh.gen+1 || s.options[0] == "" {
		names := [3]string{i18n.P("info tab", "Media"), i18n.P("info tab", "Docs"), i18n.P("info tab", "Links")}
		for i := range names {
			if s.sh.loaded && s.sh.counts[i] > 0 {
				s.options[i] = names[i] + " " + itoa(s.sh.counts[i])
			} else {
				s.options[i] = names[i]
			}
		}
		s.optionGen = s.sh.gen + 1
	}
	return s.options[:]
}

// mediaView is the media view: a back header, the three tabs, and the tab's
// list.
func (s *Screen) mediaView(e *screen.Env, a *state.Account, r screen.Rect) {
	k := e.K
	if s.header(e, s.ic.back, i18n.T("Back"), mediaTitle(), true) {
		e.S.Panes.InfoView = state.InfoOverview
		e.S.Touch()
		return
	}
	s.tab = int(e.S.Panes.InfoView) - 1
	k.NextPadding(ztheme.T.InfoMargin, 12, ztheme.T.InfoMargin, 12)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "tabs", ui.Pct(1), ui.Children(1), 0)
	tabs := rexui.SegmentedControl(k.Kit, mediaTitle(), &s.tab, s.tabOptions(), rexui.SegmentedOpts{Role: rexui.SegmentedTabs})
	// the chosen tab; the arrows go among them
	e.TabStopKey(screen.StopInfoControl+tabStop+screen.TabStop(min(max(s.tab, 0), 9)), ui.KeyN(e.C().KeyOf(tabs.ID), uint64(max(s.tab, 0))))
	if tabs.Changed {
		e.S.Panes.InfoView = state.InfoView(s.tab + 1)
		e.S.Touch()
	}
	k.EndContainer()
	rexui.Separator(k.Kit, rexui.SeparatorOpts{Hairline: true})
	switch {
	case s.sh.loaded:
		tab := int(e.S.Panes.InfoView) - 1
		if s.sh.counts[tab] > 0 {
			s.findField(e, tab)
		}
		if s.sh.cut[tab] {
			k.NextPadding(ztheme.T.InfoMargin, 8, ztheme.T.InfoMargin, 4)
			k.BeginContainer(ui.AxisX, ui.CrossStart, "note", ui.Pct(1), ui.Children(1), 0)
			rexui.Label(k.Kit, i18n.T("Only the newest items are listed."), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
			k.EndContainer()
		}
		switch tab {
		case 0:
			s.mediaGrid(e, a, r)
		case 1:
			s.docsList(e, a, r)
		default:
			s.linksList(e, a, r)
		}
	case s.sh.failed:
		k.NextPadding(ztheme.T.InfoMargin, 24, ztheme.T.InfoMargin, 0)
		k.BeginContainer(ui.AxisX, ui.CrossStart, "failed", ui.Pct(1), ui.Children(1), 0)
		s.failure(e, a)
		k.EndContainer()
	case s.sh.pending:
		k.NextPadding(0, 32, 0, 0)
		k.NextJustify(ui.JustifyCenter)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "loading", ui.Pct(1), ui.Children(1), 0)
		rexui.Spinner(k.Kit, i18n.T("Loading…"), rexui.SpinnerOpts{Size: theme.SizeSmall})
		k.EndContainer()
	}
}

// findField is the search over the tab's list, under the tabs.
func (s *Screen) findField(e *screen.Env, tab int) {
	k := e.K
	k.NextPadding(ztheme.T.InfoMargin, 8, ztheme.T.InfoMargin, 8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "find", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(ui.Pct(1))
	sig := rexui.SearchField(k.Kit, i18n.T("Search")+"###info-find", &s.find.buf, i18n.T("Search"), rexui.FieldOpts{})
	e.TabStop(screen.StopInfoControl+findStop, sig.ID)
	k.EndContainer()
	s.find.update(&s.sh, tab)
}

// noHits is what a tab shows when nothing matches the search.
func (s *Screen) noHits(e *screen.Env) bool {
	if !s.find.on {
		return false
	}
	s.empty(e, i18n.T("No results"), "", false)
	return true
}

func (s *Screen) empty(e *screen.Env, title, msg string, ic bool) {
	k := e.K
	k.NextSize(ui.Pct(1), widget.Fill)
	rexui.EmptyState(k.Kit, title, rexui.EmptyStateOpts{Message: msg})
}

// mediaGrid is the chat's pictures and videos, newest first, in square cells.
func (s *Screen) mediaGrid(e *screen.Env, a *state.Account, r screen.Rect) {
	k := e.K
	if len(s.sh.media) == 0 {
		s.empty(e, i18n.T("No media"), i18n.T("Photos and videos shared in this chat will show here."), true)
		return
	}
	width := r.W/k.Scale() - 2*ztheme.T.InfoMargin - 17
	cols := 3
	if width >= 4*84+3*gapPt {
		cols = 4
	}
	n := s.find.rows(len(s.sh.media))
	if n == 0 && s.noHits(e) {
		return
	}
	cell := (width - float32(cols-1)*gapPt) / float32(cols)
	rows := (n + cols - 1) / cols
	rowPx := k.Pt(cell + gapPt)
	k.NextSize(ui.Pct(1), widget.Fill)
	v := k.BeginScroll(i18n.T("Pictures and videos"), &s.gridSc, base.ScrollVertical)
	k.NextPadding(ztheme.T.InfoMargin, 8, ztheme.T.InfoMargin, 8)
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "grid", ui.Pct(1), ui.Children(1), 0)
	first, last := s.window(e, v, rows, rowPx, r.H)
	gap(e, "above", float32(first)*rowPx)
	for row := first; row < last; row++ {
		k.NextGap(gapPt)
		k.BeginContainer(ui.AxisX, ui.CrossStart, rowName(row), ui.Pct(1), ui.Px(rowPx), 0)
		for col := 0; col < cols; col++ {
			if i := row*cols + col; i < n {
				s.mediaCell(e, a, s.find.at(i), cell)
			}
		}
		k.EndContainer()
	}
	gap(e, "below", float32(rows-last)*rowPx)
	k.EndContainer()
	k.EndScroll(v)
}

func rowName(i int) string {
	return "row " + itoa(i)
}

// itemRow is one document or link row: an icon in a tile, a title and a
// detail line. A pointer and accessibility target.
func (s *Screen) itemRow(e *screen.Env, label string, title, detail []byte, tile int) bool {
	k := e.K
	c := e.C()
	th := k.Theme()
	rowPx := k.Pt(docPt)
	p := k.PressState(label, base.HoverOn)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(12))
	c.NextPadding(k.Pt(ztheme.T.InfoMargin), 0, k.Pt(ztheme.T.InfoMargin), 0)
	c.NextBG(theme.WithAlpha(th.Colors[theme.Label], 0.06*p.Hot))
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), ui.Px(rowPx), 0, nil)
	d := k.Pt(40)
	c.NextSize(ui.Px(d), ui.Px(d))
	c.NextBG(th.Colors[theme.Fill2])
	c.NextRadius(k.Pt(8))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.Push(c.BoxKeyed(ui.DrawBackground, ui.KeyN(p.Key, 1), nil))
	ic := s.ic.doc
	if tile == 1 {
		ic = s.ic.link
	}
	k.Icon(ic, th.Colors[theme.Label2])
	c.Pop()
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextGap(k.Pt(4))
	c.Push(c.BoxKeyed(0, ui.KeyN(p.Key, 2), nil))
	c.NextSize(ui.Pct(1), ui.Px(k.Pt(18)))
	c.NextFont(s.font(e, 13.5, 500))
	c.NextTextColor(th.Colors[theme.Label])
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 3), title)
	c.NextSize(ui.Pct(1), ui.Px(k.Pt(16)))
	c.NextFont(s.font(e, 12, 400))
	c.NextTextColor(th.Colors[theme.Label2])
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 4), detail)
	c.Pop()
	return k.EndPress(&p).Clicked
}

// docsList is the chat's documents, newest first.
func (s *Screen) docsList(e *screen.Env, a *state.Account, r screen.Rect) {
	k := e.K
	if len(s.sh.docs) == 0 {
		s.empty(e, i18n.T("No documents"), i18n.T("Files shared in this chat will show here."), true)
		return
	}
	n := s.find.rows(len(s.sh.docs))
	if n == 0 && s.noHits(e) {
		return
	}
	rowPx := k.Pt(docPt)
	k.NextSize(ui.Pct(1), widget.Fill)
	v := k.BeginScroll(i18n.T("Documents"), &s.docSc, base.ScrollVertical)
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "docs", ui.Pct(1), ui.Children(1), 0)
	first, last := s.window(e, v, n, rowPx, r.H)
	gap(e, "above", float32(first)*rowPx)
	for i := first; i < last; i++ {
		d := &s.sh.docs[s.find.at(i)]
		if s.itemRow(e, s.sh.str(d.label), s.sh.bytes(d.name), s.sh.bytes(d.detail), 0) {
			s.goTo(e, a, s.sh.bytes(d.chat), s.sh.bytes(d.msg))
		}
	}
	gap(e, "below", float32(n-last)*rowPx)
	k.EndContainer()
	k.EndScroll(v)
}

// linksList is the web links written in the chat, newest first. A row opens
// the address in the browser.
func (s *Screen) linksList(e *screen.Env, a *state.Account, r screen.Rect) {
	k := e.K
	if len(s.sh.links) == 0 {
		s.empty(e, i18n.T("No links"), i18n.T("Links shared in this chat will show here."), true)
		return
	}
	n := s.find.rows(len(s.sh.links))
	if n == 0 && s.noHits(e) {
		return
	}
	rowPx := k.Pt(docPt)
	k.NextSize(ui.Pct(1), widget.Fill)
	v := k.BeginScroll(i18n.P("info tab", "Links"), &s.linkSc, base.ScrollVertical)
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "links", ui.Pct(1), ui.Children(1), 0)
	first, last := s.window(e, v, n, rowPx, r.H)
	gap(e, "above", float32(first)*rowPx)
	for i := first; i < last; i++ {
		l := &s.sh.links[s.find.at(i)]
		if s.itemRow(e, s.sh.str(l.label), s.sh.bytes(l.title), s.sh.bytes(l.detail), 1) {
			s.openURL(e, a, s.sh.str(l.url))
		}
	}
	gap(e, "below", float32(n-last)*rowPx)
	k.EndContainer()
	k.EndScroll(v)
}

// openURL opens an address in the browser, or an invite link in the
// join-group dialog; anything else is refused (zf:src/safety.rs).
func (s *Screen) openURL(e *screen.Env, a *state.Account, raw string) {
	if code := inviteCode(raw); code != "" {
		cmd := a.Cmd(abi.CommandTagPreviewInvite)
		cmd.PreviewInvite.Code = []byte(code)
		a.Send(cmd)
		e.S.OpenDialog(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte(code)})
		return
	}
	u := externalURL(raw)
	if u == "" {
		e.S.Toast([]byte(i18n.T("This link type cannot be opened from Zap")), true)
		return
	}
	sh := e.S.Shell
	go func() { _ = sh.Open(u) }()
}
