package peek

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

// The floating peek's window (WinPeek) shows the chat, answers in it, and
// never reads it; Open closes it and opens the chat in the main window,
// Escape closes it.
func TestTheFloatingPeek(t *testing.T) {
	f := fake.New(nil)
	s := state.New()
	a := s.AddAccount("1", nil, f)
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(grace), Name: []byte("Grace Hopper"), Unread: 3, LastActivity: 30}}}
		}),
	)
	s.Pump(0)
	ref := a.Model.Chats.Find([]byte(grace))
	if !s.FloatPeekChat(a, ref) || !s.Win.Want[state.WinPeek] {
		t.Fatal("no floating peek")
	}
	scr := NewWindow()
	r := widgettest.Open(t, scr.Config(s), floatW, floatH, 1, screen.NewWindow(s, state.WinPeek, scr, nil, nil))
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	r.Must(ui.RoleDialog, "Peek: Grace Hopper")

	r.Play(widgettest.Type("on my way"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if got := sent(f, abi.CommandTagSendText); len(got) != 1 || string(got[0].SendText.Text) != "on my way" {
		t.Fatalf("the reply sent %v", f.SentTags())
	}
	if len(sent(f, abi.CommandTagMarkRead)) != 0 {
		t.Fatal("the floating peek read the chat")
	}

	r.Click(ui.RoleButton, "Open")
	r.Frames(2)
	if s.FloatPeek.Open() || a.Open != ref || !s.Req.RaiseMain {
		t.Fatalf("Open: peek %v, open %v, raise %v", s.FloatPeek.Open(), a.Open, s.Req.RaiseMain)
	}

	a.OpenChat(0)
	s.FloatPeekChat(a, ref)
	r.Frames(3)
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if s.FloatPeek.Open() || s.Win.Want[state.WinPeek] {
		t.Error("Escape did not close it")
	}
}
