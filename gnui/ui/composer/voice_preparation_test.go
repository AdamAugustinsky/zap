package composer

import (
	"bytes"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
)

func TestVoiceBlobFailureKeepsPCMUntilRetry(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	b := &blobFailure{Conn: r.f}
	b.fail.Store(true)
	r.a.Conn = b
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"))
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = 2 * time.Second
	want := core.AppendVoicePCM16(nil, pcm16(r.mic.el, 0.25), 48000, 1)
	r.scr.voice.send(r.scr, r.scr.env)
	r.awaitPreparation("voice preparation error not reported", func() bool { return r.toasted("test blob storage unavailable") })
	if !r.scr.voice.unsentHere(r.scr) || !r.hasText("Voice message (0:02) not sent") || r.s.Panes.Reply.Len() != 1 {
		t.Fatal("failed PutBlob lost recording or reply")
	}
	r.scr.voice.resend(r.scr, r.scr.env)
	r.awaitPreparation("failed retry lost recording", func() bool { return r.scr.voice.unsentHere(r.scr) })
	if len(r.sent(abi.CommandTagSendVoice)) != 0 || r.s.Panes.Reply.Len() != 1 {
		t.Fatal("failed retry sent empty samples or lost the quote")
	}
	r.recreate()
	b.fail.Store(false)
	r.scr.voice.resend(r.scr, r.scr.env)
	c := r.waitVoice()
	got, ok := r.blob(c.SendVoice.Samples)
	if !ok || !bytes.Equal(got, want) || !c.SendVoice.HasQuoting || !eq(c.SendVoice.Quoting.ID, "m1") || r.scr.voice.unsentHere(r.scr) || r.s.Panes.Reply.Len() != 0 {
		t.Fatal("retry lost PCM/quote or failed to clear the unsent recording")
	}
}

func TestPendingVoicePreparationSurvivesWindowRecreation(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	b, release := holdBlobs(r)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = time.Second
	want := core.AppendVoicePCM16(nil, pcm16(r.mic.el, 0.25), 48000, 1)
	r.scr.voice.send(r.scr, r.scr.env)
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("voice preparation did not reach the blob boundary")
	}
	r.recreate()
	release()
	c := r.waitVoice()
	if pcm, ok := r.blob(c.SendVoice.Samples); !ok || !bytes.Equal(pcm, want) {
		t.Fatal("voice samples lost across recreation")
	}
}

func TestUnpreparedVoiceCanBeDiscarded(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	b := &blobFailure{Conn: r.f}
	b.fail.Store(true)
	r.a.Conn = b
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = time.Second
	r.scr.voice.send(r.scr, r.scr.env)
	r.awaitPreparation("failed preparation lost voice", func() bool { return r.scr.voice.unsentHere(r.scr) })
	puts := b.puts.Load()
	r.Frames(20)
	if b.puts.Load() != puts {
		t.Fatal("failed preparation retries without an explicit Send")
	}
	r.scr.voice.dropUnsent()
	b.fail.Store(false)
	r.scr.voice.resend(r.scr, r.scr.env)
	if r.scr.voice.unsentHere(r.scr) || len(r.sent(abi.CommandTagSendVoice)) != 0 || b.puts.Load() != puts {
		t.Fatal("discarded voice was retried")
	}
}
