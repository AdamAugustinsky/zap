// Ported from whatsapp-rust 5e72baf src/voip/transport/native.rs (its tests), MIT.

package relay

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pion/sctp"

	"zapfast/gocore/voip/demux"
)

// A relay that advertises 3478 must still be tried on the web-client port,
// since which of the two a host serves is not visible in the offer.
func TestDialCandidatesAddsTheWebClientPort(t *testing.T) {
	advertised := netip.MustParseAddrPort("57.144.43.57:3478")
	want := []netip.AddrPort{advertised, netip.MustParseAddrPort("57.144.43.57:3480")}
	if got := dialCandidates(advertised); !slices.Equal(got, want) {
		t.Fatalf("dialCandidates = %v, want %v", got, want)
	}
}

func TestDialCandidatesDoesNotDuplicateTheWebClientPort(t *testing.T) {
	advertised := netip.MustParseAddrPort("170.78.54.98:3480")
	if got := dialCandidates(advertised); !slices.Equal(got, []netip.AddrPort{advertised}) {
		t.Fatalf("dialCandidates = %v", got)
	}
}

// The race tests run the production policy at a 1/20 time scale.
const scale = 20

func scaled(d time.Duration) time.Duration { return d / scale }

var raceTiming = timing{
	connect:    scaled(defaultTiming.connect),
	stunHold:   defaultTiming.stunHold,
	disconnect: defaultTiming.disconnect,
	shutdown:   defaultTiming.shutdown,
	grace:      scaled(defaultTiming.grace),
	cutoff:     scaled(defaultTiming.cutoff),
}

// dial answers after ms (production milliseconds, scaled), as answer.
func dial(ms int, answer string, fail bool) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		select {
		case <-time.After(scaled(time.Duration(ms) * time.Millisecond)):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if fail {
			return "", errors.New(answer)
		}
		return answer, nil
	}
}

type kept struct {
	v      string
	choice PortChoice
}

func race(t *testing.T, advertised, webClient func(context.Context) (string, error)) (kept, error) {
	t.Helper()
	v, choice, err := preferWebClientPort(context.Background(), raceTiming, advertised, webClient, nil)
	return kept{v, choice}, err
}

func TestARelayServingBothPortsIsKeptOnTheWebClientPort(t *testing.T) {
	got, err := race(t, dial(80, "advertised", false), dial(140, "web client", false))
	if err != nil || got != (kept{"web client", BothAnsweredPreferredWebClient}) {
		t.Fatalf("kept %v, %v", got, err)
	}
}

// Both handshakes completed, so the log must not claim the advertised port
// went unserved.
func TestTheLogSaysBothPortsAnsweredWhenTheWebClientPortWasPreferred(t *testing.T) {
	advertised := netip.MustParseAddrPort("57.144.43.57:3478")
	k := netip.MustParseAddrPort("57.144.43.57:3480")
	line := connectedMessage(k, advertised, BothAnsweredPreferredWebClient)
	want := "voip: relay media connected on 57.144.43.57:3480 (advertised 57.144.43.57:3478): " +
		"both ports answered; preferred the web-client port"
	if line != want {
		t.Fatalf("line = %q", line)
	}
	if line := connectedMessage(k, advertised, AdvertisedFailed); !strings.HasSuffix(line, "the advertised port failed") {
		t.Fatalf("line = %q", line)
	}
}

func TestTheAdvertisedPortIsKeptWhenTheWebClientPortDoesNotAnswer(t *testing.T) {
	got, err := race(t, dial(80, "advertised", false), dial(60_000, "web client", false))
	if err != nil || got != (kept{"advertised", GraceExpired}) {
		t.Fatalf("kept %v, %v", got, err)
	}
	got, err = race(t, dial(80, "advertised", false), dial(200, "refused", true))
	if err != nil || got != (kept{"advertised", WebClientFailed}) {
		t.Fatalf("kept %v, %v", got, err)
	}
}

func TestEitherPortAloneIsEnoughAndBothFailingNamesBoth(t *testing.T) {
	got, err := race(t, dial(10, "advertised: timed out", true), dial(300, "web client", false))
	if err != nil || got != (kept{"web client", AdvertisedFailed}) {
		t.Fatalf("kept %v, %v", got, err)
	}
	got, err = race(t, dial(90, "advertised", false), dial(10, "web client", false))
	if err != nil || got != (kept{"web client", WebClientFirst}) {
		t.Fatalf("the web-client port answering first is kept at once: %v, %v", got, err)
	}
	_, err = race(t, dial(10, "advertised: refused", true), dial(20, "web client: refused", true))
	if err == nil || !strings.Contains(err.Error(), "advertised") || !strings.Contains(err.Error(), "web client") {
		t.Fatalf("err = %v", err)
	}
}

// The call driver gives a relay reconnect 10 s and the factory's own
// deadline is 12 s: an advertised port answering at 9 s must come back at
// once, not be held past either deadline by the grace.
func TestALateAnswerIsReturnedInsideTheReconnectDeadline(t *testing.T) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	v, choice, err := preferWebClientPort(ctx, raceTiming, dial(9_000, "advertised", false), dial(60_000, "web client", false), nil)
	if err != nil || (kept{v, choice}) != (kept{"advertised", CutoffReached}) {
		t.Fatalf("kept %v %v, %v", v, choice, err)
	}
	if took := time.Since(started); took > scaled(9500*time.Millisecond) {
		t.Fatalf("kept after %v, want at once (about %v)", took, scaled(9*time.Second))
	}
}

func TestALateAnswerIsReturnedInsideTheConnectDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), raceTiming.connect)
	defer cancel()
	v, choice, err := preferWebClientPort(ctx, raceTiming, dial(11_000, "advertised", false), dial(60_000, "web client", false), nil)
	if err != nil || (kept{v, choice}) != (kept{"advertised", CutoffReached}) {
		t.Fatalf("kept %v %v, %v", v, choice, err)
	}
}

// An answer shortly before the cutoff gets only what is left of the grace.
func TestTheGraceIsCutShortAtTheCutoff(t *testing.T) {
	started := time.Now()
	got, err := race(t, dial(7_500, "advertised", false), dial(60_000, "web client", false))
	if err != nil || got != (kept{"advertised", CutoffReached}) {
		t.Fatalf("kept %v, %v", got, err)
	}
	took := time.Since(started)
	if took < raceTiming.cutoff || took > raceTiming.cutoff+scaled(400*time.Millisecond) {
		t.Fatalf("returned after %v, want the cutoff %v", took, raceTiming.cutoff)
	}
	got, err = race(t, dial(7_500, "advertised", false), dial(7_900, "web client", false))
	if err != nil || got != (kept{"web client", BothAnsweredPreferredWebClient}) {
		t.Fatalf("kept %v, %v", got, err)
	}
}

// The channel type WA Web opens the media channel with has to decompose into
// the SCTP send parameters the stream is configured with.
func TestMediaChannelTypeMeansUnorderedAndNeverRetransmit(t *testing.T) {
	unordered, reliability := reliabilityParams(MediaChannelType)
	if !unordered {
		t.Fatal("real-time RTP must not ride an ordered stream")
	}
	if reliability != sctp.ReliabilityTypeRexmit {
		t.Fatalf("reliability = %d", reliability)
	}
	if MediaMaxRetransmits != 0 {
		t.Fatal("a retransmitted media packet arrives too late to play and blocks the ones behind it")
	}
}

// A packet that would fail its SRTP tag still counts as RTP before
// decryption.
func TestTheInboundTallyCountsEachKindBeforeDecryption(t *testing.T) {
	tally := newInboundTally("")
	rtp := make([]byte, 12)
	rtp[0], rtp[1] = 0x80, 0x78
	rtcp := make([]byte, 8)
	rtcp[0], rtcp[1] = 0x81, 0xc9
	tally.count([]byte{0x00, 0x01, 0x00, 0x00})
	tally.count(rtp)
	tally.count(rtp)
	tally.count(rtcp)
	tally.count([]byte{0x80})
	if got := [4]uint64{tally.stun, tally.rtp, tally.rtcp, tally.other}; got != [4]uint64{1, 2, 1, 1} {
		t.Fatalf("tally = %v", got)
	}
}

func TestTheInboundTallyUnwrapsGroupMediaBeforeClassifying(t *testing.T) {
	tally := newInboundTally("")
	for _, f := range []struct {
		format    byte
		headerLen int
	}{{2, 8}, {4, 12}, {7, 18}} {
		for _, pt := range []byte{120, 201} {
			packet := make([]byte, f.headerLen+12)
			packet[0], packet[1] = 0x09, f.format
			packet[f.headerLen], packet[f.headerLen+1] = 0x80, pt
			tally.count(packet)
		}
	}
	if got := [4]uint64{tally.stun, tally.rtp, tally.rtcp, tally.other}; got != [4]uint64{0, 3, 3, 0} {
		t.Fatalf("tally = %v", got)
	}
}

func TestTheInboundTallyCountsMalformedGroupEnvelopesAsOther(t *testing.T) {
	tally := newInboundTally("")
	for _, p := range [][]byte{
		{0x09},
		{0x09, 3},
		{0x09, 2, 0, 0, 0, 0, 0, 0},
		{0x09, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x80, 120},
		{0x09, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		tally.count(p)
	}
	if got := [4]uint64{tally.stun, tally.rtp, tally.rtcp, tally.other}; got != [4]uint64{0, 0, 0, 5} {
		t.Fatalf("tally = %v", got)
	}
}

var never = make(chan struct{})

func TestDeliverMapsPacketsToEvents(t *testing.T) {
	events := make(chan Event, 8)
	var dropped uint32
	if !deliver(events, []byte{1, 2, 3}, &dropped, time.Second, never) ||
		!deliver(events, []byte{4, 5}, &dropped, time.Second, never) {
		t.Fatal("deliver stopped")
	}
	for _, want := range [][]byte{{1, 2, 3}, {4, 5}} {
		ev := <-events
		if ev.Kind != EventPacket || !slices.Equal(ev.Packet, want) {
			t.Fatalf("event = %v, want packet %x", ev, want)
		}
	}
}

// A drop report is only sent when a later packet arrives, so a relay that
// fails right after an overload would carry the final burst away with it.
func TestTheLastDropBurstSurvivesTheDisconnect(t *testing.T) {
	c := &Channel{events: make(chan Event, 8), hangup: make(chan struct{})}
	c.finish(7, Closed())
	close(c.events)
	var got []Event
	for ev := range c.events {
		got = append(got, ev)
	}
	if len(got) != 2 || got[0].Kind != EventInboundDropped || got[0].Dropped != 7 || got[1].Kind != EventDisconnected {
		t.Fatalf("events = %v", got)
	}
}

// Under the backpressure the drop report describes (the call draining one
// slot per arriving packet), the slot must go to the report first.
func TestAPendingDropReportReachesTheCallUnderSustainedBackpressure(t *testing.T) {
	events := make(chan Event, 1)
	var dropped uint32
	deliver(events, []byte{0x90, 0x78, 1, 2}, &dropped, time.Second, never)
	deliver(events, []byte{0x90, 0x78, 3, 4}, &dropped, time.Second, never)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want the discarded media pending a report", dropped)
	}
	<-events
	deliver(events, []byte{0x90, 0x78, 5, 6}, &dropped, time.Second, never)
	if ev := <-events; ev.Kind != EventInboundDropped || ev.Dropped != 1 {
		t.Fatalf("event = %v, want the drop report", ev)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d: the media displaced by the report is itself counted", dropped)
	}
}

// A hung-up channel stops delivery (the Rust test closes the receiver, which
// a Go channel's consumer cannot do; the hang-up is its equivalent).
func TestDeliverStopsOnHangUp(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	var dropped uint32
	if deliver(make(chan Event, 1), []byte{1}, &dropped, time.Second, stop) {
		t.Fatal("a hung-up channel must stop delivery")
	}
}

// The STUN hold is bounded: the reader must keep going rather than stall
// behind a call that stopped draining.
func TestDeliverBoundsHowLongStunCanHoldTheReader(t *testing.T) {
	events := make(chan Event, 1)
	var dropped uint32
	deliver(events, []byte{0x90, 0x78, 1, 2}, &dropped, defaultTiming.stunHold, never)
	held := time.Now()
	done := make(chan bool)
	go func() { done <- deliver(events, []byte{0x00, 0x01, 5, 6}, &dropped, defaultTiming.stunHold, never) }()
	select {
	case keptGoing := <-done:
		if !keptGoing {
			t.Fatal("an undeliverable STUN packet is dropped, not a reason to stop")
		}
	case <-time.After(defaultTiming.stunHold * 50):
		t.Fatal("the STUN hold must give up")
	}
	if took := time.Since(held); took < defaultTiming.stunHold {
		t.Fatalf("STUN held for %v, want the bound %v", took, defaultTiming.stunHold)
	}
}

// Under backpressure, delivery drops media but preserves STUN.
func TestDeliverPreservesStunButDropsMediaUnderBackpressure(t *testing.T) {
	events := make(chan Event, 1)
	fed := make(chan struct{})
	var dropped uint32
	// Media never blocks: the first fills the queue, the second is dropped.
	deliver(events, []byte{0x90, 0x78, 1, 2}, &dropped, 5*time.Second, never)
	deliver(events, []byte{0x90, 0x78, 3, 4}, &dropped, 5*time.Second, never)
	go func() {
		defer close(fed)
		deliver(events, []byte{0x00, 0x01, 5, 6}, &dropped, 5*time.Second, never)
	}()
	if first := <-events; first.Kind != EventPacket || first.Packet[0] != 0x90 {
		t.Fatalf("first = %v", first)
	}
	// The pending drop report may take the freed slot ahead of the STUN.
	for {
		ev := <-events
		if ev.Kind == EventInboundDropped {
			continue
		}
		if ev.Kind != EventPacket || demux.Classify(ev.Packet) != demux.KindStun {
			t.Fatalf("second packet = %v, want the STUN", ev)
		}
		break
	}
	<-fed
}

func TestADroppedMediaPacketIsCountedAndReportedOnTheNextDelivery(t *testing.T) {
	events := make(chan Event, 4)
	var dropped uint32
	for n := range byte(4) {
		deliver(events, []byte{0x90, 0x78, n}, &dropped, time.Second, never)
	}
	if dropped != 0 {
		t.Fatal("nothing was dropped while there was room")
	}
	for n := range byte(2) {
		deliver(events, []byte{0x90, 0x78, 100 + n}, &dropped, time.Second, never)
	}
	if dropped != 2 {
		t.Fatalf("dropped = %d", dropped)
	}
	<-events
	<-events
	deliver(events, []byte{0x90, 0x78, 9}, &dropped, time.Second, never)
	if dropped != 0 {
		t.Fatal("the count was handed over, not kept")
	}
	reported := uint32(0)
	for len(events) > 0 {
		if ev := <-events; ev.Kind == EventInboundDropped {
			reported = ev.Dropped
		}
	}
	if reported != 2 {
		t.Fatalf("reported %d", reported)
	}
}

func TestEndpointParamsRedactTheRelayKey(t *testing.T) {
	p := EndpointParams{Addr: netip.MustParseAddrPort("192.0.2.1:3478"), ICEUfrag: "u", ICEPwd: "secret-key"}
	for _, s := range []string{p.String(), p.GoString()} {
		if strings.Contains(s, "secret-key") || !strings.Contains(s, "[redacted]") {
			t.Fatalf("leaks the key: %s", s)
		}
	}
}
