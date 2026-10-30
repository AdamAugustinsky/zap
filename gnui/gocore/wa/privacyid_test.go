package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/gocore/store"
)

// A direct chat addressed by a privacy id whose mapping is not known yet
// is filed under the phone number the message carries: ours from the phone
// (RecipientAlt), theirs (SenderAlt). What was kept under the id before
// joins the chat, so the phone's one chat is not two here.
func TestAPrivacyIDChatIsFiledUnderItsNumber(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	lid := types.NewJID("88009990001", types.HiddenUserServer)
	pn := types.NewJID("5519999990001", types.DefaultUserServer)
	// what an earlier message, without the number, filed under the id
	if err := h.e.arch.SaveMessages([]store.Message{{ID: "OLD", ChatID: lid.String(), Sender: lid.String(), Timestamp: t0.Unix() - 60, Kind: kindText, Text: "earlier", Status: "delivered"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.e.arch.UpsertChat(store.Chat{ID: lid.String(), Kind: "direct"}); err != nil {
		t.Fatal(err)
	}

	ours := message(lid, me, "P1", t0, text("sent from the phone"))
	ours.Info.RecipientAlt = pn
	h.inject(ours)
	if m := one(t, h.take(), abi.EventTagMessages); string(m.Messages.Chat) != pn.String() || !m.Messages.Messages[0].FromMe {
		t.Fatalf("ours went to %q", m.Messages.Chat)
	}
	theirs := message(lid, lid, "T1", t0.Add(time.Second), text("an answer"))
	theirs.Info.SenderAlt = pn
	h.inject(theirs)
	m := one(t, h.take(), abi.EventTagMessages)
	if string(m.Messages.Chat) != pn.String() || string(m.Messages.Messages[0].Sender) != pn.String() {
		t.Fatalf("theirs went to %q from %q", m.Messages.Chat, m.Messages.Messages[0].Sender)
	}
	if _, err := h.e.arch.Chat(lid.String()); !noRows(err) {
		t.Errorf("the privacy id's chat is still there: %v", err)
	}
	if _, err := h.e.arch.Message(pn.String(), "OLD"); err != nil {
		t.Errorf("the earlier message did not join the number's chat: %v", err)
	}
}
