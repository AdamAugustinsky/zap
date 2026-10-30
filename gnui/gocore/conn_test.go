package gocore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// These are gnui/core's core_test.go and close_test.go, against a stub
// engine instead of the Rust demo core, and the Conn's own.

var _ core.Conn = (*Conn)(nil)

func TestSession(t *testing.T) {
	s := open(t, newStub())

	// At open: who we are and the chat list, before the permit.
	s.until("the whole chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	if strings.Join(s.h.chats, ",") != strings.Join(stubChats, ",") {
		t.Fatalf("chats %q", s.h.chats)
	}
	if s.h.tags[0] != abi.EventTagMe {
		t.Errorf("first event %v, want Me", s.h.tags[0])
	}

	if err := s.c.Start(); err != nil {
		t.Fatal(err)
	}
	s.until("Connected", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagLink && e.Link.Status.Tag == abi.LinkStatusTagConnected
	})
	if err := s.c.Start(); !core.IsCode(err, core.EState) {
		t.Errorf("a second Start: %v, want ZF_E_STATE", err)
	}

	var cmd abi.Command
	cmd.Tag = abi.CommandTagLoadChat
	cmd.LoadChat = abi.CommandLoadChat{Chat: []byte(ada)}
	s.send(&cmd)
	s.until("Ada's messages", func(e *abi.Event) bool { return e.Tag == abi.EventTagMessages && e.Messages.Last })
	if len(s.h.messages) != 3 || s.h.messages[1] != "Did the analytical engine build finish?" {
		t.Fatalf("messages %q", s.h.messages)
	}
	// The engine got its own copy: the caller's buffer may change.
	cmd.LoadChat.Chat[0] = 'X'
	if got := s.e.commands(); len(got) != 1 || string(got[0].LoadChat.Chat) != ada {
		t.Errorf("the engine's command: %q", got[0].LoadChat.Chat)
	}

	// A command the engine does not answer is refused, naming it.
	cmd = abi.Command{Tag: abi.CommandTagUnlink}
	err := s.c.Send(&cmd)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.EState || ce.Op != "zf_send" || ce.Msg != "the stub core does not answer Unlink" {
		t.Errorf("Unlink: %v", err)
	}

	if err := s.c.Close(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.c.Done():
	default:
		t.Error("the engine goroutine is still running after Close")
	}
	if !s.e.stopped.Load() {
		t.Error("Stop was not called")
	}
	if err := s.c.Send(&cmd); err != core.ErrClosed {
		t.Errorf("Send after Close: %v", err)
	}
	if _, err := s.c.Drain(s.h, 0); err != core.ErrClosed {
		t.Errorf("Drain after Close: %v", err)
	}
	if err := s.c.Start(); err != core.ErrClosed {
		t.Errorf("Start after Close: %v", err)
	}
	if _, err := s.c.PutBlob([]byte{1}); err != core.ErrClosed {
		t.Errorf("PutBlob after Close: %v", err)
	}
	if _, err := s.c.ReadBlob(1, 0, nil); err != core.ErrClosed {
		t.Errorf("ReadBlob after Close: %v", err)
	}
	if err := s.c.Close(time.Second); err != core.ErrClosed {
		t.Errorf("a second Close: %v", err)
	}
}

// A real Conn checks its configuration as the Rust core does, before the
// engine opens.
func TestARealConnChecksItsConfiguration(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		cfg  core.Config
		want int32
	}{
		{core.Config{StateDir: dir, CacheDir: dir, KeyringService: "rocks.zapfast.ZapFast"}, core.EArg},
		{core.Config{StateDir: dir, CacheDir: dir, Account: "../1"}, core.EArg},
		{core.Config{StateDir: dir, CacheDir: dir, Account: "01"}, core.EArg},
	} {
		e := newStub()
		if _, err := Open(c.cfg, e); !core.IsCode(err, c.want) {
			t.Errorf("%+v: %v, want %s", c.cfg, err, core.CodeName(c.want))
		}
		if e.h != nil {
			t.Error("the engine was opened")
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("refused opens wrote %v", ents)
	}

	e := newStub()
	c, err := Open(core.Config{StateDir: dir, CacheDir: dir + "/cache", Account: "2"}, e)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(time.Second)
	cfg := e.h.Config()
	if cfg.Account != "2" || cfg.KeyringService != core.KeyringService {
		t.Errorf("config %+v", cfg)
	}
	if st, ca := e.h.AccountDirs(); st != dir+"/accounts/2" || ca != dir+"/cache/accounts/2" {
		t.Errorf("account dirs %q, %q", st, ca)
	}
}

func TestAnEngineThatFailsToOpenFailsOpen(t *testing.T) {
	e := newStub()
	started := make(chan struct{})
	ended := make(chan struct{})
	e.open = func(h *Host) error {
		h.Go("early", func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			close(ended)
		})
		<-started
		return errors.New("no archive")
	}
	if _, err := Open(core.Config{Demo: true}, e); err == nil || err.Error() != "no archive" {
		t.Fatalf("Open: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine's goroutine was not stopped")
	}
}

func TestBlobsRoundTripInRanges(t *testing.T) {
	s := open(t, newStub())
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
	_, err = s.c.ReadBlob(ref.ID, 0, piece)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ENotFound || ce.Op != "zf_blob_read" || ce.Msg != "no blob 1" {
		t.Errorf("a freed blob: %v", err)
	}
}

// The engine goroutine, and the engine's own, end on Close and leave no
// goroutine behind.
func TestTheEngineGoroutinesExitOnClose(t *testing.T) {
	settle := func() int {
		for range 50 {
			runtime.GC()
			time.Sleep(2 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()
	for _, drained := range []bool{false, true} {
		e := newStub()
		w := newWakes()
		c, err := Open(core.Config{Demo: true, Wake: w.wake}, e)
		if err != nil {
			t.Fatal(err)
		}
		e.h.Go("worker", func(ctx context.Context) { <-ctx.Done() })
		<-w.ch
		if drained {
			if _, err := c.Drain(&recorder{}, 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Close(5 * time.Second); err != nil {
			t.Fatal(err)
		}
		select {
		case <-c.Done():
		default:
			t.Fatalf("drained %v: Close returned before the goroutines ended", drained)
		}
		if e.h.Go("late", func(context.Context) { t.Error("a goroutine started after Close") }) {
			t.Error("Go after Close started a goroutine")
		}
	}
	if after := settle(); after > before {
		t.Errorf("%d goroutines before, %d after two opens and closes", before, after)
	}
}

// Wakes are not lost and do not spin: after a drain to empty, each new
// event wakes once more, and an idle Conn does not wake at all.
func TestWakesFollowEventsWithoutSpinning(t *testing.T) {
	s := open(t, newStub())
	s.until("the chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	if _, err := s.c.Drain(s.h, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	idle := s.w.n.Load()
	time.Sleep(50 * time.Millisecond)
	if n := s.w.n.Load(); n != idle {
		t.Fatalf("%d wakes while idle", n-idle)
	}
	for range 20 {
		before := s.w.n.Load()
		s.send(typing(ada, ada))
		s.until("typing", tag(abi.EventTagTyping))
		if n := s.w.n.Load() - before; n != 1 {
			t.Fatalf("%d wakes for one event", n)
		}
	}
	if s.h.typing != 20 {
		t.Errorf("%d typing events, want 20", s.h.typing)
	}
	// Undrained events wake once, however many there are.
	before := s.w.n.Load()
	for range 10 {
		s.send(typing(ada, ada))
	}
	for s.c.Pending() < 10 {
		time.Sleep(time.Millisecond)
	}
	if n := s.w.n.Load() - before; n != 1 {
		t.Errorf("%d wakes for ten undrained events", n)
	}
}

// A drain with a budget stops early, wakes the loop again, and the next
// drain goes on where it stopped.
func TestABudgetedDrainResumes(t *testing.T) {
	s := open(t, newStub())
	s.until("the chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	for range 50 {
		s.send(typing(ada, ada))
	}
	deadline := time.Now().Add(10 * time.Second)
	calls := 0
	for s.h.typing < 50 && time.Now().Before(deadline) {
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

// Each refusal names its own command, from many goroutines at once.
func TestErrorsComeBackInTheirOwnCallAcrossGoroutines(t *testing.T) {
	s := open(t, newStub())
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := range 8 {
		wg.Go(func() {
			var cmd abi.Command
			for i := range 300 {
				name := "Unlink"
				cmd.Tag = abi.CommandTagUnlink
				if (g+i)%2 == 1 {
					name, cmd.Tag = "RemoveAccount", abi.CommandTagRemoveAccount
				}
				err := s.c.Send(&cmd)
				if !core.IsCode(err, core.EState) || !strings.HasSuffix(err.(*core.Error).Msg, name) {
					bad.Add(1)
				}
			}
		})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			s.c.Send(typing(ada, ada))
		}
	}()
	wg.Wait()
	<-done
	s.until("the last typing event", func(*abi.Event) bool { return s.h.typing >= 200 })
	if n := bad.Load(); n != 0 {
		t.Fatalf("%d calls read another call's error, or none", n)
	}
}

// sendingHandler sends from inside Drain, as a handler answering a
// PickRequest does, once Close has begun.
type sendingHandler struct {
	c       *Conn
	inside  chan struct{}
	closing chan struct{}
	sent    chan error
	done    bool
}

func (h *sendingHandler) Event(*abi.Event) {
	if h.done {
		return
	}
	h.done = true
	close(h.inside)
	<-h.closing
	time.Sleep(50 * time.Millisecond) // let Close take what it can
	h.sent <- h.c.Send(typing(ada, ada))
}

// Close must not take anything a handler needs while it waits for Drain.
func TestCloseWhileAHandlerSendsDoesNotDeadlock(t *testing.T) {
	s := open(t, newStub())
	closing := make(chan struct{})
	closeHook = func() { close(closing) }
	defer func() { closeHook = nil }()
	h := &sendingHandler{c: s.c, inside: make(chan struct{}), closing: closing, sent: make(chan error, 1)}

	closed := make(chan error, 1)
	drained := make(chan error, 1)
	go func() {
		for {
			_, err := s.c.Drain(h, 0)
			if err != nil || h.done {
				drained <- err
				return
			}
			<-s.w.ch
		}
	}()
	<-h.inside
	go func() { closed <- s.c.Close(5 * time.Second) }()

	timeout := time.After(10 * time.Second)
	select {
	case err := <-h.sent:
		if err != nil {
			t.Errorf("the handler's Send: %v", err)
		}
	case <-timeout:
		t.Fatal("deadlock: the handler's Send waits for Close, which waits for Drain")
	}
	for _, ch := range []chan error{drained, closed} {
		select {
		case <-ch:
		case <-timeout:
			t.Fatal("deadlock: Drain or Close never returned")
		}
	}
	select {
	case <-s.c.Done():
	default:
		t.Error("the engine goroutine is still running")
	}
}

func TestCloseTimesOutAndFinishesInTheBackground(t *testing.T) {
	e := newStub()
	release := make(chan struct{})
	e.stop = func() { <-release }
	s := open(t, e)
	err := s.c.Close(20 * time.Millisecond)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ETimeout || ce.Op != "zf_core_close" {
		t.Fatalf("Close: %v, want ZF_E_TIMEOUT", err)
	}
	if err := s.c.Send(typing(ada, ada)); err != core.ErrClosed {
		t.Errorf("Send while closing: %v", err)
	}
	close(release)
	select {
	case <-s.c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("closing never finished")
	}
}

// Draining allocates nothing per event once warm, as the Rust core's
// Drain does (D35).
func TestDrainingAllocatesNothing(t *testing.T) {
	s := open(t, newStub())
	s.until("the chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	ev := abi.Event{Tag: abi.EventTagTyping}
	ev.Typing = abi.EventTyping{Chat: []byte(ada), Sender: []byte(ada)}
	rec, err := ev.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	q := (*queue)(s.c)
	h := &countHandler{}
	allocs := testing.AllocsPerRun(100, func() {
		for range 10 {
			q.Push(rec)
		}
		s.c.Drain(h, 0)
	})
	if allocs != 0 {
		t.Errorf("%.1f allocations per drain of ten events", allocs)
	}
	if h.n != 1010 {
		t.Errorf("%d events drained", h.n)
	}
}

type countHandler struct{ n int }

func (h *countHandler) Event(*abi.Event) { h.n++ }
