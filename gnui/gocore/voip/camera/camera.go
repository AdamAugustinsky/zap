package camera

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"zapfast/gocore/voip/video"
)

// Device is a camera: the system's id for it and its name.
type Device struct {
	ID, Label string
}

// Config is what a camera is opened for.
type Config struct {
	// Device is a Device.ID; empty is the first camera.
	Device string
	// Long and Short bound the frames' size either way round (the call's
	// budget, 1280 by 720 when 0); the frames keep the camera's own shape.
	Long, Short int
	// FPS is the frame rate asked for (20 when 0); a camera that grants
	// less is used at what it gives.
	FPS int
}

// Defaults for a Config's zero fields: the top step of the call's rate
// control (video_rate.rs's TIERS).
const (
	DefaultLong  = 1280
	DefaultShort = 720
	DefaultFPS   = 20
)

func (c Config) withDefaults() Config {
	if c.Long <= 0 || c.Short <= 0 {
		c.Long, c.Short = DefaultLong, DefaultShort
	}
	c.Long, c.Short = max(c.Long, c.Short), min(c.Long, c.Short)
	if c.FPS <= 0 {
		c.FPS = DefaultFPS
	}
	return c
}

// Frame is one picture from the camera and when the camera took it.
type Frame struct {
	Pic *video.I420
	At  time.Time
}

// Errors.
var (
	// ErrUnavailable is matched by the error of a system without camera
	// capture (AVFoundation off macOS). It wraps errors.ErrUnsupported.
	ErrUnavailable = fmt.Errorf("camera: not available: %w", errors.ErrUnsupported)
	// ErrNoCamera: no camera is connected, or none has the id asked for.
	ErrNoCamera = errors.New("camera: no such camera")
	// ErrPermission: the user (or a policy) refused this app the camera.
	ErrPermission = errors.New("camera: access is not allowed")
	// ErrNoMode: the camera offers no picture size or rate a call can use.
	ErrNoMode = errors.New("camera: no usable mode")
)

// SystemError is an AVFoundation call that failed, with the NSError it
// gave when it gave one.
type SystemError struct {
	// Op is the call ("AVCaptureDeviceInput").
	Op string
	// Domain and Code are the NSError's; Desc its localized description.
	Domain string
	Code   int
	Desc   string
}

func (e *SystemError) Error() string {
	if e.Domain == "" {
		return "camera: " + e.Op + " failed"
	}
	return fmt.Sprintf("camera: %s: %s %d: %s", e.Op, e.Domain, e.Code, e.Desc)
}

// Mode is one way a camera delivers: a size and its best frame rate.
type Mode struct {
	W, H int
	FPS  float64
}

// ChooseMode is the mode a call asks for (camera.rs's choose_mode): the
// largest size that fits the budget either way round at a usable rate
// (fps, or 15 when fps asks for more), the higher rate between two of a
// size. A camera with only bigger pictures gives its smallest. ok is false
// when no mode is usable.
func ChooseMode(modes []Mode, long, short, fps int) (Mode, bool) {
	long, short = max(long, short), min(long, short)
	fits := func(m Mode) bool { return max(m.W, m.H) <= long && min(m.W, m.H) <= short }
	floor := float64(min(fps, 15))
	var best Mode
	found, bestFits := false, false
	better := func(m Mode) bool {
		if !found {
			return true
		}
		f := fits(m)
		if f != bestFits {
			return f
		}
		pm, pb := m.W*m.H, best.W*best.H
		if pm != pb {
			if f {
				return pm > pb
			}
			return pm < pb
		}
		return min(m.FPS, float64(fps)) > min(best.FPS, float64(fps))
	}
	for _, m := range modes {
		if m.FPS+0.5 < floor || m.W < 2 || m.H < 2 {
			continue
		}
		if better(m) {
			best, found, bestFits = m, true, fits(m)
		}
	}
	return best, found
}

// queue is a camera's frames for its reader: the newest few, the oldest
// let go when the reader is behind.
type queue struct {
	ch     chan Frame
	mu     sync.Mutex
	closed bool
	drops  uint64
}

func newQueue() *queue { return &queue{ch: make(chan Frame, 2)} }

func (q *queue) put(f Frame) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	for {
		select {
		case q.ch <- f:
			return
		default:
		}
		select {
		case <-q.ch:
			q.drops++
		default:
		}
	}
}

func (q *queue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

func (q *queue) dropped() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.drops
}

// Camera is one open camera, delivering I420 frames of Size on Frames.
// Its methods are safe from any goroutine.
type Camera struct {
	q    *queue
	info opened
	sys  *sysCamera
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// opened is what an open camera turned out to be.
type opened struct {
	id   string
	w, h int
	fps  float64
}

// Devices lists this system's cameras.
func Devices() ([]Device, error) { return sysDevices() }

// Open starts the camera cfg names (the first when none) in the mode that
// best fits cfg's budget and rate. The camera's light is on until Close.
func Open(cfg Config) (*Camera, error) {
	cfg = cfg.withDefaults()
	q := newQueue()
	s, info, err := openSys(cfg, q)
	if err != nil {
		return nil, err
	}
	c := &Camera{q: q, info: info, sys: s, stop: make(chan struct{}), done: make(chan struct{})}
	go c.watch()
	return c, nil
}

// watch closes Done when the camera stops delivering for good: unplugged,
// or its session stopped (another app took it).
func (c *Camera) watch() {
	defer close(c.done)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			if !c.sys.alive() {
				return
			}
		}
	}
}

// Frames is the camera's pictures, the newest two at most: a reader that
// falls behind gets the latest. Closed by Close.
func (c *Camera) Frames() <-chan Frame { return c.q.ch }

// Size is the frames' size: the camera's own shape within the budget,
// both sides even.
func (c *Camera) Size() (w, h int) { return c.info.w, c.info.h }

// FPS is the frame rate the camera granted.
func (c *Camera) FPS() float64 { return c.info.fps }

// Device is the id of the camera opened.
func (c *Camera) Device() string { return c.info.id }

// Done is closed when the camera stops (unplugged, taken by another app)
// or is closed.
func (c *Camera) Done() <-chan struct{} { return c.done }

// Dropped counts the frames let go because the reader was behind.
func (c *Camera) Dropped() uint64 { return c.q.dropped() }

// Close stops the camera and closes Frames.
func (c *Camera) Close() {
	c.once.Do(func() {
		close(c.stop)
		<-c.done
		c.sys.close()
		c.q.close()
	})
}
