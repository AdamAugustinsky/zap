package demo_test

import (
	"bytes"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/demo"
)

// The demo core through core.Conn, as the UI uses it: no account, no
// network, no disk, no keyring. These tests ran against zfcore's demo
// (gnui/core, over the Rust library) until WP9; they now run on the Go demo.

const ada = "393331234567@s.whatsapp.net" // a chat of the demo's

// wakes counts the core's wakes and lets a test wait for the next one.
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

// session is a demo core and what its events said.
type session struct {
	t testing.TB
	c *demo.Conn
	w *wakes
	h *recorder
}

func openDemo(t testing.TB) *session {
	t.Helper()
	w := newWakes()
	c, err := demo.Open(core.Config{Demo: true, Wake: w.wake})
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, c: c, w: w, h: &recorder{}}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	return s
}

// recorder keeps a copy of what the tests look at: the events' bytes are
// only valid during the call.
type recorder struct {
	tags     []abi.EventTag
	chats    []string // names, in order, from the latest Chats batch
	batch    uint32
	chatsEnd bool
	messages []string // texts of the latest Messages for a chat
	statuses []abi.Delivery
	incoming []string
	typing   int
	opened   []string
	errors   []string
	match    func(e *abi.Event) bool
	matched  bool
}

func (r *recorder) Event(e *abi.Event) {
	r.tags = append(r.tags, e.Tag)
	switch e.Tag {
	case abi.EventTagChats:
		if r.chatsEnd || e.Chats.Batch != r.batch {
			r.chats = r.chats[:0] // a new batch replaces the list
		}
		r.batch = e.Chats.Batch
		for i := range e.Chats.Chats {
			r.chats = append(r.chats, string(e.Chats.Chats[i].Name))
		}
		r.chatsEnd = e.Chats.Last
	case abi.EventTagMessages:
		for i := range e.Messages.Messages {
			m := &e.Messages.Messages[i]
			if m.Content.Tag == abi.ContentTagText {
				r.messages = append(r.messages, string(m.Content.Text.Text))
			}
		}
	case abi.EventTagMessageUpdated:
		r.statuses = append(r.statuses, e.MessageUpdated.Message.Status)
	case abi.EventTagIncoming:
		r.incoming = append(r.incoming, string(e.Incoming.Message.Content.Text.Text))
	case abi.EventTagTyping:
		r.typing++
	case abi.EventTagOpenPath:
		r.opened = append(r.opened, string(e.OpenPath.Path))
	case abi.EventTagError:
		r.errors = append(r.errors, string(e.Error.Text))
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

func TestDemoSession(t *testing.T) {
	s := openDemo(t)

	// At open: who we are and the chat list, before the permit.
	s.until("the whole chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	// Dorothy Vaughan's is locked: in Chats, shown in the locked folder.
	want := []string{"Ada Lovelace", "Rust Berlin", "Grace Hopper", "Katherine Johnson", "Family", "Margaret Hamilton", "Dorothy Vaughan"}
	if len(s.h.chats) != len(want) {
		t.Fatalf("chats %q, want %q", s.h.chats, want)
	}
	for i := range want {
		if s.h.chats[i] != want[i] {
			t.Fatalf("chats %q, want %q", s.h.chats, want)
		}
	}
	if s.h.tags[0] != abi.EventTagMe {
		t.Errorf("first event %v, want Me", s.h.tags[0])
	}

	// The permit: the demo "connects".
	if err := s.c.Start(); err != nil {
		t.Fatal(err)
	}
	s.until("Connected", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagLink && e.Link.Status.Tag == abi.LinkStatusTagConnected
	})
	if err := s.c.Start(); !core.IsCode(err, core.EState) {
		t.Errorf("a second Start: %v, want ZF_E_STATE", err)
	}

	// Opening a chat lists its messages.
	var cmd abi.Command
	cmd.Tag = abi.CommandTagLoadChat
	cmd.LoadChat = abi.CommandLoadChat{Chat: []byte(ada)}
	s.send(&cmd)
	s.until("Ada's messages", func(e *abi.Event) bool { return e.Tag == abi.EventTagMessages && e.Messages.Last })
	// Her two links come before her lines (the demo's shared media).
	if len(s.h.messages) != 5 || s.h.messages[2] != "Did the analytical engine build finish?" {
		t.Fatalf("messages %q", s.h.messages)
	}

	// Sending: archived as pending, then sent, delivered and read.
	cmd.Tag = abi.CommandTagSendText
	cmd.SendText = abi.CommandSendText{Chat: []byte(ada), Text: []byte("hello from Go")}
	s.send(&cmd)
	s.until("the message read", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagMessageUpdated && e.MessageUpdated.Message.Status == abi.DeliveryRead
	})
	if s.h.messages[len(s.h.messages)-1] != "hello from Go" {
		t.Errorf("the sent message: %q", s.h.messages)
	}
	if got := s.h.statuses; len(got) != 3 || got[0] != abi.DeliverySent || got[1] != abi.DeliveryDelivered {
		t.Errorf("statuses %v, want sent, delivered, read", got)
	}

	// An injected incoming message.
	cmd.Tag = abi.CommandTagDemoIncoming
	cmd.DemoIncoming = abi.CommandDemoIncoming{DelayMS: 20, Chat: []byte(ada), Sender: []byte(ada), Text: []byte("a reply")}
	s.send(&cmd)
	s.until("the incoming message", tag(abi.EventTagIncoming))
	if s.h.incoming[0] != "a reply" {
		t.Errorf("incoming %q", s.h.incoming)
	}

	// A command the demo does not answer is refused, and the reason comes
	// back in this call's zf_err.
	cmd.Tag = abi.CommandTagUnlink
	err := s.c.Send(&cmd)
	if !core.IsCode(err, core.EState) || !bytes.Contains([]byte(err.Error()), []byte("Unlink")) {
		t.Errorf("Unlink to the demo: %v", err)
	}

	if err := s.c.Close(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.c.Done():
	default:
		t.Error("the event goroutine is still running after Close")
	}
	if err := s.c.Send(&cmd); err != core.ErrClosed {
		t.Errorf("Send after Close: %v", err)
	}
	if _, err := s.c.Drain(s.h, 0); err != core.ErrClosed {
		t.Errorf("Drain after Close: %v", err)
	}
	if err := s.c.Close(time.Second); err != core.ErrClosed {
		t.Errorf("a second Close: %v", err)
	}
}

// The core asks the UI to open the log; Go opens it through a Shell.
func TestOpenPathGoesToTheShell(t *testing.T) {
	s := openDemo(t)
	var cmd abi.Command
	cmd.Tag = abi.CommandTagOpenLog
	cmd.OpenLog.Path = []byte("/tmp/zapfast-test.log")
	s.send(&cmd)
	sh := &fakeShell{done: make(chan string, 1)}
	s.h.match = nil
	deadline := time.After(10 * time.Second)
	for len(s.h.opened) == 0 {
		h := openPathHandler{rec: s.h, sh: sh}
		if _, err := s.c.Drain(&h, 0); err != nil {
			t.Fatal(err)
		}
		if len(s.h.opened) > 0 {
			break
		}
		select {
		case <-s.w.ch:
		case <-deadline:
			t.Fatal("no OpenPath")
		}
	}
	select {
	case p := <-sh.done:
		if p != "/tmp/zapfast-test.log" {
			t.Errorf("opened %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the shell was never asked")
	}
}

type openPathHandler struct {
	rec *recorder
	sh  core.Shell
}

func (h *openPathHandler) Event(e *abi.Event) {
	h.rec.Event(e)
	if e.Tag == abi.EventTagOpenPath {
		core.OpenPath(h.sh, &e.OpenPath, nil)
	}
}

type fakeShell struct{ done chan string }

func (f *fakeShell) Open(p string) error { f.done <- p; return nil }

func TestBlobsRoundTripInRanges(t *testing.T) {
	s := openDemo(t)
	src := make([]byte, 100_000)
	for i := range src {
		src[i] = byte(i * 7)
	}
	ref, err := s.c.PutBlob(src)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Len != uint64(len(src)) {
		t.Fatalf("ref %+v", ref)
	}
	var got []byte
	piece := make([]byte, 30_000)
	for off := uint64(0); ; {
		n, err := s.c.ReadBlob(ref.ID, off, piece)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		got = append(got, piece[:n]...)
		off += uint64(n)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("the blob read back differs")
	}
	if _, err := s.c.ReadBlob(ref.ID, uint64(len(src))+1, piece); !core.IsCode(err, core.ERange) {
		t.Errorf("past the end: %v", err)
	}
	s.c.FreeBlob(ref.ID)
	if _, err := s.c.ReadBlob(ref.ID, 0, piece); !core.IsCode(err, core.ENotFound) {
		t.Errorf("a freed blob: %v", err)
	}
}

// Wakes are not lost and do not spin: after a drain to empty, each new
// event wakes once more, and an idle core does not wake at all.
func TestWakesFollowEventsWithoutSpinning(t *testing.T) {
	s := openDemo(t)
	s.until("the chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	if _, err := s.c.Drain(s.h, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	idle := s.w.n.Load()
	time.Sleep(100 * time.Millisecond)
	if n := s.w.n.Load(); n != idle {
		t.Fatalf("%d wakes while idle", n-idle)
	}
	var cmd abi.Command
	cmd.Tag = abi.CommandTagDemoTyping
	cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(ada), Sender: []byte(ada), Composing: true}
	for range 20 {
		s.send(&cmd)
		s.until("typing", tag(abi.EventTagTyping))
		if _, err := s.c.Drain(s.h, 0); err != nil {
			t.Fatal(err)
		}
	}
	if s.h.typing != 20 {
		t.Errorf("%d typing events, want 20", s.h.typing)
	}
	time.Sleep(50 * time.Millisecond)
	idle = s.w.n.Load()
	time.Sleep(100 * time.Millisecond)
	if n := s.w.n.Load(); n != idle {
		t.Fatalf("%d wakes while idle after the burst", n-idle)
	}
}

// A drain with a budget stops early, wakes the loop again, and the next
// drain goes on where it stopped.
func TestABudgetedDrainResumes(t *testing.T) {
	s := openDemo(t)
	s.until("the chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	var cmd abi.Command
	cmd.Tag = abi.CommandTagDemoTyping
	cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(ada), Sender: []byte(ada), Composing: true}
	for range 50 {
		s.send(&cmd)
	}
	deadline := time.Now().Add(10 * time.Second)
	calls := 0
	for s.h.typing < 50 && time.Now().Before(deadline) {
		// The clock may not move between two events: a few per call.
		n, err := s.c.Drain(s.h, time.Nanosecond)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			calls++
		}
		if n == 0 {
			select {
			case <-s.w.ch:
			case <-time.After(time.Second):
			}
		}
	}
	if s.h.typing != 50 {
		t.Fatalf("%d of 50 typing events", s.h.typing)
	}
	if calls < 5 {
		t.Errorf("50 events in %d budgeted drains", calls)
	}
}

// Errors come back from the call that failed, from many goroutines at
// once.
func TestErrorsComeBackInTheirOwnCallAcrossGoroutines(t *testing.T) {
	s := openDemo(t)
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var cmd abi.Command
			for i := range 300 {
				if (g+i)%2 == 0 {
					cmd.Tag = abi.CommandTagUnlink
					if err := s.c.Send(&cmd); !core.IsCode(err, core.EState) || !bytes.HasSuffix([]byte(err.(*core.Error).Msg), []byte("Unlink")) {
						bad.Add(1)
					}
				} else {
					cmd.Tag = abi.CommandTagRemoveAccount
					if err := s.c.Send(&cmd); !core.IsCode(err, core.EState) || !bytes.HasSuffix([]byte(err.(*core.Error).Msg), []byte("RemoveAccount")) {
						bad.Add(1)
					}
				}
				if i%50 == 0 {
					runtime.Gosched()
				}
			}
		}()
	}
	// Meanwhile the UI side drains what typing the demo sends back.
	done := make(chan struct{})
	go func() {
		defer close(done)
		var cmd abi.Command
		cmd.Tag = abi.CommandTagDemoTyping
		cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(ada), Sender: []byte(ada)}
		for range 200 {
			s.c.Send(&cmd)
		}
	}()
	wg.Wait()
	<-done
	s.until("the last typing event", func(*abi.Event) bool { return s.h.typing >= 200 })
	if n := bad.Load(); n != 0 {
		t.Fatalf("%d calls read another call's error, or none", n)
	}
}
