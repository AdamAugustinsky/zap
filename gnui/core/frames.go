package core

// The streams a call's video frames come on.
const (
	// StreamLocal is our own camera, the corner preview.
	StreamLocal uint32 = 0
	// StreamRemote is a one-to-one peer's camera.
	StreamRemote uint32 = 1
	// StreamMember is the first group member's camera: participant i of the
	// newest Call event's group is StreamMember+i.
	StreamMember uint32 = 2
)

// The frame formats. The core hands out I420 only.
const (
	FrameI420 uint32 = 1
	FrameRGBA uint32 = 2
)

// Frame is one video frame. For I420 the three
// planes follow each other from Px, Stride[i] bytes a row: Y has H rows, U
// and V (H+1)/2. Px is the core's memory, valid until ReleaseFrame of
// its stream (or Close): copy what must outlive that. PTS (nanoseconds) grows
// with every new frame, so a caller can skip uploading one it already has.
type Frame struct {
	Px     []byte
	W, H   int
	Stride [3]int
	Format uint32 // FrameI420 or FrameRGBA
	PTS    int64  // nanoseconds
}

// Frames is what hands out a call's video frames: a real account's core,
// the demo's, and fake.Core.
type Frames interface {
	// Frame is the newest frame of stream; ok is false while it has none
	// (and while the stream's last frame is still held).
	Frame(stream uint32) (f Frame, ok bool)
	// ReleaseFrame gives the frame of stream back.
	ReleaseFrame(stream uint32)
}

// CallNumbers numbers the calls the UI places (StartCall, JoinCallLink):
// a real account's core, the demo's, and fake.Core.
type CallNumbers interface {
	// CallGeneration is a generation no call has had yet.
	CallGeneration() uint64
}
