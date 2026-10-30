package wa

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/gocore/av"
)

// The helpers of WP4b's tests: dialogs answered as the UI would, messages
// put in the archive through whatsmeow's events, and picture files made
// for the test.

// pick waits for the engine's dialog and answers it with paths; none
// cancels it. The events before it stay for the next take.
func (h *harness) pick(paths ...string) abi.EventPickRequest {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.drain()
		for i, ev := range h.events {
			if ev.Tag != abi.EventTagPickRequest {
				continue
			}
			req := ev.PickRequest
			h.events = append(h.events[:i:i], h.events[i+1:]...)
			answer := abi.Command{Tag: abi.CommandTagPickAnswer, PickAnswer: abi.CommandPickAnswer{ID: req.ID}}
			for _, p := range paths {
				answer.PickAnswer.Paths = append(answer.PickAnswer.Paths, []byte(p))
			}
			h.send(answer)
			return req
		}
		if time.Now().After(deadline) {
			h.t.Fatal("the engine asked for no dialog")
		}
		time.Sleep(time.Millisecond)
	}
}

// receive files a message from sender in chat and drops what it emitted.
func (h *harness) receive(chat, sender types.JID, id string, at time.Time, p *waE2E.Message) {
	h.t.Helper()
	h.inject(message(chat, sender, id, at, p))
	h.take()
}

// linked is a harness whose link is up, with one message from peer.
func linked(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, options{})
	h.start()
	h.receive(peer, peer, "M1", t0, text("hello"))
	return h
}

// writePNG writes a picture of w × h, see-through on its left half when
// clear is set.
func writePNG(t *testing.T, dir string, w, h int, clear bool) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255}
			if clear && x < w/2 {
				c.A = 0
			}
			m.SetNRGBA(x, y, c)
		}
	}
	path := filepath.Join(dir, "picture.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, m); err != nil {
		t.Fatal(err)
	}
	return path
}

// stickerBytes is a small still WebP, different for each seed; the test
// is skipped where the WebP encoder is not built.
func stickerBytes(t *testing.T, seed uint8) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range m.Pix {
		m.Pix[i] = seed + uint8(i%7)
	}
	data, err := av.EncodeWebP(m, 80)
	if err != nil {
		t.Skipf("no WebP encoder: %v", err)
	}
	return data
}

// writeSticker writes stickerBytes(seed) as name in dir.
func writeSticker(t *testing.T, dir, name string, seed uint8) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, stickerBytes(t, seed), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
