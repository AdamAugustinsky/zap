package calls

import (
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/voip/groupcall"
)

// Media is a call's media: the Go call engine (NewMedia), or a test's
// fake. One account has one, and it carries at most one call at a time.
// Its methods are safe from any goroutine.
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

// updateWatcher is a Media that follows the call's updates (which call is
// live, whose pictures still arrive), as the engine's video frames do.
type updateWatcher interface {
	OnUpdate(u *abi.CallUpdate)
}

// OpenParams are one call's media, as the signaling found them.
type OpenParams struct {
	CallID     string
	Generation uint64
	Outgoing   bool
	SelfLID    string
	PeerLID    string
	CallKey    []byte
	// Relay is the <relay> node or a node holding one (the offer, the
	// offer's ack).
	Relay      *waBinary.Node
	MLow       bool
	Video      bool
	CameraOn   bool
	Microphone *string
	Speaker    *string
	Camera     *string
	// Group makes it a group call's media: its relay is the roster's, and
	// its keys arrive as epochs (groupCommand); CallKey and Relay are
	// unused.
	Group *GroupOpen
}

// GroupOpen is a group call's media inputs.
type GroupOpen struct {
	Creator types.JID
	// Self is our device's LID, as the roster names it.
	Self types.JID
	// Initial is the roster the media opens on, with its relay.
	Initial *groupcall.Update
	// MemberID is the tile a media sender's pictures go on; false for one
	// the roster does not name. Called from the media's goroutines.
	MemberID func(sender types.JID) (string, bool)
}

// Epoch is a group call's shared key for a roster transaction.
type Epoch struct {
	Transaction uint32
	Key         []byte
}

// MediaCommand is one command to the live call: an "op" and its fields
// (the names zfcalls' wire.rs had).
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
func videoEnableCommand() MediaCommand        { return op("video_enable") }
func cameraOnCommand(cam any) MediaCommand    { return op("camera_on", "device", cam) }
func cameraOffCommand() MediaCommand          { return op("camera_off") }
func videoStopCommand() MediaCommand          { return op("video_stop") }
func peerVideoCommand(on bool) MediaCommand   { return op("peer_video", "on", on) }
func orientationCommand(v uint8) MediaCommand { return op("orientation", "value", v) }
func keyframeCommand() MediaCommand           { return op("keyframe") }

// groupCommand gives a group call's media a committed roster, an epoch,
// or both (nil leaves one out).
func groupCommand(u *groupcall.Update, e *Epoch) MediaCommand {
	var uv, ev any
	if u != nil {
		uv = u
	}
	if e != nil {
		ev = e
	}
	return op("group", "update", uv, "epoch", ev)
}
func microphoneCommand(dev any) MediaCommand   { return op("microphone", "device", dev) }
func speakerCommand(dev any) MediaCommand      { return op("speaker", "device", dev) }
func cameraDeviceCommand(dev any) MediaCommand { return op("camera_device", "device", dev) }

// The kinds of MediaEvent.
const (
	MediaConnected     = "connected"
	MediaFailed        = "failed"
	MediaClosed        = "closed"
	MediaSilent        = "silent"
	MediaStalled       = "stalled"
	MediaAudible       = "audible"
	MediaRemoteVideo   = "remote_video"
	MediaCameraStopped = "camera_stopped"
	// MediaMemberVideo: a group member's first picture (Member).
	MediaMemberVideo = "member_video"
	// MediaEnded is a generation's last event: every device is released.
	MediaEnded = "ended"
)

// MediaEvent is one event of the call's media.
type MediaEvent struct {
	Generation uint64
	Kind       string
	Reason     string
	Member     types.JID
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
