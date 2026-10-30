package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
)

// History's system notices are no messages, as the phone mostly hides
// them, and a conversation of notices alone is no chat; a missed call's
// notice is a call. The HD copy of a photo is not a message of its own.
func TestNoticesAndHDCopies(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	stranger := types.NewJID("5519999990002", types.DefaultUserServer)
	notice := func(chat types.JID, id string, stub waWeb.WebMessageInfo_StubType) *waHistorySync.HistorySyncMsg {
		return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
			Key:              &waCommon.MessageKey{ID: proto.String(id), FromMe: proto.Bool(true), RemoteJID: proto.String(chat.String())},
			MessageTimestamp: proto.Uint64(uint64(t0.Unix())),
			MessageStubType:  stub.Enum(),
		}}
	}
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		Conversations: []*waHistorySync.Conversation{
			{ID: proto.String(stranger.String()), Messages: []*waHistorySync.HistorySyncMsg{notice(stranger, "N1", waWeb.WebMessageInfo_E2E_ENCRYPTED)}},
			conversation(peer, "Grace", webMessage("H1", t0.Unix(), false, text("hi")), notice(peer, "N2", waWeb.WebMessageInfo_CALL_MISSED_VIDEO)),
		},
	}})
	h.take()
	if _, err := h.e.arch.Chat(stranger.String()); !noRows(err) {
		t.Errorf("a conversation of notices alone made a chat: %v", err)
	}
	page, _, err := h.e.arch.Messages(peer.String(), "", 10)
	if err != nil || len(page) != 2 {
		t.Fatalf("Grace's chat: %d messages, %v", len(page), err)
	}
	for _, m := range page {
		if m.ID == "N2" && (m.Kind != store.KindCall || m.Call == nil || m.Call.Status != "missed" || !m.Call.Video) {
			t.Errorf("the missed call's notice: %+v", m)
		}
	}

	hd := &waE2E.Message{
		AssociatedChildMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}},
		MessageContextInfo:     &waE2E.MessageContextInfo{MessageAssociation: &waE2E.MessageAssociation{AssociationType: waE2E.MessageAssociation_HD_IMAGE_DUAL_UPLOAD.Enum()}},
	}
	h.inject(message(peer, peer, "HD1", t0.Add(time.Minute), hd))
	if evs := h.take(); len(evs) != 0 {
		t.Errorf("the HD copy of a photo was filed: %s", tags(evs))
	}
}

// What history filed before notices were told apart goes once a session:
// the notices, the chats they alone made, and the HD copies; a chat with a
// real message keeps it.
func TestStoredNoticesAreMended(t *testing.T) {
	h := newHarness(t, options{})
	stranger := types.NewJID("5519999990003", types.DefaultUserServer)
	hd, err := proto.Marshal(&waE2E.Message{
		AssociatedChildMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}},
		MessageContextInfo:     &waE2E.MessageContextInfo{MessageAssociation: &waE2E.MessageAssociation{AssociationType: waE2E.MessageAssociation_HD_VIDEO_DUAL_UPLOAD.Enum()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Unix() - 1000
	for _, c := range []store.Chat{{ID: stranger.String(), Kind: "direct"}, {ID: peer.String(), Kind: "direct"}} {
		if err := h.e.arch.UpsertChat(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.e.arch.SaveMessages([]store.Message{
		{ID: "S1", ChatID: stranger.String(), Sender: me.String(), FromMe: true, Timestamp: at, Kind: kindUnsupported, Status: "delivered"},
		{ID: "S2", ChatID: peer.String(), Sender: me.String(), FromMe: true, Timestamp: at, Kind: kindUnsupported, Status: "delivered"},
		{ID: "M1", ChatID: peer.String(), Sender: peer.String(), Timestamp: at + 1, Kind: kindText, Text: "real", Status: "delivered"},
		{ID: "V1", ChatID: peer.String(), Sender: peer.String(), Timestamp: at + 2, Kind: kindUnsupported, Text: "message", Status: "delivered", Raw: hd},
	}); err != nil {
		t.Fatal(err)
	}
	h.start()
	h.settle()
	if _, err := h.e.arch.Chat(stranger.String()); !noRows(err) {
		t.Errorf("the chat of notices alone stayed: %v", err)
	}
	page, _, err := h.e.arch.Messages(peer.String(), "", 10)
	if err != nil || len(page) != 1 || page[0].ID != "M1" {
		t.Fatalf("Grace's chat after mending: %+v %v", page, err)
	}
}
