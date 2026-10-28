package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Local chat labels follow src/archive/labels.rs: a name, a colour and the
// chats that wear them, kept on this computer only (not WhatsApp Business
// labels, nothing syncs). A chat's payload carries its label ids too
// (Chat.Labels), kept in step here.

const (
	// LabelLimit is how many labels are kept.
	LabelLimit = 20
	// LabelNameLimit is the longest name kept, in characters.
	LabelNameLimit = 32
	// DefaultLabelColor is used when a colour is not a hex colour.
	DefaultLabelColor = "#3b82f6"
)

// Labels lists the labels in the order they were created.
func (s *Archive) Labels() ([]Label, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return labels(s.db)
}

func labels(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]Label, error) {
	rows, err := q.Query("SELECT id,name,color,created_at FROM local_labels ORDER BY created_at,rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Label{}
	for rows.Next() {
		var l Label
		if err = rows.Scan(&l.ID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

// CreateLabel adds a label created at createdAt (the caller's clock), which
// its id derives from. ok is false when the name is empty or taken
// (ignoring ASCII case) or LabelLimit is reached.
func (s *Archive) CreateLabel(name, color string, createdAt int64) (l Label, ok bool, err error) {
	name = cleanLabelName(name)
	if name == "" {
		return Label{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Label{}, false, err
	}
	defer tx.Rollback()
	var count, taken int
	if err = tx.QueryRow("SELECT count(*),count(CASE WHEN lower(name)=lower(?) THEN 1 END) FROM local_labels", name).Scan(&count, &taken); err != nil {
		return Label{}, false, err
	}
	if count >= LabelLimit || taken > 0 {
		return Label{}, false, nil
	}
	l = Label{Name: name, Color: CleanLabelColor(color), CreatedAt: createdAt}
	for suffix := 0; ; suffix++ {
		l.ID = fmt.Sprintf("label-%d", createdAt)
		if suffix > 0 {
			l.ID = fmt.Sprintf("label-%d-%d", createdAt, suffix)
		}
		var used int
		if err = tx.QueryRow("SELECT count(*) FROM local_labels WHERE id=?", l.ID).Scan(&used); err != nil {
			return Label{}, false, err
		}
		if used == 0 {
			break
		}
	}
	if _, err = tx.Exec("INSERT INTO local_labels(id,name,color,created_at) VALUES(?,?,?,?)", l.ID, l.Name, l.Color, l.CreatedAt); err != nil {
		return Label{}, false, err
	}
	return l, true, tx.Commit()
}

// UpdateLabel renames and recolours a label; false when the name is empty
// or taken, or there is no such label.
func (s *Archive) UpdateLabel(id, name, color string) (bool, error) {
	name = cleanLabelName(name)
	if name == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var taken int
	if err := s.db.QueryRow("SELECT count(*) FROM local_labels WHERE lower(name)=lower(?) AND id<>?", name, id).Scan(&taken); err != nil || taken > 0 {
		return false, err
	}
	r, err := s.db.Exec("UPDATE local_labels SET name=?,color=? WHERE id=?", name, CleanLabelColor(color), id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// DeleteLabel deletes a label and takes it off every chat that wore it.
func (s *Archive) DeleteLabel(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	chats, err := queryStrings(tx, "SELECT chat FROM local_chat_labels WHERE label=?", id)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec("DELETE FROM local_chat_labels WHERE label=?", id); err != nil {
		return false, err
	}
	r, err := tx.Exec("DELETE FROM local_labels WHERE id=?", id)
	if err != nil {
		return false, err
	}
	for _, chat := range chats {
		if err = syncChatLabels(tx, chat); err != nil {
			return false, err
		}
	}
	n, err := r.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// SetChatLabels replaces the labels of one chat. Ids of labels that no
// longer exist are skipped, so a menu drawn just before a delete cannot
// bring a label back.
func (s *Archive) SetChatLabels(chat string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM local_chat_labels WHERE chat=?", chat); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec("INSERT OR IGNORE INTO local_chat_labels(chat,label) SELECT ?,id FROM local_labels WHERE id=?", chat, id); err != nil {
			return err
		}
	}
	if err = syncChatLabels(tx, chat); err != nil {
		return err
	}
	return tx.Commit()
}

// ChatLabels are the label ids a chat wears, in creation order.
func (s *Archive) ChatLabels(chat string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return chatLabels(s.db, chat)
}

func chatLabels(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, chat string) ([]string, error) {
	return queryStrings(q, `SELECT c.label FROM local_chat_labels c JOIN local_labels l ON l.id=c.label
WHERE c.chat=? ORDER BY l.created_at,l.rowid`, chat)
}

// syncChatLabels copies a chat's labels into its payload, when the archive
// holds the chat.
func syncChatLabels(tx *sql.Tx, chat string) error {
	var data []byte
	err := tx.QueryRow("SELECT payload FROM chats WHERE id=?", chat).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var c Chat
	if err = json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Labels, err = chatLabels(tx, chat); err != nil {
		return err
	}
	if data, err = encode(c); err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE chats SET payload=? WHERE id=?", data, chat)
	return err
}

// cleanLabelName trims a name and cuts it to LabelNameLimit characters.
func cleanLabelName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) <= LabelNameLimit {
		return name
	}
	return string([]rune(name)[:LabelNameLimit])
}

// CleanLabelColor normalises #rgb, #rrggbb, rgb and rrggbb to #rrggbb, and
// anything else to DefaultLabelColor, so a stray paste cannot make a label
// invisible.
func CleanLabelColor(color string) string {
	raw := strings.TrimPrefix(strings.TrimSpace(color), "#")
	switch len(raw) {
	case 3:
		raw = string([]byte{raw[0], raw[0], raw[1], raw[1], raw[2], raw[2]})
	case 6:
	default:
		return DefaultLabelColor
	}
	for i := 0; i < len(raw); i++ {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(raw[i])) {
			return DefaultLabelColor
		}
	}
	return "#" + strings.ToLower(raw)
}

// PutLabel files a label the phone keeps (a WhatsApp label from app
// state) under its own id: added, or renamed and recoloured in place. The
// name is cleaned as CreateLabel's; an empty one is refused (false). Labels
// from the phone are not counted against LabelLimit, which bounds the ones
// made here.
func (s *Archive) PutLabel(l Label) (bool, error) {
	l.Name = cleanLabelName(l.Name)
	if l.Name == "" || l.ID == "" {
		return false, nil
	}
	l.Color = CleanLabelColor(l.Color)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.db.Exec(`INSERT INTO local_labels(id,name,color,created_at) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,color=excluded.color
WHERE local_labels.name<>excluded.name OR local_labels.color<>excluded.color`, l.ID, l.Name, l.Color, l.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// LabelChat puts one label on a chat or takes it off, keeping the others;
// a label that does not exist is not put on. It reports whether the chat's
// labels changed.
func (s *Archive) LabelChat(chat, id string, on bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var r sql.Result
	if on {
		r, err = tx.Exec("INSERT OR IGNORE INTO local_chat_labels(chat,label) SELECT ?,id FROM local_labels WHERE id=?", chat, id)
	} else {
		r, err = tx.Exec("DELETE FROM local_chat_labels WHERE chat=? AND label=?", chat, id)
	}
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if err = syncChatLabels(tx, chat); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
