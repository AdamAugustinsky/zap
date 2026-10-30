// Ported from whatsapp-rust 5e72baf src/voip/transport/native.rs (its stack,
// loopback_relay, media_channel_reliability and relay_failures tests), MIT.

package relay

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
)

// testTiming is production timing with a short connect bound, so a failing
// handshake fails the test quickly.
var testTiming = func() timing {
	tm := defaultTiming
	tm.connect = 5 * time.Second
	return tm
}()

// fakeRelay accepts relay media channels on 127.0.0.1: the accepting end of
// the same stack the client dials (DTLS server, SCTP server, the
// pre-negotiated id 0 channel), standing in for a WhatsApp relay.
type fakeRelay struct {
	ln       net.Listener
	accepted chan *relaySide
}

// relaySide is one accepted client.
type relaySide struct {
	st      *stack
	packets chan []byte
	// ended receives the read error that ended the channel.
	ended chan error
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	cfg, err := dtlsConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := dtls.Listen("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRelay{ln: ln, accepted: make(chan *relaySide, 4)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.accept(conn.(*dtls.Conn))
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return r
}

func (r *fakeRelay) addr() netip.AddrPort {
	return r.ln.Addr().(*net.UDPAddr).AddrPort()
}

func (r *fakeRelay) accept(conn *dtls.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return
	}
	assoc, err := sctp.Server(sctpConfig(conn))
	if err != nil {
		_ = conn.Close()
		return
	}
	stream, dc, err := openMediaStream(assoc)
	if err != nil {
		_ = assoc.Close()
		return
	}
	side := &relaySide{
		st:      &stack{udp: nopConn{}, dtls: conn, assoc: assoc, stream: stream, dc: dc},
		packets: make(chan []byte, 1024),
		ended:   make(chan error, 1),
	}
	go func() {
		buf := make([]byte, sctpMaxMessage)
		for {
			n, ppi, err := stream.ReadSCTP(buf)
			if err != nil {
				side.ended <- err
				close(side.packets)
				return
			}
			if ppi == sctp.PayloadTypeWebRTCBinary {
				side.packets <- append([]byte(nil), buf[:n]...)
			}
		}
	}()
	r.accepted <- side
}

func (r *fakeRelay) next(t *testing.T) *relaySide {
	t.Helper()
	select {
	case s := <-r.accepted:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("the relay accepted no channel")
		return nil
	}
}

// nopConn stands in for the UDP flow the listener owns on the relay side.
type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }

func (s *relaySide) send(t *testing.T, p []byte) {
	t.Helper()
	if err := s.st.write(p); err != nil {
		t.Fatalf("relay write: %v", err)
	}
}

func (s *relaySide) recv(t *testing.T) []byte {
	t.Helper()
	select {
	case p, ok := <-s.packets:
		if !ok {
			t.Fatal("relay channel ended")
		}
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("the relay received nothing")
		return nil
	}
}

func dialTest(t *testing.T, addr netip.AddrPort) *Channel {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTiming.connect)
	defer cancel()
	c, err := connectRelayMedia(ctx, addr, testTiming)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// nextEvent is the next event that is not a packet the test ignores.
func nextEvent(t *testing.T, c *Channel, want EventKind) Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatalf("event stream closed before %v", want)
			}
			if ev.Kind == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %v event", want)
		}
	}
}

// DTLS, the SCTP association and the media stream all come up, with the
// dialling and accepting halves built from the same stack.
func TestBothHalvesReachAnOpenMediaChannel(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	relay.next(t)
	if ev := <-c.Events(); ev.Kind != EventConnected {
		t.Fatalf("first event = %v, want Connected", ev)
	}
}

// A message written on one side comes out the other, both ways.
func TestMediaCrossesTheOpenChannel(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	side := relay.next(t)
	if err := c.Send(context.Background(), []byte("hello relay")); err != nil {
		t.Fatal(err)
	}
	if got := side.recv(t); string(got) != "hello relay" {
		t.Fatalf("relay got %q", got)
	}
	side.send(t, []byte("hello client"))
	if ev := nextEvent(t, c, EventPacket); string(ev.Packet) != "hello client" {
		t.Fatalf("client got %q", ev.Packet)
	}
}

// The relay ends a call by resetting the media stream while the
// association stays up; the channel must end as a clean close.
func TestAPeerStreamResetClosesTheChannel(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	side := relay.next(t)
	if err := side.st.stream.Close(); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, c, EventDisconnected); ev.Reason.Kind != ReasonClosed {
		t.Fatalf("reason = %v, want Closed: a peer reset is the peer hanging up", ev.Reason)
	}
	if err := c.Send(context.Background(), []byte{0x90, 0x78}); !errors.Is(err, ErrClosed) {
		t.Fatalf("send after the end = %v, want ErrClosed", err)
	}
}

// A relay that hangs up properly (SCTP SHUTDOWN, then DTLS close_notify) is
// a clean close, not a read error.
func TestAPeerCloseIsReportedAsClosed(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	side := relay.next(t)
	side.st.closePolitely(time.Second)
	if ev := nextEvent(t, c, EventDisconnected); ev.Reason.Kind != ReasonClosed {
		t.Fatalf("reason = %v, want Closed", ev.Reason)
	}
}

// Our own Close says goodbye on the wire: the relay's channel ends too.
func TestCloseTearsTheRelaySideDown(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	side := relay.next(t)
	if err := c.Send(context.Background(), []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = c.Close()
	if took := time.Since(start); took > defaultTiming.disconnect {
		t.Fatalf("Close took %v", took)
	}
	// What was queued before the hang-up still went out.
	if got := side.recv(t); len(got) != 3 {
		t.Fatalf("relay got %x", got)
	}
	select {
	case <-side.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay's channel did not end after our close")
	}
	var last Event
	for ev := range c.Events() {
		last = ev
	}
	if last.Kind != EventDisconnected || last.Reason.Kind != ReasonClosed {
		t.Fatalf("last event = %v", last)
	}
}

// udpProxy forwards datagrams between one client and a relay, optionally
// reversing a run of client-to-relay datagrams, and can inject datagrams
// toward the client.
type udpProxy struct {
	front, back *net.UDPConn
	client      atomic.Pointer[net.UDPAddr]
	// holdTarget is how many client-to-relay datagrams to capture before
	// releasing them in reverse; zero passes everything through. A test arms
	// it after the handshake, so only application data is reordered.
	holdTarget atomic.Int64
	// held is how many are captured so far.
	held atomic.Int64
}

func newProxy(t *testing.T, relay netip.AddrPort) *udpProxy {
	t.Helper()
	front, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	back, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(relay))
	if err != nil {
		t.Fatal(err)
	}
	p := &udpProxy{front: front, back: back}
	t.Cleanup(func() { _ = front.Close(); _ = back.Close() })
	go func() {
		buf := make([]byte, DatagramBuf)
		var mu sync.Mutex
		var captured [][]byte
		for {
			n, from, err := front.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p.client.Store(from)
			if p.holdTarget.Load() == 0 {
				_, _ = back.Write(buf[:n])
				continue
			}
			mu.Lock()
			captured = append(captured, append([]byte(nil), buf[:n]...))
			p.held.Store(int64(len(captured)))
			if int64(len(captured)) >= p.holdTarget.Load() {
				p.holdTarget.Store(0)
				for i := len(captured) - 1; i >= 0; i-- {
					_, _ = back.Write(captured[i])
				}
				captured = nil
			}
			mu.Unlock()
		}
	}()
	go func() {
		buf := make([]byte, DatagramBuf)
		for {
			n, err := back.Read(buf)
			if err != nil {
				return
			}
			if c := p.client.Load(); c != nil {
				_, _ = front.WriteToUDP(buf[:n], c)
			}
		}
	}()
	return p
}

func (p *udpProxy) addr() netip.AddrPort { return p.front.LocalAddr().(*net.UDPAddr).AddrPort() }

func (p *udpProxy) injectToClient(b []byte) {
	if c := p.client.Load(); c != nil {
		_, _ = p.front.WriteToUDP(b, c)
	}
}

// Garbage on the wire is one lost datagram, not a lost call.
func TestACorruptDatagramAfterTheHandshakeIsDroppedNotFatal(t *testing.T) {
	relay := newFakeRelay(t)
	proxy := newProxy(t, relay.addr())
	c := dialTest(t, proxy.addr())
	side := relay.next(t)
	proxy.injectToClient(make([]byte, 48))
	garbage := make([]byte, 48)
	for i := range garbage {
		garbage[i] = 0xff
	}
	proxy.injectToClient(garbage)
	side.send(t, []byte("still here"))
	if ev := nextEvent(t, c, EventPacket); string(ev.Packet) != "still here" {
		t.Fatalf("client got %q", ev.Packet)
	}
}

// The media channel must stay unordered: reversing the client's datagrams
// on the wire must reverse what the relay reads. An ordered stream would
// hold each arrival until its predecessor came and hand over 0,1,2,3.
func TestMediaChannelDeliversOutOfOrderArrivalsImmediately(t *testing.T) {
	const messages = 4
	relay := newFakeRelay(t)
	proxy := newProxy(t, relay.addr())
	c := dialTest(t, proxy.addr())
	side := relay.next(t)
	proxy.holdTarget.Store(messages)
	for i := range messages {
		if err := c.Send(context.Background(), []byte{byte(i), 1, 1, 1, 1, 1, 1, 1}); err != nil {
			t.Fatal(err)
		}
		// One datagram at a time, so each message is provably its own.
		deadline := time.Now().Add(5 * time.Second)
		for proxy.held.Load() <= int64(i) && proxy.holdTarget.Load() != 0 {
			if time.Now().After(deadline) {
				t.Fatal("the proxy did not capture the datagram just written")
			}
			time.Sleep(time.Millisecond)
		}
	}
	var got []byte
	for range messages {
		got = append(got, side.recv(t)[0])
	}
	if string(got) != string([]byte{3, 2, 1, 0}) {
		t.Fatalf("the relay read %v; an ordered stream would have re-sorted them to [0 1 2 3]", got)
	}
}

// An endpoint answering the ClientHello with something that is not DTLS
// must fail the connect inside the bound, not park the caller. (pion
// ignores records it cannot parse, so this fails on the connect deadline
// rather than on the first bad record as the Rust stack does.)
func TestConnectFailsWhenTheHandshakeNeverCompletes(t *testing.T) {
	junk, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer junk.Close()
	go func() {
		buf := make([]byte, DatagramBuf)
		garbage := make([]byte, 32)
		for i := range garbage {
			garbage[i] = 0xff
		}
		for {
			_, from, err := junk.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = junk.WriteToUDP(garbage, from)
		}
	}()
	tm := testTiming
	tm.connect = time.Second
	f := &NativeFactory{addr: junk.LocalAddr().(*net.UDPAddr).AddrPort(), tm: tm}
	start := time.Now()
	_, err = f.Connect(context.Background())
	if err == nil {
		t.Fatal("an endpoint that never completes the DTLS handshake must not yield a channel")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("connect failed after %v, outside the bound", took)
	}
}

// Hanging up must not wait on a call that stopped listening: once the
// event queue is full nothing drains it, and blocking the goodbye there
// would add the whole disconnect bound to teardown.
func TestHangingUpDoesNotWaitOnABackedUpEventQueue(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	side := relay.next(t)
	deadline := time.Now().Add(10 * time.Second)
	for len(c.events) < eventCap {
		if time.Now().After(deadline) {
			t.Fatalf("event queue reached only %d", len(c.events))
		}
		side.send(t, []byte{0x90, 0x78, 7, 7})
		time.Sleep(50 * time.Microsecond)
	}
	start := time.Now()
	_ = c.Close()
	if took := time.Since(start); took >= defaultTiming.disconnect/2 {
		t.Fatalf("hanging up took %v: the driver blocked on the full event queue", took)
	}
}

// A relay that goes away after the channel opened, without a word on the
// wire, must take the channel down with it. On a connected UDP socket the
// vanished peer surfaces as an ICMP-driven socket error. The relay sits
// behind a proxy whose socket is closed, so no close_notify ever arrives.
func TestARelayThatDisappearsDisconnectsTheChannel(t *testing.T) {
	relay := newFakeRelay(t)
	proxy := newProxy(t, relay.addr())
	c := dialTest(t, proxy.addr())
	relay.next(t)
	nextEvent(t, c, EventConnected)
	_ = proxy.front.Close()
	_ = proxy.back.Close()
	deadline := time.After(20 * time.Second)
	for {
		if err := c.Send(context.Background(), []byte{0x90, 0x78, 0, 0}); err != nil {
			return
		}
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatal("the channel must end with a Disconnected event, not by the stream closing")
			}
			if ev.Kind == EventDisconnected {
				t.Logf("disconnected: %v", ev.Reason)
				return
			}
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("a vanished relay must disconnect the channel")
		}
	}
}

// The data channel framing pion applies matches the channel's
// configuration: negotiated, so no DCEP is ever sent.
func TestTheMediaChannelIsPreNegotiated(t *testing.T) {
	relay := newFakeRelay(t)
	c := dialTest(t, relay.addr())
	relay.next(t)
	cfg := c.st.dc.Config
	if !cfg.Negotiated || cfg.Label != DataChannelLabel || cfg.ChannelType != datachannel.ChannelTypePartialReliableRexmitUnordered ||
		c.st.dc.StreamIdentifier() != MediaStreamID {
		t.Fatalf("config = %+v", cfg)
	}
}
