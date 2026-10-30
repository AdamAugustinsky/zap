// From zapfast-go 3104c46 (internal/archive/store.go), MIT. Changed for Zap:
// SQLCipher through the purego driver (package sqlite) instead of cgo, the
// type renamed Archive, and schema version 5 (schema.go): message kinds,
// mentions, links, stars with their time, message pins and local labels.

package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"zapfast/gocore/store/sqlite"
)

// ErrKey is returned for a key that is not 32 bytes.
var ErrKey = errors.New("archive key must contain 32 bytes")

// Archive keeps one account's complete archive in SQLCipher. Message bodies,
// raw protobufs, search tokens, drafts and database journals are all
// encrypted and authenticated. It never falls back to ordinary SQLite.
type Archive struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenDatabase opens an encrypted database for one owner: the archive, and
// the whatsmeow store in its own file. The caller owns the returned handle.
// The folder is made 0700 and the file 0600; a symlink is refused. One
// connection avoids concurrent key/PRAGMA state and keeps memory bounded.
// SQLCipher's page HMAC stays enabled. A wrong key fails closed with
// sqlite.ErrWrongKey, a missing library with a *sqlite.LibraryError.
func OpenDatabase(path string, key []byte) (*sql.DB, error) {
	return openDatabase(path, key, 1)
}

func openDatabase(path string, key []byte, conns int) (*sql.DB, error) {
	if len(key) != 32 {
		return nil, ErrKey
	}
	if path == "" || path == ":memory:" {
		return nil, errors.New("an encrypted database requires a file path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing database symlink")
	}
	db, err := sqlite.Open(sqlite.Config{
		Path:        path,
		Key:         key,
		BusyTimeout: 5 * time.Second,
		Pragmas: []string{
			"PRAGMA journal_mode = WAL",
			"PRAGMA synchronous = FULL",
			"PRAGMA cache_size = -4096",
			"PRAGMA secure_delete = ON",
		},
	})
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenArchive opens (creating) the archive at path with its 32-byte key and
// brings its schema up to date.
func OpenArchive(path string, key []byte) (*Archive, error) {
	db, err := OpenDatabase(path, key)
	if err != nil {
		return nil, err
	}
	s := &Archive{db: db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Archive) Close() error    { s.mu.Lock(); defer s.mu.Unlock(); return s.db.Close() }
func encode(v any) ([]byte, error) { return json.Marshal(v) }

func (s *Archive) UpsertChat(c Chat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		return errors.New("chat id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = putChat(tx, c.ID, c); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Archive) Chats() ([]Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT payload FROM chats ORDER BY activity DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := make([]Chat, 0)
	for rows.Next() {
		var b []byte
		var c Chat
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}
func (s *Archive) Chat(id string) (Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	var c Chat
	err := s.db.QueryRow("SELECT payload FROM chats WHERE id=?", id).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}
func (s *Archive) UpdateChat(id string, update func(*Chat)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	var c Chat
	err := s.db.QueryRow("SELECT payload FROM chats WHERE id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		c = Chat{ID: id}
	} else if err != nil {
		return err
	} else if err = json.Unmarshal(b, &c); err != nil {
		return err
	}
	update(&c)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = putChat(tx, id, c); err != nil {
		return err
	}
	return tx.Commit()
}

func tokenize(text string) []string {
	text = Fold(text)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	seen := make(map[string]struct{}, len(words))
	result := make([]string, 0, len(words))
	for _, word := range words {
		if len(word) > 256 {
			continue
		}
		if _, ok := seen[word]; !ok {
			seen[word] = struct{}{}
			result = append(result, word)
		}
	}
	return result
}

// Fold gives chat names and encrypted search tokens the same accent-insensitive
// Unicode treatment. The encrypted database stores neither plaintext bodies nor
// unencrypted search indexes.
func Fold(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, cases.Fold().String(norm.NFKD.String(text)))
}

// liveInPlace reports whether old is a row filed live, which has no order
// id, that m, history's copy of it, would only move among the messages of
// its second, where the reader has seen it already (Zap; found by the
// simulation, gocore/wa's sim_test.go).
func liveInPlace(old, m Message) bool { return old.Order == 0 && m.Timestamp == old.Timestamp }

func save(tx *sql.Tx, m Message) error {
	if m.ID == "" || m.ChatID == "" {
		return errors.New("message and chat ids are required")
	}
	var blocked int
	if err := tx.QueryRow("SELECT count(*) FROM tombstones WHERE chat=? AND id=?", m.ChatID, m.ID).Scan(&blocked); err != nil {
		return err
	}
	if blocked != 0 {
		return nil
	}
	var barrier int64
	err := tx.QueryRow("SELECT ts FROM barriers WHERE chat=?", m.ChatID).Scan(&barrier)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && m.Timestamp <= barrier {
		return nil
	}
	var oldData, oldRaw []byte
	err = tx.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND id=?", m.ChatID, m.ID).Scan(&oldData, &oldRaw)
	if err == nil {
		var old Message
		if err = json.Unmarshal(oldData, &old); err != nil {
			return err
		}
		// Phone replay cannot restore an original over an edit or a revocation.
		if old.Deleted {
			return nil
		}
		if old.Edited && !m.Edited {
			// A historical original can supply its original placement after an
			// edit arrived first, without restoring the old content.
			original := m
			m = old
			m.Raw = oldRaw
			if original.Timestamp != 0 {
				m.Timestamp = original.Timestamp
			}
			if original.Order != 0 && !liveInPlace(old, original) {
				m.Order = original.Order
			}
		} else if m.Edited {
			// The edit-envelope timestamp/order describes the edit operation,
			// never the position of the message it modifies.
			m.Timestamp, m.Order = old.Timestamp, old.Order
		} else if liveInPlace(old, m) {
			m.Order = 0
		}
		if m.Order == 0 {
			m.Order = old.Order
		}
		if m.Timestamp == 0 {
			m.Timestamp = old.Timestamp
		}
		m.Starred = old.Starred || m.Starred
		if m.LinkPreview == nil && m.Text == old.Text {
			m.LinkPreview = old.LinkPreview
		}
		if old.PollHistoryComplete {
			m.PollHistoryComplete = true
		}
		if len(m.Poll) > 0 && len(old.Poll) == len(m.Poll) {
			m.Poll = old.Poll
		}
		rank := map[string]int{"pending": 0, "sent": 1, "delivered": 2, "read": 3, "played": 4}
		if rank[old.Status] > rank[m.Status] {
			m.Status = old.Status
		}
		if m.Media != nil && old.Media != nil && m.Media.Path == "" {
			m.Media.Path = old.Media.Path
		}
		if len(m.Reactions) == 0 {
			m.Reactions = old.Reactions
		}
		if m.Motion == nil {
			m.Motion = old.Motion
		}
		if len(m.Raw) == 0 {
			m.Raw = oldRaw
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Zap: the stars table, which keeps when a star happened, decides; a
	// message that arrives starred without a row there gets one, at its
	// own time and as a replay, so a later unstar wins.
	if err = starFromTable(tx, &m); err != nil {
		return err
	}
	data, err := encode(m)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO messages(chat,id,ts,ord,payload,raw) VALUES(?,?,?,?,?,?) ON CONFLICT(chat,id) DO UPDATE SET ts=excluded.ts,ord=excluded.ord,payload=excluded.payload,raw=excluded.raw", m.ChatID, m.ID, m.Timestamp, m.Order, data, m.Raw); err != nil {
		return err
	}
	return index(tx, m)
}

// index writes a message's derived rows: its sender, its flags and kind,
// its search tokens, and (Zap) its mentions and links.
func index(tx *sql.Tx, m Message) error {
	if _, err := tx.Exec("INSERT INTO message_identities(chat,id,sender) VALUES(?,?,?) ON CONFLICT(chat,id) DO UPDATE SET sender=excluded.sender", m.ChatID, m.ID, m.Sender); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO message_flags(chat,id,starred,from_me,status,kind) VALUES(?,?,?,?,?,?) ON CONFLICT(chat,id) DO UPDATE SET starred=excluded.starred,from_me=excluded.from_me,status=excluded.status,kind=excluded.kind", m.ChatID, m.ID, m.Starred, m.FromMe, m.Status, indexedKind(m)); err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM tokens WHERE chat=? AND id=?", "DELETE FROM message_mentions WHERE chat=? AND id=?", "DELETE FROM message_links WHERE chat=? AND id=?"} {
		if _, err := tx.Exec(q, m.ChatID, m.ID); err != nil {
			return err
		}
	}
	if m.Deleted {
		return nil
	}
	for _, word := range tokenize(m.Text) {
		if _, err := tx.Exec("INSERT OR IGNORE INTO tokens(token,chat,id) VALUES(?,?,?)", word, m.ChatID, m.ID); err != nil {
			return err
		}
	}
	for _, mention := range m.Mentions {
		if mention.User == "" && mention.ID == "" {
			continue
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO message_mentions(chat,id,jid,user) VALUES(?,?,?,?)", m.ChatID, m.ID, mention.ID, mention.User); err != nil {
			return err
		}
	}
	for _, link := range messageLinks(m) {
		if _, err := tx.Exec("INSERT OR IGNORE INTO message_links(chat,id,url,title,ts,ord) VALUES(?,?,?,?,?,?)", m.ChatID, m.ID, link.URL, link.Title, m.Timestamp, m.Order); err != nil {
			return err
		}
	}
	return nil
}
func (s *Archive) SaveMessages(messages []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range messages {
		if err = save(tx, m); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var b, raw []byte
	err := row.Scan(&b, &raw)
	if err == nil {
		err = json.Unmarshal(b, &m)
		m.Raw = raw
	}
	return m, err
}
func (s *Archive) Message(chat, id string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return scanMessage(s.db.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND id=?", chat, id))
}

// OfKind returns the messages of a kind sent since a time (Unix seconds),
// with their protobufs, for a newer parser or the phone to look at again.
func (s *Archive) OfKind(kind string, since int64) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT payload,raw FROM messages WHERE ts>=? AND payload LIKE ? ORDER BY ts,ord`, since, `%"kind":"`+kind+`"%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if m.Kind == kind {
			result = append(result, m)
		}
	}
	return result, rows.Err()
}

func clamp(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}
func (s *Archive) Messages(chat, before string, limit int) ([]Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit = clamp(limit)
	query := "SELECT payload,NULL FROM messages WHERE chat=?"
	args := []any{chat}
	if before != "" {
		var ts, ord int64
		if err := s.db.QueryRow("SELECT ts,ord FROM messages WHERE chat=? AND id=?", chat, before).Scan(&ts, &ord); err != nil {
			return nil, false, err
		}
		query += " AND (ts<? OR (ts=? AND ord<?) OR (ts=? AND ord=? AND id<?))"
		args = append(args, ts, ts, ord, ts, ord, before)
	}
	query += " ORDER BY ts DESC,ord DESC,id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Message, 0, limit+1)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, m)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	hasOlder := len(result) > limit
	if hasOlder {
		result = result[:limit]
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, hasOlder, nil
}
func (s *Archive) Search(query, chat string, limit int) ([]SearchHit, error) {
	hits, _, err := s.SearchRange(query, chat, 0, 0, false, limit)
	return hits, err
}

// SearchRange uses half-open Unix-second date bounds. Locked chats are always
// excluded, including in-chat queries and starred/calendar-only searches.
func (s *Archive) SearchRange(query, chat string, start, end int64, starred bool, limit int) ([]SearchHit, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	words := tokenize(query)
	if len(words) == 0 && start == 0 && end == 0 && !starred {
		return nil, false, nil
	}
	if end > 0 && start >= end {
		return nil, false, errors.New("invalid search date range")
	}
	if len(words) > 16 {
		return nil, false, errors.New("search supports at most 16 words")
	}
	q := "SELECT m.payload,NULL,c.payload FROM messages m LEFT JOIN chats c ON c.id=m.chat LEFT JOIN chat_visibility visibility ON visibility.chat=m.chat LEFT JOIN message_flags flags ON flags.chat=m.chat AND flags.id=m.id WHERE COALESCE(visibility.locked,0)=0"
	args := []any{}
	if len(words) > 0 {
		q = "SELECT m.payload,NULL,c.payload FROM (SELECT DISTINCT chat,id FROM tokens WHERE token>=? AND token<?) candidate JOIN messages m ON m.chat=candidate.chat AND m.id=candidate.id LEFT JOIN chats c ON c.id=m.chat LEFT JOIN chat_visibility visibility ON visibility.chat=m.chat LEFT JOIN message_flags flags ON flags.chat=m.chat AND flags.id=m.id WHERE COALESCE(visibility.locked,0)=0"
		args = append(args, words[0], words[0]+"\U0010ffff")
		for _, word := range words[1:] {
			q += " AND EXISTS(SELECT 1 FROM tokens t WHERE t.token>=? AND t.token<? AND t.chat=m.chat AND t.id=m.id)"
			args = append(args, word, word+"\U0010ffff")
		}
	}
	if chat != "" {
		q += " AND m.chat=?"
		args = append(args, chat)
	}
	if start > 0 {
		q += " AND m.ts>=?"
		args = append(args, start)
	}
	if end > 0 {
		q += " AND m.ts<?"
		args = append(args, end)
	}
	if starred {
		q += " AND flags.starred=1"
	}
	q += " ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?"
	limit = clamp(limit)
	args = append(args, limit+1)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []SearchHit{}
	for rows.Next() {
		var b, raw, c []byte
		var m Message
		var name Chat
		if err = rows.Scan(&b, &raw, &c); err != nil {
			return nil, false, err
		}
		if err = json.Unmarshal(b, &m); err != nil {
			return nil, false, err
		}
		m.Raw = raw
		if len(c) > 0 {
			if err = json.Unmarshal(c, &name); err != nil {
				return nil, false, err
			}
		}
		result = append(result, SearchHit{ChatID: m.ChatID, ChatName: name.Name, Message: m})
	}
	truncated := len(result) > limit
	if truncated {
		result = result[:limit]
	}
	return result, truncated, rows.Err()
}

// UpdateMessage changes derived state without history merge rules, atomically.
func (s *Archive) UpdateMessage(chat, id string, update func(*Message)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := scanMessage(tx.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND id=?", chat, id))
	if err != nil {
		return err
	}
	starred := m.Starred
	update(&m)
	if m.Starred != starred {
		// Zap: a star changed here is a live event, timed now.
		if _, err = setStar(tx, chat, id, m.Starred, time.Now().Unix(), false); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM messages WHERE chat=? AND id=?", chat, id); err != nil {
		return err
	}
	if err = save(tx, m); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Archive) DeleteLocal(chat, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"INSERT OR IGNORE INTO tombstones(chat,id) VALUES(?,?)", "DELETE FROM tokens WHERE chat=? AND id=?", "DELETE FROM messages WHERE chat=? AND id=?", "DELETE FROM receipt_times WHERE chat=? AND id=?", "DELETE FROM receipts WHERE chat=? AND id=?", "DELETE FROM audiences WHERE chat=? AND id=?", "DELETE FROM message_flags WHERE chat=? AND id=?", "DELETE FROM votes WHERE chat=? AND parent=?", "DELETE FROM message_identities WHERE chat=? AND id=?", "DELETE FROM message_mentions WHERE chat=? AND id=?", "DELETE FROM message_links WHERE chat=? AND id=?", "DELETE FROM message_pins WHERE chat=? AND id=?", "DELETE FROM stars WHERE chat=? AND id=?"} {
		if _, err = tx.Exec(q, chat, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Archive) ClearChat(chat string, through int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO barriers(chat,ts) VALUES(?,?) ON CONFLICT(chat) DO UPDATE SET ts=MAX(ts,excluded.ts)", chat, through); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM tokens WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", chat, chat, through); err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM receipt_times WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM receipts WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM audiences WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM message_flags WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM votes WHERE chat=? AND parent IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM message_identities WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM message_mentions WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM message_links WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM message_pins WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)", "DELETE FROM stars WHERE chat=? AND id IN (SELECT id FROM messages WHERE chat=? AND ts<=?)"} {
		if _, err = tx.Exec(q, chat, chat, through); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM messages WHERE chat=? AND ts<=?", chat, through); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Archive) DeleteChat(chat string, through int64) error {
	if err := s.ClearChat(chat, through); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM chats WHERE id=?", "DELETE FROM local_chat_labels WHERE chat=?"} {
		if _, err = tx.Exec(q, chat); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Archive) Draft(chat string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var text string
	err := s.db.QueryRow("SELECT text FROM drafts WHERE chat=?", chat).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return text, err
}
func (s *Archive) SaveDraft(chat, text string) error {
	return s.SaveDraftMentions(chat, text, nil)
}
func (s *Archive) DraftMentions(chat string) (string, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var text string
	var payload []byte
	err := s.db.QueryRow("SELECT d.text,m.payload FROM drafts d LEFT JOIN draft_mentions m ON m.chat=d.chat WHERE d.chat=?", chat).Scan(&text, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	var mentions []string
	if len(payload) > 0 {
		err = json.Unmarshal(payload, &mentions)
	}
	return text, mentions, err
}
func (s *Archive) SaveDraftMentions(chat, text string, mentions []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if chat == "" {
		return errors.New("draft chat is required")
	}
	data, err := encode(mentions)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO drafts(chat,text) VALUES(?,?) ON CONFLICT(chat) DO UPDATE SET text=excluded.text", chat, text); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO draft_mentions(chat,payload) VALUES(?,?) ON CONFLICT(chat) DO UPDATE SET payload=excluded.payload", chat, data); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Archive) Count() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	err := s.db.QueryRow("SELECT count(*) FROM messages").Scan(&n)
	return n, err
}

type Pending struct {
	ID      int64
	Kind    string
	Payload []byte
}

func (s *Archive) Enqueue(kind string, payload []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.db.Exec("INSERT INTO pending(kind,payload) VALUES(?,?)", kind, payload)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
func (s *Archive) Pending(limit int) ([]Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id,kind,payload FROM pending ORDER BY id LIMIT ?", clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Pending
	for rows.Next() {
		var p Pending
		if err = rows.Scan(&p.ID, &p.Kind, &p.Payload); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (s *Archive) Acknowledge(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM pending WHERE id=?", id)
	return err
}
func (s *Archive) ReplacePending(id int64, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE pending SET payload=? WHERE id=?", data, id)
	return err
}
func (s *Archive) SetMetadata(key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, data)
	return err
}
func (s *Archive) Metadata(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	err := s.db.QueryRow("SELECT value FROM metadata WHERE key=?", key).Scan(&b)
	return b, err
}

type Vote struct {
	Sender    string
	Timestamp int64
	ID        string
	Options   [][]byte
}

func (s *Archive) SaveVote(chat, parent string, v Vote) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := encode(v.Options)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO votes(chat,parent,sender,ts,id,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(chat,parent,sender) DO UPDATE SET ts=excluded.ts,id=excluded.id,payload=excluded.payload WHERE excluded.ts>ts OR (excluded.ts=ts AND excluded.id>id)", chat, parent, v.Sender, v.Timestamp, v.ID, data)
	return err
}
func (s *Archive) Votes(chat, parent string) ([]Vote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT sender,ts,id,payload FROM votes WHERE chat=? AND parent=?", chat, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Vote
	for rows.Next() {
		var v Vote
		var b []byte
		if err = rows.Scan(&v.Sender, &v.Timestamp, &v.ID, &b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &v.Options); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// SetAudience snapshots a group audience at send time. Later membership cannot
// turn a partial receipt into an aggregate read acknowledgement.
func (s *Archive) SetAudience(chat, id string, senders []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec("INSERT OR IGNORE INTO audiences(chat,id) VALUES(?,?)", chat, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	for _, sender := range senders {
		if _, err = tx.Exec("INSERT OR IGNORE INTO receipts(chat,id,sender,status) VALUES(?,?,?,0)", chat, id, sender); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Archive) Receipt(chat, id, sender string, status int) (int, bool, error) {
	return s.ReceiptAt(chat, id, sender, status, 0, true)
}

// Canonicalize rekeys a privacy-ID chat after WhatsApp supplies its phone-ID
// mapping. Collisions merge by message ID and preserve edit/delete barriers.
func (s *Archive) Canonicalize(from, to string) error {
	if from == to || from == "" || to == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var mapped string
	if err = tx.QueryRow("SELECT target FROM identities WHERE source=?", from).Scan(&mapped); err == nil && mapped == to {
		return tx.Commit()
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := tx.Query("SELECT payload,raw FROM messages WHERE chat=?", from)
	if err != nil {
		return err
	}
	var msgs []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return e
		}
		m.ChatID = to
		if m.Sender == from {
			m.Sender = to
		}
		msgs = append(msgs, m)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO tombstones(chat,id) SELECT ?,id FROM tombstones WHERE chat=?", to, from); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO barriers(chat,ts) SELECT ?,ts FROM barriers WHERE chat=? ON CONFLICT(chat) DO UPDATE SET ts=MAX(ts,excluded.ts)", to, from); err != nil {
		return err
	}
	// Zap: stars and pins move before the messages are saved under the new
	// chat (save reads the star); the newer event wins a collision. Labels
	// join the target's.
	for _, q := range []string{
		"INSERT INTO stars(chat,id,starred,starred_at) SELECT ?,id,starred,starred_at FROM stars WHERE chat=? ON CONFLICT(chat,id) DO UPDATE SET starred=excluded.starred,starred_at=excluded.starred_at WHERE excluded.starred_at>stars.starred_at",
		"INSERT INTO message_pins(chat,id,pinned,pinned_at,expires_at,by_me,sender) SELECT ?,id,pinned,pinned_at,expires_at,by_me,sender FROM message_pins WHERE chat=? ON CONFLICT(chat,id) DO UPDATE SET pinned=excluded.pinned,pinned_at=excluded.pinned_at,expires_at=excluded.expires_at,by_me=excluded.by_me,sender=excluded.sender WHERE excluded.pinned_at>message_pins.pinned_at",
		"INSERT OR IGNORE INTO local_chat_labels(chat,label) SELECT ?,label FROM local_chat_labels WHERE chat=?",
		"UPDATE message_pins SET sender=? WHERE sender=?",
	} {
		if _, err = tx.Exec(q, to, from); err != nil {
			return err
		}
	}
	for _, m := range msgs {
		oldHash := sha256.Sum256([]byte(from + "\x00" + m.ID))
		newHash := sha256.Sum256([]byte(to + "\x00" + m.ID))
		for _, prefix := range []string{"poll-identity/", "media-retry/", "send-intent/"} {
			oldKey := prefix + hex.EncodeToString(oldHash[:])
			newKey := prefix + hex.EncodeToString(newHash[:])
			if _, err = tx.Exec("INSERT OR IGNORE INTO metadata(key,value) SELECT ?,value FROM metadata WHERE key=?", newKey, oldKey); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", oldKey); err != nil {
				return err
			}
		}
		if err = save(tx, m); err != nil {
			return err
		}
	}
	rows, err = tx.Query("SELECT m.payload,m.raw FROM messages m JOIN message_identities sender ON sender.chat=m.chat AND sender.id=m.id WHERE sender.sender=?", from)
	if err != nil {
		return err
	}
	var senderMessages []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return e
		}
		m.Sender = to
		senderMessages = append(senderMessages, m)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, m := range senderMessages {
		if err = save(tx, m); err != nil {
			return err
		}
	}
	// Zap: a mention of the old identity names the new one.
	rows, err = tx.Query("SELECT m.payload,m.raw FROM messages m WHERE EXISTS(SELECT 1 FROM message_mentions mention WHERE mention.chat=m.chat AND mention.id=m.id AND mention.jid=?)", from)
	if err != nil {
		return err
	}
	var mentioning []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return e
		}
		for i := range m.Mentions {
			if m.Mentions[i].ID == from {
				m.Mentions[i].ID = to
			}
		}
		mentioning = append(mentioning, m)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, m := range mentioning {
		if err = save(tx, m); err != nil {
			return err
		}
	}
	if err = moveReceiptTimes(tx, from, to); err != nil {
		return err
	}
	if err = moveWaitingReceipts(tx, from, to); err != nil {
		return err
	}
	// Move scoped auxiliary rows before deleting the old identity. Vote/receipt
	// collisions use the newer vote and the least-progress receipt; neither a
	// duplicate identity nor a membership change can invent an acknowledgement.
	for _, q := range []string{
		"INSERT INTO drafts(chat,text) SELECT ?,text FROM drafts WHERE chat=? ON CONFLICT(chat) DO NOTHING",
		"INSERT INTO draft_mentions(chat,payload) SELECT ?,payload FROM draft_mentions WHERE chat=? ON CONFLICT(chat) DO NOTHING",
		"INSERT INTO votes(chat,parent,sender,ts,id,payload) SELECT ?,parent,sender,ts,id,payload FROM votes WHERE chat=? ON CONFLICT(chat,parent,sender) DO UPDATE SET ts=excluded.ts,id=excluded.id,payload=excluded.payload WHERE excluded.ts>ts OR (excluded.ts=ts AND excluded.id>id)",
		"INSERT INTO receipts(chat,id,sender,status) SELECT ?,id,sender,status FROM receipts WHERE chat=? ON CONFLICT(chat,id,sender) DO UPDATE SET status=MIN(status,excluded.status)",
		"INSERT OR IGNORE INTO audiences(chat,id) SELECT ?,id FROM audiences WHERE chat=?",
	} {
		if _, err = tx.Exec(q, to, from); err != nil {
			return err
		}
	}
	for _, q := range []string{
		"INSERT INTO votes(chat,parent,sender,ts,id,payload) SELECT chat,parent,?,ts,id,payload FROM votes WHERE sender=? ON CONFLICT(chat,parent,sender) DO UPDATE SET ts=excluded.ts,id=excluded.id,payload=excluded.payload WHERE excluded.ts>ts OR (excluded.ts=ts AND excluded.id>id)",
		"INSERT INTO receipts(chat,id,sender,status) SELECT chat,id,?,status FROM receipts WHERE sender=? ON CONFLICT(chat,id,sender) DO UPDATE SET status=MIN(status,excluded.status)",
	} {
		if _, err = tx.Exec(q, to, from); err != nil {
			return err
		}
	}
	// Only normalized queued actions are rekeyed. Encrypted poll-vote envelopes
	// keep their original wire identities because those identify encryption keys.
	rows, err = tx.Query("SELECT id,payload FROM pending WHERE kind='action'")
	if err != nil {
		return err
	}
	type pendingUpdate struct {
		id      int64
		payload []byte
	}
	var updates []pendingUpdate
	for rows.Next() {
		var id int64
		var payload []byte
		if err = rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return err
		}
		var object map[string]any
		if err = json.Unmarshal(payload, &object); err != nil {
			rows.Close()
			return err
		}
		if action, ok := object["Action"].(map[string]any); ok && action["ChatID"] == from {
			action["ChatID"] = to
			payload, err = encode(object)
			if err != nil {
				rows.Close()
				return err
			}
			updates = append(updates, pendingUpdate{id, payload})
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err = tx.Exec("UPDATE pending SET payload=? WHERE id=?", u.payload, u.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM votes WHERE sender=?", from); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM receipts WHERE sender=?", from); err != nil {
		return err
	}
	var b []byte
	if err = tx.QueryRow("SELECT payload FROM chats WHERE id=?", from).Scan(&b); err == nil {
		var c Chat
		if err = json.Unmarshal(b, &c); err != nil {
			return err
		}
		c.ID = to
		var targetData []byte
		if err = tx.QueryRow("SELECT payload FROM chats WHERE id=?", to).Scan(&targetData); err == nil {
			var target Chat
			if err = json.Unmarshal(targetData, &target); err != nil {
				return err
			}
			target.Locked = target.Locked || c.Locked
			target.Favorite = target.Favorite || c.Favorite
			target.Pinned = target.Pinned || c.Pinned
			target.PinnedAt = max(target.PinnedAt, c.PinnedAt)
			target.Unread = max(target.Unread, c.Unread)
			if target.Name == "" {
				target.Name = c.Name
			}
			if c.LastActivity > target.LastActivity {
				target.LastActivity = c.LastActivity
				target.Preview = c.Preview
			}
			seen := map[string]bool{}
			for _, id := range target.Labels {
				seen[id] = true
			}
			for _, id := range c.Labels {
				if !seen[id] {
					target.Labels = append(target.Labels, id)
					seen[id] = true
				}
			}
			c = target
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec("INSERT INTO chat_visibility(chat,locked) VALUES(?,?) ON CONFLICT(chat) DO UPDATE SET locked=MAX(locked,excluded.locked)", to, c.Locked); err != nil {
			return err
		}
		b, err = encode(c)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO chats(id,activity,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET activity=excluded.activity,payload=excluded.payload", to, c.LastActivity, b); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, q := range []string{"DELETE FROM messages WHERE chat=?", "DELETE FROM tokens WHERE chat=?", "DELETE FROM tombstones WHERE chat=?", "DELETE FROM barriers WHERE chat=?", "DELETE FROM chats WHERE id=?", "DELETE FROM drafts WHERE chat=?", "DELETE FROM draft_mentions WHERE chat=?", "DELETE FROM votes WHERE chat=?", "DELETE FROM receipt_times WHERE chat=?", "DELETE FROM receipts WHERE chat=?", "DELETE FROM audiences WHERE chat=?", "DELETE FROM message_flags WHERE chat=?", "DELETE FROM chat_visibility WHERE chat=?", "DELETE FROM message_identities WHERE chat=?", "DELETE FROM message_mentions WHERE chat=?", "DELETE FROM message_links WHERE chat=?", "DELETE FROM message_pins WHERE chat=?", "DELETE FROM stars WHERE chat=?", "DELETE FROM local_chat_labels WHERE chat=?"} {
		if _, err = tx.Exec(q, from); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO identities(source,target) VALUES(?,?) ON CONFLICT(source) DO UPDATE SET target=excluded.target", from, to); err != nil {
		return err
	}
	return tx.Commit()
}

func SortMessages(messages []Message) {
	sort.Slice(messages, func(i, j int) bool {
		a, b := messages[i], messages[j]
		if a.Timestamp != b.Timestamp {
			return a.Timestamp < b.Timestamp
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
}
func (s *Archive) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
func (s *Archive) Integrity() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result string
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("archive integrity failure")
	}
	return nil
}

// Keep time available for callers choosing a whole-chat deletion barrier.
func Now() int64 { return time.Now().Unix() }

// EnqueueUnique makes replayed encrypted votes and sticker sync events idempotent.
// The caller supplies a stable operation kind scoped by its hashed wire ID.
func (s *Archive) EnqueueUnique(kind string, payload []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.db.QueryRow("SELECT id FROM pending WHERE kind=? ORDER BY id LIMIT 1", kind).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	r, err := s.db.Exec("INSERT INTO pending(kind,payload) VALUES(?,?)", kind, payload)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
func (s *Archive) PendingKind(prefix string, limit int) ([]Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id,kind,payload FROM pending WHERE substr(kind,1,?)=? ORDER BY id LIMIT ?", len(prefix), prefix, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Pending
	for rows.Next() {
		var p Pending
		if err = rows.Scan(&p.ID, &p.Kind, &p.Payload); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// PollAnchor requests immediately before the next archived message so the poll
// itself and its attached aggregate snapshot are included in the phone response.
func (s *Archive) PollAnchor(chat, id string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := scanMessage(s.db.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND id=?", chat, id))
	if err != nil {
		return m, err
	}
	next, err := scanMessage(s.db.QueryRow("SELECT payload,raw FROM messages WHERE chat=? AND (ts>? OR (ts=? AND ord>?) OR (ts=? AND ord=? AND id>?)) ORDER BY ts,ord,id LIMIT 1", chat, m.Timestamp, m.Timestamp, m.Order, m.Timestamp, m.Order, m.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{ChatID: chat, Timestamp: m.Timestamp + 1}, nil
	}
	return next, err
}

// Pending sends after a crash have uncertain acknowledgements. Keep their
// original wire IDs and require an explicit retry instead of generating a new
// message or silently sending every queued message on reconnect.
func (s *Archive) RecoverOutbox() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT m.payload,m.raw FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id WHERE f.from_me=1 AND f.status='pending'")
	if err != nil {
		return err
	}
	var msgs []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return e
		}
		m.Status = "failed"
		msgs = append(msgs, m)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, m := range msgs {
		data, err := encode(m)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE messages SET payload=? WHERE chat=? AND id=?", data, m.ChatID, m.ID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE message_flags SET status='failed' WHERE chat=? AND id=?", m.ChatID, m.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Archive) Blocked(chat, id string, timestamp int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var blocked int
	if err := s.db.QueryRow("SELECT count(*) FROM tombstones WHERE chat=? AND id=?", chat, id).Scan(&blocked); err != nil {
		return false, err
	}
	if blocked > 0 {
		return true, nil
	}
	var barrier int64
	err := s.db.QueryRow("SELECT ts FROM barriers WHERE chat=?", chat).Scan(&barrier)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return timestamp > 0 && timestamp <= barrier, err
}

// SetMetadataBatch commits a custom pack and its shelf records together. A
// failure cannot publish a pack whose sticker membership is only half saved.
func (s *Archive) SetMetadataBatch(values map[string][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, data := range values {
		if _, err = tx.Exec("INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, data); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Archive) UnreadAfter(chat string, boundary Message) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id WHERE m.chat=? AND f.from_me=0 AND (m.ts>? OR (m.ts=? AND m.ord>?) OR (m.ts=? AND m.ord=? AND m.id>?))", chat, boundary.Timestamp, boundary.Timestamp, boundary.Order, boundary.Timestamp, boundary.Order, boundary.ID).Scan(&count)
	return count, err
}

func (s *Archive) MessageCount(chat string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM messages WHERE chat=?", chat).Scan(&count)
	return count, err
}
