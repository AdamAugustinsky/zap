// Package out is how the Go backend's engines emit events: typed abi.Event
// values, encoded with the same bounds as zfcore's (zfcore/src/dto.rs, Out).
//
//   - A record is encoded with abi.(*Event).Encode, which refuses a field
//     over its schema bound, and queued as the bytes Drain decodes, so every
//     bound the schema states holds for the Go backend too.
//   - A record that does not encode is dropped: an EventDropped naming its
//     tag goes in its place, and the event blobs it made are freed.
//   - A list the schema declares as chunk(N, B) goes out in records of at
//     most N elements and B encoded bytes (Limits), all under one batch id,
//     the last with last set; an element too large on its own is dropped by
//     itself (Chunks, List).
//   - Display text over its bound is cut at a grapheme boundary with an
//     ellipsis (Clip, Toast); ids, paths, URLs and message bodies never are.
package out

import (
	"log/slog"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"gonativeui/text"

	"zapfast/abi"
	"zapfast/gocore/blobs"
)

// Queue receives encoded records: the Conn's event queue.
type Queue interface {
	// Push queues one record, which it owns from now on, and reports
	// whether it was queued: false once the Conn is closed.
	Push(rec []byte) bool
}

// Out emits records into a queue. Its methods are safe from any goroutine;
// a Record is not.
type Out struct {
	q       Queue
	blobs   *blobs.Table
	batches atomic.Uint32
}

// New returns an Out over q, whose event blobs go into b.
func New(q Queue, b *blobs.Table) *Out { return &Out{q: q, blobs: b} }

// Blobs is the table event blobs go into.
func (o *Out) Blobs() *blobs.Table { return o.blobs }

// Batch returns a new batch id, from 1 up. Chunks takes its own; a caller
// needs one only to build chunked records by hand.
func (o *Out) Batch() uint32 { return o.batches.Add(1) }

// Emit queues e, a record that names no blob made for it (use a Record for
// one that does), and reports whether it was queued. One that does not
// encode is dropped as EventDropped.
func (o *Out) Emit(e *abi.Event) bool { return o.send(e, nil) }

// Record starts a record whose event blobs are freed again if it is
// dropped. A Record may be reused once emitted; it is for one goroutine.
func (o *Out) Record() *Record { return &Record{o: o} }

// send encodes and queues e; made are the event blobs it names, freed when
// it is dropped or cannot be queued.
func (o *Out) send(e *abi.Event, made []uint64) bool {
	rec, err := e.Encode(nil)
	if err != nil {
		o.free(made)
		o.dropped(e.Tag, len(rec), err)
		return false
	}
	if !o.q.Push(rec) {
		o.free(made)
		return false
	}
	return true
}

func (o *Out) free(ids []uint64) {
	for _, id := range ids {
		o.blobs.Free(id)
	}
}

// dropped queues an EventDropped for a record (or chunk element) of tag
// that came to size bytes and did not encode.
func (o *Out) dropped(tag abi.EventTag, size int, err error) {
	// The variant and the reason, never the content.
	slog.Warn("zapfast gocore: dropped a record", "tag", tag.String(), "size", size, "err", err)
	d := abi.Event{Tag: abi.EventTagEventDropped}
	d.EventDropped = abi.EventEventDropped{Kind: uint16(tag), Size: uint64(size)}
	rec, err := d.Encode(nil)
	if err != nil {
		panic("gocore/out: EventDropped does not encode: " + err.Error()) // a few fixed-width bytes
	}
	o.q.Push(rec)
}

// Record is one record being built, and the event blobs made for it.
type Record struct {
	o    *Out
	made []uint64
}

// Blob keeps b (owned by the table from now on) as an event blob for this
// record: shared with any blob of the same bytes, and freed again if the
// record is dropped.
func (r *Record) Blob(b []byte) abi.BlobRef {
	ref := r.o.blobs.PutEvent(b)
	r.made = append(r.made, ref.ID)
	return ref
}

// Emit queues e, which names the blobs made with Blob since the last Emit,
// and reports whether it was queued: dropped (EventDropped), its blobs are
// freed.
func (r *Record) Emit(e *abi.Event) bool {
	ok := r.o.send(e, r.made)
	r.made = r.made[:0]
	return ok
}

// Limit is a chunked list's bounds: at most Count elements and Bytes
// encoded bytes of them in one record.
type Limit struct{ Count, Bytes int }

// limits are the schema's chunk(N, B) lists, by event (one each); a test
// checks them against zfcore/abi/zf.schema.
var limits = map[abi.EventTag]Limit{
	abi.EventTagChats:       {256, 512 << 10},
	abi.EventTagMessages:    {256, 576 << 10},
	abi.EventTagContacts:    {4096, 512 << 10},
	abi.EventTagDrafts:      {4096, 512 << 10},
	abi.EventTagLabels:      {1024, 512 << 10},
	abi.EventTagStars:       {4096, 512 << 10},
	abi.EventTagStarredList: {256, 576 << 10},
	abi.EventTagChatHits:    {256, 576 << 10},
	abi.EventTagChatShared:  {256, 576 << 10},
	abi.EventTagChatMedia:   {1024, 512 << 10},
	abi.EventTagPins:        {8, 512 << 10},
	abi.EventTagSearchHits:  {256, 576 << 10},
	abi.EventTagGifs:        {256, 512 << 10},
	abi.EventTagStickers:    {4096, 512 << 10},
	abi.EventTagCallLog:     {256, 576 << 10},
}

// Limits returns the bounds of the chunked list of events with tag, and
// whether they have one.
func Limits(tag abi.EventTag) (Limit, bool) {
	l, ok := limits[tag]
	return l, ok
}

// List is Chunks of elements that need no conversion.
func List[U any](o *Out, items []U, record func(batch uint32, last bool, chunk []U) *abi.Event) {
	Chunks(o, items, func(_ *Record, u U) U { return u }, record)
}

// Chunks emits items as one batch of chunked records, as zfcore's
// Out::chunked does. convert turns each item into the list's element (it
// may make event blobs with r); record builds the record of one chunk with
// the batch id, whether it is the last, and its elements, and is called
// with no elements to learn the record's tag and size. Chunks splits the
// list at the Limits of that tag: an element that does not encode alone,
// or is over the byte bound alone, is dropped by itself (EventDropped) and
// its blobs freed. The last record, empty when items is, carries last; a
// flag that only the whole batch can state (Messages.Complete,
// ChatHits.Truncated) belongs on that one, so record sets it only when last
// is true.
//
// It panics when the record has no chunked list: a mistake in the engine.
func Chunks[T, U any](o *Out, items []T, convert func(r *Record, item T) U, record func(batch uint32, last bool, chunk []U) *abi.Event) {
	batch := o.Batch()
	probe := record(batch, true, nil)
	lim, ok := Limits(probe.Tag)
	if !ok {
		panic("gocore/out: " + probe.Tag.String() + " has no chunked list")
	}
	scratch, err := probe.Encode(nil)
	if err != nil {
		// The record's own fields are over their bounds: no chunk of it
		// can be sent. One EventDropped stands for the batch.
		o.dropped(probe.Tag, len(scratch), err)
		return
	}
	base := len(scratch)

	r := o.Record()
	var chunk []U
	var chunkMade []uint64 // the blobs of the elements in chunk
	size := 0
	one := make([]U, 1)
	for _, item := range items {
		el := convert(r, item)
		elMade := r.made
		r.made = nil
		// Encoded alone first, so a field over its bound costs this
		// element and not the chunk around it.
		one[0] = el
		enc, err := record(batch, true, one).Encode(scratch[:0])
		scratch = enc
		n := len(enc) - base
		if err == nil && n > lim.Bytes {
			err = abi.ErrTooLarge
		}
		if err != nil {
			o.free(elMade)
			o.dropped(probe.Tag, n, err)
			continue
		}
		if len(chunk) > 0 && (len(chunk) == lim.Count || size+n > lim.Bytes) {
			o.send(record(batch, false, chunk), chunkMade)
			chunk, chunkMade, size = chunk[:0], chunkMade[:0], 0
		}
		size += n
		chunk = append(chunk, el)
		chunkMade = append(chunkMade, elMade...)
	}
	var zero U
	one[0] = zero
	o.send(record(batch, true, chunk), chunkMade)
}

const ellipsis = "…"

// Clip cuts display text to at most max bytes: at a grapheme boundary, the
// trailing space trimmed, with an ellipsis, as zfcore's clip does. Only for
// text a person reads and nothing parses (summaries, names, errors); ids,
// paths, URLs and message bodies are never cut. Invalid UTF-8 becomes
// U+FFFD, so the result always encodes.
func Clip(s string, max int) string {
	if len(s) <= max && utf8.ValidString(s) {
		return s
	}
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= max {
		return s
	}
	room := max - len(ellipsis)
	if room < 0 {
		// No room for the ellipsis: whole runes only.
		end := 0
		for i := range s {
			if i > max {
				break
			}
			end = i
		}
		return s[:end]
	}
	// GraphemeStart(b, room) is the last boundary at or before room, and
	// the boundary rules look past it, so the text after room counts.
	end := text.GraphemeStart([]byte(s[:min(len(s), room+utf8.UTFMax*8)]), room)
	return strings.TrimRightFunc(s[:end], unicode.IsSpace) + ellipsis
}

// ToastMax is the bound of the Info and Error records' text, and of the
// other records' error strings.
const ToastMax = 4096

// Toast is Clip of an error or notice to ToastMax.
func Toast(s string) string { return Clip(s, ToastMax) }
