package gocore

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/out"
)

// The tests open a Conn on stub, an engine that plays a small part of the
// demo's: Me and the chat list at Open, Connected at Start, three
// messages for LoadChat, and DemoTyping echoed as Typing. It answers what
// its table holds and records every command it gets.

const ada = "393331234567@s.whatsapp.net"

var stubChats = []string{"Ada Lovelace", "Rust Berlin", "Grace Hopper", "Katherine Johnson", "Family", "Margaret Hamilton", "Dorothy Vaughan"}

type stub struct {
	table Table
	h     *Host

	mu      sync.Mutex
	got     []*Command
	order   []string // commands and "Start", as they came
	starts  int
	stopped atomic.Bool

	// Optional behaviour, set before Open.
	open    func(h *Host) error
	command func(s *stub, cmd *Command) bool // true: handled
	accept  func(cmd *abi.Command) error
	stop    func()
}

func newStub() *stub {
	return &stub{table: Answers(abi.CommandTagLoadChat, abi.CommandTagSendText, abi.CommandTagSendImage,
		abi.CommandTagSendVoice, abi.CommandTagDemoTyping, abi.CommandTagMarkRead)}
}

func (s *stub) Name() string { return "stub" }

func (s *stub) Accept(cmd *abi.Command) error {
	if s.accept != nil {
		return s.accept(cmd)
	}
	return s.table.Accept(cmd)
}

func (s *stub) Open(h *Host) error {
	s.h = h
	if s.open != nil {
		return s.open(h)
	}
	me := abi.Event{Tag: abi.EventTagMe}
	me.Me.ID = []byte("15550001111@s.whatsapp.net")
	h.Out().Emit(&me)
	out.List(h.Out(), stubChats, func(batch uint32, last bool, names []string) *abi.Event {
		e := &abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: batch, Last: last}}
		for i, n := range names {
			e.Chats.Chats = append(e.Chats.Chats, abi.Chat{ID: []byte(strconv.Itoa(i) + "@s.whatsapp.net"), Name: []byte(n)})
		}
		return e
	})
	return nil
}

func (s *stub) Start() {
	s.mu.Lock()
	s.starts++
	s.order = append(s.order, "Start")
	s.mu.Unlock()
	ev := abi.Event{Tag: abi.EventTagLink}
	ev.Link.Status.Tag = abi.LinkStatusTagConnected
	s.h.Out().Emit(&ev)
}

func (s *stub) Command(cmd *Command) {
	s.mu.Lock()
	s.got = append(s.got, cmd)
	s.order = append(s.order, cmd.Tag.String())
	s.mu.Unlock()
	if s.command != nil && s.command(s, cmd) {
		return
	}
	switch cmd.Tag {
	case abi.CommandTagDemoTyping:
		ev := abi.Event{Tag: abi.EventTagTyping}
		ev.Typing = abi.EventTyping{Chat: cmd.DemoTyping.Chat, Sender: cmd.DemoTyping.Sender, Composing: cmd.DemoTyping.Composing}
		s.h.Out().Emit(&ev)
	case abi.CommandTagLoadChat:
		texts := []string{"Hello", "Did the analytical engine build finish?", "Yes"}
		chat := cmd.LoadChat.Chat
		out.List(s.h.Out(), texts, func(batch uint32, last bool, ts []string) *abi.Event {
			e := &abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{Chat: chat, Batch: batch, Last: last, Complete: last}}
			for i, t := range ts {
				m := abi.Message{ID: []byte(strconv.Itoa(i)), Chat: chat, Sender: chat}
				m.Content.Tag = abi.ContentTagText
				m.Content.Text.Text = []byte(t)
				e.Messages.Messages = append(e.Messages.Messages, m)
			}
			return e
		})
	}
}

func (s *stub) Stop() {
	s.stopped.Store(true)
	if s.stop != nil {
		s.stop()
	}
}

func (s *stub) commands() []*Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Command(nil), s.got...)
}

// wakes counts the Conn's wakes and lets a test wait for the next one.
type wakes struct {
	n  atomic.Int64
	ch chan struct{}
}

func newWakes() *wakes { return &wakes{ch: make(chan struct{}, 1)} }

func (w *wakes) wake() {
	w.n.Add(1)
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

// session is a Conn on a stub and what its events said.
type session struct {
	t testing.TB
	c *Conn
	e *stub
	w *wakes
	h *recorder
}

func open(t testing.TB, e *stub) *session {
	t.Helper()
	w := newWakes()
	c, err := Open(core.Config{Demo: true, Wake: w.wake}, e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !c.closing.Load() {
			c.Close(5 * time.Second)
		}
	})
	return &session{t: t, c: c, e: e, w: w, h: &recorder{}}
}

// recorder keeps a copy of what the tests look at: the events' bytes are
// only valid during the call.
type recorder struct {
	tags     []abi.EventTag
	chats    []string
	batch    uint32
	chatsEnd bool
	messages []string
	typing   int
	picks    []abi.EventPickRequest // copied
	fatal    []string               // origin/thread/location
	match    func(e *abi.Event) bool
	matched  bool
}

func (r *recorder) Event(e *abi.Event) {
	r.tags = append(r.tags, e.Tag)
	switch e.Tag {
	case abi.EventTagChats:
		if r.chatsEnd || e.Chats.Batch != r.batch {
			r.chats = r.chats[:0]
		}
		r.batch = e.Chats.Batch
		for i := range e.Chats.Chats {
			r.chats = append(r.chats, string(e.Chats.Chats[i].Name))
		}
		r.chatsEnd = e.Chats.Last
	case abi.EventTagMessages:
		for i := range e.Messages.Messages {
			r.messages = append(r.messages, string(e.Messages.Messages[i].Content.Text.Text))
		}
	case abi.EventTagTyping:
		r.typing++
	case abi.EventTagPickRequest:
		p := e.PickRequest
		p.Title = append([]byte(nil), p.Title...)
		p.Filters = nil
		r.picks = append(r.picks, p)
	case abi.EventTagFatal:
		r.fatal = append(r.fatal, string(e.Fatal.Origin)+"/"+string(e.Fatal.Thread)+"/"+string(e.Fatal.Location))
	}
	if r.match != nil && r.match(e) {
		r.matched = true
	}
}

// until drains, waiting for wakes, until an event satisfies match.
func (s *session) until(what string, match func(e *abi.Event) bool) {
	s.t.Helper()
	s.h.match, s.h.matched = match, false
	deadline := time.After(10 * time.Second)
	for {
		if _, err := s.c.Drain(s.h, 0); err != nil {
			s.t.Fatalf("drain: %v", err)
		}
		if s.h.matched {
			return
		}
		select {
		case <-s.w.ch:
		case <-deadline:
			s.t.Fatalf("no %s within 10 s; saw %v", what, s.h.tags)
		}
	}
}

func (s *session) send(cmd *abi.Command) {
	s.t.Helper()
	if err := s.c.Send(cmd); err != nil {
		s.t.Fatalf("send %v: %v", cmd.Tag, err)
	}
}

func tag(t abi.EventTag) func(*abi.Event) bool {
	return func(e *abi.Event) bool { return e.Tag == t }
}

func typing(chat, sender string) *abi.Command {
	cmd := &abi.Command{Tag: abi.CommandTagDemoTyping}
	cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(chat), Sender: []byte(sender), Composing: true}
	return cmd
}
