package stickermaker

import "gonativeui/text"

// Square is the part of a picture that becomes the sticker: a square region
// in the picture's pixels (zf:src/model.rs StickerCrop).
type Square struct{ X, Y, Side uint32 }

// Centered is the largest square in the middle of a w × h picture.
func Centered(w, h uint32) Square {
	side := max(min(w, h), 1)
	return Square{X: sub(w, side) / 2, Y: sub(h, side) / 2, Side: side}
}

// Clamped is the same square kept inside a w × h picture.
func (q Square) Clamped(w, h uint32) Square {
	side := min(max(q.Side, 1), max(min(w, h), 1))
	return Square{min(q.X, sub(w, side)), min(q.Y, sub(h, side)), side}
}

// Moved is the square moved by whole pixels, staying inside the picture.
func (q Square) Moved(dx, dy int64, w, h uint32) Square {
	shift := func(at uint32, by int64) uint32 { return uint32(max(int64(at)+by, 0)) }
	q.X, q.Y = shift(q.X, dx), shift(q.Y, dy)
	return q.Clamped(w, h)
}

// Resized is the square resized around its centre, staying inside the
// picture.
func (q Square) Resized(side, w, h uint32) Square {
	centre := func(at uint32) int64 { return int64(at) + int64(q.Side)/2 }
	half := int64(side) / 2
	return Square{
		uint32(max(centre(q.X)-half, 0)),
		uint32(max(centre(q.Y)-half, 0)),
		side,
	}.Clamped(w, h)
}

func sub(a, b uint32) uint32 {
	if b > a {
		return 0
	}
	return a - b
}

// Smallest is the smallest side the Size slider offers for a w × h picture: an
// eighth of the shorter side.
func Smallest(w, h uint32) uint32 { return max(max(min(w, h), 1)/8, 1) }

// CleanEmojis keeps the emojis of text only, one per entry, in the order
// typed, dropping other text and repeats (zf:src/sticker_meta.rs
// clean_emojis).
func CleanEmojis(typed []byte) [][]byte {
	var out [][]byte
	for off := 0; off < len(typed); {
		next := text.NextGrapheme(typed, off)
		if next <= off {
			break
		}
		g := typed[off:next]
		off = next
		if text.EmojiOnly(g) != 1 {
			continue
		}
		dup := false
		for _, seen := range out {
			if string(seen) == string(g) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, append([]byte(nil), g...))
		}
	}
	return out
}
