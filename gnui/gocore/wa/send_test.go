package wa

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/av"
	"zapfast/gocore/store"
)

func chatID(j types.JID) []byte { return []byte(j.String()) }

func TestSendText(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "Q1", t0, text("question")))
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{
		Chat:       chatID(peer),
		Text:       []byte("answer @4917600000002"),
		Quoting:    abi.ReplyTarget{ID: []byte("Q1")},
		HasQuoting: true,
		Mentions:   [][]byte{chatID(peer2), []byte("not a jid"), chatID(peer2)},
	}})
	evs := h.take()
	expectTags(t, evs, "Messages ChatUpdated MessageUpdated ChatUpdated")
	pending := evs[0].Messages.Messages[0]
	if !pending.FromMe || pending.Status != abi.DeliveryPending || string(pending.Content.Text.Text) != "answer @4917600000002" || string(pending.Sender) != me.String() {
		t.Fatalf("pending: %+v", pending)
	}
	if !pending.HasQuoted || string(pending.Quoted.ID) != "Q1" || string(pending.Quoted.Summary) != "question" || len(pending.Mentions) != 1 {
		t.Fatalf("quote and mentions: %+v", pending)
	}
	if m := evs[2].MessageUpdated.Message; m.Status != abi.DeliverySent || string(m.ID) != string(pending.ID) {
		t.Fatalf("sent: %+v", m)
	}
	sent := h.f.lastSent()
	ctx := sent.msg.GetExtendedTextMessage().GetContextInfo()
	if sent.chat != peer || sent.id != string(pending.ID) || ctx.GetStanzaID() != "Q1" || ctx.GetQuotedMessage().GetConversation() != "question" || len(ctx.GetMentionedJID()) != 1 || ctx.GetMentionedJID()[0] != peer2.String() {
		t.Fatalf("wire: %v", sent.msg)
	}

	// The phone refuses: the row fails and the reader hears why.
	h.f.sendErr = errors.New("server said no")
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(peer), Text: []byte("again")}})
	evs = h.take()
	expectTags(t, evs, "Messages ChatUpdated MessageUpdated ChatUpdated Error")
	if evs[2].MessageUpdated.Message.Status != abi.DeliveryFailed {
		t.Fatal("not failed")
	}
}

func TestSendRefused(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	// A quote that is not here.
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(peer), Text: []byte("hi"), Quoting: abi.ReplyTarget{ID: []byte("gone"), Chat: chatID(group), HasChat: true}, HasQuoting: true}})
	r := one(t, h.take(), abi.EventTagSendRefused).SendRefused
	if r.Reason != abi.RefusalQuoteUnavailable || string(r.Unsent.Text.Text) != "hi" || string(r.Quoting.Chat) != group.String() || !r.HasQuoting {
		t.Fatalf("quote refusal: %+v", r)
	}
	// Offline: every kind comes back to the composer.
	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(peer), Text: []byte("later")}})
	if r := one(t, h.take(), abi.EventTagSendRefused).SendRefused; r.Reason != abi.RefusalOffline || r.Unsent.Tag != abi.UnsentTagText {
		t.Fatalf("offline: %+v", r)
	}
	h.send(abi.Command{Tag: abi.CommandTagSendLocation, SendLocation: abi.CommandSendLocation{Chat: chatID(peer), Latitude: 1, Longitude: 2}})
	if r := one(t, h.take(), abi.EventTagSendRefused).SendRefused; r.Unsent.Tag != abi.UnsentTagLocation || r.Unsent.Location.Longitude != 2 {
		t.Fatalf("location: %+v", r)
	}
	rgba := bytes.Repeat([]byte{1, 2, 3, 255}, 4)
	ref, _ := h.c.PutBlob(rgba)
	h.send(abi.Command{Tag: abi.CommandTagSendImage, SendImage: abi.CommandSendImage{Chat: chatID(peer), Width: 2, Height: 2, RGBA: ref, Caption: []byte("c"), HasCaption: true}})
	r = one(t, h.take(), abi.EventTagSendRefused).SendRefused
	if r.Unsent.Tag != abi.UnsentTagImage || r.Unsent.Image.Width != 2 || string(r.Unsent.Image.Caption) != "c" {
		t.Fatalf("image: %+v", r)
	}
	back := make([]byte, 16)
	if n, err := h.c.ReadBlob(r.Unsent.Image.RGBA.ID, 0, back); err != nil || n != 16 || !bytes.Equal(back, rgba) {
		t.Fatalf("the pixels come back: %v %v", n, err)
	}
	pcm := av.PCM(make([]float32, 480))
	ref, _ = h.c.PutBlob(pcm)
	h.send(abi.Command{Tag: abi.CommandTagSendVoice, SendVoice: abi.CommandSendVoice{Chat: chatID(peer), Samples: ref}})
	r = one(t, h.take(), abi.EventTagSendRefused).SendRefused
	if r.Unsent.Tag != abi.UnsentTagVoice || r.Unsent.Voice.Samples.Len != uint64(len(pcm)) {
		t.Fatalf("voice: %+v", r)
	}
	h.send(abi.Command{Tag: abi.CommandTagSendFiles, SendFiles: abi.CommandSendFiles{Chat: chatID(peer), Paths: [][]byte{[]byte("/tmp/x")}, Documents: true}})
	if r := one(t, h.take(), abi.EventTagSendRefused).SendRefused; r.Unsent.Tag != abi.UnsentTagFiles || !r.Unsent.Files.Documents {
		t.Fatalf("files: %+v", r)
	}
	if h.f.sentCount() != 0 {
		t.Fatal("something went out")
	}
	// A chat that takes no messages is refused outright.
	if err := h.e.arch.UpsertChat(store.Chat{ID: group.String(), Kind: "group", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	h.f.setConnected(true)
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(group), Text: []byte("hi")}})
	if got := lastError(t, h.take()); got != readOnly {
		t.Fatalf("read only: %q", got)
	}
}

func TestSendLocation(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagSendLocation, SendLocation: abi.CommandSendLocation{Chat: chatID(peer), Latitude: 52.5, Longitude: 13.4}})
	evs := h.take()
	expectTags(t, evs, "Messages ChatUpdated MessageUpdated ChatUpdated")
	m := evs[0].Messages.Messages[0]
	if m.Content.Tag != abi.ContentTagLocation || m.Content.Location.Latitude != 52.5 || m.Content.Location.HasName {
		t.Fatalf("location: %+v", m.Content)
	}
	if loc := h.f.lastSent().msg.GetLocationMessage(); loc.GetDegreesLongitude() != 13.4 {
		t.Fatalf("wire: %v", loc)
	}
}

func TestSendImage(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	// A disappearing chat gives its timer to what is sent there.
	if err := h.e.arch.UpsertChat(store.Chat{ID: peer.String(), Kind: "direct", EphemeralSeconds: 86400}); err != nil {
		t.Fatal(err)
	}
	const w, hgt = 40, 30
	rgba := make([]byte, w*hgt*4)
	for i := range rgba {
		rgba[i] = byte(i)
	}
	ref, err := h.c.PutBlob(rgba)
	if err != nil {
		t.Fatal(err)
	}
	h.send(abi.Command{Tag: abi.CommandTagSendImage, SendImage: abi.CommandSendImage{Chat: chatID(peer), Width: w, Height: hgt, RGBA: ref, Caption: []byte("pasted"), HasCaption: true, Mentions: [][]byte{chatID(peer2)}}})
	evs := h.take()
	expectTags(t, evs, "Messages ChatUpdated MessageUpdated ChatUpdated")
	if h.f.uploads[0] != whatsmeow.MediaImage {
		t.Fatalf("upload: %v", h.f.uploads)
	}
	jpg := h.f.uploaded[0]
	cfg, format, err := image.DecodeConfig(bytes.NewReader(jpg))
	if err != nil || format != "jpeg" || cfg.Width != w || cfg.Height != hgt {
		t.Fatalf("encoded: %v %s %+v", err, format, cfg)
	}
	img := h.f.lastSent().msg.GetImageMessage()
	if img.GetWidth() != w || img.GetMimetype() != "image/jpeg" || img.GetCaption() != "pasted" || len(img.GetJPEGThumbnail()) == 0 || img.GetContextInfo().GetExpiration() != 86400 || len(img.GetContextInfo().GetMentionedJID()) != 1 {
		t.Fatalf("wire: %v", img)
	}
	m := evs[0].Messages.Messages[0]
	if m.Content.Tag != abi.ContentTagImage || !m.Content.Image.Media.HasPath || !m.HasThumbnail {
		t.Fatalf("row: %+v", m)
	}
	if data, err := os.ReadFile(string(m.Content.Image.Media.Path)); err != nil || !bytes.Equal(data, jpg) {
		t.Fatalf("the sent picture is kept: %v", err)
	}
}

func TestSendFiles(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	dir := t.TempDir()
	var pic bytes.Buffer
	m := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	m.Set(1, 1, color.NRGBA{255, 0, 0, 255})
	png.Encode(&pic, m)
	files := map[string][]byte{"photo.png": pic.Bytes(), "notes.txt": []byte("plain words"), "clip.mp4": []byte("\x00\x00\x00\x18ftypmp42")}
	var paths [][]byte
	for _, name := range []string{"photo.png", "notes.txt", "clip.mp4"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, files[name], 0o600)
		paths = append(paths, []byte(p))
	}
	h.inject(message(peer, peer, "Q1", t0, text("send me files")))
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagSendFiles, SendFiles: abi.CommandSendFiles{Chat: chatID(peer), Paths: paths, Caption: []byte("three files"), HasCaption: true, Quoting: abi.ReplyTarget{ID: []byte("Q1")}, HasQuoting: true}})
	h.take()
	if len(h.f.sent) != 3 {
		t.Fatalf("sent %d", len(h.f.sent))
	}
	byKind := map[string]*waE2E.Message{}
	for _, s := range h.f.sent {
		switch {
		case s.msg.GetImageMessage() != nil:
			byKind["image"] = s.msg
		case s.msg.GetDocumentMessage() != nil:
			byKind["document"] = s.msg
		case s.msg.GetVideoMessage() != nil:
			byKind["video"] = s.msg
		}
	}
	img, doc := byKind["image"].GetImageMessage(), byKind["document"].GetDocumentMessage()
	if img.GetWidth() != 8 || img.GetMimetype() != "image/png" || img.GetCaption() != "three files" || img.GetContextInfo().GetStanzaID() != "Q1" {
		t.Fatalf("image: %v", img)
	}
	if doc.GetFileName() != "notes.txt" || doc.GetCaption() != "" || doc.GetContextInfo() != nil || byKind["video"] == nil {
		t.Fatalf("document: %v", doc)
	}

	// As documents, a picture stays a file.
	h.send(abi.Command{Tag: abi.CommandTagSendFiles, SendFiles: abi.CommandSendFiles{Chat: chatID(peer), Paths: paths[:1], Documents: true}})
	h.take()
	if d := h.f.lastSent().msg.GetDocumentMessage(); d.GetFileName() != "photo.png" {
		t.Fatalf("as a document: %v", h.f.lastSent().msg)
	}
	h.send(abi.Command{Tag: abi.CommandTagSendFiles, SendFiles: abi.CommandSendFiles{Chat: chatID(peer), Paths: [][]byte{[]byte(filepath.Join(dir, "missing"))}}})
	if got := lastError(t, h.take()); got == "" {
		t.Fatal("a missing file is reported")
	}
}

func TestSendVoice(t *testing.T) {
	samples := make([]float32, 48000)
	for i := range samples {
		samples[i] = float32(0.3 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}
	if _, err := av.EncodeOpus(samples[:960]); errors.Is(err, av.ErrUnavailable) {
		t.Skip("no Opus encoder here")
	}
	h := newHarness(t, options{})
	h.start()
	ref, _ := h.c.PutBlob(av.PCM(samples))
	h.send(abi.Command{Tag: abi.CommandTagSendVoice, SendVoice: abi.CommandSendVoice{Chat: chatID(peer), Samples: ref}})
	evs := h.take()
	expectTags(t, evs, "Messages ChatUpdated MessageUpdated ChatUpdated")
	audio := h.f.lastSent().msg.GetAudioMessage()
	if !audio.GetPTT() || audio.GetSeconds() != 1 || len(audio.GetWaveform()) != 64 || audio.GetMimetype() != "audio/ogg; codecs=opus" || h.f.uploads[0] != whatsmeow.MediaAudio {
		t.Fatalf("voice: %v", audio)
	}
	if !bytes.HasPrefix(h.f.uploaded[0], []byte("OggS")) {
		t.Fatal("not Ogg")
	}
	if c := evs[0].Messages.Messages[0].Content; c.Tag != abi.ContentTagAudio || !c.Audio.VoiceNote || !c.Audio.Media.HasPath {
		t.Fatalf("row: %+v", c)
	}
}

func TestForward(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "F1", t0, text("one")))
	h.inject(message(peer, peer, "F2", t0.Add(time.Second), &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("two"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("F1"), QuotedMessage: text("one")}}}))
	viral := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("viral"), ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(7)}}}
	h.inject(message(peer, peer, "F3", t0.Add(2*time.Second), viral))
	h.take()

	h.send(abi.Command{Tag: abi.CommandTagForwardLimit, ForwardLimit: abi.CommandForwardLimit{FromChat: chatID(peer), Messages: [][]byte{[]byte("F1"), []byte("F2")}}})
	if l := one(t, h.take(), abi.EventTagForwardLimit).ForwardLimit; l.Limit != 5 || len(l.Messages) != 2 {
		t.Fatalf("limit: %+v", l)
	}
	h.send(abi.Command{Tag: abi.CommandTagForwardLimit, ForwardLimit: abi.CommandForwardLimit{FromChat: chatID(peer), Messages: [][]byte{[]byte("F1"), []byte("F3")}}})
	if l := one(t, h.take(), abi.EventTagForwardLimit).ForwardLimit; l.Limit != 1 {
		t.Fatalf("viral limit: %+v", l)
	}
	h.send(abi.Command{Tag: abi.CommandTagForward, Forward: abi.CommandForward{FromChat: chatID(peer), Messages: [][]byte{[]byte("F3")}, ToChats: [][]byte{chatID(peer2), chatID(group)}}})
	if lastError(t, h.take()) == "" || h.f.sentCount() != 0 {
		t.Fatal("over the limit")
	}

	h.send(abi.Command{Tag: abi.CommandTagForward, Forward: abi.CommandForward{FromChat: chatID(peer), Messages: [][]byte{[]byte("F1"), []byte("F2")}, ToChats: [][]byte{chatID(peer2), chatID(group)}}})
	evs := h.take()
	if got := len(only(evs, abi.EventTagMessages)); got != 4 || len(only(evs, abi.EventTagMessageUpdated)) != 4 {
		t.Fatalf("forwarded: %s", tags(evs))
	}
	if len(h.f.sent) != 4 || h.f.sent[0].chat != peer2 || h.f.sent[2].chat != group {
		t.Fatalf("order: %+v", h.f.sent)
	}
	first, second := h.f.sent[0].msg, h.f.sent[1].msg
	if ctx := first.GetExtendedTextMessage().GetContextInfo(); first.GetExtendedTextMessage().GetText() != "one" || !ctx.GetIsForwarded() || ctx.GetForwardingScore() != 1 {
		t.Fatalf("first: %v", first)
	}
	if ctx := second.GetImageMessage().GetContextInfo(); ctx.GetStanzaID() != "" || ctx.GetQuotedMessage() != nil || !ctx.GetIsForwarded() {
		t.Fatalf("the quote is not forwarded: %v", second)
	}
	if m := only(evs, abi.EventTagMessages)[0].Messages.Messages[0]; !m.Forwarded || string(m.Chat) != peer2.String() {
		t.Fatalf("row: %+v", m)
	}
	// What cannot be forwarded is reported, the rest goes.
	h.inject(message(peer, peer, "F4", t0.Add(3*time.Second), &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("?")}}))
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagForward, Forward: abi.CommandForward{FromChat: chatID(peer), Messages: [][]byte{[]byte("F4"), []byte("F1")}, ToChats: [][]byte{chatID(peer2)}}})
	evs = h.take()
	if lastError(t, evs) != "This message cannot be forwarded" || len(h.f.sent) != 5 {
		t.Fatalf("partial: %s", tags(evs))
	}
}

func TestEditText(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(peer), Text: []byte("tpyo")}})
	h.take()
	id := h.f.lastSent().id
	h.send(abi.Command{Tag: abi.CommandTagEditText, EditText: abi.CommandEditText{Chat: chatID(peer), ID: []byte(id), Text: []byte("typo @4917600000002"), Mentions: [][]byte{chatID(peer2)}}})
	evs := h.take()
	expectTags(t, evs, "MessageUpdated ChatUpdated")
	if m := evs[0].MessageUpdated.Message; string(m.Content.Text.Text) != "typo @4917600000002" || !m.Edited || len(m.Mentions) != 1 {
		t.Fatalf("edited: %+v", m)
	}
	pm := h.f.lastSent().msg.GetEditedMessage().GetMessage().GetProtocolMessage()
	if pm.GetType() != waE2E.ProtocolMessage_MESSAGE_EDIT || pm.GetKey().GetID() != id || pm.GetEditedMessage().GetExtendedTextMessage().GetText() != "typo @4917600000002" {
		t.Fatalf("wire: %v", pm)
	}
	// Refused: the original goes back.
	h.f.sendErr = errors.New("too late")
	h.send(abi.Command{Tag: abi.CommandTagEditText, EditText: abi.CommandEditText{Chat: chatID(peer), ID: []byte(id), Text: []byte("never")}})
	evs = h.take()
	expectTags(t, evs, "Error MessageUpdated")
	if string(evs[1].MessageUpdated.Message.Content.Text.Text) != "typo @4917600000002" {
		t.Fatal("restored")
	}
	// Not ours.
	h.inject(message(peer, peer, "THEIRS", t0, text("x")))
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagEditText, EditText: abi.CommandEditText{Chat: chatID(peer), ID: []byte("THEIRS"), Text: []byte("y")}})
	expectTags(t, h.take(), "Error MessageUpdated")
}

func TestRevokeDeleteLocalAndReact(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(group, peer, "G1", t0, text("theirs")))
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(group), Text: []byte("mine")}})
	h.take()
	mine := h.f.lastSent().id

	h.send(abi.Command{Tag: abi.CommandTagReact, React: abi.CommandReact{Chat: chatID(group), Message: []byte("G1"), Emoji: []byte("❤️")}})
	m := one(t, h.take(), abi.EventTagMessageUpdated).MessageUpdated.Message
	if len(m.Reactions) != 1 || !m.Reactions[0].FromMe || string(m.Reactions[0].Sender) != me.String() {
		t.Fatalf("reaction: %+v", m.Reactions)
	}
	r := h.f.lastSent().msg.GetReactionMessage()
	if r.GetText() != "❤️" || r.GetKey().GetID() != "G1" || r.GetKey().GetFromMe() || r.GetKey().GetParticipant() != peer.String() {
		t.Fatalf("wire: %v", r)
	}
	h.send(abi.Command{Tag: abi.CommandTagReact, React: abi.CommandReact{Chat: chatID(group), Message: []byte("G1")}})
	if m := one(t, h.take(), abi.EventTagMessageUpdated).MessageUpdated.Message; len(m.Reactions) != 0 {
		t.Fatalf("taken off: %+v", m.Reactions)
	}

	// Theirs, not as an admin: refused.
	h.send(abi.Command{Tag: abi.CommandTagRevoke, Revoke: abi.CommandRevoke{Chat: chatID(group), ID: []byte("G1")}})
	if lastError(t, h.take()) == "" {
		t.Fatal("not an admin")
	}
	h.send(abi.Command{Tag: abi.CommandTagRevoke, Revoke: abi.CommandRevoke{Chat: chatID(group), ID: []byte(mine)}})
	evs := h.take()
	expectTags(t, evs, "MessageUpdated ChatUpdated")
	if evs[0].MessageUpdated.Message.Content.Tag != abi.ContentTagRevoked {
		t.Fatal("revoked")
	}
	if pm := h.f.lastSent().msg.GetProtocolMessage(); pm.GetType() != waE2E.ProtocolMessage_REVOKE || pm.GetKey().GetID() != mine || !pm.GetKey().GetFromMe() {
		t.Fatalf("wire: %v", pm)
	}
	// As an admin, anyone's.
	h.e.arch.UpdateChat(group.String(), func(c *store.Chat) { c.Admin = true })
	h.send(abi.Command{Tag: abi.CommandTagRevoke, Revoke: abi.CommandRevoke{Chat: chatID(group), ID: []byte("G1")}})
	h.take()
	if k := h.f.lastSent().msg.GetProtocolMessage().GetKey(); k.GetParticipant() != peer.String() || k.GetFromMe() {
		t.Fatalf("admin revoke: %v", k)
	}

	h.send(abi.Command{Tag: abi.CommandTagDeleteLocal, DeleteLocal: abi.CommandDeleteLocal{Chat: chatID(group), ID: []byte(mine)}})
	evs = h.take()
	expectTags(t, evs, "MessageDeleted ChatUpdated")
	idx := h.f.patches[len(h.f.patches)-1].Mutations[0].Index
	if idx[0] != appstate.IndexDeleteMessageForMe || idx[2] != mine || idx[3] != "1" {
		t.Fatalf("patch: %v", idx)
	}
	if _, err := h.e.arch.Message(group.String(), mine); !noRows(err) {
		t.Fatal("still here")
	}
	// The phone refuses: nothing is deleted here.
	h.f.appStateErr = errors.New("no")
	h.send(abi.Command{Tag: abi.CommandTagDeleteLocal, DeleteLocal: abi.CommandDeleteLocal{Chat: chatID(group), ID: []byte("G1")}})
	expectTags(t, h.take(), "Error")
	// Offline: nothing goes out.
	h.f.setConnected(false)
	for _, cmd := range []abi.Command{
		{Tag: abi.CommandTagReact, React: abi.CommandReact{Chat: chatID(group), Message: []byte("G1"), Emoji: []byte("x")}},
		{Tag: abi.CommandTagRevoke, Revoke: abi.CommandRevoke{Chat: chatID(group), ID: []byte("G1")}},
		{Tag: abi.CommandTagDeleteLocal, DeleteLocal: abi.CommandDeleteLocal{Chat: chatID(group), ID: []byte("G1")}},
	} {
		h.send(cmd)
		expectTags(t, h.take(), "Error")
	}
}
