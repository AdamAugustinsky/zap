package out

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"zapfast/abi"
	"zapfast/gocore/blobs"
)

// sink is a Queue that keeps what it is given.
type sink struct {
	recs   [][]byte
	closed bool
}

func (s *sink) Push(rec []byte) bool {
	if s.closed {
		return false
	}
	s.recs = append(s.recs, rec)
	return true
}

// events decodes what was queued; each event's bytes point into its own
// record, which the sink keeps.
func (s *sink) events(t *testing.T) []*abi.Event {
	t.Helper()
	var evs []*abi.Event
	for i, rec := range s.recs {
		if len(rec) > abi.MaxRecord {
			t.Fatalf("record %d is %d bytes, over MaxRecord", i, len(rec))
		}
		e := &abi.Event{}
		if err := e.Decode(rec); err != nil {
			t.Fatalf("record %d does not decode: %v", i, err)
		}
		evs = append(evs, e)
	}
	return evs
}

func newOut() (*Out, *sink, *blobs.Table) {
	s := &sink{}
	b := blobs.New()
	return New(s, b), s, b
}

func chatsRecord(batch uint32, last bool, chats []abi.Chat) *abi.Event {
	return &abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: batch, Last: last, Chats: chats}}
}

func chat(i int, name string) abi.Chat {
	return abi.Chat{ID: []byte(fmt.Sprintf("%d@s.whatsapp.net", i)), Name: []byte(name)}
}

// checkBatch checks events are one batch of tag with last on the final
// one only, and returns the size of each chunk.
func checkBatch(t *testing.T, evs []*abi.Event, tag abi.EventTag, batch func(*abi.Event) (uint32, bool, int)) []int {
	t.Helper()
	var sizes []int
	var id uint32
	for i, e := range evs {
		if e.Tag != tag {
			t.Fatalf("record %d is %v, want %v", i, e.Tag, tag)
		}
		b, last, n := batch(e)
		if i == 0 {
			id = b
		}
		if b != id {
			t.Errorf("record %d in batch %d, want %d", i, b, id)
		}
		if last != (i == len(evs)-1) {
			t.Errorf("record %d of %d: last %v", i, len(evs), last)
		}
		sizes = append(sizes, n)
	}
	return sizes
}

func TestAThousandChatsGoInChunksOf256UnderOneBatch(t *testing.T) {
	o, s, _ := newOut()
	o.Batch() // batch ids need not start at 1
	chats := make([]abi.Chat, 1000)
	for i := range chats {
		chats[i] = chat(i, "Chat "+strconv.Itoa(i))
	}
	List(o, chats, chatsRecord)
	evs := s.events(t)
	sizes := checkBatch(t, evs, abi.EventTagChats, func(e *abi.Event) (uint32, bool, int) {
		return e.Chats.Batch, e.Chats.Last, len(e.Chats.Chats)
	})
	if fmt.Sprint(sizes) != "[256 256 256 232]" {
		t.Errorf("chunks %v", sizes)
	}
	if evs[0].Chats.Batch != 2 {
		t.Errorf("batch %d, want a new one (2)", evs[0].Chats.Batch)
	}
	n := 0
	for _, e := range evs {
		for _, c := range e.Chats.Chats {
			if want := "Chat " + strconv.Itoa(n); string(c.Name) != want {
				t.Fatalf("chat %d is %q, want %q", n, c.Name, want)
			}
			n++
		}
	}
}

func TestLongChatsSplitAtTheByteBudget(t *testing.T) {
	o, s, _ := newOut()
	name := strings.Repeat("n", 1000)
	chats := make([]abi.Chat, 1000)
	for i := range chats {
		chats[i] = chat(i, name)
		for range 16 {
			chats[i].Labels = append(chats[i].Labels, bytes.Repeat([]byte{'l'}, 64))
		}
	}
	List(o, chats, chatsRecord)
	evs := s.events(t)
	sizes := checkBatch(t, evs, abi.EventTagChats, func(e *abi.Event) (uint32, bool, int) {
		return e.Chats.Batch, e.Chats.Last, len(e.Chats.Chats)
	})
	total := 0
	for i, n := range sizes {
		total += n
		if n >= 256 && i < len(sizes)-1 {
			t.Errorf("chunk %d has %d chats: the byte budget should split first", i, n)
		}
	}
	if total != 1000 {
		t.Errorf("%d chats, want 1000", total)
	}
	for i, rec := range s.recs {
		if len(rec) > 512<<10+64 {
			t.Errorf("record %d is %d bytes", i, len(rec))
		}
	}
}

func messagesRecord(complete bool) func(uint32, bool, []abi.Message) *abi.Event {
	return func(batch uint32, last bool, ms []abi.Message) *abi.Event {
		return &abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{
			Chat: []byte("1@s.whatsapp.net"), Batch: batch, Last: last, Complete: complete && last, Messages: ms,
		}}
	}
}

type row struct {
	id    string
	text  int    // bytes of text
	thumb []byte // a thumbnail, made into an event blob
	bad   bool   // a sender over its bound
}

func message(r *Record, x row) abi.Message {
	m := abi.Message{ID: []byte(x.id), Chat: []byte("1@s.whatsapp.net"), Sender: []byte("me")}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = bytes.Repeat([]byte{'t'}, x.text)
	if x.thumb != nil {
		m.Thumbnail, m.HasThumbnail = r.Blob(x.thumb), true
	}
	if x.bad {
		m.Sender = bytes.Repeat([]byte{'s'}, 200)
	}
	return m
}

func TestLargeMessagesSplitAtTheirByteBudgetAndCompleteComesLast(t *testing.T) {
	o, s, _ := newOut()
	rows := make([]row, 10)
	for i := range rows {
		rows[i] = row{id: strconv.Itoa(i), text: 150_000}
	}
	Chunks(o, rows, message, messagesRecord(true))
	evs := s.events(t)
	sizes := checkBatch(t, evs, abi.EventTagMessages, func(e *abi.Event) (uint32, bool, int) {
		return e.Messages.Batch, e.Messages.Last, len(e.Messages.Messages)
	})
	if fmt.Sprint(sizes) != "[3 3 3 1]" {
		t.Errorf("chunks %v, want 3 messages of 150 kB to each 576 KiB", sizes)
	}
	for i, e := range evs {
		if e.Messages.Complete != (i == len(evs)-1) {
			t.Errorf("record %d: complete %v", i, e.Messages.Complete)
		}
	}
}

func TestAnElementOverItsBoundIsDroppedAloneAndItsBlobsFreed(t *testing.T) {
	o, s, b := newOut()
	rows := []row{
		{id: "a", thumb: []byte("thumb a")},
		{id: "b", thumb: []byte("thumb b"), bad: true},
		{id: "c", thumb: []byte("thumb c")},
		{id: "d", text: 600_000 / 4, thumb: []byte("thumb a")}, // fits
	}
	Chunks(o, rows, message, messagesRecord(false))
	evs := s.events(t)
	if len(evs) != 2 {
		t.Fatalf("%d records, want EventDropped then the batch", len(evs))
	}
	d := evs[0]
	if d.Tag != abi.EventTagEventDropped || d.EventDropped.Kind != uint16(abi.EventTagMessages) || d.EventDropped.Size == 0 {
		t.Fatalf("first record %v %+v, want EventDropped of Messages", d.Tag, d.EventDropped)
	}
	var ids []string
	for _, m := range evs[1].Messages.Messages {
		ids = append(ids, string(m.ID))
	}
	if fmt.Sprint(ids) != "[a c d]" {
		t.Errorf("messages %v", ids)
	}
	// a and d share a's thumbnail; b's went with it.
	if b.Count() != 2 {
		t.Errorf("%d blobs held, want 2 (a's shared, c's)", b.Count())
	}
	ms := evs[1].Messages.Messages
	if ms[0].Thumbnail != ms[2].Thumbnail {
		t.Error("the same thumbnail is not one blob")
	}
}

func TestAnElementOverTheByteBudgetAloneIsDropped(t *testing.T) {
	// No field of this message is over its bound, but under a budget of
	// 100 kB (the schema's are 512 and 576 KiB) it is too large alone.
	saved := limits[abi.EventTagMessages]
	limits[abi.EventTagMessages] = Limit{256, 100_000}
	defer func() { limits[abi.EventTagMessages] = saved }()
	o, s, _ := newOut()
	rows := []row{{id: "a", text: 10}, {id: "big", text: 150_000}, {id: "c", text: 60_000}, {id: "d", text: 60_000}}
	Chunks(o, rows, message, messagesRecord(false))
	evs := s.events(t)
	if len(evs) != 3 || evs[0].Tag != abi.EventTagEventDropped || evs[0].EventDropped.Size <= 150_000 {
		t.Fatalf("%d records, the first %v %+v", len(evs), evs[0].Tag, evs[0].EventDropped)
	}
	sizes := checkBatch(t, evs[1:], abi.EventTagMessages, func(e *abi.Event) (uint32, bool, int) {
		return e.Messages.Batch, e.Messages.Last, len(e.Messages.Messages)
	})
	if fmt.Sprint(sizes) != "[2 1]" {
		t.Errorf("chunks %v, want a and c, then d", sizes)
	}
}

func TestAnEmptyListIsOneLastRecord(t *testing.T) {
	o, s, _ := newOut()
	List(o, nil, chatsRecord)
	evs := s.events(t)
	if len(evs) != 1 || !evs[0].Chats.Last || len(evs[0].Chats.Chats) != 0 {
		t.Fatalf("%d records", len(evs))
	}
}

func TestARecordWhoseOwnFieldsAreOverTheirBoundsDropsTheBatch(t *testing.T) {
	o, s, _ := newOut()
	rec := func(batch uint32, last bool, ms []abi.Message) *abi.Event {
		e := messagesRecord(false)(batch, last, ms)
		e.Messages.Chat = bytes.Repeat([]byte{'c'}, 129)
		return e
	}
	Chunks(o, []row{{id: "a", thumb: []byte("x")}}, message, rec)
	evs := s.events(t)
	if len(evs) != 1 || evs[0].Tag != abi.EventTagEventDropped {
		t.Fatalf("records %d", len(evs))
	}
}

func TestARecordOverABoundBecomesEventDroppedAndFreesItsBlobs(t *testing.T) {
	o, s, b := newOut()
	ev := abi.Event{Tag: abi.EventTagTyping}
	ev.Typing.Chat = bytes.Repeat([]byte{'c'}, 129)
	if o.Emit(&ev) {
		t.Error("an over-long chat id was queued")
	}
	r := o.Record()
	m := abi.Event{Tag: abi.EventTagMessageUpdated}
	m.MessageUpdated.Message = message(r, row{id: "x", thumb: []byte("thumb"), bad: true})
	if r.Emit(&m) {
		t.Error("an over-long sender was queued")
	}
	if b.Count() != 0 {
		t.Errorf("%d blobs left by the dropped record", b.Count())
	}
	m.MessageUpdated.Message = message(r, row{id: "y", thumb: []byte("thumb")})
	if !r.Emit(&m) {
		t.Error("a good record was dropped")
	}
	evs := s.events(t)
	if len(evs) != 3 || evs[0].EventDropped.Kind != uint16(abi.EventTagTyping) || evs[1].EventDropped.Kind != uint16(abi.EventTagMessageUpdated) {
		t.Fatalf("records %d", len(evs))
	}
	if b.Count() != 1 {
		t.Errorf("%d blobs, want the good record's", b.Count())
	}
	// Once closed, nothing is queued and a record's blobs are freed.
	s.closed = true
	m.MessageUpdated.Message = message(r, row{id: "z", thumb: []byte("another")})
	if r.Emit(&m) {
		t.Error("queued after close")
	}
	if b.Count() != 1 {
		t.Errorf("%d blobs after a refused push", b.Count())
	}
}

func TestChunksPanicsForARecordWithNoList(t *testing.T) {
	o, _, _ := newOut()
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	List(o, []abi.Chat{{}}, func(uint32, bool, []abi.Chat) *abi.Event { return &abi.Event{Tag: abi.EventTagTyping} })
}

// The chunk limits are the schema's: one per chunk(N, B) list, and none
// that the schema does not have.
func TestLimitsMatchTheSchema(t *testing.T) {
	schema, err := os.ReadFile("../../../zfcore/abi/zf.schema")
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]abi.EventTag{}
	for i := range 1 << 10 {
		if name := abi.EventTag(i).String(); name != "unknown" {
			tags[name] = abi.EventTag(i)
		}
	}
	event := regexp.MustCompile(`^event (\w+) = \d+`)
	chunk := regexp.MustCompile(`^\s+\w+: chunk\((\d+), (\d+)\)`)
	found := map[abi.EventTag]Limit{}
	var current string
	for line := range strings.Lines(string(schema)) {
		if m := event.FindStringSubmatch(line); m != nil {
			current = m[1]
		} else if strings.HasPrefix(line, "}") {
			current = ""
		}
		if m := chunk.FindStringSubmatch(line); m != nil && current != "" {
			n, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			tag, ok := tags[current]
			if !ok {
				t.Fatalf("the schema's %s is not in package abi", current)
			}
			if _, twice := found[tag]; twice {
				t.Fatalf("%s has two chunked lists", current)
			}
			found[tag] = Limit{n, b}
		}
	}
	if len(found) < 10 {
		t.Fatalf("found only %d chunked lists: the parse is wrong", len(found))
	}
	for tag, l := range found {
		if got, ok := Limits(tag); !ok || got != l {
			t.Errorf("%v: limits %v, the schema says %v", tag, got, l)
		}
	}
	for tag := range limits {
		if _, ok := found[tag]; !ok {
			t.Errorf("%v has limits but no chunked list in the schema", tag)
		}
	}
}

func TestClip(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"hello world", 9, "hello…"},             // "hello " fits; its space is trimmed
		{"abcdefghij", 6, "abc…"},                // 3 + 3
		{"e\u0301e\u0301e\u0301", 8, "e\u0301…"}, // e and a combining accent: never split
		{"👨‍👩‍👧 family", 21, "👨‍👩‍👧…"},           // the ZWJ sequence is one grapheme of 18 bytes
		{"👨‍👩‍👧 family", 10, "…"},
		{"abc\xffdef", 100, "abc\uFFFDdef"}, // invalid UTF-8 replaced
		{"abcdef", 2, "ab"},                 // no room for the ellipsis
	} {
		got := Clip(c.in, c.max)
		if got != c.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if len(got) > c.max || !utf8.ValidString(got) {
			t.Errorf("Clip(%q, %d) = %q: over the bound or invalid", c.in, c.max, got)
		}
	}
	long := strings.Repeat("ab ", 3000)
	if got := Toast(long); len(got) > ToastMax || !strings.HasSuffix(got, ellipsis) {
		t.Errorf("Toast: %d bytes", len(got))
	}
}
