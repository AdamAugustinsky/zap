package composer

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
	"zapfast/ui/shell"
)

// Hold a real preparation worker at the blob boundary, rather than racing a
// large image against window recreation. No production test hook is needed.
type blobBarrier struct {
	core.Conn
	entered chan struct{}
	release chan struct{}
	result  chan abi.BlobRef
	once    sync.Once
}

func (b *blobBarrier) PutBlob(data []byte) (abi.BlobRef, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	ref, err := b.Conn.PutBlob(data)
	b.result <- ref
	return ref, err
}

func holdBlobs(r *rig) (*blobBarrier, func()) {
	b := &blobBarrier{Conn: r.f, entered: make(chan struct{}), release: make(chan struct{}), result: make(chan abi.BlobRef, 1)}
	release := sync.OnceFunc(func() { close(b.release) })
	r.t.Cleanup(release)
	r.a.Conn = b
	return b, release
}

func (r *rig) awaitPreparation(what string, ready func() bool) {
	r.t.Helper()
	for i := 0; i < 400; i++ {
		r.Frames(1)
		if ready() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatal(what)
}

func (r *rig) recreate() {
	r.t.Helper()
	// A fresh offscreen window re-initializes the same shell and screens,
	// exactly as the live close/reopen path does. Both rigs clean up at exit.
	r.Rig = widgettest.Open(r.t, shell.Config(r.s), 1100, 720, 1, r.h)
	r.Frames(3)
}

func TestPendingPictureSurvivesWindowRecreation(t *testing.T) {
	for _, parked := range []bool{false, true} {
		t.Run(fmt.Sprint(parked), func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.open(ada)
			b, release := holdBlobs(r)
			r.dropAt(ui.DropImage, pngOf(2, 2), 1)
			select {
			case <-b.entered:
			case <-time.After(time.Second):
				t.Fatal("picture worker never reached the blob boundary")
			}
			id := r.s.Staged[0].ID
			if parked {
				r.open(grace)
			}
			r.recreate()
			release()
			if parked {
				r.awaitPreparation("parked picture never became ready", func() bool {
					st := r.scr.asides.staged(id)
					return st != nil && st.Ready
				})
				r.open(ada)
			}
			r.awaitPreparation("picture stuck after recreation", func() bool { return r.s.StagedByID(id).Ready })
			st := r.s.StagedByID(id)
			if px, ok := r.blob(st.Blob); !ok || len(px) != 16 || px[0] != 255 {
				t.Fatal("decoded pixels lost after recreation")
			}
			ch, _ := r.a.Model.Chats.Get(r.a.Open)
			r.scr.send(r.scr.env, r.a, ch)
			if c, ok := r.last(abi.CommandTagSendImage); !ok || !eq(c.SendImage.Chat, ada) || len(r.s.Staged) != 0 {
				t.Fatal("recreated picture could not be sent to its chat")
			}
		})
	}
}

func TestRemovedPendingPictureIsFreedAfterRecreation(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	b, release := holdBlobs(r)
	r.dropAt(ui.DropImage, pngOf(2, 2), 1)
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("picture preparation did not start")
	}
	r.scr.clearStaged(r.a)
	r.recreate()
	release()
	var ref abi.BlobRef
	select {
	case ref = <-b.result:
	case <-time.After(time.Second):
		t.Fatal("picture preparation did not finish")
	}
	r.awaitPreparation("removed picture's blob stranded after recreation", func() bool {
		_, ok := r.blob(ref)
		return !ok
	})
}

type blobFailure struct {
	core.Conn
	fail atomic.Bool
	puts atomic.Int32
}

func (b *blobFailure) PutBlob(data []byte) (abi.BlobRef, error) {
	b.puts.Add(1)
	if b.fail.Load() {
		return abi.BlobRef{}, errors.New("test blob storage unavailable")
	}
	return b.Conn.PutBlob(data)
}

func TestFailedPicturePreparationKeepsInputsAndRetries(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	b := &blobFailure{Conn: r.f}
	b.fail.Store(true)
	r.a.Conn = b
	stageFiles(r, 1, state.StagedFile)
	r.s.Panes.Reply.Add([]byte("m1"))
	r.scr.st.SetText([]byte("caption"))
	r.dropAt(ui.DropImage, pngOf(2, 2), 1)
	id := r.s.Staged[1].ID
	r.awaitPreparation("picture never failed", func() bool { return r.s.StagedByID(id).Failed })
	if !r.toasted("test blob storage unavailable") || !r.hasText("test blob storage unavailable") {
		t.Error("failed preparation has no visible error")
	}
	ch, _ := r.a.Model.Chats.Get(r.a.Open)
	r.scr.send(r.scr.env, r.a, ch)
	r.awaitPreparation("retry lost the failed picture", func() bool {
		st := r.s.StagedByID(id)
		return st != nil && st.Failed
	})
	if len(r.s.Staged) != 2 || r.text() != "caption" || r.s.Panes.Reply.Len() != 1 || len(r.sent(abi.CommandTagSendFiles)) != 0 || len(r.sent(abi.CommandTagSendImage)) != 0 {
		t.Fatal("failed picture consumed files, caption or reply")
	}
	r.open(grace)
	r.open(ada)
	if !r.hasText("test blob storage unavailable") {
		t.Error("failed picture lost its error while parked")
	}
	b.fail.Store(false)
	r.scr.send(r.scr.env, r.a, ch)
	r.awaitPreparation("picture retry never sent", func() bool { return len(r.sent(abi.CommandTagSendImage)) == 1 })
	c, _ := r.last(abi.CommandTagSendImage)
	if px, ok := r.blob(c.SendImage.RGBA); !ok || len(px) != 16 || px[0] != 255 {
		t.Fatal("retry lost the original picture data")
	}
	f, _ := r.last(abi.CommandTagSendFiles)
	if len(r.s.Staged) != 0 || r.text() != "" || !eq(f.SendFiles.Caption, "caption") || !f.SendFiles.HasQuoting {
		t.Fatal("successful retry lost caption/quote or did not clear inputs")
	}
}

func TestInvalidPNGStaysStagedUntilRemoved(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.scr.st.SetText([]byte("caption"))
	r.dropAt(ui.DropImage, []byte("not a PNG"), 1)
	r.awaitPreparation("invalid PNG never failed", func() bool { return r.s.Staged[0].Failed })
	id := r.s.Staged[0].ID
	ch, _ := r.a.Model.Chats.Get(r.a.Open)
	r.scr.send(r.scr.env, r.a, ch)
	r.awaitPreparation("invalid PNG was discarded by Send", func() bool {
		st := r.s.StagedByID(id)
		return st != nil && st.Failed
	})
	if r.text() != "caption" || !r.toasted("png:") {
		t.Fatal("invalid picture lost its caption or error")
	}
	r.scr.clearStaged(r.a)
	r.scr.send(r.scr.env, r.a, ch)
	if c, ok := r.last(abi.CommandTagSendText); !ok || !eq(c.SendText.Text, "caption") {
		t.Fatal("removing the invalid picture did not unblock the caption")
	}
}

func TestPicturePasteWithoutCoreKeepsPNGUntilRetry(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"))
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	r.scr.st.SetText([]byte("caption"))
	r.a.Model.SetDraft(r.scr.chat, r.scr.st.Text())
	r.a.Conn = nil
	png := pngOf(2, 2)
	r.before(func(*ui.Context) {
		r.scr.pasted(r.scr.env, app.Paste{Kind: platform.PasteImage, Data: png})
	})
	r.Frames(2)
	clear(png) // clipboard buffers are borrowed; retry needs an owned copy
	if len(r.s.Staged) != 1 {
		t.Fatal("picture paste without a core was discarded")
	}
	st := &r.s.Staged[0]
	if !st.Failed || !r.toasted(state.ErrNoCore.Error()) || !r.hasText(state.ErrNoCore.Error()) {
		t.Fatal("missing core was not shown as a retained preparation failure")
	}
	ch, _ := r.a.Model.Chats.Get(r.a.Open)
	r.scr.send(r.scr.env, r.a, ch)
	if len(r.s.Staged) != 1 || !r.s.Staged[0].Failed || r.text() != "caption" || !eq(r.a.Model.Draft(r.scr.chat), "caption") || r.s.Panes.Reply.Len() != 1 || len(r.sent(abi.CommandTagSendImage)) != 0 {
		t.Fatal("retry without a core consumed picture, caption, draft or reply")
	}
	r.a.Conn = r.f
	r.scr.send(r.scr.env, r.a, ch)
	r.awaitPreparation("picture did not retry when the core returned", func() bool {
		return len(r.sent(abi.CommandTagSendImage)) == 1
	})
	c, _ := r.last(abi.CommandTagSendImage)
	if px, ok := r.blob(c.SendImage.RGBA); !ok || len(px) != 16 || px[0] != 255 {
		t.Fatal("retry lost the pasted PNG's pixels")
	}
	if !eq(c.SendImage.Caption, "caption") || !c.SendImage.HasQuoting || !eq(c.SendImage.Quoting.ID, "m1") || len(r.s.Staged) != 0 || r.text() != "" || r.s.Panes.Reply.Len() != 0 || len(r.a.Model.Draft(r.scr.chat)) != 0 {
		t.Fatal("successful retry lost caption/quote or did not clear sent inputs")
	}
}
