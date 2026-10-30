package calls

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"zapfast/abi"
	"zapfast/gocore/voip/groupcall"
)

// The group-call signaling against the fake whatsmeow and media of
// calls_test.go: a call rung in its group, answered and keyed; one placed
// to a group; one joined through a link's waiting room. Every id and key is
// synthetic.

var (
	testGroup = types.JID{User: "120363000000000001", Server: types.GroupServer}
	bLID      = types.JID{User: "100000000003003", Server: types.HiddenUserServer}
	bDev      = types.JID{User: "100000000003003", Server: types.HiddenUserServer, Device: 1}
	cLID      = types.JID{User: "100000000004004", Server: types.HiddenUserServer}
	cDev      = types.JID{User: "100000000004004", Server: types.HiddenUserServer, Device: 2}
	outsider  = types.JID{User: "100000000009009", Server: types.HiddenUserServer, Device: 1}
)

const groupCallID = "00AAAAAAAAAAAAAAAAAAAAAAAAAAAA01"

var groupCall = CallJID(groupCallID)

// gUser is one roster entry: a device, its state and its pid (0: none).
type gUser struct {
	dev   types.JID
	state string
	pid   uint32
}

func groupInfo(callID string, creator types.JID, tx uint32, media string, rekey bool, users ...gUser) waBinary.Node {
	attrs := waBinary.Attrs{"call-id": callID, "call-creator": creator, "media": media,
		"transaction-id": strconv.Itoa(int(tx)), "connected-limit": "32"}
	if rekey {
		attrs["rekey"] = "1"
	}
	var children []waBinary.Node
	for _, u := range users {
		d := waBinary.Node{Tag: "device", Attrs: waBinary.Attrs{"jid": u.dev}}
		if u.pid != 0 {
			d.Attrs["pid"] = strconv.Itoa(int(u.pid))
		}
		children = append(children, waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": u.dev.ToNonAD(), "state": u.state},
			Content: []waBinary.Node{d}})
	}
	return waBinary.Node{Tag: "group_info", Attrs: attrs, Content: children}
}

func groupRelayWire() waBinary.Node { return groupRelayWireAt("1") }

// groupRelayWireAt is a relay allocation numbered tx.
func groupRelayWireAt(tx string) waBinary.Node {
	return waBinary.Node{Tag: "relay", Attrs: waBinary.Attrs{"uuid": "u", "participant_uuid": "p", "transaction-id": tx},
		Content: []waBinary.Node{
			{Tag: "key", Content: []byte("group-relay-key")},
			{Tag: "token", Attrs: waBinary.Attrs{"id": "0"}, Content: []byte("group-token")},
			{Tag: "auth_token", Attrs: waBinary.Attrs{"id": "0"}, Content: []byte("auth")},
			{Tag: "te2", Attrs: waBinary.Attrs{"relay_id": "0", "token_id": "0", "auth_token_id": "0", "relay_name": "test"},
				Content: []byte{127, 0, 0, 1, 0x0d, 0x96}},
		}}
}

// groupStanza is a <call> the call service routed from participant.
func groupStanza(participant types.JID, action waBinary.Node) any {
	attrs := waBinary.Attrs{"from": groupCall, "id": "S-G", "t": "1766847160"}
	if !participant.IsEmpty() {
		attrs["participant"] = participant
	}
	return &events.UnknownCallEvent{Node: &waBinary.Node{Tag: "call", Attrs: attrs, Content: []waBinary.Node{action}}}
}

func groupUpdateFrom(participant types.JID, tx uint32, rekey, relay bool, users ...gUser) any {
	children := []waBinary.Node{groupInfo(groupCallID, peerDev3, tx, "audio", rekey, users...)}
	if relay {
		children = append(children, groupRelayWire())
	}
	return groupStanza(participant, waBinary.Node{Tag: TagGroupUpdate,
		Attrs: waBinary.Attrs{"call-id": groupCallID, "call-creator": peerDev3}, Content: children})
}

func encRekeyFrom(participant types.JID, tx uint32, ct string) any {
	return groupStanza(participant, waBinary.Node{Tag: TagEncRekey,
		Attrs: waBinary.Attrs{"call-id": groupCallID, "call-creator": peerDev3, "transaction-id": strconv.Itoa(int(tx))},
		Content: []waBinary.Node{
			{Tag: "encopt", Attrs: waBinary.Attrs{"keygen": "2"}},
			{Tag: "enc", Attrs: waBinary.Attrs{"type": "msg", "v": "2"}, Content: []byte(ct)},
		}})
}

func groupMeta(participant types.JID) types.BasicCallMeta {
	return types.BasicCallMeta{From: groupCall, Participant: participant, Timestamp: time.Unix(1766847151, 0),
		CallCreator: peerDev3, CallID: groupCallID, GroupJID: testGroup, StanzaID: "STANZA-G"}
}

// groupOffer is the creator's group call ringing us, through the service.
func groupOffer(video bool, users ...gUser) *events.CallOffer {
	children := []waBinary.Node{{Tag: "audio", Attrs: waBinary.Attrs{"enc": "opus", "rate": "16000"}}}
	media := "audio"
	if video {
		children = append(children, waBinary.Node{Tag: "video", Attrs: waBinary.Attrs{"enc": "H264"}})
		media = "video"
	}
	children = append(children, groupInfo(groupCallID, peerDev3, 1, media, false, users...))
	return &events.CallOffer{BasicCallMeta: groupMeta(peerDev3), Data: &waBinary.Node{Tag: "offer",
		Attrs: waBinary.Attrs{"call-id": groupCallID, "call-creator": peerDev3, "group-jid": testGroup}, Content: children}}
}

func groupTerminateFrom(from, participant types.JID) any {
	m := groupMeta(participant)
	m.From = from
	return &events.CallTerminate{BasicCallMeta: m, Data: &waBinary.Node{Tag: "terminate",
		Attrs: waBinary.Attrs{"call-id": groupCallID, "call-creator": peerDev3}}}
}

func (h *harness) memberPicture(sender types.JID) {
	h.t.Helper()
	h.do(func() {
		h.c.mediaEvent(MediaEvent{Generation: h.last().Generation, Kind: MediaMemberVideo, Member: sender})
	})
}

func participantsOf(u abi.CallUpdate) []string {
	var out []string
	for _, p := range u.Group.Participants {
		s := string(p.ID)
		if p.Joined {
			s += "+"
		}
		if p.Video {
			s += "v"
		}
		out = append(out, s)
	}
	return out
}

func (f *fakeClient) epochFor(d types.JID) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.epochs[d]
}

func (m *fakeMedia) lastEpoch() Epoch {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.epochs[len(m.epochs)-1]
}

// --- incoming --------------------------------------------------------------------

func TestAGroupCallRingsInItsGroupIsAnsweredKeyedAndEndedByItsCreator(t *testing.T) {
	h := newHarness(t, true)
	h.signal(groupOffer(false,
		gUser{peerDev3, "connected", 1}, gUser{bDev, "connected", 2}, gUser{ownLID, "invited", 0}))
	receipt := h.expectSent("receipt:offer→" + groupCall.String())[0]
	if receipt.Attrs["from"] != ownPN {
		t.Fatalf("the receipt of a call-service offer comes from our phone number: %v", receipt.Attrs)
	}
	u := h.last()
	if u.Phase != abi.CallPhaseIncoming || string(u.Chat) != testGroup.String() || !u.HasGroup || u.Group.Link {
		t.Fatalf("ringing: %+v", u)
	}
	if string(u.Group.Caller) != peerPN.String() {
		t.Fatalf("caller %q", u.Group.Caller)
	}
	// Our own entry is left out; the creator is named by its chat.
	if got := participantsOf(u); !slices.Equal(got, []string{peerPN.String() + "+", bLID.String() + "+"}) {
		t.Fatalf("roster %q", got)
	}

	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.expectSent("call:preaccept→"+groupCall.String(), "call:accept→"+groupCall.String())
	if len(h.media.opened) != 0 {
		t.Fatal("the media opened without a relay")
	}
	if h.last().Phase != abi.CallPhaseAccepted {
		t.Fatalf("answered: %v", h.last().Phase)
	}

	// The service admits us with a relay, and asks us for an epoch.
	h.signal(groupUpdateFrom(types.EmptyJID, 2, true, true,
		gUser{peerDev3, "connected", 1}, gUser{bDev, "connected", 2}, gUser{ownLID, "connected", 3}))
	sent := h.expectSent("call:enc_rekey→"+peerDev3.String(), "call:enc_rekey→"+bDev.String())
	if actionOf(sent[0]).Attrs["transaction-id"] != "2" {
		t.Fatalf("the epoch's transaction: %v", sent[0])
	}
	if len(h.media.opened) != 1 {
		t.Fatalf("media opened %d times", len(h.media.opened))
	}
	p := h.media.opened[0]
	if p.Group == nil || p.Group.Initial.TransactionID != 2 || p.Group.Self != ownLID || p.Group.Creator != peerDev3 ||
		p.CallKey != nil || p.Outgoing || p.CallID != groupCallID {
		t.Fatalf("the group media's parameters: %+v %+v", p, p.Group)
	}
	h.expectCommands("group epoch=2")
	ours := h.media.lastEpoch()
	if len(ours.Key) != 32 || !bytes.Equal(ours.Key, h.cli.epochFor(peerDev3)) || !bytes.Equal(ours.Key, h.cli.epochFor(bDev)) {
		t.Fatal("the epoch installed is not the one shared")
	}
	if id, ok := p.Group.MemberID(bDev); !ok || id != bLID.String() {
		t.Fatalf("a member's pictures go on %q %v", id, ok)
	}

	// A member's own epoch.
	theirs := bytes.Repeat([]byte{0xb0}, 32)
	h.cli.plain["epoch-from-b"], h.cli.senders = theirs, map[types.JID]bool{bDev: true, outsider: true}
	h.signal(encRekeyFrom(bDev, 2, "epoch-from-b"))
	h.expectCommands("group epoch=2")
	if !bytes.Equal(h.media.lastEpoch().Key, theirs) {
		t.Fatal("the member's epoch was not installed")
	}
	// Nobody else's, and nothing but 32 bytes.
	h.cli.plain["epoch-from-x"] = theirs
	h.signal(encRekeyFrom(outsider, 2, "epoch-from-x"))
	h.cli.plain["short"] = theirs[:31]
	h.signal(encRekeyFrom(bDev, 2, "short"))
	h.expectCommands()

	// A relay moved on an older roster asks for no new epoch.
	moved := groupUpdateFrom(types.EmptyJID, 1, true, false,
		gUser{peerDev3, "connected", 1}, gUser{bDev, "connected", 2}, gUser{ownLID, "connected", 3})
	act := &moved.(*events.UnknownCallEvent).Node.GetChildren()[0]
	act.Content = append(act.GetChildren(), groupRelayWireAt("5"))
	h.signal(moved)
	h.expectSent()
	h.expectCommands("group roster=2")

	// A roster from a member is refused; the service's is followed.
	h.signal(groupUpdateFrom(bDev, 3, false, false, gUser{peerDev3, "connected", 1}))
	h.expectCommands()
	h.signal(groupUpdateFrom(types.EmptyJID, 3, false, false,
		gUser{peerDev3, "connected", 1}, gUser{bDev, "connected", 2}, gUser{ownLID, "connected", 3}, gUser{cDev, "connected", 4}))
	h.expectCommands("group roster=3")
	h.expectSent()
	if got := participantsOf(h.last()); len(got) != 3 || got[2] != cLID.String()+"+" {
		t.Fatalf("C joined: %q", got)
	}

	h.mediaEvent(MediaConnected)
	if h.last().Phase != abi.CallPhaseActive {
		t.Fatalf("connected: %v", h.last().Phase)
	}
	h.memberPicture(bDev)
	if got := participantsOf(h.last()); got[1] != bLID.String()+"+v" {
		t.Fatalf("B's picture: %q", got)
	}
	// B leaves: their tile goes dark.
	h.signal(groupUpdateFrom(types.EmptyJID, 4, false, false,
		gUser{peerDev3, "connected", 1}, gUser{ownLID, "connected", 3}, gUser{cDev, "connected", 4}))
	if got := participantsOf(h.last()); !slices.Equal(got, []string{peerPN.String() + "+", cLID.String() + "+"}) {
		t.Fatalf("B left: %q", got)
	}
	h.media.takeCommands()

	// A member who is not the creator cannot end it; the service can.
	h.signal(groupTerminateFrom(groupCall, cDev))
	if !live(h.last().Phase) {
		t.Fatal("a member ended the group call")
	}
	h.signal(groupTerminateFrom(groupCall, types.EmptyJID))
	h.ended(abi.CallStatusAnswered)
	if len(h.entries) != 1 {
		t.Fatalf("filed %+v", h.entries)
	}
	e := h.entries[0]
	if e.Chat != testGroup.String() || !e.Group || e.FromMe || e.Caller != peerPN.String() || e.Status != abi.CallStatusAnswered {
		t.Fatalf("filed %+v", e)
	}
}

func TestAGroupCallDeclinedOrMissed(t *testing.T) {
	h := newHarness(t, true)
	h.signal(groupOffer(true, gUser{peerDev3, "connected", 1}, gUser{ownLID, "invited", 0}))
	h.cli.take()
	if !h.last().Video {
		t.Fatal("a video group call rings as one")
	}
	h.command(abi.Command{Tag: abi.CommandTagDeclineCall})
	h.expectSent("call:reject→" + groupCall.String())
	h.ended(abi.CallStatusDeclined)

	// The creator gives up before we answer: missed, in the group.
	h.signal(groupOffer(false, gUser{peerDev3, "connected", 1}, gUser{ownLID, "invited", 0}))
	h.cli.take()
	h.signal(groupTerminateFrom(groupCall, peerDev3))
	h.ended(abi.CallStatusMissed)
	if len(h.entries) != 2 || h.entries[1].Chat != testGroup.String() || !h.entries[1].Group {
		t.Fatalf("filed %+v", h.entries)
	}
	if len(h.media.opened) != 0 {
		t.Fatal("media opened")
	}
}

func TestAGroupCallsRelayThatNeverComesFailsIt(t *testing.T) {
	h := newHarness(t, true)
	h.c.timing.Ack = 20 * time.Millisecond
	h.signal(groupOffer(false, gUser{peerDev3, "connected", 1}, gUser{ownLID, "invited", 0}))
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.cli.take()
	time.Sleep(60 * time.Millisecond)
	h.settle()
	h.expectSent("call:terminate→" + groupCall.String())
	h.ended(abi.CallStatusFailed)
}

func TestAGroupEpochThatCannotReachEveryDeviceEndsTheCall(t *testing.T) {
	h := newHarness(t, true)
	h.cli.noKey = map[types.JID]bool{bDev: true}
	h.signal(groupOffer(false, gUser{peerDev3, "connected", 1}, gUser{bDev, "connected", 2}, gUser{ownLID, "invited", 0}))
	h.command(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.cli.take()
	h.signal(groupUpdateFrom(types.EmptyJID, 2, true, true,
		gUser{peerDev3, "connected", 1}, gUser{bDev, "connected", 2}, gUser{ownLID, "connected", 3}))
	h.expectSent("call:enc_rekey→"+peerDev3.String(), "call:terminate→"+groupCall.String())
	h.ended(abi.CallStatusFailed)
	if len(h.errs) != 1 || !strings.Contains(h.errs[0], "key could not be shared") {
		t.Fatalf("errors %q", h.errs)
	}
}

// --- outgoing --------------------------------------------------------------------

func groupAck(rekey bool, users ...gUser) func(offer waBinary.Node) (*waBinary.Node, error) {
	return func(offer waBinary.Node) (*waBinary.Node, error) {
		id, _ := actionOf(offer).Attrs["call-id"].(string)
		return &waBinary.Node{Tag: "ack", Attrs: waBinary.Attrs{"class": "call", "type": "offer", "id": offer.Attrs["id"]},
			Content: []waBinary.Node{groupInfo(id, ownLID, 1, "audio", rekey, users...), groupRelayWire()}}, nil
	}
}

func startGroupCall(h *harness, generation uint64) {
	h.command(abi.Command{Tag: abi.CommandTagStartCall, StartCall: abi.CommandStartCall{Chat: []byte(testGroup.String()),
		Generation: generation, Cancel: generation}})
}

func TestAGroupCallIsPlacedFromItsGroup(t *testing.T) {
	h := newHarness(t, true)
	h.cli.members = []types.JID{ownLID.ToNonAD(), peerPN, bLID}
	h.cli.devices = map[types.JID][]types.JID{peerLID: {peerDev3}, bLID: {bDev}}
	h.cli.ack = groupAck(true, gUser{ownLID, "connected", 1}, gUser{peerDev3, "invited", 0}, gUser{bDev, "invited", 0})
	startGroupCall(h, 1<<47+950)
	sent := h.cli.take()
	if len(sent) != 1 || actionOf(sent[0]).Tag != "offer" {
		t.Fatalf("sent %q", shape(sent))
	}
	offer := sent[0]
	act := actionOf(offer)
	callID, _ := act.Attrs["call-id"].(string)
	if offer.Attrs["to"] != CallJID(callID) {
		t.Fatalf("the offer goes to the call: %v", offer.Attrs)
	}
	if act.Attrs["call-creator"] != ownLID || act.Attrs["group-jid"] != testGroup || len(callID) != 32 {
		t.Fatalf("offer %v", offer)
	}
	// Nobody else is connected yet: the epoch is ours alone.
	h.expectCommands("group epoch=1")
	u := h.last()
	if u.Phase != abi.CallPhaseRinging || u.Direction != abi.CallDirectionOutgoing || string(u.Chat) != testGroup.String() || !u.HasGroup {
		t.Fatalf("placed: %+v", u)
	}
	if len(h.media.opened) != 1 || h.media.opened[0].Group == nil || !h.media.opened[0].Outgoing {
		t.Fatal("the media did not open on the ack's relay")
	}

	// B joins: the call is answered, and the media follows the roster.
	join := groupStanza(types.EmptyJID, waBinary.Node{Tag: TagGroupUpdate,
		Attrs: waBinary.Attrs{"call-id": callID, "call-creator": ownLID},
		Content: []waBinary.Node{groupInfo(callID, ownLID, 2, "audio", false,
			gUser{ownLID, "connected", 1}, gUser{peerDev3, "invited", 0}, gUser{bDev, "connected", 2})}})
	join.(*events.UnknownCallEvent).Node.Attrs["from"] = CallJID(callID)
	h.signal(join)
	h.expectCommands("group roster=2")
	if h.last().Phase != abi.CallPhaseConnecting {
		t.Fatalf("someone joined: %v", h.last().Phase)
	}
	h.mediaEvent(MediaConnected)
	if h.last().Phase != abi.CallPhaseActive {
		t.Fatalf("connected: %v", h.last().Phase)
	}
	h.command(abi.Command{Tag: abi.CommandTagSetCallMuted, SetCallMuted: abi.CommandSetCallMuted{Muted: true}})
	h.expectSent("call:mute_v2→" + CallJID(callID).String())
	h.command(abi.Command{Tag: abi.CommandTagHangupCall})
	h.expectSent("call:terminate→" + CallJID(callID).String())
	h.ended(abi.CallStatusAnswered)
	if len(h.entries) != 1 || !h.entries[0].FromMe || !h.entries[0].Group || h.entries[0].Chat != testGroup.String() {
		t.Fatalf("filed %+v", h.entries)
	}
}

func TestAGroupCallTheServerRefuses(t *testing.T) {
	h := newHarness(t, true)
	h.cli.members = []types.JID{peerLID, bLID}
	h.cli.devices = map[types.JID][]types.JID{peerLID: {peerDev3}, bLID: {bDev}}
	h.cli.ack = func(offer waBinary.Node) (*waBinary.Node, error) {
		return &waBinary.Node{Tag: "ack", Attrs: waBinary.Attrs{"class": "call", "type": "offer", "id": offer.Attrs["id"], "error": "427"}}, nil
	}
	startGroupCall(h, 1<<47+951)
	h.cli.take()
	if u := h.last(); u.Phase != abi.CallPhaseFailed || len(h.errs) != 1 || !strings.Contains(h.errs[0], "427") {
		t.Fatalf("refused: %+v %q", u, h.errs)
	}
	if len(h.entries) != 0 || len(h.media.opened) != 0 {
		t.Fatal("a refused group call was filed or opened")
	}

	// One other person is no group call.
	h.cli.members = []types.JID{peerLID}
	startGroupCall(h, 1<<47+952)
	if len(h.cli.take()) != 0 || len(h.errs) != 2 || !strings.Contains(h.errs[1], "at least two") {
		t.Fatalf("a group of two: %q", h.errs)
	}
}

// --- call links ------------------------------------------------------------------

const linkToken = "TESTLINKTOKEN"

func linkAck(waiting bool, users ...gUser) func(waBinary.Node) (*waBinary.Node, error) {
	return func(req waBinary.Node) (*waBinary.Node, error) {
		n := &waBinary.Node{Tag: "ack", Attrs: waBinary.Attrs{"class": "call", "type": TagLinkJoin, "id": req.Attrs["id"]}}
		if waiting {
			n.Content = []waBinary.Node{{Tag: "waiting_room", Attrs: waBinary.Attrs{"call-id": groupCallID, "call-creator": peerDev3,
				"link-token": linkToken, "media": "audio", "enabled": "1"}}}
		} else {
			n.Content = []waBinary.Node{groupInfo(groupCallID, peerDev3, 1, "audio", true, users...), groupRelayWire()}
		}
		return n, nil
	}
}

func joinLink(h *harness, token string, generation uint64) {
	h.command(abi.Command{Tag: abi.CommandTagJoinCallLink, JoinCallLink: abi.CommandJoinCallLink{Token: []byte(token),
		Generation: generation, Cancel: generation}})
}

func TestACallLinkIsJoinedThroughItsWaitingRoom(t *testing.T) {
	h := newHarness(t, true)
	h.c.timing.Heartbeat = time.Hour
	h.cli.ack = linkAck(true)
	joinLink(h, "https://call.whatsapp.com/audio/"+linkToken+"?src=chat", 1<<47+960)
	sent := h.expectSent("call:link_join→call", "call:heartbeat→"+groupCall.String())
	if actionOf(sent[0]).Attrs["token"] != linkToken {
		t.Fatalf("the join %v", sent[0])
	}
	u := h.last()
	if u.Phase != abi.CallPhaseConnecting || !u.Group.Link || len(u.Chat) != 0 || len(h.media.opened) != 0 {
		t.Fatalf("waiting: %+v", u)
	}

	// Admitted: an update that lists this device connected.
	h.signal(groupUpdateFrom(types.EmptyJID, 2, true, true, gUser{peerDev3, "connected", 1}, gUser{ownLID, "connected", 2}))
	h.expectSent("call:enc_rekey→" + peerDev3.String())
	h.expectCommands("group epoch=2")
	if len(h.media.opened) != 1 || !h.media.opened[0].Outgoing || h.last().Phase != abi.CallPhaseAccepted {
		t.Fatalf("admitted: %v, %d", h.last().Phase, len(h.media.opened))
	}
	h.mediaEvent(MediaConnected)
	h.command(abi.Command{Tag: abi.CommandTagHangupCall})
	h.expectSent("call:terminate→" + groupCall.String())
	h.ended(abi.CallStatusAnswered)
	if len(h.entries) != 1 || h.entries[0].Chat != LinkChat || !h.entries[0].Group || !h.entries[0].FromMe {
		t.Fatalf("filed %+v", h.entries)
	}
}

func TestACallLinkAdmittedAtOnce(t *testing.T) {
	h := newHarness(t, true)
	h.cli.ack = linkAck(false, gUser{peerDev3, "connected", 1}, gUser{ownLID, "connected", 2})
	joinLink(h, linkToken, 1<<47+961)
	h.expectSent("call:link_join→call", "call:enc_rekey→"+peerDev3.String())
	h.expectCommands("group epoch=1")
	if len(h.media.opened) != 1 || h.last().Phase != abi.CallPhaseAccepted {
		t.Fatalf("admitted: %v", h.last().Phase)
	}
}

func TestACallLinkWaitingRoomLostOrLeft(t *testing.T) {
	h := newHarness(t, true)
	h.c.timing.Heartbeat = 5 * time.Millisecond
	h.cli.ack = linkAck(true)
	h.cli.sendErr = func(n waBinary.Node) error {
		if actionOf(n).Tag == "heartbeat" {
			return errors.New("the socket is closed")
		}
		return nil
	}
	joinLink(h, linkToken, 1<<47+962)
	deadline := time.Now().Add(2 * time.Second)
	for live(h.last().Phase) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		h.settle()
	}
	h.ended(abi.CallStatusFailed)
	if len(h.errs) != 1 || !strings.Contains(h.errs[0], "waiting room") {
		t.Fatalf("errors %q", h.errs)
	}

	// Leaving the waiting room says nothing to the call.
	l := newHarness(t, true)
	l.c.timing.Heartbeat = time.Hour
	l.cli.ack = linkAck(true)
	joinLink(l, linkToken, 1<<47+963)
	l.cli.take()
	l.command(abi.Command{Tag: abi.CommandTagHangupCall})
	l.expectSent()
	l.ended(abi.CallStatusNoAnswer)

	// A bad link is refused before anything is sent.
	b := newHarness(t, true)
	joinLink(b, "https://call.whatsapp.com/video/"+linkToken, 1<<47+964)
	if len(b.cli.take()) != 0 || b.last().Phase != abi.CallPhaseFailed || len(b.errs) != 1 {
		t.Fatalf("a video link joined for audio: %+v %q", b.last(), b.errs)
	}
}

func TestCallLinkTokens(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		media    groupcall.CallLinkMedia
	}{
		{"  TOKEN  ", "TOKEN", groupcall.LinkAudio},
		{"https://call.whatsapp.com/video/TOKEN#x", "TOKEN", groupcall.LinkVideo},
		{"https://call.whatsapp.com/audio/TOKEN", "", groupcall.LinkVideo},
		{"https://call.whatsapp.com/audio/TOKEN/extra", "", groupcall.LinkAudio},
		{"https://example.com/audio/TOKEN", "", groupcall.LinkAudio},
		{"a/b", "", groupcall.LinkAudio},
		{"", "", groupcall.LinkAudio},
	} {
		got, err := normalizeCallLinkToken(tc.in, tc.media)
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("%q: %q %v", tc.in, got, err)
		}
	}
}
