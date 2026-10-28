package calls

import (
	"bytes"
	"sort"

	"zapfast/abi"
	"zapfast/model"
)

// LinkChat is the chat a call joined through a link is filed under: it
// belongs to no chat (zf:src/model.rs:780).
const LinkChat = "zapfast-call-link"

// span is a run of bytes in an arena.
type span struct{ off, n uint32 }

func (s span) of(b []byte) []byte { return b[s.off : s.off+s.n : s.off+s.n] }

func put(b *[]byte, v []byte) span {
	off := len(*b)
	*b = append(*b, v...)
	return span{uint32(off), uint32(len(v))}
}

// entry is one logged call (zf:src/ui/calls.rs:21): pointer-free, its ids
// in the log's arena.
type entry struct {
	chat, id span
	ts       int64
	duration uint64
	status   abi.CallStatus
	fromMe   bool
	video    bool
	group    bool
}

// missed is whether this was a call to us that nobody took.
func (e *entry) missed() bool { return e.status == abi.CallStatusMissed && !e.fromMe }

// bad is whether the call never connected (zf:src/model.rs:812).
func (e *entry) bad() bool {
	return e.status != abi.CallStatusAnswered && e.status != abi.CallStatusAnsweredElsewhere
}

// callLog is an account's call history, newest first, as the core sent it
// last (CallLog, in batches of one id; the last one completes it).
type callLog struct {
	b   []byte
	e   []entry
	gen uint32
	// loaded: a whole history arrived (or the core cannot send one).
	loaded bool

	pb    []byte
	pe    []entry
	batch uint32
	open  bool
}

// add takes one CallLog event.
func (l *callLog) add(ev *abi.EventCallLog) {
	if !l.open || ev.Batch != l.batch {
		l.pb, l.pe, l.batch, l.open = l.pb[:0], l.pe[:0], ev.Batch, true
	}
	for i := range ev.Messages {
		m := &ev.Messages[i]
		if m.Content.Tag != abi.ContentTagCall {
			continue
		}
		c := &m.Content.Call
		l.pe = append(l.pe, entry{chat: put(&l.pb, m.Chat), id: put(&l.pb, m.ID), ts: m.Timestamp,
			duration: c.Duration, status: c.Status, fromMe: m.FromMe, video: c.Video, group: c.Group})
	}
	if !ev.Last {
		return
	}
	l.b, l.pb = l.pb, l.b
	l.e, l.pe = l.pe, l.e
	l.open = false
	sort.SliceStable(l.e, func(i, j int) bool { return l.e[i].ts > l.e[j].ts })
	l.loaded = true
	l.gen++
}

// chat is entry i's chat id.
func (l *callLog) chat(i int) []byte { return l.e[i].chat.of(l.b) }

// id is entry i's message id.
func (l *callLog) id(i int) []byte { return l.e[i].id.of(l.b) }

// row is a run of alike calls the history shows as one row: consecutive
// calls with the same chat that went the same way and ended alike
// (zf:src/ui/calls.rs:130). first is its newest.
type row struct {
	first, n int
	// The row's words, in the page's row arena: its name (the count after
	// it when n > 1), the folded name for search, the summary and the stamp.
	name, label, key, summary, stamp span
}

// fold appends the history's rows to dst, skipping the calls hidden while
// hidden(chat) says so (a locked chat's, while its folder is closed).
func fold(dst []row, l *callLog, hidden func(chat []byte) bool) []row {
	var last *entry
	var lastChat []byte
	for i := range l.e {
		e := &l.e[i]
		chat := l.chat(i)
		if hidden != nil && hidden(chat) {
			continue
		}
		if last != nil && bytes.Equal(lastChat, chat) && last.fromMe == e.fromMe && last.missed() == e.missed() && last.group == e.group {
			dst[len(dst)-1].n++
		} else {
			dst = append(dst, row{first: i, n: 1})
		}
		last, lastChat = e, chat
	}
	return dst
}

// frequentCalls is how many people the new-call list offers as frequently
// contacted.
const frequentCalls = 3

// frequent appends to dst the indexes in l of the calls naming the people
// called most often, most first, the newer call breaking a tie
// (zf:src/ui/calls.rs:481). Groups, ourselves, call links and the chats
// skip(chat) refuses stay out.
func frequent(dst []int, l *callLog, me []byte, skip func(chat []byte) bool) []int {
	type count struct {
		i, n   int
		newest int64
	}
	var counts [64]count
	k := 0
	for i := range l.e {
		chat := l.chat(i)
		if model.Phone(chat) == nil || bytes.Equal(chat, me) || (skip != nil && skip(chat)) {
			continue
		}
		found := false
		for j := 0; j < k; j++ {
			if bytes.Equal(l.chat(counts[j].i), chat) {
				counts[j].n++
				counts[j].newest = max(counts[j].newest, l.e[i].ts)
				found = true
				break
			}
		}
		if !found && k < len(counts) {
			counts[k] = count{i: i, n: 1, newest: l.e[i].ts}
			k++
		}
	}
	for n := 0; n < frequentCalls; n++ {
		best := -1
		for j := 0; j < k; j++ {
			if counts[j].n == 0 {
				continue
			}
			if best < 0 || counts[j].n > counts[best].n || (counts[j].n == counts[best].n && counts[j].newest > counts[best].newest) {
				best = j
			}
		}
		if best < 0 {
			break
		}
		dst = append(dst, counts[best].i)
		counts[best].n = 0
	}
	return dst
}
