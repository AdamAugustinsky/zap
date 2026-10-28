package shell

import "testing"

// SH-007: the narrow window's slide (zf:src/ui/mod.rs
// a_narrow_window_slides_between_the_list_and_what_it_opens).
func TestANarrowWindowSlidesBetweenTheListAndWhatItOpens(t *testing.T) {
	var sl narrowSlide
	const s = int64(1e9)
	at := func(now int64, view int) (float32, bool) { return sl.step(view, now) }
	// Narrowing the window shows its view at once.
	if _, on := at(0, narrowNone); on {
		t.Error("slid while wide")
	}
	if _, on := at(s/10, narrowList); on {
		t.Error("narrowing slid")
	}
	// Opening a chat starts a slide that eases into place and ends.
	if p, on := at(s, narrowDetail); !on || p != 0 {
		t.Errorf("the slide began at %v (%v)", p, on)
	}
	if p, on := at(s+slideNs/2, narrowDetail); !on || p <= 0.5 || p >= 1 {
		t.Errorf("halfway at %v (%v), want eased", p, on)
	}
	if _, on := at(s+slideNs, narrowDetail); on {
		t.Error("the slide did not end")
	}
	// Back to the list slides too.
	if p, on := at(2*s, narrowList); !on || p != 0 {
		t.Errorf("back began at %v (%v)", p, on)
	}
	// Widening mid-slide drops it, and narrowing again does not slide.
	if _, on := at(2*s+s/20, narrowNone); on {
		t.Error("slid while wide")
	}
	if _, on := at(2*s+s/10, narrowDetail); on {
		t.Error("narrowing again slid")
	}
}
