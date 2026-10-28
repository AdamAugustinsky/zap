package store

import "strings"

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
