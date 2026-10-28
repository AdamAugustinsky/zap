package wa

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

func picture(caption string, size uint64) *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype: proto.String("image/jpeg"), Caption: proto.String(caption), FileLength: proto.Uint64(size),
		DirectPath: proto.String("/v/x"), MediaKey: []byte("key"), FileSHA256: []byte("sha"), FileEncSHA256: []byte("enc"),
	}}
}

func TestDownload(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.inject(message(peer, peer, "M1", t0, picture("small", 1000)))
	h.inject(message(peer, peer, "M2", t0, picture("huge", 100<<20)))
	h.inject(message(peer, peer, "T1", t0, text("words")))
	h.take()
	jpg := []byte("\xff\xd8\xff\xe0 a picture")
	h.f.downloaded = jpg

	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M1")}})
	evs := h.take()
	expectTags(t, evs, "MessageUpdated Media MessageUpdated")
	if s := evs[0].MessageUpdated.Message.Content.Image.Media.State; s.Tag != abi.MediaStateTagDownloading {
		t.Fatalf("downloading: %+v", s)
	}
	media := evs[1].Media
	if media.Result.Tag != abi.PathResultTagOk || string(media.Message) != "M1" || media.HasCard {
		t.Fatalf("media: %+v", media)
	}
	if data, err := os.ReadFile(string(media.Result.Ok.Path)); err != nil || !bytes.Equal(data, jpg) {
		t.Fatalf("kept: %v", err)
	}
	if m := evs[2].MessageUpdated.Message.Content.Image.Media; string(m.Path) != string(media.Result.Ok.Path) || m.State.Tag != abi.MediaStateTagIdle {
		t.Fatalf("row: %+v", m)
	}

	// Large attachments wait for the reader; asked for, they come.
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M2")}})
	evs = h.take()
	expectTags(t, evs, "Media MessageUpdated")
	if r := evs[0].Media.Result; r.Tag != abi.PathResultTagErr || string(r.Err.Error) != "Attachments over 64 MB download when you open them" {
		t.Fatalf("limit: %+v", r)
	}
	if s := evs[1].MessageUpdated.Message.Content.Image.Media.State; s.Tag != abi.MediaStateTagFailed {
		t.Fatalf("failed state: %+v", s)
	}
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M2"), Manual: true}})
	if r := one(t, h.take(), abi.EventTagMedia).Media.Result; r.Tag != abi.PathResultTagOk {
		t.Fatalf("manual: %+v", r)
	}

	// Failures: the server's, no attachment, no message, a card that is not there, offline.
	h.f.downloadErr = errors.New("media gone")
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M1"), Manual: true}})
	if r := one(t, h.take(), abi.EventTagMedia).Media.Result; r.Tag != abi.PathResultTagErr || string(r.Err.Error) != "media gone" {
		t.Fatalf("server: %+v", r)
	}
	for id, want := range map[string]string{"T1": "This message has no attachment to download", "NONE": "This message is not stored on this computer"} {
		h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte(id)}})
		if r := one(t, h.take(), abi.EventTagMedia).Media.Result; string(r.Err.Error) != want {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M1"), Card: 2, HasCard: true}})
	evs = h.take()
	expectTags(t, evs, "Media")
	if m := evs[0].Media; !m.HasCard || m.Card != 2 || m.Result.Tag != abi.PathResultTagErr {
		t.Fatalf("card: %+v", m)
	}
	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M1")}})
	if r := one(t, h.take(), abi.EventTagMedia).Media.Result; string(r.Err.Error) != "Not connected to WhatsApp" {
		t.Fatalf("offline: %+v", r)
	}
}

func TestFetchAvatar(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.f.picture = &types.ProfilePictureInfo{URL: "https://pps.example/p", ID: "1"}
	h.f.pictureBytes = []byte("\xff\xd8\xff\xe0 a face")

	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(peer)}})
	a := one(t, h.take(), abi.EventTagAvatar).Avatar
	if !a.HasPath || a.Full || string(a.ID) != peer.String() {
		t.Fatalf("avatar: %+v", a)
	}
	if data, err := os.ReadFile(string(a.Path)); err != nil || !bytes.Equal(data, h.f.pictureBytes) {
		t.Fatalf("kept: %v", err)
	}
	// Fresh: from the cache.
	asked := h.f.called("ProfilePicture")
	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(peer)}})
	if b := one(t, h.take(), abi.EventTagAvatar).Avatar; string(b.Path) != string(a.Path) || h.f.called("ProfilePicture") != asked {
		t.Fatal("not cached")
	}

	// No picture: answered without a path, and remembered.
	h.f.picture = nil
	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(peer2), Full: true}})
	if b := one(t, h.take(), abi.EventTagAvatar).Avatar; b.HasPath || !b.Full {
		t.Fatalf("none: %+v", b)
	}
	asked = h.f.called("ProfilePicture")
	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(peer2), Full: true}})
	if b := one(t, h.take(), abi.EventTagAvatar).Avatar; b.HasPath || h.f.called("ProfilePicture") != asked {
		t.Fatal("asked again")
	}

	// Offline: asked for once connected.
	h.f.setConnected(false)
	h.f.picture = &types.ProfilePictureInfo{URL: "https://pps.example/g"}
	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(group)}})
	expectTags(t, h.take(), "")
	h.f.setConnected(true)
	h.inject(&events.Connected{})
	if b := one(t, h.take(), abi.EventTagAvatar).Avatar; !b.HasPath || string(b.ID) != group.String() {
		t.Fatalf("after connecting: %+v", b)
	}

	// A failing server gives up after a few tries, with no picture.
	h.f.pictureBytes = []byte("not an image")
	stranger := types.NewJID("4917600000009", types.DefaultUserServer)
	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(stranger)}})
	expectTags(t, h.take(), "")
	var evs []abi.Event
	for range avatarTries - 1 {
		h.inject(&events.Connected{})
		evs = only(h.take(), abi.EventTagAvatar)
	}
	if len(evs) != 1 || evs[0].Avatar.HasPath || string(evs[0].Avatar.ID) != stranger.String() {
		t.Fatalf("gave up: %+v", evs)
	}
	if _, err := os.Stat(h.e.avatarFile(stranger.String(), false)); err == nil {
		t.Fatal("a failure is not cached")
	}
}
