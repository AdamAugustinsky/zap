package core

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/clib"
	gcore "gonativeui/core"

	"zapfast/abi"
)

// DrainMin is ZF_DRAIN_MIN: zf_drain's buffer, one record and its prefix.
const DrainMin = abi.MaxRecord + abi.FrameHeader

// Config opens one account's core.
type Config struct {
	// Library is the core library's path, for unbundled runs; empty: the
	// loading policy's search (ResolveLibrary). A bundled app refuses any
	// path but its own Frameworks' library.
	Library string
	// Demo opens the demo core: a synthetic account, no network, no disk,
	// no keyring. Tests use nothing else.
	Demo bool
	// StateDir and CacheDir are the app's roots; empty: Dirs (the app's
	// own). The demo ignores them.
	StateDir, CacheDir string
	// Account is the account's folder name, digits; empty: "1".
	Account string
	// KeyringService names the OS keyring items of the archive keys; empty:
	// KeyringService. The core refuses upstream ZapFast's. The demo
	// ignores it.
	KeyringService string
	// Wake is called from the core's event goroutine when events wait (and
	// by OpenAsync once the core is open): platform.Wake in an app. It must
	// not block. Nil: the caller polls Drain.
	Wake func()
}

// Conn is what the UI uses of one account's core: *Core over the library,
// or fake.Core (package core/fake) in tests that run without it.
type Conn interface {
	Start() error
	Send(cmd *abi.Command) error
	Drain(h Handler, budget time.Duration) (int, error)
	ReadBlob(id, off uint64, dst []byte) (int, error)
	PutBlob(src []byte) (abi.BlobRef, error)
	FreeBlob(id uint64)
	Close(timeout time.Duration) error
}

var _ Conn = (*Core)(nil)

// Handler receives drained events, in order, on the goroutine that called
// Drain. The event and its byte fields are valid only during the call.
// Event may Send, but must not call Drain or Close.
type Handler interface {
	Event(e *abi.Event)
}

// Stats counts what Drain did.
type Stats struct {
	Events    uint64 // records decoded and handed out
	Malformed uint64 // records skipped because they did not decode
	Drains    uint64 // zf_drain calls that returned records
}

// site is one call site: its clib.Call and everything the call passes by
// pointer, together in the Core's heap struct, used by one goroutine at a
// time under mu (a lock that never parks the goroutine).
type site struct {
	mu    gcore.Mutex
	call  *clib.Call
	err   zfErr
	n     uint64 // size_t *len
	count uint32 // uint32_t *count
	id    uint64 // uint64_t *id
}

// Core is one open account core.
type Core struct {
	lib  *Lib
	wake func()

	// h is the zf_core *: C memory, read and cleared under the sites' locks.
	h unsafe.Pointer

	sendS, drainS, startS, readS, putS, freeS, frameS site
	cmd                                               []byte  // the encoded command, reused
	frame                                             zfFrame // zf_frame_acquire's result, under frameS

	// voiceS guards only the start of a DecodeVoice, which then runs with
	// no lock held, counted in decoding: closeHandle waits for those before
	// it closes the core.
	voiceS   site
	decoding sync.WaitGroup

	buf   []byte // DrainMin bytes, allocated at open
	rest  []byte // records of buf not handed out yet
	ev    abi.Event
	more  bool  // the last Drain stopped on its budget
	fail  error // ErrFailed once the stream cannot be read
	stats Stats
	noted bool // a malformed record was logged

	// The event goroutine.
	signalled atomic.Bool // it woke the UI, which has not drained to empty since
	closing   atomic.Bool
	park      *gcore.Parker // where it waits for that drain
	file      *os.File      // a duplicate of the core's event descriptor, in the poller
	raw       syscall.RawConn
	readable  func(uintptr) bool
	done      chan struct{} // closed when the goroutine has returned
}

// Open loads the library (ResolveLibrary(cfg.Library)) and opens one core
// with cfg. It blocks for the dlopen: call it off the UI thread, or use
// OpenAsync.
func Open(cfg Config) (*Core, error) {
	path, err := ResolveLibrary(cfg.Library)
	if err != nil {
		return nil, err
	}
	lib, err := Load(path)
	if err != nil {
		return nil, err
	}
	return lib.Open(cfg)
}

// Open opens one core of l with cfg.
func (l *Lib) Open(cfg Config) (*Core, error) {
	return l.open(cfg, abi.ABIVersion, abi.SchemaHash)
}

func (l *Lib) open(cfg Config, version uint32, hash uint64) (*Core, error) {
	oc := abi.OpenConfig{ABI: version, SchemaHash: hash, Demo: cfg.Demo, Account: []byte(cfg.Account)}
	if len(oc.Account) == 0 {
		oc.Account = []byte("1")
	}
	if !cfg.Demo {
		state, cache := cfg.StateDir, cfg.CacheDir
		if state == "" || cache == "" {
			s, c, err := Dirs()
			if err != nil {
				return nil, fmt.Errorf("zapfast core: the app's directories: %w", err)
			}
			state, cache = cmp(state, s), cmp(cache, c)
		}
		oc.StateDir, oc.CacheDir = []byte(state), []byte(cache)
		oc.KeyringService = []byte(cmp(cfg.KeyringService, KeyringService))
		oc.DeviceName = []byte(DeviceName)
	}
	rec, err := oc.Encode(nil)
	if err != nil {
		return nil, fmt.Errorf("zapfast core: the open configuration: %w", err)
	}

	c := &Core{lib: l, wake: cfg.Wake, buf: make([]byte, DrainMin), cmd: make([]byte, 0, 4096), done: make(chan struct{})}
	for _, s := range c.sites() {
		s.call = clib.NewCall()
	}
	s := &c.startS
	var h uint64 // zf_core *out, written by C: an address, never a Go pointer
	st := s.call.Reset().Ptr(unsafe.Pointer(&rec[0])).Uint(uint64(len(rec))).
		Ptr(unsafe.Pointer(&h)).Ptr(unsafe.Pointer(&s.err)).Do(l.fn.open).Int32()
	if st != StatusOK {
		return nil, s.err.error("zf_core_open", st)
	}
	c.h = *(*unsafe.Pointer)(unsafe.Pointer(&h)) // C memory: reinterpret, as clib.Result.Ptr does

	if err := c.watch(); err != nil {
		c.closeHandle(time.Second)
		return nil, err
	}
	return c, nil
}

func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// sites lists the call sites in their lock order. Drain holds drainS while
// its Handler runs, and a Handler may call any other method, so drainS comes
// first: whoever takes several sites (closeHandle) takes drainS before the
// rest. No other method holds two sites at once.
func (c *Core) sites() [8]*site {
	return [...]*site{&c.drainS, &c.sendS, &c.startS, &c.readS, &c.putS, &c.freeS, &c.frameS, &c.voiceS}
}

// Lib is the library the core belongs to.
func (c *Core) Lib() *Lib { return c.lib }

// Start gives the core its startup permit (zf_core_start): migrations, then
// the connection. Call it once, after the first frame.
func (c *Core) Start() error {
	s := &c.startS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return ErrClosed
	}
	if st := s.call.Reset().Ptr(c.h).Ptr(unsafe.Pointer(&s.err)).Do(c.lib.fn.start).Int32(); st != StatusOK {
		return s.err.error("zf_core_start", st)
	}
	return nil
}

// Send encodes cmd into the core's reused buffer and queues it (zf_send,
// which copies it and never blocks). Any goroutine may call it. Once the
// buffer has grown to the commands sent, it allocates nothing unless it
// fails. A command naming a blob takes it on success.
func (c *Core) Send(cmd *abi.Command) error {
	s := &c.sendS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return ErrClosed
	}
	b, err := cmd.Encode(c.cmd[:0])
	c.cmd = b[:0]
	if err != nil {
		return fmt.Errorf("zapfast core: %v: %w", cmd.Tag, err)
	}
	st := s.call.Reset().Ptr(c.h).Ptr(unsafe.Pointer(&b[0])).Uint(uint64(len(b))).
		Ptr(unsafe.Pointer(&s.err)).Do(c.lib.fn.send).Int32()
	if st != StatusOK {
		return s.err.error("zf_send", st)
	}
	return nil
}

// Drain hands every waiting event to h, in order, and returns how many it
// handed out. Call it on the UI thread when the Wake came (calling it more
// often is cheap: without a wake and with nothing left over it returns at
// once). With a budget above 0 it stops once that much time has passed
// (after at least one event) and calls Wake, so the next frame goes on; 0
// drains until the queue is empty, which re-arms the wake.
//
// A record that does not decode is skipped and counted (and logged once). A
// record length over ZF_MAX_RECORD means the stream cannot be read further:
// Drain returns ErrFailed from then on.
//
// Once warm, draining allocates nothing per event.
func (c *Core) Drain(h Handler, budget time.Duration) (int, error) {
	s := &c.drainS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return 0, ErrClosed
	}
	if c.fail != nil {
		return 0, c.fail
	}
	if len(c.rest) == 0 && !c.more && !c.signalled.Load() {
		return 0, nil
	}
	var t0 time.Time
	if budget > 0 {
		t0 = time.Now()
	}
	n := 0
	for {
		for len(c.rest) > 0 {
			if budget > 0 && n > 0 && time.Since(t0) >= budget {
				c.more = true
				if c.wake != nil {
					c.wake()
				}
				return n, nil
			}
			rec, rest, err := abi.NextFrame(c.rest)
			if err != nil {
				// zf_drain writes whole records: this is a length prefix
				// that cannot be trusted, and nothing after it can be found.
				c.fail = ErrFailed
				c.rest = nil
				slog.Error("zapfast core: unreadable event stream; the account needs a restart", "err", err)
				return n, c.fail
			}
			c.rest = rest
			if err := c.ev.Decode(rec); err != nil {
				c.malformed(rec, err)
				continue
			}
			n++
			c.stats.Events++
			h.Event(&c.ev)
		}
		st := s.call.Reset().Ptr(c.h).Ptr(unsafe.Pointer(&c.buf[0])).Uint(uint64(len(c.buf))).
			Ptr(unsafe.Pointer(&s.n)).Ptr(unsafe.Pointer(&s.count)).Ptr(unsafe.Pointer(&s.err)).
			Do(c.lib.fn.drain).Int32()
		switch st {
		case StatusOK:
			c.stats.Drains++
			c.rest = c.buf[:min(s.n, uint64(len(c.buf)))]
		case StatusEmpty:
			// The core re-armed its descriptor: let the event goroutine
			// wait in the poller again.
			c.more = false
			c.signalled.Store(false)
			c.park.Unpark()
			return n, nil
		default:
			return n, s.err.error("zf_drain", st)
		}
	}
}

func (c *Core) malformed(rec []byte, err error) {
	c.stats.Malformed++
	if c.noted {
		return
	}
	c.noted = true
	var tag uint16
	if len(rec) >= 2 {
		tag = uint16(rec[0]) | uint16(rec[1])<<8
	}
	// The tag and the reason only: a record may hold message content.
	slog.Error("zapfast core: skipped an event record that does not decode (further ones are counted, not logged)",
		"tag", abi.EventTag(tag).String(), "len", len(rec), "err", err)
}

// Stats returns what Drain has done so far. Call it where Drain is called.
func (c *Core) Stats() Stats { return c.stats }

// ReadBlob copies the blob's bytes from off into dst and returns how many,
// 0 at its end (zf_blob_read). Read a large blob in pieces. Any goroutine
// may call it; concurrent readers take turns.
func (c *Core) ReadBlob(id, off uint64, dst []byte) (int, error) {
	s := &c.readS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return 0, ErrClosed
	}
	var p unsafe.Pointer
	if len(dst) > 0 {
		p = unsafe.Pointer(&dst[0])
	}
	st := s.call.Reset().Ptr(c.h).Uint(id).Uint(off).Ptr(p).Uint(uint64(len(dst))).
		Ptr(unsafe.Pointer(&s.n)).Ptr(unsafe.Pointer(&s.err)).Do(c.lib.fn.blobRead).Int32()
	if st != StatusOK {
		return 0, s.err.error("zf_blob_read", st)
	}
	return int(min(s.n, uint64(len(dst)))), nil
}

// PutBlob copies src into a new blob held by the core, for a command to
// name (outgoing images, voice notes): the command takes it on success;
// otherwise free it.
func (c *Core) PutBlob(src []byte) (abi.BlobRef, error) {
	s := &c.putS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return abi.BlobRef{}, ErrClosed
	}
	var p unsafe.Pointer
	if len(src) > 0 {
		p = unsafe.Pointer(&src[0])
	}
	st := s.call.Reset().Ptr(c.h).Ptr(p).Uint(uint64(len(src))).
		Ptr(unsafe.Pointer(&s.id)).Ptr(unsafe.Pointer(&s.err)).Do(c.lib.fn.blobPut).Int32()
	if st != StatusOK {
		return abi.BlobRef{}, s.err.error("zf_blob_put", st)
	}
	return abi.BlobRef{ID: s.id, Len: uint64(len(src))}, nil
}

// FreeBlob frees a blob; an unknown id is ignored.
func (c *Core) FreeBlob(id uint64) {
	s := &c.freeS
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.h == nil {
		return
	}
	s.call.Reset().Ptr(c.h).Uint(id).Do(c.lib.fn.blobFree)
}

// Close stops the event goroutine and then the core (zf_core_close), which
// blocks for up to timeout: never on the UI thread while the app runs. Every
// call on c afterwards returns ErrClosed. A core that takes longer finishes
// closing in the background, and Close returns an *Error with ETimeout.
// Decodes in progress (DecodeVoice) finish first, outside the timeout.
func (c *Core) Close(timeout time.Duration) error {
	if !c.closing.CompareAndSwap(false, true) {
		return ErrClosed
	}
	// The goroutine waits either for a drain (the parker) or in the poller
	// (the file); waking both ends it. File.Close waits for its read to end.
	c.park.Close()
	c.file.Close()
	<-c.done
	return c.closeHandle(timeout)
}

// closeHook, when a test sets it, runs as closeHandle starts taking the
// call sites' locks.
var closeHook func()

// closeHandle takes the handle from every call site, in lock order, then
// closes it.
func (c *Core) closeHandle(timeout time.Duration) error {
	if closeHook != nil {
		closeHook()
	}
	sites := c.sites()
	for _, s := range sites {
		s.mu.Lock()
	}
	h := c.h
	c.h = nil
	for _, s := range sites {
		s.mu.Unlock()
	}
	if h == nil {
		return ErrClosed
	}
	// No DecodeVoice starts now that h is nil; those running still use it.
	c.decoding.Wait()
	ms := min(timeout.Milliseconds(), int64(^uint32(0)))
	var e zfErr
	st := clib.NewCall().Ptr(h).Uint32(uint32(max(ms, 0))).Ptr(unsafe.Pointer(&e)).Do(c.lib.fn.coreClose).Int32()
	if st != StatusOK {
		return e.error("zf_core_close", st)
	}
	return nil
}

// Done is closed once the event goroutine has returned (after Close).
func (c *Core) Done() <-chan struct{} { return c.done }

// watch starts the event goroutine on a duplicate of the core's event
// descriptor, so closing ours never closes the core's.
func (c *Core) watch() error {
	s := &c.startS
	fd := s.call.Reset().Ptr(c.h).Do(c.lib.fn.eventFD).Int32()
	if fd < 0 {
		return errors.New("zapfast core: zf_event_fd returned no descriptor")
	}
	dup, err := dupCloexec(int(fd))
	if err != nil {
		return fmt.Errorf("zapfast core: duplicating the event descriptor: %w", err)
	}
	// The descriptor is O_NONBLOCK (the core sets it), so the file is
	// registered with the network poller. Never call c.file.Fd: it would
	// make the shared descriptor blocking.
	c.file = os.NewFile(uintptr(dup), "zapfast-core-events")
	if c.raw, err = c.file.SyscallConn(); err != nil {
		c.file.Close()
		return fmt.Errorf("zapfast core: the event descriptor: %w", err)
	}
	c.park = gcore.NewParker()
	c.readable = c.onReadable
	go c.run()
	return nil
}

// run is the event goroutine. RawConn.Read calls onReadable, then waits in
// the network poller for the descriptor to become readable (a goroutine
// parked there pins no thread and takes no sudog) and calls it again,
// until it returns true or the file is closed. The descriptor is never
// read: zf_drain re-arms it.
//
// No wakeup is lost. The core writes a byte only when its queue goes from
// drained-to-empty to non-empty, and the poller reports that write. While
// onReadable sleeps, waiting for the UI's drain, a write is kept by the
// poller for the next wait. The first call, before any wait, wakes the UI
// unconditionally, since what the descriptor held before it entered the
// poller is not reported.
func (c *Core) run() {
	defer close(c.done)
	c.raw.Read(c.readable)
}

func (c *Core) onReadable(uintptr) bool {
	if c.closing.Load() {
		return true
	}
	c.signalled.Store(true)
	if c.wake != nil {
		c.wake()
	}
	// Until Drain finds the queue empty (or Close): the descriptor stays
	// readable until then, so waiting in the poller would spin.
	return !c.park.Park() || c.closing.Load()
}
