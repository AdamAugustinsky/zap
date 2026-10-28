package cropper

// Edge is the part of a crop that is being dragged: an edge moves one side, a
// corner two, Inside moves the whole region (zf:src/model.rs CropEdge).
type Edge uint8

const (
	Inside Edge = iota
	Left
	Right
	Top
	Bottom
	TopLeft
	TopRight
	BottomLeft
	BottomRight
)

func (e Edge) movesLeft() bool   { return e == Left || e == TopLeft || e == BottomLeft }
func (e Edge) movesRight() bool  { return e == Right || e == TopRight || e == BottomRight }
func (e Edge) movesTop() bool    { return e == Top || e == TopLeft || e == TopRight }
func (e Edge) movesBottom() bool { return e == Bottom || e == BottomLeft || e == BottomRight }

// MinSide is the smallest crop side, so a drag cannot close the region to
// nothing (zf:src/model.rs CROP_MIN_SIDE).
const MinSide = 16

// Crop is a region of a picture to keep, in its pixels, as the picture is
// shown (zf:src/model.rs PictureCrop).
type Crop struct{ X, Y, W, H uint32 }

// Full is the whole picture.
func Full(w, h uint32) Crop { return Crop{0, 0, max(w, 1), max(h, 1)} }

// Clamped is the same region kept inside a picture of this size.
func (c Crop) Clamped(w, h uint32) Crop {
	w, h = max(w, 1), max(h, 1)
	cw, ch := min(max(c.W, 1), w), min(max(c.H, 1), h)
	return Crop{min(c.X, w-cw), min(c.Y, h-ch), cw, ch}
}

// Moved is the region moved by whole pixels, staying inside the picture.
func (c Crop) Moved(dx, dy int64, w, h uint32) Crop {
	shift := func(at uint32, by int64) uint32 { return uint32(max(int64(at)+by, 0)) }
	c.X, c.Y = shift(c.X, dx), shift(c.Y, dy)
	return c.Clamped(w, h)
}

// Drag is the region after the edge or corner is dragged by whole pixels.
func (c Crop) Drag(e Edge, dx, dy int64, w, h uint32) Crop {
	pw, ph := int64(max(w, 1)), int64(max(h, 1))
	if e == Inside {
		return c.Moved(dx, dy, uint32(pw), uint32(ph))
	}
	left, top := int64(c.X), int64(c.Y)
	right, bottom := left+int64(c.W), top+int64(c.H)
	if e.movesLeft() {
		left += dx
	}
	if e.movesRight() {
		right += dx
	}
	if e.movesTop() {
		top += dy
	}
	if e.movesBottom() {
		bottom += dy
	}
	left, right = min(max(left, 0), pw), min(max(right, 0), pw)
	top, bottom = min(max(top, 0), ph), min(max(bottom, 0), ph)
	// A moving edge gives way to the fixed one, so the region keeps a usable
	// size without the picture's own edge being pushed out.
	least := min(int64(MinSide), pw, ph)
	if e.movesLeft() {
		left = max(min(left, right-least), 0)
	}
	if e.movesRight() {
		right = min(max(right, left+least), pw)
	}
	if e.movesTop() {
		top = max(min(top, bottom-least), 0)
	}
	if e.movesBottom() {
		bottom = min(max(bottom, top+least), ph)
	}
	return Crop{uint32(left), uint32(top), uint32(max(right-left, 1)), uint32(max(bottom-top, 1))}
}

// TurnedClockwise is the same content after the picture is turned a quarter
// clockwise: the picture's former height becomes its width.
func (c Crop) TurnedClockwise(oldH uint32) Crop {
	return Crop{sub(oldH, c.Y+c.H), c.X, c.H, c.W}
}

// TurnedCounterClockwise is the same content after a quarter turn the other
// way.
func (c Crop) TurnedCounterClockwise(oldW uint32) Crop {
	return Crop{c.Y, sub(oldW, c.X+c.W), c.H, c.W}
}

func sub(a, b uint32) uint32 {
	if b > a {
		return 0
	}
	return a - b
}

// Edit is a picture on its way to being cropped and turned
// (zf:src/model.rs PictureEdit, less the source and the target, which the
// screen keeps).
type Edit struct {
	// W and H are the source picture's own size, before any turn.
	W, H uint32
	Crop Crop
	// Turns are the quarter turns clockwise applied to the source.
	Turns uint8
}

// NewEdit is a whole, unturned picture.
func NewEdit(w, h uint32) Edit {
	w, h = max(w, 1), max(h, 1)
	return Edit{W: w, H: h, Crop: Full(w, h)}
}

// DisplayedSize is the source turned to the side the crop is measured in.
func (e Edit) DisplayedSize() (w, h uint32) {
	if e.Turns%2 == 0 {
		return e.W, e.H
	}
	return e.H, e.W
}

// Turned turns the picture, carrying the selection round with it.
func (e Edit) Turned(clockwise bool) Edit {
	w, h := e.DisplayedSize()
	if clockwise {
		e.Crop, e.Turns = e.Crop.TurnedClockwise(h), (e.Turns+1)%4
	} else {
		e.Crop, e.Turns = e.Crop.TurnedCounterClockwise(w), (e.Turns+3)%4
	}
	return e
}

// Reset puts the picture back the way it started: no turn and nothing
// cropped away.
func (e Edit) Reset() Edit {
	e.Crop, e.Turns = Full(e.W, e.H), 0
	return e
}
