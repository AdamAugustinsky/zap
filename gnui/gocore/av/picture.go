package av

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // decoders the picture functions read
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"gonativeui/img"
)

// The largest picture decoded: gonativeui/img's limits.
const (
	maxPixels = 100_000_000
	maxSide   = 16384
)

// editQuality is the JPEG quality of an opaque edited picture, upstream's.
// The send path passes a JPEG through untouched, so this is its only lossy
// step.
const editQuality = 92

// PictureCrop is a region of a picture, in pixels. Its fields are
// abi.PictureCrop's, so each converts to the other.
type PictureCrop struct {
	X, Y, Width, Height uint32
}

// FullCrop is the whole of a picture of this size.
func FullCrop(width, height uint32) PictureCrop {
	return PictureCrop{Width: max(width, 1), Height: max(height, 1)}
}

// Clamped is the same region kept inside a picture of this size, as
// upstream's PictureCrop::clamped.
func (c PictureCrop) Clamped(width, height uint32) PictureCrop {
	width, height = max(width, 1), max(height, 1)
	cw, ch := min(max(c.Width, 1), width), min(max(c.Height, 1), height)
	return PictureCrop{X: min(c.X, width-cw), Y: min(c.Y, height-ch), Width: cw, Height: ch}
}

// InspectPicture is a picture's size upright: turned by its EXIF
// orientation, as the system's decoder shows it (a photo stored sideways
// with orientation 6 is reported tall). The formats the standard library and
// golang.org/x/image read are measured from their header, without decoding
// pixels; anything else (HEIC) is decoded by the system.
func InspectPicture(data []byte) (width, height int, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		m, err := systemDecode(data)
		if err != nil {
			return 0, 0, err
		}
		return m.Rect.Dx(), m.Rect.Dy(), nil
	}
	if cfg.Width < 1 || cfg.Height < 1 {
		return 0, 0, fmt.Errorf("%w: no pixels", ErrPicture)
	}
	if o := orientation(data); o >= 5 && o <= 8 {
		return cfg.Height, cfg.Width, nil
	}
	return cfg.Width, cfg.Height, nil
}

// decodeUpright decodes the first frame of a picture into straight-alpha
// RGBA, turned upright by its EXIF orientation.
func decodeUpright(data []byte) (*image.NRGBA, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return systemDecode(data)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxSide || cfg.Height > maxSide ||
		int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("%w: a picture of %d × %d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// An animated WebP, say: its header reads, its frames do not.
		if m, serr := systemDecode(data); serr == nil {
			return m, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrPicture, err)
	}
	return nrgba(img.Orient(m, orientation(data))), nil
}

// systemDecode decodes what the standard library does not read with the
// system's decoder (ImageIO on macOS), which turns it upright itself.
func systemDecode(data []byte) (*image.NRGBA, error) {
	m, err := img.Decode(data, 0)
	switch {
	case errors.Is(err, img.ErrTooLarge):
		return nil, fmt.Errorf("%w: %v", ErrTooLarge, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrPicture, err)
	}
	return nrgba(m), nil
}

// nrgba is m as straight-alpha RGBA, its bounds at the origin.
func nrgba(m image.Image) *image.NRGBA {
	if n, ok := m.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := m.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Rect, m, b.Min, draw.Src)
	return n
}

// seeThrough reports whether any pixel of m is less than opaque.
func seeThrough(m *image.NRGBA) bool {
	for y := range m.Rect.Dy() {
		row := m.Pix[y*m.Stride : y*m.Stride+4*m.Rect.Dx()]
		for i := 3; i < len(row); i += 4 {
			if row[i] < 0xff {
				return true
			}
		}
	}
	return false
}

// Edited is an edited picture, encoded, and the extension of its format:
// "png" when any pixel is see-through, else "jpg".
type Edited struct {
	Bytes     []byte
	Extension string
}

// EditPicture is ApplyPictureEdit on a file's bytes: the picture turned
// upright by its EXIF orientation, then turns quarter turns clockwise, then
// cropped to crop, which is measured in the picture after both turns (and
// kept inside it). The result carries no orientation of its own.
func EditPicture(data []byte, crop PictureCrop, turns int) (Edited, error) {
	m, err := decodeUpright(data)
	if err != nil {
		return Edited{}, err
	}
	return turnedAndCropped(m, crop, turns)
}

// EditPasted is EditPicture on a pasted picture: straight RGBA rows of
// width × height, which has no orientation to apply.
func EditPasted(rgba []byte, width, height int, crop PictureCrop, turns int) (Edited, error) {
	width, height = max(width, 1), max(height, 1)
	if int64(width)*int64(height) > maxPixels || len(rgba) < 4*width*height {
		return Edited{}, fmt.Errorf("%w: not %d × %d RGBA", ErrPicture, width, height)
	}
	m := &image.NRGBA{Pix: rgba[:4*width*height], Stride: 4 * width, Rect: image.Rect(0, 0, width, height)}
	return turnedAndCropped(m, crop, turns)
}

// quarterTurns are the EXIF orientations of 0 to 3 quarter turns clockwise.
var quarterTurns = [4]int{1, 6, 3, 8}

func turnedAndCropped(m *image.NRGBA, crop PictureCrop, turns int) (Edited, error) {
	m = nrgba(img.Orient(m, quarterTurns[(turns%4+4)%4]))
	c := crop.Clamped(uint32(m.Rect.Dx()), uint32(m.Rect.Dy()))
	r := image.Rect(int(c.X), int(c.Y), int(c.X+c.Width), int(c.Y+c.Height))
	kept := nrgba(m.SubImage(r))
	var b bytes.Buffer
	if seeThrough(kept) {
		if err := png.Encode(&b, kept); err != nil {
			return Edited{}, fmt.Errorf("av: could not write the picture: %w", err)
		}
		return Edited{Bytes: b.Bytes(), Extension: "png"}, nil
	}
	if err := jpeg.Encode(&b, kept, &jpeg.Options{Quality: editQuality}); err != nil {
		return Edited{}, fmt.Errorf("av: could not write the picture: %w", err)
	}
	return Edited{Bytes: b.Bytes(), Extension: "jpg"}, nil
}

// WriteEdited writes e in dir, named by the SHA-256 of its bytes, so the
// same edit of the same picture is written once, and returns its path.
func WriteEdited(e Edited, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256(e.Bytes)
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+"."+e.Extension)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, ".edit-*")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(e.Bytes)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

// orientation is a picture's EXIF orientation, 1 to 8 (1 without one): from
// a JPEG's APP1 segment, a PNG's eXIf chunk, a WebP's EXIF chunk, or a
// TIFF's first directory. These are the formats upstream's decoder reads it
// from.
func orientation(data []byte) int {
	switch {
	case len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8:
		return img.Orientation(data)
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		for at := 8; at+12 <= len(data); {
			n := int(binary.BigEndian.Uint32(data[at:]))
			kind := string(data[at+4 : at+8])
			if n < 0 || at+12+n > len(data) || kind == "IDAT" || kind == "IEND" {
				break
			}
			if kind == "eXIf" {
				return tiffOrientation(data[at+8 : at+8+n])
			}
			at += 12 + n
		}
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		for _, c := range riffChunks(data) {
			if c.id == "EXIF" {
				return tiffOrientation(bytes.TrimPrefix(c.data, []byte("Exif\x00\x00")))
			}
		}
	case len(data) >= 4 && (string(data[:4]) == "II*\x00" || string(data[:4]) == "MM\x00*"):
		return tiffOrientation(data)
	}
	return 1
}

// tiffOrientation is tag 0x0112 of a TIFF block's first directory, 1 to 8.
func tiffOrientation(t []byte) int {
	v, typ, ok := tiffEntry(t, 0x0112)
	if !ok || typ != 3 || len(v) < 2 {
		return 1
	}
	o := int(tiffOrder(t).Uint16(v))
	if o < 1 || o > 8 {
		return 1
	}
	return o
}

func tiffOrder(t []byte) binary.ByteOrder {
	if len(t) >= 2 && string(t[:2]) == "MM" {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// tiffEntry is the value and type of tag in the first directory of a
// little- or big-endian TIFF block.
func tiffEntry(t []byte, tag uint16) (value []byte, typ uint16, ok bool) {
	if len(t) < 8 || (string(t[:2]) != "II" && string(t[:2]) != "MM") {
		return nil, 0, false
	}
	bo := tiffOrder(t)
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 0 || ifd+2 > len(t) {
		return nil, 0, false
	}
	count := int(bo.Uint16(t[ifd:]))
	for i := range count {
		e := ifd + 2 + 12*i
		if e+12 > len(t) {
			return nil, 0, false
		}
		if bo.Uint16(t[e:]) != tag {
			continue
		}
		typ = bo.Uint16(t[e+2:])
		size := 1
		if int(typ) < len(tiffSizes) {
			size = max(tiffSizes[typ], 1)
		}
		n := int(bo.Uint32(t[e+4:])) * size
		if n < 0 {
			return nil, 0, false
		}
		if n <= 4 {
			return t[e+8 : e+8+n], typ, true
		}
		off := int(bo.Uint32(t[e+8:]))
		if off < 0 || off+n > len(t) || off+n < off {
			return nil, 0, false
		}
		return t[off : off+n], typ, true
	}
	return nil, 0, false
}

// tiffSizes are the bytes of one value of each TIFF type.
var tiffSizes = [...]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8}
