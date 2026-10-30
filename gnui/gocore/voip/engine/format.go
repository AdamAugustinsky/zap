// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/audio_format.rs
// and wacore/src/voip/audio.rs, MIT.

package engine

import (
	"errors"

	"zapfast/gocore/voip/opus"
	"zapfast/gocore/voip/rtp"
)

// AudioCodec is the codec that produces a call's audio payloads.
type AudioCodec int

const (
	// MLow is WhatsApp's proprietary codec (gocore/voip/mlow).
	MLow AudioCodec = iota
	// Opus is standard Opus, which the engine encodes and decodes through
	// a [ForeignCodec].
	Opus
)

func (c AudioCodec) String() string {
	if c == MLow {
		return "mlow"
	}
	return "opus"
}

// AudioProfile is the RTP payload grammar: how payloads are framed and how
// DTX and speech markers are read.
type AudioProfile int

const (
	// ProfileMLow is MLow's profile, whose escape carries CELT inside it.
	ProfileMLow AudioProfile = iota
	// ProfileStandardOpus is RFC Opus bytes.
	ProfileStandardOpus
)

// AudioFormat is a codec together with its RTP timing.
type AudioFormat struct {
	Codec           AudioCodec
	Profile         AudioProfile
	SignalingRate   uint32
	SampleRate      uint32
	Channels        uint8
	SamplesPerFrame uint32
	ClockRate       uint32
	TimestampStep   uint32
	PayloadType     uint8
}

// The formats a 1:1 call uses: the swappable pair at 16 kHz, 60 ms and
// payload type 120, which differ only in codec and profile.
var (
	MLow16k60ms = AudioFormat{
		Codec: MLow, Profile: ProfileMLow, SignalingRate: 16_000, SampleRate: 16_000, Channels: 1,
		SamplesPerFrame: 960, ClockRate: 16_000, TimestampStep: 960, PayloadType: rtp.PayloadTypeMLow,
	}
	Opus16k60ms = AudioFormat{
		Codec: Opus, Profile: ProfileStandardOpus, SignalingRate: 16_000, SampleRate: 16_000, Channels: 1,
		SamplesPerFrame: 960, ClockRate: 16_000, TimestampStep: 960, PayloadType: rtp.PayloadTypeWhatsAppAudio,
	}
)

// Valid reports whether no timing or channel value is zero.
func (f AudioFormat) Valid() bool {
	return f.SignalingRate != 0 && f.SampleRate != 0 && f.Channels != 0 && f.SamplesPerFrame != 0 &&
		f.ClockRate != 0 && f.TimestampStep != 0 && f.PayloadType <= 127
}

// AcceptsPayloadType reports whether an inbound packet of pt belongs to
// this format: its own type, and MLow's redundancy and separate types on
// the MLow profile.
func (f AudioFormat) AcceptsPayloadType(pt uint8) bool {
	return pt == f.PayloadType ||
		f.Profile == ProfileMLow && (pt == rtp.PayloadTypeMLowRed || pt == rtp.PayloadTypeMLowSeparate)
}

// SiblingFor is the format of codec with this one's RTP timing, or false
// when the pair does not share it.
func (f AudioFormat) SiblingFor(codec AudioCodec) (AudioFormat, bool) {
	c := MLow16k60ms
	if codec == Opus {
		c = Opus16k60ms
	}
	same := c.PayloadType == f.PayloadType && c.ClockRate == f.ClockRate && c.TimestampStep == f.TimestampStep &&
		c.SamplesPerFrame == f.SamplesPerFrame && c.SampleRate == f.SampleRate && c.Channels == f.Channels &&
		c.SignalingRate == f.SignalingRate
	return c, same
}

// isMLowEmbeddedOpus is MLow's escape marker: the top two bits set.
func isMLowEmbeddedOpus(p []byte) bool { return len(p) > 0 && p[0]&0xc0 == 0xc0 }

// inboundCodec is the codec of an inbound payload once the profile is
// known.
func (f AudioFormat) inboundCodec(pt uint8, payload []byte) AudioCodec {
	switch {
	case f.Profile == ProfileStandardOpus:
		return Opus
	case pt != rtp.PayloadTypeMLowRed && isMLowEmbeddedOpus(payload):
		return Opus
	}
	return MLow
}

// payloadIsMLowEscape tells MLow's escape apart from native CELT, which
// also sets the marker bits: an escape read back as Opus can never claim
// the negotiated packet duration, while native CELT claims it exactly.
func (f AudioFormat) payloadIsMLowEscape(payload []byte) bool {
	if f.Profile != ProfileMLow || !isMLowEmbeddedOpus(payload) {
		return false
	}
	if s, ok := opus.Shape(payload); ok {
		if n, ok := s.TotalSamples(f.ClockRate); ok && n == f.TimestampStep {
			return false
		}
	}
	return true
}

var errNotMLowEscape = errors.New("packet is not an MLOW CELT escape")

// depacketizeOpusFromMLow restores the RFC Opus TOC of an MLow escape
// before a stock Opus decoder sees it.
func depacketizeOpusFromMLow(packet []byte) error {
	if len(packet) == 0 {
		return errors.New("empty Opus packet")
	}
	toc := packet[0]
	if toc&0xc0 != 0xc0 {
		return errNotMLowEscape
	}
	multiple := toc&1 != 0
	if multiple && (len(packet) < 2 || packet[1]&0x3f == 0 || packet[1]&0x3f > 48) {
		return errors.New("invalid Opus frame packing")
	}
	config := 16 + (toc>>2)&0x0f
	stereo := (toc >> 1) & 1
	code := byte(0)
	if multiple {
		code = 3
	}
	packet[0] = config<<3 | stereo<<2 | code
	return nil
}
