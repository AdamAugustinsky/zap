package wa

import (
	"fmt"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAICommonDeprecated"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

var t0 = time.Unix(1_790_000_000, 0)

func TestIncomingContentKinds(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	ctx := &waE2E.ContextInfo{MentionedJID: []string{me.String()}}
	cases := []struct {
		name  string
		msg   *waE2E.Message
		check func(t *testing.T, m abi.Message)
	}{
		{"text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("see https://zap.example @15550001111"), MatchedText: proto.String("https://zap.example"), Title: proto.String("Zap"), ContextInfo: ctx}}, func(t *testing.T, m abi.Message) {
			c := m.Content
			if c.Tag != abi.ContentTagText || !c.Text.HasPreview || string(c.Text.Preview.URL) != "https://zap.example" || string(c.Text.Preview.Title) != "Zap" {
				t.Fatalf("text: %+v", c)
			}
			if len(m.Mentions) != 1 || string(m.Mentions[0].User) != "15550001111" || string(m.Mentions[0].ID) != me.String() {
				t.Fatalf("mentions: %+v", m.Mentions)
			}
		}},
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("look"), Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(2048), Width: proto.Uint32(640), Height: proto.Uint32(480), JPEGThumbnail: []byte{0xff, 0xd8, 1, 2}}}, func(t *testing.T, m abi.Message) {
			c := m.Content.Image
			if m.Content.Tag != abi.ContentTagImage || string(c.Caption) != "look" || c.Media.Width != 640 || c.Media.Size != 2048 || c.Media.HasPath || !m.HasThumbnail {
				t.Fatalf("image: %+v", m)
			}
		}},
		{"gif", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(3), GifPlayback: proto.Bool(true)}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.Video; m.Content.Tag != abi.ContentTagVideo || !c.Gif || c.Note || c.Seconds != 3 {
				t.Fatalf("gif: %+v", m.Content)
			}
		}},
		{"video note", &waE2E.Message{PtvMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(9)}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.Video; m.Content.Tag != abi.ContentTagVideo || !c.Note {
				t.Fatalf("video note: %+v", m.Content)
			}
		}},
		{"voice", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true), Seconds: proto.Uint32(7), Waveform: make([]byte, 64)}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.Audio; m.Content.Tag != abi.ContentTagAudio || !c.VoiceNote || c.Seconds != 7 || len(c.Waveform) != 64 {
				t.Fatalf("voice: %+v", m.Content)
			}
		}},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("plan.pdf"), Caption: proto.String("the plan")}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.Document; m.Content.Tag != abi.ContentTagDocument || string(c.FileName) != "plan.pdf" || string(c.Caption) != "the plan" {
				t.Fatalf("document: %+v", m.Content)
			}
		}},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), IsAnimated: proto.Bool(true)}}, func(t *testing.T, m abi.Message) {
			if m.Content.Tag != abi.ContentTagSticker || !m.Content.Sticker.Animated {
				t.Fatalf("sticker: %+v", m.Content)
			}
		}},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(52.52), DegreesLongitude: proto.Float64(13.4), Name: proto.String("Alexanderplatz"), Address: proto.String("Berlin")}}, func(t *testing.T, m abi.Message) {
			c := m.Content.Location
			if m.Content.Tag != abi.ContentTagLocation || c.Latitude != 52.52 || string(c.Name) != "Alexanderplatz" || string(c.Address) != "Berlin" {
				t.Fatalf("location: %+v", m.Content)
			}
		}},
		{"live location", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{DegreesLatitude: proto.Float64(1), DegreesLongitude: proto.Float64(2), SequenceNumber: proto.Int64(4), AccuracyInMeters: proto.Uint32(10)}}, func(t *testing.T, m abi.Message) {
			c := m.Content.LiveLocation
			if m.Content.Tag != abi.ContentTagLiveLocation || c.Sequence != 4 || c.AccuracyM != 10 || !c.HasAccuracyM {
				t.Fatalf("live: %+v", m.Content)
			}
		}},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Katherine"), Vcard: proto.String("BEGIN:VCARD\nEND:VCARD")}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.Contact; m.Content.Tag != abi.ContentTagContact || string(c.DisplayName) != "Katherine" || string(c.Vcard) != "BEGIN:VCARD\nEND:VCARD" {
				t.Fatalf("contact: %+v", m.Content)
			}
		}},
		{"poll", &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Yes")}, {OptionName: proto.String("No")}}, SelectableOptionsCount: proto.Uint32(1)}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.Poll; m.Content.Tag != abi.ContentTagPoll || string(c.Question) != "Lunch?" || len(c.Options) != 2 || c.State.Selectable != 1 {
				t.Fatalf("poll: %+v", m.Content)
			}
		}},
		{"view once", &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}}}, func(t *testing.T, m abi.Message) {
			if c := m.Content.PhoneOnly; m.Content.Tag != abi.ContentTagPhoneOnly || !c.ViewOnce || c.Once != abi.OnceMediaPhoto {
				t.Fatalf("view once: %+v", m.Content)
			}
		}},
		{"buttons", &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{ContentText: proto.String("Pick"), Buttons: []*waE2E.ButtonsMessage_Button{{Type: waE2E.ButtonsMessage_Button_RESPONSE.Enum(), ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: proto.String("OK")}}}}}, func(t *testing.T, m abi.Message) {
			c := m.Content.Interactive
			if m.Content.Tag != abi.ContentTagInteractive || !c.HasCard || len(c.Card.Buttons) != 1 || c.Card.Buttons[0].Action.Tag != abi.InteractiveActionTagReply {
				t.Fatalf("buttons: %+v", m.Content)
			}
		}},
		{"unknown", &waE2E.Message{ScheduledCallCreationMessage: &waE2E.ScheduledCallCreationMessage{}}, func(t *testing.T, m abi.Message) {
			if m.Content.Tag != abi.ContentTagUnsupported {
				t.Fatalf("unknown: %+v", m.Content)
			}
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := fmt.Sprintf("IN%02d", i)
			h.inject(message(peer, peer, id, t0.Add(time.Duration(i)*time.Second), c.msg))
			evs := h.take()
			want := "Messages ChatUpdated Incoming"
			if c.name == "text" || c.name == "image" || c.name == "document" {
				// A link, a picture or a document joins the chat's shared media.
				want += " ChatSharedChanged"
			}
			expectTags(t, evs, want)
			batch := evs[0].Messages
			if string(batch.Chat) != peer.String() || batch.Older || batch.Complete || !batch.Last || len(batch.Messages) != 1 {
				t.Fatalf("batch: %+v", batch)
			}
			m := evs[2].Incoming.Message
			if string(m.ID) != id || m.FromMe || string(m.SenderName) != "Grace" || m.Status != abi.DeliveryNone {
				t.Fatalf("message: %+v", m)
			}
			c.check(t, m)
			chat := evs[1].ChatUpdated.Chat
			if chat.Unread != uint32(i+1) || !chat.HasLast || string(chat.Last.Sender) != peer.String() {
				t.Fatalf("chat: %+v", chat)
			}
		})
	}
	// A duplicate delivery updates the row and rings nothing.
	h.inject(message(peer, peer, "IN00", t0, text("again")))
	expectTags(t, h.take(), "MessageUpdated ChatUpdated")
}

func TestQuotesAndOwnMessagesFromThePhone(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "Q1", t0, text("question")))
	h.take()
	reply := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("answer"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q1"), Participant: proto.String(peer.String()), QuotedMessage: text("question")}}}
	h.inject(message(peer, me, "R1", t0.Add(time.Second), reply))
	evs := h.take()
	// Ours, from the phone: no Incoming, and the chat is read.
	expectTags(t, evs, "Messages ChatUpdated")
	m := evs[0].Messages.Messages[0]
	if !m.FromMe || !m.HasQuoted || string(m.Quoted.ID) != "Q1" || string(m.Quoted.Summary) != "question" || string(m.Quoted.Sender) != peer.String() {
		t.Fatalf("quote: %+v", m.Quoted)
	}
	if evs[1].ChatUpdated.Chat.Unread != 0 {
		t.Fatal("a reply from the phone reads the chat")
	}
}

func TestEditsRevokesAndReactions(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "M1", t0, text("first")))
	h.take()

	edit := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: &waCommon.MessageKey{ID: proto.String("M1")}, EditedMessage: text("second")}}
	h.inject(message(peer, peer, "E1", t0.Add(time.Minute), edit))
	evs := h.take()
	expectTags(t, evs, "MessageUpdated ChatUpdated")
	m := evs[0].MessageUpdated.Message
	if string(m.Content.Text.Text) != "second" || !m.Edited || m.Timestamp != t0.Unix() {
		t.Fatalf("edited: %+v", m)
	}

	react := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("M1")}, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(t0.Add(2 * time.Minute).UnixMilli())}}
	h.inject(message(peer, peer, "X1", t0.Add(2*time.Minute), react))
	m = one(t, h.take(), abi.EventTagMessageUpdated).MessageUpdated.Message
	if len(m.Reactions) != 1 || string(m.Reactions[0].Emoji) != "👍" || m.Reactions[0].FromMe {
		t.Fatalf("reaction: %+v", m.Reactions)
	}
	// A reaction to a message not here is dropped quietly.
	h.inject(message(peer, peer, "X2", t0, &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("nope")}, Text: proto.String("x")}}))
	if evs := h.take(); len(evs) != 0 {
		t.Fatalf("orphan reaction: %s", tags(evs))
	}

	revoke := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: &waCommon.MessageKey{ID: proto.String("M1")}}}
	h.inject(message(peer, peer, "D1", t0.Add(3*time.Minute), revoke))
	evs = h.take()
	expectTags(t, evs, "MessageUpdated ChatUpdated")
	if m := evs[0].MessageUpdated.Message; m.Content.Tag != abi.ContentTagRevoked || m.Edited {
		t.Fatalf("revoked: %+v", m)
	}
}

func webMessage(id string, at int64, fromMe bool, p *waE2E.Message) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: proto.String(id), FromMe: proto.Bool(fromMe), RemoteJID: proto.String(peer.String())},
		MessageTimestamp: proto.Uint64(uint64(at)),
		Message:          p,
		Status:           waWeb.WebMessageInfo_READ.Enum(),
	}, MsgOrderID: proto.Uint64(uint64(at))}
}

func conversation(chat types.JID, name string, msgs ...*waHistorySync.HistorySyncMsg) *waHistorySync.Conversation {
	return &waHistorySync.Conversation{ID: proto.String(chat.String()), Name: proto.String(name), Messages: msgs, UnreadCount: proto.Uint32(2)}
}

func TestHistorySync(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.e.syncQuiet = 400 * time.Millisecond
	var msgs []*waHistorySync.HistorySyncMsg
	for i := range 130 {
		msgs = append(msgs, webMessage(fmt.Sprintf("H%03d", i), t0.Unix()+int64(i), i%3 == 0, text(fmt.Sprintf("line %d", i))))
	}
	edited := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: &waCommon.MessageKey{ID: proto.String("H129")}, EditedMessage: text("line 129, edited")}}
	msgs = append(msgs, webMessage("H-edit", t0.Unix()+500, false, edited))
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		Progress:      proto.Uint32(40),
		Conversations: []*waHistorySync.Conversation{conversation(peer, "Grace", msgs...), conversation(group, "Rust Berlin")},
	}})
	evs := h.take()
	expectTags(t, evs, "Syncing SyncProgress Chats Labels Drafts")
	if !evs[0].Syncing.Syncing || evs[1].SyncProgress.Percent != 40 {
		t.Fatalf("sync: %+v", evs[:2])
	}
	chats := evs[2].Chats.Chats
	if len(chats) != 2 || string(chats[0].ID) != peer.String() || chats[0].Unread != 2 || string(chats[0].Last.Summary) != "line 129, edited" || string(chats[1].Name) != "Rust Berlin" {
		t.Fatalf("chats: %+v", chats)
	}
	// History is no news: nothing rings while syncing, and Syncing ends
	// once the phone goes quiet.
	h.inject(message(peer, peer, "LIVE", t0.Add(time.Hour), text("live")))
	expectTags(t, h.take(), "Messages ChatUpdated")
	evs = h.wait(500 * time.Millisecond)
	expectTags(t, evs, "Syncing")
	if evs[0].Syncing.Syncing {
		t.Fatal("still syncing")
	}

	// Paging: 60, then 60 older, then the rest, complete.
	h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: []byte(peer.String())}})
	page := one(t, h.take(), abi.EventTagMessages).Messages
	if len(page.Messages) != 60 || page.Older || page.Complete || string(page.Messages[59].ID) != "LIVE" || string(page.Messages[0].ID) != "H071" {
		t.Fatalf("first page: %d %v %v %s", len(page.Messages), page.Older, page.Complete, page.Messages[0].ID)
	}
	if page.Messages[0].FromMe || page.Messages[2].Status != abi.DeliveryNone || page.Messages[1].Status != abi.DeliveryRead {
		t.Fatalf("statuses: %+v", page.Messages[:2])
	}
	if m := page.Messages[0]; !m.HasHistoryOrder || m.HistoryOrder != t0.Unix()+71 {
		t.Fatalf("history order: %+v", m)
	}
	for _, want := range []struct {
		first    string
		n        int
		complete bool
	}{{"H011", 60, false}, {"H000", 11, true}} {
		key := abi.PageKey{Timestamp: page.Messages[0].Timestamp, ID: page.Messages[0].ID}
		h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: []byte(peer.String()), Before: key, HasBefore: true}})
		page = one(t, h.take(), abi.EventTagMessages).Messages
		if len(page.Messages) != want.n || !page.Older || page.Complete != want.complete || string(page.Messages[0].ID) != want.first {
			t.Fatalf("older page: %d %v %v %s", len(page.Messages), page.Older, page.Complete, page.Messages[0].ID)
		}
	}
	if m := page.Messages[0]; !m.FromMe || m.Status != abi.DeliveryRead {
		t.Fatalf("our history message: %+v", m)
	}

	// LoadUntil reaches a quoted message from a page key.
	h.send(abi.Command{Tag: abi.CommandTagLoadUntil, LoadUntil: abi.CommandLoadUntil{Chat: []byte(peer.String()), ID: []byte("H005"), Before: abi.PageKey{Timestamp: t0.Unix() + 71, ID: []byte("H071")}}})
	page = one(t, h.take(), abi.EventTagMessages).Messages
	if len(page.Messages) != 66 || string(page.Messages[0].ID) != "H005" || string(page.Messages[65].ID) != "H070" || !page.Older || page.Complete {
		t.Fatalf("until: %d %s", len(page.Messages), page.Messages[0].ID)
	}
	h.send(abi.Command{Tag: abi.CommandTagLoadUntil, LoadUntil: abi.CommandLoadUntil{Chat: []byte(peer.String()), ID: []byte("gone"), Before: abi.PageKey{ID: []byte("H071")}}})
	expectTags(t, h.take(), "Messages Error")
}

func TestFetchOlder(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "N1", t0, text("newest")))
	h.take()
	fetch := abi.Command{Tag: abi.CommandTagFetchOlder, FetchOlder: abi.CommandFetchOlder{Chat: []byte(peer.String()), Explicit: true}}
	h.send(fetch)
	if evs := h.take(); len(evs) != 0 {
		t.Fatalf("asking: %s", tags(evs))
	}
	req := h.f.peer[0].GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest()
	if req.GetOldestMsgID() != "N1" || req.GetChatJID() != peer.String() || req.GetOnDemandMsgCount() != phoneBatch || req.GetOldestMsgTimestampMS() != t0.Unix() {
		t.Fatalf("request: %v", req)
	}
	// A second ask while one waits is the same request.
	h.send(fetch)
	h.take()
	if len(h.f.peer) != 1 {
		t.Fatal("asked twice")
	}
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{conversation(peer, "", webMessage("O1", t0.Unix()-20, false, text("older one")), webMessage("O2", t0.Unix()-10, false, text("older two")))},
	}})
	evs := h.take()
	expectTags(t, evs, "ChatUpdated Messages OlderFetched")
	if page := evs[1].Messages; !page.Older || page.Complete || len(page.Messages) != 2 || string(page.Messages[0].ID) != "O1" {
		t.Fatalf("answer: %+v", page)
	}
	if f := evs[2].OlderFetched; !f.More || f.Silent || evs[0].ChatUpdated.Chat.Unread != 1 {
		t.Fatalf("fetched: %+v", f)
	}

	// Unanswered: OlderFetched, and an Error for a request the reader made.
	h.send(fetch)
	evs = h.wait(80 * time.Millisecond)
	expectTags(t, evs, "OlderFetched Error")
	if !evs[0].OlderFetched.More {
		t.Fatal("more")
	}
	// A chunk nobody asked for pages quietly.
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(peer.String()), EndOfHistoryTransfer: proto.Bool(true), Messages: []*waHistorySync.HistorySyncMsg{webMessage("O0", t0.Unix()-30, false, text("oldest"))}}},
	}})
	evs = h.take()
	expectTags(t, evs, "ChatUpdated OlderFetched")
	if f := evs[1].OlderFetched; f.More || !f.Silent {
		t.Fatalf("unclaimed: %+v", f)
	}
	// The phone said it has nothing older.
	h.send(fetch)
	evs = h.take()
	if f := one(t, evs, abi.EventTagOlderFetched).OlderFetched; f.More || len(h.f.peer) != 2 {
		t.Fatalf("history start: %+v", f)
	}
	// Offline: nothing is asked.
	h.inject(message(peer2, peer2, "P1", t0, text("hi")))
	h.take()
	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagFetchOlder, FetchOlder: abi.CommandFetchOlder{Chat: []byte(peer2.String())}})
	if f := one(t, h.take(), abi.EventTagOlderFetched).OlderFetched; !f.More || len(h.f.peer) != 2 {
		t.Fatalf("offline: %+v", f)
	}
	h.f.setConnected(true)
	h.send(abi.Command{Tag: abi.CommandTagReloadHistory, ReloadHistory: abi.CommandReloadHistory{Chat: []byte(peer.String()), Message: []byte("N1")}})
	h.take()
	if len(h.f.peer) != 3 || h.f.peer[2].GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest().GetOldestMsgID() != "N1" {
		t.Fatal("reload")
	}
}

func TestReceiptsAndWatching(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: []byte(peer.String()), Text: []byte("ping")}})
	h.take()
	id := h.f.lastSent().id
	h.send(abi.Command{Tag: abi.CommandTagWatchReceipts, WatchReceipts: abi.CommandWatchReceipts{Target: abi.ReceiptTarget{Chat: []byte(peer.String()), Message: []byte(id)}, HasTarget: true}})
	r := one(t, h.take(), abi.EventTagReceipts).Receipts
	if string(r.Message) != id || len(r.Recipients) != 0 {
		t.Fatalf("receipts: %+v", r)
	}
	at := t0.Add(time.Minute)
	h.inject(&events.Receipt{MessageSource: types.MessageSource{Chat: peer, Sender: peer}, MessageIDs: []string{id}, Timestamp: at, Type: types.ReceiptTypeDelivered})
	evs := h.take()
	expectTags(t, evs, "MessageUpdated Receipts")
	if evs[0].MessageUpdated.Message.Status != abi.DeliveryDelivered {
		t.Fatalf("delivered: %+v", evs[0].MessageUpdated.Message.Status)
	}
	if rc := evs[1].Receipts.Recipients; len(rc) != 1 || rc[0].DeliveredAt != at.Unix() || rc[0].HasReadAt {
		t.Fatalf("recipient: %+v", rc)
	}
	h.inject(&events.Receipt{MessageSource: types.MessageSource{Chat: peer, Sender: peer}, MessageIDs: []string{id}, Timestamp: at.Add(time.Minute), Type: types.ReceiptTypeRead})
	evs = h.take()
	if m := evs[0].MessageUpdated.Message; m.Status != abi.DeliveryRead || !m.HasReadAt {
		t.Fatalf("read: %+v", m)
	}
	h.send(abi.Command{Tag: abi.CommandTagWatchReceipts})
	h.take()
	h.inject(&events.Receipt{MessageSource: types.MessageSource{Chat: peer, Sender: peer}, MessageIDs: []string{id}, Type: types.ReceiptTypePlayed})
	expectTags(t, h.take(), "MessageUpdated")
	// A receipt for a message not ours changes nothing.
	h.inject(&events.Receipt{MessageSource: types.MessageSource{Chat: peer, Sender: peer}, MessageIDs: []string{"unknown"}, Type: types.ReceiptTypeRead})
	if evs := h.take(); len(evs) != 0 {
		t.Fatalf("unknown receipt: %s", tags(evs))
	}
	// Read on another device: the chat is read here too.
	h.inject(message(peer, peer, "U1", t0.Add(time.Hour), text("unread")))
	h.take()
	h.inject(&events.Receipt{MessageSource: types.MessageSource{Chat: peer, Sender: me, IsFromMe: true}, MessageIDs: []string{"U1"}, Type: types.ReceiptTypeReadSelf})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; c.Unread != 0 {
		t.Fatalf("read elsewhere: %+v", c)
	}
}

func TestTypingAndPresence(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(&events.ChatPresence{MessageSource: types.MessageSource{Chat: group, Sender: peer, IsGroup: true}, State: types.ChatPresenceComposing})
	ty := one(t, h.take(), abi.EventTagTyping).Typing
	if string(ty.Chat) != group.String() || string(ty.Sender) != peer.String() || !ty.Composing {
		t.Fatalf("typing: %+v", ty)
	}
	h.inject(&events.ChatPresence{MessageSource: types.MessageSource{Chat: peer, Sender: peer}, State: types.ChatPresencePaused})
	if one(t, h.take(), abi.EventTagTyping).Typing.Composing {
		t.Fatal("paused")
	}
	seen := t0.Add(-time.Hour)
	h.inject(&events.Presence{From: peer, Unavailable: true, LastSeen: seen})
	p := one(t, h.take(), abi.EventTagPresence).Presence
	if p.Online || !p.HasLastSeen || p.LastSeen != seen.Unix() || string(p.ID) != peer.String() {
		t.Fatalf("presence: %+v", p)
	}
	h.inject(&events.Presence{From: peer})
	if p := one(t, h.take(), abi.EventTagPresence).Presence; !p.Online || p.HasLastSeen {
		t.Fatalf("online: %+v", p)
	}
	// Opening a direct chat follows its presence, once.
	for range 2 {
		h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: []byte(peer.String())}})
		h.take()
	}
	h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: []byte(group.String())}})
	if m := one(t, h.take(), abi.EventTagMessages).Messages; !m.Complete || len(m.Messages) != 0 {
		t.Fatalf("empty chat: %+v", m)
	}
	if len(h.f.follows) != 1 || h.f.follows[0] != peer {
		t.Fatalf("follows: %v", h.f.follows)
	}
	// A privacy id with a known number is filed under the number.
	lidPeer := types.NewJID("111222333444", types.HiddenUserServer)
	h.f.lids[lidPeer] = peer2
	h.inject(&events.ChatPresence{MessageSource: types.MessageSource{Chat: lidPeer, Sender: lidPeer}, State: types.ChatPresenceComposing})
	if ty := one(t, h.take(), abi.EventTagTyping).Typing; string(ty.Chat) != peer2.String() {
		t.Fatalf("canonical: %s", ty.Chat)
	}
}

func TestPhoneChatSettings(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	for i, id := range []string{"S1", "S2", "S3"} {
		h.inject(message(peer, peer, id, t0.Add(time.Duration(i)*time.Second), text(id)))
	}
	h.take()
	chat := func(evs []abi.Event) abi.Chat { return one(t, evs, abi.EventTagChatUpdated).ChatUpdated.Chat }
	h.inject(&events.Pin{JID: peer, Timestamp: t0, Action: &waSyncAction.PinAction{Pinned: proto.Bool(true)}})
	if c := chat(h.take()); !c.Pinned || c.PinnedAt != t0.Unix() {
		t.Fatalf("pin: %+v", c)
	}
	h.inject(&events.Mute{JID: peer, Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(-1)}})
	if c := chat(h.take()); !c.HasMutedUntil || c.MutedUntil != 0 {
		t.Fatalf("mute forever: %+v", c)
	}
	h.inject(&events.Mute{JID: peer, Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(t0.UnixMilli())}})
	if c := chat(h.take()); c.MutedUntil != t0.Unix() {
		t.Fatalf("mute until: %+v", c)
	}
	h.inject(&events.Archive{JID: peer, Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}})
	if c := chat(h.take()); !c.Archived {
		t.Fatalf("archive: %+v", c)
	}
	h.inject(&events.MarkChatAsRead{JID: peer, Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false)}})
	if c := chat(h.take()); !c.MarkedUnread || c.Unread != 3 {
		t.Fatalf("unread mark: %+v", c)
	}
	h.inject(&events.MarkChatAsRead{JID: peer, Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(true)}})
	if c := chat(h.take()); c.MarkedUnread || c.Unread != 0 {
		t.Fatalf("read: %+v", c)
	}
	h.inject(&events.DeleteForMe{ChatJID: peer, MessageID: "S1"})
	evs := h.take()
	expectTags(t, evs, "MessageDeleted ChatUpdated")
	if d := evs[0].MessageDeleted; string(d.ID) != "S1" {
		t.Fatalf("deleted: %+v", d)
	}
	h.inject(&events.ClearChat{JID: peer, Action: &waSyncAction.ClearChatAction{MessageRange: &waSyncAction.SyncActionMessageRange{LastMessageTimestamp: proto.Int64(t0.Unix() + 1)}}})
	evs = h.take()
	expectTags(t, evs, "ChatCleared ChatUpdated")
	if c := evs[0].ChatCleared; c.Through != t0.Unix()+1 {
		t.Fatalf("cleared: %+v", c)
	}
	h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: []byte(peer.String())}})
	if m := messagesOf(h.take()); len(m) != 1 || string(m[0].ID) != "S3" {
		t.Fatalf("after clearing: %+v", m)
	}
	h.inject(&events.DeleteChat{JID: peer, Action: &waSyncAction.DeleteChatAction{}})
	evs = h.take()
	expectTags(t, evs, "ChatRemoved")
	if _, err := h.e.arch.Chat(peer.String()); !noRows(err) {
		t.Fatalf("still archived: %v", err)
	}
	h.inject(&events.PushNameSetting{Action: &waSyncAction.PushNameSetting{Name: proto.String("Ada L.")}})
	expectTags(t, h.take(), "Me")
}

// stored is a message as the archive holds it, for setting up a test.
func storeMessage(t *testing.T, h *harness, m store.Message) {
	t.Helper()
	if err := h.e.arch.SaveMessages([]store.Message{m}); err != nil {
		t.Fatal(err)
	}
}

func TestAMessageWaitedForNotifiesWhenItArrives(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	info := message(group, peer, "W1", t0, nil).Info
	// Not decrypted yet: a placeholder in the chat, counted unread, silent.
	h.inject(&events.UndecryptableMessage{Info: info})
	evs := h.take()
	expectTags(t, evs, "Messages ChatUpdated")
	if m := evs[0].Messages.Messages[0]; m.Content.Tag != abi.ContentTagWaiting {
		t.Fatalf("placeholder: %+v", m.Content)
	}
	if c := evs[1].ChatUpdated.Chat; c.Unread != 1 {
		t.Fatalf("placeholder unread: %d", c.Unread)
	}
	// A repeat, as whatsmeow sends when the resend fails too, changes nothing.
	h.inject(&events.UndecryptableMessage{Info: info})
	expectTags(t, h.take(), "")
	// The resend replaces the row and notifies, without counting it twice.
	h.inject(message(group, peer, "W1", t0, text("the real one")))
	evs = h.take()
	expectTags(t, evs, "MessageUpdated ChatUpdated Incoming")
	if m := evs[2].Incoming.Message; m.Content.Tag != abi.ContentTagText || string(m.Content.Text.Text) != "the real one" {
		t.Fatalf("incoming: %+v", m.Content)
	}
	if c := evs[1].ChatUpdated.Chat; c.Unread != 1 {
		t.Fatalf("unread after the resend: %d", c.Unread)
	}
	// Hidden traffic and view-once messages never wait.
	hidden := message(group, peer, "W2", t0, nil).Info
	h.inject(&events.UndecryptableMessage{Info: hidden, DecryptFailMode: events.DecryptFailHide})
	expectTags(t, h.take(), "")
	once := message(group, peer, "W3", t0.Add(time.Second), nil).Info
	h.inject(&events.UndecryptableMessage{Info: once, IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce})
	evs = h.take()
	expectTags(t, evs, "Messages ChatUpdated Incoming")
	if m := evs[0].Messages.Messages[0]; m.Content.Tag != abi.ContentTagPhoneOnly || !m.Content.PhoneOnly.ViewOnce {
		t.Fatalf("view once: %+v", m.Content)
	}
}

func TestSignallingIsNoMessage(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	key := &waE2E.Message{SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{GroupID: proto.String(group.String())}, MessageContextInfo: &waE2E.MessageContextInfo{}}
	// A group's sender key comes under the id of the message it unlocks,
	// before or after it: the message stays.
	h.inject(message(group, peer, "S1", t0, text("hello all")))
	expectTags(t, h.take(), "Messages ChatUpdated Incoming")
	h.inject(message(group, peer, "S1", t0, key))
	expectTags(t, h.take(), "")
	if m, err := h.e.arch.Message(group.String(), "S1"); err != nil || m.Kind != kindText || m.Text != "hello all" {
		t.Fatalf("after the key: %+v %v", m, err)
	}
	// Alone, a key, a reaction sealed for the group or bare context info
	// is no row at all.
	h.inject(message(group, peer, "S2", t0, key))
	h.inject(message(group, peer, "S3", t0, &waE2E.Message{EncReactionMessage: &waE2E.EncReactionMessage{}}))
	h.inject(message(group, peer, "S4", t0, &waE2E.Message{MessageContextInfo: &waE2E.MessageContextInfo{}}))
	expectTags(t, h.take(), "")
	// What is shown only on the phone says so; content not shown here
	// says what it is.
	h.inject(message(group, peer, "S5", t0, &waE2E.Message{PlaceholderMessage: &waE2E.PlaceholderMessage{}}))
	if m := one(t, h.take(), abi.EventTagIncoming).Incoming.Message; m.Content.Tag != abi.ContentTagPhoneOnly || m.Content.PhoneOnly.ViewOnce || m.Content.PhoneOnly.LiveLocation {
		t.Fatalf("placeholder: %+v", m.Content)
	}
	h.inject(message(group, peer, "S6", t0, &waE2E.Message{GroupInviteMessage: &waE2E.GroupInviteMessage{}}))
	if m := one(t, h.take(), abi.EventTagIncoming).Incoming.Message; m.Content.Tag != abi.ContentTagUnsupported || string(m.Content.Unsupported.What) != "group invite" {
		t.Fatalf("invite: %+v", m.Content)
	}
}

func TestSignallingFiledAsUnsupportedIsMended(t *testing.T) {
	h := newHarness(t, options{})
	raw := func(p *waE2E.Message) []byte {
		b, err := proto.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	row := func(id string, at int64, p *waE2E.Message) store.Message {
		return store.Message{ID: id, ChatID: group.String(), Sender: peer.String(), Timestamp: at, Kind: kindUnsupported, Text: "message", Status: "delivered", Raw: raw(p)}
	}
	// What an earlier build filed for a group.
	if err := h.e.arch.UpsertChat(store.Chat{ID: group.String(), Kind: "group", LastActivity: 104}); err != nil {
		t.Fatal(err)
	}
	if err := h.e.arch.SaveMessages([]store.Message{
		row("U1", 100, &waE2E.Message{SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{}, MessageContextInfo: &waE2E.MessageContextInfo{}}),
		row("U2", 101, &waE2E.Message{KeepInChatMessage: &waE2E.KeepInChatMessage{}}),
		row("U3", 102, &waE2E.Message{EventMessage: &waE2E.EventMessage{}}),
		row("U4", 103, &waE2E.Message{RequestPhoneNumberMessage: &waE2E.RequestPhoneNumberMessage{}}),
		row("U5", 104, &waE2E.Message{EncCommentMessage: &waE2E.EncCommentMessage{}}),
	}); err != nil {
		t.Fatal(err)
	}
	h.start()
	kinds := map[string]string{}
	page, _, err := h.e.arch.Messages(group.String(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range page {
		kinds[m.ID] = m.Kind + ":" + m.Text
	}
	if fmt.Sprint(kinds) != "map[U1:waiting: U3:unsupported:event U4:unsupported:message]" {
		t.Fatalf("mended: %v", kinds)
	}
	if c, err := h.e.arch.Chat(group.String()); err != nil || c.Preview != "Unsupported message (message)" {
		t.Fatalf("preview: %q %v", c.Preview, err)
	}
	// The phone is asked for the message the key came with, once.
	h.settle()
	if len(h.f.peer) != 1 {
		t.Fatalf("asked the phone %d times", len(h.f.peer))
	}
	if r := h.f.peer[0].GetProtocolMessage().GetPeerDataOperationRequestMessage().GetPlaceholderMessageResendRequest(); len(r) != 1 || r[0].GetMessageKey().GetID() != "U1" || r[0].GetMessageKey().GetParticipant() != peer.String() {
		t.Fatalf("request: %+v", r)
	}
	// Its answer fills the row, quietly: the message is old news.
	resent := message(group, peer, "U1", time.Unix(100, 0), text("found it"))
	resent.UnavailableRequestID = "R1"
	h.inject(resent)
	expectTags(t, h.take(), "MessageUpdated ChatUpdated")
	// A second connection mends nothing again.
	h.inject(&events.Connected{})
	h.settle()
	if len(h.f.peer) != 1 {
		t.Fatalf("asked the phone again: %d", len(h.f.peer))
	}
}

// A Meta AI reply reads as one text message; one with no readable part is
// a placeholder (zf:src/backend/worker/bot_replies.rs tests).
func TestMetaAIRepliesReadAsText(t *testing.T) {
	h := newHarness(t, options{})
	raw, err := proto.Marshal(&waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{CallOutcome: waE2E.CallLogMessage_MISSED.Enum()}})
	if err != nil {
		t.Fatal(err)
	}
	// What an earlier build filed for a call-log message is mended.
	storeMessage(t, h, store.Message{ID: "OLDCALL", ChatID: peer.String(), Sender: peer.String(), Timestamp: t0.Unix() - 60, Kind: kindUnsupported, Text: "call", Status: "delivered", Raw: raw})
	h.start()
	if m, err := h.e.arch.Message(peer.String(), "OLDCALL"); err != nil || m.Kind != store.KindCall || m.Call == nil || m.Call.Status != "missed" {
		t.Fatalf("mended call: %+v %v", m, err)
	}
	sub := func(s string) *waAICommonDeprecated.AIRichResponseSubMessage {
		return &waAICommonDeprecated.AIRichResponseSubMessage{MessageText: proto.String(s)}
	}
	code := &waAICommonDeprecated.AIRichResponseSubMessage{CodeMetadata: &waAICommonDeprecated.AIRichResponseCodeMetadata{CodeLanguage: proto.String("rust"),
		CodeBlocks: []*waAICommonDeprecated.AIRichResponseCodeMetadata_AIRichResponseCodeBlock{{CodeContent: proto.String("fn main() ")}, {CodeContent: proto.String("{}")}, {CodeContent: proto.String("\n")}}}}
	table := &waAICommonDeprecated.AIRichResponseSubMessage{TableMetadata: &waAICommonDeprecated.AIRichResponseTableMetadata{Title: proto.String("Planets"),
		Rows: []*waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow{{Items: []string{"Name", "Moons"}, IsHeading: proto.Bool(true)}, {Items: []string{"Mars", "2"}}}}}
	mapPart := &waAICommonDeprecated.AIRichResponseSubMessage{MapMetadata: &waAICommonDeprecated.AIRichResponseMapMetadata{}}
	reply := func(parts ...*waAICommonDeprecated.AIRichResponseSubMessage) *waE2E.Message {
		// Bot replies carry their bot metadata in the context info.
		return &waE2E.Message{RichResponseMessage: &waE2E.AIRichResponseMessage{Submessages: parts}, MessageContextInfo: &waE2E.MessageContextInfo{}}
	}
	h.inject(message(peer, peer, "AI1", t0, reply(sub("Here you go:\n"), code, table, mapPart)))
	if m := one(t, h.take(), abi.EventTagIncoming).Incoming.Message; m.Content.Tag != abi.ContentTagText ||
		string(m.Content.Text.Text) != "Here you go:\n\n```\nfn main() {}\n```\n\nPlanets\nName | Moons\nMars | 2" {
		t.Fatalf("reply: %+v", m.Content)
	}
	for i, p := range []*waE2E.Message{reply(), reply(mapPart, sub("  "))} {
		h.inject(message(peer, peer, fmt.Sprintf("AI%d", i+2), t0.Add(time.Second), p))
		if m := one(t, h.take(), abi.EventTagIncoming).Incoming.Message; m.Content.Tag != abi.ContentTagUnsupported || string(m.Content.Unsupported.What) != "Meta AI reply" {
			t.Fatalf("unreadable reply: %+v", m.Content)
		}
	}
}
