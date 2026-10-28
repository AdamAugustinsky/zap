package demo

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"zapfast/abi"
	"zapfast/gocore"
	"zapfast/gocore/av"
	"zapfast/gocore/out"
)

// command answers one command the demo answers (Disposition Answers), as
// zfcore's State::command does.
func (s *state) command(cmd *gocore.Command) {
	switch cmd.Tag {
	case abi.CommandTagLoadChat:
		c := &cmd.LoadChat
		var ms []*message
		if !c.HasBefore {
			ms = s.messages[string(c.Chat)]
		}
		s.emitMessages(string(c.Chat), ms, c.HasBefore, true)
	case abi.CommandTagSendText:
		c := &cmd.SendText
		s.send(string(c.Chat), textContent(string(c.Text)), c.Quoting, c.HasQuoting, c.Mentions, nil)
	case abi.CommandTagSendLocation:
		c := &cmd.SendLocation
		content := abi.Content{Tag: abi.ContentTagLocation}
		content.Location = abi.ContentLocation{Latitude: c.Latitude, Longitude: c.Longitude}
		s.send(string(c.Chat), content, c.Quoting, c.HasQuoting, nil, nil)
	case abi.CommandTagSendImage:
		c := &cmd.SendImage
		content := abi.Content{Tag: abi.ContentTagImage}
		content.Image.Media = abi.Media{Mime: bs("image/png"), Size: uint64(len(cmd.Blob)),
			Width: c.Width, HasWidth: true, Height: c.Height, HasHeight: true}
		content.Image.Caption, content.Image.HasCaption = c.Caption, c.HasCaption
		s.send(string(c.Chat), content, c.Quoting, c.HasQuoting, c.Mentions, nil)
	case abi.CommandTagSendVoice:
		// As the worker does: normalized, measured, encoded, and filed
		// where it can be played back.
		c := &cmd.SendVoice
		samples := make([]float32, len(cmd.Blob)/4)
		for i := range samples {
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(cmd.Blob[4*i:]))
		}
		av.Normalize(samples)
		name := fmt.Sprintf("sent-%d-%d.ogg", os.Getpid(), sentVoice.Add(1))
		content := voiceNote(name, samples, true)
		if content.Audio.Media.HasPath {
			s.sentVoice = append(s.sentVoice, string(content.Audio.Media.Path))
		}
		s.send(string(c.Chat), content, c.Quoting, c.HasQuoting, nil, nil)
	case abi.CommandTagOpenLog:
		// As the headless worker does: the host opens the file.
		ev := abi.Event{Tag: abi.EventTagOpenPath}
		ev.OpenPath.Path = cmd.OpenLog.Path
		s.out.Emit(&ev)
	case abi.CommandTagReconnect:
		s.emitLink(abi.LinkStatusTagConnecting)
		s.emitLink(abi.LinkStatusTagConnected)
	case abi.CommandTagMarkRead:
		chat := string(cmd.MarkRead.Chat)
		if c := s.chat(chat); c != nil {
			c.Unread, c.MarkedUnread = 0, false
		}
		s.emitChat(chat)
	case abi.CommandTagSetMuted:
		chat := string(cmd.SetMuted.Chat)
		if c := s.chat(chat); c != nil {
			c.MutedUntil, c.HasMutedUntil = cmd.SetMuted.Until, cmd.SetMuted.HasUntil
		}
		s.emitChat(chat)
	case abi.CommandTagWatchReceipts:
		c := &cmd.WatchReceipts
		s.hasWatching = c.HasTarget
		s.watching = [2]string{string(c.Target.Chat), string(c.Target.Message)}
		s.emitReceipts()
	case abi.CommandTagFetchAvatar:
		c := &cmd.FetchAvatar
		ev := abi.Event{Tag: abi.EventTagAvatar}
		ev.Avatar = abi.EventAvatar{ID: c.ID, Full: c.Full}
		if p := avatarPath(string(c.ID)); p != "" {
			ev.Avatar.Path, ev.Avatar.HasPath = opt(p)
		}
		s.out.Emit(&ev)
	case abi.CommandTagListChatShared:
		s.listChatShared(string(cmd.ListChatShared.Chat), cmd.ListChatShared.Request)
	case abi.CommandTagLoadStorageStats:
		s.emitStorage(cmd.LoadStorageStats.Token, false)
	case abi.CommandTagClearMediaCache:
		s.cacheCleared = true
		s.emitStorage(cmd.ClearMediaCache.Token, true)
	case abi.CommandTagFetchAccountPrivacy:
		ev := abi.Event{Tag: abi.EventTagAccountPrivacy}
		ev.AccountPrivacy.Values = slices.Clone(s.privacy)
		s.out.Emit(&ev)
	case abi.CommandTagSetAccountPrivacy:
		c := &cmd.SetAccountPrivacy
		i := slices.IndexFunc(s.privacy, func(v abi.PrivacyValue) bool { return v.Kind == c.Kind })
		if i >= 0 {
			s.privacy[i].Choice = c.Choice
		} else {
			s.privacy = append(s.privacy, abi.PrivacyValue{Kind: c.Kind, Choice: c.Choice})
		}
		ev := abi.Event{Tag: abi.EventTagAccountPrivacySaved}
		ev.AccountPrivacySaved.Kind = c.Kind
		s.out.Emit(&ev)
	case abi.CommandTagStartCall:
		c := &cmd.StartCall
		s.place(string(c.Chat), c.Video, c.Generation, s.e.flags.held(c.Cancel), false)
	case abi.CommandTagJoinCallLink:
		c := &cmd.JoinCallLink
		s.place("", c.Video, c.Generation, s.e.flags.held(c.Cancel), true)
	case abi.CommandTagAnswerCall:
		s.answerCall()
	case abi.CommandTagDeclineCall:
		if s.call != nil && s.call.update.Phase == abi.CallPhaseIncoming {
			s.call.update.Phase = abi.CallPhaseEnded
			s.call.update.Outcome, s.call.update.HasOutcome = abi.CallStatusDeclined, true
			s.emitCall()
		}
	case abi.CommandTagHangupCall:
		s.hangup()
	case abi.CommandTagSetCallMuted:
		s.changeCall(func(u *abi.CallUpdate) { u.Muted = cmd.SetCallMuted.Muted })
	case abi.CommandTagSetCallCamera:
		s.changeCall(func(u *abi.CallUpdate) {
			// Our camera turns a voice call into a video call, and the
			// demo's peer answers with theirs.
			u.CameraOn = cmd.SetCallCamera.On
			u.Video = u.Video || cmd.SetCallCamera.On
		})
	case abi.CommandTagSetCallMicrophone:
		s.changeCall(func(u *abi.CallUpdate) {
			u.Microphone, u.HasMicrophone = cmd.SetCallMicrophone.Device, cmd.SetCallMicrophone.HasDevice
		})
	case abi.CommandTagSetCallSpeaker:
		s.changeCall(func(u *abi.CallUpdate) {
			u.Speaker, u.HasSpeaker = cmd.SetCallSpeaker.Device, cmd.SetCallSpeaker.HasDevice
		})
	case abi.CommandTagSetCallCameraDevice:
		s.changeCall(func(u *abi.CallUpdate) {
			u.Camera, u.HasCamera = cmd.SetCallCameraDevice.Device, cmd.SetCallCameraDevice.HasDevice
		})
	case abi.CommandTagRefreshCallDevices:
		s.emitDevices()
	case abi.CommandTagLoadCallLog:
		s.emitCallLog()
	case abi.CommandTagSearchGifs:
		s.searchGifs(cmd.SearchGifs.Query, string(cmd.SearchGifs.Key))
	case abi.CommandTagSendGif:
		c := &cmd.SendGif
		// The still the result showed, over upstream's stock clip.
		var still []byte
		for _, g := range gifs {
			if string(c.Gif.ID) == "demo-gif-"+g.name {
				still = gifStill(g.name)
				break
			}
		}
		content := video(stock("gif-launch.mp4", gifClip), c.Gif.Width, c.Gif.Height, "", true, false)
		content.Video.Media.Size = uint64(len(gifClip))
		content.Video.Media.Width, content.Video.Media.HasWidth = c.Gif.Width, true
		content.Video.Media.Height, content.Video.Media.HasHeight = c.Gif.Height, true
		s.send(string(c.Chat), content, c.Quoting, c.HasQuoting, nil, still)
	case abi.CommandTagLoadChatMedia:
		s.loadChatMedia(cmd.LoadChatMedia.Chat, cmd.LoadChatMedia.Around, cmd.LoadChatMedia.HasAround)
	case abi.CommandTagVotePoll:
		s.votePoll(string(cmd.VotePoll.Chat), string(cmd.VotePoll.Message), cmd.VotePoll.Choices)
	case abi.CommandTagRecentStickers:
		s.emitStickers()
	case abi.CommandTagSendSticker:
		c := &cmd.SendSticker
		path := string(c.Path)
		if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
			s.emitError("Demo: there is no such sticker.")
			return
		}
		s.send(string(c.Chat), stickerContent(path), c.Quoting, c.HasQuoting, nil, nil)
		s.stickers.used(path)
		s.emitStickers()
	case abi.CommandTagRemoveRecentSticker:
		s.stickers.recent = without(s.stickers.recent, string(cmd.RemoveRecentSticker.Path))
		s.emitStickers()
	case abi.CommandTagSaveSticker:
		path := string(cmd.SaveSticker.Path)
		if !slices.ContainsFunc(s.stickers.favorites, func(p string) bool { return samePath(p, path) }) {
			s.stickers.favorites = append([]string{path}, s.stickers.favorites...)
		}
		s.stickers.received = without(s.stickers.received, path)
		s.emitStickers()
	case abi.CommandTagForgetSticker:
		s.stickers.favorites = without(s.stickers.favorites, string(cmd.ForgetSticker.Path))
		s.emitStickers()
	case abi.CommandTagSendStickerPack:
		c := &cmd.SendStickerPack
		if pk := s.pack(string(c.Dir)); pk != nil {
			content := abi.Content{Tag: abi.ContentTagStickerPack}
			content.StickerPack = abi.ContentStickerPack{Name: bs(pk.name), Publisher: bs("Demo"), Count: uint32(len(pk.stickers))}
			s.send(string(c.Chat), content, abi.ReplyTarget{}, false, nil, nil)
		}
	case abi.CommandTagViewStickerPack:
		s.viewStickerPack(string(cmd.ViewStickerPack.Chat), string(cmd.ViewStickerPack.Message))
	case abi.CommandTagAddStickerPack:
		c := &cmd.AddStickerPack
		dir, name := string(c.Dir), string(c.Name)
		if pk := s.pack(dir); pk != nil {
			pk.name = name
		} else {
			var files []string
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if filepath.Ext(e.Name()) == ".webp" {
						files = append(files, filepath.Join(dir, e.Name()))
					}
				}
			}
			slices.Sort(files)
			s.stickers.packs = append(s.stickers.packs, stickerPack{name: name, dir: dir, stickers: files})
		}
		s.emitInfo("Added sticker pack \"" + name + "\"")
		s.emitStickers()
	case abi.CommandTagSetLocked:
		// As the worker files it: the chat, then its update.
		chat := string(cmd.SetLocked.Chat)
		if c := s.chat(chat); c != nil {
			c.Locked = cmd.SetLocked.Locked
		}
		s.emitChat(chat)
	case abi.CommandTagEnsureChat:
		chat := string(cmd.EnsureChat.Chat)
		if s.chat(chat) == nil {
			c := newChat(chat, string(cmd.EnsureChat.Name))
			c.LastActivity = now()
			s.chats = append([]*abi.Chat{c}, s.chats...)
			if _, ok := s.messages[chat]; !ok {
				s.messages[chat] = nil
			}
			s.emitChat(chat)
		}
	}
}

func (s *state) pack(dir string) *stickerPack {
	for i := range s.stickers.packs {
		if samePath(s.stickers.packs[i].dir, dir) {
			return &s.stickers.packs[i]
		}
	}
	return nil
}

func (s *state) emitStorage(token uint64, cleared bool) {
	ev := abi.Event{Tag: abi.EventTagStorageStats}
	ev.StorageStats = abi.EventStorageStats{Stats: s.storage(), HasStats: true, Token: token, Cleared: cleared}
	s.out.Emit(&ev)
}

// listChatShared is the chat's shared media, documents and links, newest
// first (upstream's ChatMedia::collect, at most 200 each). A chat with no
// link of its own lists two, on its first line.
func (s *state) listChatShared(chat string, request uint64) {
	ms, ok := s.messages[chat]
	if !ok {
		ev := abi.Event{Tag: abi.EventTagChatShared}
		ev.ChatShared = abi.EventChatShared{Request: request, Chat: bs(""), Last: true}
		ev.ChatShared.Error, ev.ChatShared.HasError = opt(out.Toast("Demo: there is no such chat."))
		s.out.Emit(&ev)
		return
	}
	const limit = 200
	rows := slices.Clone(ms)
	slices.SortStableFunc(rows, func(a, b *message) int { return cmp.Compare(b.Timestamp, a.Timestamp) })
	var media, docs []*message
	var links []abi.SharedEntryLink
	for _, m := range rows {
		switch m.Content.Tag {
		case abi.ContentTagImage, abi.ContentTagVideo:
			media = append(media, m)
		case abi.ContentTagDocument:
			docs = append(docs, m)
		case abi.ContentTagText:
			for _, l := range webLinks(&m.Content) {
				l.Message, l.Timestamp = m.ID, m.Timestamp
				links = append(links, l)
			}
		}
	}
	mediaCut, docsCut, linksCut := len(media) > limit, len(docs) > limit, len(links) > limit
	media, docs, links = media[:min(len(media), limit)], docs[:min(len(docs), limit)], links[:min(len(links), limit)]
	if len(links) == 0 && len(ms) > 0 {
		first := ms[0]
		for _, l := range [][2]string{
			{"https://www.rust-lang.org/", "Rust Programming Language"},
			{"https://github.com/crmne/zapfast", "ZapFast"},
		} {
			links = append(links, abi.SharedEntryLink{Message: first.ID, Timestamp: first.Timestamp, URL: bs(l[0]), Title: bs(l[1]), HasTitle: true})
		}
	}
	type row struct {
		m    *message
		doc  bool
		link *abi.SharedEntryLink
	}
	var all []row
	for _, m := range media {
		all = append(all, row{m: m})
	}
	for _, m := range docs {
		all = append(all, row{m: m, doc: true})
	}
	for i := range links {
		all = append(all, row{link: &links[i]})
	}
	out.Chunks(s.out, all, func(r *out.Record, x row) abi.SharedEntry {
		switch {
		case x.link != nil:
			return abi.SharedEntry{Tag: abi.SharedEntryTagLink, Link: *x.link}
		case x.doc:
			e := abi.SharedEntry{Tag: abi.SharedEntryTagDoc}
			e.Doc.Message = messageRecord(r, x.m)
			return e
		}
		e := abi.SharedEntry{Tag: abi.SharedEntryTagMedia}
		e.Media.Message = messageRecord(r, x.m)
		return e
	}, func(batch uint32, last bool, entries []abi.SharedEntry) *abi.Event {
		return &abi.Event{Tag: abi.EventTagChatShared, ChatShared: abi.EventChatShared{
			Request: request, Chat: bs(chat), Batch: batch, Last: last,
			MediaTruncated: mediaCut && last, DocsTruncated: docsCut && last, LinksTruncated: linksCut && last,
			Entries: entries,
		}}
	})
}

// loadChatMedia is the whole chat's photos and clips, oldest first: the
// demo's chats are short enough to be one page around any of them
// (upstream's MediaItem::of_content, with files that exist).
func (s *state) loadChatMedia(chat, around []byte, hasAround bool) {
	type item struct {
		m    *message
		path string
	}
	var items []item
	for _, m := range s.messages[string(chat)] {
		if _, ok := galleryKind(&m.Content); !ok {
			continue
		}
		path := ""
		if md := contentMedia(&m.Content); md != nil && md.HasPath {
			if _, err := os.Stat(string(md.Path)); err == nil {
				path = string(md.Path)
			}
		}
		if path != "" && !galleryFile(path) {
			continue
		}
		items = append(items, item{m, path})
	}
	out.Chunks(s.out, items, func(r *out.Record, it item) abi.MediaItem {
		isVideo, _ := galleryKind(&it.m.Content)
		x := abi.MediaItem{ID: it.m.ID, Timestamp: it.m.Timestamp, Video: isVideo}
		if it.path != "" {
			x.Path, x.HasPath = opt(it.path)
		}
		if it.m.thumb != nil {
			x.Thumbnail, x.HasThumbnail = r.Blob(it.m.thumb), true
		}
		return x
	}, func(batch uint32, last bool, chunk []abi.MediaItem) *abi.Event {
		return &abi.Event{Tag: abi.EventTagChatMedia, ChatMedia: abi.EventChatMedia{
			Chat: chat, Around: around, HasAround: hasAround, Batch: batch, Last: last, Items: chunk,
		}}
	})
}

// votePoll counts a vote as upstream's demo does: ours moves from the old
// choices to the new ones.
func (s *state) votePoll(chat, id string, choices []uint64) {
	ev := abi.Event{Tag: abi.EventTagPollVoted}
	ev.PollVoted = abi.EventPollVoted{Chat: bs(chat), Message: bs(id)}
	row := s.find(chat, id)
	if row == nil || row.Content.Tag != abi.ContentTagPoll {
		ev.PollVoted.Error, ev.PollVoted.HasError = opt(out.Toast("Demo: that message is not a poll."))
		s.out.Emit(&ev)
		return
	}
	st := &row.Content.Poll.State
	counts := slices.Clone(st.Counts)
	for _, old := range st.Selected {
		if old < uint64(len(counts)) && counts[old] > 0 {
			counts[old]--
		}
	}
	if len(st.Selected) > 0 && st.Voters > 0 {
		st.Voters--
	}
	for _, n := range choices {
		if n < uint64(len(counts)) {
			counts[n]++
		}
	}
	if len(choices) > 0 {
		st.Voters++
	}
	st.Counts = counts
	st.Selected = slices.Clone(choices)
	r := s.out.Record()
	up := abi.Event{Tag: abi.EventTagMessageUpdated}
	up.MessageUpdated.Message = messageRecord(r, row)
	r.Emit(&up)
	s.out.Emit(&ev)
}

func (s *state) searchGifs(query []byte, key string) {
	if key == BadGifKey {
		ev := abi.Event{Tag: abi.EventTagGifs}
		ev.Gifs = abi.EventGifs{Query: query, HasError: true, Last: true}
		ev.Gifs.Error = abi.GifFailure{Message: bs(out.Toast("GIPHY rejected the API key (error 401).")), BadKey: true}
		s.out.Emit(&ev)
		return
	}
	words := strings.ToLower(strings.TrimSpace(string(query)))
	dir := filepath.Join(MediaDir(), "gifs")
	var found []abi.Gif
	for _, g := range gifs {
		if words != "" && !strings.Contains(g.name, words) &&
			!slices.ContainsFunc(g.tags, func(t string) bool { return strings.Contains(t, words) || strings.Contains(words, t) }) {
			continue
		}
		x := abi.Gif{ID: bs("demo-gif-" + g.name), Mp4: bs(""), Width: 320, Height: 240}
		if p := writeOnce(dir, g.name+".jpg", gifStill(g.name)); p != "" {
			x.Still, x.HasStill = opt(p)
		}
		found = append(found, x)
	}
	out.List(s.out, found, func(batch uint32, last bool, chunk []abi.Gif) *abi.Event {
		return &abi.Event{Tag: abi.EventTagGifs, Gifs: abi.EventGifs{Query: query, Batch: batch, Last: last, Gifs: chunk}}
	})
}

func (s *state) viewStickerPack(chat, id string) {
	ev := abi.Event{Tag: abi.EventTagStickerPackPreview}
	res := &ev.StickerPackPreview.Result
	fail := func(msg string) {
		res.Tag = abi.StickerPackResultTagErr
		res.Err.Error = bs(out.Toast(msg))
	}
	m := s.find(chat, id)
	switch {
	case m == nil || m.Content.Tag != abi.ContentTagStickerPack:
		fail("Demo: that message is not a sticker pack.")
	default:
		name := string(m.Content.StickerPack.Name)
		i := slices.IndexFunc(s.stickers.packs, func(p stickerPack) bool { return p.name == name })
		if i < 0 {
			fail("Demo: the pack's stickers are not here.")
			break
		}
		pk := s.stickers.packs[i]
		res.Tag = abi.StickerPackResultTagOk
		res.Ok.Pack = abi.StickerPackInfo{Name: bs(pk.name), Dir: bs(pk.dir), Local: pk.local}
		for _, p := range pk.stickers {
			res.Ok.Pack.Stickers = append(res.Ok.Pack.Stickers, bs(p))
		}
		res.Ok.Publisher = m.Content.StickerPack.Publisher
	}
	s.out.Emit(&ev)
}

// emitReceipts is the followed message's receipts: who it went to, and
// when it was delivered and read as far as its status says, with
// upstream's partial cases (no receipts at all, receipts with no saved
// audience, a recipient outside the saved audience).
func (s *state) emitReceipts() {
	if !s.hasWatching {
		return
	}
	chat, id := s.watching[0], s.watching[1]
	m := s.find(chat, id)
	if m == nil {
		return
	}
	var people []string
	switch {
	case !m.FromMe:
	case strings.HasSuffix(chat, "@g.us"):
		people = membersOf(chat)
	default:
		people = []string{chat}
	}
	st := m.Status
	type at struct {
		v   int64
		has bool
	}
	var delivered, read, played at
	if st == abi.DeliveryDelivered || st == abi.DeliveryRead || st == abi.DeliveryPlayed {
		delivered = at{m.Timestamp + 2, true}
		if m.HasDeliveredAt {
			delivered.v = m.DeliveredAt
		}
	}
	if st == abi.DeliveryRead || st == abi.DeliveryPlayed {
		read = at{m.Timestamp + 40, true}
		if m.HasReadAt {
			read.v = m.ReadAt
		}
	}
	if st == abi.DeliveryPlayed {
		played = at{m.Timestamp + 90, true}
	}
	var rs []abi.Recipient
	for _, p := range people {
		rs = append(rs, abi.Recipient{ID: bs(p), Expected: true,
			DeliveredAt: delivered.v, HasDeliveredAt: delivered.has,
			ReadAt: read.v, HasReadAt: read.has,
			PlayedAt: played.v, HasPlayedAt: played.has})
	}
	switch {
	// A saved audience with one outside it: Ada read it, Grace has it, and
	// Katherine joined after it was sent, so she was never expected and is
	// in no section.
	case chat == berlin && id == "Rust-Berlin-1":
		for i := range rs {
			switch string(rs[i].ID) {
			case grace:
				rs[i].ReadAt, rs[i].HasReadAt = 0, false
			case katherine:
				rs[i] = abi.Recipient{ID: rs[i].ID}
			}
		}
	// Sent while only some receipts were kept: no saved audience, Ada's
	// read and Grace's delivery came in, Katherine's not.
	case chat == berlin && id == "DEMORECEIPTS0":
		for i := range rs {
			rs[i].Expected = false
			switch string(rs[i].ID) {
			case grace:
				rs[i].ReadAt, rs[i].HasReadAt = 0, false
			case katherine:
				rs[i].DeliveredAt, rs[i].HasDeliveredAt = 0, false
				rs[i].ReadAt, rs[i].HasReadAt = 0, false
			}
		}
	// Sent before receipts were recorded: none are known.
	case chat == family && id == "Family-1":
		rs = nil
	}
	ev := abi.Event{Tag: abi.EventTagReceipts}
	ev.Receipts = abi.EventReceipts{Chat: bs(chat), Message: bs(id), Recipients: rs}
	s.out.Emit(&ev)
}
