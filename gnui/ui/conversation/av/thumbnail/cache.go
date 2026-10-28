// Package thumbnail bounds the immutable Go copies of core-owned thumbnails.
package thumbnail

import (
	"sync"

	"gonativeui/app"
	"gonativeui/img"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
)

const Budget = 2 << 20 // 32 typical 64 KiB thumbnails, half the decoded budget across both caches.
const Readers = 2

type key struct{ account, blob uint64 }
type entry struct {
	c             *Cache
	data          []byte
	refs, size    int // cache, reader and decode leases; bytes count until the last release
	running, done bool
	conn          core.Conn // non-nil only while queued; no buffer until admitted
	ref           abi.BlobRef
	wanted        uint64
	frame         uint32
}

func (e *entry) Bytes() []byte { return e.data }
func (e *entry) Release() {
	c := e.c
	c.Lock()
	before := c.bytes
	c.release(e)
	var wake func()
	if c.bytes < before {
		c.start()
		wake, c.retryWake = c.retryWake, nil
	}
	c.Unlock()
	if wake != nil {
		wake()
	} // a closed image cache's last decoder can free the budget
}

// Cache's zero value supports direct requests. Conversation caches use New
// so Frame expires queued requests as the view changes.
type Cache struct {
	sync.Mutex
	m                           map[key]*entry
	order                       [512]key
	head, count, bytes, readers int
	owner                       *state.State // UI-thread only
	retryWake                   func()       // refused admission, under Mutex
	frame                       uint32
	wanted                      uint64
	wake                        func()
}

var caches []*Cache // the two process-lifetime conversation caches

// New registers a conversation cache for the main window's frame boundary.
func New() *Cache { c := new(Cache); caches = append(caches, c); return c }

// Frame expires requests even on frames with no media. Standalone blocks call it too.
func Frame(frame uint32) {
	for _, c := range caches {
		c.beginFrame(frame)
	}
}
func (c *Cache) beginFrame(frame uint32) {
	if c.frame == frame {
		return
	} // UI thread; workers only read frame under Mutex
	c.Lock()
	c.frame = frame
	c.Unlock()
}

func (c *Cache) release(e *entry) {
	e.refs--
	if e.refs == 0 {
		c.bytes -= e.size
		e.data = nil
	}
}
func (c *Cache) evict() {
	k := c.order[c.head]
	c.release(c.m[k])
	delete(c.m, k)
	c.head = (c.head + 1) % len(c.order)
	c.count--
}

// Reset drops cache ownership, never the core's blob or a reader's/decoder's lease.
func (c *Cache) Reset() {
	c.Lock()
	defer c.Unlock()
	for c.count != 0 {
		c.evict()
	}
	c.retryWake = nil
}

func (c *Cache) bind(s *state.State) {
	if c.owner == s {
		return
	}
	c.Reset()
	c.owner = s
	s.OnChatClose(c, func(*state.State, *state.Account, []byte) { c.Reset() })
	s.OnQuit(c, func(*state.State) { c.Reset() })
	s.OnAppEvent(c, func(_ *state.State, e *app.Event) bool {
		if e.Kind == app.EvWindowClosed {
			c.Reset()
		}
		return false
	})
	s.Listen(c, abi.EventTagAccountRemoved)
}
func (c *Cache) Event(*state.Account, *abi.Event) { c.Reset() }

// Get records this frame's demand and returns a decode lease, or nil while
// pending. Completions admit the newest wanted read and wake the window. Call on UI.
func (c *Cache) Get(s *state.State, a *state.Account, ref abi.BlobRef, limit int) img.Lease {
	if a == nil || a.Conn == nil || ref.Len == 0 || ref.Len > 1<<20 {
		return nil
	}
	c.bind(s)
	c.Lock()
	defer c.Unlock()
	k := key{a.Serial, ref.ID}
	e := c.m[k]
	if e == nil {
		if c.m == nil {
			c.m = make(map[key]*entry)
		}
		if c.count == limit {
			c.evict()
		}
		e = &entry{c: c, refs: 1}
		c.m[k] = e
		c.order[(c.head+c.count)%len(c.order)] = k
		c.count++
	}
	if len(e.data) > 0 {
		e.refs++
		return e
	}
	if e.done || e.running {
		return nil
	}
	c.wanted++
	e.conn, e.ref, e.wanted, e.frame = a.Conn, ref, c.wanted, c.frame
	c.wake = s.Wake
	c.start()
	return nil
}

// start admits the newest request still wanted this frame. The map is already
// bounded; using it avoids a second queue that could retain evicted requests.
// Caller holds Mutex. A completion starts the next read, not the next UI frame.
func (c *Cache) start() {
	if c.readers == Readers {
		c.retryWake = c.wake
		return
	}
	var k key
	var e *entry
	for key, t := range c.m {
		if t.frame != c.frame {
			t.conn = nil
		}
		if t.conn != nil && (e == nil || t.wanted > e.wanted) {
			k, e = key, t
		}
	}
	if e == nil {
		return
	}
	conn, ref := e.conn, e.ref
	for c.bytes+int(ref.Len) > Budget && c.count > 0 {
		c.evict()
	}
	if c.m[k] != e {
		if c.bytes+int(ref.Len) > Budget { // all available bytes are still leased
			c.retryWake = c.wake
			return
		}
		// An older pending request may itself have been evicted before a ready
		// entry freed enough bytes. Admit it now, rather than strand its retry.
		e.refs = 1
		c.m[k] = e
		c.order[(c.head+c.count)%len(c.order)] = k
		c.count++
	}
	e.size, e.running = int(ref.Len), true
	e.conn = nil
	e.refs++
	c.bytes += e.size
	c.readers++ // admission happens before spawning, never waiting goroutines
	go c.read(conn, ref, k, e, c.wake)
}

func (c *Cache) read(conn core.Conn, ref abi.BlobRef, k key, e *entry, wake func()) {
	buf := make([]byte, ref.Len)
	n, err := conn.ReadBlob(ref.ID, 0, buf)
	c.Lock()
	if c.m[k] == e && err == nil && n > 0 && n <= len(buf) {
		e.data = buf[:n]
	}
	e.running, e.done = false, true
	if len(e.data) == 0 {
		c.bytes -= e.size
		e.size = 0
	}
	c.readers--
	c.release(e)
	c.start()
	if c.retryWake != nil {
		wake, c.retryWake = c.retryWake, nil
	}
	c.Unlock()
	if wake != nil {
		wake()
	}
}

// Stats includes evicted buffers still held by readers or decoders.
func (c *Cache) Stats() (entries, bytes, readers int) {
	c.Lock()
	defer c.Unlock()
	return c.count, c.bytes, c.readers
}
func (c *Cache) Contains(account, blob uint64) bool {
	c.Lock()
	defer c.Unlock()
	return c.m[key{account, blob}] != nil
}
