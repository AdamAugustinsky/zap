package engine

// In-process group-call tests: the sans-I/O engine's group state machine
// (epoch gating, rekey, join, leave, relay refresh) and three Go
// participants over the in-memory relay, which fans every packet out to
// the other ends as an SFU does, with the mixed audio held to tone SNR.

import (
	"bytes"
	"errors"
	"math"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/demux"
	"zapfast/gocore/voip/groupcall"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/stun"
	"zapfast/gocore/voip/video"
)

const groupCallID = "A1B2C3D4E5F60718293A4B5C6D7E8F90"

var (
	gA = types.JID{User: "111111111111111", Server: types.HiddenUserServer, Device: 3}
	gB = types.JID{User: "222222222222222", Server: types.HiddenUserServer}
	gC = types.JID{User: "333333333333333", Server: types.HiddenUserServer, Device: 5}
	gD = types.JID{User: "444444444444444", Server: types.HiddenUserServer, Device: 1}
)

func groupRelay(addr string, tx uint32) *groupcall.Relay {
	ap := netip.MustParseAddrPort(addr)
	ip := ap.Addr().As4()
	return &groupcall.Relay{
		TransactionID: tx, HasTransactionID: true, UUID: "u", ParticipantUUID: "p",
		Key: []byte("group-relay-key"), Tokens: [][]byte{[]byte("group-token")}, AuthTokens: [][]byte{[]byte("auth")},
		Endpoints: []groupcall.RelayEndpoint{{
			Name: "test", IPv4: ap.Addr().String(), Port: ap.Port(),
			Address: append(ip[:], byte(ap.Port()>>8), byte(ap.Port())),
		}},
	}
}

func member(j types.JID, pid uint32) groupcall.Participant {
	return groupcall.Participant{JID: j.ToNonAD(), State: "connected",
		Devices: []groupcall.Device{{JID: j, PID: pid, HasPID: true}}}
}

func groupRoster(tx uint32, relay *groupcall.Relay, ps ...groupcall.Participant) *groupcall.Update {
	return &groupcall.Update{CallID: groupCallID, Creator: gA, TransactionID: tx, Media: "audio",
		ConnectedLimit: 32, Participants: ps, Relay: relay}
}

// counterTx is a deterministic transaction id source.
type counterTx struct{ n uint32 }

func (c *counterTx) NextTxID() (id [12]byte) {
	c.n++
	id[4], id[5], id[6], id[7] = byte(c.n>>24), byte(c.n>>16), byte(c.n>>8), byte(c.n)|0x80
	id[0] = byte(c.n)
	return id
}

func newGroupEngine(t *testing.T, self types.JID, initial *groupcall.Update) *Engine {
	t.Helper()
	cfg, err := NewGroupConfig(Incoming, groupCallID, self.String(), gA.String(), initial.Relay)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg, &counterTx{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ConfigureGroup(GroupConfig{Creator: gA, Self: self, Initial: initial}); err != nil {
		t.Fatal(err)
	}
	return e
}

func drainOutputs(e *Engine) []Output {
	var out []Output
	for {
		o := e.PollOutput()
		if o.Kind == OutTimeout {
			return out
		}
		out = append(out, o)
	}
}

// rtpSent are the RTP packets among outs.
func rtpSent(outs []Output) []rtp.Header {
	var hs []rtp.Header
	for _, o := range outs {
		if o.Kind == OutTransmit && demux.Classify(o.Packet) == demux.KindRtp {
			if h, ok := rtp.ParseHeader(o.Packet); ok {
				hs = append(hs, h)
			}
		}
	}
	return hs
}

func allocates(outs []Output) int {
	n := 0
	for _, o := range outs {
		if o.Kind == OutTransmit && stun.IsSTUN(o.Packet) {
			if typ, ok := stun.MessageType(o.Packet); ok && typ == stun.MsgAllocateRequest {
				n++
			}
		}
	}
	return n
}

func tone(n int) []int16 {
	f := make([]int16, MicFrameSamples)
	for i := range f {
		f[i] = toneSample(n*MicFrameSamples+i, toneFreqs)
	}
	return f
}

func TestGroupEngineGatesMediaUntilItsEpochAndAgainOnARekey(t *testing.T) {
	relay := groupRelay("10.0.0.1:3480", 1)
	e := newGroupEngine(t, gB, groupRoster(1, relay, member(gA, 0), member(gB, 1)))
	e.Start(0, 1_700_000_000_000)
	if outs := drainOutputs(e); allocates(outs) != 1 {
		t.Fatalf("the group allocate did not go out: %d", allocates(outs))
	}
	e.MicFrame(10, tone(0))
	if hs := rtpSent(drainOutputs(e)); len(hs) != 0 {
		t.Fatal("media went out under the zeroed bootstrap key")
	}
	if r, err := e.ApplyGroupEpoch(1, epochOf(0x11)); err != nil || r != EpochInstalled {
		t.Fatalf("epoch %v %v", r, err)
	}
	drainOutputs(e)
	e.MicFrame(70, tone(1))
	hs := rtpSent(drainOutputs(e))
	want := kdf.RelayStreamSSRCs(groupCallID, kdf.FormatParticipantID(gB.String()))[0]
	if len(hs) != 1 || hs[0].SSRC != want {
		t.Fatalf("audio after the epoch: %+v, want SSRC %d", hs, want)
	}

	// A rekey request retires the epoch at once: nothing goes out until
	// the requested transaction's epoch installs.
	rekey := groupRoster(2, nil, member(gA, 0), member(gB, 1))
	rekey.RekeyRequested = true
	if r, err := e.ApplyGroupUpdate(100, rekey); err != nil || r != RosterApplied {
		t.Fatalf("rekey roster %v %v", r, err)
	}
	drainOutputs(e)
	e.MicFrame(130, tone(2))
	if hs := rtpSent(drainOutputs(e)); len(hs) != 0 {
		t.Fatal("media went out after a rekey request, before its epoch")
	}
	if r, _ := e.ApplyGroupEpoch(1, epochOf(0x11)); r != EpochDuplicate {
		t.Fatalf("the old epoch again: %v", r)
	}
	if _, err := e.ApplyGroupEpoch(2, epochOf(0x22)); err != nil {
		t.Fatal(err)
	}
	if tx, ok := e.GroupEpochTransaction(); !ok || tx != 2 {
		t.Fatalf("send epoch %d %v", tx, ok)
	}
	drainOutputs(e)
	e.MicFrame(190, tone(3))
	if hs := rtpSent(drainOutputs(e)); len(hs) != 1 {
		t.Fatal("no media under the new epoch")
	}
}

func TestGroupEngineJoinAndLeaveMoveTheSubscriptions(t *testing.T) {
	relay := groupRelay("10.0.0.1:3480", 1)
	e := newGroupEngine(t, gB, groupRoster(1, relay, member(gA, 0), member(gB, 1)))
	e.Start(0, 1)
	drainOutputs(e)
	if _, err := e.ApplyGroupEpoch(1, epochOf(0x11)); err != nil {
		t.Fatal(err)
	}
	drainOutputs(e)
	// C joins: a new subscription, so a fresh allocate.
	if _, err := e.ApplyGroupUpdate(10, groupRoster(2, nil, member(gA, 0), member(gB, 1), member(gC, 2))); err != nil {
		t.Fatal(err)
	}
	if n := allocates(drainOutputs(e)); n != 1 {
		t.Fatalf("a join re-sent %d allocates", n)
	}
	if got := e.GroupParticipants(); len(got) != 2 {
		t.Fatalf("participants %v", got)
	}
	// A stale roster changes nothing.
	if r, _ := e.ApplyGroupUpdate(20, groupRoster(2, nil, member(gA, 0), member(gB, 1))); r != RosterStale {
		t.Fatalf("stale roster %v", r)
	}
	// C leaves.
	if _, err := e.ApplyGroupUpdate(30, groupRoster(3, nil, member(gA, 0), member(gB, 1))); err != nil {
		t.Fatal(err)
	}
	if n := allocates(drainOutputs(e)); n != 1 {
		t.Fatalf("a leave re-sent %d allocates", n)
	}
	if got := e.GroupParticipants(); !slices.Equal(got, []string{groupcall.ParticipantID(gA)}) {
		t.Fatalf("participants after the leave %v", got)
	}
	// A roster without us ends our media.
	_, err := e.ApplyGroupUpdate(40, groupRoster(4, nil, member(gA, 0), member(gC, 2)))
	if !errors.Is(err, ErrLocalParticipantRemoved) || !e.Terminated() {
		t.Fatalf("removal: %v terminated %v", err, e.Terminated())
	}
}

func TestGroupEngineRelayRefreshRedialsAndWaitsForTheNewChannel(t *testing.T) {
	e := newGroupEngine(t, gB, groupRoster(1, groupRelay("10.0.0.1:3480", 1), member(gA, 0), member(gB, 1)))
	e.Start(0, 1)
	drainOutputs(e)
	// A newer allocation on another address, on an already-seen roster.
	moved := groupRoster(1, groupRelay("10.0.0.2:3480", 2), member(gA, 0), member(gB, 1))
	if r, err := e.ApplyGroupUpdate(50, moved); err != nil || r != RosterStale {
		t.Fatalf("relay refresh %v %v", r, err)
	}
	outs := drainOutputs(e)
	if len(outs) < 2 || outs[0].Kind != OutReconnectRelay || outs[0].Addr != netip.MustParseAddrPort("10.0.0.2:3480") || allocates(outs) != 1 {
		t.Fatalf("no reconnect before the allocate: %+v", outs)
	}
	if e.allocateDeadline != Never {
		t.Fatal("the allocate's budget ran during the redial")
	}
	e.RelayReconnected(60)
	if e.allocateDeadline != 60+allocateTimeoutMS {
		t.Fatal("the allocate's budget did not start on the new channel")
	}
	// An older relay never replaces the one held.
	if r, _ := e.ApplyGroupUpdate(70, groupRoster(1, groupRelay("10.0.0.3:3480", 1), member(gA, 0), member(gB, 1))); r != RosterStale {
		t.Fatal("an older relay applied")
	}
	if outs := drainOutputs(e); len(outs) != 0 {
		t.Fatalf("an older relay redialled: %+v", outs)
	}
	// A refresh that changes the WARP tag length is refused.
	bad := groupRelay("10.0.0.4:3480", 3)
	bad.WarpMITagLen, bad.HasWarpMITagLen = 8, true
	if _, err := e.ApplyGroupUpdate(80, groupRoster(5, bad, member(gA, 0), member(gB, 1))); !errors.Is(err, ErrGroupPipeline) {
		t.Fatalf("a tag-length change: %v", err)
	}
}

func TestGroupEngineFutureEpochWaitsForItsRoster(t *testing.T) {
	e := newGroupEngine(t, gB, groupRoster(1, groupRelay("10.0.0.1:3480", 1), member(gA, 0), member(gB, 1)))
	e.Start(0, 1)
	drainOutputs(e)
	if r, _ := e.ApplyGroupEpoch(2, epochOf(0x22)); r != EpochBuffered {
		t.Fatalf("a future epoch: %v", r)
	}
	if _, ok := e.GroupEpochTransaction(); ok {
		t.Fatal("a future epoch keyed the send side")
	}
	if _, err := e.ApplyGroupUpdate(10, groupRoster(2, nil, member(gA, 0), member(gB, 1), member(gC, 2))); err != nil {
		t.Fatal(err)
	}
	if tx, ok := e.GroupEpochTransaction(); !ok || tx != 2 {
		t.Fatal("the buffered epoch did not install with its roster")
	}
}

// --- three Go participants over the in-memory relay --------------------------

type groupSide struct {
	*side
	self types.JID
}

func openGroupSide(t *testing.T, r *memRelay, self types.JID, freqs []float64, initial *groupcall.Update) *groupSide {
	t.Helper()
	return openGroupVideoSide(t, r, self, freqs, initial, false, false)
}

func openGroupVideoSide(t *testing.T, r *memRelay, self types.JID, freqs []float64, initial *groupcall.Update, videoCall, cam bool) *groupSide {
	t.Helper()
	s := &side{ev: newEvents(), frames: &frameSink{}, audio: newFakeAudio(freqs)}
	d := Devices{
		Dial: r.Dial, Frames: s.frames, TxIDs: &counterTx{n: uint32(self.Device) << 16},
		OpenAudio: func(string, string) (Audio, error) { return s.audio, nil },
		OpenCamera: func(string) (Camera, error) {
			s.cam = newFakeCamera(320, 240)
			return s.cam, nil
		},
		NewEncoder: func(w, h, fps int) (VideoEncoder, error) {
			return video.NewEncoder(video.EncoderConfig{Width: w, Height: h, FPS: fps, Bitrate: 500_000})
		},
		NewDecoder: func() (VideoDecoder, error) { return video.NewDecoder() },
	}
	c, err := Open(Params{
		CallID: groupCallID, Generation: 9, Outgoing: self == gA, SelfLID: self.String(), MLow: true,
		Video: videoCall, CameraOn: cam,
		Group: &GroupParams{Creator: gA, Self: self, Initial: initial.Clone(),
			MemberID: func(j types.JID) (string, bool) { return j.User, true }},
	}, d, s.ev.add)
	if err != nil {
		t.Fatal(err)
	}
	s.call = c
	t.Cleanup(func() {
		c.Close()
		<-c.Done()
		s.audio.Close()
	})
	return &groupSide{side: s, self: self}
}

// mixSNR is the SNR of what s heard against the union of the speakers'
// tones, and how much worse it gets with each speaker's tones left out:
// both drops must be large, or one speaker was missing from the mix.
func mixSNR(s *side, speakers ...[]float64) (all float64, without []float64) {
	pcm := s.audio.playedCopy()
	var union []float64
	for _, f := range speakers {
		union = append(union, f...)
	}
	all = toneSNR(pcm, union)
	for i := range speakers {
		var rest []float64
		for j, f := range speakers {
			if j != i {
				rest = append(rest, f...)
			}
		}
		without = append(without, all-toneSNR(pcm, rest))
	}
	return all, without
}

func TestThreeGoParticipantsHearEachOtherMixedOverAGroupRelay(t *testing.T) {
	r := &memRelay{}
	relay := groupRelay("10.0.0.1:3480", 1)
	roster := groupRoster(1, relay, member(gA, 0), member(gB, 1), member(gC, 2))
	freqs := map[types.JID][]float64{gA: {350, 1100}, gB: {500, 1300}, gC: {650, 1500}}
	sides := []*groupSide{
		openGroupSide(t, r, gA, freqs[gA], roster),
		openGroupSide(t, r, gB, freqs[gB], roster),
		openGroupSide(t, r, gC, freqs[gC], roster),
	}
	for _, s := range sides {
		s.ev.wait(t, CallConnected, 5*time.Second)
		s.call.Command(Command{Op: OpGroup, Epoch: &GroupEpoch{Transaction: 1, Key: epochOf(0x5a)}})
	}
	time.Sleep(3 * time.Second)
	// The codec alone on one speaker's tone: the mix of two, through the
	// relay, SRTP and the mixer, is held to within 6 dB of the worse one.
	ref := math.Inf(1)
	for _, f := range freqs {
		ref = min(ref, codecSNR(t, f, nil))
	}
	for _, s := range sides {
		var others [][]float64
		for _, o := range sides {
			if o != s {
				others = append(others, freqs[o.self])
			}
		}
		all, without := mixSNR(s.side, others...)
		st := s.call.Stats()
		t.Logf("%s heard the mix at %.1f dB (each speaker's share %.1f/%.1f dB); codec alone %.1f; rtp %d decoded %d auth failures %d",
			s.self.User[:3], all, without[0], without[1], ref, st.RTPReceived, st.AudioFramesDecoded, st.SRTPUnprotectFailed)
		if all < 10 || all < ref-6 {
			t.Errorf("%s: the mix is at %.1f dB", s.self, all)
		}
		for i, w := range without {
			if w < 6 {
				t.Errorf("%s: speaker %d is missing from the mix (%.1f dB)", s.self, i, w)
			}
		}
		if st.SRTPUnprotectFailed != 0 {
			t.Errorf("%s: %d authentication failures", s.self, st.SRTPUnprotectFailed)
		}
	}

	// A rekey: the creator's next roster asks for one, every side gets the
	// new epoch, and the call carries on under it.
	rekey := groupRoster(2, nil, member(gA, 0), member(gB, 1), member(gC, 2))
	rekey.RekeyRequested = true
	for _, s := range sides {
		s.call.Command(Command{Op: OpGroup, Group: rekey.Clone(), Epoch: &GroupEpoch{Transaction: 2, Key: epochOf(0x6b)}})
	}
	// C leaves: A and B hear only each other.
	time.Sleep(1500 * time.Millisecond)
	left := groupRoster(3, nil, member(gA, 0), member(gB, 1))
	for _, s := range sides[:2] {
		s.call.Command(Command{Op: OpGroup, Group: left.Clone()})
	}
	sides[2].end(t)
	time.Sleep(2 * time.Second)
	for i, s := range sides[:2] {
		other := sides[1-i]
		pcm := s.audio.playedCopy()
		snr := toneSNR(pcm, freqs[other.self])
		withC := toneSNR(pcm, append(slices.Clone(freqs[other.self]), freqs[gC]...))
		t.Logf("%s after the rekey and C's leave: %.1f dB on %s's tone alone (%.1f with C's)", s.self.User[:3], snr, other.self.User[:3], withC)
		if snr < 10 {
			t.Errorf("%s: %.1f dB after the rekey", s.self, snr)
		}
		if math.Abs(withC-snr) > 1 {
			t.Errorf("%s: C is still in the mix after leaving (%.1f vs %.1f dB)", s.self, withC, snr)
		}
		if st := s.call.Stats(); st.SRTPUnprotectFailed != 0 {
			t.Errorf("%s: %d authentication failures across the rekey", s.self, st.SRTPUnprotectFailed)
		}
	}
	for _, s := range sides[:2] {
		s.end(t)
	}
}

func TestAGroupMemberRemovedByTheRosterEndsItsMedia(t *testing.T) {
	r := &memRelay{}
	roster := groupRoster(1, groupRelay("10.0.0.1:3480", 1), member(gA, 0), member(gB, 1), member(gD, 3))
	s := openGroupSide(t, r, gD, []float64{700}, roster)
	s.ev.wait(t, CallConnected, 5*time.Second)
	s.call.Command(Command{Op: OpGroup, Group: groupRoster(2, nil, member(gA, 0), member(gB, 1))})
	ev := s.ev.wait(t, CallClosed, 5*time.Second)
	if !bytes.Contains([]byte(ev.Reason), []byte("no longer connected")) {
		t.Fatalf("closed for %q", ev.Reason)
	}
}

func TestAGroupVideoCallPutsEachMembersPictureOnItsOwnTile(t *testing.T) {
	needVideo(t)
	r := &memRelay{}
	roster := groupRoster(1, groupRelay("10.0.0.1:3480", 1), member(gA, 0), member(gB, 1), member(gC, 2))
	roster.Media = "video"
	a := openGroupVideoSide(t, r, gA, freqsA, roster, true, true)
	b := openGroupVideoSide(t, r, gB, freqsB, roster, true, true)
	c := openGroupVideoSide(t, r, gC, freqsC, roster, true, false)
	sides := []*groupSide{a, b, c}
	for _, s := range sides {
		s.ev.wait(t, CallConnected, 5*time.Second)
		s.call.Command(Command{Op: OpGroup, Epoch: &GroupEpoch{Transaction: 1, Key: epochOf(0x7c)}})
	}
	// Every side holds the epoch before the senders open on a keyframe:
	// members send keyframes on their own schedule, nobody asks for one.
	time.Sleep(500 * time.Millisecond)
	a.call.Command(Command{Op: OpCameraOn})
	b.call.Command(Command{Op: OpCameraOn})
	seen := map[types.JID]bool{}
	for start := time.Now(); len(seen) < 2; time.Sleep(50 * time.Millisecond) {
		if time.Since(start) > 8*time.Second {
			t.Fatalf("C saw video from %v", seen)
		}
		for _, ev := range c.ev.snapshot() {
			if ev.Kind == CallMemberVideo {
				seen[ev.Member] = true
			}
		}
	}
	if !seen[gA.ToNonAD()] || !seen[gB.ToNonAD()] {
		t.Fatalf("members with video: %v", seen)
	}
	time.Sleep(time.Second)
	for _, m := range []types.JID{gA, gB} {
		if n := c.frames.memberPictures(m.User); n < 5 {
			t.Errorf("C put %d pictures on %s's tile", n, m.User)
		}
	}
	if n := len(c.frames.remotes()); n != 0 {
		t.Errorf("a group member's picture went to the one-to-one stream (%d)", n)
	}
	if st := c.call.Stats(); st.VideoSRTPUnprotectFailed != 0 || st.SRTPUnprotectFailed != 0 {
		t.Errorf("authentication failures: %+v", st)
	}
	// The camera-less member receives without sending: A sees B, nobody
	// sees C.
	if a.frames.memberPictures(gC.User) != 0 || a.frames.memberPictures(gB.User) == 0 {
		t.Errorf("A's tiles: B %d, C %d", a.frames.memberPictures(gB.User), a.frames.memberPictures(gC.User))
	}
}

var (
	freqsA = []float64{350, 1100}
	freqsB = []float64{500, 1300}
	freqsC = []float64{650, 1500}
)
