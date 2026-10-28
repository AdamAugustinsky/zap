package demo

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/av"
)

// The Go demo on its own, with no library: zfcore's demo tests
// (zfcore/src/tests.rs), against the Go demo.

func openDemo(t *testing.T) *session {
	t.Helper()
	s := newSession(t, "go", openGo)
	started(s)
	return s
}

// loaded loads chat and returns its messages.
func (s *session) loaded(chat string) []abi.Message {
	s.t.Helper()
	var got []abi.Message
	h := &collect{chat: chat, out: &got}
	c := cmdOf(abi.CommandTagLoadChat)
	c.LoadChat.Chat = []byte(chat)
	s.send(c)
	deadline := time.Now().Add(10 * time.Second)
	for !h.done {
		if _, err := s.c.Drain(h, 0); err != nil {
			s.t.Fatal(err)
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("no messages of %s", chat)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return got
}

// collect keeps copies of one chat's loaded messages.
type collect struct {
	chat string
	out  *[]abi.Message
	done bool
}

func (h *collect) Event(e *abi.Event) {
	if e.Tag != abi.EventTagMessages || string(e.Messages.Chat) != h.chat {
		return
	}
	for _, m := range e.Messages.Messages {
		*h.out = append(*h.out, clone(m))
	}
	h.done = e.Messages.Complete
}

// clone deep-copies what the tests read of a message.
func clone(m abi.Message) abi.Message {
	c := m
	c.ID = bytes.Clone(m.ID)
	switch m.Content.Tag {
	case abi.ContentTagAudio:
		c.Content.Audio.Media.Path = bytes.Clone(m.Content.Audio.Media.Path)
	case abi.ContentTagImage:
		c.Content.Image.Media.Path = bytes.Clone(m.Content.Image.Media.Path)
	case abi.ContentTagVideo:
		c.Content.Video.Media.Path = bytes.Clone(m.Content.Video.Media.Path)
	}
	return c
}

func TestTheChatsReadInTimeOrderAndEndOnTheirLastLine(t *testing.T) {
	opened := time.Now().Unix()
	s := openDemo(t)
	for _, sm := range samples {
		ms := s.loaded(sm.id)
		prefix := strings.ReplaceAll(sm.name, " ", "-")
		own := 0
		for i, m := range ms {
			if i > 0 && ms[i-1].Timestamp > m.Timestamp {
				t.Errorf("%s: times go backwards at %s", sm.name, m.ID)
			}
			if m.Timestamp > opened+1 {
				t.Errorf("%s: %s is after now", sm.name, m.ID)
			}
			if strings.HasPrefix(string(m.ID), prefix) && !strings.Contains(string(m.ID), "-media-") {
				own++
			}
		}
		if last := string(ms[len(ms)-1].ID); last != prefix+"-"+itoa(own-1) {
			t.Errorf("%s ends on %s", sm.name, last)
		}
		if own != len(sm.lines) {
			t.Errorf("%s: %d of its %d lines", sm.name, own, len(sm.lines))
		}
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

// Each command the demo answers produces its events, as zfcore's
// every_command_the_demo_core_answers_produces_events checks.
func TestEveryAnsweredCommandProducesEvents(t *testing.T) {
	media := MediaDir()
	type answered struct {
		cmd  *abi.Command
		want func(*abi.Event) bool
	}
	c := func(tag abi.CommandTag, set func(*abi.Command)) *abi.Command {
		cmd := cmdOf(tag)
		if set != nil {
			set(cmd)
		}
		return cmd
	}
	cases := []answered{
		{c(abi.CommandTagLoadChat, func(c *abi.Command) { c.LoadChat.Chat = []byte(ada) }),
			func(e *abi.Event) bool { return e.Tag == abi.EventTagMessages && len(e.Messages.Messages) > 0 }},
		{c(abi.CommandTagSendText, func(c *abi.Command) { c.SendText.Chat, c.SendText.Text = []byte(ada), []byte("hi") }), isTag(abi.EventTagMessages)},
		{c(abi.CommandTagSendLocation, func(c *abi.Command) { c.SendLocation.Chat = []byte(ada) }), isTag(abi.EventTagMessages)},
		{c(abi.CommandTagMarkRead, func(c *abi.Command) { c.MarkRead.Chat = []byte(ada) }), isTag(abi.EventTagChatUpdated)},
		{c(abi.CommandTagSetMuted, func(c *abi.Command) { c.SetMuted.Chat, c.SetMuted.HasUntil = []byte(ada), true }), isTag(abi.EventTagChatUpdated)},
		{c(abi.CommandTagOpenLog, func(c *abi.Command) { c.OpenLog.Path = []byte("/tmp/zapfast.log") }),
			func(e *abi.Event) bool {
				return e.Tag == abi.EventTagOpenPath && string(e.OpenPath.Path) == "/tmp/zapfast.log"
			}},
		{c(abi.CommandTagReconnect, nil), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagLink && e.Link.Status.Tag == abi.LinkStatusTagConnected
		}},
		{c(abi.CommandTagDemoIncoming, func(c *abi.Command) {
			c.DemoIncoming = abi.CommandDemoIncoming{Chat: []byte(ada), Sender: []byte(ada), Text: []byte("hello")}
		}), isTag(abi.EventTagIncoming)},
		{c(abi.CommandTagDemoTyping, func(c *abi.Command) {
			c.DemoTyping = abi.CommandDemoTyping{Chat: []byte(ada), Sender: []byte(ada), Composing: true}
		}), isTag(abi.EventTagTyping)},
		{c(abi.CommandTagWatchReceipts, func(c *abi.Command) {
			c.WatchReceipts = abi.CommandWatchReceipts{Target: abi.ReceiptTarget{Chat: []byte(ada), Message: []byte("Ada-Lovelace-1")}, HasTarget: true}
		}), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagReceipts && len(e.Receipts.Recipients) == 1 && e.Receipts.Recipients[0].HasReadAt
		}},
		{c(abi.CommandTagFetchAvatar, func(c *abi.Command) { c.FetchAvatar.ID = []byte(ada) }), func(e *abi.Event) bool {
			if e.Tag != abi.EventTagAvatar || !e.Avatar.HasPath {
				return false
			}
			fi, err := os.Stat(string(e.Avatar.Path))
			return err == nil && fi.Mode().IsRegular()
		}},
		{c(abi.CommandTagListChatShared, func(c *abi.Command) { c.ListChatShared = abi.CommandListChatShared{Chat: []byte(ada), Request: 7} }),
			func(e *abi.Event) bool {
				return e.Tag == abi.EventTagChatShared && e.ChatShared.Request == 7 && e.ChatShared.Last && len(e.ChatShared.Entries) == 9
			}},
		{c(abi.CommandTagLoadStorageStats, func(c *abi.Command) { c.LoadStorageStats.Token = 3 }), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagStorageStats && e.StorageStats.Token == 3 && e.StorageStats.Stats.Images > 0
		}},
		{c(abi.CommandTagClearMediaCache, func(c *abi.Command) { c.ClearMediaCache.Token = 4 }), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagStorageStats && e.StorageStats.Token == 4 && e.StorageStats.Cleared && e.StorageStats.Stats.Images == 0
		}},
		{c(abi.CommandTagFetchAccountPrivacy, nil), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagAccountPrivacy && len(e.AccountPrivacy.Values) == 7
		}},
		{c(abi.CommandTagSetAccountPrivacy, func(c *abi.Command) {
			c.SetAccountPrivacy = abi.CommandSetAccountPrivacy{Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceNobody}
		}), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagAccountPrivacySaved && e.AccountPrivacySaved.Kind == abi.PrivacyKindAbout
		}},
		{c(abi.CommandTagStartCall, func(c *abi.Command) {
			c.StartCall = abi.CommandStartCall{Chat: []byte(ada), Generation: 1 << 48, Cancel: 1}
		}), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagCall && e.Call.Update.Generation == 1<<48 && e.Call.Update.Phase == abi.CallPhaseDialing
		}},
		{c(abi.CommandTagJoinCallLink, func(c *abi.Command) {
			c.JoinCallLink = abi.CommandJoinCallLink{Token: []byte("abc"), Video: true, Generation: 1<<48 + 1, Cancel: 2}
		}), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagCall && e.Call.Update.Phase == abi.CallPhaseConnecting && e.Call.Update.Group.Link
		}},
		{c(abi.CommandTagRefreshCallDevices, nil), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagCallDevices && len(e.CallDevices.Devices.Cameras) == 2
		}},
		{c(abi.CommandTagLoadCallLog, nil), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagCallLog && e.CallLog.Last && len(e.CallLog.Messages) == 5
		}},
		{c(abi.CommandTagVotePoll, func(c *abi.Command) {
			c.VotePoll = abi.CommandVotePoll{Chat: []byte(margaret), Message: []byte("DEMOPOLL0"), Choices: []uint64{2}}
		}), func(e *abi.Event) bool {
			if e.Tag != abi.EventTagMessageUpdated || e.MessageUpdated.Message.Content.Tag != abi.ContentTagPoll {
				return false
			}
			st := e.MessageUpdated.Message.Content.Poll.State
			return len(st.Counts) == 3 && st.Counts[2] == 1 && st.Counts[0] == 2 && st.Voters == 4 && len(st.Selected) == 1 && st.Selected[0] == 2
		}},
		{c(abi.CommandTagSetLocked, func(c *abi.Command) { c.SetLocked = abi.CommandSetLocked{Chat: []byte(ada), Locked: true} }),
			func(e *abi.Event) bool {
				return e.Tag == abi.EventTagChatUpdated && string(e.ChatUpdated.Chat.ID) == ada && e.ChatUpdated.Chat.Locked
			}},
		{c(abi.CommandTagEnsureChat, func(c *abi.Command) {
			c.EnsureChat = abi.CommandEnsureChat{Chat: []byte("4930123456@s.whatsapp.net"), Name: []byte("Dorothy Vaughan")}
		}), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagChatUpdated && string(e.ChatUpdated.Chat.Name) == "Dorothy Vaughan"
		}},
		{c(abi.CommandTagLoadChatMedia, func(c *abi.Command) {
			c.LoadChatMedia = abi.CommandLoadChatMedia{Chat: []byte(ada), Around: []byte("Ada-media-0"), HasAround: true}
		}), func(e *abi.Event) bool {
			if e.Tag != abi.EventTagChatMedia || !e.ChatMedia.Last || len(e.ChatMedia.Items) != 4 {
				return false
			}
			videos := 0
			for _, it := range e.ChatMedia.Items {
				if it.Video {
					videos++
				}
				if _, err := os.Stat(string(it.Path)); !it.HasPath || err != nil {
					return false
				}
			}
			return videos == 1
		}},
		{c(abi.CommandTagSearchGifs, func(c *abi.Command) {
			c.SearchGifs = abi.CommandSearchGifs{Query: []byte("rocket"), Key: []byte("any")}
		}),
			func(e *abi.Event) bool {
				return e.Tag == abi.EventTagGifs && !e.Gifs.HasError && len(e.Gifs.Gifs) == 1 && e.Gifs.Gifs[0].HasStill
			}},
		{c(abi.CommandTagSendGif, func(c *abi.Command) {
			c.SendGif = abi.CommandSendGif{Chat: []byte(ada), Gif: abi.Gif{ID: []byte("demo-gif-pug"), Width: 320, Height: 240}}
		}), func(e *abi.Event) bool {
			if e.Tag != abi.EventTagMessages {
				return false
			}
			for _, m := range e.Messages.Messages {
				if m.FromMe && m.HasThumbnail && m.Content.Tag == abi.ContentTagVideo && m.Content.Video.Gif && m.Content.Video.Media.HasPath {
					return true
				}
			}
			return false
		}},
		{c(abi.CommandTagRecentStickers, nil), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagStickers && e.Stickers.Last && countEntries(e, abi.StickerEntryTagRecent) == 4
		}},
		{c(abi.CommandTagSendSticker, func(c *abi.Command) {
			c.SendSticker = abi.CommandSendSticker{Chat: []byte(ada), Path: []byte(filepath.Join(media, "stickers/owl.webp"))}
		}), func(e *abi.Event) bool {
			if e.Tag != abi.EventTagStickers {
				return false
			}
			for _, en := range e.Stickers.Entries {
				if en.Tag == abi.StickerEntryTagRecent {
					return strings.HasSuffix(string(en.Recent.Path), "owl.webp")
				}
			}
			return false
		}},
		{c(abi.CommandTagSaveSticker, func(c *abi.Command) { c.SaveSticker.Path = []byte(filepath.Join(media, "stickers/goat.webp")) }),
			func(e *abi.Event) bool {
				if e.Tag != abi.EventTagStickers {
					return false
				}
				for _, en := range e.Stickers.Entries {
					if en.Tag == abi.StickerEntryTagFavorite && strings.HasSuffix(string(en.Favorite.Path), "goat.webp") {
						return true
					}
				}
				return false
			}},
		{c(abi.CommandTagRemoveRecentSticker, func(c *abi.Command) {
			c.RemoveRecentSticker.Path = []byte(filepath.Join(media, "stickers/pug.webp"))
		}), func(e *abi.Event) bool {
			return e.Tag == abi.EventTagStickers && countEntries(e, abi.StickerEntryTagRecent) == 3
		}},
		{c(abi.CommandTagForgetSticker, func(c *abi.Command) { c.ForgetSticker.Path = []byte(filepath.Join(media, "stickers/owl.webp")) }),
			func(e *abi.Event) bool {
				return e.Tag == abi.EventTagStickers && countEntries(e, abi.StickerEntryTagFavorite) == 1
			}},
		{c(abi.CommandTagSendStickerPack, func(c *abi.Command) {
			c.SendStickerPack = abi.CommandSendStickerPack{Chat: []byte(ada), Dir: []byte(filepath.Join(media, "stickers"))}
		}), func(e *abi.Event) bool {
			if e.Tag != abi.EventTagMessages {
				return false
			}
			for _, m := range e.Messages.Messages {
				if m.FromMe && m.Content.Tag == abi.ContentTagStickerPack && m.Content.StickerPack.Count == 8 {
					return true
				}
			}
			return false
		}},
		{c(abi.CommandTagViewStickerPack, func(c *abi.Command) {
			c.ViewStickerPack = abi.CommandViewStickerPack{Chat: []byte(berlin), Message: []byte("DEMOPACK0")}
		}), func(e *abi.Event) bool {
			r := e.StickerPackPreview.Result
			return e.Tag == abi.EventTagStickerPackPreview && r.Tag == abi.StickerPackResultTagOk && string(r.Ok.Publisher) == "Grace Hopper"
		}},
		{c(abi.CommandTagAddStickerPack, func(c *abi.Command) {
			c.AddStickerPack = abi.CommandAddStickerPack{Dir: []byte(filepath.Join(media, "stickers")), Name: []byte("Launch party")}
		}), isTag(abi.EventTagInfo)},
	}
	named := map[abi.CommandTag]bool{}
	for _, tc := range cases {
		named[tc.cmd.Tag] = true
		if DispositionOf(tc.cmd.Tag) != Answers {
			t.Errorf("%v is not answered", tc.cmd.Tag)
		}
		t.Run(tc.cmd.Tag.String(), func(t *testing.T) {
			s := newSession(t, "go", openGo)
			// The demo's own opening events do not count.
			s.until("the chats", lastOfList(abi.EventTagChats))
			s.send(tc.cmd)
			s.until("its events", tc.want)
		})
	}
	// SendImage and SendVoice are covered below, DemoPanic by its test, the
	// call controls by the comparisons and the calls test.
	for _, tag := range []abi.CommandTag{abi.CommandTagSendImage, abi.CommandTagSendVoice, abi.CommandTagDemoPanic,
		abi.CommandTagAnswerCall, abi.CommandTagDeclineCall, abi.CommandTagHangupCall, abi.CommandTagSetCallMuted,
		abi.CommandTagSetCallCamera, abi.CommandTagSetCallMicrophone, abi.CommandTagSetCallSpeaker, abi.CommandTagSetCallCameraDevice} {
		named[tag] = true
	}
	for _, tag := range answers {
		if !named[tag] {
			t.Errorf("%v is untested", tag)
		}
	}
}

func countEntries(e *abi.Event, tag abi.StickerEntryTag) int {
	n := 0
	for _, en := range e.Stickers.Entries {
		if en.Tag == tag {
			n++
		}
	}
	return n
}

// An image and a voice note go pending, sent, delivered and read, as text
// does, and take their blobs; the note is encoded where it plays back, and
// a stopped demo removes it.
func TestImagesAndVoiceNotesSendLikeText(t *testing.T) {
	s := openDemo(t)
	rgba, _ := s.c.PutBlob(bytes.Repeat([]byte{255}, 4*3*2))
	c := cmdOf(abi.CommandTagSendImage)
	c.SendImage = abi.CommandSendImage{Chat: []byte(ada), Width: 3, Height: 2, RGBA: rgba, Caption: []byte("look"), HasCaption: true}
	s.send(c)
	var image abi.Message
	statuses := map[abi.Delivery]bool{}
	s.until("read", func(e *abi.Event) bool {
		switch e.Tag {
		case abi.EventTagMessages:
			for _, m := range e.Messages.Messages {
				if m.FromMe && strings.HasPrefix(string(m.ID), "DEMO0") {
					image = clone(m)
				}
			}
		case abi.EventTagMessageUpdated:
			statuses[e.MessageUpdated.Message.Status] = true
			return e.MessageUpdated.Message.Status == abi.DeliveryRead
		}
		return false
	})
	if im := image.Content.Image; image.Content.Tag != abi.ContentTagImage || im.Media.Width != 3 || im.Media.Height != 2 || im.Media.Size != 24 {
		t.Errorf("the image was filed as %+v", image.Content)
	}
	if !statuses[abi.DeliverySent] || !statuses[abi.DeliveryDelivered] {
		t.Errorf("statuses %v", statuses)
	}
	if _, err := s.c.ReadBlob(rgba.ID, 0, make([]byte, 1)); !core.IsCode(err, core.ENotFound) {
		t.Errorf("the command left its blob: %v", err)
	}

	pcm := av.PCM(speech(2, 180))
	samples, _ := s.c.PutBlob(pcm)
	c = cmdOf(abi.CommandTagSendVoice)
	c.SendVoice = abi.CommandSendVoice{Chat: []byte(ada), Samples: samples}
	s.send(c)
	var note abi.Message
	s.until("the note", func(e *abi.Event) bool {
		if e.Tag == abi.EventTagMessages {
			for _, m := range e.Messages.Messages {
				if m.FromMe && m.Content.Tag == abi.ContentTagAudio {
					note = clone(m)
					return true
				}
			}
		}
		return false
	})
	a := note.Content.Audio
	if !a.VoiceNote || a.Seconds != 2 || len(a.Waveform) != 64 {
		t.Errorf("the note: %+v", a)
	}
	if !a.Media.HasPath {
		t.Skip("no Opus encoder here")
	}
	path := string(a.Media.Path)
	ref, format, err := s.c.(*Conn).DecodeVoice(path)
	if err != nil {
		t.Fatal(err)
	}
	s.c.FreeBlob(ref.ID)
	if format.Frames < 2*av.Rate {
		t.Errorf("decoded %d frames", format.Frames)
	}
	// Over 15 minutes is refused before the blob is looked at.
	c = cmdOf(abi.CommandTagSendVoice)
	c.SendVoice = abi.CommandSendVoice{Chat: []byte(ada), Samples: abi.BlobRef{ID: 9999, Len: (15*60*48_000 + 1) * 4}}
	if err := s.c.Send(c); !core.IsCode(err, core.ERefused) {
		t.Errorf("a long note: %v", err)
	}
	s.c.Close(5 * time.Second)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a stopped demo kept the note it was sent: %v", err)
	}
}

// The demo's voice notes decode; nothing outside its media folder does.
func TestTheVoiceNotesDecodeAndNothingOutsideDoes(t *testing.T) {
	s := openDemo(t)
	var note abi.Message
	for _, m := range s.loaded(ada) {
		if m.Content.Tag == abi.ContentTagAudio {
			note = m
		}
	}
	a := note.Content.Audio
	if string(note.ID) != "DEMOVOICE0" || a.Seconds != 4 || len(a.Waveform) != 64 {
		t.Fatalf("Ada's voice note: %s %+v", note.ID, a)
	}
	if !a.Media.HasPath {
		t.Skip("no Opus encoder here")
	}
	d := s.c.(*Conn)
	ref, format, err := d.DecodeVoice(string(a.Media.Path))
	if err != nil {
		t.Fatal(err)
	}
	d.FreeBlob(ref.ID)
	if format.Frames < 4*av.Rate || format.Frames >= 5*av.Rate {
		t.Errorf("%d frames", format.Frames)
	}
	loud := false
	for _, b := range format.Waveform {
		loud = loud || b > 80
	}
	if !loud {
		t.Errorf("waveform %v", format.Waveform)
	}
	for _, outside := range []string{"/etc/hosts", filepath.Join(AvatarDir(), "ada.jpg")} {
		if _, _, err := d.DecodeVoice(outside); !core.IsCode(err, core.EArg) && !core.IsCode(err, core.ENotFound) {
			t.Errorf("%s: %v", outside, err)
		}
	}
	// Through the note as if it were a folder: zfcore's realpath resolves
	// it, outside (ZF_E_ARG); gocore/av's EvalSymlinks fails (ZF_E_IO).
	// Refused either way.
	if _, _, err := d.DecodeVoice(string(a.Media.Path) + "/../../zapfast-demo-avatars/ada.jpg"); err == nil {
		t.Error("a path out through the note decoded")
	}
}

// DemoIncoming and DemoTyping wait for their delay.
func TestInjectionsWaitForTheirDelay(t *testing.T) {
	s := openDemo(t)
	for _, tc := range []struct {
		cmd *abi.Command
		tag abi.EventTag
	}{
		{&abi.Command{Tag: abi.CommandTagDemoIncoming, DemoIncoming: abi.CommandDemoIncoming{DelayMS: 300, Chat: []byte(ada), Sender: []byte(ada), Text: []byte("later")}}, abi.EventTagIncoming},
		{&abi.Command{Tag: abi.CommandTagDemoTyping, DemoTyping: abi.CommandDemoTyping{DelayMS: 300, Chat: []byte(ada), Sender: []byte(ada), Composing: true}}, abi.EventTagTyping},
	} {
		t0 := time.Now()
		s.send(tc.cmd)
		s.until(tc.tag.String(), isTag(tc.tag))
		if d := time.Since(t0); d < 300*time.Millisecond || d > 2*time.Second {
			t.Errorf("%v after %v", tc.tag, d)
		}
	}
}

// DemoPanic inside Send fails that call with ZF_E_PANIC and poisons the
// core, which still drains and closes; on the demo's goroutine it is a
// Fatal event.
func TestDemoPanic(t *testing.T) {
	s := openDemo(t)
	s.send(&abi.Command{Tag: abi.CommandTagDemoPanic, DemoPanic: abi.CommandDemoPanic{OnThread: true}})
	var fatal [2]string
	s.until("Fatal", func(e *abi.Event) bool {
		fatal = [2]string{string(e.Fatal.Origin), string(e.Fatal.Thread)}
		return e.Tag == abi.EventTagFatal
	})
	if fatal != [2]string{"engine", "zf-demo"} {
		t.Errorf("Fatal from %v", fatal)
	}

	s = openDemo(t)
	err := s.c.Send(&abi.Command{Tag: abi.CommandTagDemoPanic})
	if !core.IsCode(err, core.EPanic) || !strings.Contains(err.Error(), "poisoned") {
		t.Errorf("in Send: %v", err)
	}
	if err := s.c.Send(&abi.Command{Tag: abi.CommandTagSetOnline}); !core.IsCode(err, core.EPoisoned) {
		t.Errorf("after: %v", err)
	}
	s.until("Fatal", func(e *abi.Event) bool { return e.Tag == abi.EventTagFatal && string(e.Fatal.Origin) == "abi" })
	if err := s.c.Close(5 * time.Second); err != nil {
		t.Error(err)
	}
}
