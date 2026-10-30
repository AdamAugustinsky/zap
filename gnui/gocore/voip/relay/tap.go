// Ported from whatsapp-rust 5e72baf wacore/src/voip/tap.rs, MIT.

package relay

import (
	"context"
	"net/netip"
	"sync"

	"zapfast/gocore/voip/demux"
)

// The tap is an optional packet capture over the Transport seam, for
// debugging a live call: it decorates a transport or factory to record
// every packet crossing the seam, both directions, into a PacketTap, without
// touching the engine. It costs nothing unless a transport is wrapped.

// tapForwardCap is the depth of the tapped event stream. The forwarder
// drops media (after recording it) rather than block when the driver falls
// behind, like the relay's own reader.
const tapForwardCap = 256

// PacketDir is which way a captured packet was crossing the seam.
type PacketDir int

const (
	// Outbound: sent by us to the relay.
	Outbound PacketDir = iota
	// Inbound: received from the relay.
	Inbound
)

func (d PacketDir) String() string {
	if d == Outbound {
		return "Outbound"
	}
	return "Inbound"
}

// PacketTap is a sink for relay packets crossing the seam: a file, a pcap,
// a log, a buffer. It is called once per packet in both directions, inline
// on the send path and on the inbound forwarding hop, so it must be cheap
// and must not block; data is only valid during the call.
//
// It sees what reaches the seam: media the transport's own reader dropped
// under backpressure never got there. Past that point nothing is lost: a
// packet the forwarder later drops is recorded first.
type PacketTap interface {
	OnPacket(dir PacketDir, data []byte)
}

// TappedTransport decorates a Transport, recording every outbound packet
// before delegating and every inbound one before forwarding it.
type TappedTransport struct {
	inner  Transport
	tap    PacketTap
	events chan Event
	stop   chan struct{}
	once   sync.Once
}

// Tap wraps inner so both directions reach tap. A reconnect through the
// result stays tapped.
func Tap(inner Transport, tap PacketTap) *TappedTransport {
	t := &TappedTransport{
		inner:  inner,
		tap:    tap,
		events: make(chan Event, tapForwardCap),
		stop:   make(chan struct{}),
	}
	go tapForward(inner.Events(), t.events, tap, t.stop)
	return t
}

// Send implements Transport.
func (t *TappedTransport) Send(ctx context.Context, packet []byte) error {
	t.tap.OnPacket(Outbound, packet)
	return t.inner.Send(ctx, packet)
}

// Events implements Transport.
func (t *TappedTransport) Events() <-chan Event { return t.events }

// Close implements Transport.
func (t *TappedTransport) Close() error {
	err := t.inner.Close()
	t.once.Do(func() { close(t.stop) })
	return err
}

// Reconnect implements Transport. It follows the call to the new relay
// rather than refusing: a group update moving the relay is routine, and
// tapping a call must not change whether it survives one.
func (t *TappedTransport) Reconnect(ctx context.Context, endpoint netip.AddrPort) (Transport, error) {
	inner, err := t.inner.Reconnect(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return Tap(inner, t.tap), nil
}

// tapForward forwards inbound events from in to out, recording each packet
// to tap first. It drops media on a full out (loss tolerant), counts what it
// dropped and reports it as EventInboundDropped as soon as there is room,
// and ends (closing out) when in closes or stop is closed.
func tapForward(in <-chan Event, out chan<- Event, tap PacketTap, stop <-chan struct{}) {
	defer close(out)
	// Media this forwarder discarded, waiting for room to report it. A
	// running total: the moment there is something to report is the moment
	// out is full, so a blocking send would stall the tap behind the driver.
	var drops uint32
	send := func(ev Event) bool {
		select {
		case out <- ev:
			return true
		case <-stop:
			return false
		}
	}
	for ev := range in {
		if ev.Kind == EventPacket {
			tap.OnPacket(Inbound, ev.Packet)
		}
		// Before the packet, for the same reason as deliver: under the
		// backpressure this describes the driver frees one slot per packet.
		if drops > 0 {
			select {
			case out <- Event{Kind: EventInboundDropped, Dropped: drops}:
				drops = 0
			default:
			}
		}
		select {
		case out <- ev:
			continue
		default:
		}
		// Media is the only loss-tolerant thing here. STUN, the
		// lifecycle events and the transport's own drop reports wait.
		control := ev.Kind != EventPacket || demux.Classify(ev.Packet) == demux.KindStun
		if !control {
			if drops < ^uint32(0) {
				drops++
			}
			continue
		}
		// A terminal event is the last thing the driver reads, so the
		// report has to precede it.
		if ev.Kind == EventDisconnected && drops > 0 {
			if !send(Event{Kind: EventInboundDropped, Dropped: drops}) {
				return
			}
			drops = 0
		}
		if !send(ev) {
			return
		}
	}
	// The inner relay simply stopped, perhaps with the driver still
	// draining a full queue: the count still has to reach it.
	if drops > 0 {
		send(Event{Kind: EventInboundDropped, Dropped: drops})
	}
}

// TappedFactory decorates a Factory so every channel it connects is
// tapped in both directions.
type TappedFactory struct {
	Inner Factory
	Tap   PacketTap
}

// Connect implements Factory.
func (f TappedFactory) Connect(ctx context.Context) (Transport, error) {
	inner, err := f.Inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return Tap(inner, f.Tap), nil
}

// CapturedPacket is one packet an InMemoryTap recorded.
type CapturedPacket struct {
	Dir  PacketDir
	Data []byte
}

// InMemoryTap records every packet into memory, for tests and in-process
// inspection.
type InMemoryTap struct {
	mu       sync.Mutex
	captured []CapturedPacket
}

// OnPacket implements PacketTap.
func (m *InMemoryTap) OnPacket(dir PacketDir, data []byte) {
	m.mu.Lock()
	m.captured = append(m.captured, CapturedPacket{dir, append([]byte(nil), data...)})
	m.mu.Unlock()
}

// Captured is a snapshot of every captured packet, in capture order.
func (m *InMemoryTap) Captured() []CapturedPacket {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]CapturedPacket(nil), m.captured...)
}
