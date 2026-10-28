package core

import (
	"sync/atomic"
	"unsafe"

	"gonativeui/clib"
)

// The streams a call's video frames come on (zfcore.h, "Call video").
const (
	// StreamLocal is our own camera, the corner preview.
	StreamLocal uint32 = 0
	// StreamRemote is a one-to-one peer's camera.
	StreamRemote uint32 = 1
	// StreamMember is the first group member's camera: participant i of the
	// newest Call event's group is StreamMember+i.
	StreamMember uint32 = 2
)

// The frame formats (ZF_FRAME_I420, ZF_FRAME_RGBA). The core hands out I420
// only.
const (
	FrameI420 uint32 = 1
	FrameRGBA uint32 = 2
)

// Frame is one video frame as zf_frame describes it. For I420 the three
// planes follow each other from Px, Stride[i] bytes a row: Y has H rows, U
// and V (H+1)/2. Px is the core's memory, valid until ReleaseFrame of its
// stream (or Close): copy what must outlive that. PTS (nanoseconds) grows
// with every new frame, so a caller can skip uploading one it already has.
type Frame struct {
	Px     []byte
	W, H   int
	Stride [3]int
	Format uint32 // FrameI420 or FrameRGBA
	PTS    int64  // nanoseconds
}

// Frames is what hands out a call's video frames: *Core, and fake.Core.
type Frames interface {
	// Frame is the newest frame of stream; ok is false while it has none
	// (and while the stream's last frame is still held).
	Frame(stream uint32) (f Frame, ok bool)
	// ReleaseFrame gives the frame of stream back.
	ReleaseFrame(stream uint32)
}

// CallNumbers numbers the calls the UI places (StartCall, JoinCallLink):
// *Core, and fake.Core.
type CallNumbers interface {
	// CallGeneration is a generation no call has had yet.
	CallGeneration() uint64
}

var (
	_ Frames      = (*Core)(nil)
	_ CallNumbers = (*Core)(nil)
)

// zfFrame is zf_frame, filled by zf_frame_acquire.
type zfFrame struct {
	px     uint64 // const uint8_t *: C memory, never a Go pointer
	w, h   uint32
	stride [3]uint32
	fmt    uint32
	pts    int64
}

// Frame takes the newest frame of a call's video stream (zf_frame_acquire):
// it stays valid until ReleaseFrame(stream), which must come before the next
// Frame of that stream. Call both on the thread that draws, inside a frame.
// It allocates nothing.
func (c *Core) Frame(stream uint32) (Frame, bool) {
	s := &c.frameS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return Frame{}, false
	}
	f := &c.frame
	*f = zfFrame{}
	st := s.call.Reset().Ptr(c.h).Uint32(stream).Ptr(unsafe.Pointer(f)).Ptr(unsafe.Pointer(&s.err)).
		Do(c.lib.fn.frameAcquire).Int32()
	if st != StatusOK || f.px == 0 {
		return Frame{}, false
	}
	n := int(f.stride[0]) * int(f.h)
	if f.fmt == FrameI420 {
		n += (int(f.stride[1]) + int(f.stride[2])) * int((f.h+1)/2)
	}
	px := *(*unsafe.Pointer)(unsafe.Pointer(&f.px)) // C memory: reinterpret, as Open does with the handle
	return Frame{
		Px: unsafe.Slice((*byte)(px), n),
		W:  int(f.w), H: int(f.h),
		Stride: [3]int{int(f.stride[0]), int(f.stride[1]), int(f.stride[2])},
		Format: f.fmt,
		PTS:    f.pts,
	}, true
}

// ReleaseFrame gives the frame of stream back (zf_frame_release); nothing
// when none is held.
func (c *Core) ReleaseFrame(stream uint32) {
	s := &c.frameS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return
	}
	s.call.Reset().Ptr(c.h).Uint32(stream).Do(c.lib.fn.frameRelease)
}

// CallGeneration is a generation no call has had yet, for StartCall and
// JoinCallLink (zf_call_generation): the core numbers the calls it is
// offered from the same counter, so the two never collide and a later call
// always has the larger generation. A library without zf_call_generation
// (one built before it) gets numbers from 1<<48 up, which collide with none
// of the core's but may be smaller than a later offered call's.
func (c *Core) CallGeneration() uint64 { return c.lib.CallGeneration() }

var fallbackGeneration atomic.Uint64

// CallGeneration is Core.CallGeneration for the library: the counter is
// one per process. Any goroutine may call it.
func (l *Lib) CallGeneration() uint64 {
	if l.fn.callGeneration == (clib.Func{}) {
		return 1<<48 + fallbackGeneration.Add(1)
	}
	l.genMu.Lock()
	defer l.genMu.Unlock()
	return l.genCall.Reset().Do(l.fn.callGeneration).Uint()
}
