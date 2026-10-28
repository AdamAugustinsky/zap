package composer

import (
	"errors"
	"sync"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
)

type mediaCore struct {
	core.Conn
	mu    sync.Mutex
	freed map[uint64]bool
}

func (c *mediaCore) FreeBlob(id uint64) {
	c.mu.Lock()
	if c.freed == nil {
		c.freed = make(map[uint64]bool)
	}
	c.freed[id] = true
	c.mu.Unlock()
	c.Conn.FreeBlob(id)
}

func (c *mediaCore) wasFreed(id uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.freed[id]
}

// Complete allocation before blocking, so removal must free an actual blob
// even if the account's connection closes before the worker returns.
type delayedMediaCore struct {
	*mediaCore
	ready   chan abi.BlobRef
	release chan struct{}
	fail    bool
}

func (c *delayedMediaCore) PutBlob(data []byte) (abi.BlobRef, error) {
	var ref abi.BlobRef
	var err error
	if c.fail {
		err = errors.New("old preparation failure")
	} else {
		ref, err = c.Conn.PutBlob(data)
	}
	c.ready <- ref
	<-c.release
	return ref, err
}

func mediaOtherAccount(r *rig) *state.Account {
	f := fake.New(nil)
	b := r.s.AddAccount("2", nil, f)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: defaultChats()} }))
	r.Frames(2)
	return b
}

func endMediaOwner(r *rig, kind string) {
	r.t.Helper()
	switch kind {
	case "unlink":
		r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
	case "remove":
		r.f.Emit(&abi.Event{Tag: abi.EventTagAccountRemoved})
	case "chat":
		r.f.Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(ada) }))
	case "serial":
		// Exercise the session guard independently of cleanup callbacks.
		r.a.Serial += 1 << 32
	}
	r.Frames(3)
}

func restoreMediaChat(r *rig) {
	r.t.Helper()
	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 22, Last: true, Chats: defaultChats()} }))
	r.Frames(2)
	r.s.SwitchAccount(r.a.Index)
	r.open(ada)
}

func recordMediaVoice(r *rig) {
	r.t.Helper()
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = time.Second
	r.scr.voice.send(r.scr, r.scr.env)
}

func TestMediaIsDroppedWhenItsOwnerEnds(t *testing.T) {
	for _, kind := range []string{"unlink", "remove", "chat"} {
		for _, location := range []string{"held", "parked", "hidden"} {
			for _, payload := range []string{"pcm", "blob"} {
				t.Run(kind+"/"+location+"/"+payload, func(t *testing.T) {
					r := newRig(t, nil, nil)
					other := mediaOtherAccount(r)
					r.open(ada)
					c := &mediaCore{Conn: r.f}
					bad := &blobFailure{Conn: c}
					bad.fail.Store(true)
					if payload == "pcm" {
						r.a.Conn = bad
					} else {
						r.a.Conn = &sendFault{Conn: c, tag: abi.CommandTagSendVoice, on: true}
					}
					recordMediaVoice(r)
					r.awaitPreparation("voice was not retained", func() bool { return r.scr.voice.unsentHere(r.scr) })
					voiceID := r.scr.voice.unsent.blob.ID
					r.a.Conn = c
					r.dropAt(ui.DropImage, pngOf(2, 2), 1)
					r.waitPictures()
					pictureID := r.s.Staged[0].Blob.ID
					r.a.Conn = bad
					r.dropAt(ui.DropImage, pngOf(2, 2), 1)
					failedID := r.s.Staged[1].ID
					r.awaitPreparation("picture did not fail", func() bool { return r.s.StagedByID(failedID).Failed })
					if location == "parked" {
						r.open(grace)
					}
					if location == "hidden" {
						r.s.SwitchAccount(other.Index)
						other.OpenChat(other.Model.Chats.Find([]byte(ada)))
						r.Frames(3)
					}
					if location != "held" {
						r.scr.st.SetText([]byte("unrelated draft"))
						r.scr.stage(r.scr.env, state.StagedFile, []byte("/test/other.txt"))
					}
					endMediaOwner(r, kind)
					u := &r.scr.voice.unsent
					if u.acct.A != nil || len(u.samples) != 0 || u.blob.ID != 0 {
						t.Error("ended owner retained voice data")
					}
					if !c.wasFreed(pictureID) || (voiceID != 0 && !c.wasFreed(voiceID)) {
						t.Error("ended owner's picture or voice blob was not freed")
					}
					if _, ok := r.scr.paste.failed[failedID]; ok {
						t.Error("ended owner's failed PNG retained")
					}
					if location == "hidden" || (location == "parked" && kind == "chat") {
						if r.text() != "unrelated draft" || len(r.s.Staged) != 1 {
							t.Error("unrelated composer was cleared")
						}
					}
					if kind != "remove" {
						restoreMediaChat(r)
						if r.scr.voice.unsentHere(r.scr) || len(r.s.Staged) != 0 {
							t.Error("old media offered to the returning chat/session")
						}
					}
				})
			}
		}
	}
}

func TestStaleMediaPreparationCannotReachANewOwner(t *testing.T) {
	for _, kind := range []string{"unlink", "remove", "chat", "serial"} {
		for _, media := range []string{"voice", "picture"} {
			for _, failed := range []bool{false, true} {
				name := kind + "/" + media + "/ok"
				if failed {
					name = kind + "/" + media + "/error"
				}
				t.Run(name, func(t *testing.T) {
					r := newRig(t, nil, nil)
					other := mediaOtherAccount(r)
					r.open(ada)
					c := &delayedMediaCore{mediaCore: &mediaCore{Conn: r.f}, ready: make(chan abi.BlobRef, 1), release: make(chan struct{}), fail: failed}
					release := sync.OnceFunc(func() { close(c.release) })
					t.Cleanup(release)
					r.a.Conn = c
					if media == "voice" {
						recordMediaVoice(r)
					} else {
						r.dropAt(ui.DropImage, pngOf(2, 2), 1)
					}
					var blob abi.BlobRef
					select {
					case blob = <-c.ready:
					case <-time.After(time.Second):
						t.Fatal("preparation never reached barrier")
					}
					// The owner is hidden when its removal/unlink event arrives.
					r.s.SwitchAccount(other.Index)
					other.OpenChat(other.Model.Chats.Find([]byte(ada)))
					r.Frames(3)
					endMediaOwner(r, kind)
					if kind != "remove" {
						restoreMediaChat(r)
					}
					toasts := len(r.s.Toasts)
					release()
					if media == "voice" {
						select {
						case b := <-r.scr.voice.done:
							r.scr.voice.done <- b
						case <-time.After(time.Second):
							t.Fatal("voice worker did not finish")
						}
						r.scr.voice.takeBlobs(r.scr, r.scr.env)
						if r.scr.voice.unsent.acct.A != nil || len(r.sent(abi.CommandTagSendVoice)) != 0 {
							t.Error("stale voice restored or sent")
						}
					} else {
						select {
						case p := <-r.scr.paste.done:
							r.scr.pictureRead(p)
						case <-time.After(time.Second):
							t.Fatal("picture worker did not finish")
						}
						for _, st := range r.s.Staged {
							if st.Ready || st.Failed {
								t.Error("stale picture restored")
							}
						}
						if len(r.scr.paste.failed) != 0 {
							t.Error("stale PNG/error restored")
						}
					}
					if blob.ID != 0 && !c.wasFreed(blob.ID) {
						t.Error("stale completion's blob was not freed on its original connection")
					}
					if len(r.s.Toasts) != toasts {
						t.Error("stale completion exposed an error to the new owner")
					}
				})
			}
		}
	}
}

func TestUnlinkCancelsFinishingVoice(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.mic.slow = true
	recordMediaVoice(r)
	if !r.scr.voice.sending {
		t.Fatal("recorder did not remain finishing")
	}
	endMediaOwner(r, "unlink")
	if r.scr.voice.sending || r.mic.cancels == 0 {
		t.Fatal("unlink did not cancel finishing recorder")
	}
	restoreMediaChat(r)
	r.mic.ready()
	r.recorded(r.mic.ID())
	r.Frames(3)
	if len(r.sent(abi.CommandTagSendVoice)) != 0 {
		t.Fatal("old recorder sent after relink")
	}
}

func TestRetainedVoiceRequiresItsOriginalSession(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	old := &mediaCore{Conn: r.f}
	r.a.Conn = old
	ref, err := old.PutBlob(make([]byte, 4*core.VoiceRate))
	if err != nil {
		t.Fatal(err)
	}
	r.refuse(ada, func(e *abi.EventSendRefused) {
		e.Unsent.Tag = abi.UnsentTagVoice
		e.Unsent.Voice.Samples = ref
	})
	if !r.scr.voice.unsentHere(r.scr) {
		t.Fatal("voice was not retained")
	}
	nextCore := fake.New(nil)
	next := &mediaCore{Conn: nextCore}
	r.a.Serial += 1 << 32
	r.a.Conn = next
	if r.scr.voice.unsentHere(r.scr) {
		t.Error("old voice offered to replacement session")
	}
	r.scr.voice.resend(r.scr, r.scr.env)
	for _, c := range nextCore.Sent() {
		if c.Tag == abi.CommandTagSendVoice {
			t.Error("old voice retried through replacement core")
		}
	}
	r.scr.voice.dropUnsent()
	if !old.wasFreed(ref.ID) || next.wasFreed(ref.ID) {
		t.Error("voice not freed through its original connection")
	}
}

func TestUnrelatedLifecycleDoesNotCancelVoicePreparation(t *testing.T) {
	for _, kind := range []string{"unlink", "remove", "chat"} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t, nil, nil)
			other := mediaOtherAccount(r)
			r.open(ada)
			b, release := holdBlobs(r)
			recordMediaVoice(r)
			select {
			case <-b.entered:
			case <-time.After(time.Second):
				t.Fatal("preparation did not start")
			}
			switch kind {
			case "unlink":
				other.Event(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
			case "remove":
				other.Event(&abi.Event{Tag: abi.EventTagAccountRemoved})
			case "chat":
				r.f.Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(grace) }))
			}
			r.Frames(3)
			release()
			c := r.waitVoice()
			if !eq(c.SendVoice.Chat, ada) || c.SendVoice.Samples.Len != 4*core.VoiceRate {
				t.Fatal("unrelated lifecycle changed the queued voice")
			}
		})
	}
}
