package theme

// Zap's spacing grid. Every gap, padding, inset and margin in the layout is
// a step of the grid, in points: a multiple of Unit, from the scale below.
// Micro is the one exception, an optical nudge inside one element (a
// hairline's neighbour, a badge settling on an avatar's edge), never the
// space between two elements. ui's grid test (gridlint_test.go) fails on
// any literal that is not a step.
//
// Two rules sit on the scale:
//   - Space1 (4) is the space inside one element: an icon and its label, a
//     name and its time, the lines of one block.
//   - MinGap (8) is the least space between two elements: two controls,
//     two rows, a block and the text under it, content and its container's
//     edge.
const (
	Unit  = 4
	Micro = 2

	Space1 = 4
	Space2 = 8
	Space3 = 12
	Space4 = 16
	Space5 = 20
	Space6 = 24
	Space8 = 32

	MinGap = Space2
)

// Zap's radii. A shape inside another keeps their corners concentric: its
// radius is the outer one less the inset between them (theme.Concentric),
// from the window's 16 inward, and never under InnerRadius, so nothing
// nested goes square.
//   - PaneRadius: the panes, inset PaneInset from the window's corner.
//   - BubbleRadius: a bubble, and a card drawn in place of one (a carousel's).
//   - NestedRadius: what sits in a bubble's padding: a quote, a picture, a
//     video, a document, a link's preview, a map, a poll's option.
//   - InnerRadius: what sits inside those (a preview's thumbnail).
const (
	WindowRadius = 16
	PaneInset    = Space2
	PaneGutter   = Space2
	PaneRadius   = WindowRadius - PaneInset

	BubbleRadius = 16
	BubblePadX   = Space3
	BubblePadY   = Space2
	NestedRadius = BubbleRadius - BubblePadY
	InnerRadius  = Space1
)

// OnGrid reports whether pt is a step of the grid: 0, Micro, or a multiple
// of Unit.
func OnGrid(pt float32) bool {
	if pt == 0 || pt == Micro {
		return true
	}
	n := pt / Unit
	return n == float32(int(n))
}
