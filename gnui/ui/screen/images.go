package screen

import (
	"weak"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget"
)

// UI-thread bookkeeping must not keep closed windows or their caches alive.
type imagePoll struct {
	cache   weak.Pointer[img.Cache]
	context weak.Pointer[ui.Context]
	frame   uint32
}

var imagePolls []imagePoll

// ImageTuner is implemented by a WindowScreen whose window needs other image
// budgets than the default (the viewer's full-size pictures): it adjusts the
// options of its window's cache before the cache is made.
type ImageTuner interface {
	ImageOptions(o *img.Options)
}

// NewImages makes a window's image cache and gives it to the window's kit
// (k.Images()): every screen of the window draws from it with k.Image and
// asks for images with k.Images().Request. Its decodes wake that window
// only. Nil (and none given) when the app has no renderer. The window's
// owner polls it once per frame (PollImages) and closes it when the window
// has closed.
func NewImages(a *app.App, k *widget.Kit, tune func(o *img.Options)) *img.Cache {
	if a == nil || a.R == nil {
		return nil
	}
	// The CPU budget is the decoded pixels kept after their upload, to
	// upload again after a GPU eviction (pixels waiting for their upload,
	// and an animation in view, are kept whatever it is). They are C memory,
	// in the footprint whole, while the GPU copies are not: at 32 MiB the
	// tier held 28 MiB of photos the GPU already had after the 10k-message
	// session; 8 MiB leaves the footprint 16 MiB lower
	// (docs/measurements/zapfast-macmini.md, round 4).
	o := img.Options{Renderer: a.R, GPUBytes: 128 << 20, CPUBytes: 8 << 20, Entries: 4096}
	if w := a.Window(); w != nil {
		o.Wake = w.Wake
	}
	if tune != nil {
		tune(&o)
	}
	c := img.NewCache(o)
	k.SetImages(c)
	return c
}

// PollImages takes the window's finished decodes and uploads them, once per
// frame after Kit.Begin, and asks for another frame while uploads wait.
// The shell owns polling; standalone screens call the same idempotent fallback.
func PollImages(k *widget.Kit, c *ui.Context) {
	ca := k.Images()
	if ca == nil {
		return
	}
	slot := -1
	for i, p := range imagePolls {
		if p.cache.Value() == ca {
			if p.context.Value() == c && p.frame == c.Frame() {
				return
			}
			slot = i
			break
		}
		if p.cache.Value() == nil {
			slot = i
		}
	}
	if slot < 0 {
		slot = len(imagePolls)
		imagePolls = append(imagePolls, imagePoll{})
	}
	imagePolls[slot] = imagePoll{weak.Make(ca), weak.Make(c), c.Frame()}
	if ca.Poll() {
		c.RequestFrame()
	}
}
