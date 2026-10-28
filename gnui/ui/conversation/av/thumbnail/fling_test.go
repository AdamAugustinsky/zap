package thumbnail

import (
	"testing"
	"time"
	"zapfast/abi"
	"zapfast/state"
)

type flingReader struct {
	reader
	started chan uint64
	finish  [7]chan struct{}
}

func (r *flingReader) ReadBlob(id, _ uint64, b []byte) (int, error) {
	r.started <- id
	<-r.finish[id]
	return len(b), nil
}
func TestFlingAdmitsNewestWantedWithoutAnotherFrame(t *testing.T) {
	var c Cache
	r := &flingReader{started: make(chan uint64, 8)}
	for i := range r.finish {
		r.finish[i] = make(chan struct{})
	}
	t.Cleanup(func() {
		for _, ch := range r.finish {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
		until(t, func() bool { _, _, n := c.Stats(); return n == 0 })
		c.Reset()
	})
	s := state.New()
	a := s.AddAccount("1", nil, r)
	get := func(id uint64) {
		if l := c.Get(s, a, abi.BlobRef{ID: id, Len: 16}, 512); l != nil {
			l.Release()
		}
	}
	c.beginFrame(1)
	get(1)
	get(2)
	<-r.started
	<-r.started // these reads stay in flight, never cancelled
	get(3)
	get(4) // queued rows that the fling will leave behind
	c.beginFrame(2)
	get(5)
	get(6)             // currently visible; 6 is the newest request
	close(r.finish[1]) // one slot free, without another UI lookup/frame
	for _, want := range []uint64{6, 5} {
		select {
		case got := <-r.started:
			if got != want {
				t.Fatalf("read %d before newest wanted %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("wanted %d was stranded waiting for another frame", want)
		}
		close(r.finish[want])
	}
	until(t, func() bool { _, _, n := c.Stats(); return n == 1 }) // only the original read 2
	select {
	case id := <-r.started:
		t.Fatalf("offscreen queued row %d was read", id)
	default:
	}
}
