// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/transport.rs, MIT.

// Package relay is the call's media transport to one WhatsApp relay: a
// pre-negotiated WebRTC data channel (id 0) over SCTP over DTLS over UDP,
// carrying STUN, RTP and RTCP as opaque binary messages (native.go), the
// seam the call engine talks to (this file), and a packet-capture decorator
// over that seam (tap.go).
//
// The protocol stack is pion's: pion/dtls for the DTLS client (a fresh
// self-signed certificate, server certificate unverified, since media keys
// come from the callKey and not from this handshake), pion/sctp for the
// association and pion/datachannel for the channel's framing. Everything
// above the channel (STUN allocation, SRTP) is the engine's.
package relay

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
)

// ReasonKind says why a relay media channel ended.
type ReasonKind int

const (
	// ReasonUnknown: the transport did not report why.
	ReasonUnknown ReasonKind = iota
	// ReasonClosed: closed cleanly (a local Close, or the relay or peer
	// closed it).
	ReasonClosed
	// ReasonReadError: a transport-level read or I/O error ended it.
	ReasonReadError
)

// DisconnectReason is why a relay media channel ended. The relay is a
// datagram pipe with no close handshake of its own, so the set is small.
type DisconnectReason struct {
	Kind ReasonKind
	// Msg is the error text of a ReasonReadError.
	Msg string
}

// Closed is the reason of a clean close.
func Closed() DisconnectReason { return DisconnectReason{Kind: ReasonClosed} }

// ReadError is the reason of a transport failure.
func ReadError(msg string) DisconnectReason {
	return DisconnectReason{Kind: ReasonReadError, Msg: msg}
}

func (r DisconnectReason) String() string {
	switch r.Kind {
	case ReasonClosed:
		return "channel closed"
	case ReasonReadError:
		return "read error: " + r.Msg
	default:
		return "unknown"
	}
}

// EventKind tells the events of a relay channel apart.
type EventKind int

const (
	// EventConnected: the channel is open and carries packets.
	EventConnected EventKind = iota
	// EventPacket: one packet (STUN, RTP or RTCP) arrived from the relay.
	EventPacket
	// EventInboundDropped: inbound media the transport discarded under
	// backpressure before the call saw it. A silent drop would be
	// indistinguishable from a peer that stopped sending.
	EventInboundDropped
	// EventDisconnected: the channel was lost. It is the last event; the
	// event channel closes after it.
	EventDisconnected
)

func (k EventKind) String() string {
	switch k {
	case EventConnected:
		return "Connected"
	case EventPacket:
		return "Packet"
	case EventInboundDropped:
		return "InboundDropped"
	case EventDisconnected:
		return "Disconnected"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event is one event pushed from a relay media channel. Consumers should
// ignore kinds they do not know: the set has grown before.
type Event struct {
	Kind EventKind
	// Packet is the packet of an EventPacket. The receiver owns it.
	Packet []byte
	// Dropped is the count of an EventInboundDropped.
	Dropped uint32
	// Reason is the reason of an EventDisconnected.
	Reason DisconnectReason
}

func (e Event) String() string {
	switch e.Kind {
	case EventPacket:
		return fmt.Sprintf("Packet(%x)", e.Packet)
	case EventInboundDropped:
		return fmt.Sprintf("InboundDropped(%d)", e.Dropped)
	case EventDisconnected:
		return "Disconnected(" + e.Reason.String() + ")"
	}
	return e.Kind.String()
}

// ErrClosed is returned by Send on a channel that has been closed or has
// ended.
var ErrClosed = errors.New("relay media channel closed")

// Transport is a dumb packet pipe to one relay endpoint. It knows nothing
// of STUN or RTP framing. VoIP is loss tolerant, so an implementation may
// drop a media packet under backpressure rather than block.
type Transport interface {
	// Send sends one packet to the relay. The transport keeps its own copy.
	Send(ctx context.Context, packet []byte) error
	// Events is the stream of what arrives. It starts with EventConnected
	// and closes after EventDisconnected.
	Events() <-chan Event
	// Close hangs up: the queued packets go out, then the channel is torn
	// down politely, bounded so a relay that stopped answering cannot hold
	// teardown open.
	Close() error
	// Reconnect replaces this channel with one connected to a newly
	// selected relay endpoint. It carries the address alone: the engine
	// puts any rotated relay credentials inside the STUN it builds, which
	// is all a native transport needs.
	Reconnect(ctx context.Context, endpoint netip.AddrPort) (Transport, error)
}

// Factory creates a Transport connected to one relay endpoint.
type Factory interface {
	Connect(ctx context.Context) (Transport, error)
}

// EndpointParams is everything a platform needs to reach one relay
// endpoint. A native dialer uses the address alone; a browser would also
// need the ICE credentials of a synthetic SDP answer.
//
// ICEUfrag is derived from the selected endpoint's <auth_token> (indexed by
// auth_token_id), never from the <token> beside it, which is the STUN
// allocation credential (RELAY-TOKEN). ICEPwd is the relay <key> in the
// ASCII base64 form it arrived in, the bytes the engine keys STUN
// MESSAGE-INTEGRITY with. It is live credential material and is redacted
// from String and GoString.
type EndpointParams struct {
	Addr     netip.AddrPort
	ICEUfrag string
	ICEPwd   string
}

func (p EndpointParams) String() string {
	return fmt.Sprintf("EndpointParams{Addr: %s, ICEUfrag: %q, ICEPwd: [redacted]}", p.Addr, p.ICEUfrag)
}

// GoString keeps %#v from printing the relay key.
func (p EndpointParams) GoString() string { return p.String() }
