package wa

import (
	"errors"
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/core"
)

func labelsOf(t *testing.T, evs []abi.Event) []abi.Label {
	t.Helper()
	return one(t, evs, abi.EventTagLabels).Labels.Labels
}

func TestLabels(t *testing.T) {
	h := linked(t)

	h.send(abi.Command{Tag: abi.CommandTagCreateLabel, CreateLabel: abi.CommandCreateLabel{Name: []byte("Work"), ColorHex: []byte("#ff0000")}})
	got := labelsOf(t, h.take())
	if len(got) != 1 || string(got[0].Name) != "Work" || string(got[0].ColorHex) != "#ff0000" {
		t.Fatalf("created: %+v", got)
	}
	id := got[0].ID

	h.send(abi.Command{Tag: abi.CommandTagUpdateLabel, UpdateLabel: abi.CommandUpdateLabel{ID: id, Name: []byte("Office"), ColorHex: []byte("#00ff00")}})
	if got := labelsOf(t, h.take()); len(got) != 1 || string(got[0].Name) != "Office" || string(got[0].ColorHex) != "#00ff00" {
		t.Fatalf("updated: %+v", got)
	}

	h.send(abi.Command{Tag: abi.CommandTagSetChatLabels, SetChatLabels: abi.CommandSetChatLabels{Chat: chatID(peer), Labels: [][]byte{id}}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; len(c.Labels) != 1 || string(c.Labels[0]) != string(id) {
		t.Fatalf("labelled: %q", c.Labels)
	}

	h.send(abi.Command{Tag: abi.CommandTagDeleteLabel, DeleteLabel: abi.CommandDeleteLabel{ID: id}})
	if got := labelsOf(t, h.take()); len(got) != 0 {
		t.Fatalf("deleted: %+v", got)
	}

	// The phone's labels are filed beside ours.
	h.inject(&events.LabelEdit{LabelID: "5", Timestamp: t0, Action: &waSyncAction.LabelEditAction{Name: proto.String("Phone"), Color: proto.Int32(1)}})
	got = labelsOf(t, h.take())
	if len(got) != 1 || string(got[0].ID) != "wa-5" || string(got[0].Name) != "Phone" || string(got[0].ColorHex) != "#64c4ff" {
		t.Fatalf("from the phone: %+v", got)
	}
	h.inject(&events.LabelAssociationChat{JID: peer, LabelID: "5", Timestamp: t0, Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; len(c.Labels) != 1 || string(c.Labels[0]) != "wa-5" {
		t.Fatalf("labelled on the phone: %q", c.Labels)
	}
	h.inject(&events.LabelEdit{LabelID: "5", Timestamp: t0, Action: &waSyncAction.LabelEditAction{Deleted: proto.Bool(true)}})
	if got := labelsOf(t, h.take()); len(got) != 0 {
		t.Fatalf("deleted on the phone: %+v", got)
	}
}

func TestStars(t *testing.T) {
	h := linked(t)

	h.send(abi.Command{Tag: abi.CommandTagSetStar, SetStar: abi.CommandSetStar{Chat: chatID(peer), Message: []byte("M1"), Starred: true}})
	sc := one(t, h.take(), abi.EventTagStarChanged).StarChanged
	if string(sc.Message) != "M1" || !sc.Starred || !sc.Local {
		t.Fatalf("starred: %+v", sc)
	}
	if p := h.f.patches[len(h.f.patches)-1]; p.Mutations[0].Index[0] != "star" {
		t.Fatalf("patch: %+v", p)
	}

	h.send(abi.Command{Tag: abi.CommandTagLoadStarred})
	list := one(t, h.take(), abi.EventTagStarredList).StarredList
	if len(list.Starred) != 1 || string(list.Starred[0].Message.ID) != "M1" {
		t.Fatalf("starred list: %+v", list)
	}

	// Opening the chat lists its stars.
	h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: chatID(peer)}})
	if ids := one(t, h.take(), abi.EventTagStars).Stars.Ids; len(ids) != 1 || string(ids[0].ID) != "M1" {
		t.Fatalf("stars: %+v", ids)
	}

	// WhatsApp refusing: the star stays as it was.
	h.f.appStateErr = errors.New("refused")
	h.send(abi.Command{Tag: abi.CommandTagSetStar, SetStar: abi.CommandSetStar{Chat: chatID(peer), Message: []byte("M1")}})
	evs := h.take()
	if len(only(evs, abi.EventTagStarChanged)) != 0 || lastError(t, evs) != "Could not star the message: refused" {
		t.Fatalf("refused: %s", tags(evs))
	}
	h.f.appStateErr = nil

	// The phone unstars it.
	h.inject(&events.Star{ChatJID: peer, MessageID: "M1", Timestamp: time.Now().Add(time.Hour), Action: &waSyncAction.StarAction{Starred: proto.Bool(false)}})
	if sc := one(t, h.take(), abi.EventTagStarChanged).StarChanged; sc.Starred || sc.Local {
		t.Fatalf("unstarred on the phone: %+v", sc)
	}
}

func TestMessagePins(t *testing.T) {
	h := linked(t)

	h.send(abi.Command{Tag: abi.CommandTagSetMessagePinned, SetMessagePinned: abi.CommandSetMessagePinned{Chat: chatID(peer), Message: []byte("M1"), Pinned: true}})
	evs := h.take()
	if pc := one(t, evs, abi.EventTagPinChanged).PinChanged; string(pc.Message) != "M1" || !pc.Pinned {
		t.Fatalf("pinned: %+v", pc)
	}
	if items := one(t, evs, abi.EventTagPins).Pins.Items; len(items) != 1 || string(items[0].ID) != "M1" || !items[0].Pinner.ByMe {
		t.Fatalf("pins: %+v", items)
	}
	sent := h.f.lastSent().msg
	if pin := sent.GetPinInChatMessage(); pin.GetType() != waE2E.PinInChatMessage_PIN_FOR_ALL || pin.GetKey().GetID() != "M1" || sent.GetMessageContextInfo().GetMessageAddOnDurationInSecs() != pinSeconds {
		t.Fatalf("sent: %v", sent)
	}

	h.send(abi.Command{Tag: abi.CommandTagLoadPins, LoadPins: abi.CommandLoadPins{Chat: chatID(peer)}})
	if items := one(t, h.take(), abi.EventTagPins).Pins.Items; len(items) != 1 {
		t.Fatalf("loaded: %+v", items)
	}

	// Someone in the chat unpins it.
	unpin := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{
		Key:               &waCommon.MessageKey{ID: proto.String("M1"), RemoteJID: proto.String(peer.String())},
		Type:              waE2E.PinInChatMessage_UNPIN_FOR_ALL.Enum(),
		SenderTimestampMS: proto.Int64(time.Now().Add(time.Minute).UnixMilli()),
	}}
	h.inject(message(peer, peer, "U1", time.Now(), unpin))
	evs = h.take()
	if pc := one(t, evs, abi.EventTagPinChanged).PinChanged; pc.Pinned {
		t.Fatalf("unpinned: %+v", pc)
	}
	if items := one(t, evs, abi.EventTagPins).Pins.Items; len(items) != 0 {
		t.Fatalf("pins after unpin: %+v", items)
	}

	// Offline, nothing is pinned.
	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagSetMessagePinned, SetMessagePinned: abi.CommandSetMessagePinned{Chat: chatID(peer), Message: []byte("M1"), Pinned: true}})
	if evs := h.take(); lastError(t, evs) != "Not connected to WhatsApp" || len(only(evs, abi.EventTagPinChanged)) != 0 {
		t.Fatalf("offline: %s", tags(evs))
	}
}

// TestEveryCommandButTheCallsIsAnswered: the engine takes every command of
// the schema but WP7's calls and the demo's, and none of them, empty,
// upsets it.
func TestEveryCommandButTheCallsIsAnswered(t *testing.T) {
	refused := map[abi.CommandTag]bool{}
	for _, tag := range []abi.CommandTag{
		abi.CommandTagStartCall, abi.CommandTagJoinCallLink, abi.CommandTagCancelCall, abi.CommandTagAnswerCall,
		abi.CommandTagDeclineCall, abi.CommandTagHangupCall, abi.CommandTagSetCallMuted, abi.CommandTagSetCallCamera,
		abi.CommandTagSetCallMicrophone, abi.CommandTagSetCallSpeaker, abi.CommandTagSetCallCameraDevice,
		abi.CommandTagRefreshCallDevices, abi.CommandTagLoadCallLog,
		abi.CommandTagDemoIncoming, abi.CommandTagDemoTyping, abi.CommandTagDemoPanic,
	} {
		refused[tag] = true
	}
	var all []abi.CommandTag
	e := New(Options{})
	for i := range 1 << 10 {
		tag := abi.CommandTag(i)
		if tag.String() == "unknown" {
			continue
		}
		err := e.Accept(&abi.Command{Tag: tag})
		switch {
		case refused[tag] && err == nil:
			t.Errorf("%v is answered", tag)
		case !refused[tag] && err != nil:
			t.Errorf("%v is refused: %v", tag, err)
		case !refused[tag] && tag != abi.CommandTagPickAnswer:
			all = append(all, tag)
		}
	}
	if len(all) < 100 {
		t.Fatalf("only %d commands", len(all))
	}

	// Each, with an empty payload, through a linked engine; the account's
	// end comes last.
	last := []abi.CommandTag{abi.CommandTagUnlink, abi.CommandTagRemoveAccount}
	all = slices.DeleteFunc(all, func(tag abi.CommandTag) bool { return slices.Contains(last, tag) })
	h := linked(t)
	for _, tag := range append(all, last...) {
		// The Conn may find an empty payload invalid; it must not find the
		// command unanswered.
		var ce *core.Error
		if err := h.c.Send(&abi.Command{Tag: tag}); errors.As(err, &ce) && ce.Code == core.EState {
			t.Errorf("%v: %v", tag, err)
		}
		h.cancelDialogs()
	}
	if len(only(h.take(), abi.EventTagAccountRemoved)) != 1 {
		t.Fatal("the engine stopped answering")
	}
}

// cancelDialogs settles, cancelling every dialog the engine opens.
func (h *harness) cancelDialogs() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for quiet := 0; quiet < 3; {
		if time.Now().After(deadline) {
			h.t.Fatal("the engine did not settle")
		}
		h.drain()
		kept := h.events[:0]
		for _, ev := range h.events {
			if ev.Tag == abi.EventTagPickRequest {
				h.send(abi.Command{Tag: abi.CommandTagPickAnswer, PickAnswer: abi.CommandPickAnswer{ID: ev.PickRequest.ID}})
				quiet = 0
				continue
			}
			kept = append(kept, ev)
		}
		h.events = kept
		if h.e.idle() {
			quiet++
		} else {
			quiet = 0
		}
		time.Sleep(time.Millisecond)
	}
}
