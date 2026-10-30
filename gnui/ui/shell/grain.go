package shell

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"

	"gonativeui/img"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	ztheme "zapfast/theme"
)

// A space's grain (state.SpaceTheme.Grain), as Zen's texture: a tile of
// fine noise laid over the field, under the panes, repeated across the
// window at a little opacity. The framework has no blend modes, so the
// noise is light and dark specks on transparency, which lighten and darken
// what is under them about evenly instead of greying it.

// grainPx is the tile's side in pixels: drawn at its own size, one texel a
// pixel, so the specks stay sharp at any window size.
const grainPx = 256

// grainMost is the opacity at full grain: a texture, never a veil.
const grainMost = 0.1

// grainSpeck is a speck's most opacity in the tile (of 255).
const grainSpeck = 140

var grainKey = ui.HashString(0, "zapfast/grain")

// spaceGrain lays the grain of the space on screen over the window's field
// (W×H pixels), easing to it as the space changes. A space without grain
// costs nothing.
func (sh *Shell) spaceGrain(W, H float32) {
	s, e := sh.S, &sh.env
	c, k := e.C(), e.K
	g := s.SpaceTheme().Grain
	t := g
	if sh.Slides {
		t = c.AnimateValue(ui.HashString(0, "zapfast/grain level"), g, ztheme.Enter(k.Theme(), ztheme.T.DurStandard))
	}
	if t <= 0.001 {
		return
	}
	cache := k.Images()
	if !sh.grainAsked {
		sh.grainAsked = true
		sh.grain, _ = cache.Request(img.Key{Hash: img.HashString("zapfast grain v2"), W: grainPx, H: grainPx}, img.Bytes(grainTile()))
	}
	tex, uv, st := cache.Use(sh.grain)
	if st != img.Ready {
		return // the cache wakes the window once it is decoded
	}
	theme.At(c, 0, 0, W, H)
	c.PushOpacity(t * grainMost) // the tiles' (NextOpacity is one box's)
	defer c.PopOpacity()
	c.Push(c.BoxKeyed(base.Floating|ui.Clip, grainKey, nil))
	n := uint64(1)
	for y := float32(0); y < H; y += grainPx {
		for x := float32(0); x < W; x += grainPx {
			theme.At(c, x, y, grainPx, grainPx)
			c.NextImageFit(tex, uv, ui.FitNatural)
			c.NextTextColor(0xFFFFFFFF)
			c.BoxKeyed(base.Floating|ui.DrawImage, ui.KeyN(grainKey, n), nil)
			n++
		}
	}
	c.Pop()
}

// grainTile is the grain's tile as a PNG: each pixel white or black at a
// random opacity of at most grainSpeck, the same every run (a fixed seed).
func grainTile() []byte {
	m := image.NewNRGBA(image.Rect(0, 0, grainPx, grainPx))
	r := rand.New(rand.NewPCG(7, 11))
	for y := range grainPx {
		for x := range grainPx {
			v := uint8(0)
			if r.IntN(2) == 1 {
				v = 255
			}
			m.SetNRGBA(x, y, color.NRGBA{v, v, v, uint8(r.IntN(grainSpeck + 1))})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, m) // an in-memory encode of a valid image does not fail
	return b.Bytes()
}
