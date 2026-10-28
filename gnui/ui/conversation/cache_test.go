package conversation

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The caches of the transcript's blocks are keyed by the account and the
// chat as well as the message id: a group's message has the same id in
// every account in it, and a chat's id repeats across accounts. Two
// messages with one id, in two chats of an account and in the same chat of
// two accounts, each get their own card, name and size.
func TestSameMessageIdInTwoChatsAndAccountsHasItsOwnBlock(t *testing.T) {
	r := newRig(t)
	vcard := func(name string) []byte {
		return []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:" + name + "\r\nTEL;type=CELL;waid=5511999990001:+55 11 99999-0001\r\nEND:VCARD")
	}
	mk := func(chat, name string) abi.Message {
		m := msg(chat, "dup-card", base0, false, "")
		m.Content = abi.Content{Tag: abi.ContentTagContact, Contact: abi.ContentContact{DisplayName: []byte(name), Vcard: vcard(name)}}
		return m
	}
	linkOrPlain := func(chat string, preview bool) abi.Message {
		m := msg(chat, "dup-link", base0+60, false, "Background reading: https://example.com/a")
		if preview {
			m.Content.Text.HasPreview = true
			m.Content.Text.Preview = abi.LinkPreview{URL: []byte("https://example.com/a"), Title: []byte("Example"), HasTitle: true}
		}
		return m
	}
	shows := func(what string, name string, card bool) {
		t.Helper()
		if n := len(r.all(ui.RoleLabel, name)); n != 1 {
			t.Errorf("%s: %d times %q, want the contact's own name", what, n, name)
		}
		if got := len(r.all(ui.RoleLink, "Example")) == 1; got != card {
			t.Errorf("%s: link preview card shown = %v, want %v", what, got, card)
		}
	}
	r.open(ada, mk(ada, "Ada Augusta"), linkOrPlain(ada, true))
	shows("the first chat", "Ada Augusta", true)
	r.open(group, mk(group, "Grace Hopper"), linkOrPlain(group, false))
	shows("the second chat, same ids", "Grace Hopper", false)
	r.open(ada)
	r.Frames(3)
	shows("back in the first", "Ada Augusta", true)

	// the same chat in a second account, the same ids again
	f2 := fake.New(nil)
	a2 := r.s.AddAccount("2", nil, f2)
	f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 20}}}
		}))
	r.s.SwitchAccount(a2.Index)
	r.Frames(3)
	a2.OpenChat(a2.Model.Chats.Find([]byte(ada)))
	r.Frames(2)
	f2.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true, Complete: true,
			Messages: []abi.Message{mk(ada, "Someone Else"), linkOrPlain(ada, false)}}
	}))
	r.Frames(4)
	shows("the same chat of another account", "Someone Else", false)
}

func (r *rig) addGroupAccount(id string) (*state.Account, *fake.Core) {
	f := fake.New(nil)
	a := r.s.AddAccount(id, nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte(ada)}, {ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup},
			}}
		}))
	r.Frames(2)
	return a, f
}

func TestDownloadsSurviveAccountReindex(t *testing.T) {
	r := newRig(t)
	fa := r.f
	a := r.a
	b, fb := r.addGroupAccount("2")
	show := func(a *state.Account, f *fake.Core) {
		r.s.SwitchAccount(a.Index)
		r.a, r.f = a, f
		photo := msg(group, "same-group-photo", base0, false, "")
		photo.Content.Tag = abi.ContentTagImage
		photo.Content.Image.Media = abi.Media{Mime: []byte("image/png"), Size: 100}
		video := msg(group, "same-group-video", base0+1, false, "")
		video.Content.Tag = abi.ContentTagVideo
		video.Content.Video.Media = abi.Media{Mime: []byte("video/mp4"), Size: 100}
		r.open(group, photo, video)
	}
	a.Settings.AutoDownload = false
	show(a, fa)
	b.Settings.AutoDownload = true
	show(b, fb)
	if a.Model.Chats.Find([]byte(group)) != b.Open {
		t.Fatal("fixture group ChatRefs differ")
	}
	count := func(f *fake.Core, id string) int {
		n := 0
		for _, cmd := range f.Sent() {
			if cmd.Tag == abi.CommandTagDownload && string(cmd.Download.Message) == id {
				n++
			}
		}
		return n
	}
	for _, id := range []string{"same-group-photo", "same-group-video"} {
		if n := count(fb, id); n != 1 {
			t.Fatalf("B initially requested %s %d times", id, n)
		}
	}
	fa.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	r.Frames(4)
	c, fc := r.addGroupAccount("3")
	if b.Index != 0 || c.Index != 1 {
		t.Fatal("fixture did not reuse account slots")
	}
	for _, id := range []string{"same-group-photo", "same-group-video"} {
		if n := count(fb, id); n != 1 {
			t.Errorf("reindex lost B's request for %s: %d downloads", id, n)
		}
	}
	c.Settings.AutoDownload = true
	show(c, fc)
	if c.Open != b.Open {
		t.Fatal("C's group ChatRef differs")
	}
	for _, id := range []string{"same-group-photo", "same-group-video"} {
		if n := count(fc, id); n != 1 {
			t.Errorf("C inherited B's request for %s: %d downloads", id, n)
		}
	}
}

func TestPollPendingSurvivesAccountReindex(t *testing.T) {
	r := newRig(t)
	fa := r.f
	b, fb := r.addGroupAccount("2")
	show := func(a *state.Account, f *fake.Core) {
		r.s.SwitchAccount(a.Index)
		r.a, r.f = a, f
		m := msg(group, "same-group-poll", base0, false, "")
		m.Content = abi.Content{Tag: abi.ContentTagPoll, Poll: abi.ContentPoll{
			Question: []byte("Lunch?"), Options: [][]byte{[]byte("Pizza"), []byte("Pasta")},
			State: abi.PollState{Selectable: 1, CanVote: true, HistoryComplete: true},
		}}
		r.open(group, m)
	}
	show(b, fb)
	r.Play(widgettest.Click(ui.RoleRadio, "Pizza"), widgettest.Idle(2))
	r.must(ui.RoleLabel, "Sending vote…")
	fa.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	r.Frames(3)
	r.must(ui.RoleLabel, "Sending vote…")
	c, fc := r.addGroupAccount("3")
	show(c, fc)
	r.mustNot(ui.RoleLabel, "Sending vote…")
	r.s.SwitchAccount(b.Index)
	r.a, r.f = b, fb
	r.Frames(3)
	r.must(ui.RoleLabel, "Sending vote…")
	fb.Emit(ev(abi.EventTagPollVoted, func(e *abi.Event) {
		e.PollVoted.Chat, e.PollVoted.Message = []byte(group), []byte("same-group-poll")
	}))
	r.Frames(3)
	r.mustNot(ui.RoleLabel, "Sending vote…")
}

func TestConversationReadersSurviveAccountReindex(t *testing.T) {
	r := newRig(t)
	a, fa := r.a, r.f
	b, fb := r.addGroupAccount("2")
	const sender = "2@s.whatsapp.net"
	seed := func(a *state.Account, f *fake.Core, name, path string) {
		f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{{ID: []byte(sender), FullName: []byte(name), HasFullName: true}}}
		}), ev(abi.EventTagChatUpdated, func(e *abi.Event) {
			e.ChatUpdated.Chat = abi.Chat{ID: []byte(group), Kind: abi.ChatKindGroup, Participants: [][]byte{[]byte(sender)}}
		}))
		for _, jid := range []string{group, sender} {
			f.Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
				e.Avatar = abi.EventAvatar{ID: []byte(jid), Path: []byte(path), HasPath: true}
			}))
		}
		r.s.Pump(0)
		a.OpenChat(a.Model.Chats.Find([]byte(group)))
	}
	// Distinct synthetic paths suffice: even failed image decodes retain their
	// own handles. This tests cache identity, not image decoding.
	seed(a, fa, "Alice", t.TempDir()+"/a.png")
	seed(b, fb, "Bob", t.TempDir()+"/b.png")
	ca, _ := a.OpenChatView()
	cb, _ := b.OpenChatView()
	if ca.Ref() != cb.Ref() || ca.Gen() != cb.Gen() || a.Model.Avatars.Gen() != b.Model.Avatars.Gen() {
		t.Fatal("fixture local references/generations differ")
	}
	e := &screen.Env{A: r.Offscreen().App, S: r.s, K: screen.NewKit(r.Offscreen().App, nil)}
	images := screen.NewImages(e.A, e.K, nil)
	t.Cleanup(func() { images.Close(time.Second) })
	p := &Screen{h: &hub{s: r.s}}
	p.enter(e, a, ca)
	if string(p.members(a, ca)) != "Alice" {
		t.Fatal("A's member name missing")
	}
	avatar, picture := p.avatar(e, a, ca), p.senderImage(e, a, []byte(sender), 28)
	if avatar == 0 || picture == 0 {
		t.Fatal("fixture images have no handles")
	}
	p.h.search.buf.SetString("engine")
	p.h.search.request(a, ca)
	p.h.hist.first = false
	ownedB := &Screen{h: &hub{s: r.s}}
	ownedB.enter(e, b, cb)
	ownedB.flash = 99
	ownedB.h.search.buf.SetString("engine")
	ownedB.h.search.request(b, cb)
	fa.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	r.s.Pump(0)
	ownedB.enter(e, b, cb)
	if ownedB.flash != 99 || string(ownedB.h.search.buf.Bytes()) != "engine" {
		t.Error("reindex reset B's transcript/search state")
	}
	hits := abi.EventChatHits{Chat: []byte(group), Query: []byte("engine"), Last: true, Messages: []abi.Message{msg(group, "same-message", base0, false, "engine")}}
	ownedB.h.search.answer(b, &hits)
	if len(ownedB.h.search.hits) != 1 {
		t.Error("B's search answer was lost after reindex")
	}
	p.enter(e, b, cb)
	if len(p.h.search.buf.Bytes()) != 0 || !p.h.hist.first {
		t.Error("B inherited A's search/history state")
	}
	if got := string(p.members(b, cb)); got != "Bob" {
		t.Errorf("B's member name = %q", got)
	}
	if p.avatar(e, b, cb) == avatar {
		t.Error("B inherited A's avatar")
	}
	if p.senderImage(e, b, []byte(sender), 28) == picture {
		t.Error("B inherited A's sender picture")
	}
	page := &abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{Chat: []byte(group), Last: true}}
	p.h.hist.first = true
	p.h.Event(a, page)
	if !p.h.hist.first {
		t.Error("A's late page completed B's history request")
	}
	p.h.Event(b, page)
	if p.h.hist.first {
		t.Error("B's own page was ignored")
	}
}
