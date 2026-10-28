package chatlist

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// chips builds the filter chips and, once there is a label, the label chips
// after them, ending in + for the manager (zf:src/ui/chats.rs:587,
// zf:src/ui/labels.rs:96). Each row scrolls sideways when its chips do not
// fit: with the touchpad, the wheel or the keyboard (Tab to a chip brings it
// into view). A floating scrollbar shows on hover; overflowing rows fade at
// the right edge (the filters skip that fade in Messages mode).
func (s *Screen) chips(e *screen.Env, a *state.Account) {
	// The archive and the locked folder have no chips: their counts are of
	// the chats outside the folder, and an empty folder showed "Unread 1".
	if a.Mode != state.ListChats {
		return
	}
	mir := s.mirror(a)
	s.chipRow(e, "chip-filters", &s.chipScroll, func() { s.filterChips(e, a, true) })
	if len(mir.labels) > 0 {
		s.chipRow(e, "label-chips", &s.labelScroll, func() { s.labelChips(e, a, true) })
	}
}

// chipRow is a row of chips that scrolls sideways.
func (s *Screen) chipRow(e *screen.Env, name string, st *base.ScrollState, body func()) {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	h := theme.Snap(r.Sizes[theme.SizeSmall].Height + 2*sc)
	// Upstream always uses a floating bar here, even with system "Always"
	// scrollers.
	st.HideBars, st.Overlay = false, true
	k.NextSize(ui.Pct(1), ui.Px(h))
	v := k.BeginScroll(name, st, base.ScrollHorizontal)
	s.rowInner = v.Inner
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(theme.Snap(4 * sc))
	c.NextSize(ui.Children(1), ui.Px(h))
	s.rowBox = c.BoxKeyed(0, ui.HashString(c.ParentKey(), "row"), nil)
	c.Push(s.rowBox)
	body()
	c.Pop()
	k.CloseScrollContent(&v)
	s.chipScrollChrome(e, &v)
	k.CloseScroll(&v)
}

// chipScrollChrome adapts Rex's scrollbar to upstream's hover-only chip row.
func (s *Screen) chipScrollChrome(e *screen.Env, v *base.ScrollView) {
	k, c := e.K, e.C()
	in, bounds := c.Input(), c.Rect(v.Outer)
	content, view := c.ScrollExtent(v.Inner)
	inside := func(px, py float32) bool {
		return px >= bounds[0] && px < bounds[2] && py >= bounds[1] && py < bounds[3]
	}
	available := !c.ModalOpen() && !k.MenuOpen() && !k.Disabled() &&
		!e.S.Dialog.Open() && !e.S.AccountMenu && e.S.Panes.Picker == state.PickerNone
	if x, _ := c.Scroll(v.Inner); float64(content[0]-view[0])-x > 0.5 {
		// upstream's 16-point fade at the end, over the pane's glass
		c.SetFadeEnd(v.Inner, min(e.Pt(16), view[0]))
	}
	for _, bar := range k.ScrollBars(v) {
		if !bar.On || !available || !(in.Inside && inside(in.MouseX, in.MouseY) || c.ActiveKey() == bar.Key) {
			continue
		}
		bar.Show = 1
		// The generic Rex track is taller than upstream's 8-point floating
		// track; keep it below the chip centres so hover never steals clicks.
		bar.Y, bar.H = max(view[1]-e.Pt(8), 0), e.Pt(8)
		k.BeginScrollBar(&bar)
		thickness := e.Pt(3)
		theme.At(c, bar.ThumbOff, bar.H-thickness-e.Pt(1), bar.ThumbLen, thickness)
		c.NextBG(rexui.ListLook.OverlayKnob.RGBA(k.Theme()))
		c.NextRadius(thickness / 2)
		c.NextAuditExempt(ui.AuditCovered.Mask())
		c.Box(ui.DrawBackground|base.Floating, "thumb")
		c.Pop()
	}
}

// revealFocused scrolls the row so that a chip the keyboard has just reached
// shows, including when the scrollbar is hidden at rest.
func (s *Screen) revealFocused(e *screen.Env, sig base.Signal) {
	if !sig.Focused {
		if s.revealed == sig.ID {
			s.revealed = 0
		}
		return
	}
	if s.revealed == sig.ID {
		return
	}
	s.revealed = sig.ID
	c := e.C()
	in, r := c.Rect(s.rowBox), c.Rect(sig.ID) // the row's box scrolls with its content: its corner is the content's
	c.ScrollIntoView(s.rowInner, [4]float32{r[0] - in[0], r[1] - in[1], r[2] - in[0], r[3] - in[1]})
}

// chevPt is the More chip's chevron, in points.
const chevPt = 9

// filterNames are the filters' chip names, by state.Filter.
func filterNames() [state.FilterChannels + 1]string {
	return [...]string{i18n.T("All"), i18n.T("Unread"), i18n.T("Private"), i18n.T("Favorites"), i18n.T("Groups"), i18n.T("Channels")}
}

// filterChips are All, Unread, Private, Favorites, Groups and Channels. The
// ones that do not fit the row fold into a More chip at its end, whose menu
// lists them; it shows the chosen filter's name when that one is folded.
func (s *Screen) filterChips(e *screen.Env, a *state.Account, chosen bool) {
	names := filterNames()
	count := func(f state.Filter) int {
		if f == state.FilterAll {
			return 0
		}
		return s.counts[f]
	}
	s.fold = s.foldAt(e, a, chosen, &names, count)
	for f := state.FilterAll; f < s.fold; f++ {
		on := chosen && a.Filter == f
		sig := s.chip(e, names[f], count(f), on, false, 0, false)
		e.TabStop(filterStops[f], sig.ID)
		s.revealFocused(e, sig)
		if f == state.FilterChannels {
			s.channelsMenu(e, a, sig)
		}
		if sig.Clicked {
			next := f
			if on {
				next = state.FilterAll // a second click returns to every chat
			}
			s.setFilter(e, a, next)
		}
	}
	if s.fold <= state.FilterChannels {
		s.moreChip(e, a, chosen, &names)
	}
}

// foldAt is the first filter that does not fit the row's width (from the
// last layout) beside the More chip; past FilterChannels when all fit. All
// always shows.
func (s *Screen) foldAt(e *screen.Env, a *state.Account, chosen bool, names *[state.FilterChannels + 1]string, count func(state.Filter) int) state.Filter {
	_, view := e.C().ScrollExtent(s.rowInner)
	avail := view[0]
	gap := theme.Snap(4 * e.K.Scale())
	var w [state.FilterChannels + 1]float32
	total := -gap
	for f := range w {
		w[f] = s.chipWidth(e, names[f], count(state.Filter(f)), false)
		total += w[f] + gap
	}
	if avail <= 0 || total <= avail+0.5 {
		return state.FilterChannels + 1
	}
	used := w[0]
	n := state.Filter(1)
	for ; n <= state.FilterChannels; n++ {
		more := s.chipWidth(e, s.moreName(a, chosen, n, names), 0, true)
		if used+gap+w[n]+gap+more > avail+0.5 {
			break
		}
		used += gap + w[n]
	}
	return n
}

// moreName is the More chip's text when the filters from fold on are folded
// into it: the chosen filter's name if it is one of them.
func (s *Screen) moreName(a *state.Account, chosen bool, fold state.Filter, names *[state.FilterChannels + 1]string) string {
	if chosen && a.Filter >= fold && a.Filter <= state.FilterChannels {
		return names[a.Filter]
	}
	return i18n.T("More")
}

// moreChip is the chip the folded filters open from: their menu, native
// where the app has native menus, else drawn under the chip.
func (s *Screen) moreChip(e *screen.Env, a *state.Account, chosen bool, names *[state.FilterChannels + 1]string) {
	c := e.C()
	on := chosen && a.Filter >= s.fold && a.Filter <= state.FilterChannels
	sig := s.chip(e, s.moreName(a, chosen, s.fold, names)+"###more filters", 0, on, false, 0, true)
	e.TabStop(filterStops[s.fold], sig.ID)
	s.revealFocused(e, sig)
	if id, out := c.ContextMenuResult(sig.ID); out == ui.MenuChosen {
		s.menuAct(e, a, menuFilters, 0, 0, id)
	}
	if !sig.Clicked {
		return
	}
	r := c.Rect(sig.ID)
	var items []ui.MenuItem
	s.menuItems(&items, e, a, menuFilters, 0, 0)
	if !c.ContextMenu(sig.ID, r[0], r[3], items) {
		s.menu = ctxMenu{kind: menuFilters, at: [2]float32{r[0], r[3]}, items: s.menu.items[:0]}
	}
}

// chipWidth is the width in pixels chip gives a chip with name (its text up
// to "###") and count, with the More chip's chevron when chev.
func (s *Screen) chipWidth(e *screen.Env, name string, count int, chev bool) float32 {
	k := e.K
	r, sc := k.Theme(), k.Scale()
	shown, _ := ui.SplitLabel(name)
	gap := theme.Snap(5 * sc)
	w := 2*theme.Snap(9*sc) + k.TextWidth(r.Sizes[theme.SizeSmall].Font.Font, shown) + base.LabelSlack
	if count > 0 {
		f := e.C().Fmt()
		if count > 99 {
			f.Int(99).Byte('+')
		} else {
			f.Int(count)
		}
		bh := theme.Snap(rexui.BadgeLook.H[rexui.BadgeCount] * sc)
		tw := k.TextWidthBytes(r.Styles[theme.StyleCaption2].Font, f.Bytes())
		w += gap + max(bh, theme.Snap(tw+2*theme.Snap(rexui.BadgeLook.PadX[rexui.BadgeCount]*sc)+base.LabelSlack))
	}
	if chev {
		w += gap + theme.Snap(chevPt*sc)
	}
	return theme.Snap(w)
}

// labelChips are the labels and the + that opens the manager.
func (s *Screen) labelChips(e *screen.Env, a *state.Account, chosen bool) {
	k := e.K
	mir := s.mirror(a)
	for i := range mir.labels {
		lb := &mir.labels[i]
		on := chosen && a.Filter == state.FilterLabel && a.Label.Len() > 0 && string(a.Label.At(0)) == lb.id
		cnt := 0
		if i < len(s.labelCounts) {
			cnt = s.labelCounts[i]
		}
		sig := s.chip(e, lb.tag, cnt, on, true, colorOf(k.Theme(), lb.color), false)
		e.TabStop(screen.StopLabel+screen.TabStop(min(i, 98)), sig.ID)
		s.revealFocused(e, sig)
		if sig.Clicked {
			if on {
				s.setFilter(e, a, state.FilterAll)
			} else {
				s.selectLabel(e, a, lb.id)
			}
		}
	}
	manage := rexui.IconButton(k.Kit, s.ic.plus, i18n.T("Manage labels"), rexui.IconButtonOpts{Variant: rexui.IconBorderless, Size: theme.SizeSmall})
	e.TabStop(screen.StopManageLabels, manage.ID)
	s.revealFocused(e, manage)
	if manage.Clicked {
		e.S.OpenDialog(state.Dialog{Kind: state.DialogLabels})
	}
}

// filterStops are the filter chips' places in the Tab order.
var filterStops = [...]screen.TabStop{state.FilterAll: screen.StopAll, state.FilterUnread: screen.StopUnread,
	state.FilterPrivate: screen.StopPrivate, state.FilterFavorites: screen.StopFavorites,
	state.FilterGroups: screen.StopGroups, state.FilterChannels: screen.StopChannels}

// setFilter picks a chip; it lets go of the label.
func (s *Screen) setFilter(e *screen.Env, a *state.Account, f state.Filter) {
	a.SetMode(state.ListChats)
	a.Filter = f
	a.Label.Reset()
	s.kept = s.kept[:0]
	e.S.Touch()
}

// selectLabel picks a label; it lets go of the chip.
func (s *Screen) selectLabel(e *screen.Env, a *state.Account, id string) {
	a.SetMode(state.ListChats)
	a.Filter = state.FilterLabel
	a.Label.Reset()
	a.Label.Add([]byte(id))
	s.kept = s.kept[:0]
	e.S.Touch()
}

// chip builds a filter chip: a capsule that reads on or off, with the number
// of unread chats it holds and, for a label, its colour; chev ends it in a
// chevron (the More chip). chipWidth must follow its metrics.
func (s *Screen) chip(e *screen.Env, name string, count int, on, dotted bool, dot uint32, chev bool) base.Signal {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	st := &r.Sizes[theme.SizeSmall]
	tg := k.ToggleBool(name, &on, base.HoverOff)
	fill, fg, _ := rexui.ButtonRecipes[rexui.ButtonDefault].Colors(r, tg.Visual)
	if on {
		// Rex's quiet tint, the open chat's row's, in place of the accent
		fill, fg = rexui.ChatRowLook.Selected.RGBA(r), r.Colors[theme.Label]
	}
	h := theme.Snap(st.Height + 2*sc)
	var flags ui.Flags
	if fill&0xFF != 0 {
		flags |= ui.DrawBackground
	}
	c.NextBG(fill)
	c.NextRadius(h / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(theme.Snap(5 * sc))
	c.NextPadding(theme.Snap(9*sc), 0, theme.Snap(9*sc), 0)
	k.BeginToggle(&tg, ui.RoleCheckbox, ui.Children(1), ui.Px(h), flags)
	k.FocusRing(tg.ID, tg.Visual, h/2)
	if dotted {
		d := theme.Snap(8 * sc)
		c.NextSize(ui.Px(d), ui.Px(d))
		c.NextBG(dot)
		c.NextRadius(d / 2)
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(tg.Key, 40), nil)
	}
	c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(st.FontLine))
	c.NextFont(st.Font.Font)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText, ui.KeyN(tg.Key, 41), base.StrBytes(tg.Name))
	if count > 0 {
		rexui.BadgeCountN(k.Kit, count, rexui.BadgeOpts{Color: rexui.Of(theme.Accent)})
	}
	if chev {
		k.Icon(s.ic.more, fg)
	}
	return k.EndToggle(&tg)
}

// colorOf reads "#rrggbb" as a colour, the accent when it is not one.
func colorOf(r *theme.Resolved, hex string) uint32 {
	if len(hex) == 7 && hex[0] == '#' {
		var v uint32
		for i := 1; i < 7; i++ {
			d := hex[i]
			switch {
			case d >= '0' && d <= '9':
				d -= '0'
			case d >= 'a' && d <= 'f':
				d = d - 'a' + 10
			case d >= 'A' && d <= 'F':
				d = d - 'A' + 10
			default:
				return r.Colors[theme.Accent]
			}
			v = v<<4 | uint32(d)
		}
		return v<<8 | 0xFF
	}
	return r.Colors[theme.Accent]
}

// channelsMenu offers "Mute all channels" on the Channels chip's right
// click (zf:src/ui/chats.rs:627).
func (s *Screen) channelsMenu(e *screen.Env, a *state.Account, sig base.Signal) {
	if !s.native(e, sig.ID, menuChannels, 0, 0) && sig.RightClicked {
		in := e.C().Input()
		s.menu.showChannels(in.MouseX, in.MouseY)
	}
}

// muteAllChannels mutes or unmutes every channel (Action::MuteAllChannels).
func (s *Screen) muteAllChannels(e *screen.Env, a *state.Account, mute bool) {
	m := a.Model
	for _, ref := range m.Chats.Order() {
		ch := m.Chats.At(ref)
		if !isChannel(ch) {
			continue
		}
		cmd := a.Cmd(abi.CommandTagSetMuted)
		cmd.SetMuted.Chat = ch.ID()
		if mute {
			cmd.SetMuted.Until, cmd.SetMuted.HasUntil = 0, true
		}
		a.Send(cmd)
	}
}

// allChannelsMuted reports whether every channel is muted now.
func (s *Screen) allChannelsMuted(e *screen.Env, a *state.Account) bool {
	m := a.Model
	now := e.S.Now().Unix()
	for _, ref := range m.Chats.Order() {
		if ch := m.Chats.At(ref); isChannel(ch) && !ch.Muted(now) {
			return false
		}
	}
	return true
}

var _ model.ChatRef
