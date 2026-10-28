package viewer

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// A steady frame builds without allocating: a fitted photo, a zoomed one under
// the pointer with the album's arrows up, and an item still to download.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	a, _ := threePhotos(t)
	a = append(a, mediaItem("m4", "", false))
	r := open(t, a, "m2")
	r.photo()
	check := func(what string) {
		t.Helper()
		r.Play(widgettest.Settle())
		r.Frames(5)
		if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
			t.Errorf("%s: %v allocations per frame", what, n)
		}
	}
	check("fitted")
	r.Key('=', 0)
	r.Frames(2)
	r.Play(widgettest.Move(ui.RoleImage, "Photo"), widgettest.Idle(2))
	r.Must(ui.RoleButton, "Next (Right arrow)")
	check("zoomed, hovered")
	r.Click(ui.RoleButton, "Photo 4")
	r.Frames(3)
	r.Must(ui.RoleButton, "Download")
	check("not downloaded")
}

// The sticker view builds without allocating too.
func TestStickerViewAllocatesNothing(t *testing.T) {
	p := writePNG(t, t.TempDir(), "s.png", 256, 256)
	r := openSticker(t, p)
	r.wait(ui.RoleImage, "Sticker")
	r.Play(widgettest.Settle())
	r.Frames(5)
	if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

// A clip playing, with its controls up, builds frames without allocating.
func TestAPlayingClipAllocatesNothing(t *testing.T) {
	r, cs := openClipRig(t)
	r.Click(ui.RoleButton, "Play or pause the video")
	r.Frames(6)
	v := cs.last()
	v.dur = time.Hour // it plays through the measuring
	if !v.playing {
		t.Fatal("not playing")
	}
	if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
		t.Errorf("%v allocations per playing frame", n)
	}
	if v.frames < 50 {
		t.Errorf("only %d frames played", v.frames)
	}
}
