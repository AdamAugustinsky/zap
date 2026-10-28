package kinds_test

import (
	"os"
	"path/filepath"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/conversation/kinds"
)

func button(label string, tag abi.InteractiveActionTag) abi.InteractiveButton {
	return abi.InteractiveButton{Label: []byte(label), Action: abi.InteractiveAction{Tag: tag}}
}

func interactive(id string, from bool, card abi.InteractiveCard) abi.Message {
	m := msg(id, 10, from, abi.Content{Tag: abi.ContentTagInteractive})
	m.Content.Interactive = abi.ContentInteractive{Text: []byte("Pick one"), Card: card, HasCard: true}
	return m
}

func actions() abi.InteractiveCard {
	link := button("Visit site", abi.InteractiveActionTagUnavailable)
	link.URL, link.HasURL = []byte("https://example.com/menu"), true
	list := button("Choose", abi.InteractiveActionTagSelect)
	list.Action.Select.Options = []abi.InteractiveOption{{Title: []byte("One")}}
	copyB := button("Copy code", abi.InteractiveActionTagCopy)
	copyB.Action.Copy.Code = []byte("ABC123")
	return abi.InteractiveCard{Body: []byte("Pick one"), Buttons: []abi.InteractiveButton{
		button("Yes", abi.InteractiveActionTagReply), link, list, copyB, button("Pay", abi.InteractiveActionTagUnavailable)}}
}

func TestInteractiveButtonsSendTheirReplies(t *testing.T) {
	urls := make(chan string, 1)
	defer kinds.SetOpeners(nil, func(u string) error { urls <- u; return nil }, nil)()
	r := newRig(t, interactive("i", false, actions()))
	if !kinds.Has(r.view("i")) || kinds.Caption(r.view("i")) != kinds.Above {
		t.Fatal("an interactive message with a card has a block under its text")
	}
	if got := r.names(ui.RoleButton); len(got) != 5 {
		t.Fatalf("buttons %v", got)
	}
	// a reply button sends at once, naming itself
	r.Play(widgettest.Click(ui.RoleButton, "Yes"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagReplyInteractive)
	if len(got) != 1 || string(got[0].ReplyInteractive.Message) != "i" || got[0].ReplyInteractive.Button != 0 || got[0].ReplyInteractive.HasChoice {
		t.Fatalf("replies %+v", got)
	}
	// a link opens the browser, not the core
	r.Play(widgettest.Click(ui.RoleButton, "Visit site"))
	if u := <-urls; u != "https://example.com/menu" {
		t.Errorf("opened %q", u)
	}
	// a list opens its dialog, sending nothing
	r.Play(widgettest.Click(ui.RoleButton, "Choose"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogInteractiveList || r.s.Dialog.N != 2 || string(r.s.Dialog.IDs.At(0)) != "i" {
		t.Errorf("dialog %+v", r.s.Dialog)
	}
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 1 {
		t.Errorf("%d replies, the list sent one", n)
	}
	// a copy button copies its code and sends nothing
	var copied []byte
	r.Context().ClipboardSet = func(b []byte) { copied = append(copied[:0], b...) }
	r.Play(widgettest.Click(ui.RoleButton, "Copy code"), widgettest.Idle(2))
	if string(copied) != "ABC123" {
		t.Errorf("copied %q", copied)
	}
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 1 {
		t.Error("copying sent a reply")
	}
	// an option the app cannot run is dead
	r.Play(widgettest.Click(ui.RoleButton, "Pay"), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 1 {
		t.Error("an unavailable option sent a reply")
	}
}

// Replies need a connection, never go to our own cards, wait for one in
// flight and can be tried again after it ends.
func TestInteractiveRepliesNeedAConnectionAndWaitForOneInFlight(t *testing.T) {
	r := newRig(t, interactive("mine", true, actions()), interactive("i", false, actions()))
	r.Play(widgettest.Click(ui.RoleButton, "Yes").Nth(0), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 0 {
		t.Error("our own card sent a reply")
	}
	r.f.Emit(ev(abi.EventTagInteractiveReplyState, func(e *abi.Event) {
		e.InteractiveReplyState.Chat, e.InteractiveReplyState.Message, e.InteractiveReplyState.Pending = []byte(chatID), []byte("i"), true
	}))
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Yes").Nth(1), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 0 {
		t.Error("a reply was sent while one is in flight")
	}
	r.f.Emit(ev(abi.EventTagInteractiveReplyState, func(e *abi.Event) {
		e.InteractiveReplyState.Chat, e.InteractiveReplyState.Message, e.InteractiveReplyState.Pending = []byte(chatID), []byte("i"), false
	}))
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Yes").Nth(1), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 1 {
		t.Errorf("%d replies after it ended", n)
	}
	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagDisconnected }))
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Yes").Nth(1), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagReplyInteractive)); n != 1 {
		t.Errorf("%d replies while disconnected", n)
	}
}

func TestInteractiveCardWithAnImageAndAPhoneNote(t *testing.T) {
	card := actions()
	card.NeedsPhone = true
	card.Image = abi.Media{Mime: []byte("image/jpeg"), Size: 4000, Width: 600, HasWidth: true, Height: 300, HasHeight: true}
	card.HasImage = true
	r := newRig(t, interactive("i", false, card))
	if !hasText(r, "More content in WhatsApp Web or on your phone") {
		t.Error("no phone note")
	}
	if _, ok := r.Find(ui.RoleButton, "Download photo"); !ok {
		t.Error("no card image button")
	}
	if s := r.p.sized["i"]; s[0] != 320 {
		t.Errorf("card width %v", s[0])
	}
	r.Play(widgettest.Click(ui.RoleButton, "Download photo"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagDownload)
	if len(got) != 1 || got[0].Download.HasCard {
		t.Errorf("downloads %+v", got)
	}
}

func carouselOf(n int) abi.InteractiveCard {
	c := abi.InteractiveCard{Body: []byte("Our picks")}
	for i := 0; i < n; i++ {
		c.Carousel = append(c.Carousel, abi.CarouselCard{
			Body:    []byte("Card " + string(rune('A'+i))),
			Buttons: []abi.InteractiveButton{button("Pick "+string(rune('A'+i)), abi.InteractiveActionTagReply)},
		})
	}
	return c
}

// cardX is where the first card's button is, which moves as the strip scrolls.
func (r *rig) cardX() float32 {
	n, ok := r.Find(ui.RoleButton, "Pick A")
	if !ok {
		r.t.Fatal("no first card")
	}
	return n.Bounds[0]
}

func TestCarouselScrollsWithArrowsAndShiftWheel(t *testing.T) {
	r := newRig(t, interactive("c", false, carouselOf(4)))
	if s := r.p.sized["c"]; s[0] != 330 || s[1] < 60 {
		t.Fatalf("carousel size %v", s)
	}
	if _, ok := r.Find(ui.RoleButton, "Next card"); !ok {
		t.Fatal("no Next card arrow on a strip that overflows")
	}
	if _, ok := r.Find(ui.RoleButton, "Previous card"); ok {
		t.Error("a Previous card arrow at the start")
	}
	x0 := r.cardX()
	r.Play(widgettest.Click(ui.RoleButton, "Next card"), widgettest.Idle(40))
	x1 := r.cardX()
	if x1 >= x0-100 {
		t.Fatalf("Next card moved the first card from %v to %v", x0, x1)
	}
	if _, ok := r.Find(ui.RoleButton, "Previous card"); !ok {
		t.Error("no Previous card arrow after scrolling")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Previous card"), widgettest.Idle(40))
	if x2 := r.cardX(); x2 <= x1+100 {
		t.Errorf("Previous card moved the first card from %v to %v", x1, x2)
	}
	// Shift and a vertical wheel scroll it sideways
	before := r.cardX()
	r.Play(widgettest.Move(ui.RoleScrollArea, "Carousel"), widgettest.Idle(2))
	for range 3 {
		r.Offscreen().Step(16e6, platform.Event{Kind: platform.EvScroll, DY: -40, Mods: platform.ModShift, Flags: platform.FlagPrecise})
	}
	r.Play(widgettest.Idle(40))
	if after := r.cardX(); after >= before-10 {
		t.Errorf("Shift+wheel left the first card at %v (was %v)", after, before)
	}
}

func TestCarouselCardsReplyForThemselves(t *testing.T) {
	r := newRig(t, interactive("c", false, carouselOf(2)))
	r.Play(widgettest.Click(ui.RoleButton, "Pick A"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagReplyInteractive)
	if len(got) != 1 || string(got[0].ReplyInteractive.Message) != "c-card-0" {
		t.Errorf("replies %+v", got)
	}
}

func TestShortCarouselKeepsItsCardsTogetherWithoutArrows(t *testing.T) {
	r := newRig(t, interactive("c", false, carouselOf(1)))
	if _, ok := r.Find(ui.RoleButton, "Next card"); ok {
		t.Error("an arrow on a single card")
	}
	if s := r.p.sized["c"]; s[0] > 330 {
		t.Errorf("width %v", s[0])
	}
}

// A carousel card's downloaded picture opens in the viewer at its card, not
// in the system.
func TestCarouselCardPictureOpensTheViewerAtItsCard(t *testing.T) {
	opened := 0
	defer kinds.SetOpeners(func(string) error { opened++; return nil }, nil, func([]string) error { opened++; return nil })()
	data, err := os.ReadFile("testdata/anim.webp")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "card.webp")
	os.WriteFile(p, data, 0o644)
	card := carouselOf(2)
	md := &card.Carousel[1].Image
	md.Mime, md.Size, md.Width, md.HasWidth, md.Height, md.HasHeight = []byte("image/webp"), 900, 96, true, 96, true
	md.Path, md.HasPath = []byte(p), true
	card.Carousel[1].HasImage = true
	r := newRig(t, interactive("c", false, card))
	r.Play(widgettest.Click(ui.RoleButton, "Next card"), widgettest.Idle(30))
	r.Play(widgettest.Click(ui.RoleButton, "Open card image"), widgettest.Idle(2))
	w := &r.s.Win
	if !w.Want[state.WinViewer] || !w.ViewerHasCard || w.ViewerCard != 1 || w.ViewerMsg.Len() != 1 || string(w.ViewerMsg.At(0)) != "c" {
		t.Errorf("viewer want %v card %v/%d msg %d", w.Want[state.WinViewer], w.ViewerHasCard, w.ViewerCard, w.ViewerMsg.Len())
	}
	if opened != 0 {
		t.Error("the card's picture went to the system")
	}
}
