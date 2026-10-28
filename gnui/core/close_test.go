package core

import (
	"testing"
	"time"

	"zapfast/abi"
)

// sendingHandler sends from inside Drain, as a handler answering a
// PickRequest does, once Close has started taking the call sites' locks.
type sendingHandler struct {
	c       *Core
	inside  chan struct{}
	closing chan struct{}
	sent    chan error
	done    bool
}

func (h *sendingHandler) Event(e *abi.Event) {
	if h.done {
		return
	}
	h.done = true
	close(h.inside)
	<-h.closing
	time.Sleep(50 * time.Millisecond) // let Close take every lock it can
	var cmd abi.Command
	cmd.Tag = abi.CommandTagDemoTyping
	cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(ada), Sender: []byte(ada)}
	h.sent <- h.c.Send(&cmd)
}

// Close must not take a site a handler needs while it waits for Drain: the
// handler's Send and Close's wait for drainS would wait on each other.
func TestCloseWhileAHandlerSendsDoesNotDeadlock(t *testing.T) {
	s := openDemo(t)
	closing := make(chan struct{})
	closeHook = func() { close(closing) }
	defer func() { closeHook = nil }()
	h := &sendingHandler{c: s.c, inside: make(chan struct{}), closing: closing, sent: make(chan error, 1)}

	closed := make(chan error, 1)
	drained := make(chan error, 1)
	go func() {
		for { // the first wake may come before the demo's first event
			_, err := s.c.Drain(h, 0)
			if err != nil || h.done {
				drained <- err
				return
			}
			<-s.w.ch
		}
	}()
	<-h.inside // Drain holds drainS and runs the handler
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
		t.Error("the event goroutine is still running")
	}
}
