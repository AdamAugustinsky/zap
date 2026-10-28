package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// MaxPins is how many active pins one chat may keep, as WhatsApp allows.
const MaxPins = 3

// Pinner says who pinned a message, for the notice in the chat.
type Pinner struct {
	// ByMe: this account pinned it.
	ByMe bool
	// Sender is who did, when someone else did.
	Sender string
}

// Pinned is one pinned message, as the line under the chat header shows it.
type Pinned struct {
	Chat, ID            string
	PinnedAt, ExpiresAt int64
	Pinner              Pinner
	// Message is the message itself: its words, its side and time.
	Message Message
}

// Message pins follow src/archive/pins.rs: a pin is written once WhatsApp
// confirms it, or when one arrives from someone else. The row stays after
// an unpin, so its time is not forgotten and a late replay cannot put the
// pin back. Expired pins drop out on read.

// SetPin records a pin or an unpin at `at`; expiresAt is when a pin ends.
// An older event loses to the time stored. A message that is gone (deleted
// here or revoked for everyone) takes no pin: a replayed pin would hold one
// of the chat's three places with nothing to show. It reports whether the
// row changed.
func (s *Archive) SetPin(chat, id string, pinned bool, at, expiresAt int64, by Pinner) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pinned {
		expiresAt = 0
	} else if gone, err := pinTargetGone(s.db, chat, id); err != nil || gone {
		return false, err
	}
	r, err := s.db.Exec(`INSERT INTO message_pins(chat,id,pinned,pinned_at,expires_at,by_me,sender) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(chat,id) DO UPDATE SET pinned=excluded.pinned,pinned_at=excluded.pinned_at,expires_at=excluded.expires_at,by_me=excluded.by_me,sender=excluded.sender
WHERE excluded.pinned_at>=message_pins.pinned_at`, chat, id, pinned, at, expiresAt, by.ByMe, by.Sender)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// PinFull reports whether the chat already holds MaxPins active pins other
// than id at `now`.
func (s *Archive) PinFull(chat, id string, now int64) (bool, error) {
	ids, err := s.PinnedIDs(chat, now)
	if err != nil {
		return false, err
	}
	for _, p := range ids {
		if p == id {
			return false, nil
		}
	}
	return len(ids) >= MaxPins, nil
}

// PinnedIDs are a chat's active pins at `now`, newest first, at most
// MaxPins. A pin whose message is gone does not count.
func (s *Archive) PinnedIDs(chat string, now int64) ([]string, error) {
	pins, err := s.ChatPins(chat, now)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(pins))
	for i, p := range pins {
		ids[i] = p.ID
	}
	return ids, nil
}

// ChatPins are a chat's active pins at `now` with their messages, newest
// pin first, at most MaxPins. A pin whose message the archive does not hold
// (yet) or that is gone is skipped.
func (s *Archive) ChatPins(chat string, now int64) ([]Pinned, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT p.id,p.pinned_at,p.expires_at,p.by_me,p.sender,m.payload,m.raw
FROM message_pins p JOIN messages m ON m.chat=p.chat AND m.id=p.id
WHERE p.chat=? AND p.pinned=1 AND p.expires_at>?
AND NOT EXISTS(SELECT 1 FROM tombstones t WHERE t.chat=p.chat AND t.id=p.id)
ORDER BY p.pinned_at DESC,p.rowid DESC`, chat, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Pinned
	for rows.Next() && len(list) < MaxPins {
		p := Pinned{Chat: chat}
		var data []byte
		if err = rows.Scan(&p.ID, &p.PinnedAt, &p.ExpiresAt, &p.Pinner.ByMe, &p.Pinner.Sender, &data, &p.Message.Raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &p.Message); err != nil {
			return nil, err
		}
		if !p.Message.Deleted {
			list = append(list, p)
		}
	}
	return list, rows.Err()
}

// pinTargetGone reports whether a message can no longer carry a pin: the
// archive keeps a tombstone for it, or it was revoked for everyone.
func pinTargetGone(q interface {
	QueryRow(string, ...any) *sql.Row
}, chat, id string) (bool, error) {
	var tomb int
	if err := q.QueryRow("SELECT count(*) FROM tombstones WHERE chat=? AND id=?", chat, id).Scan(&tomb); err != nil || tomb > 0 {
		return tomb > 0, err
	}
	var data []byte
	err := q.QueryRow("SELECT payload FROM messages WHERE chat=? AND id=?", chat, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var m struct {
		Deleted bool `json:"deleted"`
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return false, err
	}
	return m.Deleted, nil
}
