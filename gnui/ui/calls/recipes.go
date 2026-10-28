package calls

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/widget/base"
)

// The looks of the call window's round controls (zf:src/ui/call.rs control,
// answer): solid fills that stay as they are when the window is not the key
// window (a ringing call's window is never the key one), where Rex's
// IconPrimary and IconDestructive go grey and tinted. Hovering lays the glyph
// colour over the fill at 12 % (upstream's blend(fill, tint, 0.12)), pressing
// at 24 %, and a control that does not apply yet is its fill at 60 %. They
// are values of the package, so a frame allocates nothing for them.
var (
	// recAccept is Accept: the accent with the accent's glyph.
	recAccept = solidRecipe(rexui.Of(theme.Accent), rexui.Of(theme.OnAccent))
	// recDanger is Decline and Hang up: the danger colour with a white glyph.
	recDanger = solidRecipe(rexui.Of(theme.Red), rexui.OfRGBA(0xFFFFFFFF))
	// recSurface is Mute, the camera and Devices while they are off: the
	// active surface with the text colour.
	recSurface = solidRecipe(rexui.Of(theme.SelectionUnemph), rexui.Of(theme.Label))
	// recInverse is the same three while they are on (muted, the camera
	// off, the pickers open): the text colour with the window's.
	recInverse = solidRecipe(rexui.Of(theme.Label), rexui.Of(theme.Window))
)

// solidRecipe is a round control of fill with glyph, the same in an active
// and an inactive window.
func solidRecipe(fill, glyph rexui.Tint) rexui.ButtonRecipe {
	rc := rexui.IconButtonRecipes[rexui.IconBordered]
	rc.Fill, rc.FillInactive = fill, fill
	rc.Label, rc.LabelInactive = glyph, glyph
	rc.FillHover = alpha(glyph, 0.12)
	rc.FillPressed, rc.FillPressedInactive = alpha(glyph, 0.24), alpha(glyph, 0.24)
	rc.LabelPressed, rc.LabelPressedInactive = glyph, glyph
	rc.FillDisabled, rc.LabelDisabled = alpha(fill, 0.6), glyph
	rc.Rim, rc.RimInactive, rc.Highlight, rc.HighlightTop = rexui.None, rexui.None, rexui.None, false
	rc.Shadow, rc.ShadowY, rc.ShadowBlur = rexui.None, 0, 0
	rc.Glass, rc.Overlay = false, true
	rc.Hover = base.HoverOn
	return rc
}

// alpha is t at opacity a in light and dark.
func alpha(t rexui.Tint, a float32) rexui.Tint {
	t.Alpha = [2]float32{a, a}
	return t
}
