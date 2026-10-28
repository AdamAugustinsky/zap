// Ported from whatsapp-rust 5e72baf wacore/src/voip/engine.rs (the 1:1
// path; group media is not ported), MIT.

package engine

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"net/netip"
	"slices"

	"zapfast/gocore/voip/demux"
	"zapfast/gocore/voip/h264"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/mlow"
	"zapfast/gocore/voip/opus"
	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/sframe"
	"zapfast/gocore/voip/srtp"
	"zapfast/gocore/voip/stun"
)

// The engine's clocks and bounds (engine.rs).
const (
	// keepaliveMS re-sends the allocate and a WhatsApp ping: the relay
	// drops a client after about 4 s without traffic.
	keepaliveMS Millis = 1000
	// rtcpMS is the sender-report cadence voip_settings advertises.
	rtcpMS Millis = 1500
	// Shortest gaps between two PLIs, coalesced and immediate.
	minPeerKeyframeMS          Millis = 1000
	minImmediatePeerKeyframeMS Millis = 200
	// allocateTimeoutMS: a relay that never acks the allocate is wedged.
	allocateTimeoutMS Millis = 10_000
	// playoutMS hands the speaker 20 ms at a time.
	playoutMS    Millis = 20
	playoutDrain        = 320
	// The jitter buffer: prime to two 60 ms frames, cap near 150 ms.
	opusFrame60ms = 960
	playoutCap    = 2400
	playoutTarget = 1920
	maxPrimeTicks = 10
	// MicFrameSamples is the one frame size the engine takes: 60 ms at
	// 16 kHz.
	MicFrameSamples = 960
	// How many of our video packets are kept for the peer's NACKs, and how
	// often one is resent.
	sentVideoHistory = 512
	maxVideoResends  = 2
)

// mlowDTXCNG is MLow's one-byte comfort-noise token, sent for an exactly
// silent (muted) frame so the stream never gaps.
var mlowDTXCNG = []byte{0x90}

// TxIDSource supplies STUN transaction ids; production uses the OS RNG
// (RandTxIDs), tests a counter.
type TxIDSource interface {
	NextTxID() [12]byte
}

// RandTxIDs draws transaction ids from crypto/rand.
type RandTxIDs struct{}

func (RandTxIDs) NextTxID() (id [12]byte) {
	_, _ = rand.Read(id[:])
	return id
}

// ForeignCodec is standard Opus, which the platform supplies (gocore/av on
// macOS). Methods append to dst.
type ForeignCodec interface {
	Decode(dst []int16, payload []byte) ([]int16, error)
	Conceal(dst []int16, samples int) []int16
	Encode(dst []byte, pcm []int16) ([]byte, error)
}

// CodecSource says what decided a codec switch.
type CodecSource int

const (
	// Negotiated: the peer's capability (the accept) chose it.
	Negotiated CodecSource = iota
	// Content: the probe read it from the peer's packets.
	Content
)

// KeyframeUrgency is how a peer-keyframe request is rate-limited.
type KeyframeUrgency int

const (
	// Coalesced requests share a one-second interval per stream.
	Coalesced KeyframeUrgency = iota
	// Immediate requests (a decoder that failed) skip it, bounded at 200 ms.
	Immediate
)

// EventKind tells the engine's events apart.
type EventKind int

const (
	EventRelayAllocated EventKind = iota
	EventRelayAllocateFailed
	EventRelayAllocateTimedOut
	EventAudioSilent
	EventAudioReceptionStalled
	EventAudioCodecSwitched
	EventVideoKeyframeNeeded
	EventRTCPReceived
	EventForeignAudio
	EventOutboundMediaDropped
)

// Event is one lifecycle or diagnostic event; the fields its kind uses are
// set.
type Event struct {
	Kind EventKind
	// Code is a RelayAllocateFailed's STUN error code.
	Code uint16
	// SilentForMS, RTPReceived, FramesProduced and Reason are AudioSilent's
	// (SilentForMS also AudioReceptionStalled's).
	SilentForMS                 Millis
	RTPReceived, FramesProduced uint32
	Reason                      SilenceReason
	// From, To, Source and PacketsObserved are AudioCodecSwitched's.
	From, To        AudioCodec
	Source          CodecSource
	PacketsObserved uint32
	// ReportsAudio, ReportsVideo and RTCP are RTCPReceived's.
	ReportsAudio, ReportsVideo bool
	RTCP                       rtcp.Summary
	// Payload is ForeignAudio's undecodable Opus.
	Payload []byte
	// VideoAUs and Packets are OutboundMediaDropped's.
	VideoAUs, Packets uint32
}

// VideoFrame is a reassembled peer access unit.
type VideoFrame struct {
	Data        []byte
	Keyframe    bool
	Orientation uint8
	Timestamp   uint32
}

// OutputKind tells the engine's intents apart.
type OutputKind int

const (
	// OutTransmit: send Packet to the relay.
	OutTransmit OutputKind = iota
	// OutRetransmitVideo: resend a protected video packet (a NACK).
	OutRetransmitVideo
	// OutPlayout: 20 ms of 16 kHz PCM for the speaker.
	OutPlayout
	// OutVideo: a peer access unit for the decoder.
	OutVideo
	// OutEvent: an Event.
	OutEvent
	// OutTimeout: drained; arm a timer for Deadline (Never: none).
	OutTimeout
)

// Output is one intent the driver executes.
type Output struct {
	Kind     OutputKind
	Packet   []byte
	PCM      []int16
	Video    VideoFrame
	Event    Event
	Deadline Millis
}

// Setup errors.
var (
	ErrBadCallKey          = errors.New("the callKey is too short for E2E keys (need 32 bytes)")
	ErrBadEndpoint         = errors.New("the relay endpoint is not a valid IPv4 address")
	ErrBadAudioFormat      = errors.New("the audio format contains a zero timing or channel value")
	ErrUnsupportedPCMAudio = errors.New("PCM audio is supported only for mono 16 kHz / 60 ms MLow or standard Opus")
)

// Codec-switch errors.
var (
	ErrNotASiblingFormat = errors.New("only the MLow/Opus pair that shares one RTP timing can be swapped mid-call")
	ErrCodecLatched      = errors.New("the codec changed too many times; the decision is latched")
)

type pcmState struct {
	encoder      *mlow.Encoder
	decoder      *mlow.Decoder
	encoded      []byte
	decoded      []float32
	jitter       []int16
	priming      bool
	lastDecoded  bool
	primingTicks int
	packetSamps  int
	cap          int
}

type sentPacket struct {
	seq     uint16
	packet  []byte
	resends int
}

type videoPlane struct {
	pipe              *videoPipeline
	reception         rtcp.ReceptionStats
	active            bool
	sendGated         bool
	keyframeRequired  bool
	keyframeAnnounced bool
	askedSSRC         uint32
	askedAt           Millis
	haveAsked         bool
	expectedSSRC      uint32
	expectedSeq       uint16
	haveExpected      bool
	pendingSSRC       uint32
	pendingAt         Millis
	havePending       bool
	gapTS             uint32
	haveGap           bool
	sent              []sentPacket
}

func (v *videoPlane) forgetInboundStream() {
	v.pipe.resetReassembly()
	v.haveAsked, v.haveExpected, v.havePending, v.haveGap = false, false, false, false
}

// missingBefore counts the peer's video packets missing before this one,
// for the stream being reassembled.
func (v *videoPlane) missingBefore(ssrc uint32, seq uint16) uint16 {
	if in, ok := v.pipe.inboundSSRC(); !ok || in != ssrc {
		return 0
	}
	var missing uint16
	if v.haveExpected && v.expectedSSRC == ssrc {
		ahead := seq - v.expectedSeq
		if ahead >= 0x8000 {
			return 0
		}
		missing = ahead
	} else {
		v.havePending, v.haveGap = false, false
	}
	v.expectedSSRC, v.expectedSeq, v.haveExpected = ssrc, seq+1, true
	return missing
}

func (v *videoPlane) record(packet []byte) {
	if len(packet) < 4 {
		return
	}
	if len(v.sent) == sentVideoHistory {
		v.sent = v.sent[1:]
	}
	v.sent = append(v.sent, sentPacket{seq: uint16(packet[2])<<8 | uint16(packet[3]), packet: packet})
}

func (v *videoPlane) resend(seq uint16) ([]byte, bool) {
	if len(v.sent) == 0 {
		return nil, false
	}
	i := int(seq - v.sent[0].seq)
	if i >= len(v.sent) || v.sent[i].seq != seq || v.sent[i].resends >= maxVideoResends {
		return nil, false
	}
	v.sent[i].resends++
	return v.sent[i].packet, true
}

type mediaState struct {
	pipe            *mediaPipeline
	audio           AudioFormat // negotiated
	active          AudioFormat // in use now: audio or its sibling
	reception       rtcp.ReceptionStats
	callKey         []byte
	selfLID         string
	recvPeerLID     string
	tagLen          int
	videoStride     uint32
	video           *videoPlane
	probe           codecProbe
	foreign         ForeignCodec
	foreignPCM      []int16
	foreignEnc      []byte
	rtcpAnnounced   bool
	sframe          *sframe.Session
	sframeAuth      bool
	pcm             *pcmState
	playoutDeadline Millis
}

// Engine is the sans-I/O call engine: it owns the relay control plane (the
// STUN allocate, the keepalive, consent replies) and the media plane (MLow
// or Opus, E2E SRTP, SFrame, the playout jitter buffer, H.264 RTP). It owns
// no socket, clock or goroutine: the driver applies one input, drains
// PollOutput until OutTimeout, executes each intent and arms one timer.
// Not safe for concurrent use.
type Engine struct {
	callID            string
	direction         Direction
	relayToken        []byte
	endpointXOR       [6]byte
	relayAddr         netip.AddrPort
	integrityKey      []byte
	allocate          []byte
	allocTx           [12]byte
	tx                TxIDSource
	keepaliveDeadline Millis
	rtcpDeadline      Millis
	rtcpMonoOrigin    Millis
	rtcpWallOrigin    uint64
	allocateDeadline  Millis
	allocatePending   bool
	allocated         bool
	peerHasAnswered   bool
	started           bool
	terminated        bool
	selfPID           string
	media             *mediaState
	peerOrientation   uint8
	stats             Stats
	health            healthWatch
	outbox            []Output
}

// New builds the engine, deriving every key and the relay endpoint up
// front so a bad callKey or relay fails here rather than mid-call. tx nil
// is RandTxIDs.
func New(cfg Config, tx TxIDSource) (*Engine, error) {
	if tx == nil {
		tx = RandTxIDs{}
	}
	if !cfg.Audio.Valid() {
		return nil, ErrBadAudioFormat
	}
	if cfg.Audio != MLow16k60ms && cfg.Audio != Opus16k60ms {
		return nil, ErrUnsupportedPCMAudio
	}
	if len(cfg.CallKey) < 32 {
		return nil, ErrBadCallKey
	}
	xor, ok := stun.XORRelayEndpoint(cfg.RelayIP, cfg.RelayPort)
	ip, err := netip.ParseAddr(cfg.RelayIP)
	if !ok || err != nil || !ip.Is4() {
		return nil, ErrBadEndpoint
	}
	pipe, ok := newMediaPipeline(cfg.CallKey, cfg.SelfLID, cfg.PeerLID, cfg.SSRC, cfg.Audio.TimestampStep,
		cfg.WarpMITagLen, rtcp.WhatsAppCName(tx.NextTxID()))
	if !ok {
		return nil, ErrBadCallKey
	}
	pipe.rtp.SetPayloadType(cfg.Audio.PayloadType)
	pipe.rtp.SetMLowProfile(cfg.Audio.Profile == ProfileMLow)
	m := &mediaState{
		pipe: pipe, audio: cfg.Audio, active: cfg.Audio, callKey: append([]byte(nil), cfg.CallKey...),
		selfLID: cfg.SelfLID, recvPeerLID: cfg.PeerLID, tagLen: cfg.WarpMITagLen, videoStride: rtp.VideoTSStride15FPS,
		pcm: &pcmState{
			encoder: mlow.NewEncoder(), decoder: mlow.NewDecoder(), priming: true,
			packetSamps: opusFrame60ms, cap: playoutBounds(opusFrame60ms).cap,
		},
		playoutDeadline: Never,
	}
	if cfg.SFrame {
		m.sframe, _ = sframe.NewSession(cfg.CallKey, cfg.SelfLID, cfg.PeerLID)
	}
	if cfg.Video {
		v, ok := makeVideoPlane(cfg.CallID, m, rtcp.WhatsAppCName(tx.NextTxID()))
		if !ok {
			return nil, ErrBadCallKey
		}
		m.video = v
	}
	e := &Engine{
		callID: cfg.CallID, direction: cfg.Direction, relayToken: cfg.RelayToken, endpointXOR: xor,
		relayAddr: netip.AddrPortFrom(ip, cfg.RelayPort), integrityKey: cfg.IntegrityKey, tx: tx,
		rtcpDeadline: Never, selfPID: kdf.FormatParticipantID(cfg.SelfLID), media: m, health: newHealthWatch(),
	}
	// A video-from-start plane already needs an IDR; ask now.
	e.announceVideoKeyframe()
	return e, nil
}

func makeVideoPlane(callID string, m *mediaState, cname [rtcp.WhatsAppCNameLen]byte) (*videoPlane, bool) {
	ssrc := kdf.VideoParticipantSSRC(callID, kdf.FormatParticipantID(m.selfLID))
	p, ok := newVideoPipeline(m.callKey, m.selfLID, m.recvPeerLID, ssrc, m.videoStride, m.tagLen, cname)
	if !ok {
		return nil, false
	}
	return &videoPlane{pipe: p, active: true, keyframeRequired: true}, true
}

// WithForeignCodec installs standard Opus. It belongs at construction:
// without one, a peer on Opus is reported silent rather than faked.
func (e *Engine) WithForeignCodec(c ForeignCodec) *Engine {
	e.media.foreign = c
	return e
}

// CallID is the call's id.
func (e *Engine) CallID() string { return e.callID }

// RelayAddr is the relay endpoint the transport dials.
func (e *Engine) RelayAddr() netip.AddrPort { return e.relayAddr }

// Allocated reports whether the relay acknowledged our allocate.
func (e *Engine) Allocated() bool { return e.allocated }

// Terminated reports a terminal relay failure: the engine is inert and the
// driver tears the call down.
func (e *Engine) Terminated() bool { return e.terminated }

// Stats are the media counters.
func (e *Engine) Stats() Stats { return e.stats }

// ActiveAudioFormat is the audio grammar on the wire now.
func (e *Engine) ActiveAudioFormat() AudioFormat { return e.media.active }

func (e *Engine) push(o Output)     { e.outbox = append(e.outbox, o) }
func (e *Engine) transmit(p []byte) { e.push(Output{Kind: OutTransmit, Packet: p}) }
func (e *Engine) event(ev Event)    { e.push(Output{Kind: OutEvent, Event: ev}) }

// PollOutput drains one intent; OutTimeout once the queue is empty.
func (e *Engine) PollOutput() Output {
	if len(e.outbox) == 0 {
		e.outbox = e.outbox[:0]
		d, ok := e.PollTimeout()
		if !ok {
			d = Never
		}
		return Output{Kind: OutTimeout, Deadline: d}
	}
	o := e.outbox[0]
	e.outbox[0] = Output{}
	e.outbox = e.outbox[1:]
	return o
}

// PollTimeout is the next deadline; false before Start or after a
// terminal failure.
func (e *Engine) PollTimeout() (Millis, bool) {
	if !e.started || e.terminated {
		return 0, false
	}
	next := e.keepaliveDeadline
	if e.allocatePending && e.allocateDeadline != Never {
		next = min(next, e.allocateDeadline)
	}
	m := e.media
	next = min(next, m.playoutDeadline, e.rtcpDeadline)
	if v := m.video; v != nil && v.active && v.havePending {
		if in, ok := v.pipe.inboundSSRC(); ok && in == v.pendingSSRC {
			next = min(next, v.pendingAt)
		}
	}
	return min(next, e.health.deadline), true
}

// Start begins the session once the relay channel is open: monotonic now
// schedules, wallclock (Unix ms) stamps the sender reports. Idempotent.
func (e *Engine) Start(now Millis, wallclock uint64) {
	if e.started {
		return
	}
	e.started = true
	e.rtcpMonoOrigin, e.rtcpWallOrigin = now, wallclock
	if e.allocate == nil {
		e.allocTx = e.tx.NextTxID()
		e.allocate = stun.BuildWASMAllocate(&e.allocTx, e.relayToken, &e.endpointXOR, e.integrityKey, e.callID, e.selfPID)
	}
	e.transmit(e.allocate)
	e.keepaliveDeadline = now + keepaliveMS
	e.allocatePending = true
	e.allocateDeadline = now + allocateTimeoutMS
	e.media.playoutDeadline = now + playoutMS
}

// Timeout applies the deadline PollTimeout reported.
func (e *Engine) Timeout(now Millis) {
	if !e.terminated {
		e.onTimeout(now)
	}
}

// RelayPacket applies one packet from the relay channel.
func (e *Engine) RelayPacket(now Millis, p []byte) {
	if !e.terminated {
		e.onPacket(now, p)
	}
}

// MicFrame applies one 60 ms frame of 16 kHz mono PCM; any other length is
// dropped.
func (e *Engine) MicFrame(now Millis, pcm []int16) {
	if !e.terminated {
		e.onMic(pcm)
	}
}

// VideoFrame sends one Annex-B access unit at the source's RTP timestamp.
func (e *Engine) VideoFrame(now Millis, au []byte, timestamp uint32) {
	if e.terminated {
		return
	}
	if v := e.media.video; v != nil && v.active && !v.sendGated && !v.pipe.rtp.SetTimestamp(timestamp) {
		return
	}
	e.onVideo(au)
}

func nextTick(deadline, now, interval Millis) Millis {
	if s := deadline + interval; s > now {
		return s
	}
	return now + interval
}

func (e *Engine) onTimeout(now Millis) {
	if e.allocatePending && e.started && e.allocateDeadline != Never && now >= e.allocateDeadline {
		e.allocateDeadline, e.allocatePending, e.allocated, e.terminated = Never, false, false, true
		e.event(Event{Kind: EventRelayAllocateTimedOut})
		return
	}
	if e.started && now >= e.keepaliveDeadline {
		e.transmit(e.allocate)
		tx := e.tx.NextTxID()
		ping := stun.WhatsAppPing(&tx)
		e.transmit(ping[:])
		e.keepaliveDeadline = nextTick(e.keepaliveDeadline, now, keepaliveMS)
	}
	m := e.media
	if e.started && now >= m.playoutDeadline {
		frame := drainPlayout(m.pcm)
		m.playoutDeadline = nextTick(m.playoutDeadline, now, playoutMS)
		e.push(Output{Kind: OutPlayout, PCM: frame})
	}
	if e.started && e.allocated && now >= e.rtcpDeadline {
		e.emitSenderReports(now, e.rtcpWallOrigin+satSub(now, e.rtcpMonoOrigin))
		e.rtcpDeadline = nextTick(e.rtcpDeadline, now, rtcpMS)
	}
	if v := m.video; v != nil && v.active && v.havePending && now >= v.pendingAt {
		if in, ok := v.pipe.inboundSSRC(); ok && in == v.pendingSSRC {
			e.RequestPeerKeyframe(now, Coalesced)
		}
	}
	if a, ok := e.health.poll(now, &e.stats); ok {
		if a.stalled {
			e.event(Event{Kind: EventAudioReceptionStalled, SilentForMS: a.silentForMS})
		} else {
			e.event(Event{Kind: EventAudioSilent, SilentForMS: a.silentForMS, RTPReceived: a.rtpReceived,
				FramesProduced: a.framesProduced, Reason: a.reason})
		}
	}
}

func (e *Engine) announceAudioRTCPSession() {
	m := e.media
	if m.rtcpAnnounced {
		return
	}
	m.rtcpAnnounced = true
	e.transmit(m.pipe.audioSourceDescription())
}

// emitSenderReports sends audio's and (when it is on the wire) video's
// sender reports off one wall-clock sample, for lip sync.
func (e *Engine) emitSenderReports(now Millis, wall uint64) {
	m := e.media
	var report *rtcp.ReceptionReport
	if r, ok := m.reception.Report(now); ok {
		report = &r
	}
	e.transmit(m.pipe.audioSenderReport(wall, report))
	if v := m.video; v != nil && v.active && !v.sendGated {
		var vr *rtcp.ReceptionReport
		if r, ok := v.reception.Report(now); ok {
			vr = &r
		}
		e.transmit(v.pipe.senderReport(wall, vr))
	}
}

func (e *Engine) onPacket(now Millis, p []byte) {
	pk, err := demux.UnwrapGroupForwarding(p)
	if err != nil {
		e.stats.ForwardingEnvelopeRejected++
		return
	}
	p = pk.Payload
	switch demux.Classify(p) {
	case demux.KindStun:
		e.onSTUN(now, p)
	case demux.KindRtp:
		e.onRTP(now, p)
	case demux.KindRtcp:
		e.onRTCP(now, p)
	default:
		e.stats.RelayPacketUnclassified++
	}
}

func requestsKeyframe(fb []rtcp.Feedback, ssrc uint32) bool {
	for _, f := range fb {
		if f.PacketType != rtcp.PTPSFB {
			continue
		}
		switch f.FMT {
		case 1:
			if f.MediaSSRC == ssrc {
				return true
			}
		case 4:
			if f.MediaSSRC == ssrc {
				return true
			}
			for i := 0; i+8 <= len(f.FCI); i += 8 {
				if uint32(f.FCI[i])<<24|uint32(f.FCI[i+1])<<16|uint32(f.FCI[i+2])<<8|uint32(f.FCI[i+3]) == ssrc {
					return true
				}
			}
		}
	}
	return false
}

func nackedVideoSequences(fb []rtcp.Feedback, ssrc uint32) []uint16 {
	var out []uint16
	for _, f := range fb {
		if f.PacketType != rtcp.PTRTPFB || f.FMT != 1 || f.MediaSSRC != ssrc {
			continue
		}
		for i := 0; i+4 <= len(f.FCI); i += 4 {
			first := uint16(f.FCI[i])<<8 | uint16(f.FCI[i+1])
			mask := uint16(f.FCI[i+2])<<8 | uint16(f.FCI[i+3])
			out = append(out, first)
			for bit := range uint16(16) {
				if mask&(1<<bit) != 0 {
					out = append(out, first+bit+1)
				}
			}
		}
	}
	return out
}

func (e *Engine) onRTCP(now Millis, p []byte) {
	m := e.media
	audioSSRC := m.pipe.sendSSRC()
	plain, ok := m.pipe.unprotectRTCP(p)
	if !ok {
		return
	}
	sum, ok := rtcp.Summarize(plain)
	if !ok {
		return
	}
	needsKeyframe := false
	var videoSSRC uint32
	if v := m.video; v != nil {
		videoSSRC = v.pipe.sendSSRC()
		if requestsKeyframe(sum.Feedback, videoSSRC) {
			needsKeyframe, v.keyframeRequired = true, true
		}
		for _, seq := range nackedVideoSequences(sum.Feedback, videoSSRC) {
			e.stats.VideoNACKRequested++
			if v.active && !v.sendGated {
				if pkt, ok := v.resend(seq); ok {
					e.stats.VideoRetransmitted++
					e.push(Output{Kind: OutRetransmitVideo, Packet: pkt})
				}
			}
		}
	}
	if sender, sec, frac, ok := rtcp.SenderReportTiming(plain); ok {
		m.reception.ObserveSenderReport(sender, sec, frac, now)
		if v := m.video; v != nil {
			v.reception.ObserveSenderReport(sender, sec, frac, now)
		}
	}
	if needsKeyframe {
		e.announceVideoKeyframe()
	}
	e.event(Event{
		Kind: EventRTCPReceived, ReportsAudio: slices.Contains(sum.ReferencedSSRCs, audioSSRC),
		ReportsVideo: m.video != nil && slices.Contains(sum.ReferencedSSRCs, videoSSRC), RTCP: sum,
	})
}

func (e *Engine) onSTUN(now Millis, p []byte) {
	// Consent freshness: answer a binding request with a binding success.
	if t, ok := stun.MessageType(p); ok && t == stun.MsgBindingRequest {
		if id, ok := stun.TransactionID(p); ok && len(id) == 12 {
			var tx [12]byte
			copy(tx[:], id)
			e.transmit(stun.EncodeRequest(stun.MsgBindingSuccess, &tx, nil, e.integrityKey, true))
		}
	}
	id, ok := stun.TransactionID(p)
	matches := ok && e.allocate != nil && string(id) == string(e.allocTx[:])
	if e.allocatePending && matches && stun.IsAllocateOrBindingSuccess(p) {
		was := e.allocated
		e.allocatePending, e.allocated, e.allocateDeadline = false, true, Never
		if !was {
			e.event(Event{Kind: EventRelayAllocated})
			e.rtcpDeadline = now + rtcpMS
			// A callee was answered before its media existed; a caller
			// arms on the accept (PeerAnswered), whichever lands second.
			if e.direction == Incoming || e.peerHasAnswered {
				e.health.mediaStarted(now)
			}
			e.announceAudioRTCPSession()
		}
		return
	}
	if e.allocatePending && matches && e.allocateDeadline != Never && stun.IsAllocateError(p) {
		if code, ok := stun.ParseErrorCode(p); ok {
			e.allocateDeadline, e.allocatePending, e.allocated, e.terminated = Never, false, false, true
			e.event(Event{Kind: EventRelayAllocateFailed, Code: code})
		}
	}
}

func (e *Engine) onRTP(now Millis, p []byte) {
	h, ok := rtp.ParseHeader(p)
	if !ok {
		e.stats.RelayPacketUnclassified++
		return
	}
	m := e.media
	audioCarrier := false
	if hl, ok := rtp.HeaderByteLength(p); ok && len(p) >= hl+m.tagLen {
		if off, ok := rtp.ParseAudioPiggybackOffset(p); ok {
			audioCarrier = off > 0 && off < len(p)-hl-m.tagLen
		}
	}
	if h.PayloadType == rtp.PayloadTypeH264 {
		e.onVideoRTP(now, p, audioCarrier)
		return
	}
	// Arrivals, counted before authentication and the payload-type gate:
	// wrong keys are the deafest failure, and the watchdog must see it.
	e.health.mediaStarted(now)
	e.health.onRTP(now)
	if !m.active.AcceptsPayloadType(h.PayloadType) {
		e.stats.RTPPayloadTypeUnexpected++
		e.stats.LastUnexpectedPayloadType = h.PayloadType
		_, payload, err := m.pipe.unprotectAuxiliary(p)
		switch {
		case err == nil:
			e.stats.AuxiliaryRTPAuthenticated++
			if _, audio, ok := rtp.SplitAudioPiggyback(p, payload); ok {
				e.onPiggybackedAudio(now, audio)
			}
		case errors.Is(err, srtp.ErrAuthenticationFailed) && audioCarrier:
			e.stats.SRTPUnprotectFailed++
		}
		return
	}
	hdr, payload, err := m.pipe.unprotectAudio(p)
	switch {
	case err == nil:
		e.onAudioRTP(now, hdr, payload)
	case errors.Is(err, srtp.ErrAuthenticationFailed):
		e.stats.SRTPUnprotectFailed++
	case errors.Is(err, srtp.ErrInvalidPacket):
		e.stats.RelayPacketUnclassified++
	}
}

func (e *Engine) onVideoRTP(now Millis, p []byte, audioCarrier bool) {
	m := e.media
	var piggy []byte
	var lost uint16
	if audioCarrier {
		e.health.mediaStarted(now)
		e.health.onRTP(now)
	}
	var err error
	switch v := m.video; {
	case v != nil && v.active:
		var h rtp.Header
		var done []completedAU
		h, done, piggy, err = v.pipe.unprotectSplit(p)
		if err != nil {
			break
		}
		v.reception.Observe(h.SSRC, h.SequenceNumber, h.Timestamp, now, rtp.VideoClockRate)
		lost = v.missingBefore(h.SSRC, h.SequenceNumber)
		if lost > 0 {
			v.gapTS, v.haveGap = h.Timestamp, true
		}
		for _, c := range done {
			key := h264.AUIsKeyframe(c.au)
			if key && h264.AUHasIDR(c.au) && (!v.haveGap || int32(c.timestamp-v.gapTS) > 0) {
				v.havePending, v.haveGap = false, false
			}
			o := e.peerOrientation
			if c.orientation >= 0 {
				o = uint8(c.orientation)
			}
			e.push(Output{Kind: OutVideo, Video: VideoFrame{Data: c.au, Keyframe: key, Orientation: o, Timestamp: c.timestamp}})
		}
	case v != nil:
		var payload []byte
		_, payload, err = v.pipe.unprotectWithoutPicture(p)
		if err == nil {
			_, piggy, _ = rtp.SplitAudioPiggyback(p, payload)
		}
	default:
		var payload []byte
		_, payload, err = m.pipe.unprotectVideoWithoutPlane(p)
		if err == nil {
			_, piggy, _ = rtp.SplitAudioPiggyback(p, payload)
		}
	}
	switch {
	case err == nil:
		e.stats.VideoRTPReceived++
	case errors.Is(err, srtp.ErrAuthenticationFailed):
		e.stats.VideoSRTPUnprotectFailed++
		if audioCarrier {
			e.stats.SRTPUnprotectFailed++
		}
	}
	if piggy != nil {
		e.onPiggybackedAudio(now, piggy)
	}
	if lost > 0 {
		e.stats.VideoRTPLost += uint32(lost)
		e.RequestPeerKeyframe(now, Coalesced)
	}
}

// onPiggybackedAudio is an audio RTP packet carried inside another
// authenticated packet: plain RTP, admitted against the audio stream's
// replay state.
func (e *Engine) onPiggybackedAudio(now Millis, packet []byte) {
	h, ok := rtp.ParseHeader(packet)
	if !ok {
		e.stats.RelayPacketUnclassified++
		return
	}
	m := e.media
	if !m.active.AcceptsPayloadType(h.PayloadType) {
		e.stats.RTPPayloadTypeUnexpected++
		e.stats.LastUnexpectedPayloadType = h.PayloadType
		return
	}
	payload, ok := rtp.PlainPayload(packet)
	if !ok || !m.pipe.acceptCarriedAudio(h) {
		return
	}
	e.stats.PiggybackedAudioReceived++
	e.onAudioRTP(now, h, append([]byte(nil), payload...))
}

// onAudioRTP is the receive path from an authenticated, decrypted audio
// packet: reception statistics, SFrame, then the decoder its codec picks.
func (e *Engine) onAudioRTP(now Millis, h rtp.Header, payload []byte) {
	m := e.media
	e.stats.RTPReceived++
	if s, ok := m.reception.SSRC(); !ok || s != h.SSRC {
		m.probe.streamRestarted()
	}
	m.reception.Observe(h.SSRC, h.SequenceNumber, h.Timestamp, now, m.audio.ClockRate)
	// Bytes whose SFrame tag failed are whatever the wrong key made: never
	// evidence, never produced audio.
	unauthenticated := false
	encoded := payload
	if m.sframe != nil {
		plain, res := m.sframe.Decrypt(payload)
		switch {
		case res == sframe.Decrypted:
			m.sframeAuth = true
			encoded = plain
		case res == sframe.AuthFailed && m.sframeAuth:
			e.stats.SFrameDecryptFailed++
			unauthenticated = true
		}
	}
	codec := m.active.inboundCodec(h.PayloadType, encoded)
	var verdict AudioCodec
	var haveVerdict bool
	if !unauthenticated && codec == Opus && m.active.Codec == MLow && !m.active.payloadIsMLowEscape(encoded) {
		verdict, haveVerdict = e.observeCodec(encoded)
	}
	if codec == Opus {
		if m.foreign != nil {
			if m.active.payloadIsMLowEscape(encoded) {
				encoded = append([]byte(nil), encoded...)
				if err := depacketizeOpusFromMLow(encoded); err != nil {
					slog.Debug("zapfast voip: a malformed MLow Opus escape", "err", err)
					e.stats.AudioFramesConcealed++
					return
				}
			}
			samples := int(m.active.SamplesPerFrame)
			if s, ok := opus.Shape(encoded); ok {
				if n, ok := s.TotalSamples(m.active.ClockRate); ok {
					samples = int(n)
				}
			}
			var err error
			m.foreignPCM, err = m.foreign.Decode(m.foreignPCM[:0], encoded)
			decoded := err == nil
			if decoded {
				if !unauthenticated {
					e.stats.ForeignFramesDecoded++
					e.health.onAudioProduced()
				}
			} else {
				slog.Debug("zapfast voip: Opus decode failed", "err", err)
				m.foreignPCM = m.foreign.Conceal(m.foreignPCM[:0], samples)
				e.stats.AudioFramesConcealed++
			}
			pcm := m.pcm
			pcm.packetSamps, pcm.lastDecoded = max(samples, 1), decoded
			pcm.jitter = append(pcm.jitter, m.foreignPCM...)
			e.trimJitter()
			e.applyVerdict(verdict, haveVerdict)
			return
		}
		e.stats.AudioFramesWithoutDecoder++
		e.event(Event{Kind: EventForeignAudio, Payload: encoded})
		e.applyVerdict(verdict, haveVerdict)
		return
	}
	pcm := m.pcm
	red := int32(0)
	if h.PayloadType == rtp.PayloadTypeMLowRed {
		red = 1
	}
	pcm.decoder.SetRedundancy(red)
	pcm.decoded = pcm.decoder.AppendDecode(pcm.decoded[:0], encoded)
	pcm.packetSamps = pcm.decoder.LastPacketSamples()
	for _, s := range pcm.decoded {
		pcm.jitter = append(pcm.jitter, floatToInt16(s))
	}
	r := pcm.decoder.TakeFrameReport()
	if !unauthenticated {
		e.stats.AudioFramesDecoded += uint32(r.Decoded)
	}
	e.stats.AudioFramesConcealed += uint32(r.Concealed)
	e.stats.MLowOffPointDropped += uint32(r.OffPoint)
	e.stats.MLowInactiveOrSID += uint32(r.InactiveOrSID)
	// A SID is the peer saying it is silent: a healthy, muted stream.
	if !unauthenticated && (r.Decoded > 0 || r.InactiveOrSID > 0) {
		e.health.onAudioProduced()
	}
	pcm.lastDecoded = r.Decoded > 0 || r.InactiveOrSID > 0
	e.trimJitter()
	if r.Decoded == 0 && !unauthenticated {
		verdict, haveVerdict = e.observeCodec(encoded)
	}
	e.applyVerdict(verdict, haveVerdict)
}

func floatToInt16(s float32) int16 {
	v := s * 32767
	switch {
	case v != v:
		return 0
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}

func (e *Engine) observeCodec(payload []byte) (AudioCodec, bool) {
	m := e.media
	span, have := m.reception.FrameSpan()
	return m.probe.observe(payload, m.active.Codec, span, have, m.active.ClockRate, m.audio.TimestampStep)
}

// trimJitter bounds the jitter buffer on the feed side too, so a burst
// between two playout ticks cannot grow it without limit.
func (e *Engine) trimJitter() {
	pcm := e.media.pcm
	pcm.cap = effectivePlayoutCap(pcm.cap, pcm.packetSamps, len(pcm.jitter))
	if n := len(pcm.jitter) - pcm.cap; n > 0 {
		pcm.jitter = compactFront(pcm.jitter, n)
		e.stats.PlayoutTrimmedSamples += uint32(n)
	}
}

// compactFront drops s's first n samples, reusing its array.
func compactFront(s []int16, n int) []int16 {
	return s[:copy(s, s[n:])]
}

func (e *Engine) applyVerdict(codec AudioCodec, ok bool) {
	if !ok {
		return
	}
	if err := e.SwitchAudioCodec(codec, Content); err != nil {
		slog.Debug("zapfast voip: the peer's bytes say another codec; not switching", "codec", codec, "err", err)
	}
}

// SwitchAudioCodec swaps the payload grammar within the negotiated RTP
// timing (only the MLow/Opus pair at 16 kHz, 60 ms, PT 120 shares it, so
// nothing on the wire jumps). Idempotent; latched after a few changes.
func (e *Engine) SwitchAudioCodec(to AudioCodec, source CodecSource) error {
	m := e.media
	from := m.active.Codec
	target, ok := m.audio.SiblingFor(to)
	if !ok {
		if from == to {
			return nil
		}
		return ErrNotASiblingFormat
	}
	if m.active == target {
		return nil
	}
	if e.stats.CodecSwitches >= codecFlapLimit {
		return ErrCodecLatched
	}
	observed := e.stats.RTPReceived
	m.active = target
	m.pipe.rtp.SetMLowProfile(target.Profile == ProfileMLow)
	pcm := m.pcm
	pcm.decoder.Reset()
	// A content-forced switch follows packets that stopped becoming audio:
	// what is queued is concealment, so it goes.
	if source == Content && !pcm.lastDecoded {
		pcm.jitter, pcm.priming, pcm.primingTicks = pcm.jitter[:0], true, 0
	}
	e.stats.CodecSwitches++
	slog.Info("zapfast voip: the audio codec switched", "call", e.callID, "from", from, "to", to, "source", source)
	e.event(Event{Kind: EventAudioCodecSwitched, From: from, To: to, Source: source, PacketsObserved: observed})
	return nil
}

func (e *Engine) onMic(pcm []int16) {
	m := e.media
	if len(pcm) != MicFrameSamples {
		return
	}
	if m.active.Codec == Opus {
		if m.foreign == nil {
			e.stats.OutboundFramesWithoutEncoder++
			return
		}
		var err error
		m.foreignEnc, err = m.foreign.Encode(m.foreignEnc[:0], pcm)
		if err != nil {
			slog.Debug("zapfast voip: Opus encode failed", "err", err)
			e.stats.OutboundFramesWithoutEncoder++
			return
		}
		e.transmit(m.pipe.protectAudio(m.foreignEnc))
		e.stats.RTPSent++
		return
	}
	// An exactly silent frame is the OS's mute: send the cheap comfort
	// noise token so the peer's liveness timer stays fed.
	if allZero(pcm) {
		e.transmit(m.pipe.protectAudio(mlowDTXCNG))
		e.stats.RTPSent++
		return
	}
	var err error
	m.pcm.encoded, err = m.pcm.encoder.AppendEncodeInt16(m.pcm.encoded[:0], pcm)
	if err != nil {
		e.stats.OutboundFramesWithoutEncoder++
		return
	}
	e.transmit(m.pipe.protectAudio(m.pcm.encoded))
	e.stats.RTPSent++
}

func allZero(pcm []int16) bool {
	for _, s := range pcm {
		if s != 0 {
			return false
		}
	}
	return true
}

func (e *Engine) onVideo(au []byte) {
	v := e.media.video
	if v == nil || !v.active || v.sendGated {
		return
	}
	if v.keyframeRequired && !h264.AUHasIDR(au) {
		return
	}
	packets := v.pipe.protectVideo(au)
	if len(packets) == 0 {
		return
	}
	v.keyframeRequired, v.keyframeAnnounced = false, false
	for _, p := range packets {
		v.record(p)
		e.transmit(p)
	}
	e.stats.VideoRTPSent += uint32(len(packets))
}

// RekeyRecv points the receive side at the device that answered (its LID
// is in the callee's accept). False only for a malformed callKey.
func (e *Engine) RekeyRecv(lid string) bool {
	m := e.media
	if !m.pipe.rekeyRecv(m.callKey, lid) {
		return false
	}
	m.recvPeerLID = lid
	if v := m.video; v != nil {
		if !v.pipe.rekeyRecv(m.callKey, lid) {
			return false
		}
		v.haveAsked, v.haveExpected, v.havePending, v.haveGap = false, false, false, false
	}
	return true
}

// PeerAnswered is the first instant the peer's silence means anything; the
// watchdog arms once this and the allocation have both happened.
func (e *Engine) PeerAnswered(now Millis) {
	e.peerHasAnswered = true
	if e.allocated {
		e.health.mediaStarted(now)
	}
}

// VideoEnabled reports whether the video plane is up.
func (e *Engine) VideoEnabled() bool { v := e.media.video; return v != nil && v.active }

// VideoSendActive reports whether outbound video could reach the wire.
func (e *Engine) VideoSendActive() bool {
	v := e.media.video
	return v != nil && v.active && !v.sendGated
}

// VideoKeyframeRequired reports that outbound video is dropping access
// units for want of an IDR.
func (e *Engine) VideoKeyframeRequired() bool {
	v := e.media.video
	return v != nil && v.active && !v.sendGated && v.keyframeRequired
}

// announceVideoKeyframe asks the application for the IDR the plane waits
// on, once per requirement, while it can send one.
func (e *Engine) announceVideoKeyframe() {
	v := e.media.video
	if v == nil || !v.keyframeRequired || v.keyframeAnnounced || !v.active || v.sendGated {
		return
	}
	v.keyframeAnnounced = true
	e.event(Event{Kind: EventVideoKeyframeNeeded})
}

// RequireVideoKeyframe re-arms outbound recovery (a new camera source).
func (e *Engine) RequireVideoKeyframe() {
	if v := e.media.video; v != nil {
		v.keyframeRequired, v.keyframeAnnounced = true, false
	}
	e.announceVideoKeyframe()
}

// RequestPeerKeyframe asks the peer for a keyframe by PLI: false without a
// plane, before an inbound stream authenticated, or (Coalesced) too soon
// after the last request about the same stream, which is then retried at
// the interval's end.
func (e *Engine) RequestPeerKeyframe(now Millis, urgency KeyframeUrgency) bool {
	v := e.media.video
	if e.terminated || v == nil || !v.active {
		return false
	}
	in, ok := v.pipe.inboundSSRC()
	if !ok {
		return false
	}
	interval := minPeerKeyframeMS
	if urgency == Immediate {
		interval = minImmediatePeerKeyframeMS
	}
	if v.haveAsked && v.askedSSRC == in && satSub(now, v.askedAt) < interval {
		if urgency == Coalesced {
			v.pendingSSRC, v.pendingAt, v.havePending = in, v.askedAt+interval, true
		}
		return false
	}
	pli, ok := v.pipe.pli()
	if !ok {
		return false
	}
	v.askedSSRC, v.askedAt, v.haveAsked, v.havePending = in, now, true, false
	e.transmit(pli)
	e.stats.PeerKeyframeRequests++
	return true
}

// SetVideoTimestampStride sets the nominal RTP cadence of later access
// units without resetting any sequence state.
func (e *Engine) SetVideoTimestampStride(stride uint32) bool {
	if stride == 0 {
		return false
	}
	e.media.videoStride = stride
	if v := e.media.video; v != nil {
		v.pipe.rtp.SetTimestampStride(stride)
	}
	return true
}

// EnableVideo brings the video plane up with outbound allowed (a video
// call, an accepted upgrade); it also ungates a gated plane. A plane built
// earlier is reactivated, never rebuilt, so its SRTP index never repeats
// under the same key and SSRC.
func (e *Engine) EnableVideo() bool { return e.enableVideo(false) }

// EnableVideoGated brings the plane up holding outbound video off the wire
// until EnableVideo (an upgrade we asked for); inbound still decodes.
func (e *Engine) EnableVideoGated() bool { return e.enableVideo(true) }

func (e *Engine) enableVideo(gated bool) bool {
	m := e.media
	if v := m.video; v != nil {
		wasOff := !v.active
		recover := wasOff || (v.sendGated && !gated)
		v.active, v.sendGated = true, gated
		if wasOff {
			v.forgetInboundStream()
		}
		if recover {
			v.keyframeRequired, v.keyframeAnnounced = true, false
		}
		e.announceVideoKeyframe()
		return true
	}
	v, ok := makeVideoPlane(e.callID, m, rtcp.WhatsAppCName(e.tx.NextTxID()))
	if !ok {
		return false
	}
	v.sendGated = gated
	v.pipe.adoptRecvState(m.pipe)
	m.video = v
	e.announceVideoKeyframe()
	return true
}

// GateVideoOutbound holds outbound video off the wire (our camera off)
// while the peer's keeps decoding.
func (e *Engine) GateVideoOutbound() {
	if v := e.media.video; v != nil {
		v.sendGated = true
	}
}

// DisableVideo stops sending and decoding video; the plane keeps its SRTP
// state for a later EnableVideo.
func (e *Engine) DisableVideo() {
	if v := e.media.video; v != nil {
		v.active = false
	}
}

// SetPeerVideoOrientation is the peer's device orientation (0..3 quarter
// turns) from its <video> stanza, stamped on its access units.
func (e *Engine) SetPeerVideoOrientation(o uint8) { e.peerOrientation = o & 3 }

// NoteAudioSinkDropped counts playout the speaker refused.
func (e *Engine) NoteAudioSinkDropped(n uint32) { e.stats.AudioSinkDropped += n }

// NoteVideoSinkDropped counts access units the decoder refused.
func (e *Engine) NoteVideoSinkDropped(n uint32) { e.stats.VideoSinkDropped += n }

// NoteInboundDropped counts what the transport dropped before the engine.
func (e *Engine) NoteInboundDropped(n uint32) { e.stats.InboundPipeDropped += n }

type bounds struct{ target, cap int }

// playoutBounds scales the prime target and latency ceiling to the peer's
// packet: two packets of cushion, plus a drain slice.
func playoutBounds(packetSamps int) bounds {
	t := max(playoutTarget, 2*packetSamps)
	return bounds{target: t, cap: max(playoutCap, t+playoutDrain)}
}

// effectivePlayoutCap rises with the packet at once and falls by one
// packet per packet, so a shrinking cadence gives its latency back
// gradually.
func effectivePlayoutCap(current, packetSamps, queued int) int {
	want := playoutBounds(packetSamps).cap
	if want >= current || queued <= want {
		return want
	}
	return max(current-max(packetSamps, 1), want)
}

// drainPlayout takes one 20 ms slice: silence while priming to the target
// (bounded, so a partial buffer is flushed), then up to playoutDrain
// samples, re-priming on an underrun.
func drainPlayout(p *pcmState) []int16 {
	target := playoutBounds(p.packetSamps).target
	if n := len(p.jitter) - p.cap; n > 0 {
		p.jitter = compactFront(p.jitter, n)
	}
	frame := make([]int16, playoutDrain)
	if p.priming {
		reached := len(p.jitter) >= target
		timedOut := p.primingTicks >= maxPrimeTicks && len(p.jitter) > 0
		if !reached && !timedOut {
			if len(p.jitter) == 0 {
				p.primingTicks = 0
			} else {
				p.primingTicks++
			}
			return frame
		}
		p.priming, p.primingTicks = false, 0
	}
	n := copy(frame, p.jitter)
	p.jitter = compactFront(p.jitter, n)
	if n < playoutDrain {
		p.priming, p.primingTicks = true, 0
	}
	return frame
}
