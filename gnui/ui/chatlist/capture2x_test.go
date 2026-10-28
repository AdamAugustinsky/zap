package chatlist

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"zapfast/abi"
)

// TestCaptureTicksAndChips writes the list's left 520 px of a scene with
// every kind of tick and chips with counts, at the scales in the file names,
// to the directory in ZAPFAST_CAPTURE_TICKS (a generator, not a check).
func TestCaptureTicksAndChips(t *testing.T) {
	dir := os.Getenv("ZAPFAST_CAPTURE_TICKS")
	if dir == "" {
		t.Skip("set ZAPFAST_CAPTURE_TICKS=<dir>")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, scale := range []float32{1, 2} {
		r := newRigScale(t, scale)
		r.s.Settings.SidebarWidth = 360
		r.f.Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me = abi.EventMe{ID: jid("5511999"), Name: []byte("Dana"), HasName: true} }))
		var cs []abi.Chat
		for i, st := range []abi.Delivery{abi.DeliveryRead, abi.DeliveryDelivered, abi.DeliverySent, abi.DeliveryPending, abi.DeliveryFailed} {
			c := chat(itoa(i+1), "Chat "+itoa(i+1), int64(100-i))
			c.HasLast, c.Last = true, textMsg("me", true, "last message "+itoa(i+1))
			c.Last.Status = st
			c.Unread = uint32(i * 4)
			cs = append(cs, c)
		}
		r.chats(cs...)
		r.Frames(4)
		img := r.Image()
		sub := img.SubImage(image.Rect(0, 0, min(img.Bounds().Dx(), int(520*scale)), min(img.Bounds().Dy(), int(480*scale))))
		f, err := os.Create(filepath.Join(dir, "ticks-"+itoa(int(scale))+"x.png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, sub)
		f.Close()
	}
}
