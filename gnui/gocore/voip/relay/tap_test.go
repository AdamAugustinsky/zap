// Ported from whatsapp-rust 5e72baf wacore/src/voip/tap.rs (its tests), MIT.

package relay

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"zapfast/gocore/voip/demux"
)

// fakeTransport records what it sends and can redial, handing back a fresh
// one like a real channel does.
type fakeTransport struct {
	mu     sync.Mutex
	sent   [][]byte
	events chan Event
}

func newFake() *fakeTransport { return &fakeTransport{events: make(chan Event, 16)} }

func (f *fakeTransport) Send(_ context.Context, p []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, append([]byte(nil), p...))
	f.mu.Unlock()
	return nil
}
func (f *fakeTransport) Events() <-chan Event { return f.events }
func (f *fakeTransport) Close() error         { return nil }
func (f *fakeTransport) Reconnect(context.Context, netip.AddrPort) (Transport, error) {
	return newFake(), nil
}

// settle waits until the forwarder has recorded n packets and had time to
// decide what to do with the last, so the backpressure tests see the
// queue the way a single-threaded executor would leave it.
func settle(t *testing.T, tap *InMemoryTap, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(tap.Captured()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("the forwarder recorded %d of %d packets", len(tap.Captured()), n)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
}

func packet(b ...byte) Event { return Event{Kind: EventPacket, Packet: b} }

func media() Event { return packet(0x90, 0x78, 0x01, 0x02) }

func collect(out <-chan Event) []Event {
	var seen []Event
	for ev := range out {
		seen = append(seen, ev)
	}
	return seen
}

// A group update can move a call to another relay; tapping a call must not
// decide whether it survives that.
func TestATappedCallSurvivesARelayMigrationAndStaysTapped(t *testing.T) {
	tap := &InMemoryTap{}
	tapped := Tap(newFake(), tap)
	endpoint := netip.MustParseAddrPort("203.0.113.7:3478")
	replacement, err := tapped.Reconnect(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Send(context.Background(), []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := tap.Captured(); len(got) != 1 || got[0].Dir != Outbound || !slices.Equal(got[0].Data, []byte{1, 2}) {
		t.Fatalf("captured %v", got)
	}
	if _, err := replacement.Reconnect(context.Background(), endpoint); err != nil {
		t.Fatal("a reconnected tapped transport must be able to migrate again")
	}
}

func TestTappedTransportRecordsOutboundThenDelegates(t *testing.T) {
	inner := newFake()
	tap := &InMemoryTap{}
	tapped := Tap(inner, tap)
	_ = tapped.Send(context.Background(), []byte{1, 2})
	_ = tapped.Send(context.Background(), []byte{3})
	got := tap.Captured()
	if len(got) != 2 || got[0].Dir != Outbound || !slices.Equal(got[0].Data, []byte{1, 2}) ||
		got[1].Dir != Outbound || !slices.Equal(got[1].Data, []byte{3}) {
		t.Fatalf("captured %v", got)
	}
	if len(inner.sent) != 2 {
		t.Fatalf("delegated %d", len(inner.sent))
	}
}

func TestTapForwardRecordsInboundAndForwardsEveryEvent(t *testing.T) {
	in := make(chan Event, 8)
	in <- packet(0xaa)
	in <- Event{Kind: EventConnected}
	in <- packet(0xbb, 0xcc)
	in <- Event{Kind: EventDisconnected, Reason: Closed()}
	close(in)
	out := make(chan Event, 8)
	tap := &InMemoryTap{}
	tapForward(in, out, tap, never)
	got := tap.Captured()
	if len(got) != 2 || got[0].Dir != Inbound || !slices.Equal(got[0].Data, []byte{0xaa}) ||
		!slices.Equal(got[1].Data, []byte{0xbb, 0xcc}) {
		t.Fatalf("captured %v", got)
	}
	seen := collect(out)
	if len(seen) != 4 || seen[1].Kind != EventConnected || seen[3].Kind != EventDisconnected {
		t.Fatalf("forwarded %v", seen)
	}
}

// The forwarder sits after the relay reader, so it must preserve STUN too
// while dropping media; recording happens before the drop decision.
func TestTapForwardPreservesStunButDropsMediaUnderBackpressure(t *testing.T) {
	in := make(chan Event, 8)
	in <- packet(0x90, 0x78, 0x01, 0x02)
	in <- packet(0x90, 0x78, 0x03, 0x04)
	in <- packet(0x00, 0x01, 0x05, 0x06)
	close(in)
	out := make(chan Event, 1)
	tap := &InMemoryTap{}
	done := make(chan struct{})
	go func() { tapForward(in, out, tap, never); close(done) }()
	settle(t, tap, 2)
	a, b := <-out, <-out
	if a.Kind != EventPacket || a.Packet[0] != 0x90 {
		t.Fatalf("first = %v", a)
	}
	for b.Kind == EventInboundDropped {
		b = <-out
	}
	if b.Kind != EventPacket || demux.Classify(b.Packet) != demux.KindStun {
		t.Fatalf("second = %v, want the STUN", b)
	}
	collect(out)
	<-done
	if n := len(tap.Captured()); n != 3 {
		t.Fatalf("captured %d, want every packet even ones later dropped", n)
	}
}

func TestTapForwardReportsTheMediaItDroppedAsInboundDropped(t *testing.T) {
	in := make(chan Event, 8)
	for range 4 {
		in <- media()
	}
	out := make(chan Event, 2)
	tap := &InMemoryTap{}
	go tapForward(in, out, tap, never)
	settle(t, tap, 4)
	seen := []Event{<-out, <-out}
	in <- media()
	close(in)
	seen = append(seen, collect(out)...)
	var reported uint32
	for _, ev := range seen {
		if ev.Kind == EventInboundDropped {
			reported += ev.Dropped
		}
	}
	if reported != 2 {
		t.Fatalf("reported %d, got %v", reported, seen)
	}
}

// Lifecycle is not media: a Disconnected meeting a full queue waits, and
// the drop report precedes it.
func TestTapForwardNeverDropsTheDisconnect(t *testing.T) {
	in := make(chan Event, 8)
	in <- media()
	in <- media()
	in <- Event{Kind: EventDisconnected, Reason: Closed()}
	close(in)
	out := make(chan Event, 1)
	tap := &InMemoryTap{}
	go tapForward(in, out, tap, never)
	settle(t, tap, 2)
	seen := collect(out)
	last := seen[len(seen)-1]
	if last.Kind != EventDisconnected {
		t.Fatalf("the disconnect must arrive last, got %v", seen)
	}
	report := slices.IndexFunc(seen, func(e Event) bool { return e.Kind == EventInboundDropped })
	if report < 0 || report > len(seen)-2 {
		t.Fatalf("the drop report has to precede the disconnect, got %v", seen)
	}
}

func TestTapForwardFlushesItsLastDropsWhenTheRelayJustStops(t *testing.T) {
	in := make(chan Event, 8)
	for range 3 {
		in <- media()
	}
	close(in)
	out := make(chan Event, 1)
	tap := &InMemoryTap{}
	go tapForward(in, out, tap, never)
	settle(t, tap, 3)
	seen := collect(out)
	if !slices.ContainsFunc(seen, func(e Event) bool { return e.Kind == EventInboundDropped && e.Dropped > 0 }) {
		t.Fatalf("the media dropped before the relay stopped has to be reported, got %v", seen)
	}
}

func TestTapForwardFlushesItsDropReportUnderSustainedBackpressure(t *testing.T) {
	in := make(chan Event, 8)
	in <- media()
	in <- media()
	out := make(chan Event, 1)
	tap := &InMemoryTap{}
	go tapForward(in, out, tap, never)
	settle(t, tap, 2)
	seen := []Event{<-out}
	in <- media()
	close(in)
	seen = append(seen, collect(out)...)
	if !slices.ContainsFunc(seen, func(e Event) bool { return e.Kind == EventInboundDropped && e.Dropped == 1 }) {
		t.Fatalf("the drop report must reach the driver, got %v", seen)
	}
}

func TestTappedFactoryTapsWhatItConnects(t *testing.T) {
	tap := &InMemoryTap{}
	inner := newFake()
	tr, err := TappedFactory{Inner: factoryFunc(func(context.Context) (Transport, error) { return inner, nil }), Tap: tap}.
		Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inner.events <- packet(0x80, 0x78)
	if ev := <-tr.Events(); ev.Kind != EventPacket {
		t.Fatalf("event = %v", ev)
	}
	_ = tr.Send(context.Background(), []byte{7})
	if got := tap.Captured(); len(got) != 2 || got[0].Dir != Inbound || got[1].Dir != Outbound {
		t.Fatalf("captured %v", got)
	}
	_ = tr.Close()
}

type factoryFunc func(context.Context) (Transport, error)

func (f factoryFunc) Connect(ctx context.Context) (Transport, error) { return f(ctx) }
