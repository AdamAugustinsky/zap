// Package fake is a core.Conn without the library: it replays scripted
// events and records the commands it is sent. UI tests (Z2, Z3) drive it
// deterministically, on any OS, with no dylib, no threads and no clock: an
// event is delivered only when the test has queued it and the UI drains.
//
//	f := fake.New(nil)
//	f.Replay(fixture)                          // events in zf_drain's layout
//	f.Reply(abi.CommandTagLoadChat, &messages) // queued when that command arrives
//	ui.c = f                                   // the screen under test takes a core.Conn
//	... build frames, which Drain ...
//	f.Sent()                                   // what the UI asked for
//	f.SetFrame(core.StreamRemote, frame)       // what Frame hands out
package fake

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// Core is the fake. Its methods are safe from any goroutine, as the real
// core's are; Drain hands events to the Handler with no lock held, so a
// Handler may Send, and what its Send queues is drained in the same call.
type Core struct {
	mu      sync.Mutex
	wake    func()
	queue   [][]byte // event records waiting, oldest first
	sent    [][]byte // command records received
	replies map[abi.CommandTag][][]byte
	refuse  map[abi.CommandTag]*core.Error
	blobs   map[uint64][]byte
	nextID  uint64
	started bool
	closed  bool
	skipped int
	ev      abi.Event

	frames   map[uint32]core.Frame
	held     map[uint32]bool
	acquired map[uint32]int
	gen      uint64

	voices    map[string][]float32
	voiceErrs map[string]*core.Error
	decoded   []string
}

var (
	_ core.Conn         = (*Core)(nil)
	_ core.Frames       = (*Core)(nil)
	_ core.CallNumbers  = (*Core)(nil)
	_ core.VoiceDecoder = (*Core)(nil)
)

// New returns a fake with nothing queued. wake, if not nil, is called when
// events are queued, as the real core's Config.Wake is.
func New(wake func()) *Core {
	return &Core{
		wake: wake, replies: map[abi.CommandTag][][]byte{}, refuse: map[abi.CommandTag]*core.Error{}, blobs: map[uint64][]byte{},
		frames: map[uint32]core.Frame{}, held: map[uint32]bool{}, acquired: map[uint32]int{},
		voices: map[string][]float32{}, voiceErrs: map[string]*core.Error{},
	}
}

func encode(e *abi.Event) ([]byte, error) {
	rec, err := e.Encode(nil)
	if err != nil {
		return nil, fmt.Errorf("fake: %v: %w", e.Tag, err)
	}
	return rec, nil
}

// Emit queues events, in order.
func (f *Core) Emit(events ...*abi.Event) error {
	recs := make([][]byte, 0, len(events))
	for _, e := range events {
		rec, err := encode(e)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	f.push(recs)
	return nil
}

// Replay queues the records of stream, laid out as zf_drain writes them
// (each after a little-endian u32 length): zfcore's fixtures, or a capture.
// A record that does not decode is queued all the same, and Drain skips it
// as the real core's does.
func (f *Core) Replay(stream []byte) error {
	var recs [][]byte
	for len(stream) > 0 {
		rec, rest, err := abi.NextFrame(stream)
		if err != nil {
			return fmt.Errorf("fake: replay: %w", err)
		}
		recs = append(recs, append([]byte(nil), rec...))
		stream = rest
	}
	f.push(recs)
	return nil
}

// ReplayFile is Replay of a file's contents.
func (f *Core) ReplayFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("fake: %w", err)
	}
	return f.Replay(b)
}

// Reply queues events each time a command with tag is sent, after the
// events already waiting. A later Reply for the same tag replaces it.
func (f *Core) Reply(tag abi.CommandTag, events ...*abi.Event) error {
	var recs [][]byte
	for _, e := range events {
		rec, err := encode(e)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	f.mu.Lock()
	f.replies[tag] = recs
	f.mu.Unlock()
	return nil
}

// Refuse makes Send of tag fail with code and msg, as the real core
// refuses a command (ZF_E_STATE for one the demo does not answer). The
// command is not recorded.
func (f *Core) Refuse(tag abi.CommandTag, code int32, msg string) {
	f.mu.Lock()
	f.refuse[tag] = &core.Error{Op: "zf_send", Code: code, Msg: msg}
	f.mu.Unlock()
}

func (f *Core) push(recs [][]byte) {
	if len(recs) == 0 {
		return
	}
	f.mu.Lock()
	f.queue = append(f.queue, recs...)
	f.mu.Unlock()
	if f.wake != nil {
		f.wake()
	}
}

// Start records the permit; a second one is refused with ZF_E_STATE.
func (f *Core) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return core.ErrClosed
	}
	if f.started {
		return &core.Error{Op: "zf_core_start", Code: core.EState, Msg: "the core has already started"}
	}
	f.started = true
	return nil
}

// Started reports whether Start was called.
func (f *Core) Started() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// Send records cmd (encoded, so a field over its bound fails as it would
// with the real core) and queues the events Reply gave for its tag.
func (f *Core) Send(cmd *abi.Command) error {
	rec, err := cmd.Encode(nil)
	if err != nil {
		return fmt.Errorf("zapfast core: %v: %w", cmd.Tag, err)
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return core.ErrClosed
	}
	if e := f.refuse[cmd.Tag]; e != nil {
		f.mu.Unlock()
		err := *e
		return &err
	}
	f.sent = append(f.sent, rec)
	replies := f.replies[cmd.Tag]
	f.mu.Unlock()
	f.push(append([][]byte(nil), replies...))
	return nil
}

// Sent returns the commands received so far, in order, decoded from the
// fake's own copies.
func (f *Core) Sent() []abi.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]abi.Command, len(f.sent))
	for i, rec := range f.sent {
		out[i].Decode(rec) // encoded by Send: it decodes
	}
	return out
}

// SentTags returns the tags of the commands received so far, in order.
func (f *Core) SentTags() []abi.CommandTag {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]abi.CommandTag, len(f.sent))
	for i, rec := range f.sent {
		out[i] = abi.CommandTag(uint16(rec[0]) | uint16(rec[1])<<8)
	}
	return out
}

// Pending is how many events wait to be drained.
func (f *Core) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}

// Skipped is how many records Drain skipped because they did not decode.
func (f *Core) Skipped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skipped
}

// Drain hands the waiting events to h, in order, one reused abi.Event at a
// time, as the real Drain does. With a budget above 0 it stops once that
// much time has passed (after one event) and calls wake; tests that must be
// deterministic pass 0.
func (f *Core) Drain(h core.Handler, budget time.Duration) (int, error) {
	var t0 time.Time
	if budget > 0 {
		t0 = time.Now()
	}
	n := 0
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return n, core.ErrClosed
		}
		if len(f.queue) == 0 {
			f.mu.Unlock()
			return n, nil
		}
		if budget > 0 && n > 0 && time.Since(t0) >= budget {
			f.mu.Unlock()
			if f.wake != nil {
				f.wake()
			}
			return n, nil
		}
		rec := f.queue[0]
		f.queue = f.queue[1:]
		err := f.ev.Decode(rec)
		if err != nil {
			f.skipped++
		}
		f.mu.Unlock()
		if err == nil {
			n++
			h.Event(&f.ev)
		}
	}
}

// PutBlob keeps a copy of src.
func (f *Core) PutBlob(src []byte) (abi.BlobRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return abi.BlobRef{}, core.ErrClosed
	}
	f.nextID++
	f.blobs[f.nextID] = append([]byte(nil), src...)
	return abi.BlobRef{ID: f.nextID, Len: uint64(len(src))}, nil
}

// ReadBlob copies the blob's bytes from off into dst, as zf_blob_read does.
func (f *Core) ReadBlob(id, off uint64, dst []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, core.ErrClosed
	}
	b, ok := f.blobs[id]
	if !ok {
		return 0, &core.Error{Op: "zf_blob_read", Code: core.ENotFound, Msg: fmt.Sprintf("no blob %d", id)}
	}
	if off > uint64(len(b)) {
		return 0, &core.Error{Op: "zf_blob_read", Code: core.ERange, Msg: "the offset is past the blob's end"}
	}
	return copy(dst, b[off:]), nil
}

// FreeBlob frees a blob; an unknown id is ignored.
func (f *Core) FreeBlob(id uint64) {
	f.mu.Lock()
	delete(f.blobs, id)
	f.mu.Unlock()
}

// Close closes the fake: every call afterwards returns core.ErrClosed.
func (f *Core) Close(time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return core.ErrClosed
	}
	f.closed = true
	return nil
}

// SetFrame makes f the newest frame of stream, as a call's video would.
// The fake keeps f as given: a test that changes its pixels later sees them
// change in what Frame hands out.
func (f *Core) SetFrame(stream uint32, fr core.Frame) {
	f.mu.Lock()
	f.frames[stream] = fr
	f.mu.Unlock()
}

// ClearFrame leaves stream with no frame, as when its camera stops.
func (f *Core) ClearFrame(stream uint32) {
	f.mu.Lock()
	delete(f.frames, stream)
	f.mu.Unlock()
}

// Frame hands out the frame SetFrame gave stream, as zf_frame_acquire does:
// not while the stream's last frame is held (not released yet).
func (f *Core) Frame(stream uint32) (core.Frame, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fr, ok := f.frames[stream]
	if !ok || f.closed || f.held[stream] {
		return core.Frame{}, false
	}
	f.held[stream] = true
	f.acquired[stream]++
	return fr, true
}

// ReleaseFrame gives the frame of stream back.
func (f *Core) ReleaseFrame(stream uint32) {
	f.mu.Lock()
	delete(f.held, stream)
	f.mu.Unlock()
}

// Acquired is how many times Frame handed out stream's frame.
func (f *Core) Acquired(stream uint32) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquired[stream]
}

// Held reports whether stream's frame was taken and not released.
func (f *Core) Held(stream uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[stream]
}

// CallGeneration numbers calls from 1 up, as the real core's counter does.
func (f *Core) CallGeneration() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	return f.gen
}

// TestPattern is an I420 frame of w × h (even sides) for SetFrame: grey
// bars, a lighter square where n puts it, and a tint, like the demo core's.
func TestPattern(w, h, n int) core.Frame {
	cw, ch := (w+1)/2, (h+1)/2
	px := make([]byte, w*h+2*cw*ch)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px[y*w+x] = byte(40 + 24*(((x+4*n)*8/max(w, 1))%8))
		}
	}
	side := max(h/3, 1)
	bx, by := (7*n)%max(w-side, 1), (3*n)%max(h-side, 1)
	for y := by; y < min(by+side, h); y++ {
		for x := bx; x < min(bx+side, w); x++ {
			px[y*w+x] = 235
		}
	}
	for i := w * h; i < w*h+cw*ch; i++ {
		px[i] = 90
	}
	for i := w*h + cw*ch; i < len(px); i++ {
		px[i] = 240
	}
	return core.Frame{Px: px, W: w, H: h, Stride: [3]int{w, cw, cw}, Format: core.FrameI420, PTS: int64(n)}
}

// VoiceSeconds is how long the tone DecodeVoice gives a path SetVoice has
// not named.
const VoiceSeconds = 2

// SetVoice makes DecodeVoice of path give samples (mono, 48 kHz, -1 to 1)
// instead of the tone.
func (f *Core) SetVoice(path string, samples []float32) {
	f.mu.Lock()
	f.voices[path] = samples
	f.mu.Unlock()
}

// RefuseVoice makes DecodeVoice of path fail with code (core.ENotFound,
// core.EMalformed, ...), as the real core does for a missing or damaged
// file.
func (f *Core) RefuseVoice(path string, code int32, msg string) {
	f.mu.Lock()
	f.voiceErrs[path] = &core.Error{Op: "zf_voice_decode", Code: code, Msg: msg}
	f.mu.Unlock()
}

// Decoded lists the paths DecodeVoice was asked for, in order.
func (f *Core) Decoded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.decoded...)
}

// DecodeVoice decodes as the real core does, into a blob of the fake's: what
// SetVoice gave path, else VoiceSeconds of Tone. A relative path is EArg.
func (f *Core) DecodeVoice(path string) (abi.BlobRef, core.VoiceFormat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return abi.BlobRef{}, core.VoiceFormat{}, core.ErrClosed
	}
	f.decoded = append(f.decoded, path)
	if !filepath.IsAbs(path) {
		return abi.BlobRef{}, core.VoiceFormat{}, &core.Error{Op: "zf_voice_decode", Code: core.EArg, Msg: "the path is not absolute"}
	}
	if err := f.voiceErrs[path]; err != nil {
		return abi.BlobRef{}, core.VoiceFormat{}, err
	}
	samples, ok := f.voices[path]
	if !ok {
		samples = Tone(VoiceSeconds, 440)
	}
	pcm := make([]byte, 0, 4*len(samples))
	for _, s := range samples {
		pcm = binary.LittleEndian.AppendUint32(pcm, math.Float32bits(s))
	}
	f.nextID++
	f.blobs[f.nextID] = pcm
	return abi.BlobRef{ID: f.nextID, Len: uint64(len(pcm))}, core.VoiceFormat{
		Rate: core.VoiceRate, Channels: 1, Frames: int64(len(samples)), Waveform: waveform(samples),
	}, nil
}

// Tone is seconds of a sine at hz, mono 48 kHz, its level swelling and
// fading about once a second so its waveform is not flat.
func Tone(seconds, hz float64) []float32 {
	n := int(seconds * core.VoiceRate)
	out := make([]float32, n)
	for i := range out {
		t := float64(i) / core.VoiceRate
		level := 0.5 * (0.55 + 0.45*math.Sin(2*math.Pi*0.7*t))
		out[i] = float32(level * math.Sin(2*math.Pi*hz*t))
	}
	return out
}

// waveform is upstream's voice::waveform: the loudness of 64 slices, the
// loudest 100.
func waveform(samples []float32) (bars [core.VoiceBars]byte) {
	if len(samples) == 0 {
		return bars
	}
	slice := (len(samples) + core.VoiceBars - 1) / core.VoiceBars
	var loud [core.VoiceBars]float64
	var loudest float64
	for i := 0; i*slice < len(samples) && i < core.VoiceBars; i++ {
		chunk := samples[i*slice : min((i+1)*slice, len(samples))]
		var sum float64
		for _, s := range chunk {
			sum += float64(s) * float64(s)
		}
		loud[i] = math.Sqrt(sum / float64(len(chunk)))
		loudest = max(loudest, loud[i])
	}
	if loudest == 0 {
		return bars
	}
	for i, v := range loud {
		bars[i] = byte(math.Round(v / loudest * 100))
	}
	return bars
}
