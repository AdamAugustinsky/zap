package cropper

import "testing"

func TestCropStaysInsideThePicture(t *testing.T) {
	if got := Full(800, 600); got != (Crop{0, 0, 800, 600}) {
		t.Fatalf("Full = %+v", got)
	}
	// A region larger than the picture shrinks to it and starts at the corner.
	if got := (Crop{900, 900, 4000, 4000}).Clamped(800, 600); got != Full(800, 600) {
		t.Errorf("huge = %+v", got)
	}
	// A region hanging off an edge slides back, keeping its size.
	if got := (Crop{700, 500, 400, 300}).Clamped(800, 600); got != (Crop{400, 300, 400, 300}) {
		t.Errorf("off = %+v", got)
	}
}

func TestDragAnEdgeMovesThatSideOnly(t *testing.T) {
	full := Full(800, 600)
	for _, c := range []struct {
		name   string
		edge   Edge
		dx, dy int64
		want   Crop
	}{
		{"left", Left, 100, 0, Crop{100, 0, 700, 600}},
		{"top left", TopLeft, 100, 50, Crop{100, 50, 700, 550}},
		{"past the picture's edge", Left, -100, 0, full},
		{"past the far side", Left, 1000, 0, Crop{800 - MinSide, 0, MinSide, 600}},
		{"bottom right in", BottomRight, -1000, -1000, Crop{0, 0, MinSide, MinSide}},
	} {
		if got := full.Drag(c.edge, c.dx, c.dy, 800, 600); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestDragInsideMovesWithoutResizing(t *testing.T) {
	c := Crop{100, 100, 200, 200}
	if got := c.Drag(Inside, 50, 25, 800, 600); got != (Crop{150, 125, 200, 200}) {
		t.Errorf("moved = %+v", got)
	}
	if got := c.Drag(Inside, 1000, 1000, 800, 600); got != (Crop{600, 400, 200, 200}) {
		t.Errorf("pushed = %+v", got)
	}
	if got := Full(800, 600).Drag(Inside, 40, 40, 800, 600); got != Full(800, 600) {
		t.Errorf("a full crop went to %+v", got)
	}
}

func TestTurningCarriesTheSelection(t *testing.T) {
	c := Crop{10, 5, 20, 10}
	cw := c.TurnedClockwise(50)
	if cw != (Crop{35, 10, 10, 20}) {
		t.Fatalf("clockwise = %+v", cw)
	}
	if got := c.TurnedCounterClockwise(100); got != (Crop{5, 70, 10, 20}) {
		t.Errorf("counter-clockwise = %+v", got)
	}
	// The picture is 50 across once it has been turned clockwise.
	if got := cw.TurnedCounterClockwise(50); got != c {
		t.Errorf("round trip = %+v", got)
	}
}

func TestEditTurnsUnderTheCropAndResets(t *testing.T) {
	e := NewEdit(800, 600)
	if w, h := e.DisplayedSize(); w != 800 || h != 600 {
		t.Fatalf("size %dx%d", w, h)
	}
	cw := e.Turned(true)
	if w, h := cw.DisplayedSize(); cw.Turns != 1 || w != 600 || h != 800 || cw.Crop != Full(600, 800) {
		t.Errorf("clockwise = %+v", cw)
	}
	round := cw.Turned(true).Turned(true).Turned(true)
	if w, h := round.DisplayedSize(); round.Turns != 0 || w != 800 || h != 600 || round.Crop != Full(800, 600) {
		t.Errorf("four turns = %+v", round)
	}
	if left := e.Turned(false); left.Turns != 3 {
		t.Errorf("left = %d turns", left.Turns)
	}
	if r := cw.Reset(); r.Turns != 0 || r.Crop != Full(800, 600) {
		t.Errorf("reset = %+v", r)
	}
}
