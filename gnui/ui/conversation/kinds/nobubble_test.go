package kinds_test

import (
	"testing"

	"zapfast/abi"
	"zapfast/ui/conversation/kinds"
)

// Stickers and carousels go without a bubble; pictures, lone or captioned,
// cards, polls and texts keep theirs.
func TestNoBubble(t *testing.T) {
	text := msg("t", 10, false, abi.Content{Tag: abi.ContentTagText})
	text.Content.Text.Text = []byte("hi")
	captioned := msg("pc", 11, false, abi.Content{Tag: abi.ContentTagImage})
	captioned.Content.Image.Caption, captioned.Content.Image.HasCaption = []byte("Look"), true
	r := newRig(t,
		text,
		msg("s", 12, false, abi.Content{Tag: abi.ContentTagSticker}),
		msg("p", 13, true, abi.Content{Tag: abi.ContentTagImage}),
		captioned,
		interactive("c", false, carouselOf(2)),
		interactive("i", false, actions()),
		msg("poll", 14, false, abi.Content{Tag: abi.ContentTagPoll, Poll: abi.ContentPoll{Question: []byte("Lunch?")}}),
	)
	want := map[string]bool{"t": false, "s": true, "p": false, "pc": false, "c": true, "i": false, "poll": false}
	for id, bare := range want {
		if got := kinds.NoBubble(r.view(id)); got != bare {
			t.Errorf("NoBubble(%s) = %v, want %v", id, got, bare)
		}
	}
	// the answer is cached with Has's: asking again gives the same
	if !kinds.NoBubble(r.view("c")) || !kinds.Has(r.view("c")) {
		t.Error("a carousel lost its block or its bareness on a second ask")
	}
}
