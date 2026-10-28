package fake

import (
	"os"
	"reflect"
	"testing"

	"zapfast/abi"
	"zapfast/core"
)

const fixtures = "../../abi/testdata/events.bin" // written by zfcore's Rust encoder (abi/abi_test.go)

// log records what a screen would: the tags, and copies of a few fields.
type log struct {
	tags  []abi.EventTag
	chats []string
	msgs  []string
	f     *Core // answers PickRequest from inside Drain, as the smoke app does
}

func (l *log) Event(e *abi.Event) {
	l.tags = append(l.tags, e.Tag)
	switch e.Tag {
	case abi.EventTagChats:
		for i := range e.Chats.Chats {
			l.chats = append(l.chats, string(e.Chats.Chats[i].Name))
		}
	case abi.EventTagMessages:
		for i := range e.Messages.Messages {
			l.msgs = append(l.msgs, string(e.Messages.Messages[i].Content.Text.Text))
		}
	case abi.EventTagPickRequest:
		if l.f != nil {
			var cmd abi.Command
			cmd.Tag = abi.CommandTagPickAnswer
			cmd.PickAnswer.ID = e.PickRequest.ID
			l.f.Send(&cmd)
		}
	}
}

// The ABI fixtures hold every event variant: all of them replay.
func TestReplaysEveryFixtureEvent(t *testing.T) {
	stream, err := os.ReadFile(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	f := New(nil)
	if err := f.Replay(stream); err != nil {
		t.Fatal(err)
	}
	queued := f.Pending()
	var l log
	n, err := f.Drain(&l, 0)
	if err != nil || n != queued || f.Skipped() != 0 {
		t.Fatalf("drained %d of %d (%d skipped): %v", n, queued, f.Skipped(), err)
	}
	seen := map[abi.EventTag]bool{}
	for _, tag := range l.tags {
		seen[tag] = true
	}
	for tag := abi.EventTag(1); tag < 1000; tag++ {
		if tag.String() != "unknown" && !seen[tag] {
			t.Errorf("no %v replayed", tag)
		}
	}
}

func chat(id, name string) abi.Chat { return abi.Chat{ID: []byte(id), Name: []byte(name)} }

func text(chatID, id, body string) abi.Message {
	var m abi.Message
	m.ID, m.Chat, m.Sender = []byte(id), []byte(chatID), []byte(chatID)
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(body)
	return m
}

// A screen's session, scripted: the chat list at open, a chat's messages in
// answer to LoadChat, a dialog answered from inside Drain, and the commands
// recorded in order. Two runs give the same result.
func TestScriptedSessionIsDeterministic(t *testing.T) {
	run := func() (log, []abi.CommandTag) {
		wakes := 0
		f := New(func() { wakes++ })
		var chats abi.Event
		chats.Tag = abi.EventTagChats
		chats.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat("1@s.whatsapp.net", "Ada"), chat("2@s.whatsapp.net", "Grace")}}
		if err := f.Emit(&chats); err != nil {
			t.Fatal(err)
		}
		var msgs abi.Event
		msgs.Tag = abi.EventTagMessages
		msgs.Messages = abi.EventMessages{Chat: []byte("1@s.whatsapp.net"), Batch: 1, Last: true, Complete: true,
			Messages: []abi.Message{text("1@s.whatsapp.net", "a", "hello"), text("1@s.whatsapp.net", "b", "again")}}
		var pick abi.Event
		pick.Tag = abi.EventTagPickRequest
		pick.PickRequest.ID = 7
		if err := f.Reply(abi.CommandTagLoadChat, &msgs, &pick); err != nil {
			t.Fatal(err)
		}
		if wakes != 1 {
			t.Errorf("%d wakes after one Emit", wakes)
		}
		l := log{f: f}
		f.Drain(&l, 0)
		if err := f.Start(); err != nil {
			t.Fatal(err)
		}
		var cmd abi.Command
		cmd.Tag = abi.CommandTagLoadChat
		cmd.LoadChat.Chat = []byte("1@s.whatsapp.net")
		if err := f.Send(&cmd); err != nil {
			t.Fatal(err)
		}
		f.Drain(&l, 0) // the messages, then the dialog, answered in the same call
		sent := f.Sent()
		if len(sent) != 2 || string(sent[0].LoadChat.Chat) != "1@s.whatsapp.net" || sent[1].PickAnswer.ID != 7 {
			t.Errorf("sent %+v", sent)
		}
		return l, f.SentTags()
	}
	l1, tags1 := run()
	l2, tags2 := run()
	if !reflect.DeepEqual(l1.chats, []string{"Ada", "Grace"}) || !reflect.DeepEqual(l1.msgs, []string{"hello", "again"}) {
		t.Errorf("chats %q, messages %q", l1.chats, l1.msgs)
	}
	if want := []abi.CommandTag{abi.CommandTagLoadChat, abi.CommandTagPickAnswer}; !reflect.DeepEqual(tags1, want) {
		t.Errorf("tags %v, want %v", tags1, want)
	}
	l1.f, l2.f = nil, nil
	if !reflect.DeepEqual(l1, l2) || !reflect.DeepEqual(tags1, tags2) {
		t.Error("two runs of the same script differ")
	}
}

func TestRefusalsBlobsAndClose(t *testing.T) {
	var f core.Conn = New(nil)
	fk := f.(*Core)
	fk.Refuse(abi.CommandTagUnlink, core.EState, "the demo core does not answer Unlink")
	var cmd abi.Command
	cmd.Tag = abi.CommandTagUnlink
	if err := f.Send(&cmd); !core.IsCode(err, core.EState) {
		t.Errorf("a refused command: %v", err)
	}
	if len(fk.Sent()) != 0 {
		t.Error("a refused command was recorded")
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(); !core.IsCode(err, core.EState) {
		t.Errorf("a second Start: %v", err)
	}
	ref, _ := f.PutBlob([]byte("0123456789"))
	buf := make([]byte, 4)
	if n, err := f.ReadBlob(ref.ID, 8, buf); n != 2 || err != nil || string(buf[:n]) != "89" {
		t.Errorf("a ranged read: %q, %v", buf[:n], err)
	}
	if _, err := f.ReadBlob(ref.ID, 11, buf); !core.IsCode(err, core.ERange) {
		t.Errorf("past the end: %v", err)
	}
	f.FreeBlob(ref.ID)
	if _, err := f.ReadBlob(ref.ID, 0, buf); !core.IsCode(err, core.ENotFound) {
		t.Errorf("a freed blob: %v", err)
	}
	if err := f.Close(0); err != nil {
		t.Fatal(err)
	}
	if err := f.Send(&cmd); err != core.ErrClosed {
		t.Errorf("Send after Close: %v", err)
	}
	if _, err := f.Drain(&log{}, 0); err != core.ErrClosed {
		t.Errorf("Drain after Close: %v", err)
	}
}

// A malformed record is skipped and counted, as the real Drain does.
func TestAMalformedRecordIsSkipped(t *testing.T) {
	f := New(nil)
	var e abi.Event
	e.Tag = abi.EventTagSyncing
	good, _ := e.Encode(nil)
	stream := abi.AppendFrame(nil, []byte{0xff, 0xff, 0, 0})
	stream = abi.AppendFrame(stream, good)
	if err := f.Replay(stream); err != nil {
		t.Fatal(err)
	}
	var l log
	if n, _ := f.Drain(&l, 0); n != 1 || f.Skipped() != 1 || l.tags[0] != abi.EventTagSyncing {
		t.Errorf("drained %d, skipped %d, tags %v", n, f.Skipped(), l.tags)
	}
}

func TestScriptedFramesAreHeldOneAtATime(t *testing.T) {
	f := New(nil)
	var src core.Frames = f
	if _, ok := src.Frame(core.StreamRemote); ok {
		t.Fatal("a frame before one was scripted")
	}
	f.SetFrame(core.StreamRemote, TestPattern(64, 36, 1))
	fr, ok := src.Frame(core.StreamRemote)
	if !ok || fr.W != 64 || fr.H != 36 || fr.Format != core.FrameI420 || len(fr.Px) != 64*36+2*32*18 {
		t.Fatalf("%v %d×%d %d bytes", ok, fr.W, fr.H, len(fr.Px))
	}
	if _, ok := src.Frame(core.StreamRemote); ok || !f.Held(core.StreamRemote) {
		t.Fatal("a second frame while one is held")
	}
	src.ReleaseFrame(core.StreamRemote)
	if _, ok := src.Frame(core.StreamRemote); !ok || f.Acquired(core.StreamRemote) != 2 {
		t.Fatalf("after release: %v, %d", ok, f.Acquired(core.StreamRemote))
	}
	src.ReleaseFrame(core.StreamRemote)
	f.ClearFrame(core.StreamRemote)
	if _, ok := src.Frame(core.StreamRemote); ok {
		t.Fatal("a cleared stream's frame")
	}
	var calls core.CallNumbers = f
	if a, b := calls.CallGeneration(), calls.CallGeneration(); a == 0 || b <= a {
		t.Fatalf("generations %d, %d", a, b)
	}
}
