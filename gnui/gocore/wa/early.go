package wa

import (
	"cmp"
	"log/slog"
	"slices"
	"time"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/store"
)

// The phone's own read receipts and reactions that name a message not
// archived yet (zf:src/backend/worker/early_events.rs). During the offline
// drain receipts can come before their messages, and a message can also
// reach us later through history sync. These events wait here, in memory,
// until their message is filed, and are dropped after earlyMaxAge or
// beyond earlyMax of a kind.
const (
	// earlyMaxAge is how long an event may wait for its message.
	earlyMaxAge = time.Hour
	// earlyMax is how many events of a kind may wait; the oldest go first.
	earlyMax = 512
)

type earlyRead struct {
	chat, id string
	queued   time.Time
}

// earlyReaction is a reaction, or its removal (no emoji), whose target has
// not been filed yet. at is when the reactor sent it, in milliseconds: the
// newest per sender wins.
type earlyReaction struct {
	chat, target, sender, emoji string
	at                          int64
	queued                      time.Time
}

type earlyEvents struct {
	reads     []earlyRead
	reactions []earlyReaction
}

// waitRead keeps the phone's read of a message we do not have yet.
func (q *earlyEvents) waitRead(chat, id string, now time.Time) {
	q.prune(now)
	if slices.ContainsFunc(q.reads, func(r earlyRead) bool { return r.chat == chat && r.id == id }) {
		return
	}
	if len(q.reads) >= earlyMax {
		q.reads = q.reads[1:]
	}
	q.reads = append(q.reads, earlyRead{chat: chat, id: id, queued: now})
}

// waitReaction keeps a reaction for a message we do not have yet. A
// sender's newer reaction replaces an older one; an older one arriving
// late is ignored.
func (q *earlyEvents) waitReaction(r earlyReaction, now time.Time) {
	q.prune(now)
	r.queued = now
	for i, w := range q.reactions {
		if w.chat == r.chat && w.target == r.target && w.sender == r.sender {
			if w.at <= r.at {
				q.reactions[i] = r
			}
			return
		}
	}
	if len(q.reactions) >= earlyMax {
		q.reactions = q.reactions[1:]
	}
	q.reactions = append(q.reactions, r)
}

// takeRead reports whether the phone read this message before we had it.
func (q *earlyEvents) takeRead(chat, id string) bool {
	before := len(q.reads)
	q.reads = slices.DeleteFunc(q.reads, func(r earlyRead) bool { return r.chat == chat && r.id == id })
	return len(q.reads) != before
}

// takeReactions are the reactions waiting for this message, oldest first.
func (q *earlyEvents) takeReactions(chat, id string) []earlyReaction {
	var taken []earlyReaction
	q.reactions = slices.DeleteFunc(q.reactions, func(r earlyReaction) bool {
		if r.chat == chat && r.target == id {
			taken = append(taken, r)
			return true
		}
		return false
	})
	slices.SortStableFunc(taken, func(a, b earlyReaction) int { return cmp.Compare(a.at, b.at) })
	return taken
}

// rekey files events kept under a privacy id under the id it now maps to,
// and returns the messages they wait for in that chat when any moved.
func (q *earlyEvents) rekey(from, to string) []string {
	if from == to {
		return nil
	}
	moved := false
	for i := range q.reads {
		if q.reads[i].chat == from {
			q.reads[i].chat, moved = to, true
		}
	}
	for i := range q.reactions {
		r := &q.reactions[i]
		if r.chat == from {
			r.chat, moved = to, true
		}
		if r.sender == from {
			r.sender, moved = to, true
		}
	}
	if !moved {
		return nil
	}
	var ids []string
	for _, r := range q.reads {
		if r.chat == to {
			ids = append(ids, r.id)
		}
	}
	for _, r := range q.reactions {
		if r.chat == to {
			ids = append(ids, r.target)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// prune drops events whose message never came.
func (q *earlyEvents) prune(now time.Time) {
	q.reads = slices.DeleteFunc(q.reads, func(r earlyRead) bool { return now.Sub(r.queued) >= earlyMaxAge })
	q.reactions = slices.DeleteFunc(q.reactions, func(r earlyReaction) bool { return now.Sub(r.queued) >= earlyMaxAge })
}

// settleEarly applies the reactions that waited for m, now filed, and
// reports whether the phone read it before it was
// (zf:src/backend/worker.rs:4594).
func (e *Engine) settleEarly(m store.Message) bool {
	read := e.early.takeRead(m.ChatID, m.ID) && !m.FromMe
	for _, r := range e.early.takeReactions(m.ChatID, m.ID) {
		sender := r.sender
		// The sender's privacy id may have been mapped since.
		if j, ok := jidOf(sender); ok && j.Server == types.HiddenUserServer {
			sender = e.canonical(j)
		}
		if _, err := e.arch.ApplyReaction(m.ChatID, m.ID, store.Reaction{Sender: sender, Emoji: r.emoji, Timestamp: r.at / 1000}, r.at); err != nil {
			slog.Warn("zapfast wa: a waiting reaction was not stored", "err", err)
		}
	}
	return read
}

// readThrough lowers chat's unread count to the messages after m, as the
// phone's read of m does. The phone's own unread mark stays.
func (e *Engine) readThrough(chat string, m store.Message) error {
	left, err := e.arch.UnreadAfter(chat, m)
	if err != nil {
		return err
	}
	return e.arch.UpdateChat(chat, func(c *store.Chat) { c.Unread = min(c.Unread, left) })
}

// settleFiled settles what waited for messages filed outside keep: history
// (zf:src/backend/worker.rs:5395). It reports whether any was read.
func (e *Engine) settleFiled(chat string, ids []string) (bool, error) {
	if len(e.early.reads) == 0 && len(e.early.reactions) == 0 {
		return false, nil
	}
	var newest store.Message
	for _, id := range ids {
		m, err := e.arch.Message(chat, id)
		if err != nil {
			continue
		}
		if e.settleEarly(m) && (newest.ID == "" || m.Timestamp > newest.Timestamp) {
			newest = m
		}
	}
	if newest.ID == "" {
		return false, nil
	}
	return true, e.readThrough(chat, newest)
}

// remapEarly follows a privacy id to the phone number it now maps to: what
// waited under it waits under the number, and what waited for a message
// already filed there applies (zf:src/backend/worker.rs:2794). It runs
// after the current event, which may be filing under the number itself.
func (e *Engine) remapEarly(from, to string) {
	ids := e.early.rekey(from, to)
	if len(ids) == 0 {
		return
	}
	e.post(func() {
		var filed []string
		for _, id := range ids {
			if _, err := e.arch.Message(to, id); err == nil {
				filed = append(filed, id)
			}
		}
		if len(filed) == 0 {
			return
		}
		if _, err := e.settleFiled(to, filed); err != nil {
			slog.Warn("zapfast wa: a waiting read was not applied", "err", err)
		}
		for _, id := range filed {
			e.emitMessage(to, id)
		}
		e.emitChat(to)
	})
}
