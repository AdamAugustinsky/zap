package av

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/image/webp"

	"gonativeui/img"
)

func encodePNG(t *testing.T, m image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// redCorner is a picture whose top-left pixel is red and the rest blue, so a
// turn can be told from the other direction.
func redCorner(t *testing.T, w, h int) []byte { return redCornerAt(t, w, h, 1) }

// redCornerAt is redCorner with each pixel a block of scale × scale. The
// tests that check a turn write JPEGs, and Go's encoder halves the colour
// resolution, which a single red pixel does not survive (upstream's encoder
// keeps it whole).
func redCornerAt(t *testing.T, w, h, scale int) []byte {
	m := image.NewNRGBA(image.Rect(0, 0, w*scale, h*scale))
	for y := range h * scale {
		for x := range w * scale {
			c := color.NRGBA{0, 0, 255, 255}
			if x < scale && y < scale {
				c = color.NRGBA{255, 0, 0, 255}
			}
			m.SetNRGBA(x, y, c)
		}
	}
	return encodePNG(t, m)
}

// blockAt is the colour in the middle of block (x, y) of a scaled picture.
func blockAt(m *image.NRGBA, x, y, scale int) color.NRGBA {
	return m.NRGBAAt(x*scale+scale/2, y*scale+scale/2)
}

func decodeAny(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	m, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return nrgba(m)
}

func isRed(c color.NRGBA) bool  { return c.R > 200 && c.B < 60 }
func isBlue(c color.NRGBA) bool { return c.B > 200 && c.R < 60 }

// The tests below are upstream's picture_edit.rs tests.

func TestAPictureReportsItsSize(t *testing.T) {
	if w, h, err := InspectPicture(redCorner(t, 800, 600)); err != nil || w != 800 || h != 600 {
		t.Fatal(w, h, err)
	}
	if _, _, err := InspectPicture([]byte("not a picture")); !errors.Is(err, ErrPicture) {
		t.Fatal(err)
	}
}

func TestACropKeepsOnlyTheRegionItNames(t *testing.T) {
	e, err := EditPicture(redCorner(t, 800, 600), PictureCrop{100, 50, 200, 120}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeAny(t, e.Bytes).Rect; b.Dx() != 200 || b.Dy() != 120 {
		t.Fatal(b)
	}
}

func TestAQuarterTurnClockwiseMovesTheTopLeftToTheTopRight(t *testing.T) {
	const s = 16
	e, err := EditPicture(redCornerAt(t, 4, 2, s), FullCrop(2*s, 4*s), 1)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeAny(t, e.Bytes)
	if m.Rect.Dx() != 2*s || m.Rect.Dy() != 4*s {
		t.Fatal(m.Rect)
	}
	if !isRed(blockAt(m, 1, 0, s)) || !isBlue(blockAt(m, 0, 0, s)) {
		t.Fatal(blockAt(m, 1, 0, s), blockAt(m, 0, 0, s))
	}
}

func TestTurningAndCroppingMeasureTheCropAfterTheTurn(t *testing.T) {
	const s = 16
	e, err := EditPicture(redCornerAt(t, 4, 2, s), PictureCrop{1 * s, 0, 1 * s, 2 * s}, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeAny(t, e.Bytes)
	if m.Rect.Dx() != s || m.Rect.Dy() != 2*s || !isRed(blockAt(m, 0, 0, s)) || !isBlue(blockAt(m, 0, 1, s)) {
		t.Fatal(m.Rect, blockAt(m, 0, 0, s))
	}
}

func TestSeeThroughPixelsStaySeeThrough(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			a := uint8(128)
			if x < 4 {
				a = 0
			}
			src.SetNRGBA(x, y, color.NRGBA{10, 20, 30, a})
		}
	}
	e, err := EditPicture(encodePNG(t, src), FullCrop(8, 8), 0)
	if err != nil {
		t.Fatal(err)
	}
	if e.Extension != "png" {
		t.Fatal(e.Extension)
	}
	m := decodeAny(t, e.Bytes)
	if m.NRGBAAt(2, 2).A != 0 || m.NRGBAAt(6, 2) != (color.NRGBA{10, 20, 30, 128}) {
		t.Fatal(m.NRGBAAt(2, 2), m.NRGBAAt(6, 2))
	}
}

func TestAnOpaquePictureIsWrittenAsAJPEG(t *testing.T) {
	e, err := EditPicture(redCorner(t, 8, 8), FullCrop(8, 8), 0)
	if err != nil || e.Extension != "jpg" {
		t.Fatal(e.Extension, err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(e.Bytes)); err != nil {
		t.Fatal(err)
	}
}

func TestAPastedPictureIsTurnedAndCroppedLikeAFile(t *testing.T) {
	// The same red corner as the encoded fixture, as straight RGBA.
	const s = 16
	rgba := nrgba(decodeAny(t, redCornerAt(t, 4, 2, s))).Pix
	e, err := EditPasted(rgba, 4*s, 2*s, FullCrop(2*s, 4*s), 1)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeAny(t, e.Bytes)
	if m.Rect.Dx() != 2*s || m.Rect.Dy() != 4*s || !isRed(blockAt(m, 1, 0, s)) {
		t.Fatal(m.Rect, blockAt(m, 1, 0, s))
	}
	if _, err := EditPasted([]byte{0, 0, 0}, 4, 4, FullCrop(4, 4), 0); !errors.Is(err, ErrPicture) {
		t.Fatal(err)
	}
}

func TestWritingACropPutsItUnderItsOwnBytes(t *testing.T) {
	dir := t.TempDir()
	crop := PictureCrop{100, 50, 200, 120}
	e, err := EditPicture(redCorner(t, 800, 600), crop, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := WriteEdited(e, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(p) != ".jpg" {
		t.Fatal(p)
	}
	b, _ := os.ReadFile(p)
	if m := decodeAny(t, b); m.Rect.Dx() != 200 || m.Rect.Dy() != 120 {
		t.Fatal(m.Rect)
	}
	again, err := WriteEdited(e, dir)
	if err != nil || again != p {
		t.Fatal(again, err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Fatal("one file", left)
	}
}

// exifJPEG is upstream's sideways_jpeg: a wide picture whose top-left
// 16 × 16 is red, stored with EXIF orientation o.
func exifJPEG(t *testing.T, o uint16) []byte {
	m := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			c := color.NRGBA{0, 0, 255, 255}
			if x < 16 && y < 16 {
				c = color.NRGBA{255, 0, 0, 255}
			}
			m.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	app1 := append([]byte("Exif\x00\x00"), orientationTIFF(o)...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(2+len(app1)))
	out := append([]byte{0xff, 0xd8}, seg...)
	out = append(out, app1...)
	return append(out, b.Bytes()[2:]...)
}

// orientationTIFF is a little-endian TIFF block with one entry: Orientation.
func orientationTIFF(o uint16) []byte {
	t := []byte("II*\x00\x08\x00\x00\x00\x01\x00")
	t = append(t, 0x12, 0x01, 3, 0, 1, 0, 0, 0)
	t = binary.LittleEndian.AppendUint16(t, o)
	return append(t, 0, 0, 0, 0, 0, 0)
}

func TestAPhotoIsMeasuredAndCroppedUprightByItsEXIFOrientation(t *testing.T) {
	jpg := exifJPEG(t, 6)
	if w, h, err := InspectPicture(jpg); err != nil || w != 32 || h != 64 {
		t.Fatal("upright: tall", w, h, err)
	}
	if w, h, _, err := InspectSticker(jpg); err != nil || w != 32 || h != 64 {
		t.Fatal("decoded upright too", w, h, err)
	}
	// The top-left 16 × 16 of the stored picture is the top-right of the
	// upright one.
	e, err := EditPicture(jpg, PictureCrop{16, 0, 16, 16}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeAny(t, e.Bytes); m.Rect.Dx() != 16 || !isRed(m.NRGBAAt(8, 8)) {
		t.Fatal(m.Rect, m.NRGBAAt(8, 8))
	}
	if orientation(e.Bytes) != 1 {
		t.Fatal("the edit carries no orientation")
	}
	// Turned a further quarter: upright is 64 wide again, red at the bottom
	// right.
	e, err = EditPicture(jpg, FullCrop(64, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeAny(t, e.Bytes); m.Rect.Dx() != 64 || !isRed(m.NRGBAAt(56, 24)) || !isBlue(m.NRGBAAt(8, 8)) {
		t.Fatal(m.Rect, m.NRGBAAt(56, 24))
	}
}

// pngWithEXIF is m as a PNG with an eXIf chunk of orientation o before its
// pixels.
func pngWithEXIF(t *testing.T, m image.Image, o uint16) []byte {
	b := encodePNG(t, m)
	exif := orientationTIFF(o)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(exif)))
	chunk = append(chunk, "eXIf"...)
	chunk = append(chunk, exif...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	at := 8 + 12 + int(binary.BigEndian.Uint32(b[8:])) // after IHDR
	return slices.Concat(b[:at], chunk, b[at:])
}

// TestEveryEXIFOrientation turns a picture of distinct pixels by each of
// the eight orientations (in a PNG, so no pixel is lost) and checks where
// each stored pixel lands.
func TestEveryEXIFOrientation(t *testing.T) {
	const w, h = 3, 2
	stored := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			// Alpha below 255 keeps the edit a PNG: lossless.
			stored.SetNRGBA(x, y, color.NRGBA{uint8(40 * x), uint8(100 * y), 7, 254})
		}
	}
	at := func(x, y int) color.NRGBA { return stored.NRGBAAt(x, y) }
	want := map[uint16]func(x, y int) color.NRGBA{
		1: func(x, y int) color.NRGBA { return at(x, y) },
		2: func(x, y int) color.NRGBA { return at(w-1-x, y) },
		3: func(x, y int) color.NRGBA { return at(w-1-x, h-1-y) },
		4: func(x, y int) color.NRGBA { return at(x, h-1-y) },
		5: func(x, y int) color.NRGBA { return at(y, x) },
		6: func(x, y int) color.NRGBA { return at(y, h-1-x) },
		7: func(x, y int) color.NRGBA { return at(w-1-y, h-1-x) },
		8: func(x, y int) color.NRGBA { return at(w-1-y, x) },
	}
	for o := uint16(1); o <= 8; o++ {
		data := pngWithEXIF(t, stored, o)
		if got := orientation(data); got != int(o) {
			t.Fatalf("orientation %d read as %d", o, got)
		}
		uw, uh := w, h
		if o >= 5 {
			uw, uh = h, w
		}
		if gw, gh, err := InspectPicture(data); err != nil || gw != uw || gh != uh {
			t.Fatalf("orientation %d: %d × %d, %v", o, gw, gh, err)
		}
		e, err := EditPicture(data, FullCrop(uint32(uw), uint32(uh)), 0)
		if err != nil || e.Extension != "png" {
			t.Fatal(e.Extension, err)
		}
		m := decodeAny(t, e.Bytes)
		for y := range uh {
			for x := range uw {
				if got := m.NRGBAAt(x, y); got != want[o](x, y) {
					t.Fatalf("orientation %d, (%d, %d): %v, want %v", o, x, y, got, want[o](x, y))
				}
			}
		}
	}
	// A big-endian block, and nonsense.
	if tiffOrientation([]byte("MM\x00*\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x08\x00\x00")) != 8 {
		t.Fatal("big-endian")
	}
	for _, b := range [][]byte{nil, []byte("II*\x00\xff\xff\xff\xff"), orientationTIFF(9)} {
		if tiffOrientation(b) != 1 {
			t.Fatalf("%q", b)
		}
	}
}

func TestCropsStayInside(t *testing.T) {
	if c := (PictureCrop{700, 500, 300, 300}).Clamped(800, 600); c != (PictureCrop{500, 300, 300, 300}) {
		t.Fatal(c)
	}
	if c := (PictureCrop{0, 0, 0, 9000}).Clamped(10, 20); c != (PictureCrop{0, 0, 1, 20}) {
		t.Fatal(c)
	}
	if c := CenteredSticker(800, 600); c != (StickerCrop{100, 0, 600}) {
		t.Fatal(c)
	}
	if c := (StickerCrop{900, 900, 9000}).Clamped(800, 600); c != (StickerCrop{200, 0, 600}) {
		t.Fatal(c)
	}
}

func needWebP(t *testing.T) {
	t.Helper()
	if err := loadWebP(); err != nil {
		t.Skipf("%v (packaging/macos/libwebp/build.sh)", err)
	}
}

func halfSeeThrough(t *testing.T, w, h int, seeThrough bool) []byte {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if seeThrough && x < w/2 {
				a = 0
			}
			m.SetNRGBA(x, y, color.NRGBA{200, uint8(x % 256), 40, a})
		}
	}
	return encodePNG(t, m)
}

// decodeSticker decodes a WebP as the app shows it, with the system's
// decoder. golang.org/x/image/webp reads VP8's limited-range colour as full
// range (white comes back 235), so it only checks sizes and alpha here.
func decodeSticker(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	m, err := img.Decode(b, 0)
	if errors.Is(err, img.ErrUnsupported) {
		x, err := webp.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return nrgba(x)
	}
	if err != nil {
		t.Fatal(err)
	}
	return nrgba(m)
}

func chunkIDs(webp []byte) []string {
	var ids []string
	for _, c := range riffChunks(webp) {
		ids = append(ids, c.id)
	}
	return ids
}

// The sticker tests are upstream's sticker_maker.rs tests.

func TestAPictureBecomesAStill512PixelStickerWithItsEmojis(t *testing.T) {
	needWebP(t)
	src := halfSeeThrough(t, 800, 600, true)
	if w, h, tr, err := InspectSticker(src); err != nil || w != 800 || h != 600 || !tr {
		t.Fatal(w, h, tr, err)
	}
	crop := CenteredSticker(800, 600)
	sticker, err := MakeSticker(src, crop, true, []string{"🎨"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sticker) > StickerLimit {
		t.Fatal(len(sticker))
	}
	if ids := chunkIDs(sticker); slices.Contains(ids, "ANIM") || !slices.Equal(ids, []string{"VP8X", "ALPH", "VP8 ", "EXIF"}) {
		t.Fatal("a still sticker with alpha and metadata:", ids)
	}
	if _, err := webp.Decode(bytes.NewReader(sticker)); err != nil {
		t.Fatal(err)
	}
	n := decodeSticker(t, sticker)
	if n.Rect.Dx() != StickerSide || n.Rect.Dy() != StickerSide {
		t.Fatal(n.Rect)
	}
	if n.NRGBAAt(5, 256).A >= 16 {
		t.Fatal("the see-through half stays", n.NRGBAAt(5, 256))
	}
	if n.NRGBAAt(500, 256).A != 255 {
		t.Fatal(n.NRGBAAt(500, 256))
	}
	if e := StickerEmojis(sticker); !slices.Equal(e, []string{"🎨"}) {
		t.Fatal(e)
	}
	if info, _ := ReadStickerInfo(sticker); info.PackID != "zapfast" || info.PackName != "ZapFast" {
		t.Fatal(info)
	}
}

func TestWithoutTransparencyTheBackgroundTurnsWhite(t *testing.T) {
	needWebP(t)
	sticker, err := MakeSticker(halfSeeThrough(t, 64, 64, true), CenteredSticker(64, 64), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := decodeSticker(t, sticker).NRGBAAt(5, 256)
	if c.A != 255 || c.R < 240 || c.G < 240 || c.B < 240 {
		t.Fatal(c)
	}
	if _, _, tr, _ := InspectSticker(halfSeeThrough(t, 8, 8, false)); tr {
		t.Fatal("opaque")
	}
}

// TestANoisyPhotoStillFits: a picture libwebp cannot squeeze at the first
// qualities is tried lower until it fits WhatsApp's limit.
func TestANoisyPhotoStillFits(t *testing.T) {
	needWebP(t)
	r := rand.New(rand.NewPCG(1, 2))
	m := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for i := range m.Pix {
		m.Pix[i] = uint8(r.IntN(256))
	}
	for i := 3; i < len(m.Pix); i += 4 {
		m.Pix[i] = 255
	}
	first, err := EncodeWebP(image.NewNRGBA(image.Rect(0, 0, 1, 1)), 85)
	if err != nil || len(first) == 0 {
		t.Fatal(err)
	}
	if q85, _ := EncodeWebP(m.SubImage(image.Rect(0, 0, 512, 512)), 85); len(q85) <= StickerLimit {
		t.Fatalf("the fixture is not noisy enough: %d bytes at 85", len(q85))
	}
	sticker, err := MakeSticker(encodePNG(t, m), StickerCrop{0, 0, 512}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sticker) > StickerLimit {
		t.Fatal(len(sticker))
	}
}

func TestAnAnimatedSticker(t *testing.T) {
	needWebP(t)
	var frames []WebPFrame
	for i := range 3 {
		m := image.NewNRGBA(image.Rect(0, 0, StickerSide, StickerSide))
		for p := 0; p < len(m.Pix); p += 4 {
			copy(m.Pix[p:], []byte{uint8(80 * i), 100, 200, 255})
		}
		frames = append(frames, WebPFrame{Image: m, Duration: 150 * time.Millisecond})
	}
	out, err := EncodeAnimatedWebP(frames, 70)
	if err != nil {
		t.Fatal(err)
	}
	ids := chunkIDs(out)
	if !slices.Contains(ids, "ANIM") || len(ids) < 4 || len(out) > StickerLimit*5 {
		t.Fatal(ids, len(out))
	}
	tagged, err := WriteStickerInfo(out, StickerInfo{Emojis: []string{"🐸"}})
	if err != nil || !slices.Equal(StickerEmojis(tagged), []string{"🐸"}) {
		t.Fatal(err)
	}
	if _, err := EncodeAnimatedWebP([]WebPFrame{frames[0], {Image: image.NewNRGBA(image.Rect(0, 0, 8, 8))}}, 70); err == nil {
		t.Fatal("frames of two sizes")
	}
	if _, err := EncodeAnimatedWebP(nil, 70); err == nil {
		t.Fatal("no frames")
	}
	if _, err := EncodeWebP(frames[0].Image, 101); err == nil {
		t.Fatal("quality 101")
	}
}

// The metadata tests are upstream's sticker_meta.rs tests.

func TestStickerMetadata(t *testing.T) {
	needWebP(t)
	plain, err := EncodeWebP(image.NewNRGBA(image.Rect(0, 0, 7, 5)), 80)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStickerInfo(plain); ok {
		t.Fatal("no metadata yet")
	}
	info := StickerInfo{PackID: "zapfast.test", PackName: "Ducks", Emojis: []string{"🦆", "😂"}}
	tagged, err := WriteStickerInfo(plain, info)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := ReadStickerInfo(tagged); !ok || got.PackID != info.PackID || !slices.Equal(got.Emojis, info.Emojis) {
		t.Fatal(got, ok)
	}
	m, err := webp.Decode(bytes.NewReader(tagged))
	if err != nil || m.Bounds().Dx() != 7 || m.Bounds().Dy() != 5 {
		t.Fatal("still a picture", err)
	}
	if nrgba(m).NRGBAAt(0, 0).A != 0 {
		t.Fatal("alpha survives")
	}
	// Writing again replaces the old metadata.
	again, err := WriteStickerInfo(tagged, StickerInfo{Emojis: []string{"🐸"}})
	if err != nil || !slices.Equal(StickerEmojis(again), []string{"🐸"}) {
		t.Fatal(err)
	}
	exifs := 0
	for _, id := range chunkIDs(again) {
		if id == "EXIF" {
			exifs++
		}
	}
	if exifs != 1 {
		t.Fatal(chunkIDs(again))
	}
	if _, err := webp.Decode(bytes.NewReader(again)); err != nil {
		t.Fatal(err)
	}
	// The JSON is serde's: field order, and emojis unescaped.
	c := riffChunks(again)[chunkIndex(riffChunks(again), "EXIF")]
	if !bytes.HasSuffix(c.data, []byte(`{"emojis":["🐸"]}`)) || binary.LittleEndian.Uint32(c.data[18:]) != 22 {
		t.Fatalf("%q", c.data)
	}
}

func TestWhatsAppsOwnLayoutIsRead(t *testing.T) {
	file := appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "EXIF", tiffBlock([]byte(`{"sticker-pack-id":"x","emojis":["❤"]}`)))
	if e := StickerEmojis(file); !slices.Equal(e, []string{"❤"}) {
		t.Fatal(e)
	}
	// JSON without a TIFF wrapper.
	bare := appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "EXIF", []byte(`junk{"emojis":[" 😂 ",""]}`))
	if e := StickerEmojis(bare); !slices.Equal(e, []string{"😂"}) {
		t.Fatal(e)
	}
	if _, ok := ReadStickerInfo([]byte("not a webp")); ok {
		t.Fatal("not a webp")
	}
	if _, err := WriteStickerInfo([]byte("not a webp"), StickerInfo{}); !errors.Is(err, ErrPicture) {
		t.Fatal(err)
	}
}

// TestAMissingLibraryIsATypedError: no library, bundled or not, is an
// UnavailableError that says where it looked, never a crash.
func TestAMissingLibraryIsATypedError(t *testing.T) {
	none := func(string) string { return "" }
	dir := t.TempDir()
	_, err := findWebP(filepath.Join(dir, "zapfast"), dir, none)
	var ue *UnavailableError
	if !errors.As(err, &ue) || !errors.Is(err, ErrUnavailable) || !errors.Is(err, errors.ErrUnsupported) || len(ue.Searched) == 0 {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "Zap.app/Contents/MacOS/zapfast")
	if _, err := findWebP(app, dir, func(string) string { return "/elsewhere/libzfwebp.dylib" }); !errors.As(err, &ue) ||
		ue.Path != filepath.Join(dir, "Zap.app/Contents/Frameworks", WebPLibrary) {
		t.Fatal("a bundle loads only its own", err)
	}
	if _, err := findWebP("", dir, func(string) string { return filepath.Join(dir, "gone.dylib") }); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, webpDevDir, WebPLibrary)
	os.MkdirAll(filepath.Dir(lib), 0o755)
	os.WriteFile(lib, nil, 0o644)
	if got, err := findWebP("", filepath.Join(dir, "a/b"), none); err != nil || got != lib {
		t.Fatal("found above the working directory", got, err)
	}
	if !errors.Is(&UnavailableError{What: "Opus"}, ErrUnavailable) {
		t.Fatal("Opus")
	}
}
