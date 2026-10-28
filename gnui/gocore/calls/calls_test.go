package calls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"zapfast/abi"
	"zapfast/core"
)

// The signaling against a fake whatsmeow and a fake media library: every
// stanza it sends is recorded, and every answer the server or the peer
// would give is injected. Every number and key is synthetic.

var (
	ownPN    = types.JID{User: "15550001001", Server: types.DefaultUserServer, Device: 7}
	ownLID   = types.JID{User: "100000000001001", Server: types.HiddenUserServer, Device: 7}
	peerPN   = types.JID{User: "15550002002", Server: types.DefaultUserServer}
	peerLID  = types.JID{User: "100000000002002", Server: types.HiddenUserServer}
	peerDev0 = types.JID{User: "100000000002002", Server: types.HiddenUserServer}
	peerDev3 = types.JID{User: "100000000002002", Server: types.HiddenUserServer, Device: 3}
	callKey  = bytes.Repeat([]byte{7}, 32)
)

// --- the fake client -------------------------------------------------------------

type fakeClient struct {
	mu      sync.Mutex
	sent    []waBinary.Node
	ids     int
	offered int
	// gate, when set, holds the offer in SendAwait until it is closed.
	gate  chan struct{}
	ack   func(offer waBinary.Node) (*waBinary.Node, error)
	ring  []types.JID
	plain map[string][]byte // ciphertext → callKey
}

func (f *fakeClient) Own() (types.JID, types.JID) { return ownPN, ownLID }

func (f *fakeClient) NewID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids++
	return fmt.Sprintf("ID-%d", f.ids)
}

func (f *fakeClient) Send(_ context.Context, n waBinary.Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	return nil
}

func (f *fakeClient) SendAwait(ctx context.Context, n waBinary.Node, _ time.Duration) (*waBinary.Node, error) {
	f.Send(ctx, n)
	if f.gate != nil {
		<-f.gate
	}
	return f.ack(n)
}

func (f *fakeClient) DecryptKey(_ context.Context, enc *waBinary.Node, from types.JID, _ time.Time) ([]byte, error) {
	ct, _ := enc.Content.([]byte)
	if key, ok := f.plain[string(ct)]; ok && from == peerDev3 {
		return key, nil
	}
	return nil, errors.New("no session")
}

func (f *fakeClient) EncryptKey(_ context.Context, devices []types.JID, key []byte, _ string) ([]DeviceKey, []byte, error) {
	var keys []DeviceKey
	for _, d := range devices {
		keys = append(keys, DeviceKey{Device: d, Ciphertext: append([]byte("ct-"+d.String()+"-"), key[:4]...), Type: "msg"})
	}
	return keys, nil, nil
}

func (f *fakeClient) PeerLID(_ context.Context, user types.JID) (types.JID, error) {
	if user == peerPN || user == peerLID {
		return peerLID, nil
	}
	return types.EmptyJID, errors.New("unknown contact")
}

func (f *fakeClient) Devices(context.Context, types.JID) ([]types.JID, error) { return f.ring, nil }
func (f *fakeClient) PrivacyToken(context.Context, types.JID) []byte          { return []byte("tc") }
func (f *fakeClient) Offered(context.Context, types.JID) {
	f.mu.Lock()
	f.offered++
	f.mu.Unlock()
}

func (f *fakeClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// take returns the stanzas sent since the last take.
func (f *fakeClient) take() []waBinary.Node {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

func actionOf(n waBinary.Node) *waBinary.Node {
	c := n.GetChildren()
	if len(c) == 0 {
		return nil
	}
	return &c[0]
}

func shape(nodes []waBinary.Node) []string {
	var out []string
	for _, n := range nodes {
		to, _ := n.Attrs["to"].(types.JID)
		s := n.Tag + ":" + actionOf(n).Tag + "→" + to.String()
		if r, ok := actionOf(n).Attrs["reason"].(string); ok {
			s += "(" + r + ")"
		}
		if st, ok := actionOf(n).Attrs["state"].(string); ok {
			s += "[" + st + "]"
		}
		out = append(out, s)
	}
	return out
}

// --- the fake media ----------------------------------------------------------------

type fakeMedia struct {
	mu       sync.Mutex
	opened   []OpenParams
	commands []string
	closed   []uint64
	openErr  error
}

func (m *fakeMedia) Open(p OpenParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openErr != nil {
		return m.openErr
	}
	m.opened = append(m.opened, p)
	return nil
}

func (m *fakeMedia) Command(_ uint64, c MediaCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := c.Op()
	for _, k := range []string{"on", "await_accept", "answering_lid", "codec"} {
		if v, ok := c[k]; ok {
			s += fmt.Sprintf(" %s=%v", k, v)
		}
	}
	m.commands = append(m.commands, s)
	return nil
}

func (m *fakeMedia) Close(g uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = append(m.closed, g)
	return nil
}

func (m *fakeMedia) Next(timeout time.Duration) (MediaEvent, bool, error) {
	time.Sleep(timeout)
	return MediaEvent{}, false, nil
}

func (m *fakeMedia) Devices() (abi.DeviceList, error) {
	return abi.DeviceList{Cameras: []abi.Device{{ID: []byte("cam0"), Label: []byte("Test Camera")}}}, nil
}
func (m *fakeMedia) Frame(uint32) (core.Frame, bool) { return core.Frame{}, false }
func (m *fakeMedia) ReleaseFrame(uint32)             {}
func (m *fakeMedia) Free()                           {}

func (m *fakeMedia) takeCommands() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.commands
	m.commands = nil
	return out
}

// --- the harness: the engine's goroutine, its jobs, what it emits ---------------

type harness struct {
	t     *testing.T
	cli   *fakeClient
	media *fakeMedia
	c     *Calls

	mu      sync.Mutex
	inbox   []func()
	busy    int
	updates []abi.CallUpdate
	entries []Entry
	errs    []string
	kick    chan struct{}
	cancel  context.CancelFunc
}

func newHarness(t *testing.T, withMedia bool) *harness {
	h := &harness{t: t, kick: make(chan struct{}, 1),
		cli:   &fakeClient{ring: []types.JID{peerDev0, peerDev3}, plain: map[string][]byte{"enc-for-me": callKey}},
		media: &fakeMedia{}}
	h.cli.ack = func(offer waBinary.Node) (*waBinary.Node, error) {
		return &waBinary.Node{Tag: "ack", Attrs: waBinary.Attrs{"class": "call", "type": "offer", "id": offer.Attrs["id"]},
			Content: []waBinary.Node{relayNode()}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	t.Cleanup(cancel)
	go h.loop(ctx)
	host := Host{
		Context: ctx,
		Post:    h.post,
		Background: func(_ string, f func(context.Context)) bool {
			go f(ctx)
			return true
		},
		Go: func(name string, f func(context.Context)) bool {
			h.mu.Lock()
			h.busy++
			h.mu.Unlock()
			go func() {
				defer func() { h.mu.Lock(); h.busy--; h.mu.Unlock(); h.wake() }()
				f(ctx)
			}()
			return true
		},
		Emit: func(ev *abi.Event) {
			if ev.Tag == abi.EventTagCall {
				h.mu.Lock()
				h.updates = append(h.updates, ev.Call.Update)
				h.mu.Unlock()
			}
		},
		Chat: func(user types.JID) string {
			if user.ToNonAD() == peerLID {
				return peerPN.String()
			}
			return user.ToNonAD().String()
		},
		File:  func(e Entry) { h.mu.Lock(); h.entries = append(h.entries, e); h.mu.Unlock() },
		Error: func(s string) { h.mu.Lock(); h.errs = append(h.errs, s); h.mu.Unlock() },
	}
	var media Media
	if withMedia {
		media = h.media
	}
	h.c = New(host, media, Timing{Ack: time.Second, Ring: time.Hour, Upgrade: time.Hour, Poll: 5 * time.Millisecond})
	h.c.SetClient(h.cli)
	return h
}

func (h *harness) wake() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func (h *harness) post(f func()) {
	h.mu.Lock()
	h.inbox = append(h.inbox, f)
	h.mu.Unlock()
	h.wake()
}

func (h *harness) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.kick:
		}
		for {
			h.mu.Lock()
			if len(h.inbox) == 0 {
				h.mu.Unlock()
				break
			}
			f := h.inbox[0]
			h.inbox = h.inbox[1:]
			h.busy++
			h.mu.Unlock()
			f()
			h.mu.Lock()
			h.busy--
			h.mu.Unlock()
		}
	}
}

// do runs f on the engine's goroutine, then waits until nothing is left to
// do.
func (h *harness) do(f func()) {
	h.t.Helper()
	h.post(f)
	h.settle()
}

func (h *harness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		idle := len(h.inbox) == 0 && h.busy == 0
		h.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatal("the calls did not settle")
}

func (h *harness) command(cmd abi.Command) {
	h.t.Helper()
	if err := h.c.Accept(&cmd); err != nil {
		h.t.Fatal(err)
	}
	h.do(func() { h.c.Command(&cmd) })
}

func (h *harness) signal(evt any) {
	h.t.Helper()
	s, ok := FromEvent(evt)
	if !ok {
		h.t.Fatalf("%T is not a call stanza", evt)
	}
	h.do(func() { h.c.Signal(s) })
}

func (h *harness) mediaEvent(kind string) {
	h.t.Helper()
	h.do(func() { h.c.mediaEvent(MediaEvent{Generation: h.last().Generation, Kind: kind}) })
}

func (h *harness) last() abi.CallUpdate {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.updates) == 0 {
		h.t.Fatal("no Call event")
	}
	return h.updates[len(h.updates)-1]
}

func (h *harness) phases() []abi.CallPhase {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []abi.CallPhase
	for _, u := range h.updates {
		if len(out) == 0 || out[len(out)-1] != u.Phase {
			out = append(out, u.Phase)
		}
	}
	return out
}

func (h *harness) expectSent(want ...string) []waBinary.Node {
	h.t.Helper()
	nodes := h.cli.take()
	if got := shape(nodes); !slices.Equal(got, want) {
		h.t.Fatalf("sent %q, want %q", got, want)
	}
	return nodes
}

func (h *harness) expectCommands(want ...string) {
	h.t.Helper()
	if got := h.media.takeCommands(); !slices.Equal(got, want) {
		h.t.Fatalf("media commands %q, want %q", got, want)
	}
}

func (h *harness) ended(status abi.CallStatus) {
	h.t.Helper()
	u := h.last()
	if live(u.Phase) || !u.HasOutcome || u.Outcome != status {
		h.t.Fatalf("the call is %v %v (has %v), want it over with %v", u.Phase, u.Outcome, u.HasOutcome, status)
	}
}

// --- stanzas the peer and the server send -----------------------------------------

func relayNode() waBinary.Node {
	return waBinary.Node{Tag: "relay", Attrs: waBinary.Attrs{"uuid": "test"}, Content: []waBinary.Node{
		{Tag: "key", Content: []byte("cmVsYXkta2V5LXRlc3Q=")},
		{Tag: "token", Attrs: waBinary.Attrs{"id": "0"}, Content: bytes.Repeat([]byte{0xaa}, 16)},
		{Tag: "te2", Attrs: waBinary.Attrs{"relay_id": "0", "relay_name": "test", "token_id": "0"}, Content: []byte{127, 0, 0, 1, 0x0d, 0x96}},
	}}
}

const peerCallID = "00AABBCCDDEEFF00112233445566778899"

func meta(from types.JID) types.BasicCallMeta {
	return types.BasicCallMeta{From: from, Timestamp: time.Unix(1766847151, 0), CallCreator: peerDev3, CallID: peerCallID, StanzaID: "STANZA-1"}
}

// peerOffer is the peer's offer to our device (and another of ours).
func peerOffer(video bool, capability []byte) *events.CallOffer {
	other := ownLID
	other.Device = 2
	n := BuildOffer(OfferParams{CallID: peerCallID, To: ownLID.ToNonAD(), Creator: peerDev3, Capability: capability,
		Keys:  []DeviceKey{{Device: other, Ciphertext: []byte("enc-for-other"), Type: "msg"}, {Device: ownLID, Ciphertext: []byte("enc-for-me"), Type: "pkmsg"}},
		Video: video, AudioRates: AudioRates16k, MultiDevice: true})
	offer := *actionOf(n)
	offer.Content = append(offer.GetChildren(), relayNode())
	return &events.CallOffer{BasicCallMeta: meta(peerDev3), Data: &offer}
}

func peerAction(tag string, from types.JID, attrs waBinary.Attrs, children ...waBinary.Node) any {
	a := waBinary.Attrs{"call-id": peerCallID, "call-creator": peerDev3}
	for k, v := range attrs {
		a[k] = v
	}
	n := &waBinary.Node{Tag: tag, Attrs: a}
	if len(children) > 0 {
		n.Content = children
	}
	m := meta(from)
	switch tag {
	case "preaccept":
		return &events.CallPreAccept{BasicCallMeta: m, Data: n}
	case "accept":
		return &events.CallAccept{BasicCallMeta: m, Data: n}
	case "reject":
		return &events.CallReject{BasicCallMeta: m, Data: n}
	case "terminate":
		r, _ := attrs["reason"].(string)
		return &events.CallTerminate{BasicCallMeta: m, Reason: r, Data: n}
	}
	return &events.UnknownCallEvent{Node: &waBinary.Node{Tag: "call", Attrs: waBinary.Attrs{"from": from, "id": "S-2", "t": "1766847152"},
		Content: []waBinary.Node{*n}}}
}

// ourAction is the peer's answer to our call: its call-creator is us.
func ourAction(tag string, from types.JID, callID string, attrs waBinary.Attrs, children ...waBinary.Node) any {
	ev := peerAction(tag, from, attrs, children...)
	fix := func(m *types.BasicCallMeta, n *waBinary.Node) {
		m.CallID, m.CallCreator = callID, ownLID
		n.Attrs["call-id"], n.Attrs["call-creator"] = callID, ownLID
	}
	switch v := ev.(type) {
	case *events.CallPreAccept:
		fix(&v.BasicCallMeta, v.Data)
	case *events.CallAccept:
		fix(&v.BasicCallMeta, v.Data)
	case *events.CallReject:
		fix(&v.BasicCallMeta, v.Data)
	case *events.CallTerminate:
		fix(&v.BasicCallMeta, v.Data)
	case *events.UnknownCallEvent:
		c := v.Node.GetChildren()[0]
		c.Attrs["call-id"], c.Attrs["call-creator"] = callID, ownLID
	}
	return ev
}

func capabilityNode2(blob []byte) waBinary.Node {
	return waBinary.Node{Tag: "capability", Attrs: waBinary.Attrs{"ver": "1"}, Content: blob}
}

// --- incoming ---------------------------------------------------------------------

func TestAnIncomingCallRingsIsAnsweredAndHungUp(t *testing.T) {
	h := newHarness(t, true)
	h.signal(peerOffer(false, CapabilityOffer))
	receipt := h.expectSent("receipt:offer→" + peerDev3.String())[0]
	if receipt.Attrs["id"] != "STANZA-1" || receipt.Attrs["from"] != ownLID {
		t.Fatalf("the offer's receipt: %v", receipt.Attrs)
	}
	u := h.last()
	if u.Phase != abi.CallPhaseIncoming || u.Direction != abi.CallDirectionIncoming || string(u.Chat) != peerPN.String() || u.Generation <= 1<<47 {
		t.Fatalf("ringing: %+v", u)
	}

	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	sent := h.expectSent("call:preaccept→"+peerDev3.String(), "call:accept→"+peerDev3.String())
	if c := actionOf(sent[0]).GetChildren(); c[len(c)-1].Tag != "capability" || !bytes.Equal(c[len(c)-1].Content.([]byte), CapabilityPreaccept) {
		t.Fatalf("the preaccept's capability: %v", sent[0])
	}
	if sent[1].Attrs["id"] == nil {
		t.Fatal("an accept needs an id")
	}
	if len(h.media.opened) != 1 {
		t.Fatalf("media opened %d times", len(h.media.opened))
	}
	p := h.media.opened[0]
	if p.Outgoing || p.SelfLID != ownLID.String() || p.PeerLID != peerDev3.String() || !bytes.Equal(p.CallKey, callKey) ||
		!p.MLow || p.Video || p.CallID != peerCallID || p.Generation != u.Generation {
		t.Fatalf("the media's parameters: %+v", p)
	}
	if relay := decodeRelay(t, p.Relay); relay == nil {
		t.Fatal("the media got no relay")
	}
	if h.last().Phase != abi.CallPhaseAccepted {
		t.Fatalf("answered: %v", h.last().Phase)
	}

	h.mediaEvent(MediaConnected)
	if h.last().Phase != abi.CallPhaseActive {
		t.Fatalf("connected: %v", h.last().Phase)
	}
	h.command(abi.Command{Tag: abi.CommandTagSetCallMuted, SetCallMuted: abi.CommandSetCallMuted{Muted: true}})
	mute := h.expectSent("call:mute_v2→" + peerDev3.String())[0]
	if actionOf(mute).Attrs["mute-state"] != "1" || !h.last().Muted {
		t.Fatalf("mute: %v", mute)
	}
	h.expectCommands("mute on=true")

	h.command(abi.Command{Tag: abi.CommandTagHangupCall})
	h.expectSent("call:terminate→" + peerDev3.String())
	h.ended(abi.CallStatusAnswered)
	if len(h.media.closed) != 1 {
		t.Fatal("the media stays open")
	}
	if len(h.entries) != 1 || h.entries[0].FromMe || h.entries[0].Status != abi.CallStatusAnswered || h.entries[0].Chat != peerPN.String() {
		t.Fatalf("filed %+v", h.entries)
	}
	if got := h.phases(); !slices.Equal(got, []abi.CallPhase{abi.CallPhaseIncoming, abi.CallPhaseAccepted, abi.CallPhaseActive, abi.CallPhaseEnded}) {
		t.Fatalf("phases %v", got)
	}
}

func decodeRelay(t *testing.T, packed []byte) *waBinary.Node {
	t.Helper()
	n := decode(t, packed)
	var find func(n *waBinary.Node) *waBinary.Node
	find = func(n *waBinary.Node) *waBinary.Node {
		if n.Tag == "relay" {
			return n
		}
		for _, c := range n.GetChildren() {
			if r := find(&c); r != nil {
				return r
			}
		}
		return nil
	}
	return find(n)
}

func TestAPeerAskingForStandardOpusIsAnsweredWithIt(t *testing.T) {
	h := newHarness(t, true)
	h.signal(peerOffer(false, CapabilityStandardOpusOffer))
	h.cli.take()
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	sent := h.cli.take()
	pre, acc := actionOf(sent[0]).GetChildren(), actionOf(sent[1]).GetChildren()
	if !bytes.Equal(pre[len(pre)-1].Content.([]byte), CapabilityStandardOpusPreaccept) {
		t.Fatalf("preaccept %v", sent[0])
	}
	last := acc[len(acc)-1]
	if last.Tag != "voip_settings" || !bytes.Equal(last.Content.([]byte), StandardOpusVoipSettings(false)) {
		t.Fatalf("accept %v", sent[1])
	}
	if h.media.opened[0].MLow {
		t.Fatal("the media opened with MLow")
	}
}

func TestAVideoOfferIsAnsweredWithVideo(t *testing.T) {
	h := newHarness(t, true)
	h.signal(peerOffer(true, CapabilityVideoOffer))
	h.cli.take()
	if !h.last().Video {
		t.Fatal("a video offer rings as a voice call")
	}
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	sent := h.cli.take()
	for _, c := range actionOf(sent[1]).GetChildren() {
		if c.Tag == "capability" {
			t.Fatal("a video accept carries no capability")
		}
	}
	if p := h.media.opened[0]; !p.Video || !p.CameraOn {
		t.Fatalf("media %+v", p)
	}
}

func TestDecliningAndMissing(t *testing.T) {
	h := newHarness(t, true)
	h.signal(peerOffer(false, CapabilityOffer))
	h.cli.take()
	h.command(abi.Command{Tag: abi.CommandTagDeclineCall})
	h.expectSent("call:reject→" + peerDev3.String())
	h.ended(abi.CallStatusDeclined)

	h.signal(peerOffer(false, CapabilityOffer))
	h.cli.take()
	h.signal(peerAction("terminate", peerDev3, nil))
	h.ended(abi.CallStatusMissed)

	h.signal(peerOffer(false, CapabilityOffer))
	h.cli.take()
	h.signal(peerAction("terminate", peerDev3, waBinary.Attrs{"reason": TerminateAcceptedElsewhere}))
	h.ended(abi.CallStatusAnsweredElsewhere)
	if len(h.entries) != 3 || len(h.media.opened) != 0 {
		t.Fatalf("entries %+v, media %d", h.entries, len(h.media.opened))
	}
}

func TestASecondOfferIsRefusedAsBusyAndAGroupOfferRefused(t *testing.T) {
	h := newHarness(t, true)
	h.signal(peerOffer(false, CapabilityOffer))
	h.cli.take()
	first := h.last().Generation

	second := peerOffer(false, CapabilityOffer)
	second.CallID = "00FFEEDDCCBBAA99887766554433221100"
	h.signal(second)
	sent := h.expectSent("receipt:offer→"+peerDev3.String(), "call:reject→"+peerDev3.String()+"(busy)")
	if actionOf(sent[1]).Attrs["call-id"] != second.CallID {
		t.Fatal("the busy reject is not the second call's")
	}
	if u := h.last(); u.Generation != first || u.Phase != abi.CallPhaseIncoming {
		t.Fatal("the first call was disturbed")
	}

	g := newHarness(t, true)
	group := peerOffer(false, CapabilityOffer)
	group.GroupJID = types.JID{User: "120363000000000001", Server: types.GroupServer}
	g.signal(group)
	g.expectSent("receipt:offer→"+peerDev3.String(), "call:reject→"+peerDev3.String())
	if len(g.updates) != 0 {
		t.Fatal("a group call rang")
	}
}

func TestAnOfferForAnotherDeviceFailsTheAnswer(t *testing.T) {
	h := newHarness(t, true)
	offer := peerOffer(false, CapabilityOffer)
	// Only the other device's key.
	dest := offer.Data.GetChildren()
	for i, c := range dest {
		if c.Tag == "destination" {
			dest[i].Content = dest[i].GetChildren()[:1]
		}
	}
	h.signal(offer)
	h.cli.take()
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.expectSent("call:preaccept→"+peerDev3.String(), "call:reject→"+peerDev3.String())
	h.ended(abi.CallStatusFailed)
	if len(h.errs) != 1 {
		t.Fatalf("errors %q", h.errs)
	}
}

// --- outgoing ---------------------------------------------------------------------

func startCall(h *harness, video bool, generation, cancel uint64) {
	h.command(abi.Command{Tag: abi.CommandTagStartCall, StartCall: abi.CommandStartCall{Chat: []byte(peerPN.String()), Video: video, Generation: generation, Cancel: cancel}})
}

func TestAnOutgoingCallIsOfferedRingsAndConnects(t *testing.T) {
	h := newHarness(t, true)
	startCall(h, false, 1<<47+900, 1)
	sent := h.expectSent("call:offer→" + peerLID.String())
	offer := actionOf(sent[0])
	if offer.Attrs["call-creator"] != ownLID || len(offer.Attrs["call-id"].(string)) != 32 {
		t.Fatalf("offer %v", sent[0])
	}
	var tags []string
	for _, c := range offer.GetChildren() {
		tags = append(tags, c.Tag)
	}
	if want := []string{"privacy", "audio", "net", "capability", "destination", "encopt"}; !slices.Equal(tags, want) {
		t.Fatalf("the offer's children %q, want %q (439)", tags, want)
	}
	u := h.last()
	if u.Phase != abi.CallPhaseDialing || u.Generation != 1<<47+900 || u.Direction != abi.CallDirectionOutgoing {
		t.Fatalf("placed: %+v", u)
	}
	p := h.media.opened[0]
	if !p.Outgoing || p.PeerLID != peerLID.String() || len(p.CallKey) != 32 || decodeRelay(t, p.Relay) == nil {
		t.Fatalf("media %+v", p)
	}
	if h.cli.offered != 1 {
		t.Fatal("the privacy token was not considered")
	}
	callID := offer.Attrs["call-id"].(string)

	h.signal(ourAction("preaccept", peerDev0, callID, nil))
	h.signal(ourAction("preaccept", peerDev3, callID, nil))
	if h.last().Phase != abi.CallPhaseRinging {
		t.Fatalf("ringing: %v", h.last().Phase)
	}
	// The relay came up while the phone rang: the answer makes it active.
	h.mediaEvent(MediaConnected)
	if h.last().Phase != abi.CallPhaseRinging {
		t.Fatal("active before the answer")
	}
	h.signal(ourAction("accept", peerDev3, callID, nil, capabilityNode2(CapabilityStandardOpusOffer)))
	h.expectSent("call:terminate→" + peerDev0.String() + "(" + TerminateAcceptedElsewhere + ")")
	h.expectCommands("rekey answering_lid=" + peerDev3.String() + " codec=opus")
	if h.last().Phase != abi.CallPhaseActive {
		t.Fatalf("answered: %v", h.last().Phase)
	}

	h.signal(ourAction("terminate", peerDev3, callID, nil))
	h.ended(abi.CallStatusAnswered)
	if u := h.last(); u.Phase != abi.CallPhaseEnded {
		t.Fatalf("%v", u.Phase)
	}
	if len(h.entries) != 1 || !h.entries[0].FromMe {
		t.Fatalf("filed %+v", h.entries)
	}
}

func TestAnOutgoingCallRejectedOrBusy(t *testing.T) {
	h := newHarness(t, true)
	startCall(h, false, 1<<47+901, 2)
	callID := actionOf(h.cli.take()[0]).Attrs["call-id"].(string)
	h.signal(ourAction("reject", peerDev3, callID, nil))
	h.expectSent("call:terminate→" + peerDev0.String() + "(" + TerminateRejectedElsewhere + ")")
	h.ended(abi.CallStatusDeclined)
	if h.last().Phase != abi.CallPhaseFailed {
		t.Fatal("a refused call is a failed one")
	}

	startCall(h, false, 1<<47+902, 3)
	callID = actionOf(h.cli.take()[0]).Attrs["call-id"].(string)
	h.signal(ourAction("reject", peerDev0, callID, waBinary.Attrs{"reason": RejectBusy}))
	if !live(h.last().Phase) {
		t.Fatal("one busy device ended the call")
	}
	h.signal(ourAction("reject", peerDev3, callID, waBinary.Attrs{"reason": RejectBusy}))
	h.ended(abi.CallStatusBusy)
	h.expectSent()
}

func TestHangingUpAnOutgoingCall(t *testing.T) {
	h := newHarness(t, true)
	startCall(h, false, 1<<47+903, 4)
	h.cli.take()
	h.command(abi.Command{Tag: abi.CommandTagHangupCall})
	h.expectSent("call:terminate→"+peerDev0.String(), "call:terminate→"+peerDev3.String())
	h.ended(abi.CallStatusNoAnswer)
	if len(h.entries) != 1 || h.entries[0].Status != abi.CallStatusNoAnswer {
		t.Fatalf("filed %+v", h.entries)
	}
}

func TestACallCancelledWhilePlaced(t *testing.T) {
	// Cancelled before the offer went out: nothing is sent or filed.
	h := newHarness(t, true)
	h.cli.gate = make(chan struct{})
	cancel := abi.Command{Tag: abi.CommandTagCancelCall, CancelCall: abi.CommandCancelCall{Token: 5}}
	if err := h.c.Accept(&cancel); err != nil {
		t.Fatal(err)
	}
	close(h.cli.gate)
	startCall(h, false, 1<<47+904, 5)
	h.expectSent()
	if u := h.last(); u.Phase != abi.CallPhaseFailed || len(h.entries) != 0 {
		t.Fatalf("%+v, filed %+v", u, h.entries)
	}

	// Hung up while the offer waited for the server: it ends with the
	// peer at once, as a call nobody answered.
	h = newHarness(t, true)
	h.cli.gate = make(chan struct{})
	cmd := abi.Command{Tag: abi.CommandTagStartCall, StartCall: abi.CommandStartCall{Chat: []byte(peerPN.String()), Generation: 1<<47 + 905, Cancel: 6}}
	if err := h.c.Accept(&cmd); err != nil {
		t.Fatal(err)
	}
	h.post(func() { h.c.Command(&cmd) })
	for h.cli.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	h.post(func() { h.c.Command(&abi.Command{Tag: abi.CommandTagHangupCall}) })
	time.Sleep(10 * time.Millisecond)
	close(h.cli.gate)
	h.settle()
	h.expectSent("call:offer→"+peerLID.String(), "call:terminate→"+peerDev0.String(), "call:terminate→"+peerDev3.String())
	h.ended(abi.CallStatusNoAnswer)
}

func TestTheOfferFailingOrTheMediaFailing(t *testing.T) {
	h := newHarness(t, true)
	h.cli.ack = func(waBinary.Node) (*waBinary.Node, error) {
		return nil, errors.New("the server did not answer the offer")
	}
	startCall(h, false, 1<<47+906, 7)
	h.cli.take()
	if u := h.last(); u.Phase != abi.CallPhaseFailed || len(h.errs) != 1 || len(h.entries) != 0 {
		t.Fatalf("%+v %q", u, h.errs)
	}

	h = newHarness(t, true)
	h.signal(peerOffer(false, CapabilityOffer))
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.cli.take()
	h.mediaEvent(MediaFailed)
	h.expectSent("call:terminate→" + peerDev3.String())
	h.ended(abi.CallStatusFailed)
	if len(h.media.closed) != 0 {
		t.Fatal("a media that ended itself was closed again")
	}
}

func TestOneCallAtATimeAndOneToOneOnly(t *testing.T) {
	h := newHarness(t, true)
	h.command(abi.Command{Tag: abi.CommandTagStartCall, StartCall: abi.CommandStartCall{Chat: []byte("120363000000000001@g.us"), Generation: 1<<47 + 907, Cancel: 8}})
	if u := h.last(); u.Phase != abi.CallPhaseFailed || len(h.errs) != 1 {
		t.Fatalf("a group call: %+v %q", u, h.errs)
	}
	startCall(h, false, 1<<47+908, 9)
	startCall(h, false, 1<<47+909, 10)
	if u := h.last(); u.Generation != 1<<47+909 || u.Phase != abi.CallPhaseFailed {
		t.Fatalf("the second call: %+v", u)
	}
	h.cli.take()
}

// --- video -------------------------------------------------------------------------

func activeAudioCall(t *testing.T) *harness {
	h := newHarness(t, true)
	h.signal(peerOffer(false, CapabilityOffer))
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.mediaEvent(MediaConnected)
	h.cli.take()
	h.media.takeCommands()
	return h
}

func TestAskingThePeerForVideo(t *testing.T) {
	h := activeAudioCall(t)
	h.command(abi.Command{Tag: abi.CommandTagSetCallCamera, SetCallCamera: abi.CommandSetCallCamera{On: true}})
	req := h.expectSent("call:video→" + peerDev3.String() + "[11]")[0]
	if actionOf(req).Attrs["voip_settings"] != "video" || req.Attrs["id"] == nil {
		t.Fatalf("the upgrade request %v", req)
	}
	h.expectCommands("video_start await_accept=true")
	h.signal(peerAction("video", peerDev3, waBinary.Attrs{"state": "4"}))
	h.expectCommands("video_enable")
	if u := h.last(); !u.Video || !u.CameraOn {
		t.Fatalf("accepted: %+v", u)
	}
	h.command(abi.Command{Tag: abi.CommandTagSetCallCamera, SetCallCamera: abi.CommandSetCallCamera{On: false}})
	h.expectSent("call:video→" + peerDev3.String() + "[6]")
	h.expectCommands("camera_off")
	h.signal(peerAction("video", peerDev3, waBinary.Attrs{"state": "0"}))
	h.expectCommands("video_stop")
	if u := h.last(); u.Video || u.CameraOn {
		t.Fatalf("downgraded: %+v", u)
	}
}

func TestAnUpgradeThePeerDoesNotAnswerIsCancelled(t *testing.T) {
	h := activeAudioCall(t)
	h.c.timing.Upgrade = 20 * time.Millisecond
	h.command(abi.Command{Tag: abi.CommandTagSetCallCamera, SetCallCamera: abi.CommandSetCallCamera{On: true}})
	h.cli.take()
	h.media.takeCommands()
	time.Sleep(60 * time.Millisecond)
	h.settle()
	h.expectSent("call:video→" + peerDev3.String() + "[9]")
	h.expectCommands("video_stop")
	if h.last().CameraOn {
		t.Fatal("the camera stays on")
	}
}

func TestAcceptingThePeersVideo(t *testing.T) {
	h := activeAudioCall(t)
	h.signal(peerAction("video", peerDev3, waBinary.Attrs{"state": "11", "dec": "H264"}))
	if !h.last().VideoRequested {
		t.Fatal("the peer's ask is not shown")
	}
	h.command(abi.Command{Tag: abi.CommandTagSetCallCamera, SetCallCamera: abi.CommandSetCallCamera{On: true}})
	sent := h.expectSent("call:video→"+peerDev3.String()+"[4]", "call:video→"+peerDev3.String()+"[1]")
	if actionOf(sent[0]).Attrs["dec"] != "H264,AV1" || actionOf(sent[1]).Attrs["dec"] != "H264" {
		t.Fatalf("%v", sent)
	}
	h.expectCommands("video_start await_accept=false")
	if u := h.last(); !u.Video || u.VideoRequested || !u.CameraOn {
		t.Fatalf("%+v", u)
	}
	h.mediaEvent(MediaRemoteVideo)
	if !h.last().RemoteVideo {
		t.Fatal("the peer's picture is not shown")
	}
	h.signal(peerAction("video", peerDev3, waBinary.Attrs{"state": "6"}))
	h.expectCommands("peer_video on=false")
	if h.last().RemoteVideo {
		t.Fatal("a stopped camera is still shown")
	}
}

func TestPeerAudioHealth(t *testing.T) {
	h := activeAudioCall(t)
	h.do(func() {
		h.c.mediaEvent(MediaEvent{Generation: h.last().Generation, Kind: MediaSilent, Reason: "no_decoder"})
	})
	if u := h.last(); !u.HasPeerAudio || u.PeerAudio.Tag != abi.PeerAudioTagSilent || u.PeerAudio.Silent.Reason != abi.SilenceReasonNoDecoder {
		t.Fatalf("%+v", u.PeerAudio)
	}
	h.mediaEvent(MediaAudible)
	if h.last().HasPeerAudio {
		t.Fatal("the alarm stays")
	}
}

// --- without the library --------------------------------------------------------

func TestWithoutTheLibraryOffersAreRefused(t *testing.T) {
	h := newHarness(t, false)
	if h.c.Available() {
		t.Fatal("calls without media")
	}
	h.signal(peerOffer(false, CapabilityOffer))
	h.expectSent("receipt:offer→"+peerDev3.String(), "call:reject→"+peerDev3.String()+"(busy)")
	if len(h.updates) != 0 {
		t.Fatal("an offer rang without media")
	}
}

func TestTheLibraryIsFoundAsTheCoreIs(t *testing.T) {
	dir := t.TempDir()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	app := dir + "/Zap.app/Contents/MacOS/zap"
	if _, err := resolveLib(app, dir, env(map[string]string{EnvLibrary: "/elsewhere/" + LibName})); !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("a bundle without the library: %v", err)
	}
	if _, err := resolveLib(dir+"/zap", dir, env(nil)); !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("nothing to load: %v", err)
	}
	if _, err := resolveLib(dir+"/zap", dir, env(map[string]string{EnvLibrary: dir + "/none.dylib"})); !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("a missing $%s: %v", EnvLibrary, err)
	}
	built := dir + "/target/core-release/" + LibName
	mkfile(t, built)
	if p, err := resolveLib(dir+"/zap", dir+"/gnui", env(nil)); err != nil || p != built {
		t.Fatalf("the cargo build: %q %v", p, err)
	}
	other := dir + "/ssd/debug/" + LibName
	mkfile(t, other)
	if p, err := resolveLib(dir+"/zap", "/", env(map[string]string{"CARGO_TARGET_DIR": dir + "/ssd"})); err != nil || p != other {
		t.Fatalf("$CARGO_TARGET_DIR's build: %q %v", p, err)
	}
}

func mkfile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
