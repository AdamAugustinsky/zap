package demo

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"zapfast/abi"
	"zapfast/gocore"
)

// Engine is the demo core as a gocore.Engine. Its state lives on one
// goroutine of its own ("zf-demo"), as zfcore's lives on its demo thread:
// Command hands each command over in order, and the goroutine plays them
// and its timers (the sends' receipts, the injected messages, the calls'
// steps and frames).
type Engine struct {
	h      *gocore.Host
	flags  cancelFlags
	frames frames

	mu    sync.Mutex
	inbox []input
	kick  chan struct{}
}

var _ gocore.Engine = (*Engine)(nil)

// New returns a demo engine for gocore.Open (with core.Config.Demo set).
// Open does both and adds the frames and voice notes.
func New() *Engine {
	return &Engine{kick: make(chan struct{}, 1), frames: newFrames()}
}

// Name is "demo": "the demo core does not answer X".
func (e *Engine) Name() string { return "demo" }

// tooManyCalls is zfcore's refusal when every cancel flag is held.
const tooManyCalls = "too many calls are still being placed"

// Accept is the disposition table, before any blob is taken. As zf_send
// does, it panics for DemoPanic inside the call, and takes a call's cancel
// flag (CancelCall sets it here, at once).
func (e *Engine) Accept(cmd *abi.Command) error {
	if DispositionOf(cmd.Tag) == Refuses {
		return gocore.ErrUnsupported
	}
	switch cmd.Tag {
	case abi.CommandTagDemoPanic:
		if !cmd.DemoPanic.OnThread {
			panic("DemoPanic inside zf_send")
		}
	case abi.CommandTagStartCall:
		if !e.flags.hold(cmd.StartCall.Cancel) {
			return gocore.Refuse(tooManyCalls)
		}
	case abi.CommandTagJoinCallLink:
		if !e.flags.hold(cmd.JoinCallLink.Cancel) {
			return gocore.Refuse(tooManyCalls)
		}
	case abi.CommandTagCancelCall:
		if !e.flags.cancel(cmd.CancelCall.Token) {
			return gocore.Refuse(tooManyCalls)
		}
	}
	return nil
}

// Open starts the demo's goroutine, which emits the account and the chat
// list at once, as zfcore's demo thread does before the startup permit.
func (e *Engine) Open(h *gocore.Host) error {
	e.h = h
	h.Go("zf-demo", e.run)
	return nil
}

// Start is the startup permit: the link and the contacts follow.
func (e *Engine) Start() { e.put(input{start: true}) }

// Command hands cmd to the demo's goroutine. The commands the demo only
// accepts go no further, as they get nothing back.
func (e *Engine) Command(cmd *gocore.Command) {
	if DispositionOf(cmd.Tag) != Answers {
		return
	}
	e.put(input{cmd: cmd})
}

// Stop has nothing to do: the goroutine sees the context end.
func (e *Engine) Stop() {}

type input struct {
	start bool
	cmd   *gocore.Command
}

func (e *Engine) put(in input) {
	e.mu.Lock()
	e.inbox = append(e.inbox, in)
	e.mu.Unlock()
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) take() (input, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.inbox) == 0 {
		return input{}, false
	}
	in := e.inbox[0]
	e.inbox[0] = input{}
	e.inbox = e.inbox[1:]
	return in, true
}

// run is the demo's goroutine: what open has without the network (the
// account and the chat list), the media after it, then the inputs and
// timers until Close. An input waiting goes before a timer that is due, as
// on zfcore's channel.
func (e *Engine) run(ctx context.Context) {
	s := newState(e)
	s.opened()
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		if ctx.Err() != nil {
			s.stop()
			return
		}
		if in, ok := e.take(); ok {
			s.input(in)
			continue
		}
		if len(s.timers) > 0 && !time.Now().Before(s.timers[0].at) {
			s.fire(heap.Pop(&s.timers).(*timer).due)
			continue
		}
		var due <-chan time.Time
		if len(s.timers) > 0 {
			t.Reset(time.Until(s.timers[0].at))
			due = t.C
		}
		select {
		case <-ctx.Done():
		case <-e.kick:
		case <-due:
		}
		if due != nil && !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}
}

// timer is something the demo does later, in order of (at, seq).
type timer struct {
	at  time.Time
	seq uint64
	due due
}

type timers []*timer

func (q timers) Len() int { return len(q) }
func (q timers) Less(i, j int) bool {
	if !q[i].at.Equal(q[j].at) {
		return q[i].at.Before(q[j].at)
	}
	return q[i].seq < q[j].seq
}
func (q timers) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *timers) Push(x any)   { *q = append(*q, x.(*timer)) }
func (q *timers) Pop() any {
	old := *q
	t := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]
	return t
}

// cancelFlags are the flags the UI's call tokens stand for (zfcore's
// Shared::cancel_flag): made on first use, by the call command or an early
// CancelCall. A full table forgets its oldest flag no call holds; with
// every one held, a new token is refused.
type cancelFlags struct {
	mu    sync.Mutex
	flags []*cancelFlag
}

const maxCancelFlags = 16

type cancelFlag struct {
	token     uint64
	cancelled atomic.Bool
	holds     int // calls queued or being placed that read it; under mu
}

func (f *cancelFlags) get(token uint64) *cancelFlag {
	for _, c := range f.flags {
		if c.token == token {
			return c
		}
	}
	if len(f.flags) == maxCancelFlags {
		free := -1
		for i, c := range f.flags {
			if c.holds == 0 {
				free = i
				break
			}
		}
		if free < 0 {
			return nil
		}
		f.flags = append(f.flags[:free], f.flags[free+1:]...)
	}
	c := &cancelFlag{token: token}
	f.flags = append(f.flags, c)
	return c
}

// hold takes the flag of token for a call command.
func (f *cancelFlags) hold(token uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.get(token)
	if c == nil {
		return false
	}
	c.holds++
	return true
}

// cancel sets the flag of token.
func (f *cancelFlags) cancel(token uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.get(token)
	if c == nil {
		return false
	}
	c.cancelled.Store(true)
	return true
}

// held is the flag a call command took in Accept.
func (f *cancelFlags) held(token uint64) *cancelFlag {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.flags {
		if c.token == token {
			return c
		}
	}
	return nil
}

// release lets a call's hold on its flag go.
func (f *cancelFlags) release(c *cancelFlag) {
	if c == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.holds > 0 {
		c.holds--
	}
}

// generations is the process's call counter (upstream's
// calls::next_generation): the calls the demo is rung in, and Conn's
// CallGeneration for the ones the UI places.
var generations atomic.Uint64

func nextGeneration() uint64 { return generations.Add(1) }
