package gocore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/blobs"
	"zapfast/gocore/out"
)

// upstreamKeyring is upstream ZapFast's keyring service, which a real core
// refuses (zfcore's cores.rs): this app never opens its archive keys.
const upstreamKeyring = "rocks.zapfast.ZapFast"

// Conn is one account's backend in process: a core.Conn over an Engine.
type Conn struct {
	cfg   core.Config
	eng   Engine
	host  *Host
	out   *out.Out
	blobs *blobs.Table
	picks picks
	wake  func()

	// mu guards the command queue and the flags Send and Start read.
	mu       sync.Mutex
	cmds     []item
	started  bool
	closed   atomic.Bool // set under mu and drainMu: every call is refused
	closing  atomic.Bool // Close has begun
	poisoned atomic.Bool // the engine panicked
	kick     chan struct{}

	// qmu guards the event queue.
	qmu       sync.Mutex
	events    [][]byte // encoded records; events[head:] wait
	head      int
	signalled bool // woke the UI, which has not drained to empty since
	qclosed   bool

	// life is held for reading through each blob call, and for writing
	// while Close sets closed: a blob call ends before Close goes on, or
	// sees closed.
	life sync.RWMutex

	// drainMu is held through a Drain, its Handler calls included.
	drainMu sync.Mutex
	ev      abi.Event
	stats   core.Stats

	// The engine goroutine and Host.Go's, which Close waits for.
	ctx      context.Context
	cancel   context.CancelFunc
	stop     chan struct{}
	goMu     sync.Mutex
	goClosed bool
	wg       sync.WaitGroup
	done     chan struct{}
}

// item is one entry of the command queue: a command, or the permit.
type item struct {
	cmd   *Command
	start bool
}

var _ core.Conn = (*Conn)(nil)

// Open opens one account's backend on eng with cfg, as core.Open opens
// the Rust core's: Account defaults to "1"; a real one must be digits with
// no leading zero, and its folders default to core.Dirs and its keyring
// service to core.KeyringService (upstream's is refused). Library is
// ignored. The demo (cfg.Demo) needs none of them. It calls eng.Open and
// starts the engine goroutine; nothing blocks, so it may run on the UI
// thread.
func Open(cfg core.Config, eng Engine) (*Conn, error) {
	if cfg.Account == "" {
		cfg.Account = "1"
	}
	if !cfg.Demo {
		if !safeAccount(cfg.Account) {
			return nil, &core.Error{Op: "zf_core_open", Code: core.EArg, Msg: "the account id is not a folder name of digits"}
		}
		if cfg.KeyringService == "" {
			cfg.KeyringService = core.KeyringService
		}
		if cfg.KeyringService == upstreamKeyring {
			return nil, &core.Error{Op: "zf_core_open", Code: core.EArg, Msg: "a real core needs the app's own keyring service, not empty or upstream's"}
		}
		if cfg.StateDir == "" || cfg.CacheDir == "" {
			state, cache, err := core.Dirs()
			if err != nil {
				return nil, fmt.Errorf("zapfast core: the app's directories: %w", err)
			}
			cfg.StateDir, cfg.CacheDir = cmp(cfg.StateDir, state), cmp(cfg.CacheDir, cache)
		}
	}
	c := &Conn{cfg: cfg, eng: eng, blobs: blobs.New(), wake: cfg.Wake, kick: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	c.out = out.New((*queue)(c), c.blobs)
	c.host = &Host{c: c}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	if err := eng.Open(c.host); err != nil {
		c.shut()
		go c.waitDone()
		return nil, err
	}
	c.wg.Add(1)
	go c.run()
	return c, nil
}

func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// safeAccount is upstream's AccountId::is_safe: one or more digits with no
// leading zero, so it can neither be absolute nor climb out of accounts/.
func safeAccount(id string) bool {
	if id == "" || id[0] == '0' {
		return false
	}
	for i := range len(id) {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
}

// Host is what the engine was given.
func (c *Conn) Host() *Host { return c.host }

// Start gives the engine its startup permit, queued in order with the
// commands. A second one is refused with ZF_E_STATE.
func (c *Conn) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.closed.Load():
		return core.ErrClosed
	case c.poisoned.Load():
		return poisoned("zf_core_start")
	case c.started:
		return &core.Error{Op: "zf_core_start", Code: core.EState, Msg: "the core has already started"}
	}
	c.started = true
	c.enqueue(item{start: true})
	return nil
}

func poisoned(op string) error {
	return &core.Error{Op: op, Code: core.EPoisoned, Msg: "an earlier call panicked; close this core"}
}

// enqueue queues it for the engine goroutine; mu is held.
func (c *Conn) enqueue(it item) {
	c.cmds = append(c.cmds, it)
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Send checks cmd and queues a copy of it for the engine; it never
// blocks on the engine, and any goroutine may call it. It refuses, as the
// Rust core does: a field over its bound (the encoder's error); after a
// panic, ZF_E_POISONED; a command the engine does not answer, ZF_E_STATE
// naming it; inconsistent fields, ZF_E_REFUSED; a blob that is not held at
// the length named, ZF_E_NOT_FOUND. A refused command takes no blob; a
// queued one takes the blob it names. PickAnswer goes to the Conn's own
// broker, whatever the engine.
func (c *Conn) Send(cmd *abi.Command) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	rec, err := cmd.Encode(nil)
	if err != nil {
		return fmt.Errorf("zapfast core: %v: %w", cmd.Tag, err)
	}
	if c.poisoned.Load() {
		return poisoned("zf_send")
	}
	own := &Command{}
	if err := own.Decode(rec); err != nil {
		return &core.Error{Op: "zf_send", Code: core.EMalformed, Msg: "the command: " + err.Error()}
	}
	if own.Tag == abi.CommandTagPickAnswer {
		c.picks.answer(own.PickAnswer.ID, own.PickAnswer.Paths)
		return nil
	}

	var refusal error
	if loc, ok := c.guard("abi", "caller", func() { refusal = c.eng.Accept(&own.Command) }); !ok {
		return &core.Error{Op: "zf_send", Code: core.EPanic, Msg: "zapfast core panicked at " + loc + "; the core is poisoned"}
	}
	if refusal != nil {
		return c.refused(own.Tag, refusal)
	}
	if err := check(&own.Command); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return core.ErrClosed
	}
	if ref, ok := blobOf(&own.Command); ok {
		b, err := c.blobs.Take(ref)
		if err != nil {
			return &core.Error{Op: "zf_send", Code: core.ENotFound, Msg: "blob " + strconv.FormatUint(ref.ID, 10) + " is not held at the length named"}
		}
		own.Blob = b
	}
	c.enqueue(item{cmd: own})
	return nil
}

// refused turns Accept's refusal into the error Send returns.
func (c *Conn) refused(tag abi.CommandTag, err error) error {
	if errors.Is(err, ErrUnsupported) {
		if demoOnly[tag] {
			return &core.Error{Op: "zf_send", Code: core.EState, Msg: "that command needs the demo core"}
		}
		return &core.Error{Op: "zf_send", Code: core.EState, Msg: "the " + c.eng.Name() + " core does not answer " + tag.String()}
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		e := *ce
		if e.Op == "" {
			e.Op = "zf_send"
		}
		return &e
	}
	return &core.Error{Op: "zf_send", Code: core.ERefused, Msg: err.Error()}
}

// run is the engine goroutine: the permit and the commands, in order,
// until Close; then Stop.
func (c *Conn) run() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stop:
			c.guard("engine", "engine", c.eng.Stop)
			return
		case <-c.kick:
		}
		for {
			c.mu.Lock()
			items := c.cmds
			c.cmds = nil
			c.mu.Unlock()
			if len(items) == 0 {
				break
			}
			for _, it := range items {
				if c.closing.Load() || c.poisoned.Load() {
					// Dropped: Close has begun, or the engine is in no
					// state to go on.
					break
				}
				if it.start {
					c.guard("engine", "engine", c.eng.Start)
				} else {
					c.guard("engine", "engine", func() { c.eng.Command(it.cmd) })
				}
			}
		}
	}
}

// guard runs f; a panic in it becomes a Fatal event (origin, thread and
// where it happened: never the panic's value, which may quote user data)
// and poisons the Conn. It reports where, and false, when f panicked.
func (c *Conn) guard(origin, thread string, f func()) (location string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			location = panicSite()
			ok = false
			c.poisoned.Store(true)
			slog.Error("zapfast gocore: panic", "origin", origin, "thread", thread, "at", location)
			ev := abi.Event{Tag: abi.EventTagFatal}
			ev.Fatal = abi.EventFatal{
				Origin:   []byte(out.Clip(origin, 64)),
				Thread:   []byte(out.Clip(thread, 128)),
				Location: []byte(out.Clip(location, 1024)),
			}
			c.out.Emit(&ev)
		}
	}()
	f()
	return "", true
}

// panicSite is the function and line that panicked, from a deferred
// recover: the first frame after runtime.gopanic outside the runtime.
func panicSite() string {
	pc := make([]uintptr, 64)
	frames := runtime.CallersFrames(pc[:runtime.Callers(3, pc)])
	inPanic := false
	for {
		f, more := frames.Next()
		switch {
		case f.Function == "runtime.gopanic":
			inPanic = true
		case inPanic && !strings.HasPrefix(f.Function, "runtime."):
			return f.Function + " (" + filepath.Base(f.File) + ":" + strconv.Itoa(f.Line) + ")"
		}
		if !more {
			return "unknown"
		}
	}
}

// queue is the Conn as out.Queue: what the engine emits goes into the
// event queue, and the UI is woken when it was drained to empty.
type queue Conn

// Push queues rec and reports whether it was queued: not once closed.
func (q *queue) Push(rec []byte) bool {
	c := (*Conn)(q)
	c.qmu.Lock()
	if c.qclosed {
		c.qmu.Unlock()
		return false
	}
	c.events = append(c.events, rec)
	wake := !c.signalled
	c.signalled = true
	c.qmu.Unlock()
	if wake && c.wake != nil {
		c.wake()
	}
	return true
}

// Drain hands every waiting event to h, in order, and returns how many,
// as core.Core.Drain does: call it on the UI thread when the Wake came.
// With a budget above 0 it stops once that much time has passed (after at
// least one event) and calls Wake, so the next frame goes on; 0 drains
// until the queue is empty, which re-arms the wake: the next event queued
// calls Wake again, from the goroutine that emits it.
//
// h runs with no lock but Drain's own held, so it may Send (what that
// queues may come back in this very call), but must not call Drain or
// Close. The event and its byte fields are valid only during the call.
func (c *Conn) Drain(h core.Handler, budget time.Duration) (int, error) {
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	if c.closed.Load() {
		return 0, core.ErrClosed
	}
	var t0 time.Time
	if budget > 0 {
		t0 = time.Now()
	}
	n := 0
	for {
		c.qmu.Lock()
		if c.head == len(c.events) {
			c.events, c.head = c.events[:0], 0
			c.signalled = false
			c.qmu.Unlock()
			return n, nil
		}
		if budget > 0 && n > 0 && time.Since(t0) >= budget {
			c.qmu.Unlock()
			if c.wake != nil {
				c.wake()
			}
			return n, nil
		}
		rec := c.events[c.head]
		c.events[c.head] = nil
		c.head++
		c.qmu.Unlock()
		if err := c.ev.Decode(rec); err != nil {
			// Out encoded it, so it decodes; counted all the same, as the
			// Rust core's Drain counts a malformed record.
			c.stats.Malformed++
			continue
		}
		n++
		c.stats.Events++
		h.Event(&c.ev)
	}
}

// Stats is what Drain has done so far. Call it where Drain is called.
func (c *Conn) Stats() core.Stats { return c.stats }

// Pending is how many events wait to be drained.
func (c *Conn) Pending() int {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	return len(c.events) - c.head
}

// ReadBlob copies the blob's bytes from off into dst and returns how many,
// 0 at its end.
func (c *Conn) ReadBlob(id, off uint64, dst []byte) (int, error) {
	c.life.RLock()
	defer c.life.RUnlock()
	if c.closed.Load() {
		return 0, core.ErrClosed
	}
	n, err := c.blobs.Read(id, off, dst)
	switch {
	case errors.Is(err, blobs.ErrNotFound):
		return 0, &core.Error{Op: "zf_blob_read", Code: core.ENotFound, Msg: "no blob " + strconv.FormatUint(id, 10)}
	case errors.Is(err, blobs.ErrRange):
		return 0, &core.Error{Op: "zf_blob_read", Code: core.ERange, Msg: "the offset is past the blob's end"}
	}
	return n, nil
}

// PutBlob copies src into a new blob for a command to name: the command
// takes it on success; otherwise free it.
func (c *Conn) PutBlob(src []byte) (abi.BlobRef, error) {
	c.life.RLock()
	defer c.life.RUnlock()
	if c.closed.Load() {
		return abi.BlobRef{}, core.ErrClosed
	}
	if c.poisoned.Load() {
		return abi.BlobRef{}, poisoned("zf_blob_put")
	}
	return c.blobs.Put(src), nil
}

// FreeBlob frees a blob; an unknown id is ignored.
func (c *Conn) FreeBlob(id uint64) {
	c.life.RLock()
	defer c.life.RUnlock()
	if c.closed.Load() {
		return
	}
	c.blobs.Free(id)
}

// Close stops the engine: a Drain under way finishes first, then every
// call returns core.ErrClosed, every Pick still waiting is answered with
// no paths, Host's context is cancelled, queued commands are dropped and
// the engine goroutine calls Stop. It waits up to timeout for that and
// every goroutine of Host.Go to return; past it, closing goes on in the
// background and Close returns an *core.Error with ETimeout. Never call it
// on the UI thread while the app runs, nor from a Handler.
func (c *Conn) Close(timeout time.Duration) error {
	if !c.closing.CompareAndSwap(false, true) {
		return core.ErrClosed
	}
	if closeHook != nil {
		closeHook()
	}
	c.drainMu.Lock()
	c.mu.Lock()
	c.life.Lock()
	c.closed.Store(true)
	c.cmds = nil
	c.life.Unlock()
	c.mu.Unlock()
	c.drainMu.Unlock()
	c.shut()
	close(c.stop)
	go c.waitDone()

	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case <-c.done:
		case <-t.C:
		}
	}
	select {
	case <-c.done:
	default:
		return &core.Error{Op: "zf_core_close", Code: core.ETimeout, Msg: "the core is still closing; it finishes in the background"}
	}
	c.blobs.Clear()
	return nil
}

// closeHook, when a test sets it, runs as Close starts waiting for Drain.
var closeHook func()

// shut empties the event queue for good, answers the waiting picks and
// stops Host.Go.
func (c *Conn) shut() {
	c.closed.Store(true)
	c.qmu.Lock()
	c.qclosed = true
	c.events, c.head = nil, 0
	c.qmu.Unlock()
	c.picks.close()
	c.goMu.Lock()
	c.goClosed = true
	c.goMu.Unlock()
	c.cancel()
}

func (c *Conn) waitDone() {
	c.wg.Wait()
	close(c.done)
}

// Done is closed once the engine goroutine and every goroutine of Host.Go
// have returned (after Close).
func (c *Conn) Done() <-chan struct{} { return c.done }
