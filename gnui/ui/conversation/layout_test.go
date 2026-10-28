package conversation

import (
	"strconv"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// The framework's layout audit over a transcript with the cards that carry
// an icon (a place, a contact, a sticker pack, a poll, a reaction) and the
// Newest message button: every box, a glyph's included, sits inside its
// parent.
func TestCardsAndNewestPassTheLayoutAudit(t *testing.T) {
	r := newRig(t)
	loc := msg(ada, "loc", base0+180, false, "")
	loc.Content = abi.Content{Tag: abi.ContentTagLocation, Location: abi.ContentLocation{Latitude: 48.8584, Longitude: 2.2945,
		Name: []byte("Eiffel Tower"), HasName: true}}
	card := msg(ada, "card", base0+200, false, "")
	card.Content = abi.Content{Tag: abi.ContentTagContact, Contact: abi.ContentContact{DisplayName: []byte("Ada"),
		Vcard: []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ada Augusta Lovelace\r\nTEL;type=CELL;waid=5511999990001:+55 11 99999-0001\r\nEND:VCARD")}}
	pack := msg(ada, "pack", base0+220, true, "")
	pack.Content = abi.Content{Tag: abi.ContentTagStickerPack, StickerPack: abi.ContentStickerPack{Name: []byte("Cats"), Publisher: []byte("Ada"), Count: 3}}
	poll := msg(ada, "poll", base0+240, false, "")
	poll.Content = abi.Content{Tag: abi.ContentTagPoll, Poll: abi.ContentPoll{Question: []byte("Lunch?"),
		Options: [][]byte{[]byte("Pasta"), []byte("Pizza")}, State: abi.PollState{Selectable: 1, CanVote: true, HistoryComplete: true}}}
	poll.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("👍")}, {FromMe: true, Emoji: []byte("❤️")}}
	// what is not about the icons of the cards or the Newest button: the
	// shell's rail and the link's label of the transcript, which are on the
	// audit's skip list for now
	allow := []widgettest.Allow{
		{Check: ui.AuditCentre, Where: "Switch account", Reason: "the shell's rail"},
		{Check: ui.AuditOverflow, Where: "Label \"View stickers\"", Reason: "the link's label, not its icon"},
	}
	msgs := []abi.Message{loc, card, pack, poll}
	for i := 0; i < 60; i++ {
		msgs = append(msgs, msg(ada, "audit-line-"+strconv.Itoa(i), base0+300+int64(i)*60, false, "line "+strconv.Itoa(i)))
	}
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 30, Unread: 2}
	}))
	r.open(ada, msgs...)
	r.Frames(3)
	r.AuditLayout("the cards at the top, the end of the chat", allow...)
	r.Play(widgettest.Wheel(ui.RoleList, "Messages", 100000, 3), widgettest.Idle(3))
	if len(r.all(ui.RoleButton, "Newest message")) != 1 {
		t.Fatal("no Newest message button")
	}
	r.AuditLayout("the Newest message button over the cards", allow...)
}
