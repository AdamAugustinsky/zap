package demo

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// The Go demo against zfcore's, flow by flow. Each flow runs on a fresh Go
// demo core, and its records must equal, record for record (after
// session.norm, relative times within two seconds), the ones zfcore's demo
// queued for the same flow: testdata/flows/<flow>.txt, one show(record) a
// line. Those files, testdata/send-answers.txt and testdata/disposition.txt
// were captured from zfcore's demo (zapfast_core over whatsapp-rust 5e72baf,
// the Rust core this repository carried until WP9) and can no longer be
// regenerated here: the Rust core is gone. -update rewrites them from the Go
// demo, for a deliberate change to the demo; the diff is then the review.

var update = flag.Bool("update", false, "rewrite testdata from the Go demo")

// openFlow opens the demo core the golden files are compared with (or, with
// -update, written from).
var openFlow = openGo

// flow is a script of commands, each with the event that ends its step.
type flow func(s *session)

func runFlow(t *testing.T, name string, f flow) {
	t.Helper()
	if testing.Short() {
		t.Skip("the flows take seconds")
	}
	t.Run(name, func(t *testing.T) {
		s := newSession(t, "go", openFlow)
		f(s)
		got := make([]string, len(s.events))
		for i, r := range s.events {
			got[i] = show(r)
		}
		golden(t, filepath.Join("testdata", "flows", name+".txt"), got, sameLine)
	})
}

// golden compares got with the lines of path (equal: same), or writes them
// with -update.
func golden(t *testing.T, path string, got []string, equal func(want, got string) bool) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := readLines(t, path)
	t.Logf("%s: want %d records, go %d", path, len(want), len(got))
	bad := 0
	for i := range max(len(want), len(got)) {
		w, g := "(none)", "(none)"
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if !equal(w, g) {
			if bad++; bad <= 4 {
				t.Errorf("%s: record %d differs\nzfcore %s\ngo     %s", path, i, w, g)
			}
		}
	}
	if bad > 4 {
		t.Errorf("%s: %d records differ in all (zfcore %d records, go %d)", path, bad, len(want), len(got))
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// relativeTime is show's form of a relative time.
var relativeTime = regexp.MustCompile(`\bt[+-]\d+\b`)

// sameLine compares two shown records: equal but for their relative times,
// which may differ by two seconds.
func sameLine(want, got string) bool {
	if relativeTime.ReplaceAllString(want, "t") != relativeTime.ReplaceAllString(got, "t") {
		return false
	}
	w, g := relativeTime.FindAllString(want, -1), relativeTime.FindAllString(got, -1)
	for i := range w {
		a, _ := strconv.ParseInt(w[i][1:], 10, 64)
		b, _ := strconv.ParseInt(g[i][1:], 10, 64)
		if a-b > 2 || b-a > 2 {
			return false
		}
	}
	return true
}

func cmdOf(tag abi.CommandTag) *abi.Command { return &abi.Command{Tag: tag} }

func isTag(tag abi.EventTag) func(*abi.Event) bool {
	return func(e *abi.Event) bool { return e.Tag == tag }
}

// lastOf waits for the last record of a chunked list.
func lastOfList(tag abi.EventTag) func(*abi.Event) bool {
	return func(e *abi.Event) bool {
		if e.Tag != tag {
			return false
		}
		switch tag {
		case abi.EventTagChats:
			return e.Chats.Last
		case abi.EventTagMessages:
			return e.Messages.Last
		case abi.EventTagContacts:
			return e.Contacts.Last
		case abi.EventTagStickers:
			return e.Stickers.Last
		case abi.EventTagGifs:
			return e.Gifs.Last
		case abi.EventTagChatShared:
			return e.ChatShared.Last
		case abi.EventTagChatMedia:
			return e.ChatMedia.Last
		case abi.EventTagCallLog:
			return e.CallLog.Last
		}
		return true
	}
}

// started is the opening: the account, the chats, the link, the contacts.
func started(s *session) {
	if err := s.c.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.until("contacts", lastOfList(abi.EventTagContacts))
}

// read waits for the status of our newest send to reach read, and its
// chat's row after it.
func (s *session) read() {
	s.t.Helper()
	read := false
	s.until("read and the chat after it", func(e *abi.Event) bool {
		if e.Tag == abi.EventTagMessageUpdated && e.MessageUpdated.Message.Status == abi.DeliveryRead {
			read = true
			return false
		}
		return read && e.Tag == abi.EventTagChatUpdated
	})
}

func load(s *session, chat string) {
	c := cmdOf(abi.CommandTagLoadChat)
	c.LoadChat.Chat = []byte(chat)
	s.send(c)
	s.until("messages of "+chat, lastOfList(abi.EventTagMessages))
}

var chats = []string{ada, berlin, grace, katherine, family, margaret, dorothy}

func TestTheOpeningIsZfcores(t *testing.T) {
	runFlow(t, "opening", func(s *session) {
		started(s)
		s.settle(100 * time.Millisecond)
	})
}

func TestEveryChatLoadsAsZfcoresDoes(t *testing.T) {
	runFlow(t, "load", func(s *session) {
		started(s)
		for _, c := range chats {
			load(s, c)
		}
		load(s, "1@s.whatsapp.net")
		c := cmdOf(abi.CommandTagLoadChat)
		c.LoadChat.Chat, c.LoadChat.HasBefore = []byte(ada), true
		c.LoadChat.Before = abi.PageKey{Timestamp: 1, ID: []byte("Ada-Lovelace-0")}
		s.send(c)
		s.until("older", lastOfList(abi.EventTagMessages))
	})
}

func TestSendsGoAsZfcoresDo(t *testing.T) {
	runFlow(t, "sends", func(s *session) {
		started(s)
		c := cmdOf(abi.CommandTagSendText)
		c.SendText = abi.CommandSendText{Chat: []byte(ada), Text: []byte("hi @393331234567\nsecond line"),
			Quoting: abi.ReplyTarget{ID: []byte("Ada-Lovelace-0")}, HasQuoting: true, Mentions: [][]byte{[]byte(ada)}}
		s.send(c)
		s.read()
		// A cross-chat quote, from Rust Berlin into Ada's chat.
		c = cmdOf(abi.CommandTagSendText)
		c.SendText = abi.CommandSendText{Chat: []byte(ada), Text: []byte("about this"),
			Quoting: abi.ReplyTarget{ID: []byte("Rust-Berlin-0"), Chat: []byte(berlin), HasChat: true}, HasQuoting: true}
		s.send(c)
		s.read()
		// A quote the demo does not have, and a chat it does not have.
		c = cmdOf(abi.CommandTagSendText)
		c.SendText = abi.CommandSendText{Chat: []byte(grace), Text: []byte("x"), Quoting: abi.ReplyTarget{ID: []byte("nope")}, HasQuoting: true}
		s.send(c)
		s.read()
		c = cmdOf(abi.CommandTagSendText)
		c.SendText = abi.CommandSendText{Chat: []byte("1@s.whatsapp.net"), Text: []byte("x")}
		s.send(c)
		s.until("the error", isTag(abi.EventTagError))
		c = cmdOf(abi.CommandTagSendLocation)
		c.SendLocation = abi.CommandSendLocation{Chat: []byte(katherine), Latitude: 1.5, Longitude: -2.25}
		s.send(c)
		s.read()
		rgba, err := s.c.PutBlob(make([]byte, 3*2*4))
		if err != nil {
			s.t.Fatal(err)
		}
		c = cmdOf(abi.CommandTagSendImage)
		c.SendImage = abi.CommandSendImage{Chat: []byte(ada), Width: 3, Height: 2, RGBA: rgba, Caption: []byte("look"), HasCaption: true}
		s.send(c)
		s.read()
		// A second of a quiet tone, normalised and encoded.
		pcm := make([]byte, 4*48000)
		for i := range 48000 {
			binary.LittleEndian.PutUint32(pcm[4*i:], math.Float32bits(float32(0.05*math.Sin(float64(i)/20))))
		}
		samples, err := s.c.PutBlob(pcm)
		if err != nil {
			s.t.Fatal(err)
		}
		c = cmdOf(abi.CommandTagSendVoice)
		c.SendVoice = abi.CommandSendVoice{Chat: []byte(grace), Samples: samples}
		s.send(c)
		s.read()
		for _, e := range s.events {
			// The encoders differ (libopus, AudioToolbox): a sent note's
			// file is not the same size.
			if msgs, ok := e["Messages"].(rec); ok {
				for _, m := range msgs["Messages"].([]any) {
					if c := m.(rec)["Content"].(rec); c["Tag"] == "Audio" {
						delete(c["Audio"].(rec)["Media"].(rec), "Size")
					}
				}
			}
			if up, ok := e["MessageUpdated"].(rec); ok {
				if c := up["Message"].(rec)["Content"].(rec); c["Tag"] == "Audio" {
					delete(c["Audio"].(rec)["Media"].(rec), "Size")
				}
			}
		}
	})
}

func TestInjectedMessagesAndTypingComeAsZfcoresDo(t *testing.T) {
	runFlow(t, "inject", func(s *session) {
		started(s)
		c := cmdOf(abi.CommandTagDemoIncoming)
		c.DemoIncoming = abi.CommandDemoIncoming{DelayMS: 40, Chat: []byte(ada), Sender: []byte(ada), Text: []byte("hello")}
		s.send(c)
		c = cmdOf(abi.CommandTagDemoTyping)
		c.DemoTyping = abi.CommandDemoTyping{DelayMS: 10, Chat: []byte(ada), Sender: []byte(ada), Composing: true}
		s.send(c)
		s.until("incoming", isTag(abi.EventTagIncoming))
		c = cmdOf(abi.CommandTagDemoIncoming)
		c.DemoIncoming = abi.CommandDemoIncoming{Chat: []byte("4930123456@s.whatsapp.net"), Sender: []byte("4930123456@s.whatsapp.net"),
			SenderName: []byte("Mary Jackson"), HasSenderName: true, Text: []byte("New here")}
		s.send(c)
		s.until("incoming", func(e *abi.Event) bool { return e.Tag == abi.EventTagIncoming && string(e.Incoming.Chat) != ada })
		load(s, "4930123456@s.whatsapp.net")
	})
}

func TestTheChatsSharedMediaAndReceiptsAreZfcores(t *testing.T) {
	runFlow(t, "shared", func(s *session) {
		started(s)
		for i, chat := range []string{ada, grace, dorothy, "1@s.whatsapp.net"} {
			c := cmdOf(abi.CommandTagListChatShared)
			c.ListChatShared = abi.CommandListChatShared{Chat: []byte(chat), Request: uint64(i + 1)}
			s.send(c)
			s.until("shared", lastOfList(abi.EventTagChatShared))
		}
		for _, chat := range []string{ada, dorothy, margaret} {
			c := cmdOf(abi.CommandTagLoadChatMedia)
			c.LoadChatMedia = abi.CommandLoadChatMedia{Chat: []byte(chat), Around: []byte("Ada-media-0"), HasAround: true}
			s.send(c)
			s.until("media", lastOfList(abi.EventTagChatMedia))
		}
		for _, target := range [][2]string{
			{berlin, "Rust-Berlin-1"}, {berlin, "DEMORECEIPTS0"}, {family, "Family-1"},
			{ada, "Ada-Lovelace-1"}, {grace, "DEMOVOICE2"}, {ada, "Ada-Lovelace-0"},
		} {
			c := cmdOf(abi.CommandTagWatchReceipts)
			c.WatchReceipts = abi.CommandWatchReceipts{Target: abi.ReceiptTarget{Chat: []byte(target[0]), Message: []byte(target[1])}, HasTarget: true}
			s.send(c)
			s.until("receipts", isTag(abi.EventTagReceipts))
		}
		// A send followed while it goes from pending to read.
		c := cmdOf(abi.CommandTagSendText)
		c.SendText = abi.CommandSendText{Chat: []byte(berlin), Text: []byte("watch me")}
		s.send(c)
		s.until("the send", lastOfList(abi.EventTagMessages))
		c = cmdOf(abi.CommandTagWatchReceipts)
		c.WatchReceipts = abi.CommandWatchReceipts{Target: abi.ReceiptTarget{Chat: []byte(berlin), Message: []byte("DEMO00000001")}, HasTarget: true}
		s.send(c)
		s.read()
		s.send(cmdOf(abi.CommandTagWatchReceipts))
		s.settle(50 * time.Millisecond)
	})
}

func TestStickersAndGifsAreZfcores(t *testing.T) {
	runFlow(t, "stickers", func(s *session) {
		started(s)
		stickers := filepath.Join(MediaDir(), "stickers")
		s.send(cmdOf(abi.CommandTagRecentStickers))
		s.until("stickers", lastOfList(abi.EventTagStickers))
		for _, q := range []string{"rocket", "", "  HI ", "zzz", "no"} {
			c := cmdOf(abi.CommandTagSearchGifs)
			c.SearchGifs = abi.CommandSearchGifs{Query: []byte(q), Key: []byte("any")}
			s.send(c)
			s.until("gifs", lastOfList(abi.EventTagGifs))
		}
		c := cmdOf(abi.CommandTagSearchGifs)
		c.SearchGifs = abi.CommandSearchGifs{Query: []byte("pug"), Key: []byte(BadGifKey)}
		s.send(c)
		s.until("gifs", lastOfList(abi.EventTagGifs))
		c = cmdOf(abi.CommandTagSendGif)
		c.SendGif = abi.CommandSendGif{Chat: []byte(ada), Gif: abi.Gif{ID: []byte("demo-gif-pug"), Width: 320, Height: 240}}
		s.send(c)
		s.read()
		send := func(tag abi.EventTag, cmd *abi.Command) {
			s.send(cmd)
			s.until(cmd.Tag.String(), lastOfList(tag))
		}
		c = cmdOf(abi.CommandTagSendSticker)
		c.SendSticker = abi.CommandSendSticker{Chat: []byte(ada), Path: []byte(filepath.Join(stickers, "owl.webp"))}
		send(abi.EventTagStickers, c)
		s.read()
		c = cmdOf(abi.CommandTagSendSticker)
		c.SendSticker = abi.CommandSendSticker{Chat: []byte(ada), Path: []byte(filepath.Join(stickers, "none.webp"))}
		send(abi.EventTagError, c)
		c = cmdOf(abi.CommandTagSaveSticker)
		c.SaveSticker.Path = []byte(filepath.Join(stickers, "goat.webp"))
		send(abi.EventTagStickers, c)
		c = cmdOf(abi.CommandTagRemoveRecentSticker)
		c.RemoveRecentSticker.Path = []byte(filepath.Join(stickers, "pug.webp"))
		send(abi.EventTagStickers, c)
		c = cmdOf(abi.CommandTagForgetSticker)
		c.ForgetSticker.Path = []byte(filepath.Join(stickers, "owl.webp"))
		send(abi.EventTagStickers, c)
		c = cmdOf(abi.CommandTagSendStickerPack)
		c.SendStickerPack = abi.CommandSendStickerPack{Chat: []byte(ada), Dir: []byte(stickers)}
		s.send(c)
		s.read()
		for _, m := range [][2]string{{berlin, "DEMOPACK0"}, {berlin, "Rust-Berlin-0"}} {
			c = cmdOf(abi.CommandTagViewStickerPack)
			c.ViewStickerPack = abi.CommandViewStickerPack{Chat: []byte(m[0]), Message: []byte(m[1])}
			send(abi.EventTagStickerPackPreview, c)
		}
		c = cmdOf(abi.CommandTagAddStickerPack)
		c.AddStickerPack = abi.CommandAddStickerPack{Dir: []byte(stickers), Name: []byte("Renamed")}
		send(abi.EventTagStickers, c)
		c = cmdOf(abi.CommandTagAddStickerPack)
		c.AddStickerPack = abi.CommandAddStickerPack{Dir: []byte(filepath.Join(MediaDir(), "gifs")), Name: []byte("No stickers")}
		send(abi.EventTagStickers, c)
	})
}

func TestTheRestIsZfcores(t *testing.T) {
	runFlow(t, "rest", func(s *session) {
		started(s)
		one := func(tag abi.EventTag, cmd *abi.Command) {
			s.send(cmd)
			s.until(cmd.Tag.String(), lastOfList(tag))
		}
		c := cmdOf(abi.CommandTagMarkRead)
		c.MarkRead = abi.CommandMarkRead{Chat: []byte(ada), Receipts: true}
		one(abi.EventTagChatUpdated, c)
		c = cmdOf(abi.CommandTagSetMuted)
		c.SetMuted = abi.CommandSetMuted{Chat: []byte(berlin)}
		one(abi.EventTagChatUpdated, c)
		c = cmdOf(abi.CommandTagSetMuted)
		c.SetMuted = abi.CommandSetMuted{Chat: []byte(ada), Until: 12345, HasUntil: true}
		one(abi.EventTagChatUpdated, c)
		c = cmdOf(abi.CommandTagSetLocked)
		c.SetLocked = abi.CommandSetLocked{Chat: []byte(ada), Locked: true}
		one(abi.EventTagChatUpdated, c)
		c = cmdOf(abi.CommandTagEnsureChat)
		c.EnsureChat = abi.CommandEnsureChat{Chat: []byte("4930123456@s.whatsapp.net"), Name: []byte("Mary Jackson")}
		one(abi.EventTagChatUpdated, c)
		c = cmdOf(abi.CommandTagOpenLog)
		c.OpenLog.Path = []byte("/tmp/zapfast.log")
		one(abi.EventTagOpenPath, c)
		s.send(cmdOf(abi.CommandTagReconnect))
		s.until("connected", func(e *abi.Event) bool {
			return e.Tag == abi.EventTagLink && e.Link.Status.Tag == abi.LinkStatusTagConnected
		})
		for _, id := range []string{ada, Me, family, dorothy} {
			c = cmdOf(abi.CommandTagFetchAvatar)
			c.FetchAvatar = abi.CommandFetchAvatar{ID: []byte(id), Full: id == ada}
			one(abi.EventTagAvatar, c)
		}
		c = cmdOf(abi.CommandTagLoadStorageStats)
		c.LoadStorageStats.Token = 3
		one(abi.EventTagStorageStats, c)
		c = cmdOf(abi.CommandTagClearMediaCache)
		c.ClearMediaCache.Token = 4
		one(abi.EventTagStorageStats, c)
		one(abi.EventTagAccountPrivacy, cmdOf(abi.CommandTagFetchAccountPrivacy))
		c = cmdOf(abi.CommandTagSetAccountPrivacy)
		c.SetAccountPrivacy = abi.CommandSetAccountPrivacy{Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceNobody}
		one(abi.EventTagAccountPrivacySaved, c)
		one(abi.EventTagAccountPrivacy, cmdOf(abi.CommandTagFetchAccountPrivacy))
		for _, vote := range []struct {
			id      string
			choices []uint64
		}{{"DEMOPOLL0", []uint64{2}}, {"DEMOPOLL0", []uint64{0, 1}}, {"DEMOPOLL0", nil}, {"Margaret-Hamilton-0", []uint64{1}}} {
			c = cmdOf(abi.CommandTagVotePoll)
			c.VotePoll = abi.CommandVotePoll{Chat: []byte(margaret), Message: []byte(vote.id), Choices: vote.choices}
			one(abi.EventTagPollVoted, c)
		}
		one(abi.EventTagCallLog, cmdOf(abi.CommandTagLoadCallLog))
		one(abi.EventTagCallDevices, cmdOf(abi.CommandTagRefreshCallDevices))
		load(s, margaret)
		// What the demo only accepts gets nothing back.
		c = cmdOf(abi.CommandTagComposing)
		c.Composing = abi.CommandComposing{Chat: []byte(ada), Composing: true}
		s.send(c)
		s.send(cmdOf(abi.CommandTagResumed))
		c = cmdOf(abi.CommandTagMarkPlayed)
		c.MarkPlayed = abi.CommandMarkPlayed{Chat: []byte(grace), Message: []byte("DEMOVOICE2")}
		s.send(c)
		s.settle(100 * time.Millisecond)
	})
}

func phaseIs(p abi.CallPhase) func(*abi.Event) bool {
	return func(e *abi.Event) bool { return e.Tag == abi.EventTagCall && e.Call.Update.Phase == p }
}

// A placed video call to Ada: devices, placing, dialing, ringing,
// connecting, active, the peer's picture; muted, camera off, hung up, and
// filed.
func TestAPlacedCallIsZfcores(t *testing.T) {
	runFlow(t, "placed", func(s *session) {
		started(s)
		c := cmdOf(abi.CommandTagStartCall)
		c.StartCall = abi.CommandStartCall{Chat: []byte(ada), Video: true, Generation: 1<<40 + 1, Cancel: 1}
		s.send(c)
		s.until("active", phaseIs(abi.CallPhaseActive))
		s.until("the peer's picture", func(e *abi.Event) bool { return e.Tag == abi.EventTagCall && e.Call.Update.RemoteVideo })
		for _, stream := range []uint32{core.StreamLocal, core.StreamRemote} {
			f, ok := s.c.Frame(stream)
			for i := 0; !ok && i < 50; i++ {
				time.Sleep(10 * time.Millisecond)
				f, ok = s.c.Frame(stream)
			}
			if !ok {
				t.Fatalf("%s: no frame on stream %d", s.name, stream)
			}
			s.events = append(s.events, rec{"frame": stream, "w": f.W, "h": f.H, "stride": f.Stride, "format": f.Format, "len": len(f.Px)})
			if _, again := s.c.Frame(stream); again {
				t.Errorf("%s: stream %d's frame handed out twice", s.name, stream)
			}
			s.c.ReleaseFrame(stream)
		}
		// Placed while one is up: refused, and the call goes on.
		c = cmdOf(abi.CommandTagStartCall)
		c.StartCall = abi.CommandStartCall{Chat: []byte(grace), Generation: 1<<40 + 2, Cancel: 2}
		s.send(c)
		s.until("refused", phaseIs(abi.CallPhaseFailed))
		c = cmdOf(abi.CommandTagSetCallMuted)
		c.SetCallMuted.Muted = true
		s.send(c)
		s.until("muted", isTag(abi.EventTagCall))
		c = cmdOf(abi.CommandTagSetCallCamera)
		s.send(c)
		s.until("camera off", isTag(abi.EventTagCall))
		c = cmdOf(abi.CommandTagSetCallMicrophone)
		c.SetCallMicrophone = abi.CommandSetCallMicrophone{Device: []byte("demo-headset-mic"), HasDevice: true}
		s.send(c)
		s.until("microphone", isTag(abi.EventTagCall))
		s.send(cmdOf(abi.CommandTagHangupCall))
		s.until("filed", isTag(abi.EventTagChatUpdated))
		s.settle(150 * time.Millisecond)
	})
}

// Calls rung in: one declined, a group video call answered with its
// members' pictures, one missed while busy; a cancelled call is never
// placed; a call link is joined and filed in the call log.
func TestCallsRungInAndCancelledAreZfcores(t *testing.T) {
	runFlow(t, "rung", func(s *session) {
		started(s)
		ring := func(chat, caller string, video bool) {
			c := cmdOf(abi.CommandTagDemoIncoming)
			core.DemoCall(c, chat, caller, video, 0)
			s.send(c)
		}
		ring(grace, grace, false)
		s.until("incoming", phaseIs(abi.CallPhaseIncoming))
		s.send(cmdOf(abi.CommandTagDeclineCall))
		s.until("filed", isTag(abi.EventTagChatUpdated))

		ring(berlin, ada, true)
		s.until("incoming", phaseIs(abi.CallPhaseIncoming))
		s.send(cmdOf(abi.CommandTagAnswerCall))
		s.until("active", phaseIs(abi.CallPhaseActive))
		s.until("the members' pictures", func(e *abi.Event) bool {
			return e.Tag == abi.EventTagCall && len(e.Call.Update.Group.Participants) > 1 && e.Call.Update.Group.Participants[1].Video
		})
		// Rung while busy: missed at once.
		ring(family, margaret, false)
		s.until("the busy entry", isTag(abi.EventTagChatUpdated))
		s.send(cmdOf(abi.CommandTagHangupCall))
		s.until("filed", isTag(abi.EventTagChatUpdated))

		c := cmdOf(abi.CommandTagStartCall)
		c.StartCall = abi.CommandStartCall{Chat: []byte(katherine), Generation: 1<<40 + 3, Cancel: 3}
		s.send(c)
		c = cmdOf(abi.CommandTagCancelCall)
		c.CancelCall.Token = 3
		s.send(c)
		s.until("never placed", phaseIs(abi.CallPhaseFailed))

		c = cmdOf(abi.CommandTagJoinCallLink)
		c.JoinCallLink = abi.CommandJoinCallLink{Token: []byte("abc"), Generation: 1<<40 + 4, Cancel: 4}
		s.send(c)
		s.until("active", phaseIs(abi.CallPhaseActive))
		s.send(cmdOf(abi.CommandTagHangupCall))
		s.until("the call log", lastOfList(abi.EventTagCallLog))

		// Rung and never answered: missed after 30 s is too long to wait;
		// hung up while it rings, it is missed too.
		ring(katherine, katherine, true)
		s.until("incoming", phaseIs(abi.CallPhaseIncoming))
		s.send(cmdOf(abi.CommandTagHangupCall))
		s.until("filed", isTag(abi.EventTagChatUpdated))
		s.settle(100 * time.Millisecond)
	})
}

// sampleCommand is a command of tag whose fields Send's own checks pass, its blob
// (if any) put on c.
func sampleCommand(t *testing.T, c core.Conn, tag abi.CommandTag) (*abi.Command, abi.BlobRef, bool) {
	t.Helper()
	cmd := &abi.Command{Tag: tag}
	var ref abi.BlobRef
	put := func(n int) abi.BlobRef {
		r, err := c.PutBlob(make([]byte, n))
		if err != nil {
			t.Fatal(err)
		}
		ref = r
		return r
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
	return cmd, ref, ref.ID != 0
}

// Every command gets zfcore's demo core's answer from Send
// (testdata/send-answers.txt): refused with its code and words (taking no
// blob), or taken.
func TestSendAnswersEveryCommandAsZfcoresDoes(t *testing.T) {
	g := newSession(t, "go", openFlow)
	var got []string
	for _, tag := range allTags() {
		if tag == abi.CommandTagDemoPanic {
			continue // poisons the core: TestDemoPanic
		}
		cmd, ref, named := sampleCommand(t, g.c, tag)
		err := g.c.Send(cmd)
		var answer string
		var ce *core.Error
		switch {
		case err == nil:
			answer = "ok"
		case errors.As(err, &ce):
			answer = core.CodeName(ce.Code) + ": " + ce.Msg
		default:
			answer = err.Error()
		}
		if named {
			buf := make([]byte, ref.Len)
			_, rerr := g.c.ReadBlob(ref.ID, 0, buf)
			answer += map[bool]string{true: " (blob kept)", false: " (blob taken)"}[rerr == nil]
			g.c.FreeBlob(ref.ID)
		}
		if want := DispositionOf(tag) == Refuses; want != (answer != "ok" && answer != "ok (blob taken)") {
			t.Errorf("%v: %v, but Send says %q", tag, DispositionOf(tag), answer)
		}
		got = append(got, fmt.Sprintf("%v\t%s", tag, answer))
	}
	golden(t, filepath.Join("testdata", "send-answers.txt"), got, func(w, g string) bool { return w == g })
}
