package wa

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/calls"
	"zapfast/gocore/store"
)

func linkOf(t *testing.T, ev abi.Event) abi.LinkStatus {
	t.Helper()
	if ev.Tag != abi.EventTagLink {
		t.Fatalf("want Link, got %v", ev.Tag)
	}
	return ev.Link.Status
}

func TestOpenListsTheArchiveBeforeStart(t *testing.T) {
	keys := &memKeys{m: map[string][]byte{}}
	state, cache := t.TempDir(), t.TempDir()
	// What an earlier run archived.
	{
		dir := state + "/accounts/1"
		acct, err := store.OpenAccount(t.Context(), dir, keys, nil)
		if err != nil {
			t.Fatal(err)
		}
		a := acct.Archive
		for i, id := range []string{peer.String(), group.String()} {
			if err := a.UpsertChat(store.Chat{ID: id, Name: []string{"Grace", "Rust Berlin"}[i], Kind: archiveKind(id), LastActivity: int64(100 + i)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.SaveMessages([]store.Message{{ID: "A1", ChatID: peer.String(), Sender: peer.String(), Timestamp: 100, Kind: kindText, Text: "Hello"}}); err != nil {
			t.Fatal(err)
		}
		if err := a.SaveDraft(peer.String(), "half a thought"); err != nil {
			t.Fatal(err)
		}
		acct.Close()
	}
	h := newHarness(t, options{keys: keys, state: state, cache: cache})
	evs := h.take()
	expectTags(t, evs, "Me Chats Labels Drafts Contacts")
	me := one(t, evs, abi.EventTagMe).Me
	if string(me.ID) != "15550001111@s.whatsapp.net" || !me.HasLID || string(me.Name) != "Ada" {
		t.Fatalf("Me: %+v", me)
	}
	chats := one(t, evs, abi.EventTagChats).Chats
	if !chats.Last || len(chats.Chats) != 2 || string(chats.Chats[0].ID) != group.String() {
		t.Fatalf("Chats: %+v", chats)
	}
	grace := chats.Chats[1]
	if grace.Kind != abi.ChatKindDirect || !grace.HasLast || string(grace.Last.Summary) != "Hello" || chats.Chats[0].Kind != abi.ChatKindGroup {
		t.Fatalf("chat rows: %+v", chats.Chats)
	}
	drafts := one(t, evs, abi.EventTagDrafts).Drafts
	if len(drafts.Drafts) != 1 || string(drafts.Drafts[0].Text) != "half a thought" {
		t.Fatalf("Drafts: %+v", drafts)
	}
	if h.f.called("Connect") != 0 {
		t.Fatal("connected before the startup permit")
	}
}

func TestLinkingWithAQRCode(t *testing.T) {
	h := newHarness(t, options{unlinked: true})
	expectTags(t, h.take(), "Chats Labels Drafts")
	if err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	evs := h.take()
	if s := linkOf(t, evs[0]); s.Tag != abi.LinkStatusTagUnlinked || s.Unlinked.HasQR {
		t.Fatalf("first status: %+v", s)
	}
	if h.f.called("QRChannel") != 1 || h.f.called("Connect") != 1 {
		t.Fatalf("calls: %v", h.f.calls)
	}

	h.f.qrEvent(&events.QR{Codes: []string{"2@code-one", "2@code-two"}})
	s := linkOf(t, one(t, h.take(), abi.EventTagLink))
	if s.Tag != abi.LinkStatusTagUnlinked || string(s.Unlinked.QR) != "2@code-one" {
		t.Fatalf("QR status: %+v", s)
	}

	h.inject(&events.PairSuccess{ID: types.NewADJID(me.User, 0, 7), BusinessName: "", Platform: "android"})
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagConnecting {
		t.Fatalf("after pairing: %+v", s)
	}

	h.f.mu.Lock()
	h.f.pn, h.f.lid = me, meLID
	h.f.mu.Unlock()
	h.f.setConnected(true)
	h.inject(&events.Connected{})
	evs = h.take()
	expectTags(t, evs, "Link Me PinLimit")
	if s := linkOf(t, evs[0]); s.Tag != abi.LinkStatusTagConnected {
		t.Fatalf("connected: %+v", s)
	}
	if len(h.f.states) != 1 || h.f.states[0] != types.PresenceUnavailable {
		t.Fatalf("presence announced: %v", h.f.states)
	}
}

func TestPairWithPhone(t *testing.T) {
	h := newHarness(t, options{unlinked: true})
	h.c.Start()
	h.take()
	h.f.pairCode = "ABCD-EFGH"
	h.send(abi.Command{Tag: abi.CommandTagPairWithPhone, PairWithPhone: abi.CommandPairWithPhone{Phone: []byte("4917663430455")}})
	evs := h.take()
	expectTags(t, evs, "Link Link")
	first, second := linkOf(t, evs[0]), linkOf(t, evs[1])
	if string(first.Unlinked.PairingPhone) != "4917663430455" || first.Unlinked.HasPairCode {
		t.Fatalf("pairing: %+v", first)
	}
	if string(second.Unlinked.PairCode) != "ABCD-EFGH" || string(second.Unlinked.PairingPhone) != "4917663430455" {
		t.Fatalf("code: %+v", second)
	}
	if h.f.called("PairPhone:4917663430455") != 1 {
		t.Fatalf("calls: %v", h.f.calls)
	}

	h.f.pairErr = errors.New("rate limited")
	h.send(abi.Command{Tag: abi.CommandTagPairWithPhone, PairWithPhone: abi.CommandPairWithPhone{Phone: []byte("4917600000002")}})
	evs = h.take()
	expectTags(t, evs, "Link Error Link")
	if s := linkOf(t, evs[2]); s.Unlinked.HasPairingPhone || s.Unlinked.HasPairCode {
		t.Fatalf("after a failure: %+v", s)
	}
}

func TestConnectionStates(t *testing.T) {
	h := newHarness(t, options{})
	h.take()
	h.c.Start()
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagConnecting {
		t.Fatalf("a linked account connects: %+v", s)
	}
	h.f.setConnected(true)
	h.inject(&events.Connected{})
	expectTags(t, h.take(), "Link Me PinLimit")

	h.send(abi.Command{Tag: abi.CommandTagSetOnline, SetOnline: abi.CommandSetOnline{Online: true}})
	h.take()
	if got := h.f.states[len(h.f.states)-1]; got != types.PresenceAvailable {
		t.Fatalf("online: %v", got)
	}

	h.send(abi.Command{Tag: abi.CommandTagResumed})
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagConnecting || h.f.called("ResetConnection") != 1 {
		t.Fatalf("resumed: %+v %v", s, h.f.calls)
	}
	h.inject(&events.Connected{})
	h.take()

	h.f.setConnected(false)
	h.inject(&events.Disconnected{})
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagDisconnected || len(s.Disconnected.Reason) == 0 {
		t.Fatalf("disconnected: %+v", s)
	}
	// Resumed only reconnects a working link.
	h.send(abi.Command{Tag: abi.CommandTagResumed})
	if evs := h.take(); len(evs) != 0 {
		t.Fatalf("resumed while down: %s", tags(evs))
	}
	h.send(abi.Command{Tag: abi.CommandTagReconnect})
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagConnecting || h.f.called("Connect") != 2 {
		t.Fatalf("reconnect: %+v %v", s, h.f.calls)
	}
	h.f.setConnected(true)
	h.inject(&events.Connected{})
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagReconnect})
	h.take()
	if h.f.called("ResetConnection") != 2 {
		t.Fatalf("reconnect while up: %v", h.f.calls)
	}

	h.inject(&events.StreamReplaced{})
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagDisconnected {
		t.Fatalf("replaced: %+v", s)
	}
	h.inject(&events.ConnectFailure{Reason: events.ConnectFailureServiceUnavailable})
	if s := linkOf(t, one(t, h.take(), abi.EventTagLink)); s.Tag != abi.LinkStatusTagFailed {
		t.Fatalf("failure: %+v", s)
	}
}

func TestLoggedOutLinksAgain(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	old := h.f
	h.inject(&events.LoggedOut{OnConnect: false, Reason: events.ConnectFailureLoggedOut})
	evs := h.take()
	// The account's lists are emptied with its archive.
	expectTags(t, evs, "Chats Labels Drafts Link Link")
	evs = evs[3:]
	if linkOf(t, evs[0]).Tag != abi.LinkStatusTagLoggedOut || linkOf(t, evs[1]).Tag != abi.LinkStatusTagUnlinked {
		t.Fatalf("statuses: %+v", evs)
	}
	if h.f == old || h.f.called("QRChannel") != 1 || old.called("Disconnect") == 0 {
		t.Fatal("a new device should link")
	}
	// The old client's late events change nothing.
	old.emit(&events.Connected{})
	if evs := h.take(); len(evs) != 0 {
		t.Fatalf("stale events: %s", tags(evs))
	}
}

func TestUnlink(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagUnlink})
	evs := h.take()
	expectTags(t, evs, "Chats Labels Drafts Link Link")
	evs = evs[3:]
	if linkOf(t, evs[0]).Tag != abi.LinkStatusTagLoggedOut || h.fakes[0].called("Logout") != 1 || h.fakes[0].called("Forget") != 0 {
		t.Fatalf("unlink: %s %v", tags(evs), h.fakes[0].calls)
	}

	// Offline, the session is dropped here all the same.
	h.f.logoutErr = errors.New("not connected")
	h.f.mu.Lock()
	h.f.pn = me
	h.f.mu.Unlock()
	h.send(abi.Command{Tag: abi.CommandTagUnlink})
	h.take()
	if h.fakes[1].called("Forget") != 1 || len(h.fakes) != 3 {
		t.Fatalf("offline unlink: %v", h.fakes[1].calls)
	}
}

func TestRemoveAccount(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagRemoveAccount})
	expectTags(t, h.take(), "AccountRemoved")
	if h.f.called("Logout") != 1 || h.f.called("Disconnect") == 0 {
		t.Fatalf("calls: %v", h.f.calls)
	}
	// The account is gone: nothing more is answered.
	h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: []byte(peer.String())}})
	h.inject(&events.Connected{})
	if evs := h.take(); len(evs) != 0 {
		t.Fatalf("after removal: %s", tags(evs))
	}
}

func TestWrongKeyFailsTheLink(t *testing.T) {
	keys := &memKeys{m: map[string][]byte{}}
	state, cache := t.TempDir(), t.TempDir()
	h := newHarness(t, options{keys: keys, state: state, cache: cache})
	h.take()
	h.c.Close(5 * time.Second)
	for name := range keys.m {
		keys.m[name] = make([]byte, 32) // another key
	}
	h2 := newHarness(t, options{keys: keys, state: state, cache: cache})
	evs := h2.take()
	if s := linkOf(t, one(t, evs, abi.EventTagLink)); s.Tag != abi.LinkStatusTagFailed {
		t.Fatalf("wrong key: %+v", s)
	}
	h2.c.Start()
	h2.send(abi.Command{Tag: abi.CommandTagReconnect})
	if evs := h2.take(); len(evs) != 0 {
		t.Fatalf("a failed account answers: %s", tags(evs))
	}
}

// TestCallAndDemoCommandsAreRefused: without the call library a call is
// refused as unavailable (the call log still lists the calls), a call link
// always, and the demo's commands are not this engine's.
func TestCallAndDemoCommandsAreRefused(t *testing.T) {
	h := newHarness(t, options{})
	for _, tc := range []struct {
		cmd  abi.Command
		code int32
	}{
		{abi.Command{Tag: abi.CommandTagStartCall}, core.ERefused},
		{abi.Command{Tag: abi.CommandTagAnswerCall}, core.ERefused},
		{abi.Command{Tag: abi.CommandTagJoinCallLink}, core.ERefused},
		{abi.Command{Tag: abi.CommandTagDemoIncoming}, core.EState},
	} {
		err := h.c.Send(&tc.cmd)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != tc.code {
			t.Errorf("%v: %v", tc.cmd.Tag, err)
		}
	}
	var ce *core.Error
	if err := h.c.Send(&abi.Command{Tag: abi.CommandTagStartCall}); !errors.As(err, &ce) || !strings.Contains(ce.Msg, calls.LibName) {
		t.Errorf("the refusal does not say why: %v", err)
	}
	h.send(abi.Command{Tag: abi.CommandTagLoadCallLog})
	h.settle()
	if len(only(h.take(), abi.EventTagCallLog)) != 1 {
		t.Error("the call log is not listed")
	}
}
