// From zapfast-go 3104c46 (internal/archive/reactions.go), MIT, on Zap's Archive.

package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
)

// ApplyReaction retains the latest observation even after removal, so delayed
// history cannot restore a reactor who has already removed their reaction.
// The ordering marker and visible payload share one encrypted transaction.
func (s *Archive) ApplyReaction(chat, id string, reaction Reaction, timestampMS int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var data []byte
	if err = tx.QueryRow("SELECT payload FROM messages WHERE chat=? AND id=?", chat, id).Scan(&data); err != nil {
		return false, err
	}
	var target Message
	if err = json.Unmarshal(data, &target); err != nil {
		return false, err
	}
	if target.Deleted {
		return false, nil
	}
	hash := sha256.Sum256([]byte(chat + "\x00" + id + "\x00" + reaction.Sender))
	key := "reaction-time:" + hex.EncodeToString(hash[:])
	var previous []byte
	err = tx.QueryRow("SELECT value FROM metadata WHERE key=?", key).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if len(previous) > 0 {
		at, err := strconv.ParseInt(string(previous), 10, 64)
		if err != nil {
			return false, errors.New("invalid archived reaction timestamp")
		}
		if timestampMS <= at {
			return false, nil
		}
	}
	out := make([]Reaction, 0, len(target.Reactions)+1)
	for _, existing := range target.Reactions {
		if existing.Sender == reaction.Sender {
			if existing.Timestamp > timestampMS/1000 {
				return false, nil
			}
			continue
		}
		out = append(out, existing)
	}
	if reaction.Emoji != "" {
		out = append(out, reaction)
	}
	target.Reactions = out
	data, err = json.Marshal(target)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec("UPDATE messages SET payload=? WHERE chat=? AND id=?", data, chat, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, []byte(strconv.FormatInt(timestampMS, 10))); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
