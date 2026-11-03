package theme

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	gtheme "gonativeui/theme"
)

// Zap's icons are Lucide's (go-native-ui's D96), named as on lucide.dev, in
// the framework's three sizes (gonativeui/theme: SmallIconPt, ToolIconPt,
// LargeIconPt, pressed in HitSmall, HitTool, HitLarge):
//   - SmallIcon: an indicator in a row's text (a pin, a muted bell, an @,
//     a call's direction) and a small button beside text (a strip's close,
//     the hints' keyboard).
//   - ToolIcon: a toolbar's or a pane's title row's item, the rail's, Back
//     and Close.
//   - LargeIcon: the composer's tools, on plates as tall as its field.
//
// An icon in a button with a label is the label's size, beside it.

// SmallIcon is name at the small size, as heavy as the text it sits in.
func SmallIcon(name string) icon.Spec {
	s := icon.Lucide(name).Size(gtheme.SmallIconPt)
	s.Weight = gtheme.SmallIconWeight
	return s
}

// ToolIcon is name as a toolbar's item.
func ToolIcon(name string) icon.Spec { return rexui.ToolbarIcon(name, 0) }

// TickIcon is name as a message's delivery mark: as tall as the metadata it
// ends and as heavy (semibold, as the time's figures read). A pair of checks
// overlaps by TickStep of a check's width.
func TickIcon(name string) icon.Spec {
	s := icon.Lucide(name).Size(TickPt)
	s.Weight = 0.3
	return s
}

// TickPt is the delivery marks' size; TickStep how far the second check of
// a pair sits from the first, as a share of a check's width.
const (
	TickPt   = 10
	TickStep = 0.42
)

// LargeIcon is name at the composer's size.
func LargeIcon(name string) icon.Spec { return icon.Lucide(name).Size(gtheme.LargeIconPt) }
