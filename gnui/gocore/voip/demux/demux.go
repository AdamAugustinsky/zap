// Ported from whatsapp-rust 5e72baf wacore/src/voip/demux.rs, MIT.

// Package demux classifies what arrives on the relay channel (STUN, RTP,
// RTCP) and removes the group relay's forwarding envelope.
package demux

import (
	"errors"

	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
)

// Kind is a relay packet's class.
type Kind int

const (
	KindOther Kind = iota
	KindStun
	KindRtcp
	KindRtp
)

func (k Kind) String() string {
	switch k {
	case KindStun:
		return "stun"
	case KindRtcp:
		return "rtcp"
	case KindRtp:
		return "rtp"
	}
	return "other"
}

// Packet is a packet with the group relay's forwarding metadata removed.
type Packet struct {
	Payload        []byte
	GroupForwarded bool
	HeaderLen      int
}

// ErrGroupForwarding: a 0x09 packet used an unknown or truncated envelope.
var ErrGroupForwarding = errors.New("malformed group relay forwarding envelope")

// UnwrapGroupForwarding strips the capture-pinned forwarding header of a
// packet starting 0x09 (8, 12 or 18 bytes for versions 2, 4, 7); anything
// else passes through. A malformed 0x09 packet is an error, never passed on,
// so its metadata cannot reach the RTP/SRTP parsers.
func UnwrapGroupForwarding(data []byte) (Packet, error) {
	if len(data) == 0 || data[0] != 0x09 {
		return Packet{Payload: data}, nil
	}
	if len(data) < 2 {
		return Packet{}, ErrGroupForwarding
	}
	var n int
	switch data[1] {
	case 2:
		n = 8
	case 4:
		n = 12
	case 7:
		n = 18
	default:
		return Packet{}, ErrGroupForwarding
	}
	if n > len(data) {
		return Packet{}, ErrGroupForwarding
	}
	payload := data[n:]
	if len(payload) < 12 || payload[0]>>6 != 2 {
		return Packet{}, ErrGroupForwarding
	}
	return Packet{Payload: payload, GroupForwarded: true, HeaderLen: n}, nil
}

// Classify sorts a packet by RFC 5761's non-overlapping payload-type ranges;
// looking only at byte 0 would lose feedback packets whose count/FMT changes
// it and take X=0 video RTP for RTCP.
func Classify(data []byte) Kind {
	switch {
	case len(data) < 2:
		return KindOther
	case data[0]&0xc0 == 0:
		return KindStun
	case rtcp.IsRTCP(data):
		return KindRtcp
	case rtp.IsVersion2(data):
		return KindRtp
	}
	return KindOther
}
