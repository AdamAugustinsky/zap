//go:build darwin

package composer

import (
	"os"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

type lockBeforeEditor struct {
	*shell.Shell
	scr *Screen
	on  bool
}

func (h *lockBeforeEditor) Build(c *ui.Context) {
	h.Shell.Build(c)
	if h.on && h.scr.making {
		h.on = false
		h.S.Lock.Locked = true
		h.S.Locked()
	}
}

func TestLiveEditorWaitsForAChat(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	s, scr, f := state.New(), New(), fake.New(nil)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Last: true, Chats: defaultChats()} }))
	a := s.AddAccount("1", nil, f)
	sh, err := shell.New(s, &screen.Set{Composer: scr})
	if err != nil {
		t.Fatal(err)
	}
	h := &lockBeforeEditor{Shell: sh, scr: scr, on: true}
	l := widgettest.StartLive(t, shell.Config(s), h)
	l.Until("chats loaded", 300, func() bool { return a.Model.Chats.Loaded() })
	l.Do(func(*app.App) {
		if scr.st != nil && scr.st.Hosted() {
			t.Error("chat list created a native editor")
		}
		// Hooks and listeners must already work with no native editor.
		s.Locked()
		s.Quit()
		a.Model.SetDraft([]byte(ada), []byte("waiting draft"))
		f.Emit(ev(abi.EventTagPicked, func(e *abi.Event) {
			e.Picked.Chat, e.Picked.Paths = []byte(ada), [][]byte{[]byte("/tmp/waiting.txt")}
		}))
	})
	l.Frames(3)
	l.Do(func(*app.App) { a.OpenChat(a.Model.Chats.Find([]byte(ada))) })
	l.Until("lock before editor creation", 300, func() bool { return !h.on })
	l.Frames(3)
	l.Do(func(*app.App) {
		if scr.st.Hosted() {
			t.Error("a cancelled request created an editor while locked")
		}
		s.Lock.Locked = false
		s.Touch()
	})
	l.Until("draft and attachment in the hosted editor", 300, func() bool {
		return scr.st != nil && scr.st.Hosted() && string(scr.st.Text()) == "waiting draft" && len(s.Staged) == 1
	})
	l.Do(func(ap *app.App) { ap.Window().Show(); ap.Window().Focus(); s.Req.FocusComposer = true; s.Touch() })
	l.Until("first composer focus", 300, func() bool { return scr.focused })
	l.Type("!")
	l.Until("first input", 300, func() bool { return string(scr.st.Text()) == "waiting draft!" })
	l.Close()
	// Init runs again on the same screen. Its replacement host must keep
	// the current draft even though follow sees the same account/chat.
	l = widgettest.StartLive(t, shell.Config(s), sh)
	l.Until("recreated editor and draft", 300, func() bool {
		return scr.st != nil && scr.st.Hosted() && string(scr.st.Text()) == "waiting draft!"
	})
	l.Do(func(*app.App) { s.Req.FocusComposer = true; s.Touch() })
	l.Until("recreated composer focus", 300, func() bool { return scr.focused })
	l.Press(ui.KeyEnter, 0)
	l.Until("attachment sent with draft caption", 300, func() bool {
		for _, c := range f.Sent() {
			if c.Tag == abi.CommandTagSendFiles && string(c.SendFiles.Caption) == "waiting draft!" {
				return true
			}
		}
		return false
	})
}

func TestLiveParkedAttachmentSurvivesWindowRecreation(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	s, scr, f := state.New(), New(), fake.New(nil)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Last: true, Chats: defaultChats()} }))
	a := s.AddAccount("1", nil, f)
	b := &blobBarrier{Conn: f, entered: make(chan struct{}), release: make(chan struct{}), result: make(chan abi.BlobRef, 1)}
	release := sync.OnceFunc(func() { close(b.release) })
	t.Cleanup(release)
	a.Conn = b
	sh, err := shell.New(s, &screen.Set{Composer: scr})
	if err != nil {
		t.Fatal(err)
	}
	l := widgettest.StartLive(t, shell.Config(s), sh)
	l.Until("chats loaded", 300, func() bool { return a.Model.Chats.Loaded() })
	open := func(chat string) {
		l.Do(func(*app.App) { a.OpenChat(a.Model.Chats.Find([]byte(chat))) })
		l.Until("composer follows chat", 300, func() bool { return string(scr.chat) == chat && scr.st.Hosted() })
	}
	open(ada)
	l.Do(func(*app.App) {
		f.Emit(ev(abi.EventTagPicked, func(e *abi.Event) {
			e.Picked.Chat, e.Picked.Paths = []byte(ada), [][]byte{[]byte("/tmp/parked.txt")}
		}))
	})
	l.Until("attachment staged in Ada", 300, func() bool { return len(s.Staged) == 1 })
	var id uint64
	l.Do(func(*app.App) {
		id = s.Staged[0].ID
		scr.stagePNG(scr.env, pngOf(2, 2))
	})
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("picture worker did not reach the blob boundary")
	}
	open(grace)
	l.Do(func(*app.App) {
		if len(s.Staged) != 0 {
			t.Error("Ada's attachment followed Grace")
		}
	})
	l.Close()
	l = widgettest.StartLive(t, shell.Config(s), sh)
	l.Until("Grace's recreated editor", 300, func() bool { return scr.st.Hosted() })
	release()
	open(ada)
	l.Until("pending picture completed after recreation", 300, func() bool {
		return len(s.Staged) == 2 && s.Staged[1].Ready
	})
	l.Do(func(*app.App) {
		if len(s.Staged) != 2 || s.Staged[0].ID != id || string(s.Staged[0].Path) != "/tmp/parked.txt" {
			t.Errorf("parked attachment lost or replaced after recreation: %+v", s.Staged)
		}
		var px [16]byte
		if n, err := f.ReadBlob(s.Staged[1].Blob.ID, 0, px[:]); n != len(px) || err != nil || px[0] != 255 {
			t.Errorf("recreated picture's pixels lost: n=%d err=%v", n, err)
		}
	})
}
