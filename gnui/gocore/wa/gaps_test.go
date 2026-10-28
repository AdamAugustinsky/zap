package wa

import (
	"os"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

// WP4a's gaps: the logout clears the account, a quote names its sender,
// and a carousel shows its cards.

func TestLogoutClearsTheAccount(t *testing.T) {
	h := linked(t)
	h.receive(peer, peer, "P1", t0, picture("p", 100))
	h.f.downloaded = []byte("\xff\xd8\xff a picture")
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("P1")}})
	h.take()
	if _, err := os.Stat(h.e.mediaCache()); err != nil {
		t.Fatal(err)
	}

	h.inject(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})
	evs := h.take()
	if c := one(t, evs, abi.EventTagChats).Chats; len(c.Chats) != 0 || !c.Last {
		t.Fatalf("chats: %+v", c)
	}
	if chats, _ := h.e.arch.Chats(); len(chats) != 0 {
		t.Fatalf("the archive keeps %d chats", len(chats))
	}
	if _, err := os.Stat(h.e.mediaCache()); !os.IsNotExist(err) {
		t.Fatal("the media cache stayed")
	}
}

func TestQuotesNameTheirSender(t *testing.T) {
	h := linked(t) // M1 from peer, who goes by Grace
	reply := func(id, participant string) *waE2E.Message {
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("yes"), ContextInfo: &waE2E.ContextInfo{
			StanzaID: proto.String(id), Participant: proto.String(participant), QuotedMessage: text("hello"),
		}}}
	}
	h.inject(message(peer, peer, "R1", t0, reply("M1", peer.String())))
	q := messagesOf(h.take())[0].Quoted
	if string(q.SenderName) != "Grace" || string(q.Summary) != "hello" {
		t.Fatalf("from the archive: %+v", q)
	}

	// A quote of a message not here: the contact's name.
	h.f.more().contacts[peer2] = types.ContactInfo{Found: true, FullName: "Lin Dao"}
	h.inject(message(peer, peer, "R2", t0, reply("GONE", peer2.String())))
	if q := messagesOf(h.take())[0].Quoted; string(q.SenderName) != "Lin Dao" {
		t.Fatalf("from the contact: %+v", q)
	}
}

func TestCarouselCards(t *testing.T) {
	h := linked(t)
	card := func(title string) *waE2E.InteractiveMessage {
		return &waE2E.InteractiveMessage{
			Header: &waE2E.InteractiveMessage_Header{Title: proto.String(title), Media: &waE2E.InteractiveMessage_Header_ImageMessage{ImageMessage: &waE2E.ImageMessage{
				Mimetype: proto.String("image/jpeg"), DirectPath: proto.String("/v/c"), MediaKey: []byte("key"), FileSHA256: []byte("sha"), FileEncSHA256: []byte("enc"), FileLength: proto.Uint64(100),
			}}},
			Body: &waE2E.InteractiveMessage_Body{Text: proto.String("from 9 €")},
			InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
				{Name: proto.String("cta_url"), ButtonParamsJSON: proto.String(`{"display_text":"Open","url":"https://shop.example/x"}`)},
			}}},
		}
	}
	carousel := &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{Text: proto.String("Our picks")},
		InteractiveMessage: &waE2E.InteractiveMessage_CarouselMessage_{CarouselMessage: &waE2E.InteractiveMessage_CarouselMessage{
			Cards: []*waE2E.InteractiveMessage{card("Mug"), card("Cup")},
		}},
	}}
	h.inject(message(peer, peer, "K1", t0, carousel))
	c := messagesOf(h.take())[0].Content
	cards := c.Interactive.Card.Carousel
	if c.Tag != abi.ContentTagInteractive || len(cards) != 2 || string(cards[0].Body) != "Mug\nfrom 9 €" || !cards[0].HasImage || len(cards[0].Buttons) != 1 || cards[0].NeedsPhone {
		t.Fatalf("carousel: %+v", c.Interactive)
	}

	h.f.downloaded = []byte("\xff\xd8\xff a card")
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("K1"), Card: 1, HasCard: true}})
	evs := h.take()
	m := one(t, evs, abi.EventTagMedia).Media
	if m.Result.Tag != abi.PathResultTagOk || !m.HasCard || m.Card != 1 {
		t.Fatalf("card media: %+v", m)
	}
	stored, _ := h.e.arch.Message(peer.String(), "K1")
	if stored.Cards[1].Media.Path != string(m.Result.Ok.Path) || stored.Cards[0].Media.Path != "" {
		t.Fatalf("filed: %+v", stored.Cards)
	}
}
