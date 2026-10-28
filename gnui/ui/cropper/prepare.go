package cropper

import (
	"image"
	"image/draw"
	"image/png"
	"os"

	"gonativeui/img"
	"gonativeui/platform"
	"gonativeui/task"
)

// previewMax is the longest side of a turned preview, in pixels: enough for
// the cropper's room on a large display, small enough to decode and upload
// quickly.
const previewMax = 1600

// job asks for a preview file: the source picture turned by turns quarters
// clockwise. The window's image cache has no turn of its own and the quad
// draw has no rotation, so a turned picture is made here, off the UI thread.
type job struct {
	path  string // the source file, or "" for a pasted picture
	rgba  []byte // a pasted picture's straight RGBA rows
	w, h  int    // a pasted picture's size
	turns int
	out   string // where to write the PNG ("" to only remove drop)
	drop  string // an older preview to delete first
}

// done is what a job reports: the preview's size, or that it failed. The
// file's name is the one the job was given.
type done struct {
	w, h int32
	ok   bool
}

func newPool() *task.Pool[job, done] {
	return task.NewPool(task.Options{Workers: 1, Queue: 4, Wake: platform.Wake}, func(t task.Token, j job) done {
		return render(j, t.Cancelled)
	})
}

// render makes the preview j asks for; cancelled says the answer is no longer
// wanted.
func render(j job, cancelled func() bool) done {
	if j.drop != "" {
		os.Remove(j.drop)
	}
	if j.out == "" {
		return done{}
	}
	var pic *image.RGBA
	if j.path != "" {
		b, err := os.ReadFile(j.path)
		if err != nil {
			return done{}
		}
		// The system's decoder: any format the system reads, turned upright by
		// its EXIF orientation as the core does, and scaled down to the preview.
		m, err := img.Decode(b, previewMax)
		if err != nil {
			return done{}
		}
		pic = asRGBA(m)
	} else {
		if j.w <= 0 || j.h <= 0 || len(j.rgba) < j.w*j.h*4 {
			return done{}
		}
		pic = shrink(&image.NRGBA{Pix: j.rgba[:j.w*j.h*4], Stride: j.w * 4, Rect: image.Rect(0, 0, j.w, j.h)}, previewMax)
	}
	if cancelled() {
		return done{}
	}
	for i := 0; i < j.turns%4; i++ {
		pic = quarterTurn(pic)
	}
	if cancelled() {
		return done{}
	}
	f, err := os.Create(j.out)
	if err != nil {
		return done{}
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err = enc.Encode(f, pic)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(j.out)
		return done{}
	}
	b := pic.Bounds()
	return done{int32(b.Dx()), int32(b.Dy()), true}
}

// shrink is src as premultiplied RGBA, averaged down so that its longer side
// is at most limit.
func shrink(src image.Image, limit int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	full := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(full, full.Bounds(), src, b.Min, draw.Src)
	long := w
	if h > long {
		long = h
	}
	if long <= limit {
		return full
	}
	dw, dh := max(w*limit/long, 1), max(h*limit/long, 1)
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*h/dh, max(y*h/dh+1, (y+1)*h/dh)
		for x := 0; x < dw; x++ {
			x0, x1 := x*w/dw, max(x*w/dw+1, (x+1)*w/dw)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				row := full.Pix[sy*full.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, bl, a, n = r+uint32(p[0]), g+uint32(p[1]), bl+uint32(p[2]), a+uint32(p[3]), n+1
				}
			}
			o := out.Pix[y*out.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return out
}

// quarterTurn is src turned a quarter clockwise.
func quarterTurn(src *image.RGBA) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := src.Pix[y*src.Stride+x*4:]
			d := out.Pix[x*out.Stride+(h-1-y)*4:]
			d[0], d[1], d[2], d[3] = s[0], s[1], s[2], s[3]
		}
	}
	return out
}

// asRGBA is m as the premultiplied RGBA the turns work on (Orient returns its
// source's own kind, so this is only a type check).
func asRGBA(m image.Image) *image.RGBA {
	if p, ok := m.(*image.RGBA); ok {
		return p
	}
	b := m.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), m, b.Min, draw.Src)
	return out
}
