package picker

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/ui/screen"
)

// The emoji grid and its category tabs (zf:src/ui/picker.rs:357, :602).

const (
	cellPt     = 40 // the least width of an emoji cell; columns share the width
	catMaxPt   = 36
	catMinPt   = 24
	scrollRoom = 6 // points kept for the scroll bar
)

// fieldCmd is a search field's commander: the keys the picker takes before
// the field does. The arrows move the selection in the emoji grid and Enter
// chooses; Escape closes the picker (upstream's keys.rs).
type fieldCmd struct {
	arrows bool // take the arrows (the emoji tab's)
	move   moveKey
	enter  bool
	esc    bool
}

// FieldCommand implements ui.FieldCommander.
func (f *fieldCmd) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	switch cmd {
	case ui.FieldMoveLeft:
		if f.arrows {
			f.move = moveLeft
			return true
		}
	case ui.FieldMoveRight:
		if f.arrows {
			f.move = moveRight
			return true
		}
	case ui.FieldMoveUp:
		if f.arrows {
			f.move = moveUp
			return true
		}
	case ui.FieldMoveDown:
		if f.arrows {
			f.move = moveDown
			return true
		}
	case ui.FieldInsertNewline:
		f.enter = true
		return true
	case ui.FieldCancel:
		f.esc = true
		return true
	}
	return false
}

// emojiState is the emoji tab's.
type emojiState struct {
	query    ui.TextBuffer
	cmd      fieldCmd
	g        grid
	recent   []int32 // the recent emoji as the picker opened, kept in order while it is open
	selected int
	jump     int // a category to scroll to, -1 for none
	focus    bool
	top      bool // scroll to the top
	reveal   bool // scroll the selection into view
	scroll   base.ScrollState
	visCat   int8
}

func (em *emojiState) init() {
	em.query.Max = 128
	em.query.Reserve(128)
	em.jump = -1
}

// reset is the tab opening (zf:src/app.rs, Action::TogglePicker).
func (em *emojiState) reset() {
	em.query.Reset()
	em.selected, em.jump, em.focus, em.top = 0, -1, true, true
	em.g.query = "\x00" // rebuild
}

// categories are the tabs under the grid: the recent one when there are
// recent emoji, then the groups.
func (s *Screen) categories(hasRecent bool, recentLabel string, dst *[numGroups + 1]catTab) []catTab {
	n := 0
	if hasRecent {
		dst[n] = catTab{cat: 0, glyph: recentGlyph, label: recentLabel}
		n++
	}
	for g := 0; g < numGroups; g++ {
		dst[n] = catTab{cat: int8(g + 1), glyph: groupGlyphs[g], label: groupName(g)}
		n++
	}
	return dst[:n]
}

type catTab struct {
	cat          int8
	glyph, label string
}

// emojiBody builds the emoji tab: the search field, the grid and the
// category tabs. It reports the emoji chosen this frame ("" for none).
func (s *Screen) emojiBody(e *screen.Env, reaction bool) (picked string) {
	k := e.K
	c, r := k.Context(), k.Theme()
	em := &s.em
	label := i18n.T("Recent")
	if reaction {
		label = i18n.T("Frequently Used")
	}
	recent := em.recent
	if reaction {
		recent = s.frequentIdx
	}

	em.cmd = fieldCmd{arrows: true}
	c.NextFieldCommander(&em.cmd, "")
	search := i18n.T("Search emoji")
	fs := rexui.SearchField(k.Kit, search, &em.query, search, rexui.FieldOpts{})
	if em.focus {
		k.Focus(fs.Key)
		em.focus = false
	}
	if em.cmd.esc {
		s.wantClose = true
	}

	rowPx := e.Pt(cellPt)
	v := k.BeginScroll("emoji grid", &em.scroll, base.ScrollVertical)
	_, off := c.Scroll(v.Inner)
	// the grid, rebuilt when what it depends on changed
	viewW := s.viewWidth(e, &v)
	cols := max(int(viewW/e.Pt(cellPt)), 1)
	cell := viewW / float32(cols)
	g := &em.g
	if g.cols != cols || g.label != label || g.gen == 0 || string(em.query.Bytes()) != g.query || len(recent) != len(g.recent) {
		changed := string(em.query.Bytes()) != g.query
		g.build(string(em.query.Bytes()), recent, cols, label)
		if changed {
			em.selected, em.top = 0, true
		}
	}
	if em.cmd.move != moveNone {
		em.selected = moveSelection(em.selected, g.count, cols, em.cmd.move)
		em.reveal = true
	} else {
		em.selected = max(min(em.selected, g.count-1), 0)
	}
	if em.cmd.enter && g.count > 0 {
		picked = emojis().All[g.flat[em.selected]].Glyph
	}

	switch {
	case em.jump >= 0 && g.query == "":
		if row := g.headerRow(em.jump); row >= 0 {
			off = float64(row) * float64(rowPx)
			c.SetScroll(v.Inner, 0, off)
		}
		em.jump = -1
	case em.top:
		off = 0
		c.SetScroll(v.Inner, 0, 0)
	case em.reveal:
		if row := g.rowOf(em.selected); row >= 0 {
			rc := c.Rect(v.Outer)
			viewH := float64(rc[3] - rc[1])
			top := float64(row) * float64(rowPx)
			switch {
			case top < off:
				off = top
			case top+float64(rowPx) > off+viewH:
				off = top + float64(rowPx) - viewH
			}
			c.SetScroll(v.Inner, 0, max(off, 0))
		}
	}
	em.top, em.reveal = false, false
	if vi := int(off / float64(rowPx)); vi >= 0 && vi < len(g.rows) {
		em.visCat = g.rows[vi].cat
	} else {
		em.visCat = 0
	}
	first, last := c.ListRange(v.Inner, len(g.rows), rowPx, 2)
	for i := first; i < last; i++ {
		row := &g.rows[i]
		if row.label != "" {
			s.headerRow(e, uint64(i), row.label, rowPx)
			continue
		}
		c.NextSize(ui.Pct(1), ui.Px(rowPx))
		c.NextAxis(ui.AxisX)
		c.NextGap(0)
		c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), uint64(i)+1), nil))
		for j := int32(0); j < row.n; j++ {
			idx := int(row.from + j)
			glyph := emojis().All[g.flat[idx]].Glyph
			if s.emojiCell(e, glyph, idx == em.selected, cell, rowPx) {
				em.selected = idx
				picked = glyph
			}
		}
		c.Pop()
	}
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k.Kit, &v)
	k.CloseScroll(&v)

	// the category tabs
	var tabs [numGroups + 1]catTab
	cats := s.categories(len(recent) > 0, label, &tabs)
	catPx := min(max((e.Pt(420)-e.Pt(4))/float32(len(cats)), e.Pt(catMinPt)), e.Pt(catMaxPt))
	c.NextSize(ui.Pct(1), ui.Px(catPx))
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(0, "categories"))
	for i := range cats {
		t := &cats[i]
		on := g.query == "" && em.visCat == t.cat
		if g.query == "" && em.visCat == 0 && len(recent) == 0 {
			on = t.cat == 1
		}
		if s.categoryTab(e, t, on, catPx) {
			em.query.Reset()
			em.selected, em.jump = 0, int(t.cat)
		}
	}
	c.Pop()
	_ = r
	return picked
}

// headerRow is a group's heading row: its name, bottom left.
func (s *Screen) headerRow(e *screen.Env, n uint64, label string, rowPx float32) {
	k := e.K
	c, r := k.Context(), k.Theme()
	c.NextSize(ui.Pct(1), ui.Px(rowPx))
	c.NextAxis(ui.AxisY)
	c.NextJustify(ui.JustifyEnd)
	c.NextPadding(e.Pt(4), 0, 0, e.Pt(8))
	c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), n+1), nil))
	rexui.Label(k.Kit, label, rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Label2)})
	c.Pop()
	_ = r
}

// emojiCell is one emoji of the grid: coloured by the text system, with the
// selection and the pointer shown behind it. It reports a click.
func (s *Screen) emojiCell(e *screen.Env, glyph string, selected bool, w, h float32) bool {
	k := e.K
	c, r := k.Context(), k.Theme()
	key := ui.HashString(c.ParentKey(), glyph)
	hot := c.HotTOf(key)
	flags := ui.Clickable | ui.Hoverable | ui.HotAnimation
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextRadius(e.Pt(6))
	switch {
	case selected:
		c.NextBG(theme.WithAlpha(r.Colors[theme.Accent], 0.22))
		c.NextBorder(r.Colors[theme.Accent], r.Lines.Hairline)
		flags |= ui.DrawBackground | ui.DrawBorder
	case hot > 0.004:
		c.NextBG(theme.WithAlpha(r.Colors[theme.Fill2], hot))
		flags |= ui.DrawBackground
	}
	c.NextRole(ui.RoleButton)
	c.NextSemName(glyph)
	id := c.BoxKeyed(flags, key, nil)
	c.Push(id)
	c.NextFont(s.fonts.emoji)
	c.NextTextColor(r.Colors[theme.Label])
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.NextAlign(ui.AlignCenter)
	c.Box(ui.DrawText, glyph)
	c.Pop()
	return c.Signal(id).Clicked
}

// categoryTab is one tab under the grid: its glyph, with the current one lit
// and underlined. It reports a click.
func (s *Screen) categoryTab(e *screen.Env, t *catTab, on bool, side float32) bool {
	k := e.K
	c, r := k.Context(), k.Theme()
	key := ui.HashString(c.ParentKey(), t.label)
	hot := c.HotTOf(key)
	flags := ui.Clickable | ui.Hoverable | ui.HotAnimation
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextRadius(e.Pt(6))
	switch {
	case on:
		c.NextBG(theme.WithAlpha(r.Colors[theme.Accent], 0.22))
		flags |= ui.DrawBackground
	case hot > 0.004:
		c.NextBG(theme.WithAlpha(r.Colors[theme.Fill2], hot))
		flags |= ui.DrawBackground
	}
	c.NextRole(ui.RoleTab)
	c.NextSemName(t.label)
	if on {
		c.NextSemState(ui.SemSelected)
	}
	id := c.BoxKeyed(flags, key, nil)
	c.Push(id)
	c.NextFont(s.fonts.cat)
	c.NextTextColor(r.Colors[theme.Label])
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.Box(ui.DrawText, t.glyph)
	if on {
		w := side - e.Pt(12)
		theme.At(c, e.Pt(6), side-e.Pt(3), w, e.Pt(2))
		c.NextBG(r.Colors[theme.Accent])
		c.Box(base.Floating|ui.DrawBackground, "underline")
	}
	c.Pop()
	rexui.Tooltip(k.Kit, id, t.label)
	return c.Signal(id).Clicked
}

var _ = widget.Fill

// viewWidth is the width the cells of a scroll view's content share: what
// the last layout gave it, less the room for the scroll bar.
func (s *Screen) viewWidth(e *screen.Env, v *base.ScrollView) float32 {
	rc := e.C().Rect(v.Inner)
	w := rc[2] - rc[0]
	if w <= 0 {
		w = e.Pt(widthPt)
	}
	return w - e.Pt(scrollRoom)
}
