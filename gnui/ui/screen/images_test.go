package screen

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

type imagePage struct {
	k     *widget.Kit
	cache *img.Cache
	twice bool
}

func (p *imagePage) Init(a *app.App) error {
	p.k = NewKit(a, nil)
	p.cache = NewImages(a, p.k, func(o *img.Options) { o.UploadBytes = 1 })
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	for i := range 8 {
		p.cache.Request(img.Key{Hash: uint64(i + 1)}, img.Bytes(b.Bytes()))
	}
	return nil
}
func (p *imagePage) Build(c *ui.Context) {
	p.k.Begin(c)
	PollImages(p.k, c)
	for i := range 8 {
		p.cache.Request(img.Key{Hash: uint64(i + 1)}, img.Source{})
	}
	if p.twice {
		PollImages(p.k, c)
	} // shell, then standalone-screen fallback
	p.k.End()
}
func TestImagePollOncePerFrame(t *testing.T) {
	p := &imagePage{twice: true}
	r := widgettest.Open(t, app.Config{Width: 100, Height: 100}, 100, 100, 1, p)
	t.Cleanup(func() { p.cache.Close(time.Second) })
	previous := p.cache.Stats().Uploads
	for end := time.Now().Add(2 * time.Second); previous < 8; {
		if time.Now().After(end) {
			t.Fatal("uploads stalled")
		}
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
		n := p.cache.Stats().Uploads
		if n-previous > 1 {
			t.Fatalf("%d uploads in one frame, budget allows 1", n-previous)
		}
		previous = n
	}
	t.Logf("uploads=%d, maximum per frame=1", previous)
}
