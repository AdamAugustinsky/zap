package model

import (
	"bytes"

	"zapfast/abi"
)

// endSession forgets everything the unlinked number left in the store, as
// upstream's LoggedOut does (zf:src/app.rs:4128-4174): its chats,
// conversations, contacts, avatars, stars, pins, chat pins, privacy, read
// receipts, polls and interactive replies in flight, and drafts; and, which
// upstream keeps elsewhere or rebuilds, its labels, presence, typing,
// downloads, group saves, sync progress, pin limit, storage figures and
// answers. Me stays, as upstream's me does, until the next link replaces
// it. A number linked next into the same account starts empty.
//
// Every generation moves on rather than restarting, and the rows made after
// it start above every generation the old rows reached: a screen's cache
// keyed by a ref and a generation never matches a row of the old session.
func (s *Store) endSession() {
	s.Chats.reset()
	s.Contacts.reset()
	s.Avatars.reset()
	s.phones = nil
	for i := range s.convs {
		v := &s.convs[i]
		*v = Conversation{gen: v.gen + 1, orderGen: v.orderGen + 1}
	}
	s.open, s.viewer = 0, 0
	s.typing = [maxTyping]typingRec{}
	s.tgen++
	s.labels.reset()
	s.drafts.reset()
	s.presence.reset()
	s.marks.reset()
	s.files.m.reset()
	s.saving.reset()
	s.sending.reset()
	s.voting.reset()
	for i := range s.answers {
		if abi.EventTag(i) != abi.EventTagLink {
			s.answers[i].Clear()
		}
	}
	s.forgotten.Reset()
	s.droppedBy = [maxDroppedTag]uint32{}
	s.syncing, s.hasSyncPct, s.syncPct = false, false, 0
	s.syncGen++
	s.pinLimit, s.receiptsOff, s.pollCreating = DefaultPinLimit, false, false
	s.Privacy = Privacy{gen: s.Privacy.gen + 1}
	// The token keeps rising: an answer to the old session's question is
	// stale.
	s.Storage = Storage{Token: s.Storage.Token, gen: s.Storage.gen + 1}
}

// wipe empties the arena, overwriting the bytes it held.
func (a *arena) wipe() {
	clear(a.b)
	a.b, a.live = a.b[:0], 0
}

func (c *Chats) reset() {
	c.a.wipe()
	clear(c.spare)
	c.recs, c.labels, c.spareLabels, c.order = c.recs[:0], c.labels[:0], c.spareLabels[:0], c.order[:0]
	c.idx.reset(256)
	c.sorted, c.live, c.reloading, c.loaded = false, 0, false, false
	c.scratch = abi.Chat{}
	c.gen++
	c.orderGen++
	c.base = c.gen
}

func (c *Contacts) reset() {
	c.a.wipe()
	clear(c.spare)
	clear(c.tmp)
	c.recs = c.recs[:0]
	c.idx.reset(256)
	c.scratch = abi.Contact{}
	c.gen++
	c.base = c.gen
}

func (av *Avatars) reset() {
	av.a.wipe()
	av.recs = av.recs[:0]
	av.idx.reset(0)
	av.gen++
}

func (l *labelList) reset() {
	l.a.wipe()
	l.na.wipe()
	l.recs, l.nrecs = l.recs[:0], l.nrecs[:0]
	l.streaming = false
	l.gen++
}

func (m *marks) reset() {
	m.stars.reset()
	m.starBatch.reset()
	m.pinIDs.reset()
	m.pins.reset()
	clear(m.pinChat)
	clear(m.pinBuf)
	m.pinChat, m.pinBuf, m.pinStream = m.pinChat[:0], m.pinBuf[:0], false
	m.scratch = abi.Pinned{}
	m.gen++
}

// forgetChat drops everything of a removed chat, as upstream's forget_chat
// and leave_chat do (zf:src/app.rs:2235-2316, ChatRemoved at :3651): its
// row (wiped; a chat of the same JID later gets a new ref), its loaded
// messages, its draft, the people typing in it, its stars and pins, its
// downloads, its group save and the polls and replies in flight in it, its
// in-chat search, media and shared listings, and its messages among the
// search hits and the starred list.
func (s *Store) forgetChat(chat []byte) {
	if ref := s.Chats.Find(chat); ref != 0 {
		for i := range s.typing {
			if s.typing[i].chat == ref {
				s.typing[i] = typingRec{}
			}
		}
		s.tgen++
		s.Chats.remove(uint32(ref - 1))
	}
	if v := s.Conversation(chat); v != nil {
		clear(v.a.b)
		*v = Conversation{gen: v.gen + 1, orderGen: v.orderGen + 1}
	}
	s.marks.forgetChat(chat, true)
	s.drafts.del(chat)
	s.files.m.delPrefix(s.files.m.prefix(chat))
	s.saving.del(chat)
	s.sending.delPrefix(s.sending.prefix(chat))
	s.voting.delPrefix(s.voting.prefix(chat))
	for _, tag := range [...]abi.EventTag{abi.EventTagChatHits, abi.EventTagChatShared, abi.EventTagChatMedia} {
		if a := &s.answers[tag]; a.Has() && answerChat(a.Event(), chat) {
			a.Clear()
		}
	}
	s.answers[abi.EventTagSearchHits].dropChat(chat)
	s.answers[abi.EventTagStarredList].dropChat(chat)
}

func answerChat(e *abi.Event, chat []byte) bool {
	if e == nil {
		return false
	}
	switch e.Tag {
	case abi.EventTagChatHits:
		return bytes.Equal(e.ChatHits.Chat, chat)
	case abi.EventTagChatShared:
		return bytes.Equal(e.ChatShared.Chat, chat)
	case abi.EventTagChatMedia:
		return bytes.Equal(e.ChatMedia.Chat, chat)
	}
	return false
}

// dropChat takes the chat's messages out of a SearchHits or StarredList
// answer (upstream's search_hits.retain, zf:src/app.rs:2267), the whole one
// and one arriving, keeping the rest in their order.
func (a *Answer) dropChat(chat []byte) {
	if a.filter(&a.cur, chat) {
		a.evAt = -1
		a.gen++
	}
	a.filter(&a.next, chat)
}

// filter rewrites buf without the chat's messages and reports whether any
// went.
func (a *Answer) filter(buf *answerBuf, chat []byte) bool {
	var e abi.Event
	hit := false
	start := uint32(0)
	for _, end := range buf.ends {
		if e.Decode(buf.b[start:end]) == nil && hasChat(&e, chat) {
			hit = true
			break
		}
		start = end
	}
	if !hit {
		return false
	}
	var out answerBuf
	start = 0
	total := uint32(0)
	for _, end := range buf.ends {
		if e.Decode(buf.b[start:end]) != nil {
			start = end
			continue
		}
		start = end
		n := keepOthers(&e, chat)
		b, err := e.Encode(out.b)
		if err != nil {
			continue
		}
		total += uint32(n)
		out.b = b
		out.ends = append(out.ends, uint32(len(out.b)))
		out.items = append(out.items, total)
	}
	clear(buf.b)
	*buf = out
	return true
}

func hasChat(e *abi.Event, chat []byte) bool {
	switch e.Tag {
	case abi.EventTagSearchHits:
		for i := range e.SearchHits.Messages {
			if bytes.Equal(e.SearchHits.Messages[i].Chat, chat) {
				return true
			}
		}
	case abi.EventTagStarredList:
		for i := range e.StarredList.Starred {
			if bytes.Equal(e.StarredList.Starred[i].Message.Chat, chat) {
				return true
			}
		}
	}
	return false
}

// keepOthers drops the chat's messages from e's list and returns how many
// stay.
func keepOthers(e *abi.Event, chat []byte) int {
	switch e.Tag {
	case abi.EventTagSearchHits:
		ms := e.SearchHits.Messages[:0]
		for _, m := range e.SearchHits.Messages {
			if !bytes.Equal(m.Chat, chat) {
				ms = append(ms, m)
			}
		}
		e.SearchHits.Messages = ms
		return len(ms)
	case abi.EventTagStarredList:
		ss := e.StarredList.Starred[:0]
		for _, st := range e.StarredList.Starred {
			if !bytes.Equal(st.Message.Chat, chat) {
				ss = append(ss, st)
			}
		}
		e.StarredList.Starred = ss
		return len(ss)
	}
	return 0
}
