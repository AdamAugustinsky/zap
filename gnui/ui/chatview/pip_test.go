package chatview

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

const grace = "2@s.whatsapp.net"

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func sent(f *fake.Core, tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// The floating chat's window (WinPip): the chat and a composer that sends
// to it, a button that shrinks it to the floating icon, the icon's click
// that brings it back (a click on the icon), and close.
func TestTheFloatingChatWindow(t *testing.T) {
	f := fake.New(nil)
	s := state.New()
	a := s.AddAccount("1", nil, f)
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(grace), Name: []byte("Grace Hopper"), LastActivity: 30}}}
		}),
	)
	s.Pump(0)
	if !s.FloatChat(a, a.Model.Chats.Find([]byte(grace))) {
		t.Fatal("no floating chat")
	}
	scr := NewPip()
	r := widgettest.Open(t, scr.Config(s), pipW, pipH, 1, screen.NewWindow(s, state.WinPip, scr, nil, nil))
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	r.Must(ui.RoleDialog, "Floating: Grace Hopper")

	r.Play(widgettest.Click(ui.RoleTextField, "Reply to Grace Hopper"), widgettest.Type("brb"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if got := sent(f, abi.CommandTagSendText); len(got) != 1 || string(got[0].SendText.Text) != "brb" {
		t.Fatalf("the reply sent %v", f.SentTags())
	}

	r.Click(ui.RoleButton, "Shrink to the floating icon")
	r.Frames(3)
	if !s.Pip.Mini {
		t.Fatal("not shrunk")
	}
	// a click on the icon brings the chat back
	r.Click(ui.RoleButton, "Bring back Grace Hopper")
	r.Frames(3)
	if s.Pip.Mini {
		t.Fatal("the icon's click did not bring the chat back")
	}
	r.Click(ui.RoleButton, "Close")
	r.Frames(2)
	if s.Pip.Open() || s.Win.Want[state.WinPip] {
		t.Error("not closed")
	}
}

// The floating chat shows a photo as the transcript does (its block, here
// the download button of one not on this computer yet), not as a word.
func TestTheFloatingChatShowsMedia(t *testing.T) {
	f := fake.New(nil)
	s := state.New()
	a := s.AddAccount("1", nil, f)
	m := abi.Message{ID: []byte("p1"), Chat: []byte(grace), Sender: []byte(grace), Timestamp: 20}
	m.Content.Tag = abi.ContentTagImage
	md := &m.Content.Image.Media
	md.Mime, md.Size, md.Width, md.HasWidth, md.Height, md.HasHeight = []byte("image/png"), 5000, 400, true, 300, true
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(grace), Name: []byte("Grace Hopper"), LastActivity: 30}}}
		}),
	)
	s.Pump(0)
	s.FloatChat(a, a.Model.Chats.Find([]byte(grace)))
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(grace), Batch: 1, Last: true, Messages: []abi.Message{m}}
	}))
	s.Pump(0)
	scr := NewPip()
	r := widgettest.Open(t, scr.Config(s), pipW, pipH, 1, screen.NewWindow(s, state.WinPip, scr, nil, nil))
	r.Frames(4)
	r.Must(ui.RoleButton, "Download photo")
}
