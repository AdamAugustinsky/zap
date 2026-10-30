// From zapfast-go 3104c46 (internal/archive/receipt_times.go), MIT, on Zap's Archive.

package store

import (
	"database/sql"
	"errors"
)

// ReceiptAt persists the independently observed stage and never expands the
// original group audience. Older/replayed observations keep the earliest time.
func (s *Archive) ReceiptAt(chat, id, sender string, status int, timestamp int64, group bool) (int, bool, error) {
	if status < 0 || status > 3 || timestamp < 0 || sender == "" {
		return 0, false, errors.New("invalid receipt observation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	// A group message of ours whose audience was never saved (sent from
	// the phone while the members were unknown) keeps each receipt for its
	// info, but no partial set of readers can move its ticks
	// (zf:src/archive/receipts.rs:12).
	blind := false
	if group {
		var own, audience int
		if err = tx.QueryRow("SELECT COALESCE((SELECT from_me FROM message_flags WHERE chat=? AND id=?),0),(SELECT count(*) FROM audiences WHERE chat=? AND id=?)", chat, id, chat, id).Scan(&own, &audience); err != nil {
			return 0, false, err
		}
		if own == 1 && audience == 0 {
			blind = true
			if _, err = tx.Exec("INSERT OR IGNORE INTO receipts(chat,id,sender,status) VALUES(?,?,?,0)", chat, id, sender); err != nil {
				return 0, false, err
			}
		}
	}
	if !group {
		var own bool
		err = tx.QueryRow("SELECT from_me FROM message_flags WHERE chat=? AND id=?", chat, id).Scan(&own)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !own {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO receipts(chat,id,sender,status) VALUES(?,?,?,0)", chat, id, sender); err != nil {
			return 0, false, err
		}
	}
	r, err := tx.Exec("UPDATE receipts SET status=MAX(status,?) WHERE chat=? AND id=? AND sender=?", status, chat, id, sender)
	if err != nil {
		return 0, false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n == 0 {
		return 0, false, err
	}
	observation := MessageReceipt{Sender: sender}
	if timestamp > 0 {
		switch status {
		case 1:
			observation.DeliveredAt = timestamp
		case 2:
			observation.ReadAt = timestamp
		case 3:
			observation.PlayedAt = timestamp
		}
		if err = storeReceiptTimes(tx, chat, id, observation); err != nil {
			return 0, false, err
		}
	}
	level := status
	if blind {
		return 0, true, tx.Commit()
	}
	if group {
		err = tx.QueryRow("SELECT MIN(status) FROM receipts WHERE chat=? AND id=?", chat, id).Scan(&level)
	} else {
		err = tx.QueryRow("SELECT status FROM receipts WHERE chat=? AND id=? AND sender=?", chat, id, sender).Scan(&level)
	}
	if err != nil {
		return 0, false, err
	}
	return level, true, tx.Commit()
}

func storeReceiptTimes(tx *sql.Tx, chat, id string, r MessageReceipt) error {
	_, err := tx.Exec(`INSERT INTO receipt_times(chat,id,sender,delivered,read_at,played) VALUES(?,?,?,?,?,?)
ON CONFLICT(chat,id,sender) DO UPDATE SET
delivered=CASE WHEN delivered=0 THEN excluded.delivered WHEN excluded.delivered=0 THEN delivered ELSE MIN(delivered,excluded.delivered) END,
read_at=CASE WHEN read_at=0 THEN excluded.read_at WHEN excluded.read_at=0 THEN read_at ELSE MIN(read_at,excluded.read_at) END,
played=CASE WHEN played=0 THEN excluded.played WHEN excluded.played=0 THEN played ELSE MIN(played,excluded.played) END`, chat, id, r.Sender, r.DeliveredAt, r.ReadAt, r.PlayedAt)
	return err
}

// RestoreReceiptTimes is for an already imported original audience only.
func (s *Archive) RestoreReceiptTimes(chat, id string, r MessageReceipt) error {
	if r.Sender == "" || r.DeliveredAt < 0 || r.ReadAt < 0 || r.PlayedAt < 0 {
		return errors.New("invalid receipt times")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM receipts WHERE chat=? AND id=? AND sender=?", chat, id, r.Sender).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if err = storeReceiptTimes(tx, chat, id, r); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Archive) ReceiptDetails(chat, id string) ([]MessageReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT r.sender,COALESCE(t.delivered,0),COALESCE(t.read_at,0),COALESCE(t.played,0)
FROM receipts r LEFT JOIN receipt_times t ON r.chat=t.chat AND r.id=t.id AND r.sender=t.sender
WHERE r.chat=? AND r.id=? ORDER BY r.sender`, chat, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MessageReceipt
	for rows.Next() {
		var r MessageReceipt
		if err = rows.Scan(&r.Sender, &r.DeliveredAt, &r.ReadAt, &r.PlayedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func moveReceiptTimes(tx *sql.Tx, from, to string) error {
	for _, column := range []string{"chat", "sender"} {
		rows, err := tx.Query("SELECT chat,id,sender,delivered,read_at,played FROM receipt_times WHERE "+column+"=?", from)
		if err != nil {
			return err
		}
		type entry struct {
			chat, id string
			receipt  MessageReceipt
		}
		var entries []entry
		for rows.Next() {
			var r entry
			if err = rows.Scan(&r.chat, &r.id, &r.receipt.Sender, &r.receipt.DeliveredAt, &r.receipt.ReadAt, &r.receipt.PlayedAt); err != nil {
				rows.Close()
				return err
			}
			if column == "chat" {
				r.chat = to
			} else {
				r.receipt.Sender = to
			}
			entries = append(entries, r)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, r := range entries {
			if err = storeReceiptTimes(tx, r.chat, r.id, r.receipt); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("DELETE FROM receipt_times WHERE "+column+"=?", from); err != nil {
			return err
		}
	}
	return nil
}

// OursUnreadThrough lists our messages in chat sent up to a time (Unix
// seconds) that went out and were not yet read: a read receipt for a later
// one reads them too (zf:src/archive.rs:2300).
func (s *Archive) OursUnreadThrough(chat string, through int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT f.id FROM message_flags f JOIN messages m ON m.chat=f.chat AND m.id=f.id
WHERE f.chat=? AND f.from_me=1 AND f.status IN ('sent','delivered') AND m.ts<=?`, chat, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
