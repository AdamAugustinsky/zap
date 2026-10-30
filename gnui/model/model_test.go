package model_test

import (
	"fmt"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
)

func chat(id, name string, last int64) abi.Chat {
	c := abi.Chat{ID: []byte(id), Name: []byte(name), LastActivity: last, HasLast: true}
	c.Last.Summary = []byte("last of " + name)
	return c
}

func text(chatID, id, body string, ts int64) abi.Message {
	var m abi.Message
	m.ID, m.Chat, m.Sender, m.Timestamp = []byte(id), []byte(chatID), []byte(chatID), ts
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(body)
	return m
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func names(s *model.Store) []string {
	var out []string
	for _, ref := range s.Chats.Order() {
		out = append(out, string(s.Chats.At(ref).Name()))
	}
	return out
}

func texts(v *model.Conversation) []string {
	var out []string
	for i := 0; i < v.Len(); i++ {
		out = append(out, string(v.At(i).Text()))
	}
	return out
}

// A fake core's script: the link's QR, the chat list in two chunks, the
// contacts, a chat's history with an older page, an incoming message, a
// status change, typing, a reorder, a reload without one chat, a removal.
func TestAppliesAScript(t *testing.T) {
	f := fake.New(nil)
	s := model.New()
	clock := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return clock }
	drain := func() {
		t.Helper()
		if _, err := f.Drain(s, 0); err != nil {
			t.Fatal(err)
		}
	}
	const ada, grace, linus = "1@s.whatsapp.net", "2@s.whatsapp.net", "3@g.us"

	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) {
		e.Link.Status.Tag = abi.LinkStatusTagUnlinked
		e.Link.Status.Unlinked.QR, e.Link.Status.Unlinked.HasQR = []byte("2@qr-payload"), true
	}))
	drain()
	if qr, ok := s.Link.QR(); !ok || string(qr) != "2@qr-payload" || s.Linked() {
		t.Fatalf("link: %q %v, linked %v", qr, ok, s.Linked())
	}
	linkGen := s.Link.Gen()

	pinned := chat(linus, "Kernel", 10)
	pinned.Pinned, pinned.PinnedAt = true, 5
	pinned.Kind = abi.ChatKindGroup
	pinned.Labels = [][]byte{[]byte("work"), []byte("fun")}
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Chats: []abi.Chat{chat(ada, "Ada", 100)}}
	}), ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat(grace, "Grace", 200), pinned}}
	}), ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	drain()
	if got := fmt.Sprint(names(s)); got != "[Kernel Grace Ada]" {
		t.Errorf("order %s", got)
	}
	if !s.Chats.Loaded() || !s.Linked() || s.Link.Gen() == linkGen || !s.Link.Connected() {
		t.Errorf("loaded %v, linked %v, link %v", s.Chats.Loaded(), s.Linked(), s.Link.Status())
	}
	k := s.Chats.At(s.Chats.Find([]byte(linus)))
	if !k.Pinned() || !k.Group() || k.Labels() != 2 || string(k.Label(1)) != "fun" || !k.HasLabel([]byte("work")) || string(k.Preview()) != "last of Kernel" {
		t.Errorf("kernel row: pinned %v group %v labels %d preview %q", k.Pinned(), k.Group(), k.Labels(), k.Preview())
	}
	adaRef := s.Chats.Find([]byte(ada))

	f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
		e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{
			{ID: []byte(ada), FullName: []byte("Ada Lovelace"), HasFullName: true},
			{ID: []byte("4@s.whatsapp.net"), PushName: []byte("Alan"), HasPushName: true},
		}}
	}))
	drain()
	if n := string(s.DisplayName([]byte(ada))); n != "Ada Lovelace" {
		t.Errorf("display name %q", n)
	}
	if n := string(s.DisplayName([]byte("5@s.whatsapp.net"))); n != "+5" {
		t.Errorf("unknown number's name %q", n)
	}
	if n := string(s.DisplayName([]byte("4@s.whatsapp.net"))); n != "~Alan" {
		t.Errorf("a profile name %q", n)
	}

	// History: the newest page, then an older one, applied in order.
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true,
			Messages: []abi.Message{text(ada, "c", "third", 30), text(ada, "d", "fourth", 40)}}
	}), ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 2, Last: true, Older: true, Complete: true,
			Messages: []abi.Message{text(ada, "a", "first", 10), text(ada, "b", "second", 20)}}
	}))
	drain()
	v := s.Conversation([]byte(ada))
	if v == nil || fmt.Sprint(texts(v)) != "[first second third fourth]" || !v.Complete() {
		t.Fatalf("conversation %v", v)
	}
	gen := v.Gen()

	in := text(ada, "e", "fifth", 50)
	in.HasQuoted = true
	in.Quoted.ID, in.Quoted.Summary, in.Quoted.Sender = []byte("b"), []byte("second"), []byte(ada)
	in.Reactions = []abi.Reaction{{Sender: []byte("x"), Emoji: []byte("👍")}, {Sender: []byte("y"), Emoji: []byte("👍"), FromMe: true}, {Sender: []byte("z"), Emoji: []byte("❤️")}}
	sent := text(ada, "c", "third", 30)
	sent.FromMe, sent.Status = true, abi.DeliveryRead
	f.Emit(ev(abi.EventTagIncoming, func(e *abi.Event) { e.Incoming = abi.EventIncoming{Chat: []byte(ada), Message: in} }),
		ev(abi.EventTagMessageUpdated, func(e *abi.Event) { e.MessageUpdated.Message = sent }),
		ev(abi.EventTagMessageDeleted, func(e *abi.Event) { e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(ada), ID: []byte("a")} }))
	drain()
	if got := fmt.Sprint(texts(v)); got != "[second third fourth fifth]" || v.Gen() == gen {
		t.Errorf("after incoming, update and delete: %s", got)
	}
	last := v.At(v.Find([]byte("e")))
	if id, _, sum, ok := last.Quote(); !ok || string(id) != "b" || string(sum) != "second" {
		t.Errorf("quote %q %q %v", id, sum, ok)
	}
	if last.Reactions() != 2 || !last.Reacted() {
		t.Errorf("reactions %d", last.Reactions())
	} else if e, n, mine := last.Reaction(0); string(e) != "👍" || n != 2 || !mine {
		t.Errorf("first reaction %q ×%d mine %v", e, n, mine)
	}
	if m := v.At(v.Find([]byte("c"))); !m.FromMe() || m.Status() != abi.DeliveryRead {
		t.Errorf("status %v", m.Status())
	}
	var full abi.Message
	if err := last.Decode(&full); err != nil || string(full.Content.Text.Text) != "fifth" || len(full.Reactions) != 3 {
		t.Errorf("decode: %v %+v", err, full.Content.Text)
	}

	// Typing lasts its timeout; a new message moves Ada to the top.
	f.Emit(ev(abi.EventTagTyping, func(e *abi.Event) {
		e.Typing = abi.EventTyping{Chat: []byte(ada), Sender: []byte(ada), Composing: true}
	}), ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = chat(ada, "Ada", 300) }))
	drain()
	if who, n := s.Typing(adaRef); n != 1 || string(who) != ada {
		t.Errorf("typing %q %d", who, n)
	}
	if got := fmt.Sprint(names(s)); got != "[Kernel Ada Grace]" {
		t.Errorf("order after a message %s", got)
	}
	clock = clock.Add(model.TypingTimeout)
	if _, n := s.Typing(adaRef); n != 0 {
		t.Error("typing outlived its timeout")
	}

	// A reload without Grace removes her row; Ada keeps her ref.
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{chat(ada, "Ada", 300), pinned}}
	}), ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(linus) }))
	drain()
	if got := fmt.Sprint(names(s)); got != "[Ada]" || s.Chats.Find([]byte(ada)) != adaRef || s.Chats.Len() != 1 {
		t.Errorf("after the reload and removal: %s", got)
	}
	if _, ok := s.Chats.Get(s.Chats.Find([]byte(grace))); ok {
		t.Error("Grace is still there")
	}
}

// Many updates to the same rows compact the arenas without losing a row.
func TestCompactsUnderChurn(t *testing.T) {
	f := fake.New(nil)
	s := model.New()
	for round := 0; round < 200; round++ {
		var e abi.Event
		e.Tag = abi.EventTagChats
		e.Chats = abi.EventChats{Batch: uint32(round + 1), Last: true}
		for i := 0; i < 50; i++ {
			c := chat(fmt.Sprintf("%d@s.whatsapp.net", i), fmt.Sprintf("chat %d round %d", i, round), int64(i))
			c.Labels = [][]byte{[]byte(fmt.Sprint("l", i))}
			e.Chats.Chats = append(e.Chats.Chats, c)
		}
		f.Emit(&e)
		var m abi.Event
		m.Tag = abi.EventTagMessageUpdated
		m.MessageUpdated.Message = text("0@s.whatsapp.net", "m", fmt.Sprint("edit ", round), 1)
		f.Emit(&m)
		if round == 0 {
			s.Open([]byte("0@s.whatsapp.net"))
		}
		if _, err := f.Drain(s, 0); err != nil {
			t.Fatal(err)
		}
	}
	if s.Chats.Len() != 50 {
		t.Fatalf("%d chats", s.Chats.Len())
	}
	for i, ref := range s.Chats.Order() {
		c := s.Chats.At(ref)
		want := fmt.Sprintf("chat %d round 199", 49-i)
		if string(c.Name()) != want || string(c.Label(0)) != fmt.Sprint("l", 49-i) {
			t.Fatalf("row %d: %q label %q", i, c.Name(), c.Label(0))
		}
	}
	v := s.Conversation([]byte("0@s.whatsapp.net"))
	if v.Len() != 1 || string(v.At(0).Text()) != "edit 199" {
		t.Errorf("message %q", v.At(0).Text())
	}
}

// The ABI fixtures (abi/testdata, from zfcore's Rust encoder) hold every
// event variant: the store takes them all.
func TestTakesEveryFixtureEvent(t *testing.T) {
	f := fake.New(nil)
	if err := f.ReplayFile("../abi/testdata/events.bin"); err != nil {
		t.Skip(err)
	}
	s := model.New()
	if _, err := f.Drain(s, 0); err != nil {
		t.Fatal(err)
	}
	if s.Events() == 0 {
		t.Error("no events applied")
	}
}

// Applying a warm store's events allocates nothing.
func TestApplyDoesNotAllocate(t *testing.T) {
	s := model.New()
	var chats, upd, msgs abi.Event
	chats.Tag = abi.EventTagChats
	chats.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat("1@s.whatsapp.net", "Ada", 1), chat("2@s.whatsapp.net", "Grace", 2)}}
	upd.Tag = abi.EventTagChatUpdated
	upd.ChatUpdated.Chat = chat("1@s.whatsapp.net", "Ada", 3)
	msgs.Tag = abi.EventTagMessageUpdated
	msgs.MessageUpdated.Message = text("1@s.whatsapp.net", "m", "hello", 1)
	s.Open([]byte("1@s.whatsapp.net"))
	for range 4000 { // warm: the arenas reach their high-water mark and compact
		s.Event(&chats)
		s.Event(&upd)
		s.Event(&msgs)
	}
	if n := testing.AllocsPerRun(200, func() {
		s.Event(&chats)
		s.Event(&upd)
		s.Event(&msgs)
		_ = s.Chats.Order()
	}); n != 0 {
		t.Errorf("%.1f allocations per round", n)
	}
}

// Names go address book first; a profile name wears a "~"; a chat name that
// is only a number is no name (PR-007, zf:src/app.rs:2547).
func TestPersonNameAddressBookFirst(t *testing.T) {
	const saved, pushed, bare, digits, group = "1@s.whatsapp.net", "2@s.whatsapp.net", "3@s.whatsapp.net", "4@s.whatsapp.net", "5@g.us"
	s := model.New()
	s.Event(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
			{ID: []byte(bare), Name: []byte("Sam from work")},
			{ID: []byte(digits), Name: []byte("+4 000")},
			{ID: []byte(group)},
		}}
	}))
	s.Event(ev(abi.EventTagContacts, func(e *abi.Event) {
		e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{
			{ID: []byte(saved), FullName: []byte("Ada Lovelace"), HasFullName: true, PushName: []byte("ada!"), HasPushName: true},
			{ID: []byte(pushed), PushName: []byte("Alan"), HasPushName: true},
			{ID: []byte(bare)}, // optional fields must not reuse Alan's decode
		}}
	}))
	for _, c := range []struct{ jid, hint, want string }{
		{saved, "Hinted", "Ada Lovelace"},
		{pushed, "Hinted", "~Alan"},
		{"9@s.whatsapp.net", "Hinted", "~Hinted"},
		{bare, "", "Sam from work"},
		{digits, "", "+4"},
		{"9@s.whatsapp.net", "", "+9"},
		{group, "", "Unknown"},
	} {
		if got := string(s.PersonName([]byte(c.jid), []byte(c.hint))); got != c.want {
			t.Errorf("PersonName(%s, %q) = %q, want %q", c.jid, c.hint, got, c.want)
		}
	}
	if n := testing.AllocsPerRun(20, func() {
		s.PersonName([]byte(saved), nil)
		s.PersonName([]byte(pushed), nil)
		s.PersonName([]byte(digits), nil)
		s.PersonName([]byte(group), nil)
	}); n != 0 {
		t.Errorf("%v allocations naming people already seen", n)
	}
}
