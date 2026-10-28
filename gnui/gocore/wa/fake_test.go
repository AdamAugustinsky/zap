package wa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"gonativeui/app/secret"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore"
)

// The tests run the engine in a real gocore.Conn, on a real archive and
// whatsmeow store (SQLCipher in a temporary folder, keys in memory), over
// fakeClient: no network, no WhatsApp account. whatsmeow's events are
// injected through the handler the engine registered, and every abi event
// the Conn queues is drained and kept.

var (
	me    = types.NewJID("15550001111", types.DefaultUserServer)
	meLID = types.NewJID("98765432101234", types.HiddenUserServer)
	peer  = types.NewJID("4917663430455", types.DefaultUserServer)
	peer2 = types.NewJID("4917600000002", types.DefaultUserServer)
	group = types.NewJID("120363025246125888", types.GroupServer)
)

type sentMessage struct {
	chat types.JID
	id   string
	msg  *waE2E.Message
}

type readReceipt struct {
	ids    []types.MessageID
	chat   types.JID
	sender types.JID
	kind   types.ReceiptType
}

// fakeClient stands in for whatsmeow: it records every call and answers
// as the test sets it up.
type fakeClient struct {
	mu        sync.Mutex
	pn, lid   types.JID
	pushName  string
	connected bool
	handler   func(any)
	qr        chan whatsmeow.QRChannelItem
	nextID    int
	calls     []string

	sent     []sentMessage
	peer     []*waE2E.Message
	patches  []appstate.PatchInfo
	reads    []readReceipt
	states   []types.Presence
	typing   []types.ChatPresence
	follows  []types.JID
	uploads  []whatsmeow.MediaType
	uploaded [][]byte
	lids     map[types.JID]types.JID

	sendErr, appStateErr, logoutErr, pairErr, downloadErr error
	pairCode                                              string
	downloaded                                            []byte
	picture                                               *types.ProfilePictureInfo
	pictureBytes                                          []byte
}

func newFake(linked bool) *fakeClient {
	f := &fakeClient{pushName: "Ada", lids: map[types.JID]types.JID{}}
	if linked {
		f.pn, f.lid = me, meLID
	}
	return f
}

func (f *fakeClient) call(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

func (f *fakeClient) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

func (f *fakeClient) setConnected(on bool) {
	f.mu.Lock()
	f.connected = on
	f.mu.Unlock()
}

func (f *fakeClient) emit(evt any) {
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	h(evt)
}

func (f *fakeClient) OwnID() (types.JID, types.JID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pn, f.lid
}

func (f *fakeClient) PushName() string { return f.pushName }

func (f *fakeClient) PNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lids[lid], nil
}

func (f *fakeClient) AddEventHandler(h func(any)) {
	f.mu.Lock()
	f.handler = h
	f.mu.Unlock()
}

func (f *fakeClient) QRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	f.call("QRChannel")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qr = make(chan whatsmeow.QRChannelItem, 8)
	return f.qr, nil
}

// qrEvent is what whatsmeow's QR channel makes of an events.QR: its first
// code (the channel rotates the rest itself).
func (f *fakeClient) qrEvent(evt *events.QR) {
	f.mu.Lock()
	ch := f.qr
	f.mu.Unlock()
	ch <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: evt.Codes[0], Timeout: time.Minute}
}

func (f *fakeClient) Connect(context.Context) error { f.call("Connect"); return nil }
func (f *fakeClient) Disconnect()                   { f.call("Disconnect"); f.setConnected(false) }
func (f *fakeClient) ResetConnection()              { f.call("ResetConnection") }

func (f *fakeClient) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeClient) Logout(context.Context) error {
	f.call("Logout")
	return f.logoutErr
}

func (f *fakeClient) Forget(context.Context) error { f.call("Forget"); return nil }

func (f *fakeClient) PairPhone(_ context.Context, phone string) (string, error) {
	f.call("PairPhone:" + phone)
	return f.pairCode, f.pairErr
}

func (f *fakeClient) GenerateMessageID() types.MessageID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return fmt.Sprintf("3EB0%04d", f.nextID)
}

func (f *fakeClient) SendMessage(_ context.Context, chat types.JID, msg *waE2E.Message, id types.MessageID) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chat, id, msg})
	return whatsmeow.SendResponse{ID: id, Timestamp: time.Now()}, f.sendErr
}

func (f *fakeClient) SendPeerMessage(_ context.Context, msg *waE2E.Message) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peer = append(f.peer, msg)
	return whatsmeow.SendResponse{}, nil
}

// ParseWebMessage is whatsmeow's, for the cases the tests make.
func (f *fakeClient) ParseWebMessage(chat types.JID, w *waWeb.WebMessageInfo) (*events.Message, error) {
	info := types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: w.GetKey().GetFromMe(), IsGroup: chat.Server == types.GroupServer},
		ID:            w.GetKey().GetID(),
		PushName:      w.GetPushName(),
		Timestamp:     time.Unix(int64(w.GetMessageTimestamp()), 0),
	}
	switch {
	case info.IsFromMe:
		info.Sender = f.pn
	case !info.IsGroup:
		info.Sender = chat
	default:
		s, err := types.ParseJID(w.GetParticipant())
		if err != nil {
			return nil, err
		}
		info.Sender = s
	}
	return &events.Message{Info: info, Message: w.GetMessage(), SourceWebMsg: w}, nil
}

func (f *fakeClient) MarkRead(_ context.Context, ids []types.MessageID, _ time.Time, chat, sender types.JID, kind types.ReceiptType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, readReceipt{ids, chat, sender, kind})
	return nil
}

func (f *fakeClient) SendChatPresence(_ context.Context, _ types.JID, state types.ChatPresence) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typing = append(f.typing, state)
	return nil
}

func (f *fakeClient) SendPresence(_ context.Context, state types.Presence) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, state)
	return nil
}

func (f *fakeClient) SubscribePresence(_ context.Context, jid types.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.follows = append(f.follows, jid)
	return nil
}

func (f *fakeClient) SendAppState(_ context.Context, patch appstate.PatchInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patches = append(f.patches, patch)
	return f.appStateErr
}

func (f *fakeClient) Upload(_ context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, kind)
	f.uploaded = append(f.uploaded, bytes.Clone(data))
	return whatsmeow.UploadResponse{URL: "https://mmg.example/x", DirectPath: "/v/x", MediaKey: []byte("key"), FileLength: uint64(len(data))}, nil
}

func (f *fakeClient) Download(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) {
	f.call("Download")
	return f.downloaded, f.downloadErr
}

func (f *fakeClient) ProfilePicture(context.Context, types.JID, bool) (*types.ProfilePictureInfo, error) {
	f.call("ProfilePicture")
	return f.picture, nil
}

func (f *fakeClient) Fetch(_ context.Context, url string, _ int64) ([]byte, error) {
	f.call("Fetch")
	if f.pictureBytes == nil {
		return nil, errors.New("no such picture")
	}
	return f.pictureBytes, nil
}

func (f *fakeClient) lastSent() sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return sentMessage{}
	}
	return f.sent[len(f.sent)-1]
}

func (f *fakeClient) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// memKeys is a Keychain in memory.
type memKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKeys) Get(account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[account]
	if !ok {
		return nil, secret.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (k *memKeys) Set(account string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[account] = bytes.Clone(v)
	return nil
}

// handlerFunc is a core.Handler.
type handlerFunc func(*abi.Event)

func (f handlerFunc) Event(e *abi.Event) { f(e) }

// harness is an engine in a Conn, over a fake, and the events it emitted.
type harness struct {
	t      *testing.T
	e      *Engine
	c      *gocore.Conn
	f      *fakeClient
	fakes  []*fakeClient // every client made, the newest last
	keys   *memKeys
	state  string
	cache  string
	events []abi.Event
}

type options struct {
	unlinked bool
	keys     *memKeys
	state    string
	cache    string
}

func newHarness(t *testing.T, o options) *harness {
	t.Helper()
	h := &harness{t: t, keys: o.keys, state: o.state, cache: o.cache}
	if h.keys == nil {
		h.keys = &memKeys{m: map[string][]byte{}}
	}
	if h.state == "" {
		h.state, h.cache = t.TempDir(), t.TempDir()
	}
	linked := !o.unlinked
	h.e = New(Options{Keys: h.keys, NewClient: func(*wastore.Device) Client {
		f := newFake(linked)
		// A client made after a logout belongs to a device not linked yet.
		linked = false
		h.f = f
		h.fakes = append(h.fakes, f)
		return f
	}})
	h.e.syncQuiet = 30 * time.Millisecond
	h.e.olderPatience = 30 * time.Millisecond
	c, err := gocore.Open(core.Config{StateDir: h.state, CacheDir: h.cache, Account: "1", KeyringService: "test.zap.wa"}, h.e)
	if err != nil {
		t.Fatal(err)
	}
	h.c = c
	t.Cleanup(func() {
		if err := c.Close(10 * time.Second); err != nil && !errors.Is(err, core.ErrClosed) {
			t.Errorf("close: %v", err)
		}
	})
	h.settle()
	return h
}

// settle waits until the engine is idle and drains what it emitted.
func (h *harness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	quiet := 0
	for quiet < 3 {
		if time.Now().After(deadline) {
			h.t.Fatal("the engine did not settle")
		}
		if h.e.idle() {
			quiet++
		} else {
			quiet = 0
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := h.c.Drain(handlerFunc(func(ev *abi.Event) {
		rec, err := ev.Encode(nil)
		if err != nil {
			h.t.Fatalf("re-encoding %v: %v", ev.Tag, err)
		}
		var c abi.Event
		if err := c.Decode(rec); err != nil {
			h.t.Fatal(err)
		}
		h.events = append(h.events, c)
	}), 0); err != nil {
		h.t.Fatal(err)
	}
}

// take settles and returns the events since the last take.
func (h *harness) take() []abi.Event {
	h.t.Helper()
	h.settle()
	evs := h.events
	h.events = nil
	return evs
}

// wait settles again after d (a timer of the engine's).
func (h *harness) wait(d time.Duration) []abi.Event {
	time.Sleep(d)
	return h.take()
}

func (h *harness) send(cmd abi.Command) {
	h.t.Helper()
	if err := h.c.Send(&cmd); err != nil {
		h.t.Fatalf("send %v: %v", cmd.Tag, err)
	}
}

func (h *harness) inject(evt any) { h.f.emit(evt) }

// start starts the engine and, when linked, brings the link up.
func (h *harness) start() {
	h.t.Helper()
	if err := h.c.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.settle()
	if pn, _ := h.f.OwnID(); !pn.IsEmpty() {
		h.f.setConnected(true)
		h.inject(&events.Connected{})
	}
	h.take()
}

func tags(evs []abi.Event) string {
	var names []string
	for _, ev := range evs {
		names = append(names, ev.Tag.String())
	}
	return strings.Join(names, " ")
}

func only(evs []abi.Event, tag abi.EventTag) []abi.Event {
	var out []abi.Event
	for _, ev := range evs {
		if ev.Tag == tag {
			out = append(out, ev)
		}
	}
	return out
}

func one(t *testing.T, evs []abi.Event, tag abi.EventTag) abi.Event {
	t.Helper()
	list := only(evs, tag)
	if len(list) != 1 {
		t.Fatalf("want one %v, got %d in %s", tag, len(list), tags(evs))
	}
	return list[0]
}

func expectTags(t *testing.T, evs []abi.Event, want string) {
	t.Helper()
	if got := tags(evs); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
}

// messagesOf flattens every Messages chunk.
func messagesOf(evs []abi.Event) []abi.Message {
	var out []abi.Message
	for _, ev := range evs {
		if ev.Tag == abi.EventTagMessages {
			out = append(out, ev.Messages.Messages...)
		}
	}
	return out
}

func lastError(t *testing.T, evs []abi.Event) string {
	t.Helper()
	list := only(evs, abi.EventTagError)
	if len(list) == 0 {
		t.Fatalf("no Error in %s", tags(evs))
	}
	return string(list[len(list)-1].Error.Text)
}

// message builds an incoming whatsmeow message event.
func message(chat, sender types.JID, id string, at time.Time, p *waE2E.Message) *events.Message {
	return &events.Message{Info: types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: sender == me, IsGroup: chat.Server == types.GroupServer},
		ID:            id,
		PushName:      "Grace",
		Timestamp:     at,
	}, Message: p}
}

func text(s string) *waE2E.Message { return &waE2E.Message{Conversation: &s} }
