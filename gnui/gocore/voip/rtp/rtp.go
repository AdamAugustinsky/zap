// Ported from whatsapp-rust 5e72baf wacore/src/voip/rtp.rs, MIT.

// Package rtp is WhatsApp's RTP framing: the 16-byte speech, 20-byte DTX and
// 28-byte video headers (extension profile 0xdebe), the Opus/MLow payload
// classifiers, the audio piggyback reader, and the send-side sequencers.
//
// pion/rtp is not used for the wire format: WhatsApp always sets X with its
// own 0xdebe profile (an empty block on speech), packs a fixed four-element
// one-byte-header block on video, and reads the frame-info element with its
// own precedence rules, none of which pion's Header round-trips byte for
// byte. The tests cross-check the standard fields against pion/rtp.
package rtp

import (
	"bytes"
	"encoding/binary"

	"zapfast/gocore/voip/warp"
)

// Payload types.
const (
	// PayloadTypeOpus is RFC 7587 Opus, used with the 48 kHz RTP clock.
	PayloadTypeOpus uint8 = 111
	// PayloadTypeWhatsAppAudio is the default audio payload type; the
	// negotiated codec decides whether its bytes are MLow or Opus.
	PayloadTypeWhatsAppAudio uint8 = 120
	PayloadTypeMLow                = PayloadTypeWhatsAppAudio
	// PayloadTypeMLowRed is MLow SplitRed redundancy (mlow-red-1).
	PayloadTypeMLowRed uint8 = 121
	// PayloadTypeMLowSeparate is MLow on a payload type of its own (mlow-1).
	PayloadTypeMLowSeparate uint8 = 122
	// PayloadTypeH264 is captured Android video.
	PayloadTypeH264 uint8 = 97
	// PayloadTypeAppData is the RTC app-data stream (reactions).
	PayloadTypeAppData uint8 = 119
)

// Video clock and strides.
const (
	VideoClockRate           uint32 = 90_000
	VideoTSStride15FPS              = VideoClockRate / 15
	VideoTSStride20FPS              = VideoClockRate / 20
	WhatsAppExtensionProfile uint16 = 0xdebe
	// RFC8285OneByteExtensionProfile is RFC 8285's one-byte header profile,
	// which WhatsApp's stack also reads its elements under.
	RFC8285OneByteExtensionProfile uint16 = 0xbede
)

// Header sizes.
const (
	FixedHeaderLen      = 12
	HeaderSize          = 16
	HeaderDTXSize       = 20
	VideoHeaderSize     = 28
	srtpAuthTagLen      = 10
	srtpAuthTagLenShort = 4
)

// Video media-frame-info values: upright IDR, and upright delta. The low two
// bits are rotation.
const (
	VideoMediaFrameInfoIDR   uint8 = 0x08
	VideoMediaFrameInfoDelta uint8 = 0x00
)

// ExtensionDTXWord is the extension word a DTX packet carries.
const ExtensionDTXWord uint32 = 0x3001_0000

// AudioPiggybackExtensionID is the one-byte element (`AP`) whose two-byte
// value is how many payload bytes are the packet's own media; the rest is a
// complete audio RTP packet the sender handed this one to carry.
const AudioPiggybackExtensionID = 7

const rtpVersion = 2

// VideoExtension is the four one-byte-header extensions WhatsApp's video
// sender emits (ids 3, 5, 6, 9 in that order).
type VideoExtension struct {
	MediaFrameInfo    uint8
	InitialBandwidth  uint16
	ShortOffset       int16
	TransportSequence uint16
}

func (e VideoExtension) encode() [12]byte {
	return [12]byte{
		0x30, e.MediaFrameInfo,
		0x51, byte(e.InitialBandwidth >> 8), byte(e.InitialBandwidth),
		0x61, byte(uint16(e.ShortOffset) >> 8), byte(e.ShortOffset),
		0x91, byte(e.TransportSequence >> 8), byte(e.TransportSequence),
		0,
	}
}

// Priming frames.
var (
	// OpusPrimingFrame1 is Android's first priming frame (18 bytes).
	OpusPrimingFrame1 = []byte{0x12, 0x36, 0x26, 0x2b, 0x4a, 0xc8, 0x2b, 0x09, 0xc9, 0x1f, 0x34, 0xc2, 0xd6, 0x7a, 0x01, 0x73, 0x1b, 0x2e}
	// OpusPrimingFrame1WASM is the WASM/Web caller's priming (24 bytes).
	OpusPrimingFrame1WASM = []byte{0x32, 0x36, 0x26, 0x2b, 0x4a, 0xcb, 0x1b, 0x5f, 0xba, 0x91, 0x68, 0x7e, 0xb8, 0x50, 0x93, 0x58, 0xe6, 0xd0, 0xa3, 0xa9, 0xd7, 0x1d, 0x81, 0x8c}
	// OpusPrimingFrame2 is the second priming frame (5 bytes).
	OpusPrimingFrame2 = []byte{0x90, 0xb8, 0x14, 0x14, 0xc4}
)

// IsWhatsAppOpusPayloadType reports an audio payload type.
func IsWhatsAppOpusPayloadType(pt uint8) bool {
	switch pt {
	case PayloadTypeOpus, PayloadTypeMLow, PayloadTypeMLowRed, PayloadTypeMLowSeparate:
		return true
	}
	return false
}

// IsOpusDTXPayload reports standard Opus DTX / comfort-noise and short
// warmup silence frames.
func IsOpusDTXPayload(p []byte) bool {
	n := len(p)
	switch {
	case n == 0:
		return false
	case n <= 2:
		return true
	case n <= 15:
		b0 := p[0]
		if b0&0xf8 == 0x08 || b0 == 0x0a {
			return true
		}
		return b0&0xf0 == 0x30 && n <= 6
	}
	return false
}

func isMLowDTXPayload(p []byte) bool { return len(p) > 0 && p[0]&0xc0 == 0x80 }

// IsOpusMLowSpeechPayload reports an MLow speech frame (20 ms 0x48..0x4f or
// 60 ms 0x50..0x57).
func IsOpusMLowSpeechPayload(p []byte) bool {
	if len(p) < 18 {
		return false
	}
	return p[0]&0xf8 == 0x48 || p[0]&0xf8 == 0x50
}

// IsOpusPrimingPayload reports one of the priming frames.
func IsOpusPrimingPayload(p []byte) bool {
	return bytes.Equal(p, OpusPrimingFrame1) || bytes.Equal(p, OpusPrimingFrame2)
}

// EstimateSRTPWireBytes estimates the on-wire SRTP size (header + Opus +
// auth tag) for ladder frame picking.
func EstimateSRTPWireBytes(opusPayload []byte) int {
	dtx := IsOpusDTXPayload(opusPayload)
	header := HeaderSize
	if dtx {
		header = HeaderDTXSize
	}
	tag := srtpAuthTagLen
	if dtx || IsOpusPrimingPayload(opusPayload) || len(opusPayload) <= 18 {
		tag = srtpAuthTagLenShort
	}
	return header + len(opusPayload) + tag
}

// Header is a WhatsApp RTP header.
type Header struct {
	Marker         bool
	PayloadType    uint8
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
	// HasExtensionWord: the header carries one 0xdebe extension word
	// (DTX/piggyback).
	HasExtensionWord bool
	ExtensionWord    uint32
	// HasVideo: WhatsApp video metadata, encoded as the 12-byte 0xdebe block.
	HasVideo bool
	Video    VideoExtension
}

// ByteSize is the encoded header size.
func (h Header) ByteSize() int {
	switch {
	case h.HasVideo:
		return VideoHeaderSize
	case h.HasExtensionWord:
		return HeaderDTXSize
	}
	return HeaderSize
}

// AppendTo appends the encoded header to out.
func (h Header) AppendTo(out []byte) []byte {
	size := h.ByteSize()
	var b [VideoHeaderSize]byte
	b[0] = rtpVersion << 6
	if size > FixedHeaderLen {
		b[0] |= 0x10
	}
	b[1] = h.PayloadType & 0x7f
	if h.Marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:], h.SequenceNumber)
	binary.BigEndian.PutUint32(b[4:], h.Timestamp)
	binary.BigEndian.PutUint32(b[8:], h.SSRC)
	if size >= 16 {
		binary.BigEndian.PutUint16(b[12:], WhatsAppExtensionProfile)
		switch {
		case h.HasVideo:
			b[15] = 3
		case h.HasExtensionWord:
			b[15] = 1
		}
	}
	if h.HasVideo {
		ext := h.Video.encode()
		copy(b[16:28], ext[:])
	} else if size >= 20 && h.HasExtensionWord {
		binary.BigEndian.PutUint32(b[16:], h.ExtensionWord)
	}
	return append(out, b[:size]...)
}

// Encode is the encoded header.
func (h Header) Encode() []byte { return h.AppendTo(make([]byte, 0, h.ByteSize())) }

// HeaderByteLength is the full on-wire header size (fixed 12 + CSRCs +
// optional extension block).
func HeaderByteLength(data []byte) (int, bool) {
	if len(data) < FixedHeaderLen || data[0]>>6 != rtpVersion {
		return 0, false
	}
	n := FixedHeaderLen + int(data[0]&0x0f)*4
	if len(data) < n {
		return 0, false
	}
	if data[0]&0x10 != 0 {
		if len(data) < n+4 {
			return 0, false
		}
		words := int(binary.BigEndian.Uint16(data[n+2:]))
		n += 4 + words*4
		if len(data) < n {
			return 0, false
		}
	}
	return n, true
}

// ExtensionProfileAndData returns the extension profile and its word-aligned
// payload. A valid packet without X returns hasExt false and ok true.
func ExtensionProfileAndData(data []byte) (profile uint16, ext []byte, hasExt, ok bool) {
	if len(data) < FixedHeaderLen || data[0]>>6 != rtpVersion {
		return 0, nil, false, false
	}
	off := FixedHeaderLen + int(data[0]&0x0f)*4
	if len(data) < off {
		return 0, nil, false, false
	}
	if data[0]&0x10 == 0 {
		return 0, nil, false, true
	}
	if len(data) < off+4 {
		return 0, nil, false, false
	}
	words := int(binary.BigEndian.Uint16(data[off+2:]))
	start := off + 4
	end := start + words*4
	if end > len(data) {
		return 0, nil, false, false
	}
	return binary.BigEndian.Uint16(data[off:]), data[start:end], true, true
}

// ParseMediaFrameInfo reads the frame-info byte (element id 3) regardless of
// the other 0xdebe elements. Like the captured readers it keeps the short,
// three-byte and extended forms apart and prefers them in that order; a
// truncated element ends the scan without discarding earlier metadata.
func ParseMediaFrameInfo(data []byte) (uint8, bool) {
	profile, ext, hasExt, ok := ExtensionProfileAndData(data)
	if !ok || !hasExt || profile != WhatsAppExtensionProfile {
		return 0, false
	}
	var info [3]uint8
	var have [3]bool
	for len(ext) > 0 {
		hdr := ext[0]
		ext = ext[1:]
		id := hdr >> 4
		if id == 0 {
			continue
		}
		n := int(hdr&0x0f) + 1
		if n > len(ext) {
			break
		}
		value := ext[:n]
		ext = ext[n:]
		if id == 3 {
			format := 2
			switch {
			case n <= 2:
				format = 0
			case n <= 5:
				format = 1
			}
			info[format], have[format] = value[0], true
		}
	}
	for i := range info {
		if have[i] {
			return info[i], true
		}
	}
	return 0, false
}

// ParseAudioPiggybackOffset is the payload offset at which a piggybacked
// audio packet starts, as the header states it. The reserved id 15 stops the
// scan as RFC 8285 says.
func ParseAudioPiggybackOffset(data []byte) (int, bool) {
	profile, ext, hasExt, ok := ExtensionProfileAndData(data)
	if !ok || !hasExt || (profile != WhatsAppExtensionProfile && profile != RFC8285OneByteExtensionProfile) {
		return 0, false
	}
	for len(ext) > 0 {
		hdr := ext[0]
		ext = ext[1:]
		id := hdr >> 4
		if id == 0 {
			continue
		}
		if id == 15 {
			break
		}
		n := int(hdr&0x0f) + 1
		if n > len(ext) {
			break
		}
		value := ext[:n]
		ext = ext[n:]
		if id == AudioPiggybackExtensionID && n == 2 {
			return int(binary.BigEndian.Uint16(value)), true
		}
	}
	return 0, false
}

// SplitAudioPiggyback splits a piggybacked audio packet off the decrypted
// payload of packet. It returns the packet's own media (RTP padding removed
// when the outer P bit is set) and the audio packet whole; ok is false when
// there is nothing to split and media is then the payload unchanged.
func SplitAudioPiggyback(packet, payload []byte) (media, audio []byte, ok bool) {
	offset, found := ParseAudioPiggybackOffset(packet)
	if !found || offset == 0 || offset >= len(payload) {
		return payload, nil, false
	}
	media, audio = payload[:offset:offset], payload[offset:]
	if len(packet) > 0 && packet[0]&0x20 != 0 && len(media) > 0 {
		if pad := int(media[len(media)-1]); pad <= len(media) {
			media = media[:len(media)-pad]
		}
	}
	return media, audio, true
}

// PlainPayload is the payload of an unprotected RTP packet: after the fixed
// header, CSRCs and extension block, without RTP padding. False when nothing
// is left.
func PlainPayload(packet []byte) ([]byte, bool) {
	n, ok := HeaderByteLength(packet)
	if !ok {
		return nil, false
	}
	payload := packet[n:]
	if packet[0]&0x20 != 0 {
		if len(payload) == 0 {
			return nil, false
		}
		pad := int(payload[len(payload)-1])
		if pad > len(payload) {
			return nil, false
		}
		payload = payload[:len(payload)-pad]
	}
	return payload, len(payload) > 0
}

// ParseVideoExtension decodes exactly the outgoing WhatsApp video extension
// set; any other 0xdebe layout returns false.
func ParseVideoExtension(data []byte) (VideoExtension, bool) {
	profile, e, hasExt, ok := ExtensionProfileAndData(data)
	if !ok || !hasExt || profile != WhatsAppExtensionProfile || len(e) != 12 ||
		e[0] != 0x30 || e[2] != 0x51 || e[5] != 0x61 || e[8] != 0x91 || e[11] != 0 {
		return VideoExtension{}, false
	}
	return VideoExtension{
		MediaFrameInfo:    e[1],
		InitialBandwidth:  binary.BigEndian.Uint16(e[3:]),
		ShortOffset:       int16(binary.BigEndian.Uint16(e[6:])),
		TransportSequence: binary.BigEndian.Uint16(e[9:]),
	}, true
}

// IsVersion2 reports a packet long enough for the fixed header with V=2.
func IsVersion2(data []byte) bool { return len(data) >= FixedHeaderLen && data[0]>>6 == rtpVersion }

// ParseHeader parses the fixed header fields and the exact video extension
// (the audio extension word is not decoded).
func ParseHeader(data []byte) (Header, bool) {
	if _, ok := HeaderByteLength(data); !ok {
		return Header{}, false
	}
	h := Header{
		Marker:         data[1]&0x80 != 0,
		PayloadType:    data[1] & 0x7f,
		SequenceNumber: binary.BigEndian.Uint16(data[2:]),
		Timestamp:      binary.BigEndian.Uint32(data[4:]),
		SSRC:           binary.BigEndian.Uint32(data[8:]),
	}
	h.Video, h.HasVideo = ParseVideoExtension(data)
	return h, true
}

// Stream is the send-side audio sequencer: seq starts at 1, the timestamp
// advances by samplesPerPacket.
type Stream struct {
	SSRC               uint32
	seq                uint16
	timestamp          uint32
	lastSent           uint32
	haveLastSent       bool
	samplesPerPacket   uint32
	speechStarted      bool
	speechStartMarkers bool
	mlowProfile        bool
	audioPacketIndex   int
	warpPiggyback      bool
	payloadType        uint8
}

// NewStream is an audio sequencer on the MLow profile.
func NewStream(ssrc, samplesPerPacket uint32, warpPiggyback bool) *Stream {
	return &Stream{
		SSRC: ssrc, seq: 1, samplesPerPacket: samplesPerPacket,
		speechStartMarkers: true, mlowProfile: true,
		warpPiggyback: warpPiggyback, payloadType: PayloadTypeMLow,
	}
}

// SetPayloadType changes the payload type without resetting timing.
func (s *Stream) SetPayloadType(pt uint8) bool {
	if pt > 127 {
		return false
	}
	s.payloadType = pt
	return true
}

// SetMLowProfile switches the DTX classifier and speech-start markers.
func (s *Stream) SetMLowProfile(enabled bool) {
	s.mlowProfile = enabled
	s.speechStartMarkers = enabled
}

// RTPTimestamp is the last emitted timestamp, for an RTCP sender report.
func (s *Stream) RTPTimestamp() uint32 {
	if s.haveLastSent {
		return s.lastSent
	}
	return s.timestamp
}

func (s *Stream) warpExtension(dtx bool) (uint32, bool) {
	if dtx {
		return ExtensionDTXWord, true
	}
	if !s.warpPiggyback {
		return 0, false
	}
	i := s.audioPacketIndex
	s.audioPacketIndex++
	return warp.AudioPiggybackExtensionFor(i, true, warp.PiggybackStartPacket)
}

func (s *Stream) emit(h Header) Header {
	s.lastSent, s.haveLastSent = h.Timestamp, true
	s.seq++
	s.timestamp += s.samplesPerPacket
	return h
}

// NextPacket is the header for the next audio packet.
func (s *Stream) NextPacket(payload []byte, marker bool) Header {
	var dtx bool
	if s.mlowProfile {
		dtx = isMLowDTXPayload(payload)
	} else {
		dtx = IsOpusDTXPayload(payload)
	}
	priming := IsOpusPrimingPayload(payload)
	speech := !dtx && !priming
	useMarker := marker || (s.speechStartMarkers && speech && !s.speechStarted)
	if dtx {
		s.speechStarted = false
	} else if speech {
		s.speechStarted = true
	}
	h := Header{Marker: useMarker, PayloadType: s.payloadType, SequenceNumber: s.seq, Timestamp: s.timestamp, SSRC: s.SSRC}
	h.ExtensionWord, h.HasExtensionWord = s.warpExtension(dtx)
	return s.emit(h)
}

// NextPreSpeechPacket advances seq/timestamp without a marker or speech latch.
func (s *Stream) NextPreSpeechPacket() Header {
	h := Header{PayloadType: s.payloadType, SequenceNumber: s.seq, Timestamp: s.timestamp, SSRC: s.SSRC}
	h.ExtensionWord, h.HasExtensionWord = s.warpExtension(false)
	return s.emit(h)
}

// VideoStream is the send-side video sequencer: every packet of an access
// unit shares a timestamp, which advances by the stride when the AU closes.
type VideoStream struct {
	SSRC              uint32
	seq               uint16
	timestamp         uint32
	lastSent          uint32
	haveLastSent      bool
	tsStride          uint32
	transportSequence uint16
}

// NewVideoStream fails on a zero stride.
func NewVideoStream(ssrc, tsStride uint32) (*VideoStream, bool) {
	if tsStride == 0 {
		return nil, false
	}
	return &VideoStream{SSRC: ssrc, tsStride: tsStride}, true
}

// RTPTimestamp is the last emitted timestamp, for an RTCP sender report.
func (s *VideoStream) RTPTimestamp() uint32 {
	if s.haveLastSent {
		return s.lastSent
	}
	return s.timestamp
}

// SetTimestampStride changes the cadence used after the current AU.
func (s *VideoStream) SetTimestampStride(stride uint32) bool {
	if stride == 0 {
		return false
	}
	s.tsStride = stride
	return true
}

// SetTimestamp sets the next AU's timestamp from a source capture clock,
// refusing one that goes backwards.
func (s *VideoStream) SetTimestamp(ts uint32) bool {
	if s.haveLastSent && ts-s.lastSent > 0x8000_0000 {
		return false
	}
	s.timestamp = ts
	return true
}

// NextVideoPacket is the header for the next packet of the current AU.
func (s *VideoStream) NextVideoPacket(lastInAU bool, mediaFrameInfo uint8) Header {
	h := Header{
		Marker: lastInAU, PayloadType: PayloadTypeH264, SequenceNumber: s.seq,
		Timestamp: s.timestamp, SSRC: s.SSRC, HasVideo: true,
		Video: VideoExtension{MediaFrameInfo: mediaFrameInfo, TransportSequence: s.transportSequence},
	}
	s.lastSent, s.haveLastSent = h.Timestamp, true
	s.seq++
	s.transportSequence++
	if lastInAU {
		s.timestamp += s.tsStride
	}
	return h
}
