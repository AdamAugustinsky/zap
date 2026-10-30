package shell

import (
	"math"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Spaces (state/folders.go), as Arc's: each account, and each folder shown
// alone, one after the other. ⌃1–9 go to one; a two-finger swipe over the
// list steps to the next or the one before; the strip at the list's foot
// shows them all, the one on screen lit. The list comes in from the side of
// the space it came from.

// spaceStripPt is the strip's height, and spaceCellPt a space's button.
const (
	spaceStripPt = 40
	spaceCellPt  = 28
)

// swipeStepPt is how far two fingers travel across the list to change space.
const swipeStepPt = 72

type spaceUI struct {
	dir     float32 // the side the last move came from: 1 the next, -1 the one before
	swipe   float32 // the swipe going on's travel across, pixels
	swiping bool
	icons   map[string]icon.ID // folders' symbols, made as they are met
	// the colour popover's space and where it opens from (tintPopover)
	tinting  state.Space
	tintAt   [4]float32
	tintOpen bool
	tintDown bool // opens below tintAt (the menu's, under a pane's title row), not above (the strip's)
	grab     int  // the theme creator's dot being dragged, plus one; 0 none
	// the locked dots' places from the space's own as the drag began: each
	// one's turn (degrees) and distance over the first's (heldPlaces)
	held [3][2]float64
}

// goToSpace puts sp on screen, the list coming in from its side.
func (sh *Shell) goToSpace(sp state.Space) {
	s := sh.S
	all := s.Spaces(sh.spaces[:0])
	sh.spaces = all
	_, from := s.SpaceAt(all)
	to := -1
	for i, x := range all {
		if x == sp {
			to = i
		}
	}
	sh.sp.dir = 1
	if to < from {
		sh.sp.dir = -1
	}
	s.GoToSpace(sp)
}

// stepSpace goes to the space after (step 1) or before (-1) the one on
// screen, round the end.
func (sh *Shell) stepSpace(step int) {
	s := sh.S
	all := s.Spaces(sh.spaces[:0])
	sh.spaces = all
	if len(all) < 2 {
		return
	}
	_, at := s.SpaceAt(all)
	next := (at + step + len(all)) % len(all)
	sh.goToSpace(all[next])
	sh.sp.dir = float32(step)
}

// swipeSpaces follows a two-finger swipe across the list pane r: one that
// travels swipeStepPt more across than down changes space when it ends.
func (sh *Shell) swipeSpaces(r [4]float32) {
	e := &sh.env
	c := e.C()
	in := c.Input()
	sp := &sh.sp
	for i := 0; i < in.NScrolls; i++ {
		ev := in.Scrolls[i]
		if !ev.Precise || ev.Momentum != ui.PhaseNone {
			continue // a wheel, or the glide after the fingers lift
		}
		inside := ev.X >= r[0] && ev.X < r[2] && ev.Y >= r[1] && ev.Y < r[3]
		switch ev.Phase {
		case ui.PhaseBegan:
			sp.swiping, sp.swipe = inside, 0
		case ui.PhaseChanged:
			if sp.swiping {
				if abs32(ev.DY) > abs32(ev.DX) && abs32(sp.swipe) < e.Pt(8) {
					sp.swiping = false // a scroll down the list
					continue
				}
				sp.swipe += ev.DX
			}
		case ui.PhaseEnded, ui.PhaseCancelled:
			if sp.swiping && ev.Phase == ui.PhaseEnded && abs32(sp.swipe) >= e.Pt(swipeStepPt) {
				step := 1
				if sp.swipe > 0 != ev.Inverted {
					step = -1 // the content follows the fingers: right is the one before
				}
				sh.stepSpace(step)
			}
			sp.swiping, sp.swipe = false, 0
		}
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// spaceShift is how far, and how faded in, the list is as it comes in from
// the space it left: (pixels across, opacity).
func (sh *Shell) spaceShift() (float32, float32) {
	if !sh.Slides || sh.sp.dir == 0 {
		return 0, 1 // nothing moved yet: the window opens on its space still
	}
	e := &sh.env
	c, r := e.C(), e.K.Theme()
	key := ui.HashString(ui.KeyN(0, uint64(sh.S.SpaceGen)), "space slide")
	t := c.AnimateValueFrom(key, 0, 1, ztheme.Enter(r, ztheme.T.DurStandard))
	return (1 - t) * sh.sp.dir * e.Pt(ztheme.Space6), 0.4 + 0.6*t
}

// spaceStrip builds the spaces' strip in the list pane's foot r (window
// pixels): a button for each, the one on screen lit; nothing with one.
func (sh *Shell) spaceStrip(r screen.Rect) {
	s, e := sh.S, &sh.env
	all := s.Spaces(sh.spaces[:0])
	sh.spaces = all
	if len(all) < 2 {
		return
	}
	k := e.K
	c, th := k.Context(), k.Theme()
	_, at := s.SpaceAt(all)
	cell := e.Pt(spaceCellPt)
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(e.Pt(ztheme.Space1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Spaces"))
	c.Push(c.Box(base.Floating, "spaces"))
	for i, sp := range all {
		a := s.Accounts[sp.Acct]
		name, sym := sh.spaceName(a, sp)
		label := c.Fmt().Str(name).Str("###space ").Int(i).String()
		p := k.PressState(label, base.HoverOn)
		p.Name = name
		lit := i == at
		fill := uint32(0)
		tint := ztheme.SpacePrimary(a.ThemeOf(sp))
		switch {
		case lit && tint != 0:
			fill = theme.WithAlpha(tint, 0.45) // the space's colour
		case lit:
			fill = rexui.ChatRowLook.Selected.RGBA(th)
		case tint != 0:
			fill = theme.WithAlpha(tint, 0.18+0.12*p.Hot)
		case p.Hot > 0:
			fill = theme.WithAlpha(th.Colors[theme.Label], ztheme.T.RowHover*p.Hot)
		}
		c.NextBG(fill)
		c.NextRadius(cell / 2)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		var st ui.SemState
		if lit {
			st = ui.SemSelected
		}
		c.NextSemState(st)
		k.BeginPress(&p, ui.RoleButton, ui.Px(cell), ui.Px(cell), ui.DrawBackground, nil)
		if sym == "" {
			px := e.Pt(20)
			rexui.Avatar(k.Kit, name, rexui.AvatarOpts{Size: 20, Image: screen.Avatar(sh.imgs, a, a.Model.Me.ID(), false, int32(px)), Backdrop: rexui.Of(theme.Pane)})
		} else {
			col := th.Colors[theme.Label2]
			if lit {
				col = th.Colors[theme.Label]
			}
			k.Icon(sh.folderIcon(sym), col)
		}
		sig := k.EndPress(&p)
		tip := name
		if i < 9 {
			tip = c.Fmt().Str(name).Str("  ⌃").Int(i + 1).String()
		}
		rexui.Tooltip(k.Kit, sig.ID, tip)
		if sig.Clicked && !lit {
			sh.goToSpace(sp)
		}
		if sig.RightClicked {
			// its colour: a row of swatches over the strip
			sh.sp.tinting, sh.sp.tintAt = sp, c.RectOf(c.KeyOf(sig.ID))
			sh.sp.tintOpen, sh.sp.tintDown = true, false
		}
	}
	c.Pop()
}

// openTint opens the theme creator on space sp from the View menu (Space
// colour…): under the list's title row, or the conversation's when the list
// is hidden, so that a window with one space, which has no strip to
// right-click, can colour it too.
func (sh *Shell) openTint(sp state.Space) {
	l := &sh.panes
	r := l.Rect(paneList)
	if !l.Shown(paneList) {
		r = l.Rect(paneChat)
	}
	sh.sp.tinting, sh.sp.tintAt = sp, [4]float32{r[0], r[1], r[2], r[1] + sh.headerH()}
	sh.sp.tintOpen, sh.sp.tintDown = true, true
	sh.S.Touch()
}

// tintPopover is the right-clicked space's theme creator, Zen's made small:
// a field where the space's colours are dots (round its centre the hue, out
// from it the saturation) to drag, add (up to three) and take away, a
// harmony that holds them together or leaves each free, the named colours
// to start from, and how opaque the theme lies over the window. It stays
// open while the person tries things; the window shows each at once.
func (sh *Shell) tintPopover() {
	sp := &sh.sp
	if !sp.tintOpen {
		sp.grab = 0
		return
	}
	s, e := sh.S, &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	if sp.tinting.Acct < 0 || sp.tinting.Acct >= len(s.Accounts) {
		sp.tintOpen = false
		return
	}
	a := s.Accounts[sp.tinting.Acct]
	cur := a.ThemeOf(sp.tinting)
	set := func(t state.SpaceTheme) {
		a.SetTheme(sp.tinting, t)
		s.Touch()
	}
	sw := e.Pt(20)
	pad, gap := e.Pt(ztheme.Space2), e.Pt(ztheme.Space1)
	fw := 10*sw + 9*gap // the swatches' row
	c.NextOverlay(ui.OverlayOpts{})
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextRole(ui.RoleMenu)
	c.NextSemName(i18n.T("Space colour"))
	place := ui.PlaceAbove
	if sp.tintDown {
		place = ui.PlaceBelow
	}
	ov := c.BeginOverlay(ui.HashString(0, "zapfast/space tint"), ui.OverlayPopup, sp.tintAt, place)
	if ov.Dismissed && sp.grab == 0 {
		sp.tintOpen = false
	}
	// the panel (the overlay's own box draws nothing)
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextGap(e.Pt(ztheme.Space2))
	c.NextPadding(pad, pad, pad, pad)
	c.NextBG(th.Colors[theme.Raised] | 0xFF)
	c.NextBorder(th.Colors[theme.Separator], th.Lines.Hairline)
	c.NextRadius(e.Pt(ztheme.Pane()))
	c.NextShadow(ui.Shadow{OffY: e.Pt(4), Sigma: e.Pt(12), Color: 0x00000040})
	c.Push(c.Box(ui.DrawBackground|ui.DrawBorder, "panel"))

	if mode, ok := sh.modeRow(cur.Mode, fw); ok {
		t := cur
		t.Mode = mode
		set(t)
	}
	if t, ok := sh.tintField(cur, fw, fw*0.9); ok {
		set(t)
	}

	// the named colours to start from, and none
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(gap)
	c.NextSize(ui.Children(1), ui.Children(1))
	c.Push(c.Box(0, "tints"))
	swatch := func(name string, label string) {
		ps := k.PressState(c.Fmt().Str("tint ").Str(name).String(), base.HoverOn)
		ps.Name = label
		col := ztheme.TintColor(name)
		if col == 0 {
			col = th.Colors[theme.Field] | 0xFF
		}
		on := name == cur.Tint && !cur.Custom()
		c.NextBG(col)
		c.NextRadius(sw / 2)
		if on {
			c.NextBorder(th.Colors[theme.Label], th.Lines.Hairline*2)
		} else if ps.Hot > 0 {
			c.NextBorder(theme.WithAlpha(th.Colors[theme.Label], 0.5), th.Lines.Hairline)
		}
		var st ui.SemState
		if on {
			st = ui.SemSelected
		}
		c.NextSemState(st)
		k.BeginPress(&ps, ui.RoleButton, ui.Px(sw), ui.Px(sw), ui.DrawBackground|ui.DrawBorder, nil)
		if k.EndPress(&ps).Clicked {
			if name == "" {
				set(state.SpaceTheme{Level: cur.Level, Mode: cur.Mode})
				sp.tintOpen = false // nothing more to choose
				return
			}
			a.SetTint(sp.tinting, name)
			s.Touch()
		}
	}
	swatch("", i18n.T("No colour"))
	for _, name := range state.SpaceTints {
		swatch(name, screen.TintLabel(name))
	}
	c.Pop()

	if !cur.None() {
		// how opaque it lies over the window
		c.NextAxis(ui.AxisX)
		c.NextSize(ui.Px(fw), ui.Children(1))
		c.Push(c.Box(0, "opacity head"))
		k.NextWidth(ui.Grow(1))
		rexui.Label(k.Kit, i18n.T("Opacity"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
		rexui.Label(k.Kit, c.Fmt().Int(int(cur.Strength()*100+0.5)).Str("%").String(), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
		c.Pop()
		level := cur.Strength()
		k.NextWidth(ui.Px(fw))
		rexui.Slider(k.Kit, i18n.T("Opacity")+"###tint opacity", &level, 0.1, 1, 0.05, rexui.SliderOpts{Size: theme.SizeSmall})
		if level != cur.Strength() {
			t := cur
			t.Level = level
			set(t)
		}
		// the grain, as Zen's texture dial: 16 steps
		c.NextAxis(ui.AxisX)
		c.NextSize(ui.Px(fw), ui.Children(1))
		c.Push(c.Box(0, "grain head"))
		k.NextWidth(ui.Grow(1))
		rexui.Label(k.Kit, i18n.T("Grain"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
		c.Pop()
		grain := cur.Grain
		k.NextWidth(ui.Px(fw))
		rexui.Slider(k.Kit, i18n.T("Grain")+"###tint grain", &grain, 0, 1, 1.0/16, rexui.SliderOpts{Size: theme.SizeSmall})
		if grain != cur.Grain {
			t := cur
			t.Grain = grain
			set(t)
		}
	}
	c.Pop()
	c.EndOverlay()
}

// modeRow is the theme creator's appearance, as Zen's: the app's, light or
// dark while the space shows (state.SpaceTheme.Mode), w pixels wide. It
// returns the mode chosen, and whether one was.
func (sh *Shell) modeRow(cur string, w float32) (string, bool) {
	e := &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	gap := e.Pt(ztheme.Space1)
	h := e.Pt(ztheme.T.HitTool)
	c.NextAxis(ui.AxisX)
	c.NextGap(gap)
	c.NextSize(ui.Px(w), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Appearance"))
	c.Push(c.Box(0, "tint modes"))
	chosen, picked := cur, false
	for _, mode := range state.SpaceModes {
		sym, label := "circle.lefthalf.filled", i18n.T("Like the app")
		switch mode {
		case "light":
			sym, label = "sun.max", i18n.T("Light")
		case "dark":
			sym, label = "moon", i18n.T("Dark")
		}
		ps := k.PressState(c.Fmt().Str("tint mode ").Str(mode).String(), base.HoverOn)
		ps.Name = label
		on := mode == cur
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextRadius(h / 2)
		fill := th.Colors[theme.Field] | 0xFF
		if ps.Hot > 0 && !on {
			fill = theme.MixRGBA(fill, th.Colors[theme.Label], 0.06*ps.Hot)
		}
		c.NextBG(fill)
		if on {
			c.NextBorder(th.Colors[theme.Label], th.Lines.Hairline*2)
			c.NextSemState(ui.SemSelected)
		}
		k.BeginPress(&ps, ui.RoleButton, ui.Grow(1), ui.Px(h), ui.DrawBackground|ui.DrawBorder, nil)
		col := th.Colors[theme.Label2]
		if on {
			col = th.Colors[theme.Label]
		}
		k.Icon(sh.folderIcon(sym), col)
		if k.EndPress(&ps).Clicked && !on {
			chosen, picked = mode, true
		}
	}
	c.Pop()
	return chosen, picked
}

// tintField is the theme creator's field, w×h pixels: a preview of the
// theme, a ring of hues round its edge, the theme's colours as dots, and
// the buttons that add a dot, take one away and change the harmony. It
// returns the theme changed, and whether it changed.
func (sh *Shell) tintField(cur state.SpaceTheme, w, h float32) (state.SpaceTheme, bool) {
	sp, e := &sh.sp, &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	var cols [3]uint32
	n := ztheme.SpaceColors(cur, &cols)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextBG(th.Colors[theme.Field] | 0xFF)
	c.NextRadius(e.Pt(ztheme.Space3))
	c.NextBorder(th.Colors[theme.Separator], th.Lines.Hairline)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Colours"))
	id := c.Box(ui.Clickable|ui.DrawBackground|ui.DrawBorder|ui.Clip, "tint field")
	sig := c.Signal(id)
	c.Push(id)
	// the theme as it will lie, stronger, under everything in the field
	pv := tintCorners(cols, n, 0.55)
	c.NextAnchor(ui.AnchorTopLeft, 0, 0)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG4(pv[0], pv[1], pv[2], pv[3])
	c.Box(ui.DrawBackground, "preview")

	// the wheel above the buttons' row
	tools := e.Pt(ztheme.T.HitTool) + e.Pt(ztheme.Space1)
	cx, cy := w/2, (h-tools)/2
	R := min(w/2, (h-tools)/2) - e.Pt(ztheme.Space3)
	at := func(angle, out float64) (float32, float32) {
		sn, cs := math.Sincos(angle * math.Pi / 180)
		return cx + float32(sn*out)*R, cy - float32(cs*out)*R
	}
	// the hues round the edge: where each colour lies
	tick := e.Pt(4)
	for i := 0; i < 24; i++ {
		ang := float64(i) * 15
		x, y := at(ang, 1.08)
		c.NextAnchor(ui.AnchorTopLeft, x-tick/2, y-tick/2)
		c.NextSize(ui.Px(tick), ui.Px(tick))
		c.NextBG(theme.WithAlpha(ztheme.FieldColor(ang, 1), 0.8))
		c.NextRadius(tick / 2)
		c.Box(ui.DrawBackground, c.Fmt().Str("hue ").Int(i).String())
	}
	// the dots, the space's own the largest; the others above it
	var px, py [3]float32
	for i := 0; i < n; i++ {
		ang, out := ztheme.FieldPlace(cols[i])
		px[i], py[i] = at(ang, out)
	}
	for i := n - 1; i >= 0; i-- {
		d, ring := e.Pt(14), th.Lines.Hairline*2
		if i == 0 {
			d, ring = e.Pt(22), th.Lines.Hairline*3
		}
		if sp.grab == i+1 {
			d *= 1.12
		}
		c.NextAnchor(ui.AnchorTopLeft, px[i]-d/2, py[i]-d/2)
		c.NextSize(ui.Px(d), ui.Px(d))
		c.NextBG(cols[i])
		c.NextRadius(d / 2)
		c.NextBorder(0xFFFFFFFF, ring)
		c.NextShadow(ui.Shadow{OffY: e.Pt(1), Sigma: e.Pt(3), Color: 0x00000040})
		c.Box(ui.DrawBackground|ui.DrawBorder, c.Fmt().Str("dot ").Int(i).String())
	}

	t, changed := cur, false
	// a press takes the dot under it, or the space's own to put it there;
	// locked, it is always the space's own, the others turning with it
	r := c.RectOf(c.KeyOf(id))
	in := c.Input()
	mx, my := in.MouseX-r[0], in.MouseY-r[1]
	if sig.Pressed {
		sp.grab = 1
		if cur.Lock {
			sp.held = heldPlaces(cols, n)
		} else {
			best := e.Pt(16)
			for i := 0; i < n; i++ {
				if d := float32(math.Hypot(float64(mx-px[i]), float64(my-py[i]))); d < best {
					best, sp.grab = d, i+1
				}
			}
		}
	}
	if (sig.Pressed || sig.Dragging) && sp.grab > 0 {
		dx, dy := float64(mx-cx), float64(my-cy)
		ang := math.Atan2(dx, -dy) * 180 / math.Pi
		out := math.Hypot(dx, dy) / float64(R)
		t, changed = moveDot(cur, cols, n, sp.grab-1, sp.held, ang, out), true
	}
	if !sig.Dragging && !sig.Pressed {
		sp.grab = 0
	}

	// add, take away, lock, harmony
	c.NextAnchor(ui.AnchorBottom, 0, e.Pt(ztheme.Space1))
	c.NextAxis(ui.AxisX)
	c.NextGap(e.Pt(ztheme.Space4))
	c.NextSize(ui.Children(1), ui.Children(1))
	c.Push(c.Box(0, "tint tools"))
	hit := ui.Px(e.Pt(ztheme.T.HitTool))
	tool := func(sym string, label string, off bool) bool {
		k.NextSize(hit, hit)
		if off {
			c.NextOpacity(0.35)
		}
		return rexui.IconButton(k.Kit, sh.folderIcon(sym), label, rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked && !off
	}
	if tool("plus", i18n.T("Add a colour"), n >= 3) {
		t, changed = addDot(cur, cols, n), true
	}
	if tool("minus", i18n.T("Remove a colour"), n <= 1) {
		t, changed = dropDot(cur, cols, n), true
	}
	lock, lockSym := i18n.T("Move the colours together"), "lock.open"
	if cur.Lock {
		lock, lockSym = i18n.T("Move each colour on its own"), "lock"
	}
	if tool(lockSym, lock, n <= 1) {
		t, changed = cur, true
		t.Lock = !cur.Lock
	}
	harm := i18n.Format(i18n.T("Harmony: {name}"), "{name}", mixLabel(cur.Mix, n, cur.Lock))
	if tool("point.3.connected.trianglepath.dotted", harm, n <= 1) {
		t, changed = nextMix(cur, cols, n), true
	}
	c.Pop()
	c.Pop()
	return t, changed
}

// custom is theme t with its n colours cols as its own (Dots), so that they
// can move.
func custom(t state.SpaceTheme, cols [3]uint32, n int) state.SpaceTheme {
	t.Tint, t.Dots = "", [3]uint32{}
	copy(t.Dots[:n], cols[:n])
	return t
}

// moveDot is theme t with dot i moved to angle and distance out. Unlocked,
// it moves alone and the theme has no harmony any more (its colours are
// where the person put them); locked, it is the space's own, and the others
// take their places from it as held (heldPlaces) when the drag began.
func moveDot(t state.SpaceTheme, cols [3]uint32, n, i int, held [3][2]float64, angle, out float64) state.SpaceTheme {
	if n == 0 {
		cols[0], n = ztheme.FieldColor(angle, out), 1
		return custom(t, cols, n)
	}
	if !t.Lock || n == 1 {
		cols[i] = ztheme.FieldColor(angle, out)
		t.Mix = ""
		return custom(t, cols, n)
	}
	out = min(max(out, 0), 1)
	cols[0] = ztheme.FieldColor(angle, out)
	for j := 1; j < n; j++ {
		cols[j] = ztheme.FieldColor(angle+held[j][0], out*held[j][1])
	}
	return custom(t, cols, n)
}

// heldPlaces are where n colours cols lie from the first: each one's turn
// round the field from it and its distance over the first's, which a locked
// drag keeps. Near the centre, where the first has no angle to speak of,
// the others keep their own distance.
func heldPlaces(cols [3]uint32, n int) [3][2]float64 {
	var held [3][2]float64
	a0, o0 := ztheme.FieldPlace(cols[0])
	for j := 1; j < n; j++ {
		a, o := ztheme.FieldPlace(cols[j])
		held[j] = [2]float64{a - a0, o / max(o0, 0.05)}
	}
	return held
}

// addDot is theme t with a colour more: locked, in the harmony n+1 colours
// take; free, opposite the first, or a quarter of the way round from it.
func addDot(t state.SpaceTheme, cols [3]uint32, n int) state.SpaceTheme {
	if n == 0 || n >= 3 {
		return t
	}
	if t.Lock {
		t.Mix = defaultMix(n + 1)
		ztheme.Harmonize(cols[0], t.Mix, &cols)
		return custom(t, cols, n+1)
	}
	ang, out := ztheme.FieldPlace(cols[0])
	turn := 180.0
	if n == 2 {
		turn = 90
	}
	cols[n] = ztheme.FieldColor(ang+turn, max(out, 0.5))
	return custom(t, cols, n+1)
}

// dropDot is theme t with its last colour taken away; a harmony becomes
// the one its colours left take.
func dropDot(t state.SpaceTheme, cols [3]uint32, n int) state.SpaceTheme {
	if n <= 1 {
		return t
	}
	if t.Mix != "" {
		t.Mix = defaultMix(n - 1)
		if t.Mix != "" {
			ztheme.Harmonize(cols[0], t.Mix, &cols)
		}
	}
	return custom(t, cols, n-1)
}

// nextMix is theme t with the next harmony for its n colours placed from the
// space's own and locked, so that they turn together; after the last the
// first again.
func nextMix(t state.SpaceTheme, cols [3]uint32, n int) state.SpaceTheme {
	var ring []string
	switch n {
	case 2:
		ring = []string{"complementary"}
	case 3:
		ring = []string{"analogous", "split", "triadic"}
	default:
		return t
	}
	at := -1
	for i, m := range ring {
		if m == t.Mix {
			at = i
		}
	}
	t.Mix, t.Lock = ring[(at+1)%len(ring)], true
	ztheme.Harmonize(cols[0], t.Mix, &cols)
	return custom(t, cols, n)
}

// defaultMix is the harmony n colours take when one is added or taken away
// under a harmony.
func defaultMix(n int) string {
	switch n {
	case 2:
		return "complementary"
	case 3:
		return "triadic"
	}
	return ""
}

// mixLabel is how harmony mix of n colours reads, locked or not.
func mixLabel(mix string, n int, lock bool) string {
	switch mix {
	case "analogous":
		return i18n.T("Neighbours")
	case "complementary":
		return i18n.T("Opposite")
	case "split":
		return i18n.T("Beside the opposite")
	case "triadic":
		return i18n.T("Triad")
	}
	switch {
	case n <= 1:
		return i18n.T("One colour")
	case lock:
		return i18n.T("Your own")
	}
	return i18n.T("Free")
}

// spaceName is how a space is named, and its folder's symbol ("" for an
// account: its picture).
func (sh *Shell) spaceName(a *state.Account, sp state.Space) (string, string) {
	if sp.Folder != "" {
		if i := a.FolderByID(sp.Folder); i >= 0 {
			f := &a.Settings.Folders[i]
			return f.Name, f.Icon
		}
	}
	name := a.Model.Me.Name()
	if len(name) == 0 {
		name = model.Phone(a.Model.Me.ID())
	}
	return model.S(name), ""
}

// folderIcon is a folder symbol's atlas icon, made the first time.
func (sh *Shell) folderIcon(sym string) icon.ID {
	if sh.sp.icons == nil {
		sh.sp.icons = map[string]icon.ID{}
	}
	if id, ok := sh.sp.icons[sym]; ok {
		return id
	}
	id := sh.env.K.AddIcon(ztheme.ToolIcon(sym, '▤'))
	sh.sp.icons[sym] = id
	return id
}

// spaceTint lays the colour of the space on screen over the window's field,
// as Arc's spaces and Zen's space themes tint the window: a little of it,
// showing through the panes' glass. A theme of two or three colours (its
// mix) runs across the field as a gradient: two from opposite corners, as
// Zen's 150° pair; three with the space's own along the foot and the others
// glowing from the top corners, never so strong the labels over it stop
// reading (SpaceAlpha). The accent takes the space's colour too
// (TintAccent, held to 4.5:1 on the panes). A change of space crossfades from the colours that showed.
func (sh *Shell) spaceTint(W, H float32) {
	s, e := sh.S, &sh.env
	c, r := e.C(), e.K.Theme()
	ti := &sh.tint
	th, dark := s.SpaceTheme(), r.Dark()
	if th != ti.of || dark != ti.dark || !ti.made {
		// the colours, worked out when the theme or the appearance changes
		// rather than each frame
		ti.of, ti.dark, ti.made = th, dark, true
		var cols [3]uint32
		n := ztheme.SpaceColors(th, &cols)
		alpha := float32(ztheme.TintAlphaLight)
		if dark {
			alpha = ztheme.TintAlpha
		}
		// the default opacity is the plain tint; full is nearly three times it
		alpha *= float32(math.Pow(float64(th.Strength()/state.DefaultTintLevel), 1.5))
		alpha = ztheme.SpaceAlpha(cols, n, alpha, dark)
		ti.goal = tintCorners(cols, n, alpha)
		ti.goalAcc = [2]uint32{}
		if n > 0 {
			ti.goalAcc = [2]uint32{ztheme.TintAccent(cols[0], false), ztheme.TintAccent(cols[0], true)}
		}
	}
	now, acc := ti.goal, ti.goalAcc
	if now != ti.now || acc != ti.nowAcc {
		ti.was, ti.wasAcc = ti.shown, ti.shownAcc
		ti.now, ti.nowAcc = now, acc
		ti.gen++
	}
	t := float32(1)
	if sh.Slides {
		t = c.AnimateValueFrom(ui.HashString(ui.KeyN(0, uint64(ti.gen)), "space tint"), 0, 1, ztheme.Enter(r, ztheme.T.DurStandard))
	}
	for i := range ti.shown {
		ti.shown[i] = mixTint(ti.was[i], ti.now[i], t)
	}
	for i := range ti.shownAcc {
		// the accent crossfades through the theme's own where a space has none
		a, b := ti.wasAcc[i], ti.nowAcc[i]
		if t < 1 {
			if a == 0 {
				a = ztheme.Accent(i == 1)
			}
			if b == 0 {
				b = ztheme.Accent(i == 1)
			}
			b = theme.MixRGBA(a, b, t)
		}
		ti.shownAcc[i] = b
	}
	r.Retint(ti.shownAcc[0], ti.shownAcc[1])
	if ti.shown == [4]uint32{} {
		return
	}
	theme.At(c, 0, 0, W, H)
	c.NextBG4(ti.shown[0], ti.shown[1], ti.shown[2], ti.shown[3])
	c.Box(base.Floating|ui.DrawBackground, "space tint")
}

// tintUI is the space's colour as it shows, crossfading as it changes.
type tintUI struct {
	was, now, shown          [4]uint32 // the field's corners (top left, top right, bottom left, bottom right)
	wasAcc, nowAcc, shownAcc [2]uint32 // the accent in light and dark; 0 the theme's
	gen                      uint32    // counts the changes: the crossfade's key
	// the theme last worked out, and its colours
	of         state.SpaceTheme
	dark, made bool
	goal       [4]uint32
	goalAcc    [2]uint32
}

// tintCorners are the field's corner colours for a theme of n colours cols
// at alpha.
func tintCorners(cols [3]uint32, n int, alpha float32) [4]uint32 {
	at := func(c uint32, a float32) uint32 { return theme.WithAlpha(c, a) }
	switch n {
	case 1:
		v := at(cols[0], alpha)
		return [4]uint32{v, v, v, v}
	case 2:
		mid := at(theme.MixRGBA(cols[0], cols[1], 0.5), alpha/2)
		return [4]uint32{at(cols[0], alpha), mid, mid, at(cols[1], alpha)}
	case 3:
		foot := at(cols[0], alpha)
		return [4]uint32{at(cols[2], alpha), at(cols[1], alpha), foot, foot}
	}
	return [4]uint32{}
}

// mixTint is a t of the way to b; a colour from none (0) keeps b's hue and
// fades in, and one to none fades out in its own.
func mixTint(a, b uint32, t float32) uint32 {
	switch {
	case t >= 1:
		return b
	case a == 0 && b == 0:
		return 0
	case a == 0:
		a = b &^ 0xFF
	case b == 0:
		b = a &^ 0xFF
	}
	return theme.MixRGBA(a, b, t)
}
