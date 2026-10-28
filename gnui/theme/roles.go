package theme

import (
	gtheme "gonativeui/theme"
	"gonativeui/theme/rex"
)

// rexTheme returns Rex with the colours of a palette in place of its own, in
// the palette's mode: the mapping of upstream's palette onto Rex's colour
// roles (TH-005). Rex keeps its shapes, sizes, type, motion and chrome; its
// colours become the palette's. Both mode slots hold the palette, so the
// theme never fades (the window forces the palette's mode).
//
//	window                     the window's field, the under-page, the title bar's scroll edge
//	panel                      panes, cards, the sidebar (the chat list)
//	surface                    fields, group boxes, toolbar capsules
//	surface_hover              striped rows
//	surface_active             the selection without focus, tracks
//	outline                    separators, hairlines, field outlines
//	text secondary dim         labels, secondary labels, tertiary labels and placeholders
//	accent on_accent           the accent, text on it; selection, focus ring, text selection
//	danger warning             red and orange (yellow)
//	overlay                    what floats over a pane: popovers, menus, sheets
//	shadow                     the shadow colour
//	link                       links
//
// accent_hover, chat, bubble_in, bubble_out and read have no Rex role: the
// conversation draws them through this package's API.
func rexTheme(p *Palette) *gtheme.Theme {
	th := rex.Theme()
	m := 0
	if p.Dark {
		m = 1
	}
	base := th.Roles[m]
	roles := paletteRoles(p, base)
	th.Roles = [2]gtheme.Colors{roles, roles}
	pal := palettePal(p, th.Light)
	if p.Dark {
		pal = palettePal(p, th.Dark)
	}
	th.Light, th.Dark = pal, pal
	th.SystemAccent, th.SystemColors = false, false
	th.Name = "Rex"
	return th
}

// opaque is c at full opacity.
func opaque(c uint32) uint32 { return c | 0xFF }

// at is c's colour at opacity a (0 to 1).
func at(c uint32, a float32) uint32 { return gtheme.WithAlpha(c|0xFF, a) }

// paletteRoles is base with the palette's colours in the roles it names.
func paletteRoles(p *Palette, base gtheme.Colors) gtheme.Colors {
	c := base
	win, panel, surf := opaque(p.Window), opaque(p.Panel), opaque(p.Surface)
	text, acc := opaque(p.Text), opaque(p.Accent)

	c[gtheme.Window], c[gtheme.UnderPage], c[gtheme.Field] = win, win, win
	c[gtheme.ControlBackground], c[gtheme.Pane] = panel, panel
	c[gtheme.TextBackground], c[gtheme.InputFill] = surf, surf
	c[gtheme.GroupBox] = surf
	c[gtheme.AltRow] = opaque(p.SurfaceHover)
	c[gtheme.Raised] = opaque(p.Overlay)
	c[gtheme.Shadow] = p.Shadow

	// text
	c[gtheme.Label], c[gtheme.ControlText], c[gtheme.Text] = text, text, text
	c[gtheme.HeaderText], c[gtheme.TitleText] = text, text
	c[gtheme.SelectedText] = text
	c[gtheme.Label2] = opaque(p.Secondary)
	dim := opaque(p.Dim)
	c[gtheme.Label3], c[gtheme.Placeholder] = dim, dim
	c[gtheme.Label4], c[gtheme.Label5] = at(p.Dim, 0.55), at(p.Dim, 0.3)
	c[gtheme.TextDisabled] = at(p.Dim, 0.7)
	c[gtheme.TitleTextInactive], c[gtheme.ToolbarSymbolInactive] = dim, dim
	c[gtheme.SidebarLabel] = text
	c[gtheme.SidebarLabelInactive], c[gtheme.SidebarHeader] = opaque(p.Secondary), dim
	c[gtheme.SidebarHeaderInactive], c[gtheme.SidebarDetail] = dim, at(p.Secondary, 0.8)
	c[gtheme.SelectedRowTextInactive] = text

	// fills: the text colour at the macOS fills' strengths
	for i, a := range [...]float32{0.10, 0.08, 0.05, 0.03, 0.01} {
		c[gtheme.Fill1+gtheme.Role(i)] = at(p.Text, a)
	}
	c[gtheme.FillPressed], c[gtheme.FillDisabled] = at(p.Text, 0.17), at(p.Text, 0.04)

	// lines
	out := opaque(p.Outline)
	c[gtheme.Separator], c[gtheme.Grid], c[gtheme.PaneHairline] = out, out, at(p.Outline, 0.8)
	c[gtheme.FieldOutline] = at(p.Outline, 0.9)
	c[gtheme.ScrollEdgeLine], c[gtheme.AccessoryLine] = at(p.Outline, 0.7), at(p.Outline, 0.5)
	c[gtheme.CapsuleRim], c[gtheme.CapsuleRimInactive] = at(p.Outline, 0.9), out

	// the accent and what follows it
	c[gtheme.Accent], c[gtheme.Selection], c[gtheme.SidebarSel] = acc, acc, acc
	c[gtheme.OnAccent], c[gtheme.OnSelection] = opaque(p.OnAccent), opaque(p.OnAccent)
	c[gtheme.FocusRing] = at(p.Accent, 0.5)
	c[gtheme.TextSelection] = at(p.Accent, 0.35)
	c[gtheme.Caret] = acc
	c[gtheme.Link] = opaque(p.Link)
	active := opaque(p.SurfaceActive)
	c[gtheme.SelectionUnemph], c[gtheme.TextSelectionUnemph] = active, active
	c[gtheme.SidebarSelUnemph], c[gtheme.SidebarSelInactive] = active, active

	// the sidebar and the window's bars
	c[gtheme.SidebarFill], c[gtheme.SidebarFillInactive] = panel, panel
	c[gtheme.SidebarSearchFill], c[gtheme.SidebarSearchFillInactive] = surf, surf
	c[gtheme.ScrollEdgeBar], c[gtheme.ScrollEdgeBarInactive] = win, win
	c[gtheme.CapsuleFill], c[gtheme.CapsuleFillInactive] = surf, surf
	c[gtheme.GlassTint] = at(p.Panel, 0.55)
	c[gtheme.PaneHighlight] = at(p.Text, 0.04)

	// indicator colours
	c[gtheme.Red] = opaque(p.Danger)
	c[gtheme.Orange], c[gtheme.Yellow] = opaque(p.Warning), opaque(p.Warning)

	// the roles that follow the accent and the selection (gtheme's derive
	// runs on the Resolved too; these are the values before it)
	c[gtheme.AccentPressed] = gtheme.Over(at(0x000000FF, 0.10), acc)
	if p.Dark {
		c[gtheme.AccentPressed] = gtheme.Over(at(0xFFFFFFFF, 0.10), acc)
	}
	c[gtheme.SelectionDivider] = gtheme.Over(at(0xFFFFFFFF, 0.10), acc)
	return c
}

// palettePal is base (Rex's phase-5 palette of the mode) with the palette's
// colours: the widgets that still read gtheme.Palette follow it.
func palettePal(p *Palette, base gtheme.Palette) gtheme.Palette {
	b := base
	b.Background = opaque(p.Window)
	b.Surface = opaque(p.Panel)
	b.SurfaceRaised = opaque(p.Overlay)
	b.Text, b.TextMuted, b.TextDisabled = opaque(p.Text), opaque(p.Secondary), opaque(p.Dim)
	b.Accent, b.OnAccent = opaque(p.Accent), opaque(p.OnAccent)
	b.Line = at(p.Outline, 0.9)
	b.Selection = at(p.Accent, 0.25)
	b.SelectionText = opaque(p.Text)
	b.FocusRing = at(p.Accent, 0.9)
	b.Danger, b.Warning = opaque(p.Danger), opaque(p.Warning)
	b.Shadow = p.Shadow
	b.HoverOverlay, b.PressOverlay = at(p.Text, 0.07), at(p.Text, 0.14)
	b.ControlFill, b.ControlBorder = opaque(p.Surface), at(p.Outline, 0.9)
	b.TrackFill = opaque(p.SurfaceActive)
	b.ScrollThumb = at(p.Text, 0.35)
	return b
}
