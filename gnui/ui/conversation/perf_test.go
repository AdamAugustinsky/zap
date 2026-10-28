package conversation

import (
	"os"
	"runtime"
	"strconv"
	"testing"

	"zapfast/abi"
)

// seed opens Ada's chat with n messages of mixed lengths, senders and
// reactions, delivered in pages as the core sends them.
func seedChat(r *rig, n int) {
	ref := r.a.Model.Chats.Find([]byte(ada))
	r.a.OpenChat(ref)
	r.Frames(2)
	const page = 250
	words := [...]string{"the", "engine", "room", "is", "warm", "tonight", "and", "we", "keep", "going", "👍", "fine", "see", "you", "at", "nine"}
	for at := 0; at < n; at += page {
		var msgs []abi.Message
		for i := at; i < min(at+page, n); i++ {
			body := ""
			for w := 0; w < 3+i*7%23; w++ {
				if w > 0 {
					body += " "
				}
				body += words[(i*31+w*7)%len(words)]
			}
			m := msg(ada, "b"+strconv.Itoa(i), base0+int64(i)*45, i%3 == 0, body)
			if i%17 == 0 {
				m.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("❤️")}}
			}
			if i%29 == 0 {
				m.Content.Text.Text = append(m.Content.Text.Text, " *bold* _it_ example.com"...)
			}
			msgs = append(msgs, m)
		}
		last := at+page >= n
		if err := r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: last, Complete: true, Messages: msgs}
		})); err != nil {
			r.t.Fatal(err)
		}
		r.Frames(1)
	}
	r.Frames(6)
}

// scroll moves the transcript by dy pixels and runs a frame.
func (r *rig) scroll(dy float64) {
	r.scr.t.st.ScrollBy(dy)
	r.Frame()
}

// Once warm, a frame of a 10 000 message transcript allocates nothing, idle
// or scrolling through rows it has not shown yet.
func TestFramesAllocateNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	r := newRig(t)
	seedChat(r, 10_000)
	if r.scr.t.st.Len() != 10_000 {
		t.Fatalf("%d items", r.scr.t.st.Len())
	}
	// warm: scroll the whole way up and back, so every cache has grown
	for i := 0; i < 400; i++ {
		r.scroll(-900)
	}
	for i := 0; i < 400; i++ {
		r.scroll(900)
	}
	r.Frames(10)
	if os.Getenv("ZF_ALLOCPROF") != "" {
		runtime.MemProfileRate = 1
	}
	// Per frame, as cmd/zapfast measures (testing.AllocsPerRun): the
	// runtime's own background work (the scavenger's timer) may allocate
	// once in a while, which a total over many frames would count.
	if n := testing.AllocsPerRun(60, r.Frame); n != 0 {
		t.Errorf("idle: %v allocations per frame", n)
	}
	if n := testing.AllocsPerRun(120, func() { r.scroll(-160) }); n != 0 {
		t.Errorf("scrolling: %v allocations per frame", n)
	}
}

// BenchmarkScroll10k is a frame of the 10 000 message transcript while it
// scrolls (the whole frame: shell, layout, the offscreen renderer).
func BenchmarkScroll10k(b *testing.B) {
	r := newRigSized(b, 0, 0)
	seedChat(r, 10_000)
	for i := 0; i < 200; i++ {
		r.scroll(-600)
	}
	b.ReportAllocs()
	b.ResetTimer()
	cpu0 := cpuNs()
	dy := -120.0
	for i := 0; i < b.N; i++ {
		if i%400 == 0 {
			dy = -dy
		}
		r.scroll(dy)
	}
	b.ReportMetric(float64(cpuNs()-cpu0)/float64(b.N)/1e6, "cpu-ms/frame")
}

// An idle conversation asks for no frames.
func TestIdleAsksForNoFrames(t *testing.T) {
	r := newRig(t)
	m := msg(ada, "i1", base0, false, "idle *now* example.com")
	m.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("👍")}}
	r.open(ada, m, msg(ada, "i2", base0+60, true, "ok"))
	// the phone has nothing older, and the archive nothing new
	r.f.Emit(ev(abi.EventTagOlderFetched, func(e *abi.Event) { e.OlderFetched = abi.EventOlderFetched{Chat: []byte(ada)} }))
	r.Frames(2)
	r.page(ada, true, true)
	r.Frames(30)
	r.Frame()
	if ui := r.Offscreen().UI; ui.WakeAfter() != 0 || ui.Animating() {
		t.Errorf("an idle frame asks for another (wake in %d ns, animating %v)", ui.WakeAfter(), ui.Animating())
	}
}
