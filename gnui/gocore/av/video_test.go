package av

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func demoVideo(t *testing.T, name string) (string, []byte) {
	t.Helper()
	path := filepath.Join("..", "demo", "assets", "videos", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data
}

// The sizes and lengths are the Rust core's golden ones
// (zf:src/backend/worker/video_preview.rs:46); both clips are 24 fps, so the
// poster is the 24th frame.
func TestInspectVideo(t *testing.T) {
	for _, c := range []struct {
		name string
		want Video
	}{
		{"launch.mp4", Video{Width: 640, Height: 360, Seconds: 4, Poster: 23 * time.Second / 24}},
		{"note.mp4", Video{Width: 360, Height: 360, Seconds: 4, Poster: 23 * time.Second / 24}},
	} {
		_, data := demoVideo(t, c.name)
		got, err := InspectVideo(data)
		if err != nil || got.Width != c.want.Width || got.Height != c.want.Height || got.Seconds != c.want.Seconds || (got.Poster-c.want.Poster).Abs() > time.Millisecond {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}

	// A track turned a quarter is measured upright, as its poster shows.
	_, data := demoVideo(t, "launch.mp4")
	turned := bytes.Clone(data)
	moov, _ := child(turned, "moov")
	trak, _ := child(moov, "trak")
	tkhd, _ := child(trak, "tkhd")
	m := tkhd[40:] // version 0: the matrix
	binary.BigEndian.PutUint32(m[0:], 0)
	binary.BigEndian.PutUint32(m[4:], 1<<16)
	binary.BigEndian.PutUint32(m[12:], 0xffff0000)
	binary.BigEndian.PutUint32(m[16:], 0)
	if got, err := InspectVideo(turned); err != nil || got.Width != 360 || got.Height != 640 {
		t.Errorf("turned: %+v, %v", got, err)
	}

	for _, bad := range [][]byte{nil, []byte("\x00\x00\x00\x18ftypmp42"), data[:1000]} {
		if _, err := InspectVideo(bad); !errors.Is(err, ErrVideo) {
			t.Errorf("%d bytes: %v", len(bad), err)
		}
	}
}

func TestVideoPoster(t *testing.T) {
	path, data := demoVideo(t, "launch.mp4")
	v, err := InspectVideo(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := VideoPoster(path, v.Poster, 320)
	if runtime.GOOS != "darwin" {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("off macOS: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.Rect.Dx() != 320 || m.Rect.Dy() != 180 {
		t.Fatalf("poster %v", m.Rect)
	}
	// A picture, not a blank: its pixels differ, and it is opaque.
	first, varied := m.RGBAAt(0, 0), false
	for y := 0; y < 180; y += 6 {
		for x := 0; x < 320; x += 6 {
			c := m.RGBAAt(x, y)
			varied = varied || c != first
			if c.A != 255 {
				t.Fatalf("(%d,%d) is %v", x, y, c)
			}
		}
	}
	if !varied {
		t.Fatalf("a blank poster: %v", first)
	}
	if _, err := VideoPoster(filepath.Join(t.TempDir(), "missing.mp4"), 0, 320); !errors.Is(err, ErrVideo) {
		t.Fatalf("a missing file: %v", err)
	}
}
