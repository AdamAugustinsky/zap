package shell

import (
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
		switch {
		case lit:
			fill = rexui.ChatRowLook.Selected.RGBA(th)
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
	}
	c.Pop()
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
