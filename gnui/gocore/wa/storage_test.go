package wa

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

func TestSharedMedia(t *testing.T) {
	h := linked(t)
	h.receive(peer, peer, "P1", t0.Add(time.Second), picture("a photo", 1000))
	h.receive(peer, peer, "L1", t0.Add(2*time.Second), text("see https://example.com/page"))

	h.send(abi.Command{Tag: abi.CommandTagListChatShared, ListChatShared: abi.CommandListChatShared{Chat: chatID(peer), Request: 3}})
	s := one(t, h.take(), abi.EventTagChatShared).ChatShared
	var kinds []abi.SharedEntryTag
	for _, e := range s.Entries {
		kinds = append(kinds, e.Tag)
	}
	if s.Request != 3 || !s.Last || len(kinds) != 2 || kinds[0] != abi.SharedEntryTagMedia || kinds[1] != abi.SharedEntryTagLink || string(s.Entries[1].Link.URL) != "https://example.com/page" {
		t.Fatalf("shared: %+v", s)
	}

	h.send(abi.Command{Tag: abi.CommandTagLoadChatMedia, LoadChatMedia: abi.CommandLoadChatMedia{Chat: chatID(peer), Around: []byte("P1"), HasAround: true}})
	m := one(t, h.take(), abi.EventTagChatMedia).ChatMedia
	if len(m.Items) != 1 || string(m.Items[0].ID) != "P1" || m.Items[0].Video || string(m.Around) != "P1" {
		t.Fatalf("media: %+v", m)
	}
}

func TestMotionPhoto(t *testing.T) {
	h := linked(t)
	h.receive(peer, peer, "P1", t0, picture("moving", 1000))
	clip := &waE2E.Message{
		MessageContextInfo: &waE2E.MessageContextInfo{MessageAssociation: &waE2E.MessageAssociation{
			AssociationType:  waE2E.MessageAssociation_MOTION_PHOTO.Enum(),
			ParentMessageKey: &waCommon.MessageKey{ID: proto.String("P1")},
		}},
		AssociatedChildMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Mimetype: proto.String("video/mp4"), DirectPath: proto.String("/v/clip"), MediaKey: []byte("key"), FileLength: proto.Uint64(10),
		}}},
	}
	h.inject(message(peer, peer, "C1", t0, clip))
	if m := one(t, h.take(), abi.EventTagMessageUpdated).MessageUpdated.Message; string(m.ID) != "P1" || !m.Content.Image.HasMotion {
		t.Fatalf("the clip: %+v", m.Content.Image)
	}

	h.f.downloaded = []byte("an mp4 clip")
	h.send(abi.Command{Tag: abi.CommandTagDownloadMotion, DownloadMotion: abi.CommandDownloadMotion{Chat: chatID(peer), Message: []byte("P1")}})
	r := one(t, h.take(), abi.EventTagMotion).Motion
	if r.Result.Tag != abi.PathResultTagOk || string(r.Message) != "P1" {
		t.Fatalf("motion: %+v", r)
	}
	if data, err := os.ReadFile(string(r.Result.Ok.Path)); err != nil || !bytes.Equal(data, h.f.downloaded) {
		t.Fatalf("clip file: %v", err)
	}

	h.send(abi.Command{Tag: abi.CommandTagDownloadMotion, DownloadMotion: abi.CommandDownloadMotion{Chat: chatID(peer), Message: []byte("M1")}})
	if r := one(t, h.take(), abi.EventTagMotion).Motion; r.Result.Tag != abi.PathResultTagErr {
		t.Fatalf("no clip: %+v", r)
	}
}

func TestStorageStats(t *testing.T) {
	h := linked(t)
	dir := h.e.mediaCache()
	for name, size := range map[string]int{"a.jpg": 3, "b.mp4": 5, "c.webp": 2, "d.pdf": 7, ".download-x": 9} {
		if err := writePrivate(filepath.Join(dir, name), make([]byte, size)); err != nil {
			t.Fatal(err)
		}
	}
	h.send(abi.Command{Tag: abi.CommandTagLoadStorageStats, LoadStorageStats: abi.CommandLoadStorageStats{Token: 4}})
	s := one(t, h.take(), abi.EventTagStorageStats).StorageStats
	want := abi.StorageStats{Images: 1, ImageBytes: 3, Videos: 1, VideoBytes: 5, Stickers: 1, StickerBytes: 2, Other: 1, OtherBytes: 7}
	if s.Token != 4 || !s.HasStats || s.Cleared || s.Stats != want {
		t.Fatalf("stats: %+v", s)
	}

	// Emptied: the archive forgets the files, so they download again.
	h.f.downloaded = []byte("\xff\xd8\xff a picture")
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("M1")}})
	h.receive(peer, peer, "P1", t0, picture("p", 100))
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("P1")}})
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagClearMediaCache, ClearMediaCache: abi.CommandClearMediaCache{Token: 5}})
	if s := one(t, h.take(), abi.EventTagStorageStats).StorageStats; s.Token != 5 || !s.Cleared || s.Stats != (abi.StorageStats{}) {
		t.Fatalf("cleared: %+v", s)
	}
	m, err := h.e.arch.Message(peer.String(), "P1")
	if err != nil || m.Media.Path != "" {
		t.Fatalf("the archive keeps a gone file: %+v %v", m.Media, err)
	}
}

func TestKeepChatsArchived(t *testing.T) {
	h := linked(t)
	h.send(abi.Command{Tag: abi.CommandTagSetKeepChatsArchived, SetKeepChatsArchived: abi.CommandSetKeepChatsArchived{Keep: true}})
	h.send(abi.Command{Tag: abi.CommandTagSetArchived, SetArchived: abi.CommandSetArchived{Chat: chatID(peer), Archived: true}})
	h.take()
	h.inject(message(peer, peer, "N1", t0.Add(time.Minute), text("still there?")))
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; !c.Archived {
		t.Fatal("a kept chat came back")
	}

	h.send(abi.Command{Tag: abi.CommandTagSetKeepChatsArchived, SetKeepChatsArchived: abi.CommandSetKeepChatsArchived{}})
	h.take()
	h.inject(message(peer, peer, "N2", t0.Add(2*time.Minute), text("hello?")))
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; c.Archived {
		t.Fatal("the chat stayed archived")
	}
}

func TestHistoryPrefetch(t *testing.T) {
	h := linked(t)
	h.send(abi.Command{Tag: abi.CommandTagSetHistoryPrefetch, SetHistoryPrefetch: abi.CommandSetHistoryPrefetch{On: true, Focused: chatID(peer), HasFocused: true}})
	h.take()
	if len(h.f.peer) != 1 || h.f.peer[0].GetProtocolMessage().GetPeerDataOperationRequestMessage() == nil {
		t.Fatalf("history asked: %v", h.f.peer)
	}
}

func TestDownloadFolder(t *testing.T) {
	h := linked(t)
	folder := t.TempDir()
	h.send(abi.Command{Tag: abi.CommandTagSetDownloadFolder, SetDownloadFolder: abi.CommandSetDownloadFolder{Path: []byte(folder), HasPath: true}})
	h.receive(peer, peer, "P1", t0, picture("p", 100))
	h.f.downloaded = []byte("\xff\xd8\xff a picture")
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("P1")}})
	if m := one(t, h.take(), abi.EventTagMedia).Media; filepath.Dir(string(m.Result.Ok.Path)) != folder {
		t.Fatalf("downloaded to %s", m.Result.Ok.Path)
	}

	h.send(abi.Command{Tag: abi.CommandTagPickDownloadFolder})
	if req := h.pick(folder); req.Kind != abi.PickKindFolder {
		t.Fatalf("dialog: %+v", req)
	}
	if p := one(t, h.take(), abi.EventTagDownloadFolderPicked).DownloadFolderPicked; string(p.Path) != folder {
		t.Fatalf("picked: %+v", p)
	}
}

func TestPickersAndFiles(t *testing.T) {
	h := linked(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	os.WriteFile(source, []byte("text"), 0o600)

	h.send(abi.Command{Tag: abi.CommandTagPickFiles, PickFiles: abi.CommandPickFiles{Chat: chatID(peer), Documents: true}})
	h.pick(source)
	if p := one(t, h.take(), abi.EventTagPicked).Picked; len(p.Paths) != 1 || string(p.Paths[0]) != source || !p.Documents {
		t.Fatalf("picked: %+v", p)
	}

	target := filepath.Join(dir, "copy.txt")
	h.send(abi.Command{Tag: abi.CommandTagSaveAttachmentAs, SaveAttachmentAs: abi.CommandSaveAttachmentAs{Source: []byte(source), Name: []byte("a.txt")}})
	if req := h.pick(target); req.Kind != abi.PickKindSave || string(req.FileName) != "a.txt" {
		t.Fatalf("dialog: %+v", req)
	}
	if i := one(t, h.take(), abi.EventTagInfo).Info; string(i.Text) != "Saved copy.txt" {
		t.Fatalf("saved: %s", i.Text)
	}
	if data, _ := os.ReadFile(target); string(data) != "text" {
		t.Fatal("not copied")
	}

	album := t.TempDir()
	h.send(abi.Command{Tag: abi.CommandTagSaveAttachments, SaveAttachments: abi.CommandSaveAttachments{Files: []abi.SaveFile{{Source: []byte(source), Name: []byte("a.txt")}, {Source: []byte(source), Name: []byte("a.txt")}}}})
	h.pick(album)
	if i := one(t, h.take(), abi.EventTagInfo).Info; string(i.Text) != "Saved 2 album attachments" {
		t.Fatalf("album: %s", i.Text)
	}
	for _, name := range []string{"a.txt", "a 2.txt"} {
		if _, err := os.Stat(filepath.Join(album, name)); err != nil {
			t.Fatal(err)
		}
	}

	sound := filepath.Join(dir, "ding.wav")
	h.send(abi.Command{Tag: abi.CommandTagPickNotificationSound, PickNotificationSound: abi.CommandPickNotificationSound{Mention: true}})
	h.pick(sound)
	if p := one(t, h.take(), abi.EventTagNotificationSoundPicked).NotificationSoundPicked; !p.Mention || string(p.Path) != sound {
		t.Fatalf("sound: %+v", p)
	}
	h.send(abi.Command{Tag: abi.CommandTagPickChatSound, PickChatSound: abi.CommandPickChatSound{Chat: chatID(peer)}})
	h.pick(sound)
	if p := one(t, h.take(), abi.EventTagChatSoundPicked).ChatSoundPicked; string(p.Chat) != peer.String() || string(p.Path) != sound {
		t.Fatalf("chat sound: %+v", p)
	}
	h.send(abi.Command{Tag: abi.CommandTagSetChatSound, SetChatSound: abi.CommandSetChatSound{Chat: chatID(peer), HasSound: true,
		Sound: abi.NotificationSound{Tag: abi.NotificationSoundTagCustom, Custom: abi.NotificationSoundCustom{Path: []byte(sound)}}}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; !c.HasNotificationSound || string(c.NotificationSound.Custom.Path) != sound {
		t.Fatalf("set sound: %+v", c.NotificationSound)
	}

	// A cancelled dialog answers nothing.
	h.send(abi.Command{Tag: abi.CommandTagPickChatSound, PickChatSound: abi.CommandPickChatSound{Chat: chatID(peer)}})
	h.pick()
	expectTags(t, h.take(), "")

	h.send(abi.Command{Tag: abi.CommandTagOpenLog, OpenLog: abi.CommandOpenLog{Path: []byte("/tmp/zap.log")}})
	if p := one(t, h.take(), abi.EventTagOpenPath).OpenPath; string(p.Path) != "/tmp/zap.log" {
		t.Fatalf("open: %+v", p)
	}
}

func TestWallpaper(t *testing.T) {
	h := linked(t)
	h.send(abi.Command{Tag: abi.CommandTagPickWallpaperImage})
	h.pick(writePNG(t, t.TempDir(), 30, 20, false))
	p := one(t, h.take(), abi.EventTagWallpaperImagePicked).WallpaperImagePicked
	path := string(p.Result.Ok.Path)
	if p.Result.Tag != abi.PathResultTagOk || filepath.Base(path) != "wallpaper.png" {
		t.Fatalf("wallpaper: %+v", p)
	}
	h.send(abi.Command{Tag: abi.CommandTagRemoveWallpaperImage})
	expectTags(t, h.take(), "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the wallpaper stayed")
	}
}

func TestPictures(t *testing.T) {
	h := linked(t)
	png := writePNG(t, t.TempDir(), 30, 20, false)

	h.send(abi.Command{Tag: abi.CommandTagPrepareClipboardImage, PrepareClipboardImage: abi.CommandPrepareClipboardImage{Path: []byte(png)}})
	c := one(t, h.take(), abi.EventTagClipboardImage).ClipboardImage
	if c.Result.Tag != abi.ClipboardImageResultTagOk || c.Result.Ok.Image.Width != 30 || c.Result.Ok.Image.Height != 20 || c.Result.Ok.Image.RGBA.Len != 30*20*4 {
		t.Fatalf("clipboard: %+v", c)
	}

	h.send(abi.Command{Tag: abi.CommandTagInspectPicture, InspectPicture: abi.CommandInspectPicture{Target: 3, Path: []byte(png)}})
	if p := one(t, h.take(), abi.EventTagPictureInspected).PictureInspected; p.Target != 3 || p.Result.Ok.Width != 30 || p.Result.Ok.Height != 20 {
		t.Fatalf("inspected: %+v", p)
	}

	edit := abi.CommandApplyPictureEdit{Target: 3, Crop: abi.PictureCrop{Width: 10, Height: 10}, Turns: 1}
	edit.Source.Tag, edit.Source.File.Path = abi.PictureSourceTagFile, []byte(png)
	h.send(abi.Command{Tag: abi.CommandTagApplyPictureEdit, ApplyPictureEdit: edit})
	p := one(t, h.take(), abi.EventTagPictureEdited).PictureEdited
	if p.Target != 3 || p.Result.Tag != abi.PathResultTagOk || !strings.HasPrefix(string(p.Result.Ok.Path), filepath.Join(h.e.mediaCache(), "edited")) {
		t.Fatalf("edited: %+v", p)
	}
}

func TestLockAndProxy(t *testing.T) {
	h := linked(t)
	h.send(abi.Command{Tag: abi.CommandTagSetLocked, SetLocked: abi.CommandSetLocked{Chat: chatID(peer), Locked: true}})
	if c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat; !c.Locked {
		t.Fatal("not locked")
	}
	if p := h.f.patches[len(h.f.patches)-1].Mutations[0]; p.Index[0] != "lock" || !p.Value.GetLockChatAction().GetLocked() {
		t.Fatalf("patch: %+v", p)
	}

	h.send(abi.Command{Tag: abi.CommandTagSetProxy, SetProxy: abi.CommandSetProxy{Proxy: []byte("127.0.0.1:1080")}})
	h.take()
	if h.f.called("SetProxy:http://127.0.0.1:1080") != 1 || h.f.called("Disconnect") == 0 || h.f.called("Connect") < 2 {
		t.Fatalf("proxy: %v", h.f.calls)
	}
	h.send(abi.Command{Tag: abi.CommandTagSetProxy, SetProxy: abi.CommandSetProxy{Proxy: []byte("ftp://x")}})
	if evs := h.take(); lastError(t, evs) != "Use an http://, socks5://, or socks5h:// proxy" {
		t.Fatalf("bad proxy: %s", tags(evs))
	}
}

func TestStartOverArchive(t *testing.T) {
	keys := &memKeys{m: map[string][]byte{}}
	state, cache := t.TempDir(), t.TempDir()
	h := newHarness(t, options{keys: keys, state: state, cache: cache})
	h.take()
	h.c.Close(5 * time.Second)
	for name := range keys.m {
		keys.m[name] = make([]byte, 32)
	}
	h2 := newHarness(t, options{keys: keys, state: state, cache: cache})
	h2.take()
	h2.send(abi.Command{Tag: abi.CommandTagStartOverArchive})
	if evs := h2.take(); len(only(evs, abi.EventTagChats)) != 1 {
		t.Fatalf("started over: %s", tags(evs))
	}
	var kept []string
	filepath.WalkDir(state, func(path string, _ os.DirEntry, _ error) error {
		if strings.Contains(filepath.Base(path), "-unreadable-") {
			kept = append(kept, path)
		}
		return nil
	})
	if len(kept) == 0 {
		t.Fatal("the unreadable archive was not kept")
	}
}
