package av

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// WhatsApp's stickers.
const (
	// StickerSide is a sticker's width and height, in pixels.
	StickerSide = 512
	// StickerLimit is the largest still sticker WhatsApp takes.
	StickerLimit = 100 * 1024
)

// stickerQualities are the WebP qualities a sticker is tried at, best
// first: upstream's, and one lower step so that it always fits.
var stickerQualities = [...]float32{85, 70, 55, 40, 25, 10}

// StickerCrop is the square of a picture a sticker is made of. Its fields
// are abi.StickerCrop's, so each converts to the other.
type StickerCrop struct {
	X, Y, Side uint32
}

// CenteredSticker is the largest square in the middle of a picture.
func CenteredSticker(width, height uint32) StickerCrop {
	side := max(min(width, height), 1)
	return StickerCrop{X: (width - min(side, width)) / 2, Y: (height - min(side, height)) / 2, Side: side}
}

// Clamped is the same square kept inside a picture of this size, as
// upstream's StickerCrop::clamped.
func (c StickerCrop) Clamped(width, height uint32) StickerCrop {
	side := min(max(c.Side, 1), max(min(width, height), 1))
	return StickerCrop{X: min(c.X, width-min(side, width)), Y: min(c.Y, height-min(side, height)), Side: side}
}

// InspectSticker is a picture's size upright, as InspectPicture gives it,
// and whether any of its pixels is see-through: the StickerPicture event.
// It decodes the picture.
func InspectSticker(data []byte) (width, height int, transparent bool, err error) {
	m, err := decodeUpright(data)
	if err != nil {
		return 0, 0, false, err
	}
	return m.Rect.Dx(), m.Rect.Dy(), seeThrough(m), nil
}

// MakeSticker is upstream's sticker maker: the square crop of the upright
// picture, scaled to StickerSide with a Lanczos filter, its see-through
// parts kept (transparent) or put on white, encoded as a still lossy WebP at
// the best quality that fits StickerLimit, with emojis in its metadata.
// Without libzfwebp.dylib it fails with ErrUnavailable.
func MakeSticker(data []byte, crop StickerCrop, transparent bool, emojis []string) ([]byte, error) {
	m, err := decodeUpright(data)
	if err != nil {
		return nil, err
	}
	c := crop.Clamped(uint32(m.Rect.Dx()), uint32(m.Rect.Dy()))
	sticker := image.NewNRGBA(image.Rect(0, 0, StickerSide, StickerSide))
	lanczos3.Scale(sticker, sticker.Rect, m, image.Rect(int(c.X), int(c.Y), int(c.X+c.Side), int(c.Y+c.Side)), xdraw.Src, nil)
	if !transparent {
		onWhite(sticker)
	}
	webp, err := encodeSticker(sticker)
	if err != nil {
		return nil, err
	}
	tagged, err := WriteStickerInfo(webp, StickerInfo{PackID: "zapfast", PackName: "ZapFast", Emojis: emojis})
	if err != nil {
		return webp, nil
	}
	return tagged, nil
}

// encodeSticker is the sticker at the first quality that fits.
func encodeSticker(m *image.NRGBA) ([]byte, error) {
	for _, q := range stickerQualities {
		webp, err := EncodeWebP(m, q)
		if err != nil {
			return nil, err
		}
		if len(webp) <= StickerLimit {
			return webp, nil
		}
	}
	return nil, fmt.Errorf("%w: the sticker does not fit %d KiB", ErrTooLarge, StickerLimit/1024)
}

// onWhite puts a picture's see-through parts on white, as upstream does.
func onWhite(m *image.NRGBA) {
	for i := 0; i < len(m.Pix); i += 4 {
		a := float32(m.Pix[i+3]) / 255
		for c := range 3 {
			m.Pix[i+c] = byte(math.Round(float64(float32(m.Pix[i+c])*a + 255*(1-a))))
		}
		m.Pix[i+3] = 0xff
	}
}

// lanczos3 is upstream's resize filter (image's FilterType::Lanczos3).
var lanczos3 = &xdraw.Kernel{Support: 3, At: func(t float64) float64 {
	if t == 0 {
		return 1
	}
	if t >= 3 {
		return 0
	}
	x := math.Pi * t
	return 3 * math.Sin(x) * math.Sin(x/3) / (x * x)
}}

// StickerInfo is the JSON WhatsApp keeps in a sticker's EXIF chunk, under
// TIFF tag 0x5741: WhatsApp reads the emojis to suggest stickers.
type StickerInfo struct {
	PackID    string   `json:"sticker-pack-id,omitempty"`
	PackName  string   `json:"sticker-pack-name,omitempty"`
	Publisher string   `json:"sticker-pack-publisher,omitempty"`
	Emojis    []string `json:"emojis,omitempty"`
}

// stickerTag is the TIFF tag WhatsApp stores its sticker JSON under.
const stickerTag = 0x5741

type riffChunk struct {
	id   string
	data []byte
}

// riffChunks splits a WebP file into its chunks; nil when it is not one.
func riffChunks(b []byte) []riffChunk {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil
	}
	var out []riffChunk
	for at := 12; at+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[at+4:]))
		end := at + 8 + size
		if size < 0 || end > len(b) || end < at {
			return nil
		}
		out = append(out, riffChunk{string(b[at : at+4]), b[at+8 : end]})
		at = end + size&1 // chunks are padded to an even size
	}
	return out
}

// ReadStickerInfo is the sticker JSON of a WebP file, if it has any. Like
// upstream it also takes JSON written without a TIFF wrapper.
func ReadStickerInfo(webp []byte) (StickerInfo, bool) {
	for _, c := range riffChunks(webp) {
		if c.id != "EXIF" {
			continue
		}
		if v, _, ok := tiffEntry(c.data, stickerTag); ok {
			if info, ok := jsonObject(v); ok {
				return info, true
			}
		}
		return jsonObject(c.data)
	}
	return StickerInfo{}, false
}

func jsonObject(b []byte) (StickerInfo, bool) {
	start, end := bytes.IndexByte(b, '{'), bytes.LastIndexByte(b, '}')
	if start < 0 || end < start {
		return StickerInfo{}, false
	}
	var info StickerInfo
	if json.Unmarshal(b[start:end+1], &info) != nil {
		return StickerInfo{}, false
	}
	return info, true
}

// StickerEmojis are the emojis a sticker file is associated with, trimmed,
// empty ones dropped.
func StickerEmojis(webp []byte) []string {
	info, _ := ReadStickerInfo(webp)
	var out []string
	for _, e := range info.Emojis {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// WriteStickerInfo is the WebP with info as its sticker metadata, replacing
// any EXIF chunk it had, laid out as WhatsApp's own stickers are. A simple
// WebP becomes an extended one, since only that layout carries metadata.
func WriteStickerInfo(webp []byte, info StickerInfo) ([]byte, error) {
	chunks := riffChunks(webp)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("%w: not a WebP", ErrPicture)
	}
	var js bytes.Buffer
	enc := json.NewEncoder(&js)
	enc.SetEscapeHTML(false) // as serde_json writes it
	if err := enc.Encode(info); err != nil {
		return nil, err
	}
	exif := tiffBlock(bytes.TrimSuffix(js.Bytes(), []byte("\n")))

	var body []byte
	if i := chunkIndex(chunks, "VP8X"); i >= 0 {
		header := append([]byte(nil), chunks[i].data...)
		if len(header) < 10 {
			return nil, fmt.Errorf("%w: a damaged WebP header", ErrPicture)
		}
		header[0] |= 0x08 // an EXIF chunk follows
		body = appendChunk(body, "VP8X", header)
		for _, c := range chunks {
			if c.id != "VP8X" && c.id != "EXIF" {
				body = appendChunk(body, c.id, c.data)
			}
		}
	} else {
		w, h, alpha, ok := simpleCanvas(chunks[0])
		if !ok {
			return nil, fmt.Errorf("%w: a WebP that is neither VP8 nor VP8L", ErrPicture)
		}
		header := make([]byte, 10)
		header[0] = 0x08
		if alpha {
			header[0] |= 0x10
		}
		put24(header[4:], w-1)
		put24(header[7:], h-1)
		body = appendChunk(body, "VP8X", header)
		body = appendChunk(body, chunks[0].id, chunks[0].data)
	}
	body = appendChunk(body, "EXIF", exif)
	out := make([]byte, 0, 12+len(body))
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(4+len(body)))
	out = append(out, "WEBP"...)
	return append(out, body...), nil
}

func chunkIndex(chunks []riffChunk, id string) int {
	for i, c := range chunks {
		if c.id == id {
			return i
		}
	}
	return -1
}

func appendChunk(out []byte, id string, data []byte) []byte {
	out = append(out, id...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, data...)
	if len(data)&1 == 1 {
		out = append(out, 0)
	}
	return out
}

func put24(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }

// tiffBlock is a little-endian TIFF block with the sticker JSON as its only
// entry, the JSON right after the entry at offset 22 with no next-directory
// pointer, as WhatsApp's own stickers have it.
func tiffBlock(js []byte) []byte {
	t := make([]byte, 0, 22+len(js))
	t = append(t, "II*\x00"...)
	t = binary.LittleEndian.AppendUint32(t, 8)
	t = binary.LittleEndian.AppendUint16(t, 1)
	t = binary.LittleEndian.AppendUint16(t, stickerTag)
	t = binary.LittleEndian.AppendUint16(t, 7) // UNDEFINED: raw bytes
	t = binary.LittleEndian.AppendUint32(t, uint32(len(js)))
	t = binary.LittleEndian.AppendUint32(t, 22)
	return append(t, js...)
}

// simpleCanvas is the size and alpha flag of a simple (VP8 or VP8L) WebP.
func simpleCanvas(c riffChunk) (w, h int, alpha, ok bool) {
	d := c.data
	switch c.id {
	case "VP8 ":
		if len(d) < 10 || d[3] != 0x9d || d[4] != 0x01 || d[5] != 0x2a {
			return 0, 0, false, false
		}
		return int(binary.LittleEndian.Uint16(d[6:]) & 0x3fff), int(binary.LittleEndian.Uint16(d[8:]) & 0x3fff), false, true
	case "VP8L":
		if len(d) < 5 || d[0] != 0x2f {
			return 0, 0, false, false
		}
		bits := binary.LittleEndian.Uint32(d[1:])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, bits>>28&1 == 1, true
	}
	return 0, 0, false, false
}
