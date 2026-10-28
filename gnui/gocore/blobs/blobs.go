// Package blobs is the Go backend's blob table (zfcore/src/blobs.rs): bytes
// too large for a record. The UI puts pixels and samples in for a command to
// take (PutBlob), and reads in ranges what an event names (ReadBlob):
// thumbnails, decoded pictures, voice PCM.
//
// Who frees what (zfcore.h, "Memory"):
//
//   - A blob the caller puts stays until a command takes it or the caller
//     frees it. It is never shared or expired.
//   - A blob made for an event is the backend's. The same bytes in several
//     records (a message's thumbnail each time the message is sent again)
//     are one blob under one id. The table keeps the event blobs touched
//     last (made, named again or read) up to the event budget, and frees
//     older ones itself (those over 1 MiB first), never one touched within
//     the grace. The caller may free one early once done with the record
//     that named it; an id named by several records goes when each has
//     been freed, or when it expires.
//
// Caller and event blobs share one id space, from 1 up.
package blobs

import (
	"bytes"
	"errors"
	"hash/maphash"
	"slices"
	"sync"
	"time"

	"zapfast/abi"
)

const (
	// EventBudget is how many bytes of event blobs are kept before the
	// oldest are freed.
	EventBudget = 64 << 20
	// Grace is how long an event blob is kept at least after it was last
	// touched.
	Grace = 30 * time.Second
	// SharedMax is the largest event blob shared by content; a larger one
	// also goes first when the budget is exceeded.
	SharedMax = 1 << 20
)

var (
	// ErrNotFound: no blob has the id (never made, taken, freed or expired).
	ErrNotFound = errors.New("blobs: no such blob")
	// ErrRange: an offset past the blob's end, or (Take) a length that is
	// not the blob's.
	ErrRange = errors.New("blobs: past the blob's end, or not its length")
)

type held struct {
	bytes []byte
	event bool   // made for events (false: put by the caller, never expired)
	refs  uint32 // records naming an event blob that have not freed it
	at    time.Time
	tick  uint64 // touch order
	key   uint64 // content key of a shared event blob
	keyed bool
}

// Table holds one Conn's blobs. Its methods are safe from any goroutine.
type Table struct {
	mu         sync.Mutex
	held       map[uint64]*held
	byKey      map[uint64]uint64 // shared event blobs by content key
	eventBytes int
	tick       uint64
	next       uint64
	seed       maphash.Seed

	budget int
	grace  time.Duration
	now    func() time.Time
}

// New returns a table with EventBudget and Grace.
func New() *Table { return WithLimits(EventBudget, Grace) }

// WithLimits returns a table that keeps budget bytes of event blobs, each
// at least grace after it was last touched.
func WithLimits(budget int, grace time.Duration) *Table {
	return &Table{
		held: map[uint64]*held{}, byKey: map[uint64]uint64{}, seed: maphash.MakeSeed(),
		budget: budget, grace: grace, now: time.Now,
	}
}

func (t *Table) insert(b []byte, event bool, key uint64, keyed bool) abi.BlobRef {
	t.next++
	t.tick++
	id := t.next
	t.held[id] = &held{bytes: b, event: event, refs: 1, at: t.now(), tick: t.tick, key: key, keyed: keyed}
	if event {
		t.eventBytes += len(b)
	}
	if keyed {
		t.byKey[key] = id
	}
	return abi.BlobRef{ID: id, Len: uint64(len(b))}
}

func (t *Table) touch(id uint64, h *held) {
	t.tick++
	h.at, h.tick = t.now(), t.tick
}

func (t *Table) remove(id uint64) *held {
	h := t.held[id]
	if h == nil {
		return nil
	}
	delete(t.held, id)
	if h.event {
		t.eventBytes -= len(h.bytes)
	}
	if h.keyed {
		delete(t.byKey, h.key)
	}
	return h
}

// Put keeps a copy of src for a command to take (zf_blob_put).
func (t *Table) Put(src []byte) abi.BlobRef {
	return t.Keep(bytes.Clone(src))
}

// Keep is Put without the copy: the table owns b from now on.
func (t *Table) Keep(b []byte) abi.BlobRef {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.insert(b, false, 0, false)
}

// PutEvent keeps b for an event record, and owns it from now on: when a
// blob already holds the same bytes it is named again under its id (and b
// is not kept). Past the budget, older event blobs are freed.
func (t *Table) PutEvent(b []byte) abi.BlobRef {
	t.mu.Lock()
	defer t.mu.Unlock()
	var key uint64
	keyed := len(b) <= SharedMax
	if keyed {
		key = maphash.Bytes(t.seed, b)
		if id, ok := t.byKey[key]; ok {
			if h := t.held[id]; h != nil && bytes.Equal(h.bytes, b) {
				h.refs++
				t.touch(id, h)
				return abi.BlobRef{ID: id, Len: uint64(len(h.bytes))}
			}
			// Another blob under the same key keeps it; this one goes
			// unshared.
			keyed = false
		}
	}
	ref := t.insert(b, true, key, keyed)
	t.expire()
	return ref
}

// expire frees event blobs until those kept are a quarter under the
// budget, sparing any touched within the grace: first the large ones
// (decoded pictures and sound, read once when their event arrives), then
// the rest, each kind touched longest ago first.
func (t *Table) expire() {
	if t.eventBytes <= t.budget {
		return
	}
	type old struct {
		small bool
		tick  uint64
		id    uint64
	}
	var olds []old
	now := t.now()
	for id, h := range t.held {
		if h.event && now.Sub(h.at) >= t.grace {
			olds = append(olds, old{len(h.bytes) <= SharedMax, h.tick, id})
		}
	}
	slices.SortFunc(olds, func(a, b old) int {
		switch {
		case a.small != b.small:
			if b.small {
				return -1
			}
			return 1
		case a.tick < b.tick:
			return -1
		case a.tick > b.tick:
			return 1
		}
		return 0
	})
	target := t.budget - t.budget/4
	for _, o := range olds {
		if t.eventBytes <= target {
			break
		}
		t.remove(o.id)
	}
}

// Read copies the blob's bytes from off into dst and returns how many: 0
// at its end (zf_blob_read). It touches the blob.
func (t *Table) Read(id, off uint64, dst []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.held[id]
	if h == nil {
		return 0, ErrNotFound
	}
	if off > uint64(len(h.bytes)) {
		return 0, ErrRange
	}
	n := copy(dst, h.bytes[off:])
	t.touch(id, h)
	return n, nil
}

// Len is the blob's length, and whether it is held.
func (t *Table) Len(id uint64) (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.held[id]
	if h == nil {
		return 0, false
	}
	return uint64(len(h.bytes)), true
}

// Has reports whether ref names a blob held at that length: what Take
// would take.
func (t *Table) Has(ref abi.BlobRef) bool {
	n, ok := t.Len(ref.ID)
	return ok && n == ref.Len
}

// Take removes the blob a command names and returns its bytes; ref.Len is
// the length the command says it has. ErrRange: held at another length
// (and left in place).
func (t *Table) Take(ref abi.BlobRef) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.held[ref.ID]
	if h == nil {
		return nil, ErrNotFound
	}
	if uint64(len(h.bytes)) != ref.Len {
		return nil, ErrRange
	}
	t.remove(ref.ID)
	return h.bytes, nil
}

// Free frees a caller's blob, or one record's hold on an event blob. An
// unknown id is ignored.
func (t *Table) Free(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.held[id]
	if h == nil {
		return
	}
	if h.event && h.refs > 1 {
		h.refs--
		return
	}
	t.remove(id)
}

// Count is how many blobs are held.
func (t *Table) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.held)
}

// EventBytes is how many bytes of event blobs are held.
func (t *Table) EventBytes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.eventBytes
}

// Clear frees every blob: the Conn closes.
func (t *Table) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	clear(t.held)
	clear(t.byKey)
	t.eventBytes = 0
}
