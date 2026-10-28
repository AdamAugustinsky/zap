// The media host of one 1:1 call: what Zap's zfcalls/src/host.rs did around
// whatsapp-rust's engine (dial the relay, drive the engine, the microphone
// and speaker, the camera, the H.264 encoder and the peer's decoder), on
// the Go engine and the gocore/voip device packages.

package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"zapfast/gocore/voip/camera"
	"zapfast/gocore/voip/relay"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/video"
)

// dialCeiling bounds the relay dial, above the native dialer's own 12 s.
const dialCeiling = 20 * time.Second

// heartbeat is how often the camera's health is checked.
const heartbeat = time.Second

// Audio is a call's microphone and speaker (gocore/voip/audio's Audio):
// 960-sample 16 kHz frames in, PCM out.
type Audio interface {
	Microphone() <-chan []int16
	Play(samples []int16)
	SetMicrophone(id string)
	SetSpeaker(id string)
	Close()
}

// Camera is a running camera (gocore/voip/camera's Camera).
type Camera interface {
	Frames() <-chan camera.Frame
	Done() <-chan struct{}
	Close()
}

// VideoEncoder is an H.264 encoder (gocore/voip/video's Encoder).
type VideoEncoder interface {
	Encode(p *video.I420, keyframe bool) ([]byte, error)
	RequestKeyframe()
	SetBitrate(bps int) error
	Close()
}

// VideoDecoder is an H.264 decoder (gocore/voip/video's Decoder).
type VideoDecoder interface {
	Decode(unit []byte) (*video.I420, error)
	Close()
}

// FrameSink takes the pictures the UI draws (gocore/voip/video's Frames).
type FrameSink interface {
	PutLocal(p *video.I420)
	PutRemote(p *video.I420)
}

// Devices is how a call reaches the system. Every field may be nil: no
// relay dialer is an error, no audio is silence both ways, no camera, codec
// or sink is a call without that part of video.
type Devices struct {
	Dial       func(ctx context.Context, addr netip.AddrPort) (relay.Transport, error)
	OpenAudio  func(mic, speaker string) (Audio, error)
	OpenCamera func(device string) (Camera, error)
	NewEncoder func(w, h, fps int) (VideoEncoder, error)
	NewDecoder func() (VideoDecoder, error)
	NewOpus    func() (ForeignCodec, error)
	Frames     FrameSink
	TxIDs      TxIDSource
}

// Params are one call's media, as the signaling found them.
type Params struct {
	CallID     string
	Generation uint64
	Outgoing   bool
	// SelfLID is our device's LID; PeerLID the call creator (incoming) or
	// the callee until Rekey names the device that answered (outgoing).
	SelfLID, PeerLID string
	CallKey          []byte
	Relay            Relay
	// MLow is the default codec; false is standard Opus.
	MLow  bool
	Video bool
	// CameraOn starts the camera with the call.
	CameraOn bool
	// Microphone, Speaker and Camera are device ids; empty: the default.
	Microphone, Speaker, Camera string
}

// CallEventKind tells a call's host events apart (zfcalls' wire Kind).
type CallEventKind int

const (
	// CallConnected: the relay allocated.
	CallConnected CallEventKind = iota
	// CallFailed: the media could not start, or failed before connecting.
	CallFailed
	// CallClosed: the media ended on its own after connecting.
	CallClosed
	// CallSilent: packets arrive but make no audio (Reason).
	CallSilent
	// CallStalled: nothing arrives.
	CallStalled
	// CallAudible: the codec switched, so the peer is heard again.
	CallAudible
	// CallRemoteVideo: the peer's first picture.
	CallRemoteVideo
	// CallCameraStopped: our camera stopped (unplugged, taken, refused).
	CallCameraStopped
	// CallEnded is the last event: every device is released.
	CallEnded
)

// CallEvent is one event of a call's media.
type CallEvent struct {
	Generation uint64
	Kind       CallEventKind
	Reason     string
}

// Op names a command to a live call.
type Op int

const (
	OpMute Op = iota
	OpRekey
	OpVideoStart
	OpVideoEnable
	OpCameraOn
	OpCameraOff
	OpVideoStop
	OpPeerVideo
	OpOrientation
	OpKeyframe
	OpMicrophone
	OpSpeaker
	OpCameraDevice
)

// Command is one command to a live call (zfcalls' wire Command).
type Command struct {
	Op Op
	// On is Mute's and PeerVideo's.
	On bool
	// LID and Codec ("mlow", "opus" or "") are Rekey's.
	LID, Codec string
	// Device is the device of VideoStart, CameraOn, Microphone, Speaker
	// and CameraDevice; empty: the default.
	Device string
	// AwaitAccept holds VideoStart's outbound video until VideoEnable.
	AwaitAccept bool
	// Value is Orientation's quarter turns.
	Value uint8
}

// Call is one call's media, from Open to its CallEnded event.
type Call struct {
	Generation uint64
	cmds       chan *Command // nil: close
	closeOnce  sync.Once
	done       chan struct{}
	stats      atomic.Pointer[Stats]
}

// Open checks p (a bad relay or key fails here) and starts the call: the
// relay is dialled, the engine driven and the devices opened on goroutines
// of their own; emit gets every event, from any goroutine, CallEnded last.
func Open(p Params, d Devices, emit func(CallEvent)) (*Call, error) {
	dir := Incoming
	if p.Outgoing {
		dir = Outgoing
	}
	cfg, err := NewConfig(dir, p.CallID, p.SelfLID, p.PeerLID, p.CallKey, p.Relay)
	if err != nil {
		return nil, err
	}
	if !p.MLow {
		cfg.Audio = Opus16k60ms
	}
	cfg.Video = p.Video
	e, err := New(cfg, d.TxIDs)
	if err != nil {
		return nil, fmt.Errorf("the media engine: %w", err)
	}
	if d.NewOpus != nil {
		if c, err := d.NewOpus(); err != nil {
			slog.Warn("zapfast voip: no Opus codec; a peer on Opus will not be heard", "err", err)
		} else {
			e.WithForeignCodec(c)
		}
	}
	if d.Dial == nil {
		return nil, errors.New("the media engine: no relay dialer")
	}
	c := &Call{Generation: p.Generation, cmds: make(chan *Command, 64), done: make(chan struct{})}
	h := &host{p: p, d: d, e: e, call: c, emit: func(k CallEventKind, reason string) {
		emit(CallEvent{Generation: p.Generation, Kind: k, Reason: reason})
	}}
	go func() {
		defer close(c.done)
		defer h.emit(CallEnded, "")
		defer func() {
			if r := recover(); r != nil {
				slog.Error("zapfast voip: the call's media panicked", "panic", r)
				h.emit(CallFailed, "the call's media stopped unexpectedly")
			}
		}()
		h.run()
	}()
	return c, nil
}

// Command sends cmd to the call; nothing once it is over.
func (c *Call) Command(cmd Command) {
	select {
	case <-c.done:
	case c.cmds <- &cmd:
	default:
		slog.Warn("zapfast voip: a call command was dropped (the queue is full)", "op", cmd.Op)
	}
}

// Close ends the call; its CallEnded event follows once every device is
// released.
func (c *Call) Close() {
	c.closeOnce.Do(func() {
		select {
		case <-c.done:
		case c.cmds <- nil:
		}
	})
}

// Done closes once the call's media is over.
func (c *Call) Done() <-chan struct{} { return c.done }

// Stats are the engine's counters, as last published.
func (c *Call) Stats() Stats {
	if s := c.stats.Load(); s != nil {
		return *s
	}
	return Stats{}
}

// eventQueue is an unbounded queue: the driver never blocks on, or loses,
// an event.
type eventQueue struct {
	mu     sync.Mutex
	items  []Event
	signal chan struct{}
}

func newEventQueue() *eventQueue { return &eventQueue{signal: make(chan struct{}, 1)} }

func (q *eventQueue) push(e Event) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *eventQueue) take() []Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

type host struct {
	p    Params
	d    Devices
	e    *Engine
	call *Call
	emit func(CallEventKind, string)

	audio     Audio
	muted     atomic.Bool
	connected bool
	remote    bool // the peer's first picture was announced
	ctl       chan Control
	rekey     chan PeerAnswer
	video     *videoPipe
}

func (h *host) control(c Control) {
	select {
	case h.ctl <- c:
	default:
		slog.Warn("zapfast voip: a video control was dropped", "kind", c.Kind)
	}
}

func (h *host) run() {
	// The microphone: exact-zero frames while muted keep the stream fed,
	// which the engine turns into comfort noise, so the relay never sees a
	// gap.
	engineMic := make(chan []int16, 3)
	stopAudio := make(chan struct{})
	if h.d.OpenAudio != nil {
		a, err := h.d.OpenAudio(h.p.Microphone, h.p.Speaker)
		if err != nil {
			slog.Warn("zapfast voip: the call goes on without audio devices", "err", err)
		} else {
			h.audio = a
		}
	}
	go h.feedMicrophone(engineMic, stopAudio)
	defer func() {
		close(stopAudio)
		if h.audio != nil {
			h.audio.Close()
		}
	}()

	// Commands that arrive while the relay is dialled wait for it.
	var waiting []*Command
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type dialed struct {
		t   relay.Transport
		err error
	}
	dialDone := make(chan dialed, 1)
	go func() {
		dctx, dcancel := context.WithTimeout(ctx, dialCeiling)
		defer dcancel()
		t, err := h.d.Dial(dctx, h.e.RelayAddr())
		if err == nil && dctx.Err() != nil {
			t.Close()
			err = dctx.Err()
		}
		dialDone <- dialed{t, err}
	}()
	var t relay.Transport
	for t == nil {
		select {
		case r := <-dialDone:
			switch {
			case errors.Is(r.err, context.DeadlineExceeded):
				h.emit(CallFailed, "The relay did not answer in time")
				return
			case r.err != nil:
				h.emit(CallFailed, "The relay could not be reached: "+r.err.Error())
				return
			}
			t = r.t
		case cmd := <-h.call.cmds:
			switch {
			case cmd == nil:
				cancel()
				if r := <-dialDone; r.t != nil {
					r.t.Close()
				}
				return
			case cmd.Op == OpMute:
				h.muted.Store(cmd.On)
			default:
				waiting = append(waiting, cmd)
			}
		}
	}

	h.ctl = make(chan Control, 64)
	if h.p.Outgoing {
		h.rekey = make(chan PeerAnswer, 1)
	}
	h.video = newVideoPipe(h)
	defer h.video.stop()
	if h.p.Video {
		if err := h.video.start(h.p.Camera, h.p.CameraOn); err != nil {
			slog.Warn("zapfast voip: the video call goes on without video", "err", err)
		}
		h.control(Control{Kind: CtlEnable})
		if !h.p.CameraOn {
			h.control(Control{Kind: CtlDisableOutbound})
		}
	}

	events := newEventQueue()
	driveDone := make(chan CloseReason, 1)
	go func() {
		driveDone <- Run(ctx, t, Channels{
			Mic: engineMic,
			Speaker: func(pcm []int16) bool {
				if h.audio != nil {
					h.audio.Play(pcm)
				}
				return true
			},
			Rekey:    h.rekey,
			Video:    h.video.out,
			VideoOut: h.video.deliver,
			Control:  h.ctl,
			Events:   events.push,
			Stats:    func(s Stats) { h.call.stats.Store(&s) },
		}, h.e)
	}()

	for _, cmd := range waiting {
		h.command(cmd)
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case cmd := <-h.call.cmds:
			if cmd == nil {
				cancel()
				<-driveDone
				return
			}
			h.command(cmd)
		case <-events.signal:
			for _, ev := range events.take() {
				h.engineEvent(ev)
			}
		case <-h.video.remoteSeen:
			if !h.remote {
				h.remote = true
				h.emit(CallRemoteVideo, "")
			}
		case <-h.video.lost:
			h.control(Control{Kind: CtlRequestPeerKeyframe, Urgency: Coalesced})
		case r := <-driveDone:
			for _, ev := range events.take() {
				h.engineEvent(ev)
			}
			h.closed(r)
			return
		case <-tick.C:
			if h.video.cameraWanted() && !h.video.cameraRunning() {
				h.video.cameraOff()
				h.emit(CallCameraStopped, "")
			}
		}
	}
}

func (h *host) feedMicrophone(out chan<- []int16, stop <-chan struct{}) {
	var in <-chan []int16
	var silence <-chan time.Time
	if h.audio != nil {
		in = h.audio.Microphone()
	} else {
		// No devices: 60 ms of silence on the engine's clock.
		t := time.NewTicker(60 * time.Millisecond)
		defer t.Stop()
		silence = t.C
	}
	for {
		var frame []int16
		select {
		case <-stop:
			return
		case f, ok := <-in:
			if !ok {
				return
			}
			frame = f
		case <-silence:
			frame = make([]int16, MicFrameSamples)
		}
		if h.muted.Load() && len(frame) == MicFrameSamples {
			frame = make([]int16, MicFrameSamples)
		}
		select {
		case out <- frame:
		case <-stop:
			return
		default:
			// The engine is behind; the oldest frame is not worth a gap.
		}
	}
}

func (h *host) closed(r CloseReason) {
	var reason string
	switch r.Kind {
	case CloseLocal, CloseTerminated:
		return // a hangup; a terminal relay failure was its own event
	case CloseRelayDisconnected:
		reason = "The relay connection dropped"
	case CloseSendFailed:
		reason = "The media could not be sent: " + r.Msg
	default:
		reason = r.Msg
	}
	if h.connected {
		h.emit(CallClosed, reason)
	} else {
		h.emit(CallFailed, reason)
	}
}

func (h *host) command(cmd *Command) {
	var err error
	switch cmd.Op {
	case OpMute:
		h.muted.Store(cmd.On)
	case OpRekey:
		a := PeerAnswer{AnsweringLID: cmd.LID}
		switch cmd.Codec {
		case "mlow":
			a.Codec, a.HasCodec = MLow, true
		case "opus":
			a.Codec, a.HasCodec = Opus, true
		}
		if h.rekey != nil {
			select {
			case h.rekey <- a:
			default:
			}
		}
	case OpVideoStart:
		if err = h.video.start(cmd.Device, true); err == nil {
			if cmd.AwaitAccept {
				h.control(Control{Kind: CtlEnableAwaitingAccept})
			} else {
				h.control(Control{Kind: CtlEnable})
			}
		}
	case OpVideoEnable:
		h.control(Control{Kind: CtlEnable})
	case OpCameraOn:
		err = h.video.cameraOn(cmd.Device)
	case OpCameraOff:
		h.video.cameraOff()
	case OpVideoStop:
		h.video.stop()
		h.remote = false
		h.control(Control{Kind: CtlDisable})
	case OpPeerVideo:
		if cmd.On {
			if h.video.started() {
				h.control(Control{Kind: CtlEnable})
				if !h.video.cameraWanted() {
					h.control(Control{Kind: CtlDisableOutbound})
				}
			}
		} else {
			h.remote = false
		}
	case OpOrientation:
		h.control(Control{Kind: CtlOrientation, Orientation: cmd.Value % 4})
	case OpKeyframe:
		h.control(Control{Kind: CtlRequestPeerKeyframe, Urgency: Immediate})
	case OpMicrophone:
		if h.audio != nil {
			h.audio.SetMicrophone(cmd.Device)
		}
	case OpSpeaker:
		if h.audio != nil {
			h.audio.SetSpeaker(cmd.Device)
		}
	case OpCameraDevice:
		err = h.video.switchCamera(cmd.Device)
	}
	if err != nil {
		slog.Warn("zapfast voip: a call command failed", "op", cmd.Op, "err", err)
		h.emit(CallCameraStopped, "")
	}
}

// engineEvent is one of the engine's events, as the UI sees it.
func (h *host) engineEvent(ev Event) {
	switch ev.Kind {
	case EventRelayAllocated:
		h.connected = true
		h.emit(CallConnected, "")
		if h.video.started() {
			// A decoder cannot start on a delta frame: ask for an IDR now.
			h.control(Control{Kind: CtlRequestPeerKeyframe, Urgency: Immediate})
		}
	case EventRelayAllocateFailed:
		h.emit(CallFailed, fmt.Sprintf("The relay refused the call (%d)", ev.Code))
	case EventRelayAllocateTimedOut:
		h.emit(CallFailed, "The relay did not answer")
	case EventAudioSilent:
		h.emit(CallSilent, ev.Reason.String())
	case EventAudioReceptionStalled:
		h.emit(CallStalled, "")
	case EventAudioCodecSwitched:
		h.emit(CallAudible, "")
	case EventVideoKeyframeNeeded:
		h.video.forceKeyframe()
	case EventRTCPReceived:
		h.video.rtcp(ev)
	case EventOutboundMediaDropped:
		if ev.VideoAUs > 0 {
			h.video.sendDrops()
		}
	}
}

// The encoder's bitrate follows the peer's reports between these bounds (a
// simplified video_rate.rs: the bitrate moves, the picture size does not).
const (
	minVideoBitrate   = 200_000
	startVideoBitrate = 700_000
	maxVideoBitrate   = 1_500_000
	// The camera's budget: video_rate.rs's top tier.
	cameraLong, cameraShort, cameraFPS = 1280, 720, 20
)

// videoPipe is the video direction: the camera, the encoder and the feed
// of its access units to the engine; the peer's access units through the
// decoder to the UI.
type videoPipe struct {
	h          *host
	out        chan VideoInput
	in         chan VideoFrame
	remoteSeen chan struct{}
	lost       chan struct{}

	mu       sync.Mutex
	on       bool // started (a video call or an upgrade)
	cam      Camera
	camStop  chan struct{}
	camDone  chan struct{}
	device   string
	wanted   bool
	gen      uint64
	keyframe atomic.Bool
	bitrate  atomic.Int64
	encMu    sync.Mutex // enc only: capture takes it while stop holds mu
	enc      VideoEncoder
	decStop  chan struct{}
	decDone  chan struct{}
	epoch    time.Time
}

func newVideoPipe(h *host) *videoPipe {
	v := &videoPipe{
		h: h, out: make(chan VideoInput, 4), in: make(chan VideoFrame, 8),
		remoteSeen: make(chan struct{}, 1), lost: make(chan struct{}, 1), epoch: time.Now(),
	}
	v.bitrate.Store(startVideoBitrate)
	return v
}

func (v *videoPipe) started() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.on }

func (v *videoPipe) cameraWanted() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.wanted }

func (v *videoPipe) cameraRunning() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cam == nil {
		return false
	}
	select {
	case <-v.camDone:
		return false
	default:
		return true
	}
}

// deliver hands the engine's reassembled access unit to the decoder; true
// when its queue is full (a shed unit, worth a keyframe request).
func (v *videoPipe) deliver(f VideoFrame) bool {
	select {
	case v.in <- f:
		return false
	default:
		return true
	}
}

// start brings the decoder up, and the camera unless camera is off.
func (v *videoPipe) start(device string, cam bool) error {
	v.mu.Lock()
	if device != "" {
		v.device = device
	}
	if !v.on {
		v.on = true
		v.decStop, v.decDone = make(chan struct{}), make(chan struct{})
		go v.decode(v.decStop, v.decDone)
	}
	v.mu.Unlock()
	if cam {
		return v.cameraOn("")
	}
	v.cameraOff()
	return nil
}

// cameraOn (re)starts the camera and feeds its access units to the engine
// under a fresh input generation.
func (v *videoPipe) cameraOn(device string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if device != "" {
		v.device = device
	}
	if !v.on {
		return errors.New("this call has no video")
	}
	v.stopCameraLocked()
	if v.h.d.OpenCamera == nil {
		return errors.New("no camera")
	}
	cam, err := v.h.d.OpenCamera(v.device)
	if err != nil {
		return fmt.Errorf("the camera could not start: %w", err)
	}
	v.cam, v.wanted = cam, true
	v.gen++
	v.camStop, v.camDone = make(chan struct{}), make(chan struct{})
	v.h.control(Control{Kind: CtlInputGeneration, Generation: v.gen})
	v.h.control(Control{Kind: CtlTimestampStride, Stride: rtp.VideoClockRate / cameraFPS})
	v.h.control(Control{Kind: CtlEnable})
	v.keyframe.Store(true)
	go v.capture(cam, v.gen, v.camStop, v.camDone)
	return nil
}

// cameraOff releases the camera and gates our video off the wire.
func (v *videoPipe) cameraOff() {
	v.mu.Lock()
	v.stopCameraLocked()
	v.wanted = false
	v.mu.Unlock()
	v.h.control(Control{Kind: CtlDisableOutbound})
}

func (v *videoPipe) switchCamera(device string) error {
	v.mu.Lock()
	v.device = device
	wanted := v.wanted
	v.mu.Unlock()
	if wanted {
		return v.cameraOn(device)
	}
	return nil
}

func (v *videoPipe) stopCameraLocked() {
	if v.cam == nil {
		return
	}
	close(v.camStop)
	v.cam.Close()
	<-v.camDone
	v.cam = nil
}

// stop takes the whole video direction down.
func (v *videoPipe) stop() {
	v.mu.Lock()
	v.stopCameraLocked()
	v.wanted = false
	if v.on {
		close(v.decStop)
		<-v.decDone
		v.on = false
	}
	v.mu.Unlock()
}

func (v *videoPipe) forceKeyframe() {
	v.keyframe.Store(true)
	v.encMu.Lock()
	if v.enc != nil {
		v.enc.RequestKeyframe()
	}
	v.encMu.Unlock()
}

// capture encodes the camera's pictures for the engine and the preview.
func (v *videoPipe) capture(cam Camera, gen uint64, stop, done chan struct{}) {
	defer close(done)
	frames := cam.Frames()
	var enc VideoEncoder
	var encW, encH int
	var bitrate int64
	defer func() {
		if enc != nil {
			v.encMu.Lock()
			if v.enc == enc {
				v.enc = nil
			}
			v.encMu.Unlock()
			enc.Close()
		}
	}()
	for {
		var f camera.Frame
		select {
		case <-stop:
			return
		case <-cam.Done():
			return
		case fr, ok := <-frames:
			if !ok {
				return
			}
			f = fr
		}
		if f.Pic == nil || !f.Pic.Valid() {
			continue
		}
		if v.h.d.Frames != nil {
			v.h.d.Frames.PutLocal(f.Pic)
		}
		if enc == nil || encW != f.Pic.W || encH != f.Pic.H {
			if enc != nil {
				v.encMu.Lock()
				v.enc = nil
				v.encMu.Unlock()
				enc.Close()
				enc = nil
			}
			if v.h.d.NewEncoder == nil {
				continue
			}
			e, err := v.h.d.NewEncoder(f.Pic.W, f.Pic.H, cameraFPS)
			if err != nil {
				slog.Warn("zapfast voip: the video encoder could not start", "err", err)
				continue
			}
			enc, encW, encH, bitrate = e, f.Pic.W, f.Pic.H, 0
			v.encMu.Lock()
			v.enc = enc
			v.encMu.Unlock()
			v.keyframe.Store(true)
		}
		if b := v.bitrate.Load(); b != bitrate {
			if err := enc.SetBitrate(int(b)); err == nil {
				bitrate = b
			}
		}
		au, err := enc.Encode(f.Pic, v.keyframe.Swap(false))
		if err != nil || len(au) == 0 {
			continue
		}
		at := f.At
		if at.IsZero() {
			at = time.Now()
		}
		ts := uint32(at.Sub(v.epoch).Microseconds() * int64(rtp.VideoClockRate) / 1_000_000)
		select {
		case v.out <- VideoInput{Data: au, Timestamp: ts, Generation: gen}:
		default:
			// The engine is behind: the path carries less than is sent.
			v.sendDrops()
			v.keyframe.Store(true)
		}
	}
}

// decode turns the peer's access units into pictures for the UI.
func (v *videoPipe) decode(stop, done chan struct{}) {
	defer close(done)
	var dec VideoDecoder
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()
	signal := func(c chan struct{}) {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	for {
		var f VideoFrame
		select {
		case <-stop:
			return
		case f = <-v.in:
		}
		if dec == nil {
			if v.h.d.NewDecoder == nil {
				continue
			}
			d, err := v.h.d.NewDecoder()
			if err != nil {
				slog.Warn("zapfast voip: the video decoder could not start", "err", err)
				continue
			}
			dec = d
		}
		pic, err := dec.Decode(f.Data)
		if err != nil {
			signal(v.lost)
			continue
		}
		if pic == nil {
			continue
		}
		if f.Orientation != 0 {
			pic = pic.Rotate(int(f.Orientation))
		}
		if v.h.d.Frames != nil {
			v.h.d.Frames.PutRemote(pic)
		}
		signal(v.remoteSeen)
	}
}

// rtcp moves the encoder's bitrate on the peer's reports about our video:
// down on loss or a lower estimate (REMB), slowly up while clean.
func (v *videoPipe) rtcp(ev Event) {
	if !ev.ReportsVideo || ev.ReportsAudio {
		return
	}
	b := v.bitrate.Load()
	if len(ev.RTCP.ReportBlocks) > 0 {
		switch lost := ev.RTCP.ReportBlocks[0].FractionLost; {
		case lost > 26: // above ~10%
			b = b * 85 / 100
		case lost < 5: // below ~2%
			b = b * 105 / 100
		}
	}
	for _, f := range ev.RTCP.Feedback {
		if f.PacketType == 206 && f.FMT == 15 {
			if est, ok := rembBitrate(f.FCI); ok && int64(est) < b {
				b = int64(est)
			}
		}
	}
	v.bitrate.Store(min(max(b, minVideoBitrate), maxVideoBitrate))
}

func (v *videoPipe) sendDrops() {
	v.bitrate.Store(max(v.bitrate.Load()*80/100, minVideoBitrate))
}

// rembBitrate reads a REMB FCI ("REMB", count, exponent and mantissa).
func rembBitrate(fci []byte) (uint32, bool) {
	if len(fci) < 8 || string(fci[:4]) != "REMB" {
		return 0, false
	}
	exp := fci[5] >> 2
	mantissa := uint64(fci[5]&3)<<16 | uint64(fci[6])<<8 | uint64(fci[7])
	v := mantissa << exp
	if v > 1<<32-1 {
		return 1<<32 - 1, true
	}
	return uint32(v), true
}
