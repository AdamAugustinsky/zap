package model

import (
	"sort"

	"zapfast/abi"
)

// Answer is the core's latest answer of one event tag, kept encoded:
//
//   - for a batched list (StarredList, ChatHits, ChatShared, ChatMedia,
//     SearchHits, Gifs, Stickers, CallLog), the whole list once its last
//     chunk is in: until then the previous answer stays;
//   - for any other tag (Receipts, CallDevices, Call, ForwardLimit,
//     StorageStats, InvitePreview …), the latest event.
//
// Store.Answer(tag) is the tag's. A screen that asked compares the answer's
// key fields (its chat, query or request number, in Event()) with what it
// asked for, as upstream does, and ignores a stale one.
type Answer struct {
	cur, next answerBuf
	batch     uint32
	streaming bool
	gen       uint32
	ev        *abi.Event
	evAt      int
}

type answerBuf struct {
	b     []byte
	ends  []uint32 // chunk i is b[ends[i-1]:ends[i]]
	items []uint32 // items in chunks 0…i
}

func (b *answerBuf) reset() { b.b, b.ends, b.items = b.b[:0], b.ends[:0], b.items[:0] }

// Gen moves whenever a new answer is complete.
func (a *Answer) Gen() uint32 { return a.gen }

// Has reports whether an answer has come.
func (a *Answer) Has() bool { return len(a.cur.ends) > 0 }

// Chunks is the number of events the answer came in (1 for a tag that is
// not batched).
func (a *Answer) Chunks() int { return len(a.cur.ends) }

// Chunk is event i of the answer, decoded into the answer's own event: valid
// until the next Chunk or Item of another chunk, or the next apply. Nil
// when there is no such chunk.
func (a *Answer) Chunk(i int) *abi.Event {
	if i < 0 || i >= len(a.cur.ends) {
		return nil
	}
	if a.ev == nil {
		a.ev = new(abi.Event)
		a.evAt = -1
	}
	if a.evAt != i {
		start := uint32(0)
		if i > 0 {
			start = a.cur.ends[i-1]
		}
		if a.ev.Decode(a.cur.b[start:a.cur.ends[i]]) != nil {
			a.evAt = -1
			return nil
		}
		a.evAt = i
	}
	return a.ev
}

// Event is the answer's first (for most tags, only) event, nil before any.
func (a *Answer) Event() *abi.Event { return a.Chunk(0) }

// Len is the number of items over all the chunks: list entries for a
// batched tag, 1 for another tag's event.
func (a *Answer) Len() int {
	if len(a.cur.items) == 0 {
		return 0
	}
	return int(a.cur.items[len(a.cur.items)-1])
}

// Item finds item k: the chunk holding it (as Chunk) and its index in that
// chunk's list. Nil when k is out of range.
func (a *Answer) Item(k int) (*abi.Event, int) {
	if k < 0 || k >= a.Len() {
		return nil, 0
	}
	i := sort.Search(len(a.cur.items), func(i int) bool { return int(a.cur.items[i]) > k })
	start := 0
	if i > 0 {
		start = int(a.cur.items[i-1])
	}
	return a.Chunk(i), k - start
}

// Clear forgets the answer (a screen that closed, so the next one starts
// empty).
func (a *Answer) Clear() {
	a.cur.reset()
	a.next.reset()
	a.streaming = false
	a.evAt = -1
	a.gen++
}

func (a *Answer) apply(e *abi.Event) {
	batch, last, n, batched := shape(e)
	if !batched || !a.streaming || batch != a.batch {
		a.next.reset()
	}
	a.batch, a.streaming = batch, batched && !last
	start := len(a.next.b)
	var err error
	if a.next.b, err = e.Encode(a.next.b); err != nil {
		a.next.b = a.next.b[:start]
		return
	}
	total := uint32(n)
	if k := len(a.next.items); k > 0 {
		total += a.next.items[k-1]
	}
	a.next.ends = append(a.next.ends, uint32(len(a.next.b)))
	a.next.items = append(a.next.items, total)
	if !a.streaming {
		a.cur, a.next = a.next, a.cur
		a.next.reset()
		a.evAt = -1
		a.gen++
	}
}

// shape is an event's place in a batched list: its batch, whether it is the
// last chunk, how many items it holds; batched is false for an event that
// is whole by itself.
func shape(e *abi.Event) (batch uint32, last bool, n int, batched bool) {
	switch e.Tag {
	case abi.EventTagStarredList:
		x := &e.StarredList
		return x.Batch, x.Last, len(x.Starred), true
	case abi.EventTagChatHits:
		x := &e.ChatHits
		return x.Batch, x.Last, len(x.Messages), true
	case abi.EventTagChatShared:
		x := &e.ChatShared
		return x.Batch, x.Last, len(x.Entries), true
	case abi.EventTagChatMedia:
		x := &e.ChatMedia
		return x.Batch, x.Last, len(x.Items), true
	case abi.EventTagSearchHits:
		x := &e.SearchHits
		return x.Batch, x.Last, len(x.Messages), true
	case abi.EventTagGifs:
		x := &e.Gifs
		return x.Batch, x.Last, len(x.Gifs), true
	case abi.EventTagStickers:
		x := &e.Stickers
		return x.Batch, x.Last, len(x.Entries), true
	case abi.EventTagCallLog:
		x := &e.CallLog
		return x.Batch, x.Last, len(x.Messages), true
	}
	return 0, true, 1, false
}

// answered reports whether the store keeps the tag's events as an Answer.
// The others are modelled field by field (chats, messages, contacts …) or
// carry nothing to keep (PickRequest, OpenPath, the Typing burst).
func answered(t abi.EventTag) bool {
	switch t {
	case abi.EventTagEventDropped, abi.EventTagLink, abi.EventTagMe, abi.EventTagChats, abi.EventTagChatUpdated,
		abi.EventTagMessages, abi.EventTagMessageUpdated, abi.EventTagIncoming, abi.EventTagContacts,
		abi.EventTagTyping, abi.EventTagPresence, abi.EventTagDrafts, abi.EventTagAvatar, abi.EventTagLabels,
		abi.EventTagStars, abi.EventTagStarChanged, abi.EventTagPins, abi.EventTagPinChanged,
		abi.EventTagMessageDeleted, abi.EventTagChatRemoved, abi.EventTagChatCleared, abi.EventTagMedia,
		abi.EventTagMotion, abi.EventTagSyncing, abi.EventTagSyncProgress, abi.EventTagPinLimit,
		abi.EventTagPickRequest, abi.EventTagOpenPath, abi.EventTagAccountRemoved:
		return false
	}
	return true
}
