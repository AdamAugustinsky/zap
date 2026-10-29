package theme

import (
	"gonativeui/icon"
	"gonativeui/rexui"
)

// Zap's icons come in three sizes, each with the box it is pressed in, so
// that one role looks and answers alike wherever it is:
//   - SmallIcon: an indicator in a row's text (a pin, a muted bell, an @,
//     ticks, a call's direction) and a small button beside text (a strip's
//     close, the hints' keyboard), pressed in HitSmall.
//   - ToolIcon: a toolbar's or a pane's title row's item, the rail's, Back
//     and Close: Rex's toolbar symbol (AppKit's, measured), pressed in
//     HitTool, the band's item.
//   - LargeIcon: the composer's tools, on plates as tall as its field
//     (HitLarge).
//
// An icon in a button with a label is the label's size, beside it. An
// action is outlined; a state (pinned, muted, a call going on) is filled.
const (
	SmallIconPt = 12
	LargeIconPt = 17

	HitSmall = Space6
	HitTool  = 28
	HitLarge = 36 // the composer's field's height
)

// smallWeight is the small icons' weight (medium), which keeps a glyph that
// small as heavy as the text it sits in.
const smallWeight = 0.23

// SmallIcon is symbol at the small size.
func SmallIcon(symbol string, fallback rune) icon.Spec {
	return icon.Spec{Symbol: symbol, Fallback: fallback, Pt: SmallIconPt, Weight: smallWeight}
}

// ToolIcon is symbol as a toolbar's item.
func ToolIcon(symbol string, fallback rune) icon.Spec {
	return rexui.ToolbarSymbol(symbol, fallback)
}

// LargeIcon is symbol at the composer's size.
func LargeIcon(symbol string, fallback rune) icon.Spec {
	return icon.Spec{Symbol: symbol, Fallback: fallback, Pt: LargeIconPt}
}
