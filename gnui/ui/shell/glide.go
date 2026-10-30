package shell

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/workspace"

	ztheme "zapfast/theme"
)

// The panes glide (the motion rules' 3: things come from where they live).
// Hiding the list (⌘B) sweeps its right edge toward the rail and fades it,
// and the conversation widens into its place; the side pane comes in from the
// window's edge and folds away the same way. The workspace layout places
// the panes at once: only where they are drawn eases there, over
// Motion.Standard, so presses go by the layout as before. A divider dragged,
// the window resized, a narrow window (which slides on its own) and Reduce
// Motion place them at once.

// paneGlide is where the list, the conversation and the side pane are
// drawn, by PaneID.
type paneGlide struct {
	last [4][4]float32 // where each was last shown
	wh   [2]float32    // the window's size the frame before
	on   bool          // this frame eases
}

var glideKey = ui.HashString(0, "zapfast/pane glide")

// glideFrame decides whether this frame eases: not while a divider is
// dragged, the window changes size, or a narrow window shows one pane.
func (sh *Shell) glideFrame(W, H float32, narrow, dragged bool) {
	g := &sh.glide
	g.on = sh.Slides && !narrow && !dragged && g.wh == [2]float32{W, H}
	g.wh = [2]float32{W, H}
}

// glided is where pane id is drawn this frame and how far it is shown
// (0..1): easing toward the layout's place, and, for the list and the side
// pane coming or going, that far from their edge (gut is the gutter).
func (sh *Shell) glided(id workspace.PaneID, gut float32) ([4]float32, float32) {
	g, l := &sh.glide, &sh.panes
	c, r := sh.env.C(), sh.env.K.Theme()
	on := l.Shown(id)
	if on {
		g.last[id] = l.Rect(id)
	}
	b := g.last[id]
	rate := ztheme.Toward(r, ztheme.T.DurStandard, on)
	if !g.on {
		rate = theme.Instant
	}
	target := float32(0)
	if on {
		target = 1
	}
	t := c.AnimateValue(ui.KeyN(glideKey, uint64(id)), target, rate)
	x0 := c.AnimateValue(ui.KeyN(glideKey, 10+uint64(id)), b[0], rate)
	x1 := c.AnimateValue(ui.KeyN(glideKey, 20+uint64(id)), b[2], rate)
	pr := [4]float32{theme.Snap(x0), b[1], theme.Snap(x1), b[3]}
	if t < 1 {
		switch id {
		case paneList:
			// its right edge sweeps over it toward the rail, which it never
			// covers (listIn keeps its content at its full width, clipped)
			pr[2] = theme.Snap(pr[0] + (pr[2]-pr[0])*t)
		case paneSide:
			w := (pr[2] - pr[0] + gut) * (1 - t)
			pr[0], pr[2] = theme.Snap(pr[0]+w), theme.Snap(pr[2]+w)
		}
	}
	return pr, t
}

// glideFull is pane id's width where it was last shown, pixels: what its
// content is laid out at while its edge sweeps.
func (sh *Shell) glideFull(id workspace.PaneID) float32 {
	b := sh.glide.last[id]
	return b[2] - b[0]
}
