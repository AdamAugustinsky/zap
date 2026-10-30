// Ported from whatsapp-rust 5e72baf src/voip/transport/native.rs, MIT.

package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	dtlsnet "github.com/pion/dtls/v3/pkg/net"
	"github.com/pion/logging"
	"github.com/pion/sctp"

	"zapfast/gocore/voip/demux"
)

// The synthetic-SDP / WebRTC dance WA Web goes through reduces, at this
// layer, to: connect a UDP socket to the relay, DTLS-handshake as the
// client (self-signed certificate, server certificate unverified, since the
// SRTP keys come from the callKey and not from DTLS), run an SCTP
// association over it, and open the pre-negotiated id 0 data channel that
// carries STUN/RTP/RTCP as binary messages.

const (
	// DataChannelLabel is the label WA Web gives the media channel.
	DataChannelLabel = "pre-negotiated"
	// SCTPPort is the SCTP-over-DTLS WebRTC port, on both ends.
	SCTPPort = 5000
	// MediaStreamID is the pre-negotiated channel's stream id. Both ends
	// open it directly: a pre-negotiated channel carries no DCEP handshake.
	MediaStreamID uint16 = 0

	// MediaChannelType is the shape WA Web opens the media channel with
	// (ordered=false, maxRetransmits=0). Real-time RTP must not ride a
	// reliable, ordered stream: one reordered or lost packet would
	// head-of-line-block every later one, which the peer hears as choppy
	// audio.
	MediaChannelType = datachannel.ChannelTypePartialReliableRexmitUnordered
	// MediaMaxRetransmits is the retransmit count MediaChannelType carries:
	// never retransmit, abandon instead.
	MediaMaxRetransmits uint32 = 0

	// WebClientRelayPort is the media port WA Web's relays serve; it is
	// raced beside the advertised one (see dialCandidates).
	WebClientRelayPort uint16 = 3480

	// eventCap is the inbound event queue depth. VoIP is loss tolerant,
	// so the channel drops media rather than block when the call falls
	// behind.
	eventCap = 256
	// sendCap is the outbound queue depth, with the same rule.
	sendCap = 256
	// DatagramBuf is the receive buffer a relay datagram must fit in. A
	// DTLS record carrying one SCTP packet stays inside the path MTU.
	DatagramBuf = 8192
	// sctpMaxMessage is the largest message the association reassembles.
	// It is a local receive limit and never appears on the wire.
	sctpMaxMessage = 65536
	// sctpReceiveWindow is the advertised receive window.
	sctpReceiveWindow = 1 << 20
	// sctpMTU is the SCTP MTU the relay expects (also pion's default).
	sctpMTU = 1191
)

// timing holds the deadlines of a relay channel. Tests shrink them.
type timing struct {
	// connect bounds the whole handshake (UDP, DTLS, SCTP, media stream),
	// so a black-holed or wedged endpoint fails instead of parking the
	// caller.
	connect time.Duration
	// stunHold is how long one inbound STUN packet may wait for room in
	// the event queue while the call is behind.
	stunHold time.Duration
	// disconnect bounds Close's polite teardown.
	disconnect time.Duration
	// shutdown bounds the wait for the relay's SHUTDOWN-ACK inside it.
	shutdown time.Duration
	// grace is how long a relay that answered first on its advertised
	// port is held back for the web-client port, when both are dialled.
	grace time.Duration
	// cutoff is how far into the dial the grace may run, so a completed
	// dial still comes back inside every deadline its callers set (the
	// factory's 12 s and the call driver's 10 s relay reconnect).
	cutoff time.Duration
}

var defaultTiming = timing{
	connect:    12 * time.Second,
	stunHold:   100 * time.Millisecond,
	disconnect: 2 * time.Second,
	shutdown:   500 * time.Millisecond,
	grace:      1500 * time.Millisecond,
	cutoff:     8 * time.Second,
}

// reliabilityParams maps a data channel type to the SCTP stream's
// (unordered, reliability type) pair, the RFC 8832 rule. The reliability is
// applied to the SCTP stream here, not by the data channel layer: for a
// negotiated channel pion's layer sends no DATA_CHANNEL_OPEN and so never
// commits it.
func reliabilityParams(t datachannel.ChannelType) (unordered bool, reliability byte) {
	switch t {
	case datachannel.ChannelTypeReliable:
		return false, sctp.ReliabilityTypeReliable
	case datachannel.ChannelTypeReliableUnordered:
		return true, sctp.ReliabilityTypeReliable
	case datachannel.ChannelTypePartialReliableRexmit:
		return false, sctp.ReliabilityTypeRexmit
	case datachannel.ChannelTypePartialReliableRexmitUnordered:
		return true, sctp.ReliabilityTypeRexmit
	case datachannel.ChannelTypePartialReliableTimed:
		return false, sctp.ReliabilityTypeTimed
	case datachannel.ChannelTypePartialReliableTimedUnordered:
		return true, sctp.ReliabilityTypeTimed
	}
	return false, sctp.ReliabilityTypeReliable
}

// dtlsConfig is the DTLS configuration of both ends of a relay flow. The
// relay does not validate our client certificate and we do not validate
// its: the SDP fingerprint is fixed and cosmetic at this layer, and media
// authentication is SRTP keyed from the callKey.
func dtlsConfig() (*dtls.Config, error) {
	cert, err := selfsign.GenerateSelfSignedWithDNS("wa-voip")
	if err != nil {
		return nil, fmt.Errorf("dtls self-signed cert: %w", err)
	}
	return &dtls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
		// Only the SNI, irrelevant without verification.
		ServerName:    "localhost",
		LoggerFactory: pionLogs,
	}, nil
}

// sctpConfig is the SCTP configuration of a relay association.
func sctpConfig(conn net.Conn) sctp.Config {
	return sctp.Config{
		NetConn:              conn,
		LoggerFactory:        pionLogs,
		MTU:                  sctpMTU,
		MaxMessageSize:       sctpMaxMessage,
		MaxReceiveBufferSize: sctpReceiveWindow,
	}
}

// stack is one open relay channel: the UDP flow, the DTLS session in it,
// the SCTP association inside that, and the media stream.
type stack struct {
	udp    net.Conn
	dtls   *dtls.Conn
	assoc  *sctp.Association
	stream *sctp.Stream
	dc     *datachannel.DataChannel
}

// openMediaStream opens the pre-negotiated id 0 stream with the
// reliability MediaChannelType implies, and the data channel over it.
func openMediaStream(assoc *sctp.Association) (*sctp.Stream, *datachannel.DataChannel, error) {
	stream, err := assoc.OpenStream(MediaStreamID, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		return nil, nil, fmt.Errorf("open sctp media stream: %w", err)
	}
	unordered, reliability := reliabilityParams(MediaChannelType)
	stream.SetReliabilityParams(unordered, reliability, MediaMaxRetransmits)
	dc, err := datachannel.Client(stream, &datachannel.Config{
		ChannelType:          MediaChannelType,
		Negotiated:           true,
		ReliabilityParameter: MediaMaxRetransmits,
		Label:                DataChannelLabel,
		LoggerFactory:        pionLogs,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("media data channel: %w", err)
	}
	slog.Debug("voip: media channel open", "label", DataChannelLabel, "stream", MediaStreamID)
	return stream, dc, nil
}

// dialStack connects the whole stack to addr, returning once the media
// stream is open.
func dialStack(ctx context.Context, addr netip.AddrPort) (*stack, error) {
	network := "udp4"
	if addr.Addr().Is6() && !addr.Addr().Is4In6() {
		network = "udp6"
	}
	udp, err := net.DialUDP(network, nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		return nil, fmt.Errorf("connect udp to relay: %w", err)
	}
	st := &stack{udp: udp}
	ok := false
	defer func() {
		if !ok {
			st.abort()
		}
	}()
	cfg, err := dtlsConfig()
	if err != nil {
		return nil, err
	}
	st.dtls, err = dtls.Client(dtlsnet.PacketConnFromConn(udp), udp.RemoteAddr(), cfg)
	if err != nil {
		return nil, fmt.Errorf("dtls connect: %w", err)
	}
	if err := st.dtls.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("dtls handshake: %w", err)
	}
	st.assoc, err = sctp.ClientContext(ctx, sctpConfig(st.dtls))
	if err != nil {
		return nil, fmt.Errorf("sctp handshake: %w", err)
	}
	st.stream, st.dc, err = openMediaStream(st.assoc)
	if err != nil {
		return nil, err
	}
	ok = true
	return st, nil
}

// write queues one media message. SCTP cannot carry an empty message, so
// an empty one travels as one zero byte under the Binary Empty PPI.
func (st *stack) write(p []byte) error {
	if _, err := st.dc.WriteDataChannel(p, false); err != nil {
		return fmt.Errorf("media write: %w", err)
	}
	return nil
}

// closePolitely tears the channel down: SCTP SHUTDOWN, then DTLS
// close_notify. The wait for the relay's SHUTDOWN-ACK is bounded, because
// a teardown that waits on a peer that may already be gone is one that
// hangs.
func (st *stack) closePolitely(shutdown time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdown)
	_ = st.assoc.Shutdown(ctx)
	cancel()
	st.abort()
}

// abort closes every layer without waiting on the relay.
func (st *stack) abort() {
	if st.assoc != nil {
		_ = st.assoc.Close()
	}
	if st.dtls != nil {
		_ = st.dtls.Close()
	}
	_ = st.udp.Close()
}

// Channel is an open relay media channel: STUN/RTP/RTCP travel as binary
// data channel messages. It implements Transport.
type Channel struct {
	addr     netip.AddrPort
	tm       timing
	outbound chan []byte
	events   chan Event
	// hangup is closed by Close: the driver flushes what is queued, says
	// goodbye on the wire and ends.
	hangup     chan struct{}
	hangupOnce sync.Once
	// finished is closed when the driver has ended.
	finished chan struct{}
	st       *stack
}

// connectRelayMedia connects the full media stack to one relay endpoint.
// It returns once the media channel is open; the first event is
// EventConnected.
func connectRelayMedia(ctx context.Context, addr netip.AddrPort, tm timing) (*Channel, error) {
	st, err := dialStack(ctx, addr)
	if err != nil {
		return nil, err
	}
	c := &Channel{
		addr:     addr,
		tm:       tm,
		outbound: make(chan []byte, sendCap),
		events:   make(chan Event, eventCap),
		hangup:   make(chan struct{}),
		finished: make(chan struct{}),
		st:       st,
	}
	c.events <- Event{Kind: EventConnected}
	go c.drive()
	return c, nil
}

// Events implements Transport.
func (c *Channel) Events() <-chan Event { return c.events }

// Send implements Transport. A full queue means the driver is not
// draining, and media is what a call can afford to lose; STUN is not, so
// it waits for a slot.
func (c *Channel) Send(ctx context.Context, packet []byte) error {
	select {
	case <-c.hangup:
		return ErrClosed
	case <-c.finished:
		return ErrClosed
	default:
	}
	p := append([]byte(nil), packet...)
	select {
	case c.outbound <- p:
		return nil
	default:
	}
	if demux.Classify(p) != demux.KindStun {
		return nil
	}
	select {
	case c.outbound <- p:
		return nil
	case <-c.hangup:
		return ErrClosed
	case <-c.finished:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close implements Transport: the driver sends what is already queued,
// then SCTP SHUTDOWN and DTLS close_notify. It waits for that, bounded so a
// relay that stopped answering cannot hold teardown open, then tears down
// whatever is left.
func (c *Channel) Close() error {
	c.hangupOnce.Do(func() { close(c.hangup) })
	select {
	case <-c.finished:
	case <-time.After(c.tm.disconnect):
		c.st.abort()
		<-c.finished
	}
	return nil
}

// Reconnect implements Transport.
func (c *Channel) Reconnect(ctx context.Context, endpoint netip.AddrPort) (Transport, error) {
	return (&NativeFactory{addr: endpoint, tm: c.tm}).Connect(ctx)
}

type readResult struct {
	reason  DisconnectReason
	dropped uint32
}

// drive owns the channel after it opened: it writes what is queued, reacts
// to the hang-up, and ends the event stream once the reader has stopped.
func (c *Channel) drive() {
	defer close(c.finished)
	defer close(c.events)
	readDone := make(chan readResult, 1)
	go c.read(readDone)
	for {
		select {
		case p := <-c.outbound:
			if err := c.st.write(p); err != nil {
				c.st.abort()
				r := <-readDone
				c.finish(r.dropped, ReadError(err.Error()))
				return
			}
		case <-c.hangup:
			// Flush what is already queued, then say goodbye on the wire.
			for flushing := true; flushing; {
				select {
				case p := <-c.outbound:
					_ = c.st.write(p)
				default:
					flushing = false
				}
			}
			c.st.closePolitely(c.tm.shutdown)
			<-readDone
			// Not a blocking send: this close was asked for locally, so the
			// call already knows, and by now it has left its receive loop.
			// Waiting for a slot only that reader could free would stall
			// teardown exactly when the call was behind.
			select {
			case c.events <- Event{Kind: EventDisconnected, Reason: Closed()}:
			default:
			}
			return
		case r := <-readDone:
			slog.Debug("voip: relay media channel ended", "reason", r.reason.String())
			c.st.abort()
			c.finish(r.dropped, r.reason)
			return
		}
	}
}

// read moves every message off the media stream into the event queue.
func (c *Channel) read(done chan<- readResult) {
	buf := make([]byte, sctpMaxMessage)
	tally := newInboundTally(c.addr.String())
	defer tally.log()
	var dropped uint32
	for {
		n, ppi, err := c.st.stream.ReadSCTP(buf)
		if err != nil {
			reason := ReadError(err.Error())
			if errors.Is(err, io.EOF) {
				// The relay reset the stream or closed the association: the
				// relay hanging up. Reset ours too, as a data channel does.
				_ = c.st.stream.Close()
				reason = Closed()
			}
			select {
			case <-c.hangup:
				reason = Closed()
			default:
			}
			done <- readResult{reason: reason, dropped: dropped}
			return
		}
		// A pre-negotiated channel exchanges no DCEP, so anything but user
		// data on this stream is not addressed to us.
		if ppi != sctp.PayloadTypeWebRTCBinary && ppi != sctp.PayloadTypeWebRTCString {
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		tally.count(packet)
		// False only once hanging up: keep reading until the teardown
		// closes the stream under us.
		_ = deliver(c.events, packet, &dropped, c.tm.stunHold, c.hangup)
	}
}

// deliver pushes one inbound packet to the call, dropping media (but never
// STUN) under backpressure. It returns false once the channel is hanging
// up and delivery should stop.
//
// dropped accumulates media discarded here. It cannot be reported at the
// moment of the drop, since the queue being full is why the packet goes
// away. It is flushed BEFORE the next packet: under sustained backpressure
// where the call drains one slot per packet, flushing after would let the
// packet itself refill that slot every time, and the report would stay
// pending for the life of the call.
func deliver(events chan<- Event, packet []byte, dropped *uint32, stunHold time.Duration, stop <-chan struct{}) bool {
	select {
	case <-stop:
		return false
	default:
	}
	kind := demux.Classify(packet)
	if *dropped > 0 {
		select {
		case events <- Event{Kind: EventInboundDropped, Dropped: *dropped}:
			*dropped = 0
		default:
		}
	}
	ev := Event{Kind: EventPacket, Packet: packet}
	select {
	case events <- ev:
		return true
	default:
	}
	// Media is loss tolerant, but STUN is not: a dropped Binding Request
	// never gets its Binding Success, the relay's consent-freshness check
	// fails and it tears the call down. So media is dropped, and STUN waits
	// for a slot, bounded so a call that stopped draining loses one STUN
	// packet rather than stalling this reader.
	if kind != demux.KindStun {
		if *dropped < ^uint32(0) {
			*dropped++
		}
		return true
	}
	t := time.NewTimer(stunHold)
	defer t.Stop()
	select {
	case events <- ev:
	case <-t.C:
	case <-stop:
		return false
	}
	return true
}

// finish reports the end of the channel. The pending drop count goes
// first: a relay that fails right after an overload would otherwise take
// the final burst with it, and that overload is the one worth attributing.
func (c *Channel) finish(dropped uint32, reason DisconnectReason) {
	if dropped > 0 {
		select {
		case c.events <- Event{Kind: EventInboundDropped, Dropped: dropped}:
		case <-c.hangup:
			return
		}
	}
	select {
	case c.events <- Event{Kind: EventDisconnected, Reason: reason}:
	case <-c.hangup:
	}
}

// inboundTally is what a relay channel received, by kind, before the
// engine decrypts any of it. It tells "the relay sent nothing on this port"
// apart from "it sent packets the call could not use". Logged once, when
// the channel ends.
type inboundTally struct {
	peer                   string
	stun, rtp, rtcp, other uint64
}

func newInboundTally(peer string) *inboundTally { return &inboundTally{peer: peer} }

func (t *inboundTally) count(packet []byte) {
	kind := demux.KindOther
	if p, err := demux.UnwrapGroupForwarding(packet); err == nil {
		kind = demux.Classify(p.Payload)
	}
	switch kind {
	case demux.KindStun:
		t.stun++
	case demux.KindRtp:
		t.rtp++
	case demux.KindRtcp:
		t.rtcp++
	default:
		t.other++
	}
}

func (t *inboundTally) log() {
	slog.Debug("voip: relay channel received before decryption", "peer", t.peer,
		"rtp", t.rtp, "rtcp", t.rtcp, "stun", t.stun, "other", t.other)
}

// NativeFactory dials one relay endpoint (UDP, DTLS, SCTP, data channel).
// It implements Factory.
type NativeFactory struct {
	addr netip.AddrPort
	tm   timing
}

// NewFactory returns the factory that dials addr.
func NewFactory(addr netip.AddrPort) *NativeFactory {
	return &NativeFactory{addr: addr, tm: defaultTiming}
}

// dialCandidates is the relay addresses to dial concurrently for the
// advertised one. Which of the two media ports a relay serves is not
// derivable from the offer and varies per host and per session, so either
// choice alone black-holes some relays. The advertised port comes first;
// there is never a duplicate.
func dialCandidates(advertised netip.AddrPort) []netip.AddrPort {
	out := []netip.AddrPort{advertised}
	if advertised.Port() != WebClientRelayPort {
		out = append(out, netip.AddrPortFrom(advertised.Addr(), WebClientRelayPort))
	}
	return out
}

// PortChoice is why a relay dial kept the port it did.
type PortChoice int

const (
	// OnlyCandidate: one port was dialled, the relay advertised the
	// web-client port itself.
	OnlyCandidate PortChoice = iota
	// WebClientFirst: the web-client port completed its handshake first.
	WebClientFirst
	// BothAnsweredPreferredWebClient: both completed; the web-client port
	// was preferred.
	BothAnsweredPreferredWebClient
	// AdvertisedFailed: the advertised port failed and the web-client port
	// answered.
	AdvertisedFailed
	// WebClientFailed: the web-client port failed and the advertised port
	// answered.
	WebClientFailed
	// GraceExpired: the advertised port answered and the web-client port
	// did not within the grace.
	GraceExpired
	// CutoffReached: the web-client port had not answered by the cutoff,
	// which cut the grace short or left none.
	CutoffReached
)

// Reason is the human reason logged with the kept port.
func (p PortChoice) Reason() string {
	switch p {
	case OnlyCandidate:
		return "the only port dialled"
	case WebClientFirst:
		return "the web-client port answered first"
	case BothAnsweredPreferredWebClient:
		return "both ports answered; preferred the web-client port"
	case AdvertisedFailed:
		return "the advertised port failed"
	case WebClientFailed:
		return "the web-client port failed; kept the advertised port"
	case GraceExpired:
		return "the web-client port did not answer within the grace; kept the advertised port"
	case CutoffReached:
		return "the web-client port had not answered by the grace cutoff; kept the advertised port"
	}
	return "unknown"
}

func connectedMessage(kept, advertised netip.AddrPort, choice PortChoice) string {
	return fmt.Sprintf("voip: relay media connected on %s (advertised %s): %s", kept, advertised, choice.Reason())
}

type dialResult[T any] struct {
	v   T
	err error
}

// preferWebClientPort races the dial of the advertised port against the
// dial of the web-client port and keeps the web-client port whenever it
// answers within the grace of the advertised one, never waiting past the
// cutoff from the start of the dial. The dial not kept is cancelled, and
// discard closes it if it connected anyway.
func preferWebClientPort[T any](
	ctx context.Context,
	tm timing,
	advertised, webClient func(context.Context) (T, error),
	discard func(T),
) (T, PortChoice, error) {
	started := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run := func(dial func(context.Context) (T, error)) <-chan dialResult[T] {
		ch := make(chan dialResult[T], 1)
		go func() {
			v, err := dial(ctx)
			ch <- dialResult[T]{v, err}
		}()
		return ch
	}
	adv, web := run(advertised), run(webClient)
	// Whatever a losing dial produces after the decision is closed.
	drop := func(ch <-chan dialResult[T]) {
		go func() {
			if r := <-ch; r.err == nil && discard != nil {
				discard(r.v)
			}
		}()
	}
	var zero T
	select {
	case r := <-web:
		if r.err == nil {
			drop(adv)
			return r.v, WebClientFirst, nil
		}
		a := <-adv
		if a.err != nil {
			return zero, 0, fmt.Errorf("%v; %v", a.err, r.err)
		}
		return a.v, WebClientFailed, nil
	case a := <-adv:
		if a.err != nil {
			r := <-web
			if r.err != nil {
				return zero, 0, fmt.Errorf("%v; %v", r.err, a.err)
			}
			return r.v, AdvertisedFailed, nil
		}
		left := tm.cutoff - time.Since(started)
		if left <= 0 {
			drop(web)
			return a.v, CutoffReached, nil
		}
		wait := min(tm.grace, left)
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case r := <-web:
			if r.err == nil {
				if discard != nil {
					discard(a.v)
				}
				return r.v, BothAnsweredPreferredWebClient, nil
			}
			return a.v, WebClientFailed, nil
		case <-t.C:
			drop(web)
			if left < tm.grace {
				return a.v, CutoffReached, nil
			}
			return a.v, GraceExpired, nil
		case <-ctx.Done():
			drop(web)
			if discard != nil {
				discard(a.v)
			}
			return zero, 0, ctx.Err()
		}
	}
}

// Connect implements Factory. It races every candidate port (see
// dialCandidates) under the connect deadline.
func (f *NativeFactory) Connect(ctx context.Context) (Transport, error) {
	candidates := dialCandidates(f.addr)
	tried := ""
	for i, c := range candidates {
		if i > 0 {
			tried += ", "
		}
		tried += c.String()
	}
	ctx, cancel := context.WithTimeout(ctx, f.tm.connect)
	defer cancel()
	dial := func(addr netip.AddrPort) func(context.Context) (*Channel, error) {
		return func(ctx context.Context) (*Channel, error) {
			c, err := connectRelayMedia(ctx, addr, f.tm)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", addr, err)
			}
			return c, nil
		}
	}
	var (
		ch     *Channel
		choice PortChoice
		err    error
	)
	if len(candidates) == 2 {
		ch, choice, err = preferWebClientPort(ctx, f.tm, dial(candidates[0]), dial(candidates[1]),
			func(c *Channel) { go c.Close() })
	} else {
		ch, err = dial(candidates[0])(ctx)
		choice = OnlyCandidate
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("relay connect timed out after %s (DTLS/SCTP didn't complete); tried %s", f.tm.connect, tried)
		}
		return nil, fmt.Errorf("relay connect: %w; tried %s", err, tried)
	}
	slog.Debug(connectedMessage(ch.addr, f.addr, choice))
	return ch, nil
}

// pionLogs routes pion's logging to slog at debug level.
var pionLogs logging.LoggerFactory = slogFactory{}

type slogFactory struct{}

func (slogFactory) NewLogger(scope string) logging.LeveledLogger { return slogLogger(scope) }

type slogLogger string

func (l slogLogger) emit(level slog.Level, msg string) {
	slog.Log(context.Background(), level, "pion: "+msg, "scope", string(l))
}
func (l slogLogger) Trace(msg string)          {}
func (l slogLogger) Tracef(string, ...any)     {}
func (l slogLogger) Debug(msg string)          { l.emit(slog.LevelDebug-4, msg) }
func (l slogLogger) Debugf(f string, a ...any) { l.emit(slog.LevelDebug-4, fmt.Sprintf(f, a...)) }
func (l slogLogger) Info(msg string)           { l.emit(slog.LevelDebug, msg) }
func (l slogLogger) Infof(f string, a ...any)  { l.emit(slog.LevelDebug, fmt.Sprintf(f, a...)) }
func (l slogLogger) Warn(msg string)           { l.emit(slog.LevelDebug, msg) }
func (l slogLogger) Warnf(f string, a ...any)  { l.emit(slog.LevelDebug, fmt.Sprintf(f, a...)) }
func (l slogLogger) Error(msg string)          { l.emit(slog.LevelDebug, msg) }
func (l slogLogger) Errorf(f string, a ...any) { l.emit(slog.LevelDebug, fmt.Sprintf(f, a...)) }
