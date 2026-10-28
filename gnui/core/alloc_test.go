package core

import (
	"io"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"

	gcore "gonativeui/core"

	"zapfast/abi"
)

// profile runs the CPU profiler (SIGPROF on every thread, the core's
// included) until stop.
func profile(t testing.TB) (stop func()) {
	if err := pprof.StartCPUProfile(io.Discard); err != nil {
		t.Logf("no CPU profile: %v", err)
		return func() {}
	}
	return pprof.StopCPUProfile
}

// counter counts events without keeping anything.
type counter struct{ n, incoming int }

func (c *counter) Event(e *abi.Event) {
	c.n++
	if e.Tag == abi.EventTagIncoming {
		c.incoming++
	}
}

// Steady state allocates nothing, per event or per command: the UI sends
// typing and injected messages through the reused command buffer, the
// event goroutine wakes it through a Parker, and Drain decodes into the
// reused Event. Measured as whole sessions, back to back after a warm-up
// and a collection, passing on the first with exactly 0 mallocs (the
// framework's gate, D27: the runtime itself occasionally allocates).
func TestDrainingAndSendingAllocateNothingOnceWarm(t *testing.T) {
	l := loadLib(t)
	p := gcore.NewParker()
	defer p.Close()
	c, err := l.Open(Config{Demo: true, Wake: p.Unpark})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(5 * time.Second)
	h := &counter{}
	var timedOut atomic.Bool
	watchdog := time.AfterFunc(60*time.Second, func() { timedOut.Store(true); p.Unpark() })
	defer watchdog.Stop()

	var typing, incoming abi.Command
	chat := []byte(ada)
	typing.Tag = abi.CommandTagDemoTyping
	typing.DemoTyping = abi.CommandDemoTyping{Chat: chat, Sender: chat, Composing: true}
	incoming.Tag = abi.CommandTagDemoIncoming
	incoming.DemoIncoming = abi.CommandDemoIncoming{Chat: chat, Sender: chat, Text: []byte("Are you coming tonight? Bring the notes.")}

	// One session: 200 typing changes and 20 incoming messages (each a
	// Messages, a ChatUpdated and an Incoming), all drained.
	const perSession = 200 + 20*3
	session := func() bool {
		want := h.n + perSession
		for i := range 220 {
			cmd := &typing
			if i%11 == 0 {
				cmd = &incoming
			}
			if err := c.Send(cmd); err != nil {
				t.Fatal(err)
			}
			if i%8 == 0 {
				if _, err := c.Drain(h, 0); err != nil {
					t.Fatal(err)
				}
			}
		}
		for h.n < want {
			n, err := c.Drain(h, 0)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 && h.n < want {
				p.Park()
			}
			if timedOut.Load() {
				t.Logf("timed out: %d of %d events; signalled %v, rest %d, more %v, stats %+v", h.n, want, c.signalled.Load(), len(c.rest), c.more, c.Stats())
				return false
			}
		}
		return true
	}

	for h.n == 0 { // the opening events
		if n, _ := c.Drain(h, 0); n == 0 {
			p.Park()
		}
	}
	for range 3 {
		if !session() {
			t.Fatal("the warm-up sessions timed out")
		}
	}
	runtime.GC()
	session()

	var before, after runtime.MemStats
	best := ^uint64(0)
	for w := range 12 {
		events := h.n
		runtime.ReadMemStats(&before)
		ok := session()
		runtime.ReadMemStats(&after)
		if !ok {
			t.Fatal("a measured session timed out")
		}
		mallocs := after.Mallocs - before.Mallocs
		t.Logf("session %d: %d events, %d mallocs, %d bytes", w, h.n-events, mallocs, after.TotalAlloc-before.TotalAlloc)
		best = min(best, mallocs)
		if mallocs == 0 {
			return
		}
	}
	t.Fatalf("every session allocated: the fewest was %d mallocs for %d events and %d commands", best, perSession, 220)
}
