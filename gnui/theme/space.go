package theme

import gtheme "gonativeui/theme"

// The spacing grid is the framework's (gonativeui/theme: GridUnit,
// GridMicro, Space1…Space8, MinGap, OnGrid), and ui's grid test holds the
// screens to it. These are Zap's radii on it. A shape inside another keeps
// their corners concentric: its radius is the outer one less the inset
// between them (theme.Concentric), from the window's 16 inward, and never
// under InnerRadius, so nothing nested goes square.
//   - PaneInset, PaneGutter: the panes from the window's edges and between
//     them (Theme.SetPaneGrid makes their corners concentric).
//   - BubbleRadius: a bubble, and a card drawn in place of one (a carousel's).
//   - Nested(): what sits in a bubble's padding: a quote, a picture, a
//     video, a document, a link's preview, a map, a poll's option.
//   - InnerRadius: what sits inside those (a preview's thumbnail).
const (
	PaneInset  = gtheme.Space2
	PaneGutter = gtheme.Space2

	BubbleRadius = 12 // the design playground's (zap-notes/playground.json)
	BubblePadX   = gtheme.Space2
	BubblePadY   = gtheme.Space2
	InnerRadius  = gtheme.Space1
)
