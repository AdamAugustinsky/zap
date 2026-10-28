package picker

import (
	"runtime"
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/state"
)

// allocs reports the fewest allocations over n frames of the rig, of a few
// tries, after a warm-up. A frame that allocates does so in every try; a
// stray allocation of another goroutine (a timer, a worker) lands in one.
func (r *rig) allocs(n int) uint64 {
	r.Frames(8)
	best := ^uint64(0)
	for try := 0; try < 6 && best != 0; try++ {
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		r.Frames(n)
		runtime.ReadMemStats(&b)
		best = min(best, b.Mallocs-a.Mallocs)
	}
	return best
}

// A steady frame allocates nothing: every tab, with results on screen,
// costs zero allocations, counted over all of the frames (not averaged).
func TestFramesAllocateNothingOnceWarm(t *testing.T) {
	const n = 60
	r := newRig(t)
	check := func(what string) {
		t.Helper()
		if got := r.allocs(n); got != 0 {
			t.Errorf("%s: %d allocations in %d frames", what, got, n)
		}
	}
	f := files(t, 6)
	r.s.Settings.GiphyKey = []byte("k")
	r.open(state.PickerEmoji)
	check("emoji")
	r.Type("face")
	check("emoji search")
	r.open(state.PickerGifs)
	r.emit(gifsEvent("", "g1", "g2", "g3", "g4"))
	check("gifs")
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: f, packs: []pack{{name: "Bom dia", dir: "/p/bom", local: true, stickers: f}}}))
	check("stickers")
	r.Click(ui.RoleTab, "Bom dia")
	check("a pack")
	r.Click(ui.RoleTab, "Add stickers")
	check("the add page")
	r.Click(ui.RoleTab, "Recent")
	r.Frames(2)
	r.rightClick(ui.RoleButton, "Sticker", 0)
	check("after a sticker menu")
	_ = abi.CommandTagReact
}
