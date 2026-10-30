package wa

import (
	"cmp"
	"log/slog"
	"slices"
	"time"

	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
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

// settleEarly applies the receipts and reactions that waited for m, now
// filed, and reports whether the phone read it before it was
// (zf:src/backend/worker.rs:4594).
func (e *Engine) settleEarly(m store.Message) bool {
	if m.FromMe {
		e.settleReceipts(m.ChatID, m.ID)
	}
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
	e.settleWaiting(chat)
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
	waiting, _ := e.arch.WaitingFiled(to)
	if len(ids) == 0 && len(waiting) == 0 {
		return
	}
	e.post(func() {
		moved := e.settleWaiting(to)
		for _, id := range moved {
			e.emitMessage(to, id)
		}
		var filed []string
		for _, id := range ids {
			if _, err := e.arch.Message(to, id); err == nil {
				filed = append(filed, id)
			}
		}
		if len(filed) == 0 {
			if len(moved) > 0 {
				e.emitChat(to)
			}
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

// waitReceipt keeps a recipient's receipt of a message not filed yet, on
// disk: ours, sent from the phone, may come after it, live or by history
// (zf:src/backend/worker.rs:3903).
func (e *Engine) waitReceipt(chat, id, sender string, status int, at int64) {
	if _, err := e.arch.Message(chat, id); !noRows(err) {
		return
	}
	if err := e.arch.WaitReceipt(chat, id, sender, status, at, time.Now().Unix()); err != nil {
		slog.Warn("zapfast wa: an early receipt was not kept", "err", err)
	}
}

// settleReceipts applies the receipts that waited for our message, now
// filed, and reports whether its ticks moved
// (zf:src/backend/worker.rs:3993). A group message sent elsewhere has no
// saved audience: the group's current members are the best record of who
// it went to, so they become it.
func (e *Engine) settleReceipts(chat, id string) bool {
	waiting, err := e.arch.TakeWaitingReceipts(chat, id)
	if err != nil {
		slog.Warn("zapfast wa: early receipts were not applied", "err", err)
		return false
	}
	if len(waiting) == 0 {
		return false
	}
	group := chatKind("", chat) == abi.ChatKindGroup
	if c, err := e.arch.Chat(chat); group && err == nil {
		var audience []string
		for _, p := range c.Participants {
			if !e.isMe(p) {
				audience = append(audience, p)
			}
		}
		if len(audience) > 0 {
			if err := e.arch.SetAudience(chat, id, audience); err != nil {
				slog.Warn("zapfast wa: storing a group audience", "err", err)
			}
		}
	}
	moved := false
	for _, w := range waiting {
		sender := w.Sender
		// The recipient's privacy id may have been mapped since.
		if j, ok := jidOf(sender); ok && j.Server == types.HiddenUserServer {
			sender = e.canonical(j)
		}
		// Each stage at the time it was first seen; the furthest even
		// without one.
		times := [...]int64{0, w.DeliveredAt, w.ReadAt, w.Played}
		for stage := 1; stage <= min(w.Status, 3); stage++ {
			if times[stage] == 0 && stage < w.Status {
				continue
			}
			known, err := e.fileReceipt(chat, id, sender, stage, times[stage], group)
			if err != nil {
				slog.Warn("zapfast wa: an early receipt was not applied", "err", err)
				break
			}
			moved = moved || known
		}
	}
	return moved
}

// settleWaiting applies the receipts that wait for messages already filed
// in chat: by history, or under a privacy id mapped since
// (zf:src/backend/worker.rs:2790). It returns the messages whose ticks
// moved.
func (e *Engine) settleWaiting(chat string) []string {
	ids, err := e.arch.WaitingFiled(chat)
	if err != nil {
		slog.Warn("zapfast wa: early receipts were not applied", "err", err)
		return nil
	}
	var moved []string
	for _, id := range ids {
		if e.settleReceipts(chat, id) {
			moved = append(moved, id)
		}
	}
	return moved
}
