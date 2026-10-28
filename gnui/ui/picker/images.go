package picker

import (
	"gonativeui/gpu"
	"gonativeui/img"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/ui/screen"
)

// The picker's pictures (GIF stills, stickers) come from the window's image
// cache (k.Images()). They draw as widget/base's Kit.Image does, so an
// animated sticker plays only while its tile shows and the pointer is on it.

// picture builds a box w × h pixels showing the file at path, fitted by fit
// with corner radius radius. With animate it is requested as an animation (a
// still file is a still image), which plays while play is set and the box
// shows. Until the picture is ready the box is the theme's skeleton fill.
func (s *Screen) picture(e *screen.Env, n uint64, path string, w, h float32, fit ui.ImageFit, radius float32, play, animate bool) {
	k := e.K
	c, r := k.Context(), k.Theme()
	key := ui.KeyN(c.ParentKey(), 9040+n)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextRadius(radius)
	c.NextRole(ui.RoleImage)
	if ic := k.Images(); ic != nil && path != "" {
		var fl img.Flags
		if animate {
			fl = img.Animate
		}
		hd, _ := ic.Request(img.Key{Hash: img.HashString(path) ^ s.acct*0x9e3779b97f4a7c15, W: int32(w), H: int32(h), Flags: fl}, img.File(path))
		if hd != 0 {
			t, uv, st := use(ic, c, k.Kit, key, hd, play)
			if st == img.Ready {
				c.NextImageFit(t, uv, fit)
				c.NextTextColor(0xFFFFFFFF)
				c.BoxKeyed(ui.DrawImage, key, nil)
				return
			}
		}
	}
	c.NextBG(r.Colors[theme.Fill3])
	c.BoxKeyed(ui.DrawBackground, key, nil)
}

// use is Kit.Image's choice between Use and UseAnimated for the box keyed
// key (widget/base/image.go imageTexKeyed).
func use(ic *img.Cache, c *ui.Context, k *base.Kit, key ui.Key, h img.Handle, play bool) (t gpu.TextureID, uv [4]float32, st img.State) {
	if !ic.Animated(h) {
		return ic.Use(h)
	}
	p := img.Present{
		Now:          c.Input().Time,
		Visible:      play && !c.Input().Hidden,
		ReduceMotion: k.ReduceMotion || k.Theme().Settings.ReduceMotion,
	}
	if p.Visible {
		if id, ok := c.BoxOf(key); ok {
			rc := c.ShownRect(id)
			p.Visible = rc[2] > rc[0] && rc[3] > rc[1]
		}
		c.WatchShown(key, p.Visible)
	}
	t, uv, st, wake := ic.UseAnimated(h, p)
	if wake > 0 {
		c.WakeIn(wake)
	}
	return t, uv, st
}
