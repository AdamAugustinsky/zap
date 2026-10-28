package model

import (
	"bytes"
	"math"
	"slices"
	"unsafe"

	"zapfast/abi"
)

// MaxConversations is how many chats' messages a store keeps loaded at
// once. Opening another one drops the least recently used (never the one
// marked open), whose history loads again from the archive when it opens.
const MaxConversations = 24

// msgFlags are a message row's booleans.
type msgFlags uint16

const (
	msgFromMe msgFlags = 1 << iota
	msgEdited
	msgForwarded
	msgHasQuote
	msgHasThumb
	msgHasMedia
	msgHasSeconds
	msgVoice
	msgGif
	msgVideoNote
	msgAnimated
	msgMineReacted
)

// msgRec is one message row. Every Str points into raw, the row's encoded
// abi.Message.
type msgRec struct {
	raw                                Str
	id, sender, senderName, text       Str
	quoteID, quoteSender, quoteSummary Str
	mediaPath, mime, fileName          Str
	ts, order                          int64
	size                               uint64
	thumb                              abi.BlobRef
	seconds, w, h                      uint32
	reacts                             uint32 // first of the row's reactions in Conversation.reacts
	nReacts                            uint16
	kind                               abi.ContentTag
	status                             abi.Delivery
	media                              abi.MediaStateTag
	flags                              msgFlags
	gen                                uint32
}

// reactRec is one emoji of a message's reactions summary: how many reacted
// with it.
type reactRec struct {
	emoji Str
	count uint16
	mine  bool
}

// Conversation is the loaded messages of one chat, in the transcript's
// order: by time, then by the history's own order (zf:src/app.rs:296).
type Conversation struct {
	chat     [128]byte // the chat's JID (at most 128 bytes, the schema's bound)
	chatN    uint8
	inUse    bool
	used     uint64 // when it was last opened or applied, for eviction
	recs     []msgRec
	a        arena
	spare    []byte
	idx      index
	reacts   []reactRec
	rspare   []reactRec
	gen      uint32
	orderGen uint32
	complete bool // the archive has no older messages
	scratch  abi.Message
	edit     abi.Message

	// Paging (zf:src/app.rs:174-193): an archive page asked for, the
	// phone asked for older history, and how it answered.
	loadingOlder   bool
	fetchingPhone  bool
	phoneExplicit  bool
	phoneExhausted bool
	phoneDelivered bool
	phoneMisses    uint8
	phoneAnswered  int64 // Unix nanoseconds, 0: never
	// The Messages batch arriving: its number, and whether the conversation
	// was empty when it began.
	batch     uint32
	streaming bool
	wasEmpty  bool
}

func (v *Conversation) key(row uint32) []byte { return v.a.at(v.recs[row].id) }

// Chat is the JID of the conversation's chat.
func (v *Conversation) Chat() []byte { return v.chat[:v.chatN] }

// Len is the number of loaded messages.
func (v *Conversation) Len() int { return len(v.recs) }

// Gen moves whenever a message is added, changed or removed.
func (v *Conversation) Gen() uint32 { return v.gen }

// OrderGen moves when row membership or order changes, including slot reuse.
// Content and paging changes still advance Gen independently.
func (v *Conversation) OrderGen() uint32 { return v.orderGen }

// Complete reports whether the archive holds no messages older than the
// first loaded one (the transcript stops asking for more).
func (v *Conversation) Complete() bool { return v.complete }

// At is the view of message i (0 is the oldest loaded).
func (v *Conversation) At(i int) Message { return Message{v, uint32(i)} }

// Find is the index of the message with the id, or -1.
func (v *Conversation) Find(id []byte) int { return v.idx.find(id, v) }

// Decode decodes message i's whole record (content, quote, mentions,
// reactions …) into dst, whose byte fields then point into the store: valid
// until the next apply. dst's slices are reused.
func (v *Conversation) Decode(i int, dst *abi.Message) error {
	return dst.Decode(v.a.at(v.recs[i].raw))
}

func (v *Conversation) reset(chat []byte) {
	// Store.Open calls reset only on an unpinned eviction/reuse candidate.
	// Keep small slots warm; release large slabs and decoder views together.
	if v.retainedBytes() > 1<<20 {
		*v = Conversation{gen: v.gen, orderGen: v.orderGen}
	}
	v.chatN = uint8(copy(v.chat[:], chat))
	v.inUse = true
	v.recs = v.recs[:0]
	v.a.b, v.a.live = v.a.b[:0], 0
	v.reacts = v.reacts[:0]
	v.idx.reset(0)
	v.complete = false
	v.loadingOlder, v.fetchingPhone, v.phoneExplicit, v.phoneExhausted, v.phoneDelivered = false, false, false, false, false
	v.phoneMisses, v.phoneAnswered = 0, 0
	v.streaming = false
	v.gen++
	v.orderGen++
}

func (v *Conversation) retainedBytes() uintptr {
	return uintptr(cap(v.a.b)+cap(v.spare)) + uintptr(cap(v.recs))*unsafe.Sizeof(msgRec{}) +
		uintptr(cap(v.idx.slots))*unsafe.Sizeof(slot{}) + uintptr(cap(v.reacts)+cap(v.rspare))*unsafe.Sizeof(reactRec{})
}

// LoadingOlder reports whether an older archive page was asked for and has
// not come (LoadChat with Before).
func (v *Conversation) LoadingOlder() bool { return v.loadingOlder }

// SetLoadingOlder marks an archive page asked for (Account.LoadOlder does).
func (v *Conversation) SetLoadingOlder(on bool) { v.loadingOlder = on; v.gen++ }

// FetchingPhone reports whether the phone was asked for older history
// (FetchOlder) and has not answered; explicit when the reader asked by
// scrolling to the top.
func (v *Conversation) FetchingPhone() (on, explicit bool) { return v.fetchingPhone, v.phoneExplicit }

// SetFetchingPhone marks a FetchOlder sent (Account.FetchOlder does).
func (v *Conversation) SetFetchingPhone(on, explicit bool) {
	v.fetchingPhone, v.phoneExplicit = on, explicit
	v.gen++
}

// PhoneExhausted reports whether the phone has no older history to give.
func (v *Conversation) PhoneExhausted() bool { return v.phoneExhausted }

// PhoneRefused ends a FetchOlder the core refused (the demo core cannot ask
// a phone): as if the phone had nothing older (Account.FetchOlder does).
func (v *Conversation) PhoneRefused() {
	v.fetchingPhone, v.phoneExplicit, v.phoneExhausted = false, false, true
	v.gen++
}

// PhoneMisses is how many phone answers in a row brought nothing (the
// back-off's exponent, at most 7).
func (v *Conversation) PhoneMisses() int { return int(v.phoneMisses) }

// PhoneAnswered is when the phone last answered, in Unix nanoseconds (0:
// never).
func (v *Conversation) PhoneAnswered() int64 { return v.phoneAnswered }

// messages applies one chunk of a Messages event (zf:src/app.rs:3257-3283).
func (v *Conversation) messages(m *abi.EventMessages) {
	if !v.streaming || m.Batch != v.batch {
		v.wasEmpty = len(v.recs) == 0
	}
	v.batch, v.streaming = m.Batch, !m.Last
	if m.Older && len(m.Messages) > 0 {
		v.phoneDelivered = true
	}
	added := false
	for i := range m.Messages {
		added = v.upsert(&m.Messages[i]) || added
	}
	if added {
		v.sort()
	}
	if m.Last {
		if m.Older {
			v.loadingOlder = false
			v.complete = m.Complete
		} else if v.wasEmpty {
			v.complete = m.Complete
		}
	}
	v.gen++
}

// olderFetched applies the phone's answer to FetchOlder
// (zf:src/app.rs:3719-3748).
func (v *Conversation) olderFetched(e *abi.EventOlderFetched, now int64) {
	v.gen++
	if e.Silent {
		// A background page fills the archive: the chat has older
		// messages to page in again, but no request of the reader's ended.
		v.complete = false
		return
	}
	v.fetchingPhone, v.phoneExplicit = false, false
	v.phoneExhausted = !e.More
	v.phoneAnswered = now
	if v.phoneDelivered {
		v.phoneMisses = 0
	} else {
		v.phoneMisses = min(v.phoneMisses+1, 7)
	}
	v.phoneDelivered = false
	v.complete = false
}

// upsert applies one message; it reports whether the order may have changed
// (a new row).
func (v *Conversation) upsert(m *abi.Message) bool {
	if len(m.ID) == 0 {
		return false
	}
	row := v.idx.find(m.ID, v)
	v.a.reserve(4 << 10) // a message's encoding is rarely more; a longer one grows the arena itself
	start := len(v.a.b)
	var err error
	v.a.b, err = m.Encode(v.a.b)
	if err != nil {
		v.a.b = v.a.b[:start]
		return false
	}
	raw := Str{uint32(start), uint32(len(v.a.b) - start)}
	if row >= 0 && bytes.Equal(v.a.at(raw), v.a.at(v.recs[row].raw)) {
		// The message as it is already (a chat opened again gets its
		// page again): nothing changes, and the arena does not grow.
		v.a.b = v.a.b[:start]
		return false
	}
	v.a.live += int(raw.Len)
	d := &v.scratch
	// The row keeps offsets, not the decoded views: dropping them lets an
	// arena that grows or compacts away go.
	defer d.Release()
	if d.Decode(v.a.at(raw)) != nil {
		return false
	}
	r := msgRec{
		raw: raw, id: v.a.sub(raw, d.ID), sender: v.a.sub(raw, d.Sender), senderName: v.a.sub(raw, d.SenderName),
		ts: d.Timestamp, order: math.MaxInt64, kind: d.Content.Tag, status: d.Status,
	}
	if d.HasHistoryOrder {
		r.order = d.HistoryOrder
	}
	if d.FromMe {
		r.flags |= msgFromMe
	}
	if d.Edited {
		r.flags |= msgEdited
	}
	if d.Forwarded {
		r.flags |= msgForwarded
	}
	if d.HasThumbnail {
		r.flags |= msgHasThumb
		r.thumb = d.Thumbnail
	}
	if d.HasQuoted {
		r.flags |= msgHasQuote
		r.quoteID, r.quoteSummary = v.a.sub(raw, d.Quoted.ID), v.a.sub(raw, d.Quoted.Summary)
		r.quoteSender = v.a.sub(raw, d.Quoted.SenderName)
		if r.quoteSender.Empty() {
			r.quoteSender = v.a.sub(raw, d.Quoted.Sender)
		}
	}
	v.content(&r, raw, &d.Content)
	r.reacts = uint32(len(v.reacts))
	for i := range d.Reactions {
		re := &d.Reactions[i]
		if re.FromMe {
			r.flags |= msgMineReacted
		}
		found := false
		for j := r.reacts; j < uint32(len(v.reacts)); j++ {
			if bytes.Equal(v.a.at(v.reacts[j].emoji), re.Emoji) {
				v.reacts[j].count++
				v.reacts[j].mine = v.reacts[j].mine || re.FromMe
				found = true
				break
			}
		}
		if !found && len(re.Emoji) > 0 {
			v.reacts = append(v.reacts, reactRec{emoji: v.a.sub(raw, re.Emoji), count: 1, mine: re.FromMe})
		}
	}
	r.nReacts = uint16(uint32(len(v.reacts)) - r.reacts)
	v.gen++
	if row >= 0 {
		old := &v.recs[row]
		v.a.drop(old.raw)
		r.gen = old.gen + 1
		moved := old.ts != r.ts || old.order != r.order
		*old = r
		if v.a.wasteful() {
			v.compact()
		}
		return moved
	}
	if len(v.recs) == cap(v.recs) { // doubled, as the arena (arena.reserve)
		v.recs = slices.Grow(v.recs, max(len(v.recs), 64))
	}
	v.recs = append(v.recs, r)
	v.orderGen++
	v.idx.add(m.ID, uint32(len(v.recs)-1))
	if v.a.wasteful() {
		v.compact()
	}
	return true
}

func (v *Conversation) content(r *msgRec, raw Str, c *abi.Content) {
	media := func(m *abi.Media) {
		r.flags |= msgHasMedia
		r.mediaPath, r.mime = v.a.sub(raw, m.Path), v.a.sub(raw, m.Mime)
		r.size, r.media = m.Size, m.State.Tag
		r.w, r.h = m.Width, m.Height
	}
	seconds := func(s uint32, has bool) {
		if has {
			r.seconds = s
			r.flags |= msgHasSeconds
		}
	}
	switch c.Tag {
	case abi.ContentTagText:
		r.text = v.a.sub(raw, c.Text.Text)
	case abi.ContentTagInteractive:
		r.text = v.a.sub(raw, c.Interactive.Text)
	case abi.ContentTagImage:
		r.text = v.a.sub(raw, c.Image.Caption)
		media(&c.Image.Media)
	case abi.ContentTagVideo:
		r.text = v.a.sub(raw, c.Video.Caption)
		media(&c.Video.Media)
		seconds(c.Video.Seconds, c.Video.HasSeconds)
		if c.Video.Gif {
			r.flags |= msgGif
		}
		if c.Video.Note {
			r.flags |= msgVideoNote
		}
	case abi.ContentTagAudio:
		media(&c.Audio.Media)
		seconds(c.Audio.Seconds, c.Audio.HasSeconds)
		if c.Audio.VoiceNote {
			r.flags |= msgVoice
		}
	case abi.ContentTagDocument:
		r.text = v.a.sub(raw, c.Document.Caption)
		r.fileName = v.a.sub(raw, c.Document.FileName)
		media(&c.Document.Media)
	case abi.ContentTagSticker:
		media(&c.Sticker.Media)
		if c.Sticker.Animated {
			r.flags |= msgAnimated
		}
	case abi.ContentTagStickerPack:
		r.text = v.a.sub(raw, c.StickerPack.Caption)
	case abi.ContentTagLocation:
		r.text = v.a.sub(raw, c.Location.Name)
	case abi.ContentTagContact:
		r.text = v.a.sub(raw, c.Contact.DisplayName)
	case abi.ContentTagPoll:
		r.text = v.a.sub(raw, c.Poll.Question)
	case abi.ContentTagUnsupported:
		r.text = v.a.sub(raw, c.Unsupported.What)
	}
}

// sort puts the rows in the transcript's order and rebuilds the index.
func (v *Conversation) sort() {
	if slices.IsSortedFunc(v.recs, compareMsg) {
		return
	}
	slices.SortStableFunc(v.recs, compareMsg)
	v.reindex()
	v.orderGen++
}

func compareMsg(a, b msgRec) int {
	switch {
	case a.ts != b.ts:
		if a.ts < b.ts {
			return -1
		}
		return 1
	case a.order < b.order:
		return -1
	case a.order > b.order:
		return 1
	}
	return 0
}

func (v *Conversation) reindex() {
	v.idx.reset(len(v.recs))
	for i := range v.recs {
		v.idx.add(v.a.at(v.recs[i].id), uint32(i))
	}
}

// remove deletes the rows for which drop is true.
func (v *Conversation) removeWhere(drop func(r *msgRec) bool) bool {
	n := 0
	for i := range v.recs {
		if drop(&v.recs[i]) {
			v.a.drop(v.recs[i].raw)
			continue
		}
		v.recs[n] = v.recs[i]
		n++
	}
	if n == len(v.recs) {
		return false
	}
	v.recs = v.recs[:n]
	v.reindex()
	v.gen++
	v.orderGen++
	return true
}

func (v *Conversation) compact() {
	old := v.a.b
	v.a.b, v.spare = v.spare[:0], old
	v.a.live = 0
	reacts := v.rspare[:0]
	for i := range v.recs {
		r := &v.recs[i]
		ob := r.raw
		nb := v.a.put(old[ob.Off : ob.Off+ob.Len])
		r.raw = nb
		for _, s := range [...]*Str{&r.id, &r.sender, &r.senderName, &r.text, &r.quoteID, &r.quoteSender, &r.quoteSummary, &r.mediaPath, &r.mime, &r.fileName} {
			*s = rebase(*s, ob, nb)
		}
		first := len(reacts)
		for j := uint32(0); j < uint32(r.nReacts); j++ {
			re := v.reacts[r.reacts+j]
			re.emoji = rebase(re.emoji, ob, nb)
			reacts = append(reacts, re)
		}
		r.reacts = uint32(first)
	}
	v.reacts, v.rspare = reacts, v.reacts
}

// Message is a view of one loaded message, valid until the store is next
// applied.
type Message struct {
	v *Conversation
	i uint32
}

func (m Message) r() *msgRec { return &m.v.recs[m.i] }

// Index is the message's place in its conversation.
func (m Message) Index() int { return int(m.i) }

// ID is the message id.
func (m Message) ID() []byte { return m.v.a.at(m.r().id) }

// Sender is the sender's JID (ours for our messages).
func (m Message) Sender() []byte { return m.v.a.at(m.r().sender) }

// SenderName is the name the message carried for its sender (a group
// member's push name), empty when it carried none.
func (m Message) SenderName() []byte { return m.v.a.at(m.r().senderName) }

// FromMe reports whether we sent it.
func (m Message) FromMe() bool { return m.r().flags&msgFromMe != 0 }

// Time is when it was sent, in Unix seconds.
func (m Message) Time() int64 { return m.r().ts }

// Kind is the content's kind.
func (m Message) Kind() abi.ContentTag { return m.r().kind }

// Text is the message's text: a text's body, a picture's, video's or
// document's caption, a poll's question, a location's name, a contact's
// display name. The markup and mentions ports turn it into spans.
func (m Message) Text() []byte { return m.v.a.at(m.r().text) }

// Status is the delivery state of our message.
func (m Message) Status() abi.Delivery { return m.r().status }

// Edited and Forwarded are the message's marks.
func (m Message) Edited() bool    { return m.r().flags&msgEdited != 0 }
func (m Message) Forwarded() bool { return m.r().flags&msgForwarded != 0 }

// Quote is the message replied to: its id, its sender's name (else JID)
// and its summary; ok is false for no reply.
func (m Message) Quote() (id, sender, summary []byte, ok bool) {
	r := m.r()
	return m.v.a.at(r.quoteID), m.v.a.at(r.quoteSender), m.v.a.at(r.quoteSummary), r.flags&msgHasQuote != 0
}

// Reactions is how many distinct emojis reacted; Reaction(j) is one of them.
func (m Message) Reactions() int { return int(m.r().nReacts) }

// Reaction is emoji j of the summary, how many reacted with it, and whether
// one of them is us.
func (m Message) Reaction(j int) (emoji []byte, count int, mine bool) {
	re := m.v.reacts[m.r().reacts+uint32(j)]
	return m.v.a.at(re.emoji), int(re.count), re.mine
}

// Reacted reports whether we reacted.
func (m Message) Reacted() bool { return m.r().flags&msgMineReacted != 0 }

// Media is the message's attachment, when it has one.
func (m Message) Media() (Media, bool) {
	r := m.r()
	if r.flags&msgHasMedia == 0 {
		return Media{}, false
	}
	md := Media{
		Path: m.v.a.at(r.mediaPath), Mime: m.v.a.at(r.mime), FileName: m.v.a.at(r.fileName),
		Size: r.size, Width: r.w, Height: r.h, State: r.media,
		Voice: r.flags&msgVoice != 0, Gif: r.flags&msgGif != 0, VideoNote: r.flags&msgVideoNote != 0, Animated: r.flags&msgAnimated != 0,
	}
	if r.flags&msgHasSeconds != 0 {
		md.Seconds, md.HasSeconds = r.seconds, true
	}
	if r.flags&msgHasThumb != 0 {
		md.Thumb, md.HasThumb = r.thumb, true
	}
	return md, true
}

// Thumbnail is the inline thumbnail's blob (Conn.ReadBlob), if any.
func (m Message) Thumbnail() (abi.BlobRef, bool) {
	r := m.r()
	return r.thumb, r.flags&msgHasThumb != 0
}

// Gen moves whenever this message changes.
func (m Message) Gen() uint32 { return m.r().gen }

// Decode decodes the whole message into dst (Conversation.Decode).
func (m Message) Decode(dst *abi.Message) error { return m.v.Decode(int(m.i), dst) }

// Media is a message's attachment, as the transcript draws it. Its bytes
// are views (valid until the next apply).
type Media struct {
	Path, Mime, FileName []byte // Path is empty until it is downloaded
	Size                 uint64
	Width, Height        uint32 // 0 when not known
	Seconds              uint32
	HasSeconds           bool
	State                abi.MediaStateTag
	Voice, Gif           bool // a voice note; a video that plays as a GIF
	VideoNote, Animated  bool // a round video note; an animated sticker
	Thumb                abi.BlobRef
	HasThumb             bool
}
