package conversation

import (
	"bytes"
	"testing"
	"time"

	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// shelves keeps the demo's Recent (the model does not keep the sticker
// lists): a list comes in batches, the last one marked.
type shelves struct {
	recent, next [][]byte
	batch        uint32
}

func (l *shelves) Event(a *state.Account, ev *abi.Event) {
	if ev.Tag != abi.EventTagStickers {
		return
	}
	e := &ev.Stickers
	if e.Batch != l.batch {
		l.batch, l.next = e.Batch, l.next[:0]
	}
	for i := range e.Entries {
		if en := &e.Entries[i]; en.Tag == abi.StickerEntryTagRecent {
			l.next = append(l.next, append([]byte(nil), en.Recent.Path...))
		}
	}
	if e.Last {
		l.recent = append(l.recent[:0], l.next...)
	}
}

func (l *shelves) has(p []byte) bool {
	for _, q := range l.recent {
		if bytes.Equal(q, p) {
			return true
		}
	}
	return false
}

// newDemo is a window of w×h (0: the shell's size) on the demo core
// (skipped with -short), with its chats loaded, its Recent kept by l, and wait, which
// runs frames until a condition holds, for up to 10 s.
func newDemo(t *testing.T, w, h int) (*widgettest.Rig, *state.State, *state.Account, *shelves, func(string, func() bool)) {
	t.Helper()
	if testing.Short() {
		t.Skip("the demo core takes seconds")
	}
	c, err := demo.Open(core.Config{Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	s := state.New()
	s.Shell, s.Demo = &opened{}, true
	a := s.AddAccount("1", nil, c)
	l := &shelves{}
	s.Listen(l, abi.EventTagStickers)
	sh, err := shell.New(s, &screen.Set{Conversation: New(), Search: NewSearch()})
	if err != nil {
		t.Fatal(err)
	}
	cfg := shell.Config(s)
	if w == 0 {
		w, h = int(cfg.Width), int(cfg.Height)
	}
	cfg.Width, cfg.Height = float32(w), float32(h)
	r := widgettest.Open(t, cfg, w, h, 1, sh)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	wait := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; {
			r.Frames(1)
			if ok() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s within 10 s:\n%s", what, widgettest.Dump(r.Tree()))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("chats", func() bool { return a.Model.Chats.Loaded() })
	return r, s, a, l, wait
}

// openDemoChat opens the demo chat whose name starts with name, waits for
// its messages and returns its id.
func openDemoChat(t *testing.T, a *state.Account, wait func(string, func() bool), name string) []byte {
	t.Helper()
	var ref model.ChatRef
	for _, cr := range a.Model.Chats.Order() {
		if ch, ok := a.Model.Chats.Get(cr); ok && bytes.HasPrefix(ch.Name(), []byte(name)) {
			ref = cr
		}
	}
	if ref == 0 {
		t.Fatalf("the demo has no %s chat", name)
	}
	a.OpenChat(ref)
	ch, _ := a.Model.Chats.Get(ref)
	chat := append([]byte(nil), ch.ID()...)
	wait("the chat's messages", func() bool { v := a.Model.Conversation(chat); return v != nil && v.Len() > 0 })
	return chat
}

// PK-009, the conversation's half, against the demo core.
// Clicking one of our stickers in its chat does what upstream's does: one
// on disk opens the sticker view (Action::PreviewSticker) and sends nothing,
// so one taken out of Recent stays out; one without its file asks the core
// for it (a manual Download, the media retry that brings an expired one
// back and so into Recent; the demo's stickers all have their files).
func TestDemoOurStickerInItsChat(t *testing.T) {
	r, s, a, l, wait := newDemo(t, 0, 0)
	chat := openDemoChat(t, a, wait, "Katherine")
	a.Send(a.Cmd(abi.CommandTagRecentStickers))
	wait("Recent", func() bool { return len(l.recent) > 2 })
	pick := l.recent[2]

	cmd := a.Cmd(abi.CommandTagSendSticker)
	cmd.SendSticker.Chat, cmd.SendSticker.Path = chat, pick
	a.Send(cmd)
	var n int
	wait("our sticker in the chat", func() bool {
		v := a.Model.Conversation(chat)
		if v == nil || v.Len() == 0 {
			return false
		}
		m := v.At(v.Len() - 1)
		n = v.Len()
		return m.FromMe() && m.Kind() == abi.ContentTagSticker
	})
	wait("it first in Recent", func() bool { return bytes.Equal(l.recent[0], pick) })
	rm := a.Cmd(abi.CommandTagRemoveRecentSticker)
	rm.RemoveRecentSticker.Path = pick
	a.Send(rm)
	wait("it out of Recent", func() bool { return !l.has(pick) })

	// the newest "Sticker" is ours
	tr := r.Tree()
	nth := -1
	for _, nd := range tr.Nodes {
		if nd.Role == ui.RoleButton && string(tr.Text(nd.Name)) == "Sticker" {
			nth++
		}
	}
	if nth < 0 {
		t.Fatalf("no sticker on screen:\n%s", widgettest.Dump(tr))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Sticker").Nth(nth), widgettest.Idle(4))
	if !s.Win.Want[state.WinViewer] || !s.Win.ViewerMsg.Has(a.Model.Conversation(chat).At(n-1).ID()) {
		t.Error("a click on our sticker did not open the sticker view")
	}
	a.Send(a.Cmd(abi.CommandTagRecentStickers))
	time.Sleep(200 * time.Millisecond)
	r.Frames(4)
	if v := a.Model.Conversation(chat); v.Len() != n {
		t.Errorf("the click sent something: %d messages, %d before", v.Len(), n)
	}
	if l.has(pick) {
		t.Error("a sticker on disk rejoined Recent on a click; upstream's only views it")
	}
}

// An old sticker of ours whose file is gone (its download failed: expired
// on the servers) asks the core for it again when clicked in its chat, a
// manual Download, which upstream's core answers by having the phone send
// it again; downloaded, it is Recent's again (zf:docs/_guide
// using-zapfast.md, Stickers). The demo has no such sticker: the fake core.
func TestOldStickerOfOursAsksForItAgain(t *testing.T) {
	r := newRig(t)
	st := msg(ada, "old", base0, true, "")
	st.Content = abi.Content{Tag: abi.ContentTagSticker, Sticker: abi.ContentSticker{Media: abi.Media{Mime: []byte("image/webp"),
		Width: 512, HasWidth: true, Height: 512, HasHeight: true,
		State: abi.MediaState{Tag: abi.MediaStateTagFailed, Failed: abi.MediaStateFailed{Error: []byte("expired")}}}}}
	r.open(ada, st)
	before := len(r.sent(abi.CommandTagDownload))
	r.Play(widgettest.Click(ui.RoleButton, "Retry download"), widgettest.Idle(2))
	got := r.sent(abi.CommandTagDownload)
	if len(got) != before+1 || !got[len(got)-1].Download.Manual || string(got[len(got)-1].Download.Message) != "old" {
		t.Errorf("the click asked for %+v", got)
	}
	if n := len(r.sent(abi.CommandTagSendSticker)); n != 0 {
		t.Errorf("the click sent the sticker again (%d)", n)
	}
}

// The demo's newer content as the transcript draws it, against the demo core: a poll, a shared contact, a location, a round video message,
// edited and forwarded lines and reactions (Margaret Hamilton), a sticker
// and a GIF (Katherine Johnson), a sticker pack (Rust Berlin), and Ada's
// documents and link previews over the days before.
func TestDemoTranscriptShowsItsContent(t *testing.T) {
	r, _, a, _, wait := newDemo(t, 1400, 3000)
	dump := func() string { return widgettest.Dump(r.Tree()) }
	need := func(role ui.Role, name string) {
		t.Helper()
		if _, ok := r.Find(role, name); !ok {
			t.Errorf("no %v %q in\n%s", role, name, dump())
		}
	}
	openDemoChat(t, a, wait, "Margaret")
	wait("the poll", func() bool { _, ok := r.Find(ui.RoleRadio, "Apollo 11"); return ok })
	need(ui.RoleRadio, "Apollo 11")
	need(ui.RoleButton, "Show votes")
	need(ui.RoleLabel, "Dorothy Vaughan")
	need(ui.RoleButton, "Add")
	need(ui.RoleLink, "Open in a map")
	need(ui.RoleButton, "Video message")
	need(ui.RoleLabel, "Forwarded")
	if n, ok := r.Find(ui.RoleButton, "❤️"); !ok || n.State&ui.SemChecked == 0 {
		t.Errorf("no reaction of ours in\n%s", dump())
	}
	openDemoChat(t, a, wait, "Katherine")
	wait("the sticker", func() bool { _, ok := r.Find(ui.RoleButton, "Sticker"); return ok })
	need(ui.RoleButton, "GIF")
	openDemoChat(t, a, wait, "Rust")
	wait("the sticker pack", func() bool { _, ok := r.Find(ui.RoleLink, "View stickers"); return ok })
	need(ui.RoleLabel, "Grace Hopper · 8 stickers")
	openDemoChat(t, a, wait, "Ada")
	r.Frames(4)
	r.Play(widgettest.Key(ui.KeyHome, 0), widgettest.Idle(8))
	need(ui.RoleLabel, "Notes on the Analytical Engine.pdf")
	need(ui.RoleLabel, "1 page · 825 B")
	need(ui.RoleLabel, "bernoulli.csv")
	need(ui.RoleLink, "Analytical engine - Wikipedia")
	need(ui.RoleLabel, "en.wikipedia.org")
	need(ui.RoleLink, "https://www.rust-lang.org/")
}

// A reply of ours carries its quote in the transcript, against the demo core: the bubble is card-wide and its quote names the quoted sender.
func TestDemoOurReplyShowsItsQuote(t *testing.T) {
	r, _, a, _, wait := newDemo(t, 0, 0)
	chat := openDemoChat(t, a, wait, "Margaret")
	v := a.Model.Conversation(chat)
	var quoted []byte
	for i := v.Len() - 1; i >= 0 && quoted == nil; i-- {
		if m := v.At(i); !m.FromMe() && m.Kind() == abi.ContentTagText {
			quoted = append([]byte(nil), m.ID()...)
		}
	}
	cmd := a.Cmd(abi.CommandTagSendText)
	cmd.SendText.Chat, cmd.SendText.Text = chat, []byte("ok")
	cmd.SendText.Quoting, cmd.SendText.HasQuoting = abi.ReplyTarget{ID: quoted}, true
	a.Send(cmd)
	wait("the reply", func() bool {
		_, ok := r.Find(ui.RoleButton, "Margaret Hamilton: Houston, Tranquility Base here. The Eagle has landed.")
		return ok
	})
	q, _ := r.Find(ui.RoleButton, "Margaret Hamilton: Houston, Tranquility Base here. The Eagle has landed.")
	if w := q.Bounds[2] - q.Bounds[0]; w < kindsCardPt-2*rexui.BubbleLook.PadX {
		t.Errorf("the quote is %v wide; the reply should settle card-wide", w)
	}
}

// A jump lands on its message whether it opens the chat before the history
// comes or comes just after it, against the demo core (Ada's chat has
// unread messages, whose divider the transcript would otherwise scroll to,
// and the transcript is still settling at its end).
func TestDemoJumpAsTheChatOpens(t *testing.T) {
	for _, early := range []bool{true, false} {
		r, s, a, _, wait := newDemo(t, 0, 0)
		var ada []byte
		var ref model.ChatRef
		for _, cr := range a.Model.Chats.Order() {
			if ch, ok := a.Model.Chats.Get(cr); ok && bytes.HasPrefix(ch.Name(), []byte("Ada")) {
				ada, ref = append([]byte(nil), ch.ID()...), cr
			}
		}
		if early {
			s.OpenMessage(ada, []byte("Ada-media-0"))
		} else {
			a.OpenChat(ref)
		}
		wait("the chat's messages", func() bool { v := a.Model.Conversation(ada); return v != nil && v.Len() > 0 })
		if !early {
			s.OpenMessage(ada, []byte("Ada-media-0"))
		}
		r.Frames(10)
		list, _ := r.Find(ui.RoleList, "Messages")
		var photo ui.SemNode
		tr := r.Tree()
		for _, n := range tr.Nodes {
			if n.Role == ui.RoleLabel && string(tr.Text(n.Name)) == "The Difference Engine at the museum" {
				photo = n
			}
		}
		if photo.Bounds[3] <= list.Bounds[1] || photo.Bounds[1] >= list.Bounds[3] {
			t.Errorf("early %v: the jump's message is not in view (%v in %v)", early, photo.Bounds, list.Bounds)
		}
	}
}
