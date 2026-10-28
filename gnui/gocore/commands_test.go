package gocore

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// allCommands is every command tag of the schema.
func allCommands(t *testing.T) []abi.CommandTag {
	t.Helper()
	var tags []abi.CommandTag
	for i := range 1 << 16 {
		if abi.CommandTag(i).String() != "unknown" {
			tags = append(tags, abi.CommandTag(i))
		}
	}
	if len(tags) < 100 {
		t.Fatalf("only %d command tags", len(tags))
	}
	return tags
}

// valid is a command of tag that Send's own checks pass, its blob (if
// any) put on c.
func valid(t *testing.T, c *Conn, tag abi.CommandTag) *abi.Command {
	t.Helper()
	cmd := &abi.Command{Tag: tag}
	put := func(n int) abi.BlobRef {
		ref, err := c.PutBlob(make([]byte, n))
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	switch tag {
	case abi.CommandTagSendImage:
		cmd.SendImage.Width, cmd.SendImage.Height, cmd.SendImage.RGBA = 1, 2, put(8)
	case abi.CommandTagSendVoice:
		cmd.SendVoice.Samples = put(16)
	case abi.CommandTagSetGroupPicture:
		cmd.SetGroupPicture.Jpeg, cmd.SetGroupPicture.HasJpeg = put(3), true
	case abi.CommandTagApplyPictureEdit:
		cmd.ApplyPictureEdit.Width, cmd.ApplyPictureEdit.Height = 1, 1
		cmd.ApplyPictureEdit.Source.Tag = abi.PictureSourceTagPasted
		cmd.ApplyPictureEdit.Source.Pasted.RGBA = put(4)
	}
	if _, err := cmd.Encode(nil); err != nil {
		t.Fatalf("%v does not encode: %v", tag, err)
	}
	return cmd
}

// Every command an engine does not answer is refused as the Rust demo core
// refuses one, before any blob it names is taken; PickAnswer is the Conn's.
// So a partial engine is safe.
func TestEveryCommandIsRefusedByAnEngineThatAnswersNone(t *testing.T) {
	e := newStub()
	e.table = Answers()
	s := open(t, e)
	for _, tag := range allCommands(t) {
		cmd := valid(t, s.c, tag)
		ref, named := blobOf(cmd)
		err := s.c.Send(cmd)
		if tag == abi.CommandTagPickAnswer {
			if err != nil {
				t.Errorf("PickAnswer: %v", err)
			}
			continue
		}
		want := "the stub core does not answer " + tag.String()
		if demoOnly[tag] {
			want = "that command needs the demo core"
		}
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.EState || ce.Op != "zf_send" || ce.Msg != want {
			t.Errorf("%v: %v, want ZF_E_STATE %q", tag, err, want)
		}
		if named {
			if n, ok := s.c.blobs.Len(ref.ID); !ok || n != ref.Len {
				t.Errorf("%v: the refused command took its blob", tag)
			}
			s.c.FreeBlob(ref.ID)
		}
	}
	if got := e.commands(); len(got) != 0 {
		t.Errorf("the engine got %d commands", len(got))
	}
	for _, tag := range []abi.CommandTag{abi.CommandTagDemoIncoming, abi.CommandTagDemoTyping, abi.CommandTagDemoPanic} {
		if !strings.HasPrefix(tag.String(), "Demo") {
			t.Errorf("%v is not a demo command", tag)
		}
	}
	for _, tag := range allCommands(t) {
		if strings.HasPrefix(tag.String(), "Demo") && !demoOnly[tag] {
			t.Errorf("%v is missing from demoOnly", tag)
		}
	}
}

// An engine that answers everything gets every command but PickAnswer, in
// order, each with the blob it named.
func TestEveryCommandReachesAnEngineThatAnswersAll(t *testing.T) {
	e := newStub()
	e.accept = func(*abi.Command) error { return nil }
	e.command = func(*stub, *Command) bool { return true }
	s := open(t, e)
	var want []abi.CommandTag
	for _, tag := range allCommands(t) {
		cmd := valid(t, s.c, tag)
		ref, named := blobOf(cmd)
		if err := s.c.Send(cmd); err != nil {
			t.Errorf("%v: %v", tag, err)
			continue
		}
		if named && s.c.blobs.Has(ref) {
			t.Errorf("%v: its blob was not taken", tag)
		}
		if tag != abi.CommandTagPickAnswer {
			want = append(want, tag)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(e.commands()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := e.commands()
	if len(got) != len(want) {
		t.Fatalf("the engine got %d of %d commands", len(got), len(want))
	}
	for i, cmd := range got {
		if cmd.Tag != want[i] {
			t.Fatalf("command %d is %v, want %v", i, cmd.Tag, want[i])
		}
		_, named := blobOf(&cmd.Command)
		if named != (cmd.Blob != nil) {
			t.Errorf("%v: named a blob %v, got %d bytes", cmd.Tag, named, len(cmd.Blob))
		}
	}
	if s.c.blobs.Count() != 0 {
		t.Errorf("%d blobs left", s.c.blobs.Count())
	}
}

func TestSendChecksFieldsAndBlobsAsTheRustCoreDoes(t *testing.T) {
	s := open(t, newStub())
	ref, _ := s.c.PutBlob(make([]byte, 16))
	refused := func(what string, cmd *abi.Command, code int32, msg string) {
		t.Helper()
		err := s.c.Send(cmd)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != code || ce.Msg != msg {
			t.Errorf("%s: %v, want %s %q", what, err, core.CodeName(code), msg)
		}
		if !s.c.blobs.Has(ref) {
			t.Fatalf("%s took the blob", what)
		}
	}
	img := &abi.Command{Tag: abi.CommandTagSendImage}
	img.SendImage.Width, img.SendImage.Height, img.SendImage.RGBA = 2, 3, ref
	refused("a 2 × 3 picture of 16 bytes", img, core.ERefused, "rgba is not width × height × 4 bytes")
	img.SendImage.Width, img.SendImage.Height = 1<<31, 1<<31
	refused("a picture whose size wraps", img, core.ERefused, "rgba is not width × height × 4 bytes")

	voice := &abi.Command{Tag: abi.CommandTagSendVoice}
	voice.SendVoice.Samples = abi.BlobRef{ID: ref.ID, Len: 15}
	refused("samples not whole f32s", voice, core.ERefused, "samples are whole little-endian f32s")
	voice.SendVoice.Samples.Len = 15*60*48000*4 + 4
	refused("16 minutes", voice, core.ERefused, "a voice note is at most 15 minutes")
	voice.SendVoice.Samples = abi.BlobRef{ID: ref.ID, Len: 8}
	refused("a blob at another length", voice, core.ENotFound, "blob 1 is not held at the length named")
	voice.SendVoice.Samples = abi.BlobRef{ID: 99, Len: 16}
	refused("a missing blob", voice, core.ENotFound, "blob 99 is not held at the length named")

	loc := &abi.Command{Tag: abi.CommandTagSendLocation}
	loc.SendLocation.Latitude = math.NaN()
	s.e.table[abi.CommandTagSendLocation] = true
	refused("a NaN latitude", loc, core.ERefused, "latitude and longitude must be finite")

	// The engine's own refusals.
	s.e.accept = func(cmd *abi.Command) error {
		if cmd.Tag == abi.CommandTagMarkRead {
			return Refuse("no such chat")
		}
		return errors.New("busy")
	}
	refused("the engine's Refuse", &abi.Command{Tag: abi.CommandTagMarkRead}, core.ERefused, "no such chat")
	refused("another error", &abi.Command{Tag: abi.CommandTagSendText}, core.ERefused, "busy")
	s.e.accept = nil

	// A field over its bound fails as the Rust core's Send did.
	long := &abi.Command{Tag: abi.CommandTagMarkRead}
	long.MarkRead.Chat = make([]byte, 129)
	if err := s.c.Send(long); !errors.Is(err, abi.ErrTooLarge) {
		t.Errorf("a chat id over its bound: %v", err)
	}

	// A good one takes it, and the engine gets the bytes.
	voice.SendVoice.Samples = ref
	if err := s.c.Send(voice); err != nil {
		t.Fatal(err)
	}
	if s.c.blobs.Has(ref) {
		t.Error("the blob is still held")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.e.commands()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := s.e.commands(); len(got) != 1 || len(got[0].Blob) != 16 {
		t.Fatalf("the engine got %d commands", len(got))
	}
}

func TestPickWaitsForItsAnswer(t *testing.T) {
	s := open(t, newStub())
	answered := make(chan []string, 1)
	req := &abi.EventPickRequest{Kind: abi.PickKindFiles, Title: []byte(strings.Repeat("t", 2000))}
	for range 20 {
		req.Filters = append(req.Filters, abi.PickFilter{Name: []byte("Pictures"), Extensions: [][]byte{[]byte("png")}})
	}
	req.Directory, req.HasDirectory = make([]byte, 5000), true
	s.e.h.Go("picker", func(ctx context.Context) { answered <- s.e.h.Pick(ctx, req) })
	s.until("the request", tag(abi.EventTagPickRequest))
	p := s.h.picks[0]
	if len(p.Title) > 1024 || !strings.HasSuffix(string(p.Title), "…") || p.HasDirectory || p.Kind != abi.PickKindFiles {
		t.Errorf("request: title %d bytes, directory %v", len(p.Title), p.HasDirectory)
	}

	// Another id is ignored; the right one answers.
	other := &abi.Command{Tag: abi.CommandTagPickAnswer}
	other.PickAnswer = abi.CommandPickAnswer{ID: p.ID + 1000, Paths: [][]byte{[]byte("/x")}}
	s.send(other)
	answer := &abi.Command{Tag: abi.CommandTagPickAnswer}
	answer.PickAnswer = abi.CommandPickAnswer{ID: p.ID, Paths: [][]byte{[]byte("/a.png"), []byte("/b.png")}}
	s.send(answer)
	answer.PickAnswer.Paths[0][1] = 'X' // the broker copied
	select {
	case paths := <-answered:
		if strings.Join(paths, ",") != "/a.png,/b.png" {
			t.Errorf("paths %q", paths)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pick was never answered")
	}
	s.send(answer) // answered already: ignored
}

func TestCloseAnswersWaitingPicksWithNoPaths(t *testing.T) {
	s := open(t, newStub())
	answered := make(chan []string, 2)
	h := s.e.h
	for range 2 {
		h.Go("picker", func(ctx context.Context) {
			answered <- h.Pick(context.Background(), &abi.EventPickRequest{Title: []byte("Open")})
		})
	}
	s.until("both requests", func(*abi.Event) bool { return len(s.h.picks) == 2 })
	if err := s.c.Close(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if paths := <-answered; paths != nil {
			t.Errorf("paths %q after Close", paths)
		}
	}
	if paths := h.Pick(context.Background(), &abi.EventPickRequest{}); paths != nil {
		t.Errorf("a pick after Close: %q", paths)
	}
	// A pick whose context ends stops waiting.
	s2 := open(t, newStub())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if paths := s2.e.h.Pick(ctx, &abi.EventPickRequest{}); paths != nil {
		t.Errorf("a cancelled pick: %q", paths)
	}
}

func TestAPanickingEngineIsFatalAndPoisonsTheConn(t *testing.T) {
	for _, where := range []string{"command", "goroutine", "accept"} {
		t.Run(where, func(t *testing.T) {
			e := newStub()
			e.command = func(s *stub, cmd *Command) bool {
				if cmd.Tag == abi.CommandTagMarkRead {
					var m map[string]int
					m["boom"]++ // a nil map: panics
				}
				return false
			}
			s := open(t, e)
			cmd := &abi.Command{Tag: abi.CommandTagMarkRead}
			switch where {
			case "command":
				s.send(cmd)
			case "goroutine":
				e.h.Go("worker", func(context.Context) { panic("user data") })
			case "accept":
				e.accept = func(*abi.Command) error { panic("in accept") }
				err := s.c.Send(cmd)
				if !core.IsCode(err, core.EPanic) || !strings.Contains(err.Error(), "poisoned") {
					t.Errorf("Send: %v, want ZF_E_PANIC", err)
				}
			}
			s.until("Fatal", tag(abi.EventTagFatal))
			f := s.h.fatal[0]
			wantPrefix := map[string]string{"command": "engine/engine/", "goroutine": "engine/worker/", "accept": "abi/caller/"}[where]
			if !strings.HasPrefix(f, wantPrefix) || !strings.Contains(f, "gocore.") || strings.Contains(f, "user data") {
				t.Errorf("Fatal %q, want %s… naming the function", f, wantPrefix)
			}
			err := s.c.Send(typing(ada, ada))
			if !core.IsCode(err, core.EPoisoned) {
				t.Errorf("Send after the panic: %v, want ZF_E_POISONED", err)
			}
			if err := s.c.Close(5 * time.Second); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

// Commands and the permit reach the engine in Send's order, on one
// goroutine.
func TestStartIsQueuedInOrderWithTheCommands(t *testing.T) {
	e := newStub()
	e.command = func(*stub, *Command) bool { return true }
	s := open(t, e)
	s.send(&abi.Command{Tag: abi.CommandTagMarkRead})
	if err := s.c.Start(); err != nil {
		t.Fatal(err)
	}
	s.send(&abi.Command{Tag: abi.CommandTagSendText})
	deadline := time.Now().Add(5 * time.Second)
	for len(e.commands()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := s.c.Close(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.order, ","); got != "MarkRead,Start,SendText" {
		t.Errorf("the engine saw %s", got)
	}
}
