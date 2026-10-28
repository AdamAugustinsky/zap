package composer

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
)

// sendFault exercises both the real ABI encoder and an immediate core refusal.
// It never changes the composer's inputs, only the command at the boundary.
type sendFault struct {
	core.Conn
	tag     abi.CommandTag
	encode  bool
	after   int
	attempt int
	err     error
	on      bool
}

func (f *sendFault) Send(cmd *abi.Command) error {
	if !f.on || cmd.Tag != f.tag {
		return f.Conn.Send(cmd)
	}
	f.attempt++
	if f.attempt <= f.after {
		return f.Conn.Send(cmd)
	}
	f.err = errors.New("test immediate core refusal")
	if f.encode {
		bad := *cmd
		chat := bytes.Repeat([]byte("x"), 129) // ABI Chat maximum: 128
		switch bad.Tag {
		case abi.CommandTagSendText:
			bad.SendText.Chat = chat
		case abi.CommandTagEditText:
			bad.EditText.Chat = chat
		case abi.CommandTagSendFiles:
			bad.SendFiles.Chat = chat
		case abi.CommandTagSendImage:
			bad.SendImage.Chat = chat
		case abi.CommandTagSendVoice:
			bad.SendVoice.Chat = chat
		}
		f.err = f.Conn.Send(&bad)
	}
	return f.err
}

func stageFiles(r *rig, n int, kind state.StagedKind) {
	for i := 0; i < n; i++ {
		r.scr.stage(r.scr.env, kind, []byte(fmt.Sprintf("/test/file-%02d", i)))
	}
}

func TestFilesSendInABIBatches(t *testing.T) {
	for _, kind := range []state.StagedKind{state.StagedFile, state.StagedDocument} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.open(ada)
			stageFiles(r, 65, kind)
			r.s.Panes.Reply.Add([]byte("m1"))
			r.scr.st.SetText([]byte("caption"))
			ch, _ := r.a.Model.Chats.Get(r.a.Open)
			r.scr.send(r.scr.env, r.a, ch)
			sent := r.sent(abi.CommandTagSendFiles)
			if len(sent) != 2 {
				t.Fatalf("65 files sent in %d commands, want 2", len(sent))
			}
			for batch, cmd := range sent {
				f := cmd.SendFiles
				want := 64
				if batch == 1 {
					want = 1
				}
				if len(f.Paths) != want || f.Documents != (kind == state.StagedDocument) || f.HasCaption != (batch == 0) || f.HasQuoting != (batch == 0) {
					t.Fatalf("batch %d: %+v", batch, f)
				}
				for i, p := range f.Paths {
					if string(p) != fmt.Sprintf("/test/file-%02d", batch*64+i) {
						t.Fatalf("batch %d path %d: %q", batch, i, p)
					}
				}
			}
			if !eq(sent[0].SendFiles.Caption, "caption") || !eq(sent[0].SendFiles.Quoting.ID, "m1") || len(r.s.Staged) != 0 || r.text() != "" || r.s.Panes.Reply.Len() != 0 {
				t.Fatal("caption/reply or successful-send cleanup lost")
			}
			if len(r.s.Toasts) != 1 || string(r.s.Toasts[0].Text) != "Sending 65 files…" {
				t.Fatalf("toasts: %+v", r.s.Toasts)
			}
		})
	}
}

func TestSynchronousSendFailureKeepsInputs(t *testing.T) {
	for _, encode := range []bool{true, false} {
		for _, tag := range []abi.CommandTag{abi.CommandTagSendText, abi.CommandTagEditText, abi.CommandTagSendFiles, abi.CommandTagSendImage, abi.CommandTagSendVoice} {
			t.Run(fmt.Sprintf("%s/encode=%v", tag, encode), func(t *testing.T) {
				r := newRig(t, nil, nil)
				r.open(ada)
				r.messages(ada, textMsg("m1", me, true, r.now.Unix()-60, "original"))
				if tag == abi.CommandTagEditText {
					r.s.Panes.Edit.Add([]byte("m1"))
				} else {
					r.s.Panes.Reply.Add([]byte("m1"))
				}
				r.Frames(2)
				fault := &sendFault{Conn: r.f, tag: tag, encode: encode, on: true}
				r.a.Conn = fault
				if tag == abi.CommandTagSendVoice {
					r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
					r.mic.el = 2 * time.Second
					r.scr.voice.send(r.scr, r.scr.env)
					for i := 0; i < 400 && fault.attempt == 0; i++ {
						time.Sleep(time.Millisecond)
						r.Frames(1)
					}
				} else {
					r.scr.st.SetText([]byte("keep this text"))
					if tag != abi.CommandTagEditText {
						r.a.Model.SetDraft(r.scr.chat, r.scr.st.Text())
					}
					if tag == abi.CommandTagSendFiles {
						stageFiles(r, 2, state.StagedFile)
					}
					if tag == abi.CommandTagSendImage {
						r.dropAt(ui.DropImage, pngOf(2, 2), 1)
						r.waitPictures()
					}
					ch, _ := r.a.Model.Chats.Get(r.a.Open)
					r.scr.send(r.scr.env, r.a, ch)
				}
				if fault.attempt != 1 || fault.err == nil || len(r.sent(tag)) != 0 {
					t.Fatalf("fault not exercised: attempts=%d err=%v", fault.attempt, fault.err)
				}
				if len(r.s.Toasts) != 1 || !r.s.Toasts[0].Error || !bytes.Contains(r.s.Toasts[0].Text, []byte(fault.err.Error())) {
					t.Errorf("send error not shown: %+v", r.s.Toasts)
				}
				if tag == abi.CommandTagEditText {
					if r.s.Panes.Edit.Len() != 1 || !eq(r.scr.edit, "m1") {
						t.Error("failed edit closed")
					}
				} else if r.s.Panes.Reply.Len() != 1 || !eq(r.s.Panes.Reply.At(0), "m1") {
					t.Error("reply lost")
				}
				switch tag {
				case abi.CommandTagSendFiles:
					if len(r.s.Staged) != 2 {
						t.Fatal("files lost")
					}
				case abi.CommandTagSendImage:
					if len(r.s.Staged) != 1 || !r.s.Staged[0].Ready {
						t.Fatal("picture lost")
					}
					if px, ok := r.blob(r.s.Staged[0].Blob); !ok || len(px) != 16 {
						t.Fatal("picture blob freed")
					}
				case abi.CommandTagSendVoice:
					if !r.scr.voice.unsentHere(r.scr) {
						t.Fatal("recording lost")
					}
					if pcm, ok := r.blob(r.scr.voice.unsent.blob); !ok || len(pcm) != 8*core.VoiceRate {
						t.Fatal("voice blob freed")
					}
					// The retry path must retain the same inputs on failure too.
					r.scr.voice.resend(r.scr, r.scr.env)
					if !r.scr.voice.unsentHere(r.scr) || r.s.Panes.Reply.Len() != 1 {
						t.Fatal("failed voice retry lost inputs")
					}
				}
				if tag != abi.CommandTagSendVoice && (r.text() != "keep this text" || (tag != abi.CommandTagEditText && !eq(r.a.Model.Draft(r.scr.chat), "keep this text"))) {
					t.Fatal("text/caption or stored draft lost")
				}
				fault.on = false
				if tag == abi.CommandTagSendVoice {
					r.scr.voice.resend(r.scr, r.scr.env)
				} else {
					ch, _ := r.a.Model.Chats.Get(r.a.Open)
					r.scr.send(r.scr.env, r.a, ch)
				}
				if len(r.sent(tag)) != 1 || len(r.s.Staged) != 0 || r.text() != "" || r.s.Panes.Reply.Len() != 0 || r.s.Panes.Edit.Len() != 0 || r.scr.voice.unsentHere(r.scr) {
					t.Fatal("retry did not send and clear inputs")
				}
			})
		}
	}
}

func TestLaterBatchFailureKeepsOnlyUnsentInputs(t *testing.T) {
	for _, encode := range []bool{true, false} {
		t.Run(fmt.Sprint(encode), func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.open(ada)
			stageFiles(r, 65, state.StagedFile)
			r.s.Panes.Reply.Add([]byte("m1"))
			r.scr.st.SetText([]byte("caption"))
			fault := &sendFault{Conn: r.f, tag: abi.CommandTagSendFiles, encode: encode, after: 1, on: true}
			r.a.Conn = fault
			ch, _ := r.a.Model.Chats.Get(r.a.Open)
			r.scr.send(r.scr.env, r.a, ch)
			if fault.attempt != 2 || len(r.sent(abi.CommandTagSendFiles)) != 1 || len(r.s.Staged) != 1 || !eq(r.s.Staged[0].Path, "/test/file-64") {
				t.Fatalf("accepted batch or unsent remainder lost: attempts=%d, staged=%d", fault.attempt, len(r.s.Staged))
			}
			if r.text() != "" || r.s.Panes.Reply.Len() != 0 || len(r.s.Toasts) != 1 || !r.s.Toasts[0].Error {
				t.Fatal("accepted caption/quote kept for duplicate send, or failure not shown")
			}
			fault.on = false
			r.scr.send(r.scr.env, r.a, ch)
			sent := r.sent(abi.CommandTagSendFiles)
			if len(sent) != 2 || len(sent[1].SendFiles.Paths) != 1 || !eq(sent[1].SendFiles.Paths[0], "/test/file-64") || sent[1].SendFiles.HasCaption || sent[1].SendFiles.HasQuoting || len(r.s.Staged) != 0 {
				t.Fatal("retry duplicated an accepted batch, caption or quote")
			}
		})
	}
}

func TestFileBatchesPreserveStagedOrder(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	stageFiles(r, 65, state.StagedFile)
	r.dropAt(ui.DropImage, pngOf(2, 2), 1)
	r.waitPictures()
	stageFiles(r, 2, state.StagedDocument)
	stageFiles(r, 1, state.StagedFile)
	r.s.Panes.Reply.Add([]byte("m1"))
	r.scr.st.SetText([]byte("first attachment only"))
	ch, _ := r.a.Model.Chats.Get(r.a.Open)
	r.scr.send(r.scr.env, r.a, ch)
	var sends []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == abi.CommandTagSendFiles || c.Tag == abi.CommandTagSendImage {
			sends = append(sends, c)
		}
	}
	if len(sends) != 5 {
		t.Fatalf("got %d sends, want two file batches, picture, documents, file", len(sends))
	}
	for i, n := range []int{64, 1, 0, 2, 1} {
		c := sends[i]
		if n == 0 {
			if c.Tag != abi.CommandTagSendImage || c.SendImage.HasCaption || c.SendImage.HasQuoting {
				t.Fatalf("picture out of order or duplicated caption/quote: %+v", c.SendImage)
			}
		} else if c.Tag != abi.CommandTagSendFiles || len(c.SendFiles.Paths) != n || c.SendFiles.Documents != (i == 3) || c.SendFiles.HasCaption != (i == 0) || c.SendFiles.HasQuoting != (i == 0) {
			t.Fatalf("batch %d out of order or duplicated caption/quote: %+v", i, c.SendFiles)
		}
	}
}

func TestMixedAttachmentsKeepFirstCaptionAndQuote(t *testing.T) {
	for _, kinds := range [][]state.StagedKind{
		{state.StagedFile, state.StagedPicture},
		{state.StagedDocument, state.StagedPicture, state.StagedFile, state.StagedPicture, state.StagedDocument},
		{state.StagedPicture, state.StagedFile, state.StagedPicture},
	} {
		t.Run(fmt.Sprint(kinds), func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.open(group)
			for _, kind := range kinds {
				if kind == state.StagedPicture {
					r.dropAt(ui.DropImage, pngOf(2, 2), 1)
				} else {
					stageFiles(r, 1, kind)
				}
			}
			r.waitPictures()
			r.s.Panes.Reply.Add([]byte("m1"))
			r.scr.st.SetText([]byte("@1 first only"))
			ch, _ := r.a.Model.Chats.Get(r.a.Open)
			r.scr.send(r.scr.env, r.a, ch)
			i := 0
			for _, c := range r.f.Sent() {
				var kind state.StagedKind
				var caption []byte
				var mentions [][]byte
				var quote abi.ReplyTarget
				var quoted bool
				switch c.Tag {
				case abi.CommandTagSendFiles:
					if c.SendFiles.Documents {
						kind = state.StagedDocument
					}
					caption, mentions, quote, quoted = c.SendFiles.Caption, c.SendFiles.Mentions, c.SendFiles.Quoting, c.SendFiles.HasQuoting
				case abi.CommandTagSendImage:
					kind = state.StagedPicture
					caption, mentions, quote, quoted = c.SendImage.Caption, c.SendImage.Mentions, c.SendImage.Quoting, c.SendImage.HasQuoting
				default:
					continue
				}
				if i >= len(kinds) || kind != kinds[i] {
					t.Fatalf("send %d has kind %v, staged %v", i, kind, kinds)
				}
				if i == 0 {
					if !eq(caption, "@1 first only") || !quoted || !eq(quote.ID, "m1") || len(mentions) != 1 || !eq(mentions[0], ada) {
						t.Fatal("first attachment lost caption, mentions or quote")
					}
				} else if len(caption) != 0 || quoted || len(mentions) != 0 {
					t.Fatalf("attachment %d repeated caption/mentions/quote", i)
				}
				i++
			}
			if i != len(kinds) || len(r.s.Staged) != 0 {
				t.Fatal("attachments lost or left staged")
			}
		})
	}
}
