package video

import (
	"slices"
	"sync"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// Frames is a call's video frames for the UI, as zfcore's frames.rs keeps
// them for zf_frame_acquire: the newest picture of each stream, held from
// Frame until ReleaseFrame. Stream core.StreamLocal is our camera,
// core.StreamRemote a one-to-one peer's, and core.StreamMember+i that of
// participant i of the newest Call update's group, kept by their id so a
// roster that reorders moves the picture with its person.
//
// Pictures are kept only while a call is live (OnUpdate), and a stream's
// goes when the call says its picture stopped. A picture handed to Put*
// must not change afterwards: the UI may be drawing it. Frames implements
// core.Frames; its methods are safe from any goroutine.
type Frames struct {
	mu      sync.Mutex
	live    uint64
	hasLive bool
	local   *held
	remote  *held
	members map[string]*held
	order   []string
	holding map[uint32]*held
	lastPTS int64
	epoch   time.Time
}

type held struct {
	pic *I420
	pts int64
}

var _ core.Frames = (*Frames)(nil)

// NewFrames is an empty table, no call live.
func NewFrames() *Frames {
	return &Frames{members: map[string]*held{}, holding: map[uint32]*held{}, epoch: time.Now()}
}

// stamp is a PTS strictly above the last, so the UI tells a new frame by
// it. f.mu is held.
func (f *Frames) stamp() int64 {
	f.lastPTS = max(time.Since(f.epoch).Nanoseconds(), f.lastPTS+1)
	return f.lastPTS
}

func (f *Frames) put(p *I420, set func(*held)) {
	if !p.Valid() {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasLive {
		return
	}
	set(&held{pic: p, pts: f.stamp()})
}

// PutLocal is our camera's newest picture (the corner preview).
func (f *Frames) PutLocal(p *I420) { f.put(p, func(h *held) { f.local = h }) }

// PutRemote is a one-to-one peer's newest picture.
func (f *Frames) PutRemote(p *I420) { f.put(p, func(h *held) { f.remote = h }) }

// PutMember is group member id's newest picture.
func (f *Frames) PutMember(id string, p *I420) {
	f.put(p, func(h *held) { f.members[id] = h })
}

// OnUpdate follows one call update: which call is live, whose pictures
// still arrive, and the participants' order.
func (f *Frames) OnUpdate(u *abi.CallUpdate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u.Phase == abi.CallPhaseEnded || u.Phase == abi.CallPhaseFailed {
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

// Frame is the newest frame of stream, valid until ReleaseFrame(stream);
// false while the stream has none or its last frame is still held.
func (f *Frames) Frame(stream uint32) (core.Frame, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holding[stream] != nil {
		return core.Frame{}, false
	}
	var h *held
	switch stream {
	case core.StreamLocal:
		h = f.local
	case core.StreamRemote:
		h = f.remote
	default:
		if i := int(stream - core.StreamMember); i < len(f.order) {
			h = f.members[f.order[i]]
		}
	}
	if h == nil {
		return core.Frame{}, false
	}
	f.holding[stream] = h
	return h.pic.Core(h.pts), true
}

// ReleaseFrame gives the frame of stream back; nothing when none is held.
func (f *Frames) ReleaseFrame(stream uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.holding, stream)
}
