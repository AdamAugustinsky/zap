package demo

import (
	"slices"
	"sync"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// i420 is one video frame: Y, then U and V at half the width and height,
// each plane tightly packed (upstream's CallFrame).
type i420 struct {
	w, h   int
	stride [3]int
	px     []byte
}

func newI420(w, h int) *i420 {
	c := (w + 1) / 2
	return &i420{w: w, h: h, stride: [3]int{w, c, c}, px: make([]byte, w*h+2*c*((h+1)/2))}
}

// tints are the streams' chroma, by stream.
var tints = [...][2]byte{{90, 240}, {240, 110}, {54, 34}, {200, 180}, {110, 60}, {160, 90}}

// testPattern is a moving I420 test pattern: eight grey bars sliding
// sideways, a white square crossing them, and the stream's own tint.
func testPattern(w, h, tint int, frame uint64) *i420 {
	p := newI420(w, h)
	if w == 0 || h == 0 {
		return p
	}
	f := int(frame)
	shift := (f * 4) % w
	row := make([]byte, w)
	for x := range row {
		row[x] = byte(40 + 24*(((x+shift)*8/w)%8))
	}
	side := max(h/3, 1)
	travel := func(span, step int) int {
		room := max(span-side, 1)
		at := (f * step) % (2 * room)
		if at < room {
			return at
		}
		return 2*room - at
	}
	bx, by := travel(w, 7), travel(h, 3)
	y := p.px[:w*h]
	for line := range h {
		out := y[line*w : line*w+w]
		copy(out, row)
		if line >= by && line < by+side {
			for x := bx; x < min(bx+side, w); x++ {
				out[x] = 235
			}
		}
	}
	chroma := p.stride[1] * ((h + 1) / 2)
	cu, cv := tints[tint%len(tints)][0], tints[tint%len(tints)][1]
	u, v := p.px[w*h:w*h+chroma], p.px[w*h+chroma:]
	for i := range u {
		u[i] = cu
	}
	for i := range v {
		v[i] = cv
	}
	return p
}

// frames is a call's video frames for Conn.Frame (zfcore's frames.rs): the
// newest frame of each stream, held until it is released. Stream 0 is our
// camera, 1 a one-to-one peer's, and 2+i that of participant i of the
// newest Call's group, kept by their id. Frames are kept only while the
// call is live, and a stream's goes when the call says its picture
// stopped.
type frames struct {
	mu      sync.Mutex
	live    uint64
	hasLive bool
	local   *frame
	remote  *frame
	members map[string]*frame
	order   []string
	held    map[uint32]*frame
	lastPTS int64
	epoch   time.Time
}

type frame struct {
	pic *i420
	pts int64
}

func newFrames() frames {
	return frames{members: map[string]*frame{}, held: map[uint32]*frame{}, epoch: time.Now()}
}

// put keeps the newest frames of a CallVideo; dropped when no call is
// live.
func (f *frames) put(local, remote *i420, from string) {
	now := time.Since(f.epoch).Nanoseconds()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasLive {
		return
	}
	stamp := func() int64 {
		// Strictly increasing, so a caller can tell a new frame by it.
		f.lastPTS = max(now, f.lastPTS+1)
		return f.lastPTS
	}
	if local != nil {
		f.local = &frame{local, stamp()}
	}
	if remote != nil {
		fr := &frame{remote, stamp()}
		if from != "" {
			f.members[from] = fr
		} else {
			f.remote = fr
		}
	}
}

// onUpdate follows one call update: which call is live, whose pictures
// still arrive, and the participants' order.
func (f *frames) onUpdate(u *abi.CallUpdate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !live(u.Phase) {
		// Only the live call's own end clears it: a call refused while
		// another was up says nothing about that one.
		if !f.hasLive || f.live == u.Generation {
			f.hasLive = false
			f.local, f.remote, f.order = nil, nil, nil
			clear(f.members)
		}
		return
	}
	if !f.hasLive || f.live != u.Generation {
		// Another call: nothing of the last one is its picture.
		f.local, f.remote = nil, nil
		clear(f.members)
	}
	f.live, f.hasLive = u.Generation, true
	if !u.CameraOn {
		f.local = nil
	}
	if !u.RemoteVideo {
		f.remote = nil
	}
	var ps []abi.Participant
	if u.HasGroup {
		ps = u.Group.Participants
	}
	f.order = f.order[:0]
	for _, p := range ps {
		f.order = append(f.order, string(p.ID))
	}
	for id := range f.members {
		if !slices.ContainsFunc(ps, func(p abi.Participant) bool { return p.Video && string(p.ID) == id }) {
			delete(f.members, id)
		}
	}
}

// acquire is the newest frame of stream, held until release; nil while it
// has none or its last is still held.
func (f *frames) acquire(stream uint32) *frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[stream] != nil {
		return nil
	}
	var fr *frame
	switch stream {
	case core.StreamLocal:
		fr = f.local
	case core.StreamRemote:
		fr = f.remote
	default:
		if i := int(stream - core.StreamMember); i < len(f.order) {
			fr = f.members[f.order[i]]
		}
	}
	if fr != nil {
		f.held[stream] = fr
	}
	return fr
}

func (f *frames) release(stream uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, stream)
}

func (f *frame) core() core.Frame {
	p := f.pic
	return core.Frame{Px: p.px, W: p.w, H: p.h, Stride: p.stride, Format: core.FrameI420, PTS: f.pts}
}
