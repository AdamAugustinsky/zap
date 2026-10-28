package composer

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Steady frames allocate nothing: idle, with the mention list open, and
// with a reply and attachments staged (a picture with its crop button).
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.messages(group, textMsg("m1", ada, false, 100, "Hi"))
	r.focus()
	r.Frames(10)
	if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
		t.Errorf("idle: %v allocations per frame", n)
	}
	r.typeKeys("hello ", "@")
	r.Frames(5)
	if !r.listShown() {
		t.Fatal("no list")
	}
	if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
		t.Errorf("the list open: %v allocations per frame", n)
	}
	r.key(ui.KeyEscape, 0)
	r.s.Panes.Reply.Add([]byte("m1"))
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00/tmp/b.png\x00"), 2)
	r.Frames(10)
	if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
		t.Errorf("reply and attachment: %v allocations per frame", n)
	}
}

func TestFailedPreparationFramesAllocateNothing(t *testing.T) {
	for _, kind := range []string{"picture", "voice"} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.open(ada)
			b := &blobFailure{Conn: r.f}
			b.fail.Store(true)
			r.a.Conn = b
			if kind == "voice" {
				r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
				r.mic.el = time.Second
				r.scr.voice.send(r.scr, r.scr.env)
			} else {
				r.dropAt(ui.DropImage, pngOf(2, 2), 1)
			}
			r.awaitPreparation("preparation failure not shown", func() bool { return r.toasted("test blob storage unavailable") })
			r.Frames(10)
			puts := b.puts.Load()
			if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
				t.Errorf("failed preparation: %v allocations per frame", n)
			}
			if b.puts.Load() != puts {
				t.Error("steady frames retried failed preparation")
			}
		})
	}
}
