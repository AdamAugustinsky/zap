package login

import (
	_ "embed"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/ui/screen"
)

// The linking and lock screens share a card: the logo, the name and a line
// under it. Their draggable strip across the top (zf:src/ui/mod.rs
// titlebar_strip) is the shell's toolbar band.

// markPNG is the app icon's tile (packaging/macos/icon-1024.svg) rastered at 192 px, 3×
// the 64 pt it is shown at, with transparent corners.
//
//go:embed mark.png
var markPNG []byte

const (
	markPt   = 64
	markHash = 0x7A9FD00D0A11 // names the logo in the image cache
)

// Mark draws the logo, 64 points square, as the first thing in the card. Until
// the cache has decoded it (a frame or two) the box shows the skeleton fill.
func Mark(e *screen.Env) {
	k := e.K
	px := int32(e.Pt(markPt))
	var h img.Handle
	if ic := k.Images(); ic != nil {
		h, _ = ic.Request(img.Key{Hash: markHash, W: px, H: px}, img.Bytes(markPNG))
	}
	k.NextSize(ui.Px(float32(px)), ui.Px(float32(px)))
	k.Image(1, h, ui.FitContain, 0, "")
}

// Scroller holds the card of the linking and lock screens in a scroll view
// that scrolls only when the card does not fit the window (a narrow window, a
// long language), and keeps the card centred when it does. Upstream's card is
// not scrollable: its content runs past the card; scrolling is the Go app's
// way to keep every control reachable (the QR stays whole, 260 pt).
type Scroller struct {
	st   base.ScrollState
	v    base.ScrollView
	wrap ui.Key // the card's wrapper: its height from the last frame centres it
}

// edge is the room kept around a card that does not fit.
const edge = 12 // points

// Begin opens the screen's root at r and the scroll view in it; the caller
// builds the card between Begin and End, in a column centred across.
func (s *Scroller) Begin(e *screen.Env, r screen.Rect, name string) {
	k, c := e.K, e.C()
	k.BeginContainer(ui.AxisY, ui.CrossStart, name, ui.Px(r.W), ui.Px(r.H), 0)
	s.v = k.BeginScroll(name, &s.st, base.ScrollVertical)
	pad := e.Pt(edge)
	if wr := c.RectOf(s.wrap); wr[3] > wr[1] {
		if h := wr[3] - wr[1]; h+2*pad < r.H {
			pad = theme.Snap((r.H - h) / 2)
		}
	}
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextPadding(0, pad, 0, pad)
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.Push(c.BoxKeyed(0, ui.HashString(0, name+"-content"), nil))
	k.NextGap(0)
	id := k.BeginContainer(ui.AxisY, ui.CrossCenter, "wrap", ui.Children(1), ui.Children(1), 0)
	s.wrap = c.KeyOf(id)
}

// End closes what Begin opened.
func (s *Scroller) End(e *screen.Env) {
	k := e.K
	k.EndContainer() // wrap
	e.C().Pop()      // the content
	k.CloseScrollContent(&s.v)
	rexui.ScrollBars(k.Kit, &s.v)
	k.CloseScroll(&s.v)
	k.EndContainer() // root
}
