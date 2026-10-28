package cropper

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// A 16×8 lossless WebP: the left half red, the right half blue.
var redBlueWebP = []byte{
	0x52, 0x49, 0x46, 0x46, 0x26, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x4c, 0x19, 0x00, 0x00, 0x00,
	0x2f, 0x0f, 0xc0, 0x01, 0x00, 0x0f, 0x10, 0xf3, 0xbf, 0xff, 0xf3, 0x1f, 0x0e, 0xc4, 0x82, 0xc9, 0xfc, 0xa5, 0x3b, 0x53,
	0x88, 0xe8, 0x7f, 0xe4, 0x00, 0x00,
}

func previewOf(t *testing.T, path string, turns int) image.Image {
	t.Helper()
	out := filepath.Join(t.TempDir(), "o.png")
	got := render(job{path: path, turns: turns, out: out}, func() bool { return false })
	if !got.ok {
		t.Fatalf("no preview of %s", path)
	}
	m, err := readPNG(out)
	if err != nil {
		t.Fatal(err)
	}
	if m.Bounds().Dx() != int(got.w) || m.Bounds().Dy() != int(got.h) {
		t.Fatalf("preview %v, reported %dx%d", m.Bounds(), got.w, got.h)
	}
	return m
}

func isRed(c color.Color) bool  { r, g, b, _ := c.RGBA(); return r>>8 > 200 && g>>8 < 60 && b>>8 < 60 }
func isBlue(c color.Color) bool { r, g, b, _ := c.RGBA(); return r>>8 < 60 && g>>8 < 60 && b>>8 > 200 }

// A WebP is read by the system's decoder, the cropper keeping no decoder of
// its own, and turns like any picture.
func TestWebPPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.webp")
	if err := os.WriteFile(path, redBlueWebP, 0o600); err != nil {
		t.Fatal(err)
	}
	m := previewOf(t, path, 0)
	if m.Bounds().Dx() != 16 || m.Bounds().Dy() != 8 || !isRed(m.At(2, 4)) || !isBlue(m.At(13, 4)) {
		t.Errorf("WebP %v: left %v, right %v", m.Bounds(), m.At(2, 4), m.At(13, 4))
	}
	m = previewOf(t, path, 1) // a quarter clockwise: the left half is the top
	if m.Bounds().Dx() != 8 || m.Bounds().Dy() != 16 || !isRed(m.At(4, 2)) || !isBlue(m.At(4, 13)) {
		t.Errorf("turned WebP %v: top %v, bottom %v", m.Bounds(), m.At(4, 2), m.At(4, 13))
	}
}

// A JPEG that carries an orientation is upright in the preview, pixel for
// pixel: orientation 6 (turn 90° clockwise to show) puts the left half on top.
func TestRotatedJPEGPreviewIsUpright(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			if x < 8 {
				src.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				src.Set(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	for _, c := range []struct {
		o            int
		w, h         int
		first, other [2]int // a point in the red half and one in the blue
	}{
		{1, 16, 8, [2]int{2, 4}, [2]int{13, 4}},
		{3, 16, 8, [2]int{13, 4}, [2]int{2, 4}}, // upside down: the red half is on the right
		{6, 8, 16, [2]int{4, 2}, [2]int{4, 13}},
		{8, 8, 16, [2]int{4, 13}, [2]int{4, 2}},
	} {
		exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00")
		exif[len(exif)-8], exif[len(exif)-7] = byte(c.o>>8), byte(c.o)
		seg := append([]byte{0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}, exif...)
		data := append(append(append([]byte(nil), b[:2]...), seg...), b[2:]...)
		path := filepath.Join(t.TempDir(), "p.jpg")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		m := previewOf(t, path, 0)
		if m.Bounds().Dx() != c.w || m.Bounds().Dy() != c.h || !isRed(m.At(c.first[0], c.first[1])) || !isBlue(m.At(c.other[0], c.other[1])) {
			t.Errorf("orientation %d: %v, red-half point %v, blue-half point %v", c.o, m.Bounds(), m.At(c.first[0], c.first[1]), m.At(c.other[0], c.other[1]))
		}
	}
}
