package stickermaker

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

func pictureEvent(w, h uint32, transparent bool) *abi.Event {
	return ev(abi.EventTagStickerPicture, func(e *abi.Event) {
		e.StickerPicture = abi.EventStickerPicture{Path: []byte("/tmp/duck.png"), Width: w, Height: h, Transparent: transparent}
	})
}

// pick shows the maker on a picture the core chose.
func (r *rig) pick(w, h uint32, transparent bool) {
	r.t.Helper()
	r.f.Emit(pictureEvent(w, h, transparent))
	r.Frames(4)
	if !r.has(ui.RoleDialog, "Make a sticker") {
		r.t.Fatalf("no sticker maker\n%s", widgettest.Dump(r.Tree()))
	}
}

func (r *rig) made() []abi.CommandMakeSticker {
	var out []abi.CommandMakeSticker
	for _, c := range r.sent(abi.CommandTagMakeSticker) {
		out = append(out, c.MakeSticker)
	}
	return out
}

func (r *rig) type_(text string) {
	r.t.Helper()
	r.Play(widgettest.Click(ui.RoleTextField, "Emojis that describe it, like 😂 or ❤️"), widgettest.Type(text))
	r.Frames(2)
}

// SM-001: opening the maker chooses a picture first, and the core's answer
// opens it.
func TestOpeningPicksAPicture(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.open(state.Dialog{Kind: state.DialogStickerMaker})
	if len(r.sent(abi.CommandTagPickStickerPicture)) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.s.Dialog.Open() {
		t.Error("an empty maker stays open while the picker is up")
	}
	r.pick(800, 600, false)
}

// SM-004, SM-005, SM-006: the sticker is the centred square, with the emojis
// typed; Add to favorites sends it to no chat.
func TestAddToFavorites(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.pick(800, 600, false)
	r.type_("😂 hi ❤️ 😂")
	if r.has(ui.RoleButton, "Send") {
		t.Error("Send is offered without an open chat")
	}
	r.click(ui.RoleButton, "Add to favorites")
	got := r.made()
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	m := got[0]
	if string(m.Source) != "/tmp/duck.png" || m.Crop != (abi.StickerCrop{X: 100, Y: 0, Side: 600}) || m.HasChat || m.Transparent {
		t.Errorf("sent %+v", m)
	}
	if len(m.Emojis) != 2 || string(m.Emojis[0]) != "😂" || string(m.Emojis[1]) != "❤️" {
		t.Errorf("emojis %q", m.Emojis)
	}
	if r.s.Dialog.Open() {
		t.Error("the maker stays open")
	}
}

// SM-005: Send goes to the open chat.
func TestSendGoesToTheOpenChat(t *testing.T) {
	r := newRig(t, linkedEvent(), chatsEvent(chat("1@s.whatsapp.net", "Ada", 5)))
	r.a.OpenChat(r.a.Model.Chats.Find([]byte("1@s.whatsapp.net")))
	r.pick(500, 500, false)
	r.click(ui.RoleButton, "Send")
	got := r.made()
	if len(got) != 1 || !got[0].HasChat || string(got[0].Chat) != "1@s.whatsapp.net" {
		t.Fatalf("sent %v %+v", r.f.SentTags(), got)
	}
}

// SM-003: a transparent picture offers to keep its background; turning that
// off makes it white. A picture without see-through pixels does not offer it.
func TestTransparentBackground(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.pick(400, 400, false)
	if r.has(ui.RoleSwitch, "Keep the transparent background") {
		t.Error("an opaque picture offers transparency")
	}
	r.click(ui.RoleButton, "Cancel")

	r.pick(400, 400, true)
	r.click(ui.RoleButton, "Add to favorites")
	if got := r.made(); len(got) != 1 || !got[0].Transparent {
		t.Fatalf("kept: %+v", got)
	}

	r.pick(400, 400, true)
	r.click(ui.RoleSwitch, "Keep the transparent background")
	r.click(ui.RoleButton, "Add to favorites")
	if got := r.made(); len(got) != 2 || got[1].Transparent {
		t.Fatalf("white: %+v", got)
	}
}

// SM-002: Size resizes the square around its centre; dragging the picture
// moves it.
func TestSizeAndDrag(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.pick(800, 600, false)
	r.Play(widgettest.Drag(ui.RoleSlider, "Size", -10, 8))
	r.Frames(2)
	r.Play(widgettest.Drag(ui.RoleGroup, "Sticker", -5, 4))
	r.Frames(2)
	r.click(ui.RoleButton, "Add to favorites")
	got := r.made()
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	c := got[0].Crop
	if c.Side >= 600 || c.Side < 75 || c.X+c.Side > 800 || c.Y+c.Side > 600 {
		t.Errorf("crop %+v", c)
	}
	if c.X >= 100+(600-c.Side)/2 {
		t.Errorf("the drag did not move the square left: %+v", c)
	}
}

// Cancel and Escape leave without making anything, and the next opening
// chooses a picture again.
func TestCancelMakesNothing(t *testing.T) {
	r := newRig(t, linkedEvent())
	r.pick(800, 600, false)
	r.click(ui.RoleButton, "Cancel")
	if r.s.Dialog.Open() || len(r.made()) != 0 {
		t.Fatalf("open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
	r.open(state.Dialog{Kind: state.DialogStickerMaker})
	if len(r.sent(abi.CommandTagPickStickerPicture)) != 1 {
		t.Fatalf("a spent draft opened again: %v", r.f.SentTags())
	}
	r.pick(800, 600, false)
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.s.Dialog.Open() || len(r.made()) != 0 {
		t.Fatalf("Escape: open %v, sent %v", r.s.Dialog.Open(), r.f.SentTags())
	}
}

// A sticker sent while the composer quotes a message quotes it, with the
// quoted message's chat when that is not the open one.
func TestSendQuotesWhatTheComposerQuotes(t *testing.T) {
	r := newRig(t, linkedEvent(), chatsEvent(chat("1@s.whatsapp.net", "Ada", 5)))
	r.a.OpenChat(r.a.Model.Chats.Find([]byte("1@s.whatsapp.net")))
	r.s.Panes.Reply.Add([]byte("m9"))
	r.s.Panes.ReplyChat.Add([]byte("77@g.us"))
	r.pick(500, 500, false)
	r.click(ui.RoleButton, "Send")
	got := r.made()
	if len(got) != 1 || !got[0].HasQuoting || string(got[0].Quoting.ID) != "m9" || !got[0].Quoting.HasChat || string(got[0].Quoting.Chat) != "77@g.us" {
		t.Fatalf("sent %+v", got)
	}
}

// A steady frame of the maker builds without allocating.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t, linkedEvent(), chatsEvent(chat("1@s.whatsapp.net", "Ada", 5)))
	r.a.OpenChat(r.a.Model.Chats.Find([]byte("1@s.whatsapp.net")))
	r.pick(800, 600, true)
	r.type_("😂")
	r.Play(widgettest.Settle())
	r.Frames(5)
	if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}
