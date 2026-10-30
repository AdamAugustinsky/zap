package wa

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/calls"
)

// The call bridge inside the engine: whatsmeow's call events reach the
// signaling, the call commands are answered, a finished call is filed in
// its chat and the call log, and the frames come from the call media. The
// signaling itself is gocore/calls' tests'.

type fakeCallMedia struct {
	mu       sync.Mutex
	opened   []calls.OpenParams
	released int
}

func (m *fakeCallMedia) Open(p calls.OpenParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened = append(m.opened, p)
	return nil
}
func (m *fakeCallMedia) Command(uint64, calls.MediaCommand) error { return nil }
func (m *fakeCallMedia) Close(uint64) error                       { return nil }
func (m *fakeCallMedia) Next(d time.Duration) (calls.MediaEvent, bool, error) {
	time.Sleep(d)
	return calls.MediaEvent{}, false, nil
}
func (m *fakeCallMedia) Devices() (abi.DeviceList, error) { return abi.DeviceList{}, nil }
func (m *fakeCallMedia) Frame(stream uint32) (core.Frame, bool) {
	return core.Frame{W: 2, H: 2, PTS: int64(stream)}, true
}
func (m *fakeCallMedia) ReleaseFrame(uint32) { m.mu.Lock(); m.released++; m.mu.Unlock() }
func (m *fakeCallMedia) Free()               {}

type fakeCallClient struct {
	mu   sync.Mutex
	sent []string
	ids  int
}

func (c *fakeCallClient) Own() (types.JID, types.JID) {
	pn, lid := me, meLID
	pn.Device, lid.Device = 4, 4
	return pn, lid
}
func (c *fakeCallClient) NewID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids++
	return fmt.Sprint("CALL-", c.ids)
}
func (c *fakeCallClient) Send(_ context.Context, n waBinary.Node) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, n.GetChildren()[0].Tag)
	return nil
}
func (c *fakeCallClient) SendAwait(context.Context, waBinary.Node, time.Duration) (*waBinary.Node, error) {
	return nil, fmt.Errorf("no server")
}
func (c *fakeCallClient) DecryptKey(context.Context, *waBinary.Node, types.JID, time.Time) ([]byte, error) {
	return bytes.Repeat([]byte{1}, 32), nil
}
func (c *fakeCallClient) EncryptKey(context.Context, []types.JID, []byte, string) ([]calls.DeviceKey, []byte, error) {
	return nil, nil, nil
}
func (c *fakeCallClient) PeerLID(_ context.Context, u types.JID) (types.JID, error)  { return u, nil }
func (c *fakeCallClient) Devices(context.Context, ...types.JID) ([]types.JID, error) { return nil, nil }
func (c *fakeCallClient) GroupMembers(context.Context, types.JID) ([]types.JID, error) {
	return nil, nil
}
func (c *fakeCallClient) PrivacyToken(context.Context, types.JID) []byte { return nil }
func (c *fakeCallClient) Offered(context.Context, types.JID)             {}

func (c *fakeCallClient) tags() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func TestACallRingsIsFiledAndListed(t *testing.T) {
	media, cli := &fakeCallMedia{}, &fakeCallClient{}
	h := newHarness(t, options{callMedia: media, callClient: cli})
	h.start()

	from := peer
	from.Device = 3
	offer := &waBinary.Node{Tag: "offer", Attrs: waBinary.Attrs{"call-id": "00CA11", "call-creator": from}, Content: []waBinary.Node{
		{Tag: "audio", Attrs: waBinary.Attrs{"enc": "opus", "rate": "16000"}},
		{Tag: "enc", Attrs: waBinary.Attrs{"v": "2", "type": "msg", "count": "0"}, Content: []byte("ct")},
		{Tag: "relay", Attrs: waBinary.Attrs{"uuid": "u"}},
	}}
	h.inject(&events.CallOffer{BasicCallMeta: types.BasicCallMeta{From: from, CallCreator: from, CallID: "00CA11",
		StanzaID: "S1", Timestamp: t0}, Data: offer})
	h.settle()
	ring := only(h.take(), abi.EventTagCall)
	if len(ring) != 1 || ring[0].Call.Update.Phase != abi.CallPhaseIncoming || string(ring[0].Call.Update.Chat) != peer.String() {
		t.Fatalf("ringing: %+v", ring)
	}

	h.send(abi.Command{Tag: abi.CommandTagAnswerCall})
	h.settle()
	if u := only(h.take(), abi.EventTagCall); len(u) != 1 || u[0].Call.Update.Phase != abi.CallPhaseAccepted {
		t.Fatalf("answered: %+v", u)
	}
	if len(media.opened) != 1 || media.opened[0].PeerLID != from.String() {
		t.Fatalf("media %+v", media.opened)
	}
	if f, ok := h.e.Frame(1); !ok || f.W != 2 {
		t.Fatal("the frames are not the call's")
	}
	h.e.ReleaseFrame(1)

	h.send(abi.Command{Tag: abi.CommandTagHangupCall})
	h.settle()
	evs := h.take()
	if u := only(evs, abi.EventTagCall); len(u) != 1 || u[0].Call.Update.Phase != abi.CallPhaseEnded {
		t.Fatalf("hung up: %s", tags(evs))
	}
	filed := only(evs, abi.EventTagMessages)
	if len(filed) != 1 || len(filed[0].Messages.Messages) != 1 {
		t.Fatalf("filed: %s", tags(evs))
	}
	m := filed[0].Messages.Messages[0]
	if m.Content.Tag != abi.ContentTagCall || m.Content.Call.Status != abi.CallStatusMissed || m.FromMe {
		t.Fatalf("the entry %+v", m.Content)
	}
	if got := cli.tags(); fmt.Sprint(got) != "[offer preaccept accept terminate]" {
		t.Fatalf("sent %q", got)
	}

	h.send(abi.Command{Tag: abi.CommandTagLoadCallLog})
	h.settle()
	log := only(h.take(), abi.EventTagCallLog)
	if len(log) != 1 || len(log[0].CallLog.Messages) != 1 || string(log[0].CallLog.Messages[0].ID) != "zapfast-call-00CA11" {
		t.Fatalf("the call log %+v", log)
	}
}

// A group call is filed in its group under the member who rang it; one
// joined through a link only reaches the Calls page (0.19.0's
// file_call_entry).
func TestGroupAndLinkCallsAreFiled(t *testing.T) {
	h := newHarness(t, options{callMedia: &fakeCallMedia{}, callClient: &fakeCallClient{}})
	h.start()
	h.take()
	group := "120363000000000001@g.us"
	h.e.post(func() {
		h.e.fileCall(calls.Entry{CallID: "00GROUP", Chat: group, Time: t0.Unix(), Status: abi.CallStatusMissed,
			Group: true, Caller: peer.String()})
	})
	h.settle()
	evs := h.take()
	filed := only(evs, abi.EventTagMessages)
	if len(filed) != 1 || len(filed[0].Messages.Messages) != 1 {
		t.Fatalf("filed: %s", tags(evs))
	}
	m := filed[0].Messages.Messages[0]
	if string(m.Chat) != group || string(m.Sender) != peer.String() || !m.Content.Call.Group {
		t.Fatalf("the group entry: chat %s sender %s %+v", m.Chat, m.Sender, m.Content.Call)
	}

	h.e.post(func() {
		h.e.fileCall(calls.Entry{CallID: "00LINK", Chat: calls.LinkChat, FromMe: true, Time: t0.Unix() + 60,
			Status: abi.CallStatusAnswered, Seconds: 30, Group: true})
	})
	h.settle()
	evs = h.take()
	if len(only(evs, abi.EventTagMessages)) != 0 || len(only(evs, abi.EventTagChatUpdated)) != 0 {
		t.Fatalf("a link's call reached a chat: %s", tags(evs))
	}
	log := only(evs, abi.EventTagCallLog)
	if len(log) != 1 || len(log[0].CallLog.Messages) != 2 || string(log[0].CallLog.Messages[0].Chat) != calls.LinkChat {
		t.Fatalf("the call log: %s", tags(evs))
	}
}

// The phone's call-log message is a call entry in its chat, and replaces
// the one this app filed for the same call (zf:src/backend/worker/calls.rs:891,
// call_log_content).
func TestACallLogMessageIsACallEntry(t *testing.T) {
	h := newHarness(t, options{callMedia: &fakeCallMedia{}, callClient: &fakeCallClient{}})
	h.start()
	h.e.post(func() {
		h.e.fileCall(calls.Entry{CallID: "00MINE", Chat: peer.String(), Time: t0.Unix(), Status: abi.CallStatusMissed, Caller: peer.String()})
	})
	h.take()
	log := &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{IsVideo: proto.Bool(true), CallOutcome: waE2E.CallLogMessage_REJECTED.Enum(), DurationSecs: proto.Int64(-4),
		Participants: []*waE2E.CallLogMessage_CallParticipant{{}, {}}}}
	h.inject(message(peer, peer, "CL1", t0.Add(30*time.Second), log))
	evs := h.take()
	if d := only(evs, abi.EventTagMessageDeleted); len(d) != 1 || string(d[0].MessageDeleted.ID) != "zapfast-call-00MINE" {
		t.Fatalf("the app's own entry stays: %s", tags(evs))
	}
	m := messagesOf(evs)
	if len(m) != 1 || m[0].Content.Tag != abi.ContentTagCall {
		t.Fatalf("the phone's entry: %+v", m)
	}
	if c := m[0].Content.Call; !c.Video || c.Status != abi.CallStatusDeclined || c.Duration != 0 || !c.Group {
		t.Fatalf("call %+v", c)
	}
	// Silenced or missed: nobody answered. No outcome: answered.
	for outcome, want := range map[*waE2E.CallLogMessage_CallOutcome]abi.CallStatus{
		waE2E.CallLogMessage_SILENCED_BY_DND.Enum(): abi.CallStatusMissed,
		nil: abi.CallStatusAnswered,
	} {
		if got := callLogEntry(&waE2E.CallLogMessage{CallOutcome: outcome}); callStatus(got.Status) != want {
			t.Fatalf("%v: %+v", outcome, got)
		}
	}
}
