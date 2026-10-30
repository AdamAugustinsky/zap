package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

// A push name noticed on a message from a privacy id names the phone
// number's contact, the id chats are filed under.
func TestAPushNameNamesTheNumber(t *testing.T) {
	h := linked(t)
	x := h.f.more()
	lid := types.NewJID("88009990004", types.HiddenUserServer)
	pn := types.NewJID("5519999990004", types.DefaultUserServer)
	x.contacts[pn] = types.ContactInfo{PushName: "Maria"}
	h.inject(&events.PushName{JID: lid, JIDAlt: pn, NewPushName: "Maria"})
	c := one(t, h.take(), abi.EventTagContacts).Contacts.Contacts
	if len(c) != 1 || string(c[0].ID) != pn.String() || string(c[0].PushName) != "Maria" {
		t.Fatalf("contact: %+v", c)
	}
}

// A number in no address book is named, once history is in, by the name
// its owner went by in their newest message here, as the phone's "~ name";
// a chat with a name of its own, or ours alone, is not.
func TestUnsavedNumbersGoByTheirNames(t *testing.T) {
	h := newHarness(t, options{})
	h.e.syncQuiet = 200 * time.Millisecond
	h.start()
	stranger := types.NewJID("5519999990005", types.DefaultUserServer)
	quiet := types.NewJID("5519999990006", types.DefaultUserServer)
	// what history files: no chat name, the sender's name on the message
	for _, c := range []store.Chat{{ID: stranger.String(), Kind: "direct"}, {ID: quiet.String(), Kind: "direct"}} {
		if err := h.e.arch.UpsertChat(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.e.arch.SaveMessages([]store.Message{
		{ID: "S0", ChatID: stranger.String(), Sender: stranger.String(), SenderName: "Old name", Timestamp: t0.Unix() - 10, Kind: kindText, Text: "oi", Status: "delivered"},
		{ID: "S1", ChatID: stranger.String(), Sender: stranger.String(), SenderName: "Maria", Timestamp: t0.Unix(), Kind: kindText, Text: "tudo bem?", Status: "delivered"},
		{ID: "Q1", ChatID: quiet.String(), Sender: me.String(), FromMe: true, Timestamp: t0.Unix(), Kind: kindText, Text: "only ours", Status: "delivered"},
	}); err != nil {
		t.Fatal(err)
	}
	// a history chunk: syncing until the phone goes quiet
	h.inject(&events.HistorySync{Data: &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_RECENT.Enum(), Progress: proto.Uint32(100)}})
	evs := append(h.take(), h.wait(800*time.Millisecond)...)
	var named map[string]string
	for _, ev := range evs {
		if ev.Tag != abi.EventTagContacts {
			continue
		}
		named = map[string]string{}
		for _, c := range ev.Contacts.Contacts {
			named[string(c.ID)] = string(c.PushName)
		}
	}
	if named[stranger.String()] != "Maria" {
		t.Fatalf("the unsaved number's name: %q (contacts %v)", named[stranger.String()], named)
	}
	if _, ok := named[quiet.String()]; ok {
		t.Errorf("a chat of ours alone took a name: %v", named)
	}
}
