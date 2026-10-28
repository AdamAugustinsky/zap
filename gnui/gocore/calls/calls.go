package calls

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/core"
)

// Host is what the WhatsApp engine gives the calls. Every function runs on
// the engine's goroutine but Go's and Post, which any goroutine may call.
type Host struct {
	// Context ends with the engine.
	Context context.Context
	// Post runs f on the engine's goroutine.
	Post func(f func())
	// Go runs slow work (the network, the devices) off it.
	Go func(name string, f func(ctx context.Context)) bool
	// Background runs the media's event loop, for as long as Context.
	Background func(name string, f func(ctx context.Context)) bool
	Emit       func(ev *abi.Event)
	// Chat is the chat id a call with user is shown and filed under.
	Chat func(user types.JID) string
	// File files a finished call among its chat's messages.
	File func(Entry)
	// Error shows a toast.
	Error func(text string)
}

// Entry is a finished call, for its chat and the call log.
type Entry struct {
	CallID  string
	Chat    string
	FromMe  bool
	Time    int64 // Unix seconds: when it began
	Video   bool
	Status  abi.CallStatus
	Seconds uint32
}

// Timing is the signaling's patience, shortened by the tests.
type Timing struct {
	// Ack bounds the wait for the server's <ack> (and relay) to an offer.
	Ack time.Duration
	// Ring is how long a call rings unanswered before it is given up.
	Ring time.Duration
	// Upgrade is how long a video upgrade we asked for waits for the peer.
	Upgrade time.Duration
	// Poll is the media event wait.
	Poll time.Duration
}

// DefaultTiming is whatsapp-rust's: a 10 s offer ack, a 5 s video upgrade.
var DefaultTiming = Timing{Ack: 10 * time.Second, Ring: 75 * time.Second, Upgrade: 5 * time.Second, Poll: 250 * time.Millisecond}

// NotAvailable is the refusal of a call command when the account has no
// call media.
const NotAvailable = "Calls are not available in this build of Zap: the call media could not start"

// generations is the process's call counter: the calls the UI places
// (NextGeneration, through the Conn's CallGeneration) and the ones rung in.
var generations atomic.Uint64

// NextGeneration is a generation no call has had yet.
func NextGeneration() uint64 { return 1<<47 + generations.Add(1) }

// Calls is one account's 1:1 calls: the signaling over Client, the media
// through Media. It is owned by the engine's goroutine (Host), but for
// Frame and ReleaseFrame, which the UI calls from its own.
type Calls struct {
	h      Host
	cli    Client
	media  Media
	timing Timing
	flags  cancelFlags

	call    *call
	devices abi.DeviceList
}

// New makes the calls; media nil refuses every call (the library is
// missing). It starts the media's event goroutine. SetClient gives it the
// connection.
func New(h Host, media Media, t Timing) *Calls {
	c := &Calls{h: h, media: media, timing: t}
	if media != nil {
		h.Background("calls-media", c.poll)
	}
	return c
}

// SetClient gives the calls the connection (nil: none). A call up on the
// one before ends.
func (c *Calls) SetClient(cli Client) {
	if k := c.call; k != nil {
		k.placing = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusConnectionLost)
	}
	c.cli = cli
}

// Available reports whether calls can be placed and taken.
func (c *Calls) Available() bool { return c.media != nil }

// poll hands the media's events to the engine's goroutine, and frees the
// media once the engine ends.
func (c *Calls) poll(ctx context.Context) {
	defer c.media.Free()
	for ctx.Err() == nil {
		ev, ok, err := c.media.Next(c.timing.Poll)
		if err != nil {
			slog.Warn("zapfast calls: reading the media's events", "err", err)
			return
		}
		if ok {
			c.h.Post(func() { c.mediaEvent(ev) })
		}
	}
}

// Frame and ReleaseFrame are the call's video frames (core.Frames), from
// any goroutine.
func (c *Calls) Frame(stream uint32) (core.Frame, bool) {
	if c.media == nil {
		return core.Frame{}, false
	}
	return c.media.Frame(stream)
}

func (c *Calls) ReleaseFrame(stream uint32) {
	if c.media != nil {
		c.media.ReleaseFrame(stream)
	}
}

// --- the call ------------------------------------------------------------------

// call is the one call up.
type call struct {
	u        abi.CallUpdate
	id       string    // call-id
	creator  types.JID // call-creator
	peer     types.JID // the peer: their LID once placed (outgoing), the offer's sender (incoming)
	offer    *Signal   // the offer, rung in
	outgoing bool
	cancel   *cancelFlag
	began    int64
	file     bool
	mlow     bool

	// placing: the offer is being prepared or sent (outgoing, no ack yet).
	placing bool
	// ring are the peer's devices the server rings (outgoing).
	ring []types.JID
	// answering is the device that accepted (outgoing), or the one that
	// offered (incoming).
	answering types.JID
	answered  bool // the peer accepted, or we did
	relayed   bool // the media's relay allocated
	open      bool // the media is open under u.Generation
	started   time.Time
	carried   bool // the call had video at some point, for its entry

	// The video upgrade: requested is our <video state=11> waiting for
	// the peer's answer; plane is our video pipeline running.
	requested bool
	plane     bool

	ringTimer, upgradeTimer *time.Timer
}

func live(p abi.CallPhase) bool { return p != abi.CallPhaseEnded && p != abi.CallPhaseFailed }

func (k *call) stopTimers() {
	for _, t := range []*time.Timer{k.ringTimer, k.upgradeTimer} {
		if t != nil {
			t.Stop()
		}
	}
	k.ringTimer, k.upgradeTimer = nil, nil
}

// after runs f on the engine's goroutine after d, if the call is still
// generation's.
func (c *Calls) after(d time.Duration, generation uint64, f func(k *call)) *time.Timer {
	return time.AfterFunc(d, func() {
		c.h.Post(func() {
			if k := c.call; k != nil && k.u.Generation == generation && live(k.u.Phase) {
				f(k)
			}
		})
	})
}

// emit publishes the call; a call that ended is filed, its media closed,
// and let go.
func (c *Calls) emit(k *call) {
	u := k.u
	if !k.started.IsZero() {
		u.ActiveForMS, u.HasActiveForMS = uint64(time.Since(k.started).Milliseconds()), true
	}
	if w, ok := c.media.(updateWatcher); ok {
		w.OnUpdate(&u)
	}
	ev := abi.Event{Tag: abi.EventTagCall}
	ev.Call.Update = u
	c.h.Emit(&ev)
	if live(k.u.Phase) || c.call != k {
		return
	}
	c.call = nil
	k.stopTimers()
	if k.open {
		if err := c.media.Close(k.u.Generation); err != nil {
			slog.Warn("zapfast calls: closing the media", "err", err)
		}
		k.open = false
	}
	c.flags.release(k.cancel)
	if k.file && k.u.HasOutcome {
		var secs uint32
		if !k.started.IsZero() {
			secs = uint32(time.Since(k.started) / time.Second)
		}
		c.h.File(Entry{CallID: k.id, Chat: string(k.u.Chat), FromMe: k.outgoing, Time: k.began,
			Video: k.carried || k.u.Video, Status: k.u.Outcome, Seconds: secs})
	}
}

// end ends k with outcome: Ended if it was ever active, else phase.
func (c *Calls) end(k *call, phase abi.CallPhase, outcome abi.CallStatus) {
	if !live(k.u.Phase) {
		return
	}
	if !k.started.IsZero() {
		phase = abi.CallPhaseEnded
	}
	k.u.Phase = phase
	k.u.Outcome, k.u.HasOutcome = outcome, true
	c.emit(k)
}

func notPlaced(u abi.CallUpdate) abi.CallUpdate {
	u.Phase = abi.CallPhaseFailed
	u.Outcome, u.HasOutcome = abi.CallStatusFailed, true
	return u
}

func (c *Calls) emitUpdate(u abi.CallUpdate) {
	if w, ok := c.media.(updateWatcher); ok {
		w.OnUpdate(&u)
	}
	ev := abi.Event{Tag: abi.EventTagCall}
	ev.Call.Update = u
	c.h.Emit(&ev)
}

// --- sending -------------------------------------------------------------------

func (c *Calls) send(n waBinary.Node) {
	if err := c.cli.Send(c.h.Context, n); err != nil {
		slog.Warn("zapfast calls: a call stanza was not sent", "tag", n.GetChildren()[0].Tag, "err", err)
	}
}

func (c *Calls) terminate(k *call, to types.JID, reason string) {
	c.send(BuildTerminate(TerminateParams{CallID: k.id, To: to, ID: c.cli.NewID(), Creator: k.creator, Reason: reason}))
}

// targets are the devices a terminate goes to: the one that answered,
// else every one that rings, else the peer.
func (k *call) targets() []types.JID {
	switch {
	case !k.answering.IsEmpty():
		return []types.JID{k.answering}
	case len(k.ring) > 0:
		return k.ring
	}
	return []types.JID{k.peer}
}

// peerDevice is where an in-call stanza (mute, video) goes; empty before
// there is one.
func (k *call) peerDevice() types.JID { return k.answering }

func (c *Calls) command(k *call, cmd MediaCommand) {
	if !k.open {
		return
	}
	if err := c.media.Command(k.u.Generation, cmd); err != nil {
		slog.Warn("zapfast calls: a media command failed", "op", cmd.Op(), "err", err)
	}
}

func (c *Calls) videoState(k *call, state VideoState, dec string, orientation bool) {
	to := k.peerDevice()
	if to.IsEmpty() {
		return
	}
	c.send(BuildVideoState(VideoStateParams{CallID: k.id, To: to, ID: c.cli.NewID(), Creator: k.creator,
		State: state, Dec: dec, HasOrientation: orientation}))
}

// --- the commands --------------------------------------------------------------

// Accept takes a call command's cancel flag, as the demo does (zf_send).
func (c *Calls) Accept(cmd *abi.Command) error {
	switch cmd.Tag {
	case abi.CommandTagStartCall:
		if !c.flags.hold(cmd.StartCall.Cancel) {
			return errTooManyCalls
		}
	case abi.CommandTagCancelCall:
		if !c.flags.cancel(cmd.CancelCall.Token) {
			return errTooManyCalls
		}
	}
	return nil
}

var errTooManyCalls = errors.New("Too many calls are being placed at once")

// Command handles one call command on the engine's goroutine.
func (c *Calls) Command(cmd *abi.Command) {
	switch cmd.Tag {
	case abi.CommandTagStartCall:
		s := &cmd.StartCall
		c.start(string(s.Chat), s.Video, s.Generation, c.flags.held(s.Cancel))
	case abi.CommandTagCancelCall:
		// The flag is set (Accept); a call still placing reads it.
	case abi.CommandTagAnswerCall:
		c.answer()
	case abi.CommandTagDeclineCall:
		c.decline()
	case abi.CommandTagHangupCall:
		c.hangup()
	case abi.CommandTagSetCallMuted:
		c.setMuted(cmd.SetCallMuted.Muted)
	case abi.CommandTagSetCallCamera:
		c.setCamera(cmd.SetCallCamera.On)
	case abi.CommandTagSetCallMicrophone:
		s := &cmd.SetCallMicrophone
		c.pick(func(u *abi.CallUpdate) { u.Microphone, u.HasMicrophone = s.Device, s.HasDevice },
			microphoneCommand(device(s.Device, s.HasDevice)))
	case abi.CommandTagSetCallSpeaker:
		s := &cmd.SetCallSpeaker
		c.pick(func(u *abi.CallUpdate) { u.Speaker, u.HasSpeaker = s.Device, s.HasDevice },
			speakerCommand(device(s.Device, s.HasDevice)))
	case abi.CommandTagSetCallCameraDevice:
		s := &cmd.SetCallCameraDevice
		c.pick(func(u *abi.CallUpdate) { u.Camera, u.HasCamera = s.Device, s.HasDevice },
			cameraDeviceCommand(device(s.Device, s.HasDevice)))
	case abi.CommandTagRefreshCallDevices:
		c.refreshDevices()
	}
}

// refreshDevices lists the devices off the engine's goroutine (it opens
// each one) and publishes them.
func (c *Calls) refreshDevices() {
	c.h.Go("calls-devices", func(context.Context) {
		list, err := c.media.Devices()
		if err != nil {
			slog.Warn("zapfast calls: listing the devices", "err", err)
			return
		}
		c.h.Post(func() {
			c.devices = list
			ev := abi.Event{Tag: abi.EventTagCallDevices}
			ev.CallDevices.Devices = list
			c.h.Emit(&ev)
		})
	})
}

// cameraID is the camera the call uses: the one picked, else the first.
func (c *Calls) cameraID(k *call) any {
	if k.u.HasCamera {
		return string(k.u.Camera)
	}
	if len(c.devices.Cameras) > 0 {
		return string(c.devices.Cameras[0].ID)
	}
	return nil
}

func (c *Calls) pick(set func(*abi.CallUpdate), cmd MediaCommand) {
	k := c.call
	if k == nil || !live(k.u.Phase) {
		return
	}
	set(&k.u)
	c.command(k, cmd)
	c.emit(k)
}

// callable: a one-to-one chat (a phone number or a privacy id).
func callable(chat string) (types.JID, bool) {
	j, err := types.ParseJID(chat)
	if err != nil || j.User == "" || (j.Server != types.DefaultUserServer && j.Server != types.HiddenUserServer) {
		return types.EmptyJID, false
	}
	return j.ToNonAD(), true
}

func newCallID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b[1:]) // "00" and 15 random bytes, as WhatsApp Web makes one
	return hex.EncodeToString(b)
}

// start places a call to chat under the UI's generation.
func (c *Calls) start(chat string, video bool, generation uint64, cancel *cancelFlag) {
	placing := abi.CallUpdate{Generation: generation, Chat: []byte(chat), Direction: abi.CallDirectionOutgoing, Video: video, Phase: abi.CallPhasePlacing}
	refuse := func(text string) {
		if text != "" {
			c.h.Error(text)
		}
		c.flags.release(cancel)
		c.emitUpdate(notPlaced(placing))
	}
	if c.call != nil {
		slog.Warn("zapfast calls: refusing a new call: one is already up")
		refuse("")
		return
	}
	peer, ok := callable(chat)
	if !ok {
		refuse("Calls are one to one only")
		return
	}
	cli := c.cli
	if cli == nil {
		refuse("Zap is not connected to WhatsApp")
		return
	}
	if _, lid := cli.Own(); lid.IsEmpty() {
		refuse("Zap is not connected to WhatsApp")
		return
	}
	placing.CameraOn = video
	if cancel == nil {
		cancel = &cancelFlag{} // HangupCall sets it while the offer is made
	}
	k := &call{u: placing, id: newCallID(), peer: peer, outgoing: true, cancel: cancel,
		began: time.Now().Unix(), file: true, mlow: true, placing: true, carried: video}
	_, k.creator = cli.Own()
	c.call = k
	c.refreshDevices()
	c.h.Go("calls-offer", func(ctx context.Context) {
		p, err := c.offer(ctx, cli, k)
		c.h.Post(func() { c.placed(k, p, err) })
	})
}

// placement is what an offer that went out leaves: the peer's LID, the
// devices it rings, the callKey and the server's ack, with its relay.
type placement struct {
	peer types.JID
	ring []types.JID
	key  []byte
	ack  *waBinary.Node
}

var errCancelled = errors.New("the call was hung up before it was placed")

// offer resolves the peer's devices, encrypts a new callKey for each and
// sends the offer, off the engine's goroutine (OutgoingCall::start and
// place_call). It reads nothing of k but what start set.
func (c *Calls) offer(ctx context.Context, cli Client, k *call) (placement, error) {
	peer, err := cli.PeerLID(ctx, k.peer)
	if err != nil {
		return placement{}, err
	}
	devices, err := cli.Devices(ctx, peer)
	if err != nil {
		return placement{}, err
	}
	if len(devices) == 0 {
		return placement{}, errors.New("WhatsApp lists no device for this contact")
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	id := cli.NewID()
	keys, identity, err := cli.EncryptKey(ctx, devices, key, id)
	if err != nil {
		return placement{}, err
	}
	if len(keys) == 0 {
		return placement{}, errors.New("the call could not be encrypted for any of the contact's devices")
	}
	if k.cancel.cancelled.Load() {
		return placement{}, errCancelled
	}
	offer := BuildOffer(OfferParams{
		CallID: k.id, To: peer, Creator: k.creator, Keys: keys,
		PrivacyToken: cli.PrivacyToken(ctx, peer), Capability: offerCapability(k.u.Video, false),
		DeviceIdentity: identity, ID: id, MultiDevice: len(devices) > 1, Video: k.u.Video,
		AudioRates: AudioRates16k,
	})
	ack, err := cli.SendAwait(ctx, offer, c.timing.Ack)
	if err != nil {
		return placement{}, err
	}
	if code, ok := ack.Attrs["error"]; ok {
		return placement{}, errors.New("WhatsApp refused the offer (" + attrString(code) + ")")
	}
	cli.Offered(ctx, peer)
	return placement{peer: peer, ring: devices, key: key, ack: ack}, nil
}

func attrString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case types.JID:
		return v.String()
	}
	return "?"
}

// offerCapability is whatsapp-rust's offer_capability.
func offerCapability(video, standardOpus bool) []byte {
	switch {
	case video && standardOpus:
		return CapabilityStandardOpusVideoOffer
	case video:
		return CapabilityVideoOffer
	case standardOpus:
		return CapabilityStandardOpusOffer
	}
	return CapabilityOffer
}

// placed is the offer's answer, on the engine's goroutine.
func (c *Calls) placed(k *call, p placement, err error) {
	if c.call != k {
		return
	}
	k.placing = false
	switch {
	case errors.Is(err, errCancelled):
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	case err != nil:
		slog.Error("zapfast calls: the call could not be started", "err", err)
		c.h.Error("The call could not be started: " + err.Error())
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	k.peer, k.ring = p.peer, p.ring
	if k.cancel.cancelled.Load() {
		// Hung up while the offer was on its way: ended with the peer at
		// once, and filed as a call nobody answered.
		c.hangup()
		return
	}
	k.u.Phase = abi.CallPhaseDialing
	c.openMedia(k, p.key, p.ack, p.peer)
	k.ringTimer = c.after(c.timing.Ring, k.u.Generation, func(k *call) { c.hangup() })
	if live(k.u.Phase) {
		c.emit(k)
	}
}

// openMedia opens k's media over the relay node holds.
func (c *Calls) openMedia(k *call, key []byte, relay *waBinary.Node, peer types.JID) {
	_, own := c.cli.Own()
	str := func(b []byte, has bool) *string {
		if !has {
			return nil
		}
		s := string(b)
		return &s
	}
	var cam *string
	if id, ok := c.cameraID(k).(string); ok {
		cam = &id
	}
	err := c.media.Open(OpenParams{
		CallID: k.id, Generation: k.u.Generation, Outgoing: k.outgoing,
		SelfLID: own.String(), PeerLID: peer.String(), CallKey: key, Relay: relay,
		MLow: k.mlow, Video: k.u.Video, CameraOn: k.u.Video && k.u.CameraOn,
		Microphone: str(k.u.Microphone, k.u.HasMicrophone), Speaker: str(k.u.Speaker, k.u.HasSpeaker), Camera: cam,
	})
	if err != nil {
		slog.Error("zapfast calls: the media did not open", "err", err)
		c.h.Error("The call could not be connected: " + err.Error())
		for _, to := range k.targets() {
			c.terminate(k, to, "")
		}
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	k.open = true
	k.plane = k.u.Video
	if k.u.Muted {
		c.command(k, muteCommand(true))
	}
}

// answer takes the call rung in: preaccept, the callKey, accept, then the
// media (the facade's answer).
func (c *Calls) answer() {
	k := c.call
	if k == nil || k.outgoing || k.u.Phase != abi.CallPhaseIncoming {
		return
	}
	k.stopTimers()
	own := c.ownLID()
	o := k.offer
	video := k.u.Video
	k.mlow = MLowAfterPeer(true, o.MLow())
	standard := !k.mlow
	c.send(BuildPreacceptWithCapability(k.id, k.answering, k.creator, c.cli.NewID(), AudioRates16k,
		answerPreacceptCapability(video, standard), video))
	key, err := c.callKey(o, own)
	if err != nil {
		slog.Error("zapfast calls: the call could not be answered", "err", err)
		c.h.Error("The call could not be answered: " + err.Error())
		c.send(BuildReject(k.id, k.answering, k.creator, c.cli.NewID()))
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	p := AcceptParams{CallID: k.id, To: k.answering, ID: c.cli.NewID(), Creator: k.creator,
		AudioRates: AudioRates16k, Video: video}
	switch {
	case video:
		o.Metadata(&p)
	case standard:
		p.Capability = CapabilityStandardOpusOffer
	default:
		p.Capability = CapabilityOffer
	}
	if standard {
		p.VoipSettings = StandardOpusVoipSettings(false)
	}
	c.send(BuildAccept(p))
	k.answered = true
	k.u.Phase = abi.CallPhaseAccepted
	k.u.CameraOn = video
	c.openMedia(k, key, o.Action, k.creator)
	if live(k.u.Phase) {
		c.emit(k)
	}
}

func (c *Calls) ownLID() types.JID { _, lid := c.cli.Own(); return lid }

func (c *Calls) callKey(o *Signal, own types.JID) ([]byte, error) {
	enc, ok := o.OfferKey(own)
	if !ok {
		return nil, errors.New("the offer carried no key for this device")
	}
	return c.cli.DecryptKey(c.h.Context, enc, o.Creator, o.Time)
}

// answerPreacceptCapability is whatsapp-rust's.
func answerPreacceptCapability(video, standardOpus bool) []byte {
	switch {
	case video && standardOpus:
		return CapabilityStandardOpusOffer
	case standardOpus:
		return CapabilityStandardOpusPreaccept
	case video:
		return CapabilityOffer
	}
	return CapabilityPreaccept
}

func (c *Calls) decline() {
	k := c.call
	if k == nil || k.outgoing || k.u.Phase != abi.CallPhaseIncoming {
		return
	}
	c.send(BuildReject(k.id, k.answering, k.creator, c.cli.NewID()))
	k.u.Phase = abi.CallPhaseEnded
	k.u.Outcome, k.u.HasOutcome = abi.CallStatusDeclined, true
	c.emit(k)
}

func (c *Calls) hangup() {
	k := c.call
	if k == nil || !live(k.u.Phase) {
		return
	}
	if k.placing {
		// The offer is still being made: the flag stops it, or placed
		// ends what went out.
		k.cancel.cancelled.Store(true)
		return
	}
	if k.u.Phase == abi.CallPhaseIncoming {
		c.decline()
		return
	}
	for _, to := range k.targets() {
		c.terminate(k, to, "")
	}
	outcome := abi.CallStatusNoAnswer
	switch {
	case !k.started.IsZero():
		outcome = abi.CallStatusAnswered
	case !k.outgoing:
		outcome = abi.CallStatusMissed
	}
	k.u.Phase = abi.CallPhaseEnded
	k.u.Outcome, k.u.HasOutcome = outcome, true
	c.emit(k)
}

// Shutdown hangs the call up as the engine closes, and files it: ctx
// bounds the stanzas, the engine's own being over.
func (c *Calls) Shutdown(ctx context.Context) {
	c.h.Context = ctx
	c.hangup()
}

func (c *Calls) setMuted(muted bool) {
	k := c.call
	if k == nil || !live(k.u.Phase) || k.u.Muted == muted {
		return
	}
	k.u.Muted = muted
	c.command(k, muteCommand(muted))
	if to := k.peerDevice(); !to.IsEmpty() && k.answered {
		c.send(BuildMuteV2(k.id, to, k.creator, c.cli.NewID(), muted))
	}
	c.emit(k)
}

// setCamera turns our camera on or off. In a voice call, on asks the peer
// for video, or accepts the peer's ask.
func (c *Calls) setCamera(on bool) {
	k := c.call
	if k == nil || !live(k.u.Phase) || k.u.CameraOn == on {
		return
	}
	if !k.open || !k.answered {
		// Before the media: the call opens with the camera as chosen.
		k.u.CameraOn = on && k.u.Video
		c.emit(k)
		return
	}
	cam := c.cameraID(k)
	switch {
	case !on:
		c.videoState(k, VideoStopped, "", false)
		c.command(k, cameraOffCommand())
		k.u.CameraOn = false
	case k.u.VideoRequested:
		// The peer asked for video: we accept and start ours.
		c.command(k, videoStartCommand(cam, false))
		c.videoState(k, VideoUpgradeAccept, videoDecAccept, false)
		c.videoState(k, VideoEnabled, videoDecH264, false)
		k.plane, k.carried = true, true
		k.u.Video, k.u.VideoRequested, k.u.CameraOn = true, false, true
	case !k.u.Video:
		// We ask: our video waits off the wire for the peer's answer.
		c.command(k, videoStartCommand(cam, true))
		c.videoState(k, VideoUpgradeRequestV2, videoDecH264, false)
		k.plane, k.requested, k.u.CameraOn = true, true, true
		k.upgradeTimer = c.after(c.timing.Upgrade, k.u.Generation, func(k *call) {
			if k.requested {
				c.videoState(k, VideoUpgradeCancelByTimeout, "", false)
				c.stopVideo(k)
				c.emit(k)
			}
		})
	default:
		if k.plane {
			c.command(k, cameraOnCommand(cam))
		} else {
			c.command(k, videoStartCommand(cam, false))
			k.plane = true
		}
		c.videoState(k, VideoEnabled, videoDecH264, true)
		k.u.CameraOn = true
	}
	c.emit(k)
}

// stopVideo takes the video plane down: an upgrade refused or cancelled, a
// downgrade.
func (c *Calls) stopVideo(k *call) {
	if k.plane {
		c.command(k, videoStopCommand())
	}
	if k.upgradeTimer != nil {
		k.upgradeTimer.Stop()
		k.upgradeTimer = nil
	}
	k.plane, k.requested = false, false
	k.u.CameraOn, k.u.RemoteVideo, k.u.VideoRequested = false, false, false
}

// --- the signaling ---------------------------------------------------------------

// Signal applies one incoming call stanza.
func (c *Calls) Signal(s Signal) {
	if c.cli == nil {
		return
	}
	k := c.call
	if k != nil && k.id == s.CallID {
		if live(k.u.Phase) {
			c.signal(k, s)
		}
		return
	}
	if s.Tag != "offer" {
		return
	}
	c.offered(s)
}

// offered rings a new offer, or refuses it.
func (c *Calls) offered(s Signal) {
	pn, lid := c.cli.Own()
	ownFrom := pn
	if s.From.Server == types.HiddenUserServer {
		ownFrom = lid
	}
	c.send(BuildOfferAckReceipt(s.From, s.StanzaID, s.CallID, s.Creator, ownFrom))
	switch {
	case c.media == nil, c.call != nil:
		// Refused rather than dropped: a dropped offer rings on at the
		// caller's until its own timeout.
		c.send(rejectWith(s, c.cli.NewID(), RejectBusy))
		return
	case s.IsGroup():
		slog.Info("zapfast calls: refusing a group call's offer: group calls are not bridged")
		c.send(rejectWith(s, c.cli.NewID(), ""))
		return
	}
	chat := c.h.Chat(s.From.ToNonAD())
	if _, ok := callable(chat); !ok {
		c.send(rejectWith(s, c.cli.NewID(), ""))
		return
	}
	video := s.Video()
	k := &call{
		u: abi.CallUpdate{Generation: NextGeneration(), Chat: []byte(chat), Direction: abi.CallDirectionIncoming,
			Video: video, Phase: abi.CallPhaseIncoming},
		id: s.CallID, creator: s.Creator, peer: s.From.ToNonAD(), offer: &s, answering: s.From,
		began: s.Time.Unix(), file: true, carried: video,
	}
	if s.Time.IsZero() {
		k.began = time.Now().Unix()
	}
	c.call = k
	k.ringTimer = c.after(c.timing.Ring, k.u.Generation, func(k *call) {
		if k.u.Phase == abi.CallPhaseIncoming {
			c.end(k, abi.CallPhaseEnded, abi.CallStatusMissed)
		}
	})
	c.refreshDevices()
	c.emit(k)
}

// rejectWith is a <reject> to an offer, with a reason when it is a device's
// (busy) rather than the person's.
func rejectWith(s Signal, id, reason string) waBinary.Node {
	n := BuildReject(s.CallID, s.From, s.Creator, id)
	if reason != "" {
		n.GetChildren()[0].Attrs["reason"] = reason
	}
	return n
}

// signal applies a stanza of the call up.
func (c *Calls) signal(k *call, s Signal) {
	switch s.Tag {
	case "preaccept":
		if k.outgoing && k.u.Phase == abi.CallPhaseDialing {
			k.u.Phase = abi.CallPhaseRinging
			c.emit(k)
		}
	case "accept":
		if !k.outgoing || (k.u.Phase != abi.CallPhaseDialing && k.u.Phase != abi.CallPhaseRinging) {
			return
		}
		k.answering, k.answered = s.From, true
		codec := ""
		if !MLowAfterPeer(k.mlow, s.MLow()) {
			codec = "opus"
		}
		c.command(k, rekeyCommand(s.From.String(), codec))
		// The caller dismisses the callee's other devices.
		for _, d := range k.ring {
			if !sameDevice(d, s.From) {
				c.terminate(k, d, TerminateAcceptedElsewhere)
			}
		}
		if k.ringTimer != nil {
			k.ringTimer.Stop()
			k.ringTimer = nil
		}
		k.u.Phase = abi.CallPhaseConnecting
		c.active(k)
		c.emit(k)
	case "reject":
		if !k.outgoing {
			return
		}
		reason := s.Reason()
		if reason == RejectBusy || reason == RejectEnc {
			// One device that cannot take the call: the others ring on.
			kept := k.ring[:0]
			for _, d := range k.ring {
				if !sameDevice(d, s.From) {
					kept = append(kept, d)
				}
			}
			k.ring = kept
			if len(k.ring) > 0 {
				return
			}
		} else {
			for _, d := range k.ring {
				if !sameDevice(d, s.From) {
					c.terminate(k, d, TerminateRejectedElsewhere)
				}
			}
		}
		c.end(k, abi.CallPhaseFailed, rejectionOutcome(reason))
	case "terminate":
		c.terminated(k, s.Reason())
	case "video":
		c.peerVideo(k, s)
	}
}

func rejectionOutcome(reason string) abi.CallStatus {
	switch reason {
	case RejectBusy:
		return abi.CallStatusBusy
	case RejectEnc:
		return abi.CallStatusFailed
	}
	return abi.CallStatusDeclined
}

// terminated: the peer, or another of our devices, ended the call.
func (c *Calls) terminated(k *call, reason string) {
	connected := !k.started.IsZero()
	var outcome abi.CallStatus
	switch {
	case reason == TerminateAcceptedElsewhere:
		outcome = abi.CallStatusAnsweredElsewhere
	case reason == TerminateRejectedElsewhere:
		outcome = abi.CallStatusDeclinedElsewhere
	case !k.outgoing && k.u.Phase == abi.CallPhaseIncoming:
		outcome = abi.CallStatusMissed
	case reason == TerminateTimeout && connected:
		outcome = abi.CallStatusConnectionLost
	case connected:
		outcome = abi.CallStatusAnswered
	default:
		outcome = abi.CallStatusNoAnswer
	}
	phase := abi.CallPhaseFailed
	if k.u.Phase == abi.CallPhaseIncoming {
		phase = abi.CallPhaseEnded
	}
	c.end(k, phase, outcome)
}

// peerVideo applies the peer's <video state> (the registry's
// apply_peer_video_state, for a 1:1 call).
func (c *Calls) peerVideo(k *call, s Signal) {
	state, ok := s.VideoState()
	if !ok {
		return
	}
	if o, ok := s.Orientation(); ok {
		c.command(k, orientationCommand(o))
	}
	switch {
	case state.UpgradeRequest():
		switch {
		case k.requested:
			// Both asked: both have video.
			c.acceptedUpgrade(k)
			c.videoState(k, VideoUpgradeAccept, videoDecAccept, false)
			c.videoState(k, VideoEnabled, videoDecH264, false)
		case !k.u.CameraOn:
			k.u.VideoRequested = true
		default:
			return
		}
	case state == VideoUpgradeAccept:
		if !k.requested {
			return
		}
		c.acceptedUpgrade(k)
	case state == VideoEnabled:
		c.command(k, peerVideoCommand(true))
		if k.requested {
			c.acceptedUpgrade(k)
		}
	case state == VideoUpgradeReject || state == VideoUpgradeRejectByTimeout:
		if !k.requested {
			return
		}
		c.stopVideo(k)
	case state == VideoStopped || state == VideoPaused:
		c.command(k, peerVideoCommand(false))
		k.u.RemoteVideo = false
	case state.Inactive():
		// Disabled, cancelled, an error: no video either way.
		c.stopVideo(k)
		k.u.Video = false
	default:
		return
	}
	c.emit(k)
}

func (c *Calls) acceptedUpgrade(k *call) {
	if k.upgradeTimer != nil {
		k.upgradeTimer.Stop()
		k.upgradeTimer = nil
	}
	k.requested = false
	c.command(k, videoEnableCommand())
	k.carried = true
	k.u.Video, k.u.VideoRequested = true, false
}

// active makes the call active once both halves are there: the peer's (or
// our) answer and the relay.
func (c *Calls) active(k *call) {
	if !k.answered || !k.relayed || !k.started.IsZero() {
		return
	}
	if k.u.Phase != abi.CallPhaseConnecting && k.u.Phase != abi.CallPhaseAccepted {
		return
	}
	k.u.Phase = abi.CallPhaseActive
	k.started = time.Now()
}

// mediaEvent applies one of the media's events.
func (c *Calls) mediaEvent(ev MediaEvent) {
	k := c.call
	if ev.Kind == MediaEnded || k == nil || k.u.Generation != ev.Generation || !live(k.u.Phase) {
		return
	}
	switch ev.Kind {
	case MediaConnected:
		k.relayed = true
		c.active(k)
	case MediaFailed, MediaClosed:
		slog.Warn("zapfast calls: the call's media ended", "kind", ev.Kind, "reason", ev.Reason)
		k.open = false // the library ended it
		for _, to := range k.targets() {
			c.terminate(k, to, "")
		}
		if ev.Kind == MediaFailed {
			// Failed, whatever came before (the call's fail).
			k.u.Phase = abi.CallPhaseFailed
			k.u.Outcome, k.u.HasOutcome = abi.CallStatusFailed, true
			c.emit(k)
		} else {
			c.end(k, abi.CallPhaseFailed, abi.CallStatusConnectionLost)
		}
		return
	case MediaSilent:
		pa := abi.PeerAudio{Tag: abi.PeerAudioTagSilent, Silent: abi.PeerAudioSilent{Reason: silenceReason(ev.Reason)}}
		if k.u.HasPeerAudio && k.u.PeerAudio == pa {
			return
		}
		k.u.PeerAudio, k.u.HasPeerAudio = pa, true
	case MediaStalled:
		if k.u.HasPeerAudio && k.u.PeerAudio.Tag == abi.PeerAudioTagStalled {
			return
		}
		k.u.PeerAudio, k.u.HasPeerAudio = abi.PeerAudio{Tag: abi.PeerAudioTagStalled}, true
	case MediaAudible:
		if !k.u.HasPeerAudio {
			return
		}
		k.u.PeerAudio, k.u.HasPeerAudio = abi.PeerAudio{}, false
	case MediaRemoteVideo:
		if k.u.RemoteVideo {
			return
		}
		k.u.RemoteVideo = true
	case MediaCameraStopped:
		if !k.u.CameraOn {
			return
		}
		c.videoState(k, VideoStopped, "", false)
		k.u.CameraOn = false
	default:
		return
	}
	c.emit(k)
}

// --- cancel flags --------------------------------------------------------------

// cancelFlags are the flags the UI's call tokens stand for (zfcore's
// Shared::cancel_flag), as the demo keeps them: made on first use, by
// StartCall or an early CancelCall; a full table forgets its oldest flag no
// call holds.
type cancelFlags struct {
	mu    sync.Mutex
	flags []*cancelFlag
}

const maxCancelFlags = 16

type cancelFlag struct {
	token     uint64
	cancelled atomic.Bool
	holds     int // under mu
}

func (f *cancelFlags) get(token uint64) *cancelFlag {
	for _, c := range f.flags {
		if c.token == token {
			return c
		}
	}
	if len(f.flags) == maxCancelFlags {
		free := -1
		for i, c := range f.flags {
			if c.holds == 0 {
				free = i
				break
			}
		}
		if free < 0 {
			return nil
		}
		f.flags = append(f.flags[:free], f.flags[free+1:]...)
	}
	c := &cancelFlag{token: token}
	f.flags = append(f.flags, c)
	return c
}

func (f *cancelFlags) hold(token uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.get(token)
	if c == nil {
		return false
	}
	c.holds++
	return true
}

func (f *cancelFlags) cancel(token uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.get(token)
	if c == nil {
		return false
	}
	c.cancelled.Store(true)
	return true
}

func (f *cancelFlags) held(token uint64) *cancelFlag {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.flags {
		if c.token == token {
			return c
		}
	}
	return nil
}

func (f *cancelFlags) release(c *cancelFlag) {
	if c == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.holds > 0 {
		c.holds--
	}
}
