package camera

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"zapfast/gocore/voip/video"
)

func TestChooseMode(t *testing.T) {
	modes := []Mode{{1920, 1080, 30}, {1280, 720, 30}, {1280, 720, 10}, {640, 480, 30}}
	if m, _ := ChooseMode(modes, 1280, 720, 20); m != (Mode{1280, 720, 30}) {
		t.Fatalf("chose %+v: the largest that fits at a usable rate", m)
	}
	// A camera with only bigger pictures gives its smallest.
	if m, _ := ChooseMode([]Mode{{3840, 2160, 30}, {1920, 1080, 30}}, 1280, 720, 20); m.W != 1920 {
		t.Fatalf("chose %+v", m)
	}
	// A portrait camera fits the budget turned upright.
	if m, _ := ChooseMode([]Mode{{720, 1280, 30}}, 1280, 720, 20); m.H != 1280 {
		t.Fatalf("chose %+v", m)
	}
	// Of one size, the faster up to the rate asked for.
	if m, _ := ChooseMode([]Mode{{640, 480, 15}, {640, 480, 29.97}}, 1280, 720, 20); m.FPS != 29.97 {
		t.Fatalf("chose %+v", m)
	}
	// Too slow everywhere: nothing.
	if _, ok := ChooseMode([]Mode{{640, 480, 5}}, 1280, 720, 20); ok {
		t.Fatal("a 5 fps mode was chosen")
	}
	if _, ok := ChooseMode(nil, 1280, 720, 20); ok {
		t.Fatal("no modes, one chosen")
	}
}

func TestQueueKeepsTheNewest(t *testing.T) {
	q := newQueue()
	for i := range 5 {
		q.put(Frame{Pic: video.NewI420(2*(i+1), 2)})
	}
	if q.dropped() != 3 {
		t.Fatalf("dropped %d", q.dropped())
	}
	if f := <-q.ch; f.Pic.W != 8 {
		t.Fatalf("the oldest kept is %d wide", f.Pic.W)
	}
	q.close()
	q.close()
	q.put(Frame{}) // after close: nothing
	if f := <-q.ch; f.Pic.W != 10 {
		t.Fatal("the newest was lost")
	}
	if _, ok := <-q.ch; ok {
		t.Fatal("open after close")
	}
}

// Listing cameras opens none: safe anywhere. Only the count is logged.
func TestDevices(t *testing.T) {
	ds, err := Devices()
	if runtime.GOOS != "darwin" {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Devices off macOS: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	for _, d := range ds {
		if d.ID == "" || d.Label == "" {
			t.Fatal("a camera without an id or a name")
		}
	}
	t.Logf("%d cameras", len(ds))
}

// A camera that is not there is ErrNoCamera, and nothing starts.
func TestOpenUnknownCamera(t *testing.T) {
	_, err := Open(Config{Device: "no-such-camera-unique-id"})
	if runtime.GOOS != "darwin" {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Open off macOS: %v", err)
		}
		return
	}
	if !errors.Is(err, ErrNoCamera) && !errors.Is(err, ErrPermission) {
		t.Fatalf("Open(unknown) = %v, want ErrNoCamera", err)
	}
}

// ZAP_LIVE_AV=1: the first camera delivers I420 frames of its size, and
// stops when closed.
func TestLiveCamera(t *testing.T) {
	if os.Getenv("ZAP_LIVE_AV") != "1" {
		t.Skip("opens the real camera: set ZAP_LIVE_AV=1")
	}
	ds, err := Devices()
	if err != nil || len(ds) == 0 {
		t.Skipf("no camera: %v", err)
	}
	c, err := Open(Config{Long: 640, Short: 360, FPS: 15})
	if err != nil {
		t.Fatal(err)
	}
	w, h := c.Size()
	if w > 640 || h > 640 || w%2 != 0 || h%2 != 0 {
		t.Fatalf("size %dx%d", w, h)
	}
	deadline := time.After(10 * time.Second)
	for n := 0; n < 10; {
		select {
		case f := <-c.Frames():
			if f.Pic.W != w || f.Pic.H != h || !f.Pic.Valid() || len(f.Pic.Px) != video.I420Len(w, h) {
				t.Fatalf("a frame %dx%d", f.Pic.W, f.Pic.H)
			}
			n++
		case <-c.Done():
			t.Fatal("the camera stopped")
		case <-deadline:
			t.Fatal("no frames in 10 s")
		}
	}
	t.Logf("%dx%d at %.1f fps", w, h, c.FPS())
	c.Close()
	c.Close()
	for range c.Frames() {
	}
	<-c.Done()
}
