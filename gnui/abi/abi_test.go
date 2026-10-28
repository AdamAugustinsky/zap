package abi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"testing"
	"weak"
)

// The fixtures are written by zfcore's Rust tests
// (fixtures_round_trip_and_are_current): every variant, over enough rounds
// that each union variant appears.
func readFrames(t testing.TB, name string) [][]byte {
	t.Helper()
	buf, err := os.ReadFile("../../zfcore/abi/fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for len(buf) > 0 {
		rec, rest, err := NextFrame(buf)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
		buf = rest
	}
	return out
}

func TestEventsRoundTripByteForByte(t *testing.T) {
	var e Event
	var out []byte
	seen := map[EventTag]bool{}
	for i, rec := range readFrames(t, "events.bin") {
		if err := e.Decode(rec); err != nil {
			t.Fatalf("record %d (%v): %v", i, EventTag(uint16(rec[0])|uint16(rec[1])<<8), err)
		}
		var err error
		out, err = e.Encode(out[:0])
		if err != nil {
			t.Fatalf("record %d (%v): %v", i, e.Tag, err)
		}
		if !bytes.Equal(out, rec) {
			t.Fatalf("record %d (%v) re-encodes differently", i, e.Tag)
		}
		seen[e.Tag] = true
	}
	for _, tag := range allEventTags() {
		if !seen[tag] {
			t.Errorf("no fixture for %v", tag)
		}
	}
}

func TestCommandsRoundTripByteForByte(t *testing.T) {
	var c Command
	var out []byte
	seen := map[CommandTag]bool{}
	for i, rec := range readFrames(t, "commands.bin") {
		if err := c.Decode(rec); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		var err error
		out, err = c.Encode(out[:0])
		if err != nil || !bytes.Equal(out, rec) {
			t.Fatalf("record %d (%v): %v, equal %v", i, c.Tag, err, bytes.Equal(out, rec))
		}
		seen[c.Tag] = true
	}
	for tag := CommandTag(1); tag < 1000; tag++ {
		if tag.String() != "unknown" && !seen[tag] {
			t.Errorf("no fixture for command %v", tag)
		}
	}
}

func allEventTags() []EventTag {
	var tags []EventTag
	for tag := EventTag(1); tag < 1000; tag++ {
		if tag.String() != "unknown" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// The event goroutine decodes into one reused Event: once its slices have
// grown, receiving allocates nothing.
func TestDecodingAllocatesNothingOnceWarm(t *testing.T) {
	recs := readFrames(t, "events.bin")
	var e Event
	for _, rec := range recs {
		if err := e.Decode(rec); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(50, func() {
		for _, rec := range recs {
			if err := e.Decode(rec); err != nil {
				panic(err)
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per pass over %d records", allocs, len(recs))
	}
}

// A reused decoder holds views only into the record it decoded last: the
// variant it left, its absent optionals and the list elements past the new
// length drop theirs, so an older record's buffer (a conversation's retired
// arena) can be collected, while the lists keep their capacity.
func TestADecodedValueReleasesTheRecordBeforeIt(t *testing.T) {
	encode := func(m *Message) []byte {
		b, err := m.Encode(make([]byte, 0, 64<<10))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rich := Message{
		ID: []byte("rich"), Chat: []byte("chat@demo.invalid"), Sender: []byte("a@demo.invalid"),
		SenderName: []byte("Ann"), HasSenderName: true,
		Content: Content{Tag: ContentTagImage, Image: ContentImage{
			Caption: []byte("a caption"), HasCaption: true,
			Media: Media{Mime: []byte("image/jpeg"), Path: []byte("/tmp/p.jpg"), HasPath: true},
		}},
		Quoted: Quoted{ID: []byte("q"), Sender: []byte("b@demo.invalid"), Summary: []byte("hi"),
			Mentions: []MentionRef{{User: []byte("u"), ID: []byte("m")}}},
		HasQuoted: true,
		Reactions: []Reaction{{Sender: []byte("s1"), Emoji: []byte("👍")}, {Sender: []byte("s2"), Emoji: []byte("🎉")}, {Sender: []byte("s3"), Emoji: []byte("❤")}},
		Mentions:  []MentionRef{{User: []byte("u1"), ID: []byte("i1")}},
	}
	plain := Message{ID: []byte("plain"), Chat: []byte("chat@demo.invalid"), Sender: []byte("a@demo.invalid"),
		Content:   Content{Tag: ContentTagText, Text: ContentText{Text: []byte("hello")}},
		Reactions: []Reaction{{Sender: []byte("s1"), Emoji: []byte("👍")}}}

	var d Message
	old := encode(&rich)
	if err := d.Decode(old); err != nil {
		t.Fatal(err)
	}
	gone := weak.Make(&old[0])
	old = nil
	if err := d.Decode(encode(&plain)); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	if gone.Value() != nil {
		t.Fatal("the decoded value still holds views into the record before it")
	}
	if cap(d.Reactions) < 3 || len(d.Reactions) != 1 || cap(d.Quoted.Mentions) < 1 {
		t.Fatalf("lists lost their capacity: reactions %d/%d, quote mentions %d", len(d.Reactions), cap(d.Reactions), cap(d.Quoted.Mentions))
	}
	recs := [][]byte{encode(&rich), encode(&plain)}
	if n := testing.AllocsPerRun(100, func() {
		for _, rec := range recs {
			if err := d.Decode(rec); err != nil {
				panic(err)
			}
		}
		d.Release()
	}); n != 0 {
		t.Fatalf("%v allocations decoding and releasing once warm", n)
	}
}

// Typing sends Composing and SaveDraft; they go out through one reused
// buffer.
func TestEncodingCommandsAllocatesNothingOnceWarm(t *testing.T) {
	chat := []byte("393331234567@s.whatsapp.net")
	text := []byte("Typing a draft…")
	var c Command
	buf := make([]byte, 0, 4096)
	allocs := testing.AllocsPerRun(100, func() {
		c.Tag = CommandTagComposing
		c.Composing = CommandComposing{Chat: chat, Composing: true}
		var err error
		buf, err = c.Encode(buf[:0])
		if err != nil {
			panic(err)
		}
		c.Tag = CommandTagSaveDraft
		c.SaveDraft = CommandSaveDraft{Chat: chat, Text: text}
		if buf, err = c.Encode(buf[:0]); err != nil {
			panic(err)
		}
		c.Tag = CommandTagSendText
		c.SendText = CommandSendText{Chat: chat, Text: text}
		if buf, err = c.Encode(buf[:0]); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per typing step", allocs)
	}
}

func TestMalformedRecordsAreRefused(t *testing.T) {
	var e Event
	for _, rec := range readFrames(t, "events.bin") {
		for cut := 0; cut < len(rec); cut++ {
			if err := e.Decode(rec[:cut]); err == nil {
				t.Fatalf("%d of %d bytes of a %v decoded", cut, len(rec), EventTag(uint16(rec[0])|uint16(rec[1])<<8))
			}
		}
		if err := e.Decode(append(rec[:len(rec):len(rec)], 0)); !errors.Is(err, ErrTrailing) {
			t.Fatalf("a trailing byte: %v", err)
		}
	}
	// A hostile count is refused before anything grows.
	bad := []byte{17, 0, 0, 0, 1, 0, 0, 0, 1, 0xff, 0xff, 0, 0}
	if err := e.Decode(bad); err == nil {
		t.Fatal("a count of 65535 contacts in 13 bytes decoded")
	}
}

func TestAFrameLongerThanMaxRecordFailsTheStream(t *testing.T) {
	buf := []byte{0, 0, 0x20, 0, 1, 2, 3} // 2 MiB
	if _, _, err := NextFrame(buf); !errors.Is(err, ErrFrame) {
		t.Fatalf("got %v", err)
	}
}

func TestAFieldOverItsBoundIsNotEncoded(t *testing.T) {
	var c Command
	c.Tag = CommandTagSaveDraft
	c.SaveDraft.Chat = bytes.Repeat([]byte("a"), 129)
	if _, err := c.Encode(nil); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	c.SaveDraft.Chat = []byte{0xff}
	if _, err := c.Encode(nil); !errors.Is(err, ErrUTF8) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenConfigRoundTrips(t *testing.T) {
	cfg := OpenConfig{ABI: ABIVersion, SchemaHash: SchemaHash, Demo: true, StateDir: []byte("/s"), CacheDir: []byte("/c"), Account: []byte("1")}
	b, err := cfg.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	var back OpenConfig
	if err := back.Decode(b); err != nil || !back.Demo || string(back.Account) != "1" || back.SchemaHash != SchemaHash {
		t.Fatalf("%v %+v", err, back)
	}
}

func FuzzEventDecode(f *testing.F) {
	for _, rec := range readFrames(f, "events.bin") {
		f.Add(rec)
	}
	f.Fuzz(func(t *testing.T, rec []byte) {
		var e Event
		if e.Decode(rec) != nil {
			return
		}
		// What decodes re-encodes to the same bytes.
		out, err := e.Encode(nil)
		if err != nil || !bytes.Equal(out, rec) {
			t.Fatalf("decoded but re-encodes differently: %v", err)
		}
	})
}

// messagesRecord encodes a Messages record of n messages with the longest
// text each.
func messagesRecord(t *testing.T, n int) []byte {
	t.Helper()
	text := bytes.Repeat([]byte("a"), 196_608)
	e := Event{Tag: EventTagMessages}
	e.Messages.Chat = []byte("1")
	e.Messages.Batch = 1
	e.Messages.Last = true
	for i := 0; i < n; i++ {
		var m Message
		m.ID = []byte{byte('a' + i)}
		m.Chat = []byte("1")
		m.Sender = []byte("1")
		m.Content.Tag = ContentTagText
		m.Content.Text.Text = text
		e.Messages.Messages = append(e.Messages.Messages, m)
	}
	rec, err := e.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// Three texts that each fit their field but together pass the chunk's byte
// budget: the encoder refuses to write such a record, so the decoder must
// refuse to read it (what decodes must re-encode).
func TestAChunkOverItsByteBudgetIsRefusedThoughEveryFieldFits(t *testing.T) {
	two := messagesRecord(t, 2)
	// 4 header bytes, the chat (4+1), the batch, three flags, then the count.
	const countAt = 4 + 4 + 1 + 4 + 3
	elements := two[countAt+4:]
	if len(elements)%2 != 0 {
		t.Fatalf("two messages of one size: %d bytes", len(elements))
	}
	one := elements[:len(elements)/2]
	three := append([]byte(nil), two[:countAt]...)
	three = binary.LittleEndian.AppendUint32(three, 3)
	three = append(three, elements...)
	three = append(three, one...)
	if len(three) >= MaxRecord {
		t.Fatal("the record itself fits")
	}
	var e Event
	if err := e.Decode(three); !errors.Is(err, ErrBound) {
		t.Fatalf("three over-budget messages decoded: %v", err)
	}
	if err := e.Decode(two); err != nil {
		t.Fatal(err)
	}
	out, err := e.Encode(nil)
	if err != nil || !bytes.Equal(out, two) {
		t.Fatalf("two messages re-encode: %v", err)
	}
}

func TestARecordLongerThanMaxRecordIsRefusedBeforeItIsRead(t *testing.T) {
	rec := make([]byte, MaxRecord+1)
	rec[0] = 22 // Syncing
	var e Event
	if err := e.Decode(rec); !errors.Is(err, ErrBound) {
		t.Fatalf("event: %v", err)
	}
	var c Command
	if err := c.Decode(rec); !errors.Is(err, ErrBound) {
		t.Fatalf("command: %v", err)
	}
}
