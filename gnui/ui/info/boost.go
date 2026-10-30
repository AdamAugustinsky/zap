package info

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// boost is the chat's boost (state.ChatBoost), as Zen's Boosts restyle a
// site: the face its messages read in, their size, and a colour your
// bubbles take. Only you see it; it changes the conversation at once. Its
// controls fold under a row, as the media row's, so the panel stays short.
func (s *Screen) boost(e *screen.Env, a *state.Account) {
	k := e.K
	c := e.C()
	th := k.Theme()
	jid := s.jid[:s.jidN]
	b := a.BoostOf(jid)
	k.NextGap(8)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "boost", ui.Pct(1), ui.Children(1), 0)
	p := k.PressState(i18n.T("How this chat reads"), base.HoverOn)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(4))
	c.NextRadius(k.Pt(6))
	if s.boostOpen {
		c.NextSemState(ui.SemExpanded)
	}
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), k.Fixed(36), 0, nil)
	k.NextWidth(widget.Fill)
	c.NextSize(ui.Pct(1), ui.Px(k.Pt(18)))
	c.NextFont(s.font(e, 13.5, 400))
	c.NextTextColor(th.Colors[theme.Label2])
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 1), base.StrBytes(p.Name))
	chev := s.ic.chevronRight
	if s.boostOpen {
		chev = s.ic.chevronDown
	}
	k.Icon(chev, th.Colors[theme.Label3])
	sig := k.EndPress(&p)
	e.TabStop(screen.StopInfoControl+mediaRowStop+1, sig.ID)
	if sig.Clicked {
		s.boostOpen = !s.boostOpen
	}
	if !s.boostOpen {
		k.EndContainer()
		return
	}

	s.faceOpts = [4]string{i18n.T("Default"), i18n.T("Serif"), i18n.T("Sans"), i18n.T("Mono")}
	face := 0
	for i, f := range state.BoostFonts {
		if f == b.Font {
			face = i
		}
	}
	if rexui.SegmentedControl(k.Kit, i18n.T("Font"), &face, s.faceOpts[:], rexui.SegmentedOpts{Size: theme.SizeSmall}).Changed {
		b.Font = state.BoostFonts[face]
		a.SetBoost(jid, b)
	}
	s.sizeOpts = [5]string{"90%", "100%", "110%", "125%", "150%"}
	size := 1
	for i, v := range state.BoostSizes {
		if v == b.Scale() {
			size = i
		}
	}
	if rexui.SegmentedControl(k.Kit, i18n.T("Text size"), &size, s.sizeOpts[:], rexui.SegmentedOpts{Size: theme.SizeSmall}).Changed {
		b.Size = state.BoostSizes[size]
		a.SetBoost(jid, b)
	}

	// your bubbles' colour: none (Zap's green) or one of the spaces'
	sw := k.Pt(18)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(4))
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextSemName(i18n.T("Bubble colour"))
	c.NextRole(ui.RoleGroup)
	c.Push(c.Box(0, "bubble colours"))
	swatch := func(name string) {
		ps := k.PressState(c.Fmt().Str("boost tint ").Str(name).String(), base.HoverOn)
		ps.Name = screen.TintLabel(name)
		col := ztheme.TintColor(name)
		if col == 0 {
			col = th.Colors[theme.Accent] | 0xFF
		}
		on := name == b.Tint
		c.NextBG(col)
		c.NextRadius(sw / 2)
		if on {
			c.NextBorder(th.Colors[theme.Label], th.Lines.Hairline*2)
			c.NextSemState(ui.SemSelected)
		} else if ps.Hot > 0 {
			c.NextBorder(theme.WithAlpha(th.Colors[theme.Label], 0.5), th.Lines.Hairline)
		}
		k.BeginPress(&ps, ui.RoleButton, ui.Px(sw), ui.Px(sw), ui.DrawBackground|ui.DrawBorder, nil)
		if k.EndPress(&ps).Clicked && !on {
			b.Tint = name
			a.SetBoost(jid, b)
		}
	}
	swatch("")
	for _, name := range state.SpaceTints {
		swatch(name)
	}
	c.Pop()
	k.EndContainer()
}
