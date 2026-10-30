package store

import (
	"errors"
	"os"
	"strings"
)

// Clear empties the archive for an account that logged out, as the Rust
// archive's clear does: every chat, message, star, pin, label, draft, vote,
// queued action and setting goes. The schema and the encryption key stay.
func (s *Archive) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tables, err := queryStrings(s.db, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tables {
		if _, err = tx.Exec(`DELETE FROM "` + strings.ReplaceAll(t, `"`, `""`) + `"`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Stickers lists the newest sticker messages (Kind "sticker") that are
// ours (fromMe) or others', at most limit, newest first, leaving out
// locked chats and stickers deleted for everyone: the picker's Recent and
// Received shelves.
func (s *Archive) Stickers(fromMe bool, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.messages(`SELECT m.payload,m.raw FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
LEFT JOIN chat_visibility v ON v.chat=m.chat
WHERE f.kind='sticker' AND f.from_me=? AND COALESCE(v.locked,0)=0
ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?`, fromMe, clamp(limit))
	if err != nil {
		return nil, err
	}
	kept := list[:0]
	for _, m := range list {
		if !m.Deleted {
			kept = append(kept, m)
		}
	}
	return kept, nil
}

// ForgetMissingMedia takes the path off every attachment and motion clip
// whose file is gone (the media cache was emptied), so it downloads again
// rather than draw broken. It reports how many rows changed.
func (s *Archive) ForgetMissingMedia() (int, error) {
	s.mu.Lock()
	rows, err := s.db.Query(`SELECT chat,id FROM messages WHERE payload LIKE '%"path":%'`)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	var keys [][2]string
	for rows.Next() {
		var k [2]string
		if err = rows.Scan(&k[0], &k[1]); err != nil {
			break
		}
		keys = append(keys, k)
	}
	rows.Close()
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	gone := func(p string) bool {
		if p == "" {
			return false
		}
		_, err := os.Stat(p)
		return errors.Is(err, os.ErrNotExist)
	}
	n := 0
	for _, k := range keys {
		m, err := s.Message(k[0], k[1])
		if err != nil {
			continue
		}
		missing := m.Media != nil && gone(m.Media.Path) || m.Motion != nil && gone(m.Motion.Path)
		for _, c := range m.Cards {
			missing = missing || c.Media != nil && gone(c.Media.Path)
		}
		if !missing {
			continue
		}
		if err := s.UpdateMessage(k[0], k[1], func(m *Message) {
			if m.Media != nil && gone(m.Media.Path) {
				m.Media.Path = ""
			}
			if m.Motion != nil && gone(m.Motion.Path) {
				m.Motion.Path = ""
			}
			for i := range m.Cards {
				if c := m.Cards[i].Media; c != nil && gone(c.Path) {
					c.Path = ""
				}
			}
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
