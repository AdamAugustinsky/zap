package conversation

import (
	"strconv"
	"time"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/ui/screen"
)

// The in-chat search's day filter (zf:src/ui/pane.rs day_chip,
// calendar_popup, month_header, month_grid): a calendar under the search
// field's calendar button picks one local day, and the search lists that
// day's messages, of the query or all of them.

// cellPt is a day's cell in the calendar (pane.rs CELL).
const cellPt = 30

// day is a local calendar day; the zero day is none.
type day struct {
	y int
	m time.Month
	d int
}

func dayAt(t time.Time) day {
	y, m, d := t.Local().Date()
	return day{y, m, d}
}

func (d day) none() bool { return d.y == 0 }

// at is the day's noon, local: a time on the day whatever the clock does
// at midnight.
func (d day) at() time.Time { return time.Date(d.y, d.m, d.d, 12, 0, 0, 0, time.Local) }

func (d day) after(o day) bool {
	if d.y != o.y {
		return d.y > o.y
	}
	if d.m != o.m {
		return d.m > o.m
	}
	return d.d > o.d
}

// monthStep is the first of the month n months from d's.
func monthStep(d day, n int) day {
	t := time.Date(d.y, d.m+time.Month(n), 1, 12, 0, 0, 0, time.Local)
	return day{t.Year(), t.Month(), 1}
}

// dayNums are the day numbers the grid shows.
var dayNums = func() (n [32]string) {
	for i := range n {
		n[i] = strconv.Itoa(i)
	}
	return
}()

// setDay picks the day filter's day (none clears it) and closes the
// calendar; the pane asks for the day's matches (Action::SetChatSearchDay).
func (st *searchState) setDay(d day) {
	st.day, st.calendar = d, false
	if !d.none() {
		st.month = day{d.y, d.m, 1}
	}
}

// calendarButton opens and closes the calendar; it is in the accent while a
// day is picked or the calendar is open.
func (p *Search) calendarButton(e *screen.Env, st *searchState) {
	k := e.K.Kit
	name := i18n.T("Filter by date")
	var sig base.Signal
	if !st.day.none() || st.calendar {
		sig = accentIcon(k, p.calIc, name)
	} else {
		sig = rexui.IconButton(k, p.calIc, name, rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	}
	c := e.C()
	e.TabStop(screen.StopChatSearchDate, sig.ID)
	p.calAt = c.RectOf(c.KeyOf(sig.ID))
	if sig.Clicked {
		st.calendar = !st.calendar
		if st.calendar {
			st.month = st.day
			if st.month.none() {
				st.month = dayAt(e.S.Now())
			}
			st.month.d = 1
		}
		e.S.Touch()
	}
}

// accentIcon is a borderless icon button whose symbol is in the accent
// (rexui.IconButton takes no colour); its key is IconButton's, so the focus
// stays on it as it changes.
func accentIcon(k *base.Kit, ic icon.ID, name string) base.Signal {
	c, r := k.Context(), k.Theme()
	sz := &r.Sizes[theme.SizeRegular]
	p := k.PressState(name, base.HoverOn)
	iw, ih := k.IconSize(ic)
	k.BeginPress(&p, ui.RoleButton, ui.Px(max(theme.Snap(sz.Icon), iw)), ui.Px(max(theme.Snap(sz.Icon), ih)), 0, nil)
	if f, disp := k.Icons().Prepare(c, ic, r.Colors[theme.Accent]); f != 0 {
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
	}
	k.FocusRing(p.ID, p.Visual, sz.Radius)
	return k.EndPress(&p)
}

// dayChip is the picked day under the field: clicking it opens the
// calendar on it, and the cross beside it clears it.
func (p *Search) dayChip(e *screen.Env, st *searchState) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	loc := i18n.Current()
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(4))
	c.Push(c.Box(0, "day"))
	pr := k.PressState("day chip", base.HoverOn)
	fmtBuf = format.AppendLongDate(fmtBuf[:0], loc, st.day.at())
	pr.Name = c.Fmt().Str(i18n.T("Filter by date")).Str(": ").Str(model.S(fmtBuf)).String()
	h := e.Pt(28)
	c.NextPadding(e.Pt(9), 0, e.Pt(9), 0)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextBG(theme.WithAlpha(r.Colors[theme.Accent], 0.18))
	c.NextRadius(h / 2)
	k.BeginPress(&pr, ui.RoleButton, ui.Children(1), ui.Px(h), ui.DrawBackground, nil)
	fmtBuf = format.AppendShortDate(fmtBuf[:0], loc, st.day.at())
	rexui.LabelBytes(k, fmtBuf, rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Accent)})
	k.FocusRing(pr.ID, pr.Visual, h/2)
	if k.EndPress(&pr).Clicked {
		st.month = day{st.day.y, st.day.m, 1}
		st.calendar = true
		e.S.Touch()
	}
	clear := rexui.IconButton(k, p.closeIc, i18n.T("Clear the day filter"), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	e.TabStop(screen.StopChatSearchDay, clear.ID)
	if clear.Clicked {
		st.setDay(day{})
		e.S.Touch()
	}
	c.Pop()
}

// calendar is the day filter's month, floating under its button; it closes
// on a click elsewhere.
func (p *Search) calendar(e *screen.Env, st *searchState) {
	c, k := e.C(), e.K.Kit
	cell := e.Pt(cellPt)
	pop := rexui.BeginPopover(k, "day filter", p.calAt, rexui.PopoverOpts{
		Width: cellPt*7 + 2*rexui.PopoverLook.Pad, Edge: rexui.PopoverBelow, Behavior: rexui.PopoverTransient,
		Label: i18n.T("Filter by date"),
	})
	today := dayAt(e.S.Now())
	month := st.month
	loc := i18n.Current()

	// the month, and the way to the ones before and after (none to come)
	c.NextSize(ui.Px(7*cell), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(0, "month"))
	fmtBuf = format.AppendMonthHeading(fmtBuf[:0], loc, month.at())
	k.NextWidth(ui.Pct(1))
	rexui.LabelBytes(k, fmtBuf, rexui.LabelOpts{Style: rexui.TextHeadline})
	if rexui.IconButton(k, p.backIc, i18n.T("Previous month"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		st.month = monthStep(month, -1)
		e.S.Touch()
	}
	if month.y < today.y || (month.y == today.y && month.m < today.m) {
		if rexui.IconButton(k, p.nextIc, i18n.T("Next month"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
			st.month = monthStep(month, 1)
			e.S.Touch()
		}
	}
	c.Pop()

	// the weekdays, Monday first
	if !p.headsSet || p.headsLoc != loc {
		p.heads, p.headsLoc, p.headsSet = format.WeekdayHeadings(loc), loc, true
	}
	c.NextSize(ui.Px(7*cell), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextPadding(0, e.Pt(6), 0, e.Pt(4))
	c.Push(c.Box(0, "weekdays"))
	for _, h := range p.heads {
		k.NextWidth(ui.Px(cell))
		k.NextAlign(ui.AlignCenter)
		rexui.Label(k, h, rexui.LabelOpts{Style: rexui.TextCaption2, Color: rexui.Of(theme.Label3)})
	}
	c.Pop()

	// six weeks, the month's days in place
	first := time.Date(month.y, month.m, 1, 12, 0, 0, 0, time.Local)
	lead := (int(first.Weekday()) + 6) % 7
	days := time.Date(month.y, month.m+1, 0, 12, 0, 0, 0, time.Local).Day()
	for w := range 6 {
		c.NextSize(ui.Px(7*cell), ui.Px(cell))
		c.NextAxis(ui.AxisX)
		c.Push(c.Box(0, dayNums[w]))
		for i := range 7 {
			n := w*7 + i - lead + 1
			if n < 1 || n > days {
				c.NextSize(ui.Px(cell), ui.Px(cell))
				c.Box(0, dayNums[i])
				continue
			}
			p.dayCell(e, st, day{month.y, month.m, n}, today, cell)
		}
		c.Pop()
	}
	rexui.EndPopover(k, &pop)
	if pop.Dismissed {
		st.calendar = false
		e.S.Touch()
	}
}

// dayCell is one day: picked in the accent, today ringed, the days to come
// disabled. A click picks it, a second one clears it.
func (p *Search) dayCell(e *screen.Env, st *searchState, d day, today day, cell float32) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	future := d.after(today)
	picked := d == st.day
	fmtBuf = format.AppendLongDate(fmtBuf[:0], i18n.Current(), d.at())
	k.BeginDisabled(future)
	pr := k.PressState(c.Fmt().Str(model.S(fmtBuf)).String(), base.HoverOn)
	inset := e.Pt(1)
	c.NextPadding(inset, inset, inset, inset)
	if picked {
		c.NextSemState(ui.SemSelected)
	}
	k.BeginPress(&pr, ui.RoleButton, ui.Px(cell), ui.Px(cell), 0, nil)
	fill, edge := uint32(0), uint32(0)
	switch {
	case picked:
		fill = r.Colors[theme.Accent]
	case pr.Hot > 0 && !future:
		fill = theme.WithAlpha(r.Colors[theme.Fill3], pr.Hot)
	case d == today:
		edge = r.Colors[theme.Separator]
	}
	col := theme.Label
	switch {
	case picked:
		col = theme.OnAccent
	case future:
		col = theme.Label3
	}
	in := cell - 2*inset
	c.NextSize(ui.Px(in), ui.Px(in))
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.NextRadius(in / 2)
	flags := ui.Flags(0)
	if fill&0xFF != 0 {
		c.NextBG(fill)
		flags |= ui.DrawBackground
	}
	if edge&0xFF != 0 {
		c.NextBorder(edge, r.Lines.Hairline)
		flags |= ui.DrawBorder
	}
	c.Push(c.Box(flags, "disc"))
	rexui.Label(k, dayNums[d.d], rexui.LabelOpts{Color: rexui.Of(col)})
	c.Pop()
	k.FocusRing(pr.ID, pr.Visual, in/2)
	clicked := k.EndPress(&pr).Clicked
	k.EndDisabled()
	if clicked && !future {
		if picked {
			d = day{}
		}
		st.setDay(d)
		e.S.Touch()
	}
}
