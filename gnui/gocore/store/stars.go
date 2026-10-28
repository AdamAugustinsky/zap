package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// StarredLimit is how many starred messages the list shows (upstream's
// STARRED_LIMIT).
const StarredLimit = 200

// Starred is one starred message, as the Starred list shows it.
type Starred struct {
	// Message is the message itself, read from its own row: its current
	// words. Personal data: never logged.
	Message Message
	// StarredAt is the Unix second the star was confirmed.
	StarredAt int64
}

// setStar records a star or an unstar at `at`, as src/archive/stars.rs does:
// an older event loses to the time stored, so a late response cannot put a
// row back; at the same second only a live event (not replayed) replaces
// it. It reports whether the row changed.
func setStar(tx *sql.Tx, chat, id string, starred bool, at int64, replayed bool) (bool, error) {
	r, err := tx.Exec(`INSERT INTO stars(chat,id,starred,starred_at) VALUES(?,?,?,?)
ON CONFLICT(chat,id) DO UPDATE SET starred=excluded.starred,starred_at=excluded.starred_at
WHERE excluded.starred_at>stars.starred_at OR (excluded.starred_at=stars.starred_at AND ?=0)`,
		chat, id, starred, at, replayed)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// starFromTable makes the stars table decide m.Starred, and gives a message
// that arrives starred without a row one, at its own time, as a replay.
func starFromTable(tx *sql.Tx, m *Message) error {
	var starred bool
	err := tx.QueryRow("SELECT starred FROM stars WHERE chat=? AND id=?", m.ChatID, m.ID).Scan(&starred)
	if err == nil {
		m.Starred = starred
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !m.Starred {
		return nil
	}
	_, err = setStar(tx, m.ChatID, m.ID, true, m.Timestamp, true)
	return err
}

// SetStar records a star or an unstar at `at` (Unix seconds); replayed says
// it came from a history replay, which the phone may send again at any time.
// The message's own row follows when it changed. It reports whether the
// event was newer than the one stored.
func (s *Archive) SetStar(chat, id string, starred bool, at int64, replayed bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := setStar(tx, chat, id, starred, at, replayed)
	if err != nil || !changed {
		return false, err
	}
	m, err := scanMessage(tx.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND id=?", chat, id))
	if err == nil && m.Starred != starred {
		m.Starred = starred
		data, err := encode(m)
		if err != nil {
			return false, err
		}
		if _, err = tx.Exec("UPDATE messages SET payload=? WHERE chat=? AND id=?", data, chat, id); err != nil {
			return false, err
		}
		if _, err = tx.Exec("UPDATE message_flags SET starred=? WHERE chat=? AND id=?", starred, chat, id); err != nil {
			return false, err
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, tx.Commit()
}

// StarredIDs are the starred messages of one chat, in star order, for the
// mark in the conversation.
func (s *Archive) StarredIDs(chat string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryStrings(s.db, "SELECT id FROM stars WHERE chat=? AND starred=1 ORDER BY starred_at,rowid", chat)
}

// Starred lists the starred messages, newest star first, at most limit.
// A message deleted here or revoked for everyone leaves the list, and a
// locked chat's stay out of it.
func (s *Archive) Starred(limit int) ([]Starred, error) {
	if limit <= 0 {
		limit = StarredLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT s.starred_at,m.payload,m.raw FROM stars s
JOIN messages m ON m.chat=s.chat AND m.id=s.id
LEFT JOIN chat_visibility v ON v.chat=s.chat
WHERE s.starred=1 AND COALESCE(v.locked,0)=0
ORDER BY s.starred_at DESC,s.rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Starred
	for rows.Next() && len(list) < limit {
		var st Starred
		var data []byte
		if err = rows.Scan(&st.StarredAt, &data, &st.Message.Raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &st.Message); err != nil {
			return nil, err
		}
		if !st.Message.Deleted {
			list = append(list, st)
		}
	}
	return list, rows.Err()
}

// queryStrings returns the first column of every row.
func queryStrings(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}
