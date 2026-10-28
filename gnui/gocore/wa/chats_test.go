package wa

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

func TestEnsureChatAndDrafts(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagEnsureChat, EnsureChat: abi.CommandEnsureChat{Chat: chatID(peer2), Name: []byte("Lin")}})
	c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat
	if string(c.Name) != "Lin" || c.Kind != abi.ChatKindDirect || c.HasLast {
		t.Fatalf("ensured: %+v", c)
	}
	// Known, or not a chat: nothing.
	h.send(abi.Command{Tag: abi.CommandTagEnsureChat, EnsureChat: abi.CommandEnsureChat{Chat: chatID(peer2), Name: []byte("Other")}})
	h.send(abi.Command{Tag: abi.CommandTagEnsureChat, EnsureChat: abi.CommandEnsureChat{Chat: []byte("nobody")}})
	expectTags(t, h.take(), "")

	h.send(abi.Command{Tag: abi.CommandTagSaveDraft, SaveDraft: abi.CommandSaveDraft{Chat: chatID(peer2), Text: []byte("half a thought")}})
	expectTags(t, h.take(), "")
	if err := h.c.Close(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	again := newHarness(t, options{keys: h.keys, state: h.state, cache: h.cache})
	evs := again.take()
	expectTags(t, evs, "Me Chats Drafts")
	d := evs[2].Drafts
	if !d.Last || len(d.Drafts) != 1 || string(d.Drafts[0].Chat) != peer2.String() || string(d.Drafts[0].Text) != "half a thought" {
		t.Fatalf("drafts: %+v", d)
	}
	if c := evs[1].Chats.Chats; len(c) != 1 || string(c[0].Name) != "Lin" {
		t.Fatalf("chats: %+v", c)
	}
}

func TestComposing(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagComposing, Composing: abi.CommandComposing{Chat: chatID(peer), Composing: true}})
	h.send(abi.Command{Tag: abi.CommandTagComposing, Composing: abi.CommandComposing{Chat: chatID(peer)}})
	expectTags(t, h.take(), "")
	if len(h.f.typing) != 2 || h.f.typing[0] != types.ChatPresenceComposing || h.f.typing[1] != types.ChatPresencePaused {
		t.Fatalf("typing: %v", h.f.typing)
	}
	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagComposing, Composing: abi.CommandComposing{Chat: chatID(peer), Composing: true}})
	h.take()
	if len(h.f.typing) != 2 {
		t.Fatal("sent while offline")
	}
}

func TestMarkReadUnreadAndPlayed(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(group, peer, "R1", t0, text("one")))
	h.inject(message(group, peer2, "R2", t0.Add(time.Second), text("two")))
	h.inject(message(group, peer, "R3", t0.Add(2*time.Second), &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(3)}}))
	h.take()

	h.send(abi.Command{Tag: abi.CommandTagMarkRead, MarkRead: abi.CommandMarkRead{Chat: chatID(group), Receipts: true}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; c.Unread != 0 {
		t.Fatalf("read: %+v", c)
	}
	p := h.f.patches[len(h.f.patches)-1]
	if p.Mutations[0].Index[0] != appstate.IndexMarkChatAsRead || !p.Mutations[0].Value.GetMarkChatAsReadAction().GetRead() {
		t.Fatalf("patch: %+v", p.Mutations[0])
	}
	got := map[types.JID]int{}
	for _, r := range h.f.reads {
		if r.chat != group || r.kind != types.ReceiptTypeRead {
			t.Fatalf("receipt: %+v", r)
		}
		got[r.sender] += len(r.ids)
	}
	if got[peer] != 2 || got[peer2] != 1 {
		t.Fatalf("receipts per sender: %v", got)
	}

	// Nothing unread: no patch, no receipts.
	patches, reads := len(h.f.patches), len(h.f.reads)
	h.send(abi.Command{Tag: abi.CommandTagMarkRead, MarkRead: abi.CommandMarkRead{Chat: chatID(group), Receipts: true}})
	h.take()
	if len(h.f.patches) != patches || len(h.f.reads) != reads {
		t.Fatal("read twice")
	}

	h.send(abi.Command{Tag: abi.CommandTagMarkUnread, MarkUnread: abi.CommandMarkUnread{Chat: chatID(group)}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; !c.MarkedUnread {
		t.Fatalf("unread: %+v", c)
	}
	if a := h.f.patches[len(h.f.patches)-1].Mutations[0].Value.GetMarkChatAsReadAction(); a.GetRead() {
		t.Fatal("marked read")
	}
	// Reading a chat marked unread takes the phone's mark off, without receipts.
	h.send(abi.Command{Tag: abi.CommandTagMarkRead, MarkRead: abi.CommandMarkRead{Chat: chatID(group)}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; c.MarkedUnread {
		t.Fatalf("still marked: %+v", c)
	}
	if len(h.f.patches) != patches+2 || len(h.f.reads) != reads {
		t.Fatalf("patches %d reads %d", len(h.f.patches), len(h.f.reads))
	}

	// Played: only the receipt, when receipts are on.
	h.send(abi.Command{Tag: abi.CommandTagMarkPlayed, MarkPlayed: abi.CommandMarkPlayed{Chat: chatID(group), Message: []byte("R3"), Sender: chatID(peer), Receipts: true}})
	expectTags(t, h.take(), "")
	if r := h.f.reads[len(h.f.reads)-1]; r.kind != types.ReceiptTypePlayed || r.chat != group || r.sender != peer || r.ids[0] != "R3" {
		t.Fatalf("played receipt: %+v", r)
	}
	reads = len(h.f.reads)
	h.send(abi.Command{Tag: abi.CommandTagMarkPlayed, MarkPlayed: abi.CommandMarkPlayed{Chat: chatID(group), Message: []byte("R3"), Sender: chatID(peer)}})
	h.take()
	if len(h.f.reads) != reads {
		t.Fatal("receipt with receipts off")
	}
}

func TestChatPreferences(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "P1", t0, text("hi")))
	h.inject(message(peer2, peer2, "P2", t0, text("hi")))
	h.take()
	chat := func() abi.Chat { return one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat }
	lastPatch := func() appstate.MutationInfo { return h.f.patches[len(h.f.patches)-1].Mutations[0] }

	h.send(abi.Command{Tag: abi.CommandTagSetArchived, SetArchived: abi.CommandSetArchived{Chat: chatID(peer), Archived: true}})
	if c := chat(); !c.Archived {
		t.Fatalf("archived: %+v", c)
	}
	if m := lastPatch(); m.Index[0] != appstate.IndexArchive || m.Index[1] != peer.String() || !m.Value.GetArchiveChatAction().GetArchived() || m.Value.GetArchiveChatAction().GetMessageRange().GetMessages()[0].GetKey().GetID() != "P1" {
		t.Fatalf("archive patch: %+v", m)
	}

	h.send(abi.Command{Tag: abi.CommandTagSetPinned, SetPinned: abi.CommandSetPinned{Chat: chatID(peer), Pinned: true}})
	if c := chat(); !c.Pinned || c.PinnedAt == 0 {
		t.Fatalf("pinned: %+v", c)
	}
	if m := lastPatch(); m.Index[0] != appstate.IndexPin || !m.Value.GetPinAction().GetPinned() {
		t.Fatalf("pin patch: %+v", m)
	}

	h.send(abi.Command{Tag: abi.CommandTagSetMuted, SetMuted: abi.CommandSetMuted{Chat: chatID(peer), HasUntil: true}})
	if c := chat(); !c.HasMutedUntil || c.MutedUntil != 0 {
		t.Fatalf("muted always: %+v", c)
	}
	if a := lastPatch().Value.GetMuteAction(); !a.GetMuted() || a.GetMuteEndTimestamp() != -1 {
		t.Fatalf("mute patch: %v", a)
	}
	until := t0.Add(8 * time.Hour).Unix()
	h.send(abi.Command{Tag: abi.CommandTagSetMuted, SetMuted: abi.CommandSetMuted{Chat: chatID(peer), Until: until, HasUntil: true}})
	if c := chat(); c.MutedUntil != until {
		t.Fatalf("muted until: %+v", c)
	}
	if a := lastPatch().Value.GetMuteAction(); a.GetMuteEndTimestamp() != until*1000 {
		t.Fatalf("mute until patch: %v", a)
	}
	h.send(abi.Command{Tag: abi.CommandTagSetMuted, SetMuted: abi.CommandSetMuted{Chat: chatID(peer)}})
	if c := chat(); c.HasMutedUntil {
		t.Fatalf("unmuted: %+v", c)
	}
	if lastPatch().Value.GetMuteAction().GetMuted() {
		t.Fatal("unmute patch")
	}

	h.send(abi.Command{Tag: abi.CommandTagSetFavorite, SetFavorite: abi.CommandSetFavorite{Chat: chatID(peer2), Favorite: true}})
	h.send(abi.Command{Tag: abi.CommandTagSetFavorite, SetFavorite: abi.CommandSetFavorite{Chat: chatID(peer), Favorite: true}})
	evs := h.take()
	if c := only(evs, abi.EventTagChatUpdated); len(c) != 2 || !c[1].ChatUpdated.Chat.Favorite || c[1].ChatUpdated.Chat.FavoritePosition != 1 {
		t.Fatalf("favourites: %s", tags(evs))
	}
	favs := lastPatch().Value.GetFavoritesAction().GetFavorites()
	if lastPatch().Index[0] != appstate.IndexFavorites || len(favs) != 2 || favs[0].GetID() != peer2.String() || favs[1].GetID() != peer.String() {
		t.Fatalf("favourites patch: %v", favs)
	}
	h.send(abi.Command{Tag: abi.CommandTagSetFavorite, SetFavorite: abi.CommandSetFavorite{Chat: chatID(peer2)}})
	if c := chat(); c.Favorite {
		t.Fatalf("not a favourite: %+v", c)
	}
	if favs := lastPatch().Value.GetFavoritesAction().GetFavorites(); len(favs) != 1 || favs[0].GetID() != peer.String() {
		t.Fatalf("after removing: %v", favs)
	}

	// Offline the setting stays here; the phone is not told.
	h.f.setConnected(false)
	patches := len(h.f.patches)
	h.send(abi.Command{Tag: abi.CommandTagSetArchived, SetArchived: abi.CommandSetArchived{Chat: chatID(peer)}})
	if c := chat(); c.Archived || len(h.f.patches) != patches {
		t.Fatalf("offline: %+v", c)
	}
}

func TestDeleteAndClearChat(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	for i, id := range []string{"D1", "D2"} {
		h.inject(message(peer, peer, id, t0.Add(time.Duration(i)*time.Second), text(id)))
	}
	h.inject(message(peer2, peer2, "E1", t0, text("x")))
	h.take()

	h.send(abi.Command{Tag: abi.CommandTagClearChat, ClearChat: abi.CommandClearChat{Chat: chatID(peer)}})
	evs := h.take()
	expectTags(t, evs, "ChatCleared ChatUpdated")
	if c := evs[0].ChatCleared; c.Through != t0.Unix()+1 {
		t.Fatalf("through: %+v", c)
	}
	if m := h.f.patches[len(h.f.patches)-1].Mutations[0]; m.Index[0] != appstate.IndexClearChat || m.Value.GetClearChatAction().GetMessageRange().GetLastMessageTimestamp() != t0.Unix()+1 {
		t.Fatalf("clear patch: %+v", m)
	}
	if page, _, _ := h.e.arch.Messages(peer.String(), "", 10); len(page) != 0 {
		t.Fatalf("left: %d", len(page))
	}

	h.send(abi.Command{Tag: abi.CommandTagDeleteChat, DeleteChat: abi.CommandDeleteChat{Chat: chatID(peer2)}})
	evs = h.take()
	expectTags(t, evs, "ChatRemoved")
	if string(evs[0].ChatRemoved.Chat) != peer2.String() || h.f.patches[len(h.f.patches)-1].Mutations[0].Index[0] != appstate.IndexDeleteChat {
		t.Fatalf("removed: %+v", evs[0])
	}
	if _, err := h.e.arch.Chat(peer2.String()); !noRows(err) {
		t.Fatal("still archived")
	}

	// The phone refuses: nothing changes here.
	h.inject(message(peer2, peer2, "E2", t0.Add(time.Minute), text("back")))
	h.take()
	h.f.appStateErr = errors.New("no")
	h.send(abi.Command{Tag: abi.CommandTagDeleteChat, DeleteChat: abi.CommandDeleteChat{Chat: chatID(peer2)}})
	expectTags(t, h.take(), "Error")
	// Offline: the phone comes first.
	h.f.appStateErr = nil
	h.f.setConnected(false)
	patches := len(h.f.patches)
	h.send(abi.Command{Tag: abi.CommandTagClearChat, ClearChat: abi.CommandClearChat{Chat: chatID(peer2)}})
	if got := lastError(t, h.take()); got != "Connect to WhatsApp to clear this chat" || len(h.f.patches) != patches {
		t.Fatalf("offline: %q", got)
	}
	if _, err := h.e.arch.Message(peer2.String(), "E2"); err != nil {
		t.Fatal("removed anyway")
	}
}

func TestSearch(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	var batch []store.Message
	for i := range 100 {
		chat := peer
		if i%2 == 1 {
			chat = peer2
		}
		batch = append(batch, store.Message{ChatID: chat.String(), ID: "S" + string(rune('A'+i/26)) + string(rune('a'+i%26)), Sender: chat.String(), Timestamp: t0.Unix() + int64(i), Kind: kindText, Text: "lunch plan " + chat.User})
	}
	batch = append(batch, store.Message{ChatID: peer.String(), ID: "OTHER", Sender: peer.String(), Timestamp: t0.Unix(), Kind: kindText, Text: "nothing here"})
	if err := h.e.arch.SaveMessages(batch); err != nil {
		t.Fatal(err)
	}

	h.send(abi.Command{Tag: abi.CommandTagSearchMessages, SearchMessages: abi.CommandSearchMessages{Query: []byte("lunch")}})
	evs := h.take()
	hits := one(t, evs, abi.EventTagSearchHits).SearchHits
	if string(hits.Query) != "lunch" || !hits.Last || len(hits.Messages) != searchLimit {
		t.Fatalf("hits: %d last=%v", len(hits.Messages), hits.Last)
	}

	h.send(abi.Command{Tag: abi.CommandTagSearchChatMessages, SearchChatMessages: abi.CommandSearchChatMessages{Chat: chatID(peer), Query: []byte("lunch")}})
	ch := one(t, h.take(), abi.EventTagChatHits).ChatHits
	if string(ch.Chat) != peer.String() || len(ch.Messages) != 50 || ch.Truncated || !ch.Last {
		t.Fatalf("chat hits: %d truncated=%v", len(ch.Messages), ch.Truncated)
	}
	for _, m := range ch.Messages {
		if string(m.Chat) != peer.String() {
			t.Fatalf("from another chat: %s", m.Chat)
		}
	}
	from, until := t0.Unix()+10, t0.Unix()+20
	h.send(abi.Command{Tag: abi.CommandTagSearchChatMessages, SearchChatMessages: abi.CommandSearchChatMessages{Chat: chatID(peer), Query: []byte("lunch"), From: from, HasFrom: true, Until: until, HasUntil: true}})
	ch = one(t, h.take(), abi.EventTagChatHits).ChatHits
	if !ch.HasFrom || ch.From != from || len(ch.Messages) == 0 {
		t.Fatalf("range: %+v", ch)
	}
	for _, m := range ch.Messages {
		if m.Timestamp < from || m.Timestamp > until {
			t.Fatalf("outside the range: %d", m.Timestamp)
		}
	}

	// More than the pane lists: truncated.
	batch = batch[:0]
	for i := range chatSearchLimit + 5 {
		batch = append(batch, store.Message{ChatID: group.String(), ID: "G" + string(rune('A'+i/26)) + string(rune('a'+i%26)), Sender: peer.String(), Timestamp: t0.Unix() + int64(i), Kind: kindText, Text: "dinner"})
	}
	if err := h.e.arch.SaveMessages(batch); err != nil {
		t.Fatal(err)
	}
	h.send(abi.Command{Tag: abi.CommandTagSearchChatMessages, SearchChatMessages: abi.CommandSearchChatMessages{Chat: chatID(group), Query: []byte("dinner")}})
	ch = one(t, h.take(), abi.EventTagChatHits).ChatHits
	if !ch.Truncated || len(ch.Messages) != chatSearchLimit {
		t.Fatalf("truncated: %d %v", len(ch.Messages), ch.Truncated)
	}
}
