package core

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zapfast/abi"
)

const (
	stressSenders  = 8
	stressSends    = 400 // per sender, a fifth of them refused commands
	stressBlobbers = 4
)

// orderHandler checks that each sender's typing events arrive complete and
// in order: the demo echoes the sender field, "g/i".
type orderHandler struct {
	next [stressSenders]int
	n    atomic.Int64 // read by the test while the drainer runs
	bad  []string
}

func (h *orderHandler) Event(e *abi.Event) {
	if e.Tag != abi.EventTagTyping {
		return
	}
	g, i, ok := bytes.Cut(e.Typing.Sender, []byte("/"))
	gi, err1 := strconv.Atoi(string(g))
	ii, err2 := strconv.Atoi(string(i))
	if !ok || err1 != nil || err2 != nil || gi < 0 || gi >= stressSenders {
		h.bad = append(h.bad, "sender "+string(e.Typing.Sender))
		return
	}
	if ii != h.next[gi] {
		h.bad = append(h.bad, fmt.Sprintf("sender %d: event %d, want %d", gi, ii, h.next[gi]))
	}
	h.next[gi] = ii + 1
	h.n.Add(1)
}

// drainer drains on a goroutine locked to its OS thread, as the UI thread
// does, until told to stop or the core is closed.
type drainer struct {
	h    *orderHandler
	stop atomic.Bool
	err  error
	done chan struct{}
}

func startDrainer(c *Core, w *wakes) *drainer {
	d := &drainer{h: &orderHandler{}, done: make(chan struct{})}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(d.done)
		for !d.stop.Load() {
			if _, err := c.Drain(d.h, 0); err != nil {
				d.err = err
				return
			}
			select {
			case <-w.ch:
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	return d
}

// sender sends typing changes and, every fifth command, one the demo
// refuses, checking that the refusal's zf_err names that very command. It
// returns how many typing changes went out.
func sender(c *Core, g int, n int, untilClosed bool, bad *atomic.Int64, note func(string)) int {
	var cmd abi.Command
	sent := 0
	for i := 0; untilClosed || i < n; i++ {
		if i%5 == 4 {
			name := "Unlink"
			cmd.Tag = abi.CommandTagUnlink
			if (g+i)%2 == 0 {
				name, cmd.Tag = "RemoveAccount", abi.CommandTagRemoveAccount
			}
			err := c.Send(&cmd)
			if errors.Is(err, ErrClosed) {
				return sent
			}
			var e *Error
			if !errors.As(err, &e) || e.Code != EState || e.Op != "zf_send" || e.Msg != "the demo core does not answer "+name {
				bad.Add(1)
				note(fmt.Sprintf("sender %d: %s refused with %v", g, name, err))
			}
			continue
		}
		cmd.Tag = abi.CommandTagDemoTyping
		cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(ada), Sender: []byte(strconv.Itoa(g) + "/" + strconv.Itoa(sent))}
		if err := c.Send(&cmd); err != nil {
			if errors.Is(err, ErrClosed) {
				return sent
			}
			bad.Add(1)
			note(fmt.Sprintf("sender %d: typing: %v", g, err))
			continue
		}
		sent++
	}
	return sent
}

// blobber puts, reads back in pieces and frees blobs, and reads a blob that
// does not exist, whose error must name that id.
func blobber(c *Core, g int, rounds int, untilClosed bool, bad *atomic.Int64, note func(string)) {
	piece := make([]byte, 4096)
	for r := 0; untilClosed || r < rounds; r++ {
		src := make([]byte, 1000+(g*7919+r*104729)%60000)
		for i := range src {
			src[i] = byte(g + r + i)
		}
		ref, err := c.PutBlob(src)
		if errors.Is(err, ErrClosed) {
			return
		}
		if err != nil {
			bad.Add(1)
			note(fmt.Sprintf("blobber %d: put: %v", g, err))
			continue
		}
		var got []byte
		for off := uint64(0); ; {
			n, err := c.ReadBlob(ref.ID, off, piece)
			if errors.Is(err, ErrClosed) {
				return
			}
			if err != nil {
				bad.Add(1)
				note(fmt.Sprintf("blobber %d: read: %v", g, err))
				break
			}
			if n == 0 {
				break
			}
			got = append(got, piece[:n]...)
			off += uint64(n)
		}
		if !bytes.Equal(got, src) {
			bad.Add(1)
			note(fmt.Sprintf("blobber %d: blob %d read back differs", g, ref.ID))
		}
		c.FreeBlob(ref.ID)
		missing := uint64(1)<<62 + uint64(g)<<20 + uint64(r)
		_, err = c.ReadBlob(missing, 0, piece)
		if errors.Is(err, ErrClosed) {
			return
		}
		var e *Error
		if !errors.As(err, &e) || e.Code != ENotFound || e.Msg != "no blob "+strconv.FormatUint(missing, 10) {
			bad.Add(1)
			note(fmt.Sprintf("blobber %d: a missing blob: %v", g, err))
		}
	}
}

// Many goroutines send, read and put blobs while one locked thread drains,
// with SIGPROF on every thread (the core's included) and asynchronous
// preemption on: no event is lost or reordered, every error is the one its
// own call made, and closing in the middle of all that neither deadlocks
// nor leaves the event goroutine behind.
func TestStressSendDrainBlobsUnderSignalsThenCloseMidTraffic(t *testing.T) {
	s := openDemo(t)
	c := s.rust()
	stop := profile(t)
	defer stop()
	var bad atomic.Int64
	var mu sync.Mutex
	var notes []string
	note := func(m string) {
		mu.Lock()
		if len(notes) < 20 {
			notes = append(notes, m)
		}
		mu.Unlock()
	}
	report := func() {
		if n := bad.Load(); n != 0 {
			t.Errorf("%d bad results, among them: %q", n, notes)
		}
	}

	// Phase 1: a fixed amount of traffic, all of it accounted for.
	d := startDrainer(c, s.w)
	var wg sync.WaitGroup
	var sent [stressSenders]int
	for g := range stressSenders {
		wg.Go(func() { sent[g] = sender(c, g, stressSends, false, &bad, note) })
	}
	for g := range stressBlobbers {
		wg.Go(func() { blobber(c, g, 40, false, &bad, note) })
	}
	wg.Wait()
	total := 0
	for _, n := range sent {
		total += n
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-d.done:
			t.Fatalf("the drainer stopped: %v", d.err)
		default:
		}
		if int(d.h.n.Load()) >= total || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.stop.Store(true)
	<-d.done
	if n := int(d.h.n.Load()); n != total {
		t.Fatalf("%d of %d typing events arrived", n, total)
	}
	for g, n := range sent {
		if d.h.next[g] != n {
			t.Errorf("sender %d: %d events, sent %d", g, d.h.next[g], n)
		}
	}
	if len(d.h.bad) > 0 {
		t.Errorf("out of order or malformed: %q", d.h.bad[:min(len(d.h.bad), 10)])
	}
	report()

	// Phase 2: the same traffic without end, and Close in the middle of it.
	d = startDrainer(c, s.w)
	for g := range stressSenders {
		wg.Go(func() { sender(c, g, 0, true, &bad, note) })
	}
	for g := range stressBlobbers {
		wg.Go(func() { blobber(c, g, 0, true, &bad, note) })
	}
	time.Sleep(150 * time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- c.Close(10 * time.Second) }()
	all := make(chan struct{})
	go func() { wg.Wait(); <-d.done; close(all) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not return: deadlock")
	}
	select {
	case <-all:
	case <-time.After(15 * time.Second):
		t.Fatal("senders, blob readers or the drainer are stuck after Close")
	}
	if !errors.Is(d.err, ErrClosed) {
		t.Errorf("the drainer ended with %v, want ErrClosed", d.err)
	}
	select {
	case <-c.Done():
	default:
		t.Error("the event goroutine is still running")
	}
	if len(d.h.bad) > 0 {
		t.Errorf("out of order or malformed after the restart: %q", d.h.bad[:min(len(d.h.bad), 10)])
	}
	report()
}
