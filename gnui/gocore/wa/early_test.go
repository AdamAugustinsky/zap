package wa

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

func readSelf(chat types.JID, ids ...string) *events.Receipt {
	return &events.Receipt{MessageSource: types.MessageSource{Chat: chat, Sender: me, IsFromMe: true}, MessageIDs: ids, Timestamp: t0, Type: types.ReceiptTypeReadSelf}
}

func reaction(chat, sender types.JID, target, emoji string, ms int64) *events.Message {
	p := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String(target), RemoteJID: proto.String(chat.String())}, Text: proto.String(emoji), SenderTimestampMS: proto.Int64(ms)}}
	return message(chat, sender, fmt.Sprintf("R-%s-%d", sender.User, ms), time.UnixMilli(ms), p)
}

func unreadOf(t *testing.T, h *harness, chat types.JID) int {
	t.Helper()
	c, err := h.e.arch.Chat(chat.String())
	if err != nil {
		t.Fatal(err)
	}
	return c.Unread
}

func reactionsOf(t *testing.T, h *harness, chat types.JID, id string) []string {
	t.Helper()
	m, err := h.e.arch.Message(chat.String(), id)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range m.Reactions {
		got = append(got, r.Sender+" "+r.Emoji)
	}
	slices.Sort(got)
	return got
}

// zf:src/backend/worker.rs the_phones_read_waits_for_a_message_filed_after_it
func TestThePhonesReadWaitsForItsMessage(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(readSelf(peer, "E1"))
	expectTags(t, h.take(), "")
	// The phone had read it: no count, no notification.
	h.inject(message(peer, peer, "E1", t0, text("early")))
	expectTags(t, h.take(), "Messages ChatUpdated")
	if n := unreadOf(t, h, peer); n != 0 {
		t.Fatalf("unread after the read message: %d", n)
	}
	// The read covers only its message.
	h.inject(message(peer, peer, "E2", t0.Add(time.Second), text("later")))
	expectTags(t, h.take(), "Messages ChatUpdated Incoming")
	if n := unreadOf(t, h, peer); n != 1 {
		t.Fatalf("unread after a new message: %d", n)
	}
	// The ordinary order still works.
	h.inject(readSelf(peer, "E2"))
	h.take()
	if n := unreadOf(t, h, peer); n != 0 {
		t.Fatalf("unread after the read: %d", n)
	}
}

// zf:src/backend/worker.rs the_phones_read_waits_for_a_message_from_history
func TestThePhonesReadWaitsForHistory(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "NEW", t0.Add(time.Hour), text("new")))
	h.inject(readSelf(peer, "OLD"))
	h.inject(reaction(peer, me, "OLD", "👍", t0.UnixMilli()+1000))
	h.take()
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(peer.String()), Messages: []*waHistorySync.HistorySyncMsg{webMessage("OLD", t0.Unix(), false, text("hi"))}}},
	}})
	h.take()
	if n := unreadOf(t, h, peer); n != 1 {
		t.Fatalf("the newer message stays unread: %d", n)
	}
	if got := reactionsOf(t, h, peer, "OLD"); !slices.Equal(got, []string{me.String() + " 👍"}) {
		t.Fatalf("reactions: %v", got)
	}
}

// zf:src/backend/worker.rs reactions_wait_for_their_message_and_the_newest_wins
func TestReactionsWaitForTheirMessage(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	ms := t0.UnixMilli()
	h.inject(reaction(peer, peer, "P1", "👍", ms+2000))
	h.inject(reaction(peer, peer, "P1", "", ms+3000))
	h.inject(reaction(peer, peer, "P1", "❤️", ms+1000))
	h.inject(reaction(peer, me, "P1", "😂", ms+2000))
	expectTags(t, h.take(), "")
	h.inject(message(peer, peer, "P1", t0, text("photo")))
	evs := h.take()
	// The removal was newer than both of the peer's reactions.
	if got := messagesOf(evs); len(got) != 1 || len(got[0].Reactions) != 1 || string(got[0].Reactions[0].Emoji) != "😂" {
		t.Fatalf("the filed message: %+v", got)
	}
	// After the message, a reaction applies at once, and so does removal.
	h.inject(reaction(peer, peer, "P1", "🙏", ms+5000))
	h.inject(reaction(peer, me, "P1", "", ms+5000))
	h.take()
	if got := reactionsOf(t, h, peer, "P1"); !slices.Equal(got, []string{peer.String() + " 🙏"}) {
		t.Fatalf("reactions: %v", got)
	}
}

// zf:src/backend/worker.rs an_early_read_under_a_privacy_id_follows_its_phone_number
func TestAnEarlyReadUnderAPrivacyIDFollowsItsNumber(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	lid := types.NewJID("167650256810092", types.HiddenUserServer)
	h.inject(message(peer, peer, "F1", t0, text("filed")))
	h.inject(readSelf(lid, "F1", "F2"))
	h.inject(reaction(lid, lid, "F1", "👍", t0.UnixMilli()+1000))
	h.take()
	if n := unreadOf(t, h, peer); n != 1 {
		t.Fatalf("unread before the mapping: %d", n)
	}
	// Anything that names the privacy id now maps it.
	h.f.lids[lid] = peer
	h.inject(&events.ChatPresence{MessageSource: types.MessageSource{Chat: lid, Sender: lid}, State: types.ChatPresenceComposing})
	evs := h.take()
	if n := unreadOf(t, h, peer); n != 0 {
		t.Fatalf("the mapping applies the waiting read: %d", n)
	}
	if len(only(evs, abi.EventTagMessageUpdated)) != 1 {
		t.Fatalf("events %s", tags(evs))
	}
	if got := reactionsOf(t, h, peer, "F1"); !slices.Equal(got, []string{peer.String() + " 👍"}) {
		t.Fatalf("reactions: %v", got)
	}
	// And keeps the other one waiting.
	h.inject(message(peer, peer, "F2", t0.Add(time.Second), text("later")))
	h.take()
	if n := unreadOf(t, h, peer); n != 0 {
		t.Fatalf("unread after the other message: %d", n)
	}
}

// zf:src/backend/worker/early_events.rs tests
func TestEarlyEventsAreBounded(t *testing.T) {
	var q earlyEvents
	now := time.Now()
	for n := range earlyMax + 1 {
		q.waitRead("chat", fmt.Sprint(n), now)
	}
	if q.takeRead("chat", "0") {
		t.Fatal("the oldest should have gone first")
	}
	if !q.takeRead("chat", fmt.Sprint(earlyMax)) {
		t.Fatal("the newest should wait")
	}
	q.waitReaction(earlyReaction{chat: "chat", target: "m", sender: "a", emoji: "👍", at: 1}, now)
	q.prune(now.Add(earlyMaxAge))
	if q.takeRead("chat", "1") || len(q.takeReactions("chat", "m")) != 0 {
		t.Fatal("events past their age should be dropped")
	}

	q.waitReaction(earlyReaction{chat: "1@lid", target: "m", sender: "1@lid", emoji: "👍", at: 1}, now)
	q.waitRead("1@lid", "r", now)
	if ids := q.rekey("1@lid", "2@s.whatsapp.net"); !slices.Equal(ids, []string{"m", "r"}) {
		t.Fatalf("rekeyed %v", ids)
	}
	if ids := q.rekey("1@lid", "2@s.whatsapp.net"); ids != nil {
		t.Fatalf("nothing left to move: %v", ids)
	}
	if r := q.takeReactions("2@s.whatsapp.net", "m"); len(r) != 1 || r[0].sender != "2@s.whatsapp.net" {
		t.Fatalf("reactions %+v", r)
	}
}

func theirReceipt(chat, sender types.JID, kind types.ReceiptType, at time.Time, ids ...string) *events.Receipt {
	return &events.Receipt{MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: chat.Server == types.GroupServer}, MessageIDs: ids, Timestamp: at, Type: kind}
}

func statusOf(t *testing.T, h *harness, chat types.JID, id string) string {
	t.Helper()
	m, err := h.e.arch.Message(chat.String(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Status
}

func nothingWaits(t *testing.T, h *harness, chat, id string) {
	t.Helper()
	if w, err := h.e.arch.TakeWaitingReceipts(chat, id); err != nil || len(w) != 0 {
		t.Fatalf("still waiting for %s: %+v %v", id, w, err)
	}
}

// zf:src/archive/receipts.rs receipts_that_beat_their_message_apply_when_it_arrives
func TestTheirReceiptsWaitForOurMessage(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(theirReceipt(peer, peer, types.ReceiptTypeDelivered, t0.Add(time.Minute), "S1"))
	h.inject(theirReceipt(peer, peer, types.ReceiptTypeRead, t0.Add(2*time.Minute), "S1"))
	// A status update's viewers are no chat's receipts.
	h.inject(theirReceipt(types.StatusBroadcastJID, peer, types.ReceiptTypeRead, t0, "ST"))
	expectTags(t, h.take(), "")
	nothingWaits(t, h, types.StatusBroadcastJID.String(), "ST")
	// The phone's copy of what it sent arrives with its ticks.
	h.inject(message(peer, me, "S1", t0, text("from the phone")))
	evs := h.take()
	if got := messagesOf(evs); len(got) != 1 || got[0].Status != abi.DeliveryRead {
		t.Fatalf("filed: %+v", got)
	}
	details, err := h.e.arch.ReceiptDetails(peer.String(), "S1")
	if err != nil || len(details) != 1 || details[0].DeliveredAt != t0.Add(time.Minute).Unix() || details[0].ReadAt != t0.Add(2*time.Minute).Unix() {
		t.Fatalf("times: %+v %v", details, err)
	}
	nothingWaits(t, h, peer.String(), "S1")
}

// zf:src/backend/worker.rs:5395, history filing what receipts waited for
func TestTheirReceiptsWaitForHistory(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(theirReceipt(peer, peer, types.ReceiptTypePlayed, t0.Add(time.Minute), "H1"))
	h.take()
	sent := webMessage("H1", t0.Unix(), true, text("voice"))
	sent.Message.Status = waWeb.WebMessageInfo_SERVER_ACK.Enum()
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(peer.String()), Messages: []*waHistorySync.HistorySyncMsg{sent}}},
	}})
	h.take()
	if s := statusOf(t, h, peer, "H1"); s != "played" {
		t.Fatalf("status %q", s)
	}
	nothingWaits(t, h, peer.String(), "H1")
}

// zf:src/backend/worker.rs:3993, a group message sent elsewhere
func TestEarlyGroupReceiptsMeetTheGroupsMembers(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	if err := h.e.arch.UpsertChat(store.Chat{ID: group.String(), Kind: "group", Name: "Team", Participants: []string{me.String(), peer.String(), peer2.String()}}); err != nil {
		t.Fatal(err)
	}
	h.inject(theirReceipt(group, peer, types.ReceiptTypeRead, t0.Add(time.Minute), "G1"))
	h.inject(theirReceipt(group, me, types.ReceiptTypeRead, t0.Add(time.Minute), "G1"))
	h.take()
	h.inject(message(group, me, "G1", t0, text("hello all")))
	h.take()
	details, err := h.e.arch.ReceiptDetails(group.String(), "G1")
	if err != nil || len(details) != 2 || details[0].Sender != peer2.String() || details[1].ReadAt != t0.Add(time.Minute).Unix() {
		t.Fatalf("the members become its audience: %+v %v", details, err)
	}
	// One member read it: not everyone has.
	if s := statusOf(t, h, group, "G1"); s != "delivered" {
		t.Fatalf("status %q", s)
	}
	h.inject(theirReceipt(group, peer2, types.ReceiptTypeRead, t0.Add(2*time.Minute), "G1"))
	h.take()
	if s := statusOf(t, h, group, "G1"); s != "read" {
		t.Fatalf("status %q", s)
	}
}

// zf:src/archive/receipts.rs waiting_receipts_follow_a_direct_chat_to_its_phone_number
func TestAnEarlyReceiptUnderAPrivacyIDFollowsItsNumber(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	lid := types.NewJID("167650256810092", types.HiddenUserServer)
	h.inject(theirReceipt(lid, lid, types.ReceiptTypeRead, t0.Add(time.Minute), "L1"))
	h.inject(message(peer, me, "L1", t0, text("from the phone")))
	h.take()
	if s := statusOf(t, h, peer, "L1"); s != "delivered" {
		t.Fatalf("before the mapping: %q", s)
	}
	h.f.lids[lid] = peer
	h.inject(&events.ChatPresence{MessageSource: types.MessageSource{Chat: lid, Sender: lid}, State: types.ChatPresenceComposing})
	evs := h.take()
	if m := only(evs, abi.EventTagMessageUpdated); len(m) != 1 || m[0].MessageUpdated.Message.Status != abi.DeliveryRead {
		t.Fatalf("events %s", tags(evs))
	}
	nothingWaits(t, h, peer.String(), "L1")
}
