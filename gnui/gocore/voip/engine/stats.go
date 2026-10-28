// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/media_stats.rs
// and wacore/src/voip/codec_probe.rs, MIT.

package engine

import (
	"math"

	"zapfast/gocore/voip/opus"
)

// Millis is monotonic milliseconds, supplied by the driver: the engine
// never reads a clock.
type Millis = uint64

// Never is the deadline of no timer.
const Never Millis = math.MaxUint64

// Stats are a call's media counters, additive for its life. Every discard
// on the receive path lands in exactly one of them, which is what makes a
// silent call diagnosable.
type Stats struct {
	RTPReceived                  uint32
	RTPPayloadTypeUnexpected     uint32
	SRTPUnprotectFailed          uint32
	SFrameDecryptFailed          uint32
	AudioFramesDecoded           uint32
	AudioFramesConcealed         uint32
	MLowOffPointDropped          uint32
	MLowInactiveOrSID            uint32
	ForeignFramesDecoded         uint32
	AudioFramesWithoutDecoder    uint32
	OutboundFramesWithoutEncoder uint32
	PlayoutTrimmedSamples        uint32
	InboundPipeDropped           uint32
	AudioSinkDropped             uint32
	VideoSinkDropped             uint32
	PeerKeyframeRequests         uint32
	RelayPacketUnclassified      uint32
	ForwardingEnvelopeRejected   uint32
	CodecSwitches                uint16
	VideoRTPReceived             uint32
	VideoSRTPUnprotectFailed     uint32
	PiggybackedAudioReceived     uint32
	AuxiliaryRTPAuthenticated    uint32
	LastUnexpectedPayloadType    uint8
	VideoRTPLost                 uint32
	VideoNACKRequested           uint32
	VideoRetransmitted           uint32
	// RTPSent and VideoRTPSent count what went out (not in the Rust
	// counters, which watch reception; kept for the host's diagnostics).
	RTPSent, VideoRTPSent uint32
}

// SilenceReason is the dominant cause of an AudioSilent event.
type SilenceReason int

const (
	SilenceUnknown SilenceReason = iota
	SilenceNoDecoder
	SilenceAuthenticationFailing
	SilenceUnexpectedPayloadType
	SilenceCodecRejectingFrames
	SilenceCodecFlapping
)

func (r SilenceReason) String() string {
	switch r {
	case SilenceNoDecoder:
		return "no_decoder"
	case SilenceAuthenticationFailing:
		return "authentication_failing"
	case SilenceUnexpectedPayloadType:
		return "unexpected_payload_type"
	case SilenceCodecRejectingFrames:
		return "codec_rejecting_frames"
	case SilenceCodecFlapping:
		return "codec_flapping"
	}
	return "unknown"
}

// codecFlapLimit latches the codec after this many switches.
const codecFlapLimit = 4

// The audio-health watchdog's clocks.
const (
	healthTickMS           Millis = 500
	silentWindowMS         Millis = 2_000
	silentWindowMinPackets        = 12
	stallAfterMS           Millis = 3_000
	realarmMS              Millis = 10_000
)

type healthAlarm struct {
	stalled        bool
	silentForMS    Millis
	rtpReceived    uint32
	framesProduced uint32
	reason         SilenceReason
}

// healthWatch reports a call that carries packets but produces no audio
// (AudioSilent) or that stopped receiving (AudioReceptionStalled). It
// counts arrivals, not authenticated packets: wrong keys fail every packet
// later on, and that is the deafest failure it must reach.
type healthWatch struct {
	startedAt      Millis
	deadline       Millis
	windowStart    Millis
	windowRTP      uint32
	windowProduced uint32
	totalArrivals  uint32
	lastArrival    Millis
	windowStats    Stats
	silentSince    Millis
	haveSilent     bool
	lastAlarmAt    Millis
	haveLastAlarm  bool
	stallReported  bool
}

func newHealthWatch() healthWatch {
	return healthWatch{startedAt: Never, deadline: Never, lastArrival: Never}
}

func (h *healthWatch) mediaStarted(now Millis) {
	if h.startedAt != Never {
		return
	}
	h.startedAt, h.windowStart, h.deadline = now, now, now+healthTickMS
}

func (h *healthWatch) onRTP(now Millis) {
	h.windowRTP++
	h.totalArrivals++
	h.lastArrival = now
	h.stallReported = false
}

func (h *healthWatch) onAudioProduced() { h.windowProduced++ }

func (h *healthWatch) poll(now Millis, stats *Stats) (healthAlarm, bool) {
	if h.startedAt == Never || now < h.deadline {
		return healthAlarm{}, false
	}
	h.deadline = now + healthTickMS
	quiet := h.startedAt
	if h.lastArrival != Never {
		quiet = h.lastArrival
	}
	elapsed := satSub(now, quiet)
	if !h.stallReported && elapsed >= stallAfterMS {
		h.stallReported = true
		return healthAlarm{stalled: true, silentForMS: elapsed}, true
	}
	if h.totalArrivals == 0 || satSub(now, h.windowStart) < silentWindowMS {
		return healthAlarm{}, false
	}
	rtpN, produced := h.windowRTP, h.windowProduced
	began, opened := h.windowStart, h.windowStats
	h.windowStart, h.windowRTP, h.windowProduced, h.windowStats = now, 0, 0, *stats
	if produced > 0 || rtpN < silentWindowMinPackets {
		h.haveSilent, h.haveLastAlarm = false, false
		return healthAlarm{}, false
	}
	if !h.haveSilent {
		h.silentSince, h.haveSilent = began, true
	}
	if h.haveLastAlarm && satSub(now, h.lastAlarmAt) < realarmMS {
		return healthAlarm{}, false
	}
	h.lastAlarmAt, h.haveLastAlarm = now, true
	return healthAlarm{
		silentForMS: satSub(now, h.silentSince), rtpReceived: rtpN, framesProduced: produced,
		reason: dominantReason(stats, &opened),
	}, true
}

// dominantReason names what the window's counters blame, in the Rust
// order.
func dominantReason(now, then *Stats) SilenceReason {
	d := func(a, b uint32) uint32 { return uint32(satSub(uint64(a), uint64(b))) }
	rtpN := d(now.RTPReceived, then.RTPReceived)
	switch {
	case d(now.AudioFramesWithoutDecoder, then.AudioFramesWithoutDecoder) > 0:
		return SilenceNoDecoder
	case d(now.SRTPUnprotectFailed, then.SRTPUnprotectFailed) > rtpN:
		return SilenceAuthenticationFailing
	case d(now.SFrameDecryptFailed, then.SFrameDecryptFailed) > 0:
		return SilenceAuthenticationFailing
	case d(now.RTPPayloadTypeUnexpected, then.RTPPayloadTypeUnexpected) > rtpN:
		return SilenceUnexpectedPayloadType
	case now.CodecSwitches >= codecFlapLimit:
		return SilenceCodecFlapping
	case d(now.MLowOffPointDropped, then.MLowOffPointDropped) > 0 ||
		d(now.AudioFramesConcealed, then.AudioFramesConcealed) > 0:
		return SilenceCodecRejectingFrames
	}
	return SilenceUnknown
}

func satSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// agreeingPacketsToSwitch is how many consecutive packets must agree before
// the probe moves the call (about 180 ms).
const agreeingPacketsToSwitch = 3

// codecProbe decides, from the bytes, whether a peer speaks another audio
// grammar than it negotiated: it asks whether two statements by the peer
// agree, the duration its Opus header declares and the step its RTP
// timestamps advance by. It never reads the payload alone.
type codecProbe struct {
	agreeing int
	decided  bool
}

// observe feeds one authenticated payload; it returns Opus once.
func (p *codecProbe) observe(payload []byte, active AudioCodec, span uint32, haveSpan bool, clockRate, step uint32) (AudioCodec, bool) {
	if p.decided || active != MLow || !haveSpan {
		return 0, false
	}
	// At the negotiated 960-sample step MLow's durations cannot collide
	// with Opus's; at 10 and 20 ms they agree by construction.
	if span != step {
		p.agreeing = 0
		return 0, false
	}
	s, ok := opus.Shape(payload)
	var declared uint32
	if ok {
		declared, ok = s.TotalSamples(clockRate)
	}
	if !ok || declared != span {
		p.agreeing = 0
		return 0, false
	}
	p.agreeing++
	if p.agreeing < agreeingPacketsToSwitch {
		return 0, false
	}
	p.decided = true
	return Opus, true
}

// streamRestarted forgets the streak, not the decision.
func (p *codecProbe) streamRestarted() { p.agreeing = 0 }
