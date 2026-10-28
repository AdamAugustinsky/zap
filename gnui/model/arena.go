package model

import (
	"bytes"
	"hash/maphash"
	"slices"
	"unsafe"
)

// Str is a range of an arena's bytes: an offset and a length, pointer-free.
type Str struct{ Off, Len uint32 }

// Empty reports whether the range has no bytes.
func (s Str) Empty() bool { return s.Len == 0 }

// arena is a byte arena: rows append their bytes and keep Strs into it.
// Bytes a row no longer uses stay until the owner compacts (live counts the
// bytes still in use).
type arena struct {
	b    []byte
	live int
}

func (a *arena) at(s Str) []byte {
	if s.Len == 0 {
		return nil
	}
	return a.b[s.Off : s.Off+s.Len : s.Off+s.Len]
}

func (a *arena) put(b []byte) Str {
	if len(b) == 0 {
		return Str{}
	}
	off := len(a.b)
	a.b = append(a.b, b...)
	a.live += len(b)
	return Str{uint32(off), uint32(len(b))}
}

func (a *arena) drop(s Str) { a.live -= int(s.Len) }

// reserve makes room for n more bytes, at least doubling the arena when it
// must grow. Left to append, a large slice grows by a quarter at a time,
// so a transcript paged in 250 messages at once (10,000 messages: a 2 MB
// arena) left four times the arena behind as garbage, which the Go heap
// kept as footprint until the scavenger returned it.
func (a *arena) reserve(n int) {
	if cap(a.b)-len(a.b) < n {
		a.b = slices.Grow(a.b, max(n, len(a.b)))
	}
}

// wasteful reports whether at least half the arena is garbage, and enough of
// it to be worth a compaction.
func (a *arena) wasteful() bool { return len(a.b) > 64<<10 && a.live*2 < len(a.b) }

// sub is the range part has inside the arena, when part is a slice of the
// arena's bytes at base (as a record decoded in place from at(base) is).
func (a *arena) sub(base Str, part []byte) Str {
	if len(part) == 0 {
		return Str{}
	}
	start := uintptr(unsafe.Pointer(unsafe.SliceData(a.b)))
	p := uintptr(unsafe.Pointer(unsafe.SliceData(part)))
	off := p - start
	if p < start || off+uintptr(len(part)) > uintptr(base.Off+base.Len) || off < uintptr(base.Off) {
		return Str{}
	}
	return Str{uint32(off), uint32(len(part))}
}

// rebase moves s, a range inside old (a row's previous raw record at
// oldBase), to the same place in the row's raw record now at newBase.
func rebase(s Str, oldBase, newBase Str) Str {
	if s.Len == 0 {
		return s
	}
	return Str{s.Off - oldBase.Off + newBase.Off, s.Len}
}

// S views b as a string without copying. The string is valid as long as b
// is: until the store is next applied, for bytes of a view.
func S(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }

var seed = maphash.MakeSeed()

func hashOf(b []byte) uint64 { return maphash.Bytes(seed, b) }

// index finds rows by their id: open addressing over slots of a hash and a
// row number, pointer-free.
type index struct {
	slots []slot
	n     int
}

type slot struct {
	h   uint64
	row uint32 // row + 1; 0 is empty
}

// reset takes a row count; the table reserves at least two slots per row.
func (x *index) reset(capacity int) {
	size := 16
	for size < capacity*2 {
		size <<= 1
	}
	if cap(x.slots) >= size {
		x.slots = x.slots[:size]
		clear(x.slots)
	} else {
		x.slots = make([]slot, size)
	}
	x.n = 0
}

// keyer gives a row's id.
type keyer interface{ key(row uint32) []byte }

// find returns the row whose id is id, or -1.
func (x *index) find(id []byte, k keyer) int {
	if len(x.slots) == 0 {
		return -1
	}
	h := hashOf(id)
	mask := uint64(len(x.slots) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		s := x.slots[i]
		if s.row == 0 {
			return -1
		}
		if s.h == h && bytes.Equal(k.key(s.row-1), id) {
			return int(s.row - 1)
		}
	}
}

// add records row under id, which must not be in the index yet.
func (x *index) add(id []byte, row uint32) {
	if (x.n+1)*2 > len(x.slots) {
		old := x.slots
		x.slots = nil // reset must not clear the entries we are rehashing
		x.reset(max(x.n+1, 8))
		for _, s := range old {
			if s.row != 0 {
				x.insert(s)
			}
		}
	}
	x.insert(slot{hashOf(id), row + 1})
}

func (x *index) insert(s slot) {
	mask := uint64(len(x.slots) - 1)
	for i := s.h & mask; ; i = (i + 1) & mask {
		if x.slots[i].row == 0 {
			x.slots[i] = s
			x.n++
			return
		}
	}
}

// IDList is a list of ids (chat or message ids) in one arena: what a
// selection, a forward's recipients or pending deletes keep, pointer-free.
type IDList struct {
	b    []byte
	ends []uint32
}

// Len is the number of ids.
func (l *IDList) Len() int { return len(l.ends) }

// At is id i, valid until the list changes.
func (l *IDList) At(i int) []byte {
	start := uint32(0)
	if i > 0 {
		start = l.ends[i-1]
	}
	return l.b[start:l.ends[i]:l.ends[i]]
}

// Index is the position of id, or -1.
func (l *IDList) Index(id []byte) int {
	for i := range l.ends {
		if bytes.Equal(l.At(i), id) {
			return i
		}
	}
	return -1
}

// Has reports whether id is in the list.
func (l *IDList) Has(id []byte) bool { return l.Index(id) >= 0 }

// Add appends id unless it is in the list already.
func (l *IDList) Add(id []byte) {
	if l.Has(id) {
		return
	}
	l.b = append(l.b, id...)
	l.ends = append(l.ends, uint32(len(l.b)))
}

// Remove takes id out of the list, keeping the others' order.
func (l *IDList) Remove(id []byte) {
	i := l.Index(id)
	if i < 0 {
		return
	}
	start := uint32(0)
	if i > 0 {
		start = l.ends[i-1]
	}
	n := l.ends[i] - start
	l.b = append(l.b[:start], l.b[l.ends[i]:]...)
	l.ends = append(l.ends[:i], l.ends[i+1:]...)
	for j := i; j < len(l.ends); j++ {
		l.ends[j] -= n
	}
}

// Toggle adds id, or removes it when it is there.
func (l *IDList) Toggle(id []byte) {
	if l.Has(id) {
		l.Remove(id)
	} else {
		l.Add(id)
	}
}

// Reset empties the list, keeping its memory.
func (l *IDList) Reset() { l.b, l.ends = l.b[:0], l.ends[:0] }
