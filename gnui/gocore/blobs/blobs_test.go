package blobs

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"zapfast/abi"
)

func lenOf(t *testing.T, tb *Table, id uint64) (uint64, bool) {
	t.Helper()
	return tb.Len(id)
}

// The tests below are zfcore/src/blobs.rs's, then the Go side's own.

func TestTheSameBytesInSeveralRecordsAreOneBlobFreedWithTheLast(t *testing.T) {
	tb := New()
	a := tb.PutEvent([]byte{1, 2, 3})
	b := tb.PutEvent([]byte{1, 2, 3})
	c := tb.PutEvent([]byte{4, 5})
	if a != b || a.ID == c.ID {
		t.Fatalf("a %v, b %v, c %v", a, b, c)
	}
	if tb.Count() != 2 {
		t.Fatalf("%d blobs, want 2", tb.Count())
	}
	tb.Free(a.ID)
	if n, ok := lenOf(t, tb, a.ID); !ok || n != 3 {
		t.Fatal("the second record still names it")
	}
	tb.Free(b.ID)
	if _, ok := lenOf(t, tb, a.ID); ok {
		t.Fatal("freed by both records, it stays")
	}
	if tb.EventBytes() != 2 {
		t.Errorf("%d event bytes, want 2", tb.EventBytes())
	}
}

func TestACallersBlobIsNeverSharedOrExpired(t *testing.T) {
	tb := WithLimits(4, 0)
	mine := tb.Put(bytes.Repeat([]byte{9}, 100))
	event := tb.PutEvent(bytes.Repeat([]byte{9}, 100))
	if mine.ID == event.ID {
		t.Fatal("a caller's blob was shared with an event's")
	}
	tb.PutEvent(bytes.Repeat([]byte{7}, 100))
	if n, ok := lenOf(t, tb, mine.ID); !ok || n != 100 {
		t.Fatal("the caller's blob expired")
	}
	b, err := tb.Take(mine)
	if err != nil || len(b) != 100 {
		t.Fatalf("take: %d bytes, %v", len(b), err)
	}
}

func TestPastTheBudgetTheEventBlobsTouchedLongestAgoGo(t *testing.T) {
	tb := WithLimits(300, 0)
	first := tb.PutEvent(bytes.Repeat([]byte{1}, 100))
	second := tb.PutEvent(bytes.Repeat([]byte{2}, 100))
	third := tb.PutEvent(bytes.Repeat([]byte{3}, 100))
	// Reading the first makes the second the oldest.
	if _, err := tb.Read(first.ID, 0, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	tb.PutEvent(bytes.Repeat([]byte{4}, 100))
	if _, ok := lenOf(t, tb, second.ID); ok {
		t.Error("the oldest stayed")
	}
	if tb.EventBytes() > 225 {
		t.Errorf("%d event bytes kept, want at most 225", tb.EventBytes())
	}
	if _, ok := lenOf(t, tb, first.ID); !ok {
		t.Error("the blob read since went")
	}
	if _, ok := lenOf(t, tb, third.ID); ok {
		t.Error("the next oldest stayed")
	}
}

func TestALargeBlobGoesBeforeSmallOnesTouchedEarlier(t *testing.T) {
	tb := WithLimits(SharedMax+300, 0)
	small := tb.PutEvent(bytes.Repeat([]byte{1}, 100))
	large := tb.PutEvent(bytes.Repeat([]byte{2}, SharedMax+1))
	tb.PutEvent(bytes.Repeat([]byte{3}, 300))
	if _, ok := lenOf(t, tb, large.ID); ok {
		t.Error("the large blob stayed")
	}
	if _, ok := lenOf(t, tb, small.ID); !ok {
		t.Error("the small blob went")
	}
}

func TestWithinTheGraceNothingExpires(t *testing.T) {
	tb := WithLimits(10, time.Minute)
	first := tb.PutEvent(bytes.Repeat([]byte{1}, 100))
	tb.PutEvent(bytes.Repeat([]byte{2}, 100))
	if _, ok := lenOf(t, tb, first.ID); !ok {
		t.Error("a blob within its grace expired")
	}
	// Once the grace has passed it goes.
	tb.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	tb.PutEvent(bytes.Repeat([]byte{3}, 100))
	if _, ok := lenOf(t, tb, first.ID); ok {
		t.Error("a blob past its grace stayed")
	}
}

func TestReadsInRangesAndTakeChecksTheLength(t *testing.T) {
	tb := New()
	src := make([]byte, 10_000)
	for i := range src {
		src[i] = byte(i * 7)
	}
	ref := tb.Put(src)
	src[0]++ // Put copied
	src[0]--
	var got []byte
	piece := make([]byte, 3000)
	for off := uint64(0); ; {
		n, err := tb.Read(ref.ID, off, piece)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		got = append(got, piece[:n]...)
		off += uint64(n)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("read back differs")
	}
	if n, err := tb.Read(ref.ID, ref.Len, piece); n != 0 || err != nil {
		t.Errorf("at the end: %d, %v", n, err)
	}
	if _, err := tb.Read(ref.ID, ref.Len+1, piece); !errors.Is(err, ErrRange) {
		t.Errorf("past the end: %v", err)
	}
	if _, err := tb.Take(abi.BlobRef{ID: ref.ID, Len: ref.Len - 1}); !errors.Is(err, ErrRange) {
		t.Errorf("take at another length: %v", err)
	}
	if !tb.Has(ref) {
		t.Fatal("a refused take removed the blob")
	}
	b, err := tb.Take(ref)
	if err != nil || !bytes.Equal(b, src) {
		t.Fatalf("take: %v", err)
	}
	if _, err := tb.Take(ref); !errors.Is(err, ErrNotFound) {
		t.Errorf("a second take: %v", err)
	}
	if _, err := tb.Read(ref.ID, 0, piece); !errors.Is(err, ErrNotFound) {
		t.Errorf("read after take: %v", err)
	}
	tb.Free(ref.ID) // unknown: ignored
}

func TestIDsAreSharedAndNeverReused(t *testing.T) {
	tb := New()
	a := tb.Put([]byte("a"))
	b := tb.PutEvent([]byte("b"))
	tb.Free(a.ID)
	c := tb.Put([]byte("c"))
	if a.ID != 1 || b.ID != 2 || c.ID != 3 {
		t.Errorf("ids %d %d %d, want 1 2 3", a.ID, b.ID, c.ID)
	}
	tb.Clear()
	if tb.Count() != 0 || tb.EventBytes() != 0 {
		t.Error("Clear left blobs")
	}
}

func TestConcurrentUse(t *testing.T) {
	tb := WithLimits(1<<16, 0)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			buf := make([]byte, 512)
			for i := range 500 {
				b := bytes.Repeat([]byte{byte(g), byte(i)}, 1+i%300)
				ref := tb.PutEvent(b)
				tb.Read(ref.ID, 0, buf)
				mine := tb.Put(b)
				if got, err := tb.Take(mine); err != nil || !bytes.Equal(got, b) {
					t.Errorf("take: %v", err)
					return
				}
				tb.Free(ref.ID)
			}
		})
	}
	wg.Wait()
	if tb.EventBytes() > 1<<16 {
		t.Errorf("%d event bytes over the budget", tb.EventBytes())
	}
}
