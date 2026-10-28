package demo

import (
	"cmp"
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"zapfast/abi"
	"zapfast/gocore"
	"zapfast/gocore/out"
)

// state is the demo's world, owned by its goroutine (zfcore's State).
type state struct {
	e   *Engine
	out *out.Out

	chats    []*abi.Chat // newest activity first
	messages map[string][]*message
	contacts []abi.Contact
	timers   timers
	seq      uint64
	sent     uint64
	call     *liveCall
	callLog  []*message // newest first
	// watching is the message whose receipts are followed.
	watching     [2]string
	hasWatching  bool
	privacy      []abi.PrivacyValue
	cacheCleared bool
	// sentVoice are the voice notes this core was sent, removed when it
	// stops.
	sentVoice []string
	stickers  shelves
}

func now() int64 { return time.Now().Unix() }

func newState(e *Engine) *state {
	s := &state{e: e, out: e.h.Out(), messages: map[string][]*message{}}
	t := now()
	for _, sm := range samples {
		at := t - sm.minutesAgo*60
		var lines []*message
		for i, l := range sm.lines {
			ts := at - int64(len(sm.lines)-1-i)*60
			sender := sm.id
			if l.mine {
				sender = Me
			}
			lines = append(lines, textMessage(sm.id, fmt.Sprintf("%s-%d", strings.ReplaceAll(sm.name, " ", "-"), i), l.mine, sender, ts, l.text))
		}
		c := newChat(sm.id, sm.name)
		c.GroupSubjectKnown = c.Kind == abi.ChatKindGroup
		c.LastActivity = at
		c.Unread = sm.unread
		c.Pinned = sm.pinned
		if sm.pinned {
			c.PinnedAt = at * 1000
		}
		if sm.muted {
			c.MutedUntil, c.HasMutedUntil = 0, true
		}
		if n := len(lines); n > 0 {
			c.Last, c.HasLast = lastOf(lines[n-1]), true
		}
		c.Locked = slices.Contains(locked, sm.id)
		// The groups' metadata: we are an admin of Rust Berlin, whose info
		// only admins edit; Family is open to everyone.
		var admin, infoLocked, isGroup bool
		var about string
		switch sm.id {
		case berlin:
			admin, infoLocked, isGroup, about = true, true, true, "Rust meetups in Berlin, every second Tuesday."
		case family:
			isGroup = true
		}
		if isGroup {
			c.Participants = [][]byte{bs(Me)}
			for _, m := range membersOf(sm.id) {
				c.Participants = append(c.Participants, bs(m))
			}
			c.Admin = admin
			c.InfoLocked, c.HasInfoLocked = infoLocked, true
			c.GroupDescription, c.HasGroupDescription = opt(about)
		}
		if c.Kind != abi.ChatKindGroup {
			first, _, _ := strings.Cut(sm.name, " ")
			ct := abi.Contact{ID: bs(sm.id)}
			ct.FullName, ct.HasFullName = opt(sm.name)
			ct.FirstName, ct.HasFirstName = opt(first)
			s.contacts = append(s.contacts, ct)
		}
		s.chats = append(s.chats, c)
		s.messages[sm.id] = lines
	}
	s.sortChats()
	s.callLog = seededCallLog(t)
	s.privacy = []abi.PrivacyValue{
		{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceContacts},
		{Kind: abi.PrivacyKindOnline, Choice: abi.PrivacyChoiceSameAsLastSeen},
		{Kind: abi.PrivacyKindProfile, Choice: abi.PrivacyChoiceContacts},
		{Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceEveryone},
		{Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceContacts},
		{Kind: abi.PrivacyKindReadReceipts, Choice: abi.PrivacyChoiceEveryone},
		{Kind: abi.PrivacyKindCallAdd, Choice: abi.PrivacyChoiceEveryone},
	}
	return s
}

// sortChats orders the chats newest activity first, stably.
func (s *state) sortChats() {
	slices.SortStableFunc(s.chats, func(a, b *abi.Chat) int { return cmp.Compare(b.LastActivity, a.LastActivity) })
}

// opened is what open has without the network, the archive's identity and
// chat list, then the media, after the chat list so encoding them never
// delays it.
func (s *state) opened() {
	me := abi.Event{Tag: abi.EventTagMe}
	me.Me.ID = bs(Me)
	me.Me.Name, me.Me.HasName = opt("Demo")
	s.out.Emit(&me)
	s.emitChats()
	s.addVoiceNotes()
	s.addMedia()
}

// stop removes the voice notes this core was sent.
func (s *state) stop() {
	for _, p := range s.sentVoice {
		os.Remove(p)
	}
}

func (s *state) input(in input) {
	if in.start {
		s.emitLink(abi.LinkStatusTagConnecting)
		s.emitLink(abi.LinkStatusTagConnected)
		s.emitContacts()
		return
	}
	cmd := in.cmd
	switch cmd.Tag {
	case abi.CommandTagDemoPanic:
		panic("DemoPanic on the demo thread")
	case abi.CommandTagDemoIncoming:
		c := cmd.DemoIncoming
		s.later(time.Duration(c.DelayMS)*time.Millisecond, due{kind: dueIncoming, inject: cmd})
	case abi.CommandTagDemoTyping:
		c := cmd.DemoTyping
		s.later(time.Duration(c.DelayMS)*time.Millisecond, due{kind: dueTyping, inject: cmd})
	default:
		s.command(cmd)
	}
}

// due is something the demo does later.
type due struct {
	kind dueKind
	// dueStatus: the message and its new status.
	chat, id string
	status   abi.Delivery
	// dueIncoming, dueTyping: the command.
	inject *gocore.Command
	// dueCall: the next step of call generation, if it is still the call.
	generation uint64
	step       step
}

type dueKind uint8

const (
	dueStatus dueKind = iota
	dueIncoming
	dueTyping
	dueCall
)

func (s *state) later(after time.Duration, d due) {
	s.seq++
	heap.Push(&s.timers, &timer{at: time.Now().Add(after), seq: s.seq, due: d})
}

func (s *state) chat(id string) *abi.Chat {
	for _, c := range s.chats {
		if string(c.ID) == id {
			return c
		}
	}
	return nil
}

func (s *state) emitChat(id string) {
	if c := s.chat(id); c != nil {
		ev := abi.Event{Tag: abi.EventTagChatUpdated}
		ev.ChatUpdated.Chat = chatRecord(c)
		s.out.Emit(&ev)
	}
}

// file files a message as the worker does: the row, then its chat.
func (s *state) file(m *message, incoming bool) {
	id := string(m.Chat)
	if c := s.chat(id); c != nil {
		c.LastActivity = m.Timestamp
		c.Last, c.HasLast = lastOf(m), true
		if incoming {
			c.Unread++
		}
	}
	stored := *m
	s.messages[id] = append(s.messages[id], &stored)
	s.sortChats()
	s.emitMessages(id, []*message{m}, false, false)
	s.emitChat(id)
	if incoming {
		r := s.out.Record()
		ev := abi.Event{Tag: abi.EventTagIncoming}
		ev.Incoming.Chat = bs(id)
		ev.Incoming.Message = messageRecord(r, m)
		r.Emit(&ev)
	}
}

func (s *state) find(chat, id string) *message {
	for _, m := range s.messages[chat] {
		if m.id() == id {
			return m
		}
	}
	return nil
}

// quote is the quote a reply carries, resolved from the demo's messages as
// upstream's demo does: from the target's own chat for a cross-chat reply.
// A target the demo does not have sends the message without a quote.
func (s *state) quote(chat string, target abi.ReplyTarget, has bool) (abi.Quoted, bool) {
	if !has {
		return abi.Quoted{}, false
	}
	source := chat
	if target.HasChat {
		source = string(target.Chat)
	}
	row := s.find(source, string(target.ID))
	if row == nil {
		return abi.Quoted{}, false
	}
	return abi.Quoted{
		Chat: target.Chat, HasChat: target.HasChat, ID: target.ID,
		Sender: row.Sender, SenderName: row.SenderName, HasSenderName: row.HasSenderName,
		Summary: bs(summary(&row.Content)), Mentions: row.Mentions,
	}, true
}

// send files our message as pending, then moves it through sent,
// delivered and read, as the worker does.
func (s *state) send(chat string, content abi.Content, quoting abi.ReplyTarget, hasQuoting bool, mentions [][]byte, thumb []byte) {
	if s.chat(chat) == nil {
		s.emitError("Demo: there is no such chat.")
		return
	}
	s.sent++
	id := fmt.Sprintf("DEMO%08d", s.sent)
	m := textMessage(chat, id, true, Me, now(), "")
	m.Content = content
	m.Status = abi.DeliveryPending
	m.Quoted, m.HasQuoted = s.quote(chat, quoting, hasQuoting)
	for _, mid := range mentions {
		user, _, _ := strings.Cut(string(mid), "@")
		m.Mentions = append(m.Mentions, abi.MentionRef{User: bs(user), ID: mid})
	}
	m.thumb = thumb
	s.file(m, false)
	for _, st := range []struct {
		ms     time.Duration
		status abi.Delivery
	}{{20, abi.DeliverySent}, {60, abi.DeliveryDelivered}, {150, abi.DeliveryRead}} {
		s.later(st.ms*time.Millisecond, due{kind: dueStatus, chat: chat, id: id, status: st.status})
	}
}

func (s *state) fire(d due) {
	switch d.kind {
	case dueCall:
		s.callStep(d.generation, d.step)
	case dueStatus:
		m := s.find(d.chat, d.id)
		if m == nil {
			return
		}
		m.Status = d.status
		at := now()
		switch d.status {
		case abi.DeliveryDelivered:
			m.DeliveredAt, m.HasDeliveredAt = at, true
		case abi.DeliveryRead:
			m.ReadAt, m.HasReadAt = at, true
		}
		if c := s.chat(d.chat); c != nil && c.HasLast && c.Last.FromMe {
			c.Last.Status = d.status
		}
		r := s.out.Record()
		ev := abi.Event{Tag: abi.EventTagMessageUpdated}
		ev.MessageUpdated.Message = messageRecord(r, m)
		r.Emit(&ev)
		s.emitChat(d.chat)
		if s.hasWatching && s.watching == [2]string{d.chat, d.id} {
			s.emitReceipts()
		}
	case dueIncoming:
		c := &d.inject.DemoIncoming
		chat, sender, text := string(c.Chat), string(c.Sender), string(c.Text)
		if text == callText || text == videoCallText {
			s.ring(chat, sender, c.SenderName, c.HasSenderName, text == videoCallText)
			return
		}
		if s.chat(chat) == nil {
			name := sender
			if c.HasSenderName {
				name = string(c.SenderName)
			}
			s.chats = append(s.chats, newChat(chat, name))
		}
		s.seq++
		m := textMessage(chat, fmt.Sprintf("DEMOIN%08d", s.seq), false, sender, now(), text)
		m.SenderName, m.HasSenderName = c.SenderName, c.HasSenderName
		s.file(m, true)
	case dueTyping:
		c := &d.inject.DemoTyping
		ev := abi.Event{Tag: abi.EventTagTyping}
		ev.Typing = abi.EventTyping{Chat: c.Chat, Sender: c.Sender, Composing: c.Composing}
		s.out.Emit(&ev)
	}
}

// seededCallLog is a few calls from before the demo began, newest first.
func seededCallLog(t int64) []*message {
	const hour = 3600
	entries := []struct {
		chat, sender string
		fromMe       bool
		ago          int64
		video        bool
		status       abi.CallStatus
		duration     uint64
		group        bool
	}{
		{ada, Me, true, 2 * hour, false, abi.CallStatusAnswered, 312, false},
		{berlin, grace, false, 26 * hour, true, abi.CallStatusAnswered, 1260, true},
		{grace, grace, false, 50 * hour, false, abi.CallStatusMissed, 0, false},
		{katherine, Me, true, 74 * hour, true, abi.CallStatusNoAnswer, 0, false},
		{margaret, margaret, false, 122 * hour, false, abi.CallStatusDeclined, 0, false},
	}
	var log []*message
	for i, e := range entries {
		log = append(log, callEntry(fmt.Sprintf("%sdemo-seed-%d", callEntryPrefix, i), e.chat, e.sender, e.fromMe, t-e.ago, e.video, e.status, e.duration, e.group))
	}
	return log
}

// callEntry is one call entry, as the worker files it.
func callEntry(id, chat, sender string, fromMe bool, ts int64, video bool, status abi.CallStatus, duration uint64, group bool) *message {
	m := textMessage(chat, id, fromMe, sender, ts, "")
	m.Status = abi.DeliveryNone
	m.Content = abi.Content{Tag: abi.ContentTagCall}
	m.Content.Call = abi.ContentCall{Video: video, Status: status, Duration: duration, Group: group}
	return m
}

func (s *state) emitError(text string) {
	ev := abi.Event{Tag: abi.EventTagError}
	ev.Error.Text = bs(out.Toast(text))
	s.out.Emit(&ev)
}

func (s *state) emitLink(tag abi.LinkStatusTag) {
	ev := abi.Event{Tag: abi.EventTagLink}
	ev.Link.Status.Tag = tag
	s.out.Emit(&ev)
}

// storage is the demo's storage figures, nothing once its cache was
// cleared.
func (s *state) storage() abi.StorageStats {
	if s.cacheCleared {
		return abi.StorageStats{}
	}
	return abi.StorageStats{
		Images: 128, ImageBytes: 48_300_000,
		Videos: 12, VideoBytes: 211_000_000,
		Stickers: 64, StickerBytes: 2_900_000,
		Other: 9, OtherBytes: 15_400_000,
	}
}

func joinMedia(name string) string { return filepath.Join(MediaDir(), name) }
