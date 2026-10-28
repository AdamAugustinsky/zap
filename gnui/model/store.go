package model

import (
	"bytes"
	"encoding/binary"
	"sync/atomic"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/format"
)

// TypingTimeout is how long a "typing…" lasts without a refresh
// (zf:src/app.rs:167).
const TypingTimeout = 12 * time.Second

// DefaultPinLimit is how many chats an account may pin until the core says
// otherwise (zf:src/backend/worker.rs:425).
const DefaultPinLimit = 3

// numTags bounds the event tags (abi.EventTag is a byte; the schema's
// highest is 77).
const numTags = 80

// maxTyping is how many people typing the store follows at once, over all
// chats.
const maxTyping = 32

type typingRec struct {
	chat   ChatRef
	n      uint8
	sender [128]byte
	at     time.Time
	seq    uint32 // rises with each start or refresh: the typers' order
}

// Store is one account's model. Its zero value is not ready: use New.
type Store struct {
	Link     Link
	Me       Me
	Chats    Chats
	Contacts Contacts
	Avatars  Avatars

	phones map[string][]byte // numbers as grouped, by digits (PersonName)

	// Privacy is the account's privacy settings (Settings → Privacy).
	Privacy Privacy
	// Storage is Settings → Storage's numbers.
	Storage Storage
	// CacheDir is the account's media cache folder (the state sets it): a
	// cleared cache forgets the downloaded files under it (every file, when
	// empty).
	CacheDir []byte

	convs        [MaxConversations]Conversation
	open, viewer ChatRef // at most two protected conversations per account
	tick         uint64
	typing       [maxTyping]typingRec
	tseq         uint32
	tgen         uint32

	labels    labelList
	drafts    kv // chat → text
	presence  kv // JID → presenceLen bytes
	marks     marks
	files     files
	saving    kv // chat → nothing: GroupSaving
	sending   kv // (chat, message) → nothing: InteractiveReplyState
	voting    kv // (chat, message) → nothing: a VotePoll in flight
	answers   [numTags]Answer
	forgotten IDList

	syncing, hasSyncPct bool
	syncPct             uint32
	syncGen             uint32
	pinLimit            int
	receiptsOff         bool
	pollCreating        bool

	gen uint64
	// droppedBy counts the dropped records by the tag of the event they
	// were (the tags that fit).
	droppedBy [maxDroppedTag]uint32
	fatal     []byte
	events    uint64

	// Now is the clock the store reads (typing timeouts); tests replace it.
	Now func() time.Time
}

var _ core.Handler = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	s := &Store{Now: time.Now, pinLimit: DefaultPinLimit}
	s.Chats.idx.reset(256)
	s.Contacts.idx.reset(256)
	return s
}

// Gen moves on every change to the store.
func (s *Store) Gen() uint64 { return s.gen }

// Events is how many events the store has applied.
func (s *Store) Events() uint64 { return s.events }

// Linked reports whether the account shows its chats rather than the login
// screen: connected, connecting or reconnecting, or with cached chats and
// not logged out (zf:src/account.rs:261).
func (s *Store) Linked() bool {
	switch s.Link.tag {
	case abi.LinkStatusTagConnected, abi.LinkStatusTagConnecting, abi.LinkStatusTagDisconnected:
		return true
	}
	return s.Chats.Len() > 0 && s.Link.tag != abi.LinkStatusTagLoggedOut
}

// maxDroppedTag bounds the event tags DroppedOf tells apart.
const maxDroppedTag = 96

// DroppedOf is how many records of the event with tag could not be sent
// (EventDropped's Kind: a chat of Chats, a message of Messages), for the
// list or the transcript to say so. It moves Gen.
func (s *Store) DroppedOf(tag abi.EventTag) uint64 {
	if int(tag) >= len(s.droppedBy) {
		return 0
	}
	return uint64(s.droppedBy[tag])
}

// Fatal is why the core stopped (a Fatal event), empty while it runs.
func (s *Store) Fatal() []byte { return s.fatal }

// Event applies one event (core.Handler). Its bytes are copied: they are
// valid only during the call.
func (s *Store) Event(e *abi.Event) {
	s.events++
	if int(e.Tag) < numTags && answered(e.Tag) {
		s.answers[e.Tag].apply(e)
	}
	switch e.Tag {
	case abi.EventTagLink:
		s.Link.set(&e.Link.Status)
		if e.Link.Status.Tag == abi.LinkStatusTagLoggedOut {
			s.endSession()
		}
	case abi.EventTagMe:
		s.Me.set(&e.Me)
	case abi.EventTagChats:
		s.Chats.chunk(&e.Chats)
	case abi.EventTagChatUpdated:
		s.Chats.upsert(&e.ChatUpdated.Chat)
	case abi.EventTagChatRemoved:
		s.forgetChat(e.ChatRemoved.Chat)
	case abi.EventTagChatCleared:
		if v := s.Conversation(e.ChatCleared.Chat); v != nil {
			through := e.ChatCleared.Through
			v.removeWhere(func(r *msgRec) bool { return r.ts <= through })
		}
		s.marks.forgetChat(e.ChatCleared.Chat, true)
	case abi.EventTagMessages:
		m := &e.Messages
		s.Open(m.Chat).messages(m)
	case abi.EventTagMessageUpdated:
		m := &e.MessageUpdated.Message
		if m.Content.Tag == abi.ContentTagRevoked {
			// A revoke for everyone arrives as an update: its star and its
			// pin go with it (zf:src/app.rs:3469).
			s.marks.forget(m.Chat, m.ID)
		}
		s.message(m, true)
	case abi.EventTagIncoming:
		s.message(&e.Incoming.Message, false)
	case abi.EventTagMessageDeleted:
		d := &e.MessageDeleted
		if v := s.Conversation(d.Chat); v != nil {
			if i := v.Find(d.ID); i >= 0 {
				v.removeWhere(func(r *msgRec) bool { return r == &v.recs[i] })
			}
		}
		s.marks.forget(d.Chat, d.ID)
	case abi.EventTagContacts:
		for i := range e.Contacts.Contacts {
			s.Contacts.upsert(&e.Contacts.Contacts[i])
		}
	case abi.EventTagTyping:
		s.setTyping(&e.Typing)
	case abi.EventTagPresence:
		s.setPresence(&e.Presence)
	case abi.EventTagDrafts:
		// Text typed in this session wins over the stored copy
		// (zf:src/app.rs:3127).
		for i := range e.Drafts.Drafts {
			d := &e.Drafts.Drafts[i]
			if !s.drafts.has(d.Chat) && len(d.Text) > 0 {
				s.drafts.set(d.Chat, d.Text)
			}
		}
	case abi.EventTagSyncing:
		s.syncing = e.Syncing.Syncing
		if !s.syncing {
			s.hasSyncPct = false
		}
		s.syncGen++
	case abi.EventTagSyncProgress:
		s.syncPct, s.hasSyncPct = e.SyncProgress.Percent, true
		s.syncGen++
	case abi.EventTagPinLimit:
		s.pinLimit = int(min(e.PinLimit.Limit, 1<<20))
	case abi.EventTagLabels:
		s.labels.chunk(&e.Labels)
	case abi.EventTagStars:
		s.marks.starChunk(&e.Stars)
	case abi.EventTagStarChanged:
		x := &e.StarChanged
		s.marks.setStar(x.Chat, x.Message, x.Starred)
	case abi.EventTagPins:
		s.marks.pinChunk(&e.Pins)
	case abi.EventTagPinChanged:
		x := &e.PinChanged
		s.marks.setPinned(x.Chat, x.Message, x.Pinned)
	case abi.EventTagMedia:
		x := &e.Media
		part := PartMain
		if x.HasCard {
			part = int(min(x.Card, 1<<20))
		}
		s.mediaResult(x.Chat, x.Message, part, &x.Result)
	case abi.EventTagMotion:
		x := &e.Motion
		s.mediaResult(x.Chat, x.Message, PartMotion, &x.Result)
	case abi.EventTagOlderFetched:
		if v := s.Conversation(e.OlderFetched.Chat); v != nil {
			v.olderFetched(&e.OlderFetched, s.Now().UnixNano())
		}
	case abi.EventTagReceiptsPrivacy, abi.EventTagAccountPrivacy, abi.EventTagAccountPrivacySaved, abi.EventTagAccountPrivacyFailed:
		s.privacyEvent(e)
	case abi.EventTagStorageStats:
		s.storageEvent(&e.StorageStats)
	case abi.EventTagGroupSaving:
		if x := &e.GroupSaving; x.Saving {
			s.saving.set(x.Chat, nil)
		} else {
			s.saving.del(x.Chat)
		}
	case abi.EventTagInteractiveReplyState:
		x := &e.InteractiveReplyState
		if k := s.sending.pair(x.Chat, x.Message); x.Pending {
			s.sending.set(k, nil)
		} else {
			s.sending.del(k)
		}
	case abi.EventTagPollCreated:
		s.pollCreating = false
	case abi.EventTagPollVoted:
		s.voting.del(s.voting.pair(e.PollVoted.Chat, e.PollVoted.Message))
	case abi.EventTagAvatar:
		s.Avatars.set(&e.Avatar)
	case abi.EventTagEventDropped:
		if k := int(e.EventDropped.Kind); k < len(s.droppedBy) {
			s.droppedBy[k]++
		}
	case abi.EventTagFatal:
		s.fatal = append(s.fatal[:0], e.Fatal.Origin...)
	case abi.EventTagPickRequest, abi.EventTagOpenPath, abi.EventTagAccountRemoved:
		// The state's: answered, opened, or the account closed.
	}
	s.gen++
}

// Answer is the core's latest answer of the tag (see Answer for which tags
// keep one). It is never nil.
func (s *Store) Answer(tag abi.EventTag) *Answer {
	if int(tag) >= numTags {
		return &s.answers[0]
	}
	return &s.answers[tag]
}

// message applies an updated or incoming message to its conversation, if
// that is loaded (the chat row itself comes as ChatUpdated).
func (s *Store) message(m *abi.Message, updated bool) {
	v := s.Conversation(m.Chat)
	if v == nil {
		return
	}
	if i := v.Find(m.ID); updated && i >= 0 && v.recs[i].carries() {
		if old := &v.edit; old.Decode(v.a.at(v.recs[i].raw)) == nil {
			carryMedia(m, old)
		}
	}
	if v.upsert(m) {
		v.sort()
	}
	v.edit.Release()
	s.tick++
	v.used = s.tick
}

// Conversation is the chat's loaded messages, or nil when they are not
// loaded.
func (s *Store) Conversation(chat []byte) *Conversation {
	for i := range s.convs {
		if v := &s.convs[i]; v.inUse && bytes.Equal(v.Chat(), chat) {
			return v
		}
	}
	return nil
}

// Open returns the chat's conversation, making it (empty) when it is not
// loaded, which drops the least recently used one if all are in use. The
// state calls it when a chat opens, before LoadChat.
func (s *Store) Open(chat []byte) *Conversation {
	s.tick++
	v := s.Conversation(chat)
	if v == nil {
		for i := range s.convs {
			c := &s.convs[i]
			if s.pinned(c) {
				continue
			}
			if !c.inUse {
				v = c
				break
			}
			if v == nil || c.used < v.used {
				v = c
			}
		}
		v.reset(chat)
		s.gen++
	}
	v.used = s.tick
	return v
}

// PinConversations protects the account's open transcript and media viewer.
// Zero releases a pin; parked open chats may stay pinned until closed.
func (s *Store) PinConversations(open, viewer ChatRef) { s.open, s.viewer = open, viewer }

func (s *Store) pinned(v *Conversation) bool {
	ref := s.Chats.Find(v.Chat())
	return ref != 0 && (ref == s.open || ref == s.viewer)
}

// DisplayName is how the interface names a JID (PersonName without a
// hint).
func (s *Store) DisplayName(jid []byte) []byte { return s.PersonName(jid, nil) }

// PersonName is how the interface names a person (zf:src/app.rs:2547):
// the address-book name first, as WhatsApp does; else the name they chose
// (the contact's profile name, or hint, the profile name a message
// carried), after a "~"; else the chat's name unless it is only a number;
// else the phone number as it is grouped; else "Unknown". It allocates only
// to put a "~" before hint: AppendPersonName names into storage of the
// caller's.
func (s *Store) PersonName(jid, hint []byte) []byte {
	if n := s.personName(jid, hint); n != nil {
		return n
	}
	return append([]byte{'~'}, hint...)
}

// AppendPersonName appends PersonName(jid, hint) to dst.
func (s *Store) AppendPersonName(dst, jid, hint []byte) []byte {
	if n := s.personName(jid, hint); n != nil {
		return append(dst, n...)
	}
	return append(append(dst, '~'), hint...)
}

// personName is PersonName's name, nil when it is hint after a "~".
func (s *Store) personName(jid, hint []byte) []byte {
	if i := s.Contacts.Find(jid); i >= 0 {
		r := &s.Contacts.recs[i]
		if len(bytes.TrimSpace(s.Contacts.a.at(r.full))) > 0 {
			return s.Contacts.a.at(r.full)
		}
		if t := s.Contacts.a.at(r.tilde); len(t) > 0 {
			return t
		}
	}
	if len(hint) > 0 {
		return nil
	}
	phone := Phone(jid)
	if ref := s.Chats.Find(jid); ref != 0 {
		if n := s.Chats.At(ref).Name(); len(n) > 0 && !onlyNumber(n, phone != nil) {
			return n
		}
	}
	if phone == nil {
		return unknownName
	}
	if f, ok := s.phones[string(phone)]; ok {
		return f
	}
	if s.phones == nil {
		s.phones = map[string][]byte{}
	}
	f := []byte(format.Phone(string(phone)))
	s.phones[string(phone)] = f
	return f
}

var unknownName = []byte("Unknown")

// onlyNumber reports whether name is digits alone, with a phone number's
// punctuation too when the id carries the number: no name at all.
func onlyNumber(name []byte, phone bool) bool {
	for _, b := range name {
		switch {
		case b >= '0' && b <= '9':
		case phone && (b == '+' || b == ' ' || b == '(' || b == ')' || b == '-'):
		default:
			return false
		}
	}
	return true
}

func (s *Store) setTyping(t *abi.EventTyping) {
	ref := s.Chats.Find(t.Chat)
	if ref == 0 {
		return
	}
	now := s.Now()
	free := -1
	for i := range s.typing {
		r := &s.typing[i]
		if r.chat == ref && bytes.Equal(r.sender[:r.n], t.Sender) {
			r.chat = 0
		}
		if r.chat == 0 || now.Sub(r.at) >= TypingTimeout {
			r.chat = 0
			if free < 0 {
				free = i
			}
		}
	}
	if t.Composing && free >= 0 {
		r := &s.typing[free]
		s.tseq++
		r.chat, r.n, r.at, r.seq = ref, uint8(copy(r.sender[:], t.Sender)), now, s.tseq
	}
	s.tgen++
	if c, ok := s.Chats.Get(ref); ok {
		c.r().gen++
		s.Chats.gen++
	}
}

// Typing reports who is typing in the chat: the first of them (Typers'
// order), and how many. Entries last TypingTimeout; a screen showing one
// requests a frame for when it lapses (the typing indicator animates
// anyway).
func (s *Store) Typing(ref ChatRef) (sender []byte, n int) {
	now := s.Now()
	first := -1
	for i := range s.typing {
		r := &s.typing[i]
		if r.chat == ref && ref != 0 && now.Sub(r.at) < TypingTimeout {
			if first < 0 || r.seq < s.typing[first].seq {
				first = i
			}
			n++
		}
	}
	if first >= 0 {
		sender = s.typing[first].sender[:s.typing[first].n]
	}
	return sender, n
}

// Typers appends everyone typing in the chat to dst and returns it, in
// upstream's order (zf:src/app.rs typing_in): a refresh moves its sender to
// the end. The senders (JIDs) are the store's, valid until the next event:
// ask Contacts or DisplayName for the names. With dst's room reused it
// allocates nothing.
func (s *Store) Typers(ref ChatRef, dst [][]byte) [][]byte {
	if ref == 0 {
		return dst
	}
	now := s.Now()
	last := uint32(0)
	for {
		next := -1
		for i := range s.typing {
			r := &s.typing[i]
			if r.chat == ref && r.seq > last && now.Sub(r.at) < TypingTimeout && (next < 0 || r.seq < s.typing[next].seq) {
				next = i
			}
		}
		if next < 0 {
			return dst
		}
		r := &s.typing[next]
		dst = append(dst, r.sender[:r.n])
		last = r.seq
	}
}

// TypingGen moves whenever someone starts or stops typing.
func (s *Store) TypingGen() uint32 { return s.tgen }

// Avatars are the profile pictures' files, by JID, as the core reports them
// (FetchAvatar asks for one).
type Avatars struct {
	recs []avatarRec
	a    arena
	idx  index
	gen  uint32
}

type avatarRec struct {
	id, thumb, full    Str
	thumbID, fullID    uint64 // the pictures' identities (avatarIDs)
	hasThumb, hasFull  bool
	knownThumb, asked  bool
	knownFull, askFull bool
}

func (av *Avatars) key(row uint32) []byte { return av.a.at(av.recs[row].id) }

func (av *Avatars) row(id []byte) *avatarRec {
	i := av.idx.find(id, av)
	if i < 0 {
		av.recs = append(av.recs, avatarRec{id: av.a.put(id)})
		i = len(av.recs) - 1
		av.idx.add(id, uint32(i))
	}
	return &av.recs[i]
}

func (av *Avatars) set(e *abi.EventAvatar) {
	r := av.row(e.ID)
	p := Str{}
	if e.HasPath {
		p = av.a.put(e.Path)
	}
	id := nextAvatarID()
	if e.Full {
		r.full, r.hasFull, r.knownFull, r.fullID = p, e.HasPath, true, id
	} else {
		r.thumb, r.hasThumb, r.knownThumb, r.thumbID = p, e.HasPath, true, id
	}
	av.gen++
}

// avatarIDs numbers every picture the cores report, process-wide: the core
// writes a JID's picture to the same file each time (zf:src/paths.rs:350),
// so the path does not tell a new picture from the last, nor one session's
// from another's.
var avatarIDs atomic.Uint64

// nextAvatarID is a new picture's identity: a counter, mixed so that it is
// spread over 64 bits as an image cache key's hash (splitmix64). Never 0.
func nextAvatarID() uint64 {
	z := avatarIDs.Add(1) * 0x9e3779b97f4a7c15
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	if z ^= z >> 31; z == 0 {
		z = 1
	}
	return z
}

// Thumb is the small picture's file for jid: ok when there is one; known
// once the core has answered (a known jid without a path has no picture).
func (av *Avatars) Thumb(jid []byte) (path []byte, ok, known bool) {
	i := av.idx.find(jid, av)
	if i < 0 {
		return nil, false, false
	}
	r := &av.recs[i]
	return av.a.at(r.thumb), r.hasThumb, r.knownThumb
}

// Full is the large picture's file, as Thumb.
func (av *Avatars) Full(jid []byte) (path []byte, ok, known bool) {
	i := av.idx.find(jid, av)
	if i < 0 {
		return nil, false, false
	}
	r := &av.recs[i]
	return av.a.at(r.full), r.hasFull, r.knownFull
}

// Picture is jid's picture, the large one with full: its file, and its
// identity, a number no other picture of any account or session has had,
// new with every Avatar event even when the file's path is the same. Use
// the identity as the image cache key's hash (upstream forgets the image at
// the path on every Avatar event, zf:src/app.rs:3559). ok when there is
// one; nil and 0 without.
func (av *Avatars) Picture(jid []byte, full bool) (path []byte, id uint64, ok bool) {
	i := av.idx.find(jid, av)
	if i < 0 {
		return nil, 0, false
	}
	r := &av.recs[i]
	if full {
		if !r.hasFull {
			return nil, 0, false
		}
		return av.a.at(r.full), r.fullID, true
	}
	if !r.hasThumb {
		return nil, 0, false
	}
	return av.a.at(r.thumb), r.thumbID, true
}

// Ask reports whether the picture of jid should be asked for (FetchAvatar):
// true once per JID and size, until the store is reset.
func (av *Avatars) Ask(jid []byte, full bool) bool {
	r := av.row(jid)
	if full {
		if r.askFull || r.knownFull {
			return false
		}
		r.askFull = true
		return true
	}
	if r.asked || r.knownThumb {
		return false
	}
	r.asked = true
	return true
}

// Gen moves whenever a picture arrives.
func (av *Avatars) Gen() uint32 { return av.gen }

// Draft is the chat's unsent text: stored by an earlier session (Drafts)
// or kept by SetDraft. Empty for none.
func (s *Store) Draft(chat []byte) []byte {
	d, _ := s.drafts.get(chat)
	return d
}

// SetDraft keeps the chat's unsent text (empty forgets it). The composer
// calls it as the text changes or when the chat closes; Account.SaveDraft
// also stores it in the archive (SaveDraft).
func (s *Store) SetDraft(chat, text []byte) {
	if len(text) == 0 {
		s.drafts.del(chat)
	} else {
		s.drafts.set(chat, text)
	}
	s.gen++
}

// DraftGen moves whenever a draft changes.
func (s *Store) DraftGen() uint32 { return s.drafts.gen }

// presenceLen is a presence value's size: online, has-last-seen, last seen.
const presenceLen = 10

func (s *Store) setPresence(p *abi.EventPresence) {
	var b [presenceLen]byte
	if p.Online {
		b[0] = 1
	}
	if p.HasLastSeen {
		b[1] = 1
		binary.LittleEndian.PutUint64(b[2:], uint64(p.LastSeen))
	}
	s.presence.set(p.ID, b[:])
}

// Presence is whether jid is online and when it was last seen (Unix
// seconds, 0 when not known); ok is false when no presence of jid came.
func (s *Store) Presence(jid []byte) (online bool, lastSeen int64, ok bool) {
	b, ok := s.presence.get(jid)
	if !ok || len(b) != presenceLen {
		return false, 0, false
	}
	if b[1] != 0 {
		lastSeen = int64(binary.LittleEndian.Uint64(b[2:]))
	}
	return b[0] != 0, lastSeen, true
}

// PresenceGen moves whenever a presence changes.
func (s *Store) PresenceGen() uint32 { return s.presence.gen }

// Syncing reports whether history is syncing, and how far (percent; ok is
// false while the core has not said).
func (s *Store) Syncing() (on bool, percent int, ok bool) {
	return s.syncing, int(s.syncPct), s.syncing && s.hasSyncPct
}

// SyncGen moves whenever syncing starts, progresses or ends.
func (s *Store) SyncGen() uint32 { return s.syncGen }

// PinLimit is how many chats the account may pin (WhatsApp Plus raises it).
func (s *Store) PinLimit() int { return s.pinLimit }

// GroupSaving reports whether the group's new name or picture is being
// saved (the info panel's spinner).
func (s *Store) GroupSaving(chat []byte) bool { return s.saving.has(chat) }

// InteractiveSending reports whether a reply to the interactive message is
// on its way (its buttons wait).
func (s *Store) InteractiveSending(chat, msg []byte) bool {
	return s.sending.has(s.sending.pair(chat, msg))
}

// PollVoting reports whether a vote on the poll is in flight.
func (s *Store) PollVoting(chat, msg []byte) bool { return s.voting.has(s.voting.pair(chat, msg)) }

// MarkPollVoting records a VotePoll sent; the core's PollVoted ends it.
func (s *Store) MarkPollVoting(chat, msg []byte) {
	s.voting.set(s.voting.pair(chat, msg), nil)
	s.gen++
}

// PollCreating reports whether a CreatePoll is in flight.
func (s *Store) PollCreating() bool { return s.pollCreating }

// MarkPollCreating records a CreatePoll sent; the core's PollCreated ends
// it.
func (s *Store) MarkPollCreating() { s.pollCreating = true; s.gen++ }
