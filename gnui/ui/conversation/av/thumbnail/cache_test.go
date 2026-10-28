package thumbnail

import (
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/img"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
)

type reader struct {
	core.Conn
	wait         chan struct{}
	active, peak atomic.Int32
	buffer       chan []byte
}

func (r *reader) ReadBlob(id, _ uint64, b []byte) (int, error) {
	n := r.active.Add(1)
	for old := r.peak.Load(); n > old && !r.peak.CompareAndSwap(old, n); old = r.peak.Load() {
	}
	if r.wait != nil {
		<-r.wait
	}
	if r.buffer != nil && id == 1 {
		r.buffer <- b
	}
	r.active.Add(-1)
	return len(b), nil
}
func (r *reader) FreeBlob(uint64)         { panic("UI cache must not free core blobs") }
func (r *reader) Send(*abi.Command) error { return nil }
func until(t *testing.T, f func() bool) {
	t.Helper()
	for end := time.Now().Add(time.Second); !f(); {
		if time.Now().After(end) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func ready(t *testing.T, get func() img.Lease) img.Lease {
	t.Helper()
	var l img.Lease
	until(t, func() bool { l = get(); return l != nil })
	return l
}

func TestBoundsAndLeases(t *testing.T) {
	var c Cache
	s := state.New()
	r := &reader{wait: make(chan struct{}), buffer: make(chan []byte, 64)}
	a := s.AddAccount("1", nil, r)
	get := func(id, size uint64) img.Lease { return c.Get(s, a, abi.BlobRef{ID: id, Len: size}, 512) }
	for i := range 16 {
		get(uint64(i+1), 1<<16)
	}
	_, _, admitted := c.Stats()
	until(t, func() bool { return r.peak.Load() >= int32(admitted) })
	if n := r.peak.Load(); n > 2 {
		close(r.wait)
		t.Fatalf("readers=%d, want <=2", n)
	}
	close(r.wait)
	until(t, func() bool { _, _, n := c.Stats(); return n == 0 })
	lease := get(1, 1<<16)
	if lease == nil {
		t.Fatal("no lease")
	}
	b := <-r.buffer
	r.buffer = nil
	if &lease.Bytes()[0] != &b[0] {
		t.Fatal("read result copied")
	}
	c.Reset() // the decoder can outlive raw eviction, without escaping the budget
	_, n, _ := c.Stats()
	if n != 1<<16 {
		t.Fatalf("leased bytes=%d", n)
	}
	lease.Release()
	peak := 0
	for i := range 24 {
		id, size := uint64(i+100), uint64(1+i%3)<<18
		ready(t, func() img.Lease { return get(id, size) }).Release()
		_, n, _ = c.Stats()
		actual := 0
		c.Lock()
		for _, e := range c.m {
			actual += cap(e.data)
		}
		c.Unlock()
		if actual != n {
			t.Fatalf("accounting=%d, retained capacity=%d", n, actual)
		}
		peak = max(peak, n)
		if n > 2<<20 {
			t.Fatalf("bytes=%d, budget=%d", n, 2<<20)
		}
	}
	c.Reset()
	_, n, _ = c.Stats()
	if n != 0 {
		t.Fatalf("retained bytes=%d", n)
	}
	t.Logf("peak bytes=%d, reads=%d, read-to-lease copies=0", peak, r.peak.Load())
}

func TestLateCompletionAndRetry(t *testing.T) {
	for _, how := range []string{"evict", "switch", "remove", "close", "quit"} {
		t.Run(how, func(t *testing.T) {
			var c Cache
			s := state.New()
			r := &reader{wait: make(chan struct{})}
			a := s.AddAccount("1", nil, r)
			ref := abi.BlobRef{ID: 1, Len: 16}
			c.Get(s, a, ref, 1)
			until(t, func() bool { return r.active.Load() == 1 })
			switch how {
			case "evict":
				c.Get(s, a, abi.BlobRef{ID: 2, Len: 16}, 1)
			case "switch":
				a.Model.Event(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Last: true, Chats: []abi.Chat{{ID: []byte("fixture")}}}})
				a.OpenChat(a.Model.Chats.Find([]byte("fixture")))
				s.AddAccount("2", nil, &reader{})
				s.SwitchAccount(1)
			case "remove":
				c.Event(a, &abi.Event{Tag: abi.EventTagAccountRemoved})
			case "close":
				s.AppEvent(&app.Event{Kind: app.EvWindowClosed})
			case "quit":
				s.Quit()
			}
			close(r.wait)
			until(t, func() bool { _, _, n := c.Stats(); return n == 0 })
			if c.Contains(a.Serial, ref.ID) {
				t.Fatal("stale completion was cached")
			}
			// A distinct account's colliding blob ID, or the evicted entry, can retry.
			ready(t, func() img.Lease { return c.Get(s, s.Acct(), ref, 1) }).Release()
			c.Reset()
		})
	}
}

func TestLeaseReleaseWakesBudgetRetry(t *testing.T) {
	var c Cache
	var wakes atomic.Int32
	s := state.New()
	s.Wake = func() { wakes.Add(1) }
	a := s.AddAccount("1", nil, &reader{})
	var leases [2]img.Lease
	for i := range leases {
		leases[i] = ready(t, func() img.Lease { return c.Get(s, a, abi.BlobRef{ID: uint64(i + 1), Len: 1 << 20}, 512) })
	}
	until(t, func() bool { return wakes.Load() == 2 })
	c.Reset() // a closed image cache's decoders can still hold these leases
	ref := abi.BlobRef{ID: 3, Len: 16}
	if c.Get(s, a, ref, 512) != nil {
		t.Fatal("budget should be pinned")
	}
	before := wakes.Load()
	leases[0].Release()
	leases[1].Release()
	if wakes.Load() == before {
		t.Fatal("budget freed without waking the refused request")
	}
	ready(t, func() img.Lease { return c.Get(s, a, ref, 512) }).Release()
	c.Reset()
}

func TestOlderPendingRequestSurvivesBudgetEviction(t *testing.T) {
	var c Cache
	s := state.New()
	r := &reader{wait: make(chan struct{})}
	a := s.AddAccount("1", nil, r)
	get := func(id uint64) img.Lease { return c.Get(s, a, abi.BlobRef{ID: id, Len: 1 << 20}, 512) }
	get(1)
	get(2)
	get(3)          // third is pending behind the read bound
	c.beginFrame(1) // no longer wanted: keep it pending rather than reading it
	close(r.wait)
	until(t, func() bool { _, _, n := c.Stats(); return n == 0 })
	for _, id := range []uint64{4, 5} {
		ready(t, func() img.Lease { return get(id) }).Release()
	}
	// 3 is now the oldest entry, followed by two ready 1 MiB entries.
	get(3)
	if !c.Contains(a.Serial, 3) {
		t.Fatal("budget admission evicted its own pending request, with no reader left to wake it")
	}
	ready(t, func() img.Lease { return get(3) }).Release()
	c.Reset()
}
