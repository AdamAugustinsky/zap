package model

import (
	"bytes"
	"slices"

	"zapfast/abi"
)

// ChatRef names a chat row of one account's store, 0 for none. A chat keeps
// its ref while it is in the list; a removed chat's ref names nothing ever
// again (its row is wiped, as upstream drops it: zf:src/app.rs:2237), and a
// chat of the same JID that comes back later gets a new ref and starts
// clean. An unlink ends the session and its refs (the state gives the
// account a new Serial then). It is what screens keep instead of a view.
type ChatRef uint32

// chatFlags are a chat row's booleans.
type chatFlags uint32

const (
	chatArchived chatFlags = 1 << iota
	chatPinned
	chatFavorite
	chatLocked
	chatMarkedUnread
	chatReadOnly
	chatLeft
	chatAdmin
	chatSubjectKnown
	chatHasMuted
	chatHasLast
	chatLastFromMe
	chatHasMention
	chatInfoLocked
	chatHasInfoLocked
	chatHasEphemeral
	chatRemoved // a wiped row: not in the list, its ChatRef never reused
	chatStale   // not in the reload being applied (yet)
)

// chatRec is one chat row. Every Str but avatar points into raw, the
// row's encoded abi.Chat.
type chatRec struct {
	raw                                  Str
	id, name, summary, sender, full, mnt Str
	last, pinnedAt, mutedUntil           int64
	unread, favPos, ephemeral            uint32
	labels                               uint32 // first of the row's label ids in Chats.labels
	nLabels                              uint16
	kind                                 abi.ChatKind
	lastStatus                           abi.Delivery
	flags                                chatFlags
	gen                                  uint32
}

// Chats is the chat list of one account.
type Chats struct {
	recs        []chatRec
	a           arena
	spare       []byte
	idx         index
	labels      []Str // the rows' label ids, into the arena
	spareLabels []Str
	order       []ChatRef
	sorted      bool
	live        int

	gen, orderGen uint32
	// base is the generation a new row starts at: above every row's of a
	// session that ended (Store.endSession).
	base      uint32
	batch     uint32
	reloading bool // a batch is arriving: rows not in it go at its last chunk
	loaded    bool // a whole batch has arrived
	scratch   abi.Chat
}

func (c *Chats) key(row uint32) []byte { return c.a.at(c.recs[row].id) }

// Gen moves whenever a chat row changes, appears or goes.
func (c *Chats) Gen() uint32 { return c.gen }

// OrderGen moves whenever the list's order or membership changes.
func (c *Chats) OrderGen() uint32 { return c.orderGen }

// Loaded reports whether a whole chat list has arrived (the first frame
// shows skeleton rows until then).
func (c *Chats) Loaded() bool { return c.loaded }

// Len is the number of chats in the list (not counting removed rows).
func (c *Chats) Len() int { return c.live }

// Find returns the chat with the id, or 0.
func (c *Chats) Find(id []byte) ChatRef {
	i := c.idx.find(id, c)
	if i < 0 || c.recs[i].flags&chatRemoved != 0 {
		return 0
	}
	return ChatRef(i + 1)
}

// Get is the view of the chat ref names; ok is false for 0 or a removed
// chat.
func (c *Chats) Get(ref ChatRef) (Chat, bool) {
	if ref == 0 || int(ref) > len(c.recs) || c.recs[ref-1].flags&chatRemoved != 0 {
		return Chat{}, false
	}
	return Chat{c, ref}, true
}

// At is the view of the chat ref names, which must exist (Get checks).
func (c *Chats) At(ref ChatRef) Chat { return Chat{c, ref} }

// Order is every chat in the list's base order: pinned chats first, the
// latest pinned on top, then by latest activity (upstream's chat_list,
// zf:src/app.rs:2844, without its Favorites order, which the list screen
// applies). The slice is the store's: valid until the next apply.
func (c *Chats) Order() []ChatRef {
	if !c.sorted {
		c.order = c.order[:0]
		for i := range c.recs {
			if c.recs[i].flags&chatRemoved == 0 {
				c.order = append(c.order, ChatRef(i+1))
			}
		}
		slices.SortFunc(c.order, c.compare)
		c.sorted = true
	}
	return c.order
}

func (c *Chats) compare(x, y ChatRef) int {
	a, b := &c.recs[x-1], &c.recs[y-1]
	ap, bp := a.flags&chatPinned != 0, b.flags&chatPinned != 0
	switch {
	case ap != bp:
		if ap {
			return -1
		}
		return 1
	case ap && a.pinnedAt != b.pinnedAt:
		return cmpDesc(a.pinnedAt, b.pinnedAt)
	case !ap && a.last != b.last:
		return cmpDesc(a.last, b.last)
	}
	return bytes.Compare(c.a.at(a.id), c.a.at(b.id))
}

func cmpDesc(a, b int64) int {
	if a > b {
		return -1
	}
	if a < b {
		return 1
	}
	return 0
}

// Decode decodes chat ref's whole record into dst, whose byte fields then
// point into the store (valid until the next apply). dst's slices are
// reused, so a screen that keeps one abi.Chat decodes without allocating.
func (c *Chats) Decode(ref ChatRef, dst *abi.Chat) error {
	return dst.Decode(c.a.at(c.recs[ref-1].raw))
}

// chats applies a Chats chunk. The first chunk of a new batch starts a
// reload: rows the batch does not carry are removed at its last chunk, so
// the refs of the chats that stay do not change.
func (c *Chats) chunk(e *abi.EventChats) {
	if !c.reloading || e.Batch != c.batch {
		c.batch, c.reloading = e.Batch, true
		for i := range c.recs {
			c.recs[i].flags |= chatStale
		}
	}
	for i := range e.Chats {
		c.upsert(&e.Chats[i])
	}
	if e.Last {
		c.reloading, c.loaded = false, true
		for i := range c.recs {
			if r := &c.recs[i]; r.flags&chatStale != 0 {
				r.flags &^= chatStale
				c.remove(uint32(i))
			}
		}
	}
	c.gen++
	c.orderGen++
}

// upsert applies one chat row, returning its ref.
func (c *Chats) upsert(ch *abi.Chat) ChatRef {
	if len(ch.ID) == 0 {
		return 0
	}
	row := c.idx.find(ch.ID, c)
	var old chatRec
	if row >= 0 {
		old = c.recs[row]
		c.a.drop(old.raw)
	}
	start := len(c.a.b)
	var err error
	c.a.b, err = ch.Encode(c.a.b)
	if err != nil {
		c.a.b = c.a.b[:start]
		return 0
	}
	raw := Str{uint32(start), uint32(len(c.a.b) - start)}
	c.a.live += int(raw.Len)
	d := &c.scratch
	defer d.Release() // the row keeps offsets; the views would pin a retired arena
	if d.Decode(c.a.at(raw)) != nil {
		return 0
	}
	r := chatRec{
		raw: raw, id: c.a.sub(raw, d.ID), name: c.a.sub(raw, d.Name),
		last: d.LastActivity, pinnedAt: d.PinnedAt, mutedUntil: d.MutedUntil,
		unread: d.Unread, favPos: d.FavoritePosition, kind: d.Kind,
	}
	if d.HasLast {
		r.flags |= chatHasLast
		r.summary, r.full = c.a.sub(raw, d.Last.Summary), c.a.sub(raw, d.Last.Full)
		r.sender = c.a.sub(raw, d.Last.SenderName)
		if len(r.sender.bytes(c)) == 0 {
			r.sender = c.a.sub(raw, d.Last.Sender)
		}
		r.lastStatus = d.Last.Status
		if d.Last.FromMe {
			r.flags |= chatLastFromMe
		}
	}
	if d.HasUnreadMention {
		r.flags |= chatHasMention
		r.mnt = c.a.sub(raw, d.UnreadMention)
	}
	if d.HasEphemeralExpiration {
		r.flags |= chatHasEphemeral
		r.ephemeral = d.EphemeralExpiration
	}
	set := func(on bool, f chatFlags) {
		if on {
			r.flags |= f
		}
	}
	set(d.Archived, chatArchived)
	set(d.Pinned, chatPinned)
	set(d.Favorite, chatFavorite)
	set(d.Locked, chatLocked)
	set(d.MarkedUnread, chatMarkedUnread)
	set(d.ReadOnly, chatReadOnly)
	set(d.Left, chatLeft)
	set(d.Admin, chatAdmin)
	set(d.GroupSubjectKnown, chatSubjectKnown)
	set(d.HasMutedUntil, chatHasMuted)
	set(d.InfoLocked, chatInfoLocked)
	set(d.HasInfoLocked, chatHasInfoLocked)
	r.labels, r.nLabels = uint32(len(c.labels)), uint16(len(d.Labels))
	for _, l := range d.Labels {
		c.labels = append(c.labels, c.a.sub(raw, l))
	}
	if row >= 0 {
		r.gen = old.gen + 1
		if old.flags&chatRemoved != 0 || old.last != r.last || old.pinnedAt != r.pinnedAt ||
			(old.flags^r.flags)&chatPinned != 0 {
			c.sorted = false
			c.orderGen++
		}
		if old.flags&chatRemoved != 0 {
			c.live++
		}
		c.recs[row] = r
	} else {
		row = len(c.recs)
		r.gen = c.base
		c.recs = append(c.recs, r)
		c.idx.add(ch.ID, uint32(row))
		c.live++
		c.sorted = false
		c.orderGen++
	}
	c.gen++
	if c.a.wasteful() {
		c.compact()
	}
	return ChatRef(row + 1)
}

// MarkRead clears chat ref's unread state here at once, as upstream's
// mark_read does before the core confirms (zf:src/app.rs:5057-5064): no
// unread count, no hand-made unread mark, no unread mention. It reports
// whether anything changed. It allocates nothing.
func (c *Chats) MarkRead(ref ChatRef) bool {
	if ref == 0 || int(ref) > len(c.recs) {
		return false
	}
	r := &c.recs[ref-1]
	if r.flags&chatRemoved != 0 || (r.unread == 0 && r.flags&(chatMarkedUnread|chatHasMention) == 0) {
		return false
	}
	r.unread = 0
	r.flags &^= chatMarkedUnread | chatHasMention
	r.gen++
	c.gen++
	return true
}

// remove wipes row: its record's bytes are overwritten and dropped, and its
// JID goes, so the index never finds it again (its slot stays, matching
// nothing).
func (c *Chats) remove(row uint32) {
	r := &c.recs[row]
	if r.flags&chatRemoved != 0 {
		return
	}
	clear(c.a.at(r.raw))
	c.a.drop(r.raw)
	*r = chatRec{flags: chatRemoved, gen: r.gen + 1}
	c.live--
	c.sorted = false
	c.gen++
	c.orderGen++
}

// compact copies the rows' records into a fresh arena and moves every range
// with them.
func (c *Chats) compact() {
	old := c.a.b
	c.a.b, c.spare = c.spare[:0], old
	c.a.live = 0
	labels := c.spareLabels[:0]
	for i := range c.recs {
		r := &c.recs[i]
		ob := r.raw
		nb := c.a.put(old[ob.Off : ob.Off+ob.Len])
		r.raw = nb
		r.id, r.name, r.summary = rebase(r.id, ob, nb), rebase(r.name, ob, nb), rebase(r.summary, ob, nb)
		r.sender, r.full, r.mnt = rebase(r.sender, ob, nb), rebase(r.full, ob, nb), rebase(r.mnt, ob, nb)
		first := len(labels)
		for j := uint32(0); j < uint32(r.nLabels); j++ {
			labels = append(labels, rebase(c.labels[r.labels+j], ob, nb))
		}
		r.labels = uint32(first)
	}
	c.labels, c.spareLabels = labels, c.labels
}

func (s Str) bytes(c *Chats) []byte { return c.a.at(s) }

// Chat is a view of one chat row, valid until the store is next applied.
type Chat struct {
	c   *Chats
	ref ChatRef
}

func (v Chat) r() *chatRec { return &v.c.recs[v.ref-1] }

// Ref is the chat's ref.
func (v Chat) Ref() ChatRef { return v.ref }

// ID is the chat's JID.
func (v Chat) ID() []byte { return v.c.a.at(v.r().id) }

// Name is the chat's name as the core sends it (the contact's or group's
// name; empty when it has none: screens fall back to Store.DisplayName).
func (v Chat) Name() []byte { return v.c.a.at(v.r().name) }

// Preview is the last message's summary as the chat list shows it.
func (v Chat) Preview() []byte { return v.c.a.at(v.r().summary) }

// PreviewFull is the last message's full text, for the row's tooltip.
func (v Chat) PreviewFull() []byte { return v.c.a.at(v.r().full) }

// PreviewSender is who sent the last message (their name, else their JID).
func (v Chat) PreviewSender() []byte { return v.c.a.at(v.r().sender) }

// HasLast reports whether the chat has a last message.
func (v Chat) HasLast() bool { return v.r().flags&chatHasLast != 0 }

// LastFromMe reports whether we sent the last message.
func (v Chat) LastFromMe() bool { return v.r().flags&chatLastFromMe != 0 }

// LastStatus is the last message's delivery, for its ticks.
func (v Chat) LastStatus() abi.Delivery { return v.r().lastStatus }

// Time is the chat's last activity, in Unix seconds.
func (v Chat) Time() int64 { return v.r().last }

// Unread is the unread message count.
func (v Chat) Unread() int { return int(v.r().unread) }

// MarkedUnread reports whether the chat was marked unread by hand.
func (v Chat) MarkedUnread() bool { return v.r().flags&chatMarkedUnread != 0 }

// Muted reports whether the chat is muted at now (Unix seconds): muted for
// ever (until 0) or until later (zf:src/model.rs:424).
func (v Chat) Muted(now int64) bool {
	r := v.r()
	return r.flags&chatHasMuted != 0 && (r.mutedUntil == 0 || r.mutedUntil > now)
}

// MutedUntil is the end of the mute, 0 for ever; ok is false when unmuted.
func (v Chat) MutedUntil() (until int64, ok bool) {
	r := v.r()
	return r.mutedUntil, r.flags&chatHasMuted != 0
}

// Pinned reports whether the chat is pinned, and PinnedAt when.
func (v Chat) Pinned() bool     { return v.r().flags&chatPinned != 0 }
func (v Chat) PinnedAt() int64  { return v.r().pinnedAt }
func (v Chat) Archived() bool   { return v.r().flags&chatArchived != 0 }
func (v Chat) Favorite() bool   { return v.r().flags&chatFavorite != 0 }
func (v Chat) FavoritePos() int { return int(v.r().favPos) }
func (v Chat) Locked() bool     { return v.r().flags&chatLocked != 0 }
func (v Chat) ReadOnly() bool   { return v.r().flags&chatReadOnly != 0 }
func (v Chat) Left() bool       { return v.r().flags&chatLeft != 0 }
func (v Chat) Admin() bool      { return v.r().flags&chatAdmin != 0 }

// Kind is direct, group or broadcast.
func (v Chat) Kind() abi.ChatKind { return v.r().kind }

// Group reports whether the chat is a group.
func (v Chat) Group() bool { return v.r().kind == abi.ChatKindGroup }

// SubjectKnown reports whether a group's name is known (else Name is a
// placeholder).
func (v Chat) SubjectKnown() bool { return v.r().flags&chatSubjectKnown != 0 }

// UnreadMention is the message id of the oldest unread mention of us (the
// list's @ marker); ok is false when there is none.
func (v Chat) UnreadMention() (id []byte, ok bool) {
	r := v.r()
	return v.c.a.at(r.mnt), r.flags&chatHasMention != 0
}

// Ephemeral is the disappearing-message timer in seconds; ok is false when
// the chat has none known.
func (v Chat) Ephemeral() (seconds uint32, ok bool) {
	r := v.r()
	return r.ephemeral, r.flags&chatHasEphemeral != 0
}

// InfoLocked reports whether only admins may change the group's info; ok
// is false when that is not known.
func (v Chat) InfoLocked() (locked, ok bool) {
	r := v.r()
	return r.flags&chatInfoLocked != 0, r.flags&chatHasInfoLocked != 0
}

// Labels is the number of labels the chat has; Label(i) is label i's id
// (Store.Labels names it).
func (v Chat) Labels() int { return int(v.r().nLabels) }

// Label is the id of the chat's label i.
func (v Chat) Label(i int) []byte {
	r := v.r()
	return v.c.a.at(v.c.labels[r.labels+uint32(i)])
}

// HasLabel reports whether the chat has the label with id.
func (v Chat) HasLabel(id []byte) bool {
	for i := 0; i < v.Labels(); i++ {
		if bytes.Equal(v.Label(i), id) {
			return true
		}
	}
	return false
}

// Gen moves whenever this row changes.
func (v Chat) Gen() uint32 { return v.r().gen }

// Decode decodes the whole row (participants, the group's description, the
// notification sound …) into dst: Chats.Decode.
func (v Chat) Decode(dst *abi.Chat) error { return v.c.Decode(v.ref, dst) }
