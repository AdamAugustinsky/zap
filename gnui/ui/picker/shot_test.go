package picker

import (
	"gonativeui/ui"
	"image/png"
	"os"
	"testing"
	"time"

	"zapfast/state"
)

// snap writes the frame to $PICKER_SHOT/name.png when set: a look at it for
// whoever is working on the picker; no test asserts on pixels.
func (r *rig) snap(name string) {
	dir := os.Getenv("PICKER_SHOT")
	if dir == "" {
		return
	}
	f, err := os.Create(dir + "/" + name + ".png")
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.Image()); err != nil {
		r.t.Fatal(err)
	}
}

func TestShots(t *testing.T) {
	if os.Getenv("PICKER_SHOT") == "" {
		t.Skip()
	}
	r := newRig(t)
	r.open(state.PickerEmoji)
	r.snap("emoji")
}

func TestShotsStickers(t *testing.T) {
	if os.Getenv("PICKER_SHOT") == "" {
		t.Skip()
	}
	r := newRig(t)
	files := stickerFiles(t, "a.png", "b.png", "c.png", "d.png", "e.png", "f.png", "g.png")
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: files, favorites: files[:1], packs: []pack{{name: "Bom dia", dir: "/p/bom", local: true, stickers: files}, {name: "Frogs", dir: "/p/frogs", stickers: files[2:]}}}))
	time.Sleep(200 * time.Millisecond)
	r.Frames(4)
	r.snap("stickers")
	r.open(state.PickerStickers)
	r.Click(ui.RoleTab, "Bom dia")
	r.snap("sticker-pack")
	r.Click(ui.RoleTab, "Add stickers")
	r.Frames(2)
	r.snap("sticker-add")
	r.Click(ui.RoleTab, "GIF")
	r.snap("gifs-nokey")
	r.s.Settings.GiphyKey = []byte("k")
	r.s.Panes.Picker = state.PickerEmoji
	r.Frames(2)
	r.s.Panes.Picker = state.PickerGifs
	r.Frames(2)
	r.emit(gifsEvent("", "g1", "g2", "g3", "g4", "g5"))
	time.Sleep(200 * time.Millisecond)
	r.Frames(4)
	r.snap("gifs")
}

// The picker over the demo core's data.
func TestShotsDemo(t *testing.T) {
	if os.Getenv("PICKER_SHOT") == "" {
		t.Skip()
	}
	d := newDemoRig(t)
	d.s.Settings.GiphyKey = []byte("zf-demo-key")
	d.open(state.PickerGifs)
	d.wait("six GIF stills", func() bool { return d.gifTiles() == 6 })
	time.Sleep(300 * time.Millisecond)
	d.Frames(4)
	d.snap("demo-gifs")
	d.openStickers()
	time.Sleep(300 * time.Millisecond)
	d.Frames(4)
	d.snap("demo-stickers-recent")
	for _, tab := range []string{"Favorites", "Received", "Launch party", "My stickers"} {
		d.Click(ui.RoleTab, tab)
		time.Sleep(200 * time.Millisecond)
		d.Frames(4)
		d.snap("demo-stickers-" + tab)
	}
}
