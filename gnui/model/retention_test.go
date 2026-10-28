package model

import (
	"runtime"
	"strconv"
	"testing"
	"unsafe"
	"weak"

	"zapfast/abi"
)

func TestColdEvictionTrimsOnlyLargeUnpinnedSlots(t *testing.T) {
	s := New()
	chat := []byte("pinned@demo.invalid")
	s.Chats.upsert(&abi.Chat{ID: chat})
	s.PinConversations(s.Chats.Find(chat), 0)
	open, cold := s.Open(chat), s.Open([]byte("cold@demo.invalid"))
	for _, v := range []*Conversation{open, cold} {
		for i := 0; i < 10000; i++ {
			v.upsert(&abi.Message{ID: []byte(strconv.Itoa(i)), Timestamp: int64(i)})
		}
		v.sort()
		v.compact() // exercise spare arena and decoded scratch lifetimes too
	}
	for i := 0; i < MaxConversations; i++ {
		s.Open([]byte(strconv.Itoa(i)))
	}
	if s.Conversation(chat) != open || open.Len() != 10000 || open.Find([]byte("9999")) != 9999 {
		t.Fatal("cold eviction touched the pinned conversation")
	}
	if cap(cold.recs) != 0 || cap(cold.a.b) != 0 || cap(cold.spare) != 0 || cold.scratch.ID != nil {
		t.Fatal("evicted slot retained its peak buffers")
	}
	small := s.Open([]byte("small@demo.invalid"))
	m := abi.Message{ID: []byte("one")}
	small.upsert(&m)
	if n := testing.AllocsPerRun(100, func() {
		small.reset([]byte("small@demo.invalid"))
		small.upsert(&m)
		small.sort()
	}); n != 0 {
		t.Fatalf("small-slot reuse: %g allocations", n)
	}
}

// A download's state is applied by decoding the row, changing it and
// upserting it again; when that upsert grows the arena, the decoded copy
// must not keep the retired one alive.
func TestAnArenaGrownAwayIsCollectable(t *testing.T) {
	v := New().Open([]byte("chat@demo.invalid"))
	m := abi.Message{Chat: []byte("chat@demo.invalid"), Content: abi.Content{Tag: abi.ContentTagImage,
		Image: abi.ContentImage{Media: abi.Media{Mime: []byte("image/jpeg")}}}}
	for i := 0; i == 0 || cap(v.a.b)-len(v.a.b) >= 4<<10; i++ {
		m.ID, m.Timestamp = []byte(strconv.Itoa(i)), int64(i)
		v.upsert(&m)
	}
	before := cap(v.a.b)
	old := weak.Make(unsafe.SliceData(v.a.b))
	if !v.setMedia([]byte("0"), PartMain, abi.MediaStateTagIdle, []byte("/tmp/demo.jpg"), nil) || cap(v.a.b) == before {
		t.Fatal("the download's state did not grow the arena")
	}
	runtime.GC()
	runtime.GC()
	if old.Value() != nil {
		t.Fatal("the arena grown away is still reachable")
	}
	runtime.KeepAlive(v)
}
