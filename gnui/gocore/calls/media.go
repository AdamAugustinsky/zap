package calls

import (
	"encoding/json"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// Media is a call's media: libzfcalls (Lib.Open), or a test's fake. One
// account has one, and it carries at most one call at a time. Its methods
// are safe from any goroutine.
type Media interface {
	// Open starts call p.Generation's media over p.Relay.
	Open(p OpenParams) error
	// Command changes the live call's media; an error when generation is
	// not the open call.
	Command(generation uint64, c MediaCommand) error
	// Close ends generation's media; its MediaEnded event follows. Nothing
	// when it is not open.
	Close(generation uint64) error
	// Next waits up to timeout for the next event; ok is false when none
	// came.
	Next(timeout time.Duration) (ev MediaEvent, ok bool, err error)
	// Devices lists the microphones, speakers and cameras. It opens every
	// device to name it: never on the UI thread.
	Devices() (abi.DeviceList, error)
	core.Frames
	// Free ends the live call and releases everything. No frame may be
	// held, and Next must have returned.
	Free()
}

// OpenParams are zc_open's (zfcalls/src/wire.rs, Open).
type OpenParams struct {
	CallID     string `json:"call_id"`
	Generation uint64 `json:"generation"`
	Outgoing   bool   `json:"outgoing"`
	SelfLID    string `json:"self_lid"`
	PeerLID    string `json:"peer_lid"`
	CallKey    []byte `json:"call_key"` // base64 in JSON, as the library reads it
	// Relay is binary.Marshal of the <relay> node or of a node holding one.
	Relay      []byte  `json:"relay"`
	MLow       bool    `json:"mlow"`
	Video      bool    `json:"video"`
	CameraOn   bool    `json:"camera_on,omitempty"`
	Microphone *string `json:"microphone,omitempty"`
	Speaker    *string `json:"speaker,omitempty"`
	Camera     *string `json:"camera,omitempty"`
	// Silent opens no device at all (tests).
	Silent bool `json:"silent,omitempty"`
}

// MediaCommand is one zc_command (wire.rs, Command): an "op" and its
// fields.
type MediaCommand map[string]any

func op(name string, kv ...any) MediaCommand {
	c := MediaCommand{"op": name}
	for i := 0; i+1 < len(kv); i += 2 {
		if v := kv[i+1]; v != nil {
			c[kv[i].(string)] = v
		}
	}
	return c
}

// device is a picked device's id, or nil for the system's default.
func device(id []byte, has bool) any {
	if !has {
		return nil
	}
	return string(id)
}

// Op is the command's op, for tests and logs.
func (c MediaCommand) Op() string { s, _ := c["op"].(string); return s }

func muteCommand(on bool) MediaCommand { return op("mute", "on", on) }

// rekeyCommand names the device that answered our call (its LID's wire
// string, user:device@lid); codec is "mlow", "opus" or "" (the negotiated
// one stands).
func rekeyCommand(answering, codec string) MediaCommand {
	var c any
	if codec != "" {
		c = codec
	}
	return op("rekey", "answering_lid", answering, "codec", c)
}

func videoStartCommand(cam any, awaitAccept bool) MediaCommand {
	return op("video_start", "device", cam, "await_accept", awaitAccept)
}
func videoEnableCommand() MediaCommand         { return op("video_enable") }
func cameraOnCommand(cam any) MediaCommand     { return op("camera_on", "device", cam) }
func cameraOffCommand() MediaCommand           { return op("camera_off") }
func videoStopCommand() MediaCommand           { return op("video_stop") }
func peerVideoCommand(on bool) MediaCommand    { return op("peer_video", "on", on) }
func orientationCommand(v uint8) MediaCommand  { return op("orientation", "value", v) }
func keyframeCommand() MediaCommand            { return op("keyframe") }
func microphoneCommand(dev any) MediaCommand   { return op("microphone", "device", dev) }
func speakerCommand(dev any) MediaCommand      { return op("speaker", "device", dev) }
func cameraDeviceCommand(dev any) MediaCommand { return op("camera_device", "device", dev) }

// The kinds of MediaEvent (wire.rs, Kind).
const (
	MediaConnected     = "connected"
	MediaFailed        = "failed"
	MediaClosed        = "closed"
	MediaSilent        = "silent"
	MediaStalled       = "stalled"
	MediaAudible       = "audible"
	MediaRemoteVideo   = "remote_video"
	MediaCameraStopped = "camera_stopped"
	// MediaEnded is a generation's last event: every device is released.
	MediaEnded = "ended"
)

// MediaEvent is one zc_next_event.
type MediaEvent struct {
	Generation uint64 `json:"generation"`
	Kind       string `json:"kind"`
	Reason     string `json:"reason,omitempty"`
}

// silenceReason is a MediaSilent event's reason as the schema names it.
func silenceReason(reason string) abi.SilenceReason {
	switch reason {
	case "no_decoder":
		return abi.SilenceReasonNoDecoder
	case "authentication_failing":
		return abi.SilenceReasonAuthenticationFailing
	case "unexpected_payload_type":
		return abi.SilenceReasonUnexpectedPayloadType
	case "codec_rejecting_frames":
		return abi.SilenceReasonCodecRejectingFrames
	case "codec_flapping":
		return abi.SilenceReasonCodecFlapping
	}
	return abi.SilenceReasonUnknown
}

type wireDevice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type wireDevices struct {
	Microphones []wireDevice `json:"microphones"`
	Speakers    []wireDevice `json:"speakers"`
	Cameras     []wireDevice `json:"cameras"`
}

// decodeDevices reads zc_devices' JSON as the schema's DeviceList.
func decodeDevices(data []byte) (abi.DeviceList, error) {
	var w wireDevices
	if err := json.Unmarshal(data, &w); err != nil {
		return abi.DeviceList{}, err
	}
	list := func(ds []wireDevice) []abi.Device {
		out := make([]abi.Device, len(ds))
		for i, d := range ds {
			out[i] = abi.Device{ID: []byte(d.ID), Label: []byte(d.Label)}
		}
		return out
	}
	return abi.DeviceList{Microphones: list(w.Microphones), Speakers: list(w.Speakers), Cameras: list(w.Cameras)}, nil
}
