package calls

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/av"
	"zapfast/gocore/voip/audio"
	"zapfast/gocore/voip/camera"
	"zapfast/gocore/voip/engine"
	"zapfast/gocore/voip/groupcall"
	"zapfast/gocore/voip/relay"
	"zapfast/gocore/voip/video"
)

// ErrNoRelay is Open's error for parameters without a <relay>.
var ErrNoRelay = errors.New("zapfast calls: the call carries no <relay>")

// errNotLive is a command or close for a generation that is not the open
// call.
var errNotLive = errors.New("zapfast calls: that call's media is not open")

// errBusy is an Open while another call's media is still up.
var errBusy = errors.New("zapfast calls: another call's media is still open")

// engineMedia is Media on the Go call engine (gocore/voip/engine): one
// call at a time, its events queued for Next, its pictures in frames.
type engineMedia struct {
	d      engine.Devices
	list   func() (abi.DeviceList, error)
	frames *video.Frames
	silent bool

	mu     sync.Mutex
	call   *engine.Call
	freed  bool
	events []MediaEvent
	signal chan struct{}
}

// NewMedia is the Go engine's Media over d; list names the devices; frames
// (which d.Frames should be) are the pictures Frame hands out.
func NewMedia(d engine.Devices, list func() (abi.DeviceList, error), frames *video.Frames) Media {
	return &engineMedia{d: d, list: list, frames: frames, signal: make(chan struct{}, 1)}
}

// NewSystemMedia is the Go engine on this system's devices: the relay over
// UDP (DTLS, SCTP), CoreAudio with echo cancellation, AVFoundation's camera,
// VideoToolbox's H.264, AudioToolbox's Opus.
func NewSystemMedia() Media {
	frames := video.NewFrames()
	return NewMedia(SystemDevices(frames), SystemDeviceList, frames)
}

// SystemDevices is how a call reaches this system's devices.
func SystemDevices(frames *video.Frames) engine.Devices {
	return engine.Devices{
		Dial: func(ctx context.Context, addr netip.AddrPort) (relay.Transport, error) {
			return relay.NewFactory(addr).Connect(ctx)
		},
		OpenAudio: func(mic, speaker string) (engine.Audio, error) {
			a, err := audio.Open(audio.Config{Microphone: mic, Speaker: speaker, EchoCancel: true})
			if err != nil {
				return nil, err
			}
			return a, nil
		},
		OpenCamera: func(device string) (engine.Camera, error) {
			c, err := camera.Open(camera.Config{Device: device})
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		NewEncoder: func(w, h, fps int) (engine.VideoEncoder, error) {
			e, err := video.NewEncoder(video.EncoderConfig{Width: w, Height: h, FPS: fps})
			if err != nil {
				return nil, err
			}
			return e, nil
		},
		NewDecoder: func() (engine.VideoDecoder, error) {
			d, err := video.NewDecoder()
			if err != nil {
				return nil, err
			}
			return d, nil
		},
		NewOpus: NewOpus,
		Frames:  frames,
	}
}

// SystemDeviceList lists the microphones, speakers and cameras; a kind
// that cannot be listed is left empty.
func SystemDeviceList() (abi.DeviceList, error) {
	var l abi.DeviceList
	var errs []error
	if ms, err := audio.Microphones(); err == nil {
		for _, d := range ms {
			l.Microphones = append(l.Microphones, abi.Device{ID: []byte(d.ID), Label: []byte(d.Label)})
		}
	} else {
		errs = append(errs, err)
	}
	if ss, err := audio.Speakers(); err == nil {
		for _, d := range ss {
			l.Speakers = append(l.Speakers, abi.Device{ID: []byte(d.ID), Label: []byte(d.Label)})
		}
	} else {
		errs = append(errs, err)
	}
	if cs, err := camera.Devices(); err == nil {
		for _, d := range cs {
			l.Cameras = append(l.Cameras, abi.Device{ID: []byte(d.ID), Label: []byte(d.Label)})
		}
	} else {
		errs = append(errs, err)
	}
	if len(errs) == 3 {
		return l, errors.Join(errs...)
	}
	return l, nil
}

// opusCodec is gocore/av's streaming Opus as the engine's standard Opus.
type opusCodec struct {
	enc *av.OpusStreamEncoder
	dec *av.OpusStreamDecoder
}

// NewOpus is the call's standard Opus: 16 kHz mono, 60 ms, 24 kbit/s.
func NewOpus() (engine.ForeignCodec, error) {
	enc, err := av.NewOpusStreamEncoder(16000, engine.MicFrameSamples, 24000)
	if err != nil {
		return nil, err
	}
	dec, err := av.NewOpusStreamDecoder(16000)
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &opusCodec{enc, dec}, nil
}

func (o *opusCodec) Decode(dst []int16, p []byte) ([]int16, error)  { return o.dec.Decode(dst, p) }
func (o *opusCodec) Encode(dst []byte, pcm []int16) ([]byte, error) { return o.enc.Encode(dst, pcm) }
func (o *opusCodec) Conceal(dst []int16, n int) []int16             { return append(dst, make([]int16, n)...) }

func (m *engineMedia) push(ev MediaEvent) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
	select {
	case m.signal <- struct{}{}:
	default:
	}
}

var eventKinds = map[engine.CallEventKind]string{
	engine.CallConnected: MediaConnected, engine.CallFailed: MediaFailed, engine.CallClosed: MediaClosed,
	engine.CallSilent: MediaSilent, engine.CallStalled: MediaStalled, engine.CallAudible: MediaAudible,
	engine.CallRemoteVideo: MediaRemoteVideo, engine.CallCameraStopped: MediaCameraStopped, engine.CallEnded: MediaEnded,
	engine.CallMemberVideo: MediaMemberVideo,
}

func (m *engineMedia) Open(p OpenParams) error {
	var relay engine.Relay
	var group *engine.GroupParams
	if g := p.Group; g != nil {
		if g.Initial == nil || g.Initial.Relay == nil {
			return ErrNoRelay
		}
		group = &engine.GroupParams{Creator: g.Creator, Self: g.Self, Initial: g.Initial, MemberID: g.MemberID}
	} else {
		r := engine.FindRelay(p.Relay)
		if r == nil {
			return ErrNoRelay
		}
		relay = engine.ParseRelay(r)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.freed {
		return errors.New("zapfast calls: the media is freed")
	}
	if m.call != nil {
		select {
		case <-m.call.Done():
		default:
			return errBusy
		}
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	c, err := engine.Open(engine.Params{
		CallID: p.CallID, Generation: p.Generation, Outgoing: p.Outgoing, SelfLID: p.SelfLID, PeerLID: p.PeerLID,
		CallKey: p.CallKey, Relay: relay, MLow: p.MLow, Video: p.Video, CameraOn: p.CameraOn,
		Microphone: str(p.Microphone), Speaker: str(p.Speaker), Camera: str(p.Camera), Group: group,
	}, m.d, func(ev engine.CallEvent) {
		m.push(MediaEvent{Generation: ev.Generation, Kind: eventKinds[ev.Kind], Reason: ev.Reason, Member: ev.Member})
	})
	if err != nil {
		return fmt.Errorf("zapfast calls: %w", err)
	}
	m.call = c
	return nil
}

// live is the open call of generation, or nil. m.mu is held.
func (m *engineMedia) live(generation uint64) *engine.Call {
	if m.call == nil || m.call.Generation != generation {
		return nil
	}
	return m.call
}

func (m *engineMedia) Command(generation uint64, cmd MediaCommand) error {
	m.mu.Lock()
	c := m.live(generation)
	m.mu.Unlock()
	if c == nil {
		return errNotLive
	}
	ec, err := engineCommand(cmd)
	if err != nil {
		return err
	}
	c.Command(ec)
	return nil
}

// engineCommand is cmd as the engine takes it.
func engineCommand(cmd MediaCommand) (engine.Command, error) {
	str := func(k string) string { s, _ := cmd[k].(string); return s }
	flag := func(k string) bool { b, _ := cmd[k].(bool); return b }
	var c engine.Command
	switch cmd.Op() {
	case "mute":
		c = engine.Command{Op: engine.OpMute, On: flag("on")}
	case "rekey":
		c = engine.Command{Op: engine.OpRekey, LID: str("answering_lid"), Codec: str("codec")}
	case "video_start":
		c = engine.Command{Op: engine.OpVideoStart, Device: str("device"), AwaitAccept: flag("await_accept")}
	case "video_enable":
		c = engine.Command{Op: engine.OpVideoEnable}
	case "camera_on":
		c = engine.Command{Op: engine.OpCameraOn, Device: str("device")}
	case "camera_off":
		c = engine.Command{Op: engine.OpCameraOff}
	case "video_stop":
		c = engine.Command{Op: engine.OpVideoStop}
	case "peer_video":
		c = engine.Command{Op: engine.OpPeerVideo, On: flag("on")}
	case "orientation":
		v, _ := cmd["value"].(uint8)
		c = engine.Command{Op: engine.OpOrientation, Value: v}
	case "keyframe":
		c = engine.Command{Op: engine.OpKeyframe}
	case "group":
		c = engine.Command{Op: engine.OpGroup}
		c.Group, _ = cmd["update"].(*groupcall.Update)
		if e, ok := cmd["epoch"].(*Epoch); ok {
			c.Epoch = &engine.GroupEpoch{Transaction: e.Transaction, Key: e.Key}
		}
		if c.Group == nil && c.Epoch == nil {
			return c, errors.New("zapfast calls: a group command with neither roster nor epoch")
		}
	case "microphone":
		c = engine.Command{Op: engine.OpMicrophone, Device: str("device")}
	case "speaker":
		c = engine.Command{Op: engine.OpSpeaker, Device: str("device")}
	case "camera_device":
		c = engine.Command{Op: engine.OpCameraDevice, Device: str("device")}
	default:
		return c, fmt.Errorf("zapfast calls: unknown media command %q", cmd.Op())
	}
	return c, nil
}

func (m *engineMedia) Close(generation uint64) error {
	m.mu.Lock()
	c := m.live(generation)
	m.mu.Unlock()
	if c != nil {
		c.Close()
	}
	return nil
}

func (m *engineMedia) Next(timeout time.Duration) (MediaEvent, bool, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		m.mu.Lock()
		if len(m.events) > 0 {
			ev := m.events[0]
			m.events = m.events[1:]
			m.mu.Unlock()
			return ev, true, nil
		}
		m.mu.Unlock()
		select {
		case <-m.signal:
		case <-deadline.C:
			return MediaEvent{}, false, nil
		}
	}
}

func (m *engineMedia) Devices() (abi.DeviceList, error) {
	if m.list == nil {
		return abi.DeviceList{}, nil
	}
	return m.list()
}

func (m *engineMedia) Frame(stream uint32) (core.Frame, bool) {
	if m.frames == nil {
		return core.Frame{}, false
	}
	return m.frames.Frame(stream)
}

func (m *engineMedia) ReleaseFrame(stream uint32) {
	if m.frames != nil {
		m.frames.ReleaseFrame(stream)
	}
}

// OnUpdate follows the calls' updates for the frames.
func (m *engineMedia) OnUpdate(u *abi.CallUpdate) {
	if m.frames != nil {
		m.frames.OnUpdate(u)
	}
}

func (m *engineMedia) Free() {
	m.mu.Lock()
	c := m.call
	m.freed, m.call = true, nil
	m.mu.Unlock()
	if c != nil {
		c.Close()
		<-c.Done()
	}
}
