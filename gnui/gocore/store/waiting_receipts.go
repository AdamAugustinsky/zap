package store

import (
	"database/sql"
	"errors"
)

// Receipts that name a message not archived yet: our message sent from
// the phone reaches us after its recipients' receipts often enough. They
// wait here, on disk, for the message (zf:src/archive/receipts.rs:58) and
// are dropped after WaitingMaxAge, as the Rust archive did
// (zf:src/archive/receipts.rs:229), or beyond WaitingMax rows, oldest
// first.
const (
	WaitingMaxAge = 24 * 60 * 60 // seconds
	WaitingMax    = 4096
)

// addedSchema holds the tables added after version 5. They are additive,
// so they are made on every open rather than by a version step.
const addedSchema = `
CREATE TABLE IF NOT EXISTS waiting_receipts(chat TEXT NOT NULL,id TEXT NOT NULL,sender TEXT NOT NULL,status INTEGER NOT NULL DEFAULT 0,delivered INTEGER NOT NULL DEFAULT 0,read_at INTEGER NOT NULL DEFAULT 0,played INTEGER NOT NULL DEFAULT 0,queued INTEGER NOT NULL,PRIMARY KEY(chat,id,sender));
CREATE INDEX IF NOT EXISTS waiting_receipts_age ON waiting_receipts(queued);`

// addTables makes the added tables and, as the Rust archive did on open
// (zf:src/archive.rs:494), drops receipts that waited too long.
func addTables(db *sql.DB) error {
	if _, err := db.Exec(addedSchema); err != nil {
		return err
	}
	_, err := db.Exec("DELETE FROM waiting_receipts WHERE queued<=?", Now()-WaitingMaxAge)
	return err
}

// WaitingReceipt is one recipient's receipts for a message not archived
// yet: the furthest status (1 delivered, 2 read, 3 played) and the first
// time each stage was seen, 0 when unknown.
type WaitingReceipt struct {
	Sender                      string
	Status                      int
	DeliveredAt, ReadAt, Played int64
}

const mergeWaiting = ` ON CONFLICT(chat,id,sender) DO UPDATE SET
status=MAX(status,excluded.status),
delivered=CASE WHEN delivered=0 THEN excluded.delivered WHEN excluded.delivered=0 THEN delivered ELSE MIN(delivered,excluded.delivered) END,
read_at=CASE WHEN read_at=0 THEN excluded.read_at WHEN excluded.read_at=0 THEN read_at ELSE MIN(read_at,excluded.read_at) END,
played=CASE WHEN played=0 THEN excluded.played WHEN excluded.played=0 THEN played ELSE MIN(played,excluded.played) END,
queued=MIN(queued,excluded.queued)`

// WaitReceipt keeps a receipt (status 1–3, at in Unix seconds) for a
// message not archived yet, and drops what waited too long. now is Unix
// seconds.
func (s *Archive) WaitReceipt(chat, id, sender string, status int, at, now int64) error {
	if status < 1 || status > 3 || at < 0 || chat == "" || id == "" || sender == "" {
		return errors.New("invalid waiting receipt")
	}
	times := [4]int64{}
	times[status] = at
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO waiting_receipts(chat,id,sender,status,delivered,read_at,played,queued) VALUES(?,?,?,?,?,?,?,?)"+mergeWaiting,
		chat, id, sender, status, times[1], times[2], times[3], now); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM waiting_receipts WHERE queued<=?", now-WaitingMaxAge); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM waiting_receipts WHERE rowid IN (SELECT rowid FROM waiting_receipts ORDER BY queued DESC,rowid DESC LIMIT -1 OFFSET ?)", WaitingMax); err != nil {
		return err
	}
	return tx.Commit()
}

// TakeWaitingReceipts returns the receipts that waited for a message and
// forgets them.
func (s *Archive) TakeWaitingReceipts(chat, id string) ([]WaitingReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT sender,status,delivered,read_at,played FROM waiting_receipts WHERE chat=? AND id=? ORDER BY sender", chat, id)
	if err != nil {
		return nil, err
	}
	var result []WaitingReceipt
	for rows.Next() {
		var r WaitingReceipt
		if err = rows.Scan(&r.Sender, &r.Status, &r.DeliveredAt, &r.ReadAt, &r.Played); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, r)
	}
	if err = rows.Close(); err != nil || len(result) == 0 {
		return nil, err
	}
	_, err = s.db.Exec("DELETE FROM waiting_receipts WHERE chat=? AND id=?", chat, id)
	return result, err
}

// WaitingFiled lists the messages archived in chat that receipts still
// wait for: ones filed past the usual path, or under an id mapped since
// (zf:src/archive/receipts.rs:192).
func (s *Archive) WaitingFiled(chat string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryStrings(s.db, "SELECT DISTINCT w.id FROM waiting_receipts w JOIN messages m ON m.chat=w.chat AND m.id=w.id WHERE w.chat=? ORDER BY w.id", chat)
}

// moveWaitingReceipts files receipts waiting under a privacy id under the
// id it maps to, as chat and as recipient (zf:src/archive/receipts.rs:242).
func moveWaitingReceipts(tx *sql.Tx, from, to string) error {
	for _, column := range []string{"chat", "sender"} {
		chat, sender := "chat", "sender"
		if column == "chat" {
			chat = "?"
		} else {
			sender = "?"
		}
		if _, err := tx.Exec("INSERT INTO waiting_receipts(chat,id,sender,status,delivered,read_at,played,queued) SELECT "+chat+",id,"+sender+",status,delivered,read_at,played,queued FROM waiting_receipts WHERE "+column+"=?"+mergeWaiting, to, from); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM waiting_receipts WHERE "+column+"=?", from); err != nil {
			return err
		}
	}
	return nil
}
