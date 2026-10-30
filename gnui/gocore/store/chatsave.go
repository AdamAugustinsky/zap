package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// SaveWithChat saves messages of one chat and changes the chat's row in
// one transaction, so that no message is filed without its chat having
// seen it (a chat list missing it, or sorted by an older activity). update
// gets the chat, a new one when it has no row yet, and the messages as
// stored; a message the archive refused (deleted here, or older than a
// clear) is not among them.
func (s *Archive) SaveWithChat(chat string, messages []Message, update func(c *Chat, stored []Message)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if chat == "" {
		return errors.New("chat id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stored := make([]Message, 0, len(messages))
	for _, m := range messages {
		if err = save(tx, m); err != nil {
			return err
		}
		got, err := scanMessage(tx.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND id=?", m.ChatID, m.ID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		stored = append(stored, got)
	}
	var b []byte
	c := Chat{ID: chat}
	err = tx.QueryRow("SELECT payload FROM chats WHERE id=?", chat).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	update(&c, stored)
	if err = putChat(tx, chat, c); err != nil {
		return err
	}
	return tx.Commit()
}

// putChat writes chat id's row and its visibility.
func putChat(tx *sql.Tx, id string, c Chat) error {
	data, err := encode(c)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO chats(id,activity,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET activity=excluded.activity,payload=excluded.payload", id, c.LastActivity, data); err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO chat_visibility(chat,locked) VALUES(?,?) ON CONFLICT(chat) DO UPDATE SET locked=excluded.locked", id, c.Locked)
	return err
}
