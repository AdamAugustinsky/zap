package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion is the archive's PRAGMA user_version. zapfast-go's archive
// is version 4; 5 adds Zap's tables and indexes.
const SchemaVersion = 5

// ErrNewerArchive means the archive was written by a newer schema than this
// build knows. It is left as it is.
var ErrNewerArchive = errors.New("archive: written by a newer version of the app")

// baseSchema is zapfast-go 3104c46's (internal/archive/store.go, MIT), with
// message_flags.kind for a new archive.
const baseSchema = `
CREATE TABLE IF NOT EXISTS chats(id TEXT PRIMARY KEY, activity INTEGER NOT NULL, payload BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS messages(chat TEXT NOT NULL, id TEXT NOT NULL, ts INTEGER NOT NULL, ord INTEGER NOT NULL, payload BLOB NOT NULL, raw BLOB, PRIMARY KEY(chat,id));
CREATE INDEX IF NOT EXISTS messages_page ON messages(chat,ts DESC,ord DESC,id DESC);
CREATE INDEX IF NOT EXISTS messages_recent ON messages(ts DESC,ord DESC,id DESC,chat);
CREATE TABLE IF NOT EXISTS tokens(token TEXT NOT NULL, chat TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY(token,chat,id));
CREATE INDEX IF NOT EXISTS tokens_message ON tokens(chat,id);
CREATE TABLE IF NOT EXISTS tombstones(chat TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(chat,id));
CREATE TABLE IF NOT EXISTS barriers(chat TEXT PRIMARY KEY,ts INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS drafts(chat TEXT PRIMARY KEY,text TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS draft_mentions(chat TEXT PRIMARY KEY,payload BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS pending(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,payload BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS votes(chat TEXT NOT NULL,parent TEXT NOT NULL,sender TEXT NOT NULL,ts INTEGER NOT NULL,id TEXT NOT NULL,payload BLOB NOT NULL,PRIMARY KEY(chat,parent,sender));
CREATE TABLE IF NOT EXISTS audiences(chat TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(chat,id));
CREATE TABLE IF NOT EXISTS receipts(chat TEXT NOT NULL,id TEXT NOT NULL,sender TEXT NOT NULL,status INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(chat,id,sender));
CREATE TABLE IF NOT EXISTS receipt_times(chat TEXT NOT NULL,id TEXT NOT NULL,sender TEXT NOT NULL,delivered INTEGER NOT NULL DEFAULT 0,read_at INTEGER NOT NULL DEFAULT 0,played INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(chat,id,sender));
CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS chat_visibility(chat TEXT PRIMARY KEY,locked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS identities(source TEXT PRIMARY KEY,target TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_identities(chat TEXT NOT NULL,id TEXT NOT NULL,sender TEXT NOT NULL,PRIMARY KEY(chat,id));
CREATE INDEX IF NOT EXISTS message_sender ON message_identities(sender,chat,id);
CREATE TABLE IF NOT EXISTS message_flags(chat TEXT NOT NULL,id TEXT NOT NULL,starred INTEGER NOT NULL DEFAULT 0,from_me INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL DEFAULT '',PRIMARY KEY(chat,id));
CREATE INDEX IF NOT EXISTS message_flags_star ON message_flags(starred,chat,id);`

// zapSchema is version 5's: the tables of the Rust archive that zapfast-go
// lacks (stars with their time, message pins and local labels, as
// src/archive/{stars,pins,labels}.rs keep them), and derived indexes of
// kinds, mentions and links.
const zapSchema = `
CREATE INDEX IF NOT EXISTS message_flags_kind ON message_flags(kind,chat,id);
CREATE TABLE IF NOT EXISTS message_mentions(chat TEXT NOT NULL,id TEXT NOT NULL,jid TEXT NOT NULL,user TEXT NOT NULL,PRIMARY KEY(chat,id,jid,user));
CREATE INDEX IF NOT EXISTS message_mentions_jid ON message_mentions(jid,chat,id);
CREATE TABLE IF NOT EXISTS message_links(chat TEXT NOT NULL,id TEXT NOT NULL,url TEXT NOT NULL,title TEXT NOT NULL DEFAULT '',ts INTEGER NOT NULL,ord INTEGER NOT NULL,PRIMARY KEY(chat,id,url));
CREATE INDEX IF NOT EXISTS message_links_page ON message_links(chat,ts DESC,ord DESC,id DESC);
CREATE TABLE IF NOT EXISTS stars(chat TEXT NOT NULL,id TEXT NOT NULL,starred INTEGER NOT NULL,starred_at INTEGER NOT NULL,PRIMARY KEY(chat,id));
CREATE INDEX IF NOT EXISTS stars_by_time ON stars(starred_at);
CREATE TABLE IF NOT EXISTS message_pins(chat TEXT NOT NULL,id TEXT NOT NULL,pinned INTEGER NOT NULL,pinned_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,by_me INTEGER NOT NULL DEFAULT 0,sender TEXT NOT NULL DEFAULT '',PRIMARY KEY(chat,id));
CREATE INDEX IF NOT EXISTS message_pins_by_chat ON message_pins(chat,pinned_at);
CREATE TABLE IF NOT EXISTS local_labels(id TEXT PRIMARY KEY,name TEXT NOT NULL,color TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS local_chat_labels(chat TEXT NOT NULL,label TEXT NOT NULL,PRIMARY KEY(chat,label));
CREATE INDEX IF NOT EXISTS local_chat_labels_by_label ON local_chat_labels(label);`

// migrate brings the schema to SchemaVersion. An archive older than 5 has
// its derived rows rebuilt from the encrypted payloads, in one transaction.
func (s *Archive) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > SchemaVersion {
		return fmt.Errorf("%w (schema %d, this build knows %d)", ErrNewerArchive, version, SchemaVersion)
	}
	if version == SchemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(baseSchema); err != nil {
		return err
	}
	var hasKind int
	if err = tx.QueryRow("SELECT count(*) FROM pragma_table_info('message_flags') WHERE name='kind'").Scan(&hasKind); err != nil {
		return err
	}
	if hasKind == 0 {
		if _, err = tx.Exec("ALTER TABLE message_flags ADD COLUMN kind TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(zapSchema); err != nil {
		return err
	}
	if err = reindex(tx); err != nil {
		return err
	}
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// reindex rebuilds every derived row (visibility, senders, flags, tokens,
// mentions, links, and stars of messages that carry one) from the payloads.
// SQLCipher's archive keeps no JSON in its indexes: privacy and search
// filters use ordinary encrypted indexed columns.
func reindex(tx *sql.Tx) error {
	rows, err := tx.Query("SELECT payload FROM chats")
	if err != nil {
		return err
	}
	var chats []Chat
	for rows.Next() {
		var data []byte
		var c Chat
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal(data, &c)
		}
		if err != nil {
			rows.Close()
			return err
		}
		chats = append(chats, c)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, c := range chats {
		if _, err = tx.Exec("INSERT OR REPLACE INTO chat_visibility(chat,locked) VALUES(?,?)", c.ID, c.Locked); err != nil {
			return err
		}
	}
	rows, err = tx.Query("SELECT payload FROM messages")
	if err != nil {
		return err
	}
	var msgs []Message
	for rows.Next() {
		var data []byte
		var m Message
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal(data, &m)
		}
		if err != nil {
			rows.Close()
			return err
		}
		msgs = append(msgs, m)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, m := range msgs {
		if err = starFromTable(tx, &m); err != nil {
			return err
		}
		if err = index(tx, m); err != nil {
			return err
		}
	}
	return nil
}

// indexedKind is the kind message_flags files a message under: its own,
// except that an animated picture or clip (a GIF) is "gif", which the
// gallery and the shared media leave out.
func indexedKind(m Message) string {
	if (m.Kind == KindImage || m.Kind == KindVideo) && m.Media != nil && m.Media.Animated {
		return "gif"
	}
	return m.Kind
}

// maxLinks bounds the links indexed for one message.
const maxLinks = 16

var linkPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"]+`)

// messageLinks are the web links a text message holds: its link card's
// first, then each address in its text that names its scheme or starts with
// www. (a bare domain is not a link here, as in the Rust core's shared
// list). Media and documents are listed as such, not by their captions.
func messageLinks(m Message) []LinkPreview {
	if m.Kind != "text" && m.Kind != "" {
		return nil
	}
	var links []LinkPreview
	seen := map[string]bool{}
	add := func(l LinkPreview) {
		if l.URL != "" && !seen[l.URL] && len(links) < maxLinks {
			seen[l.URL] = true
			links = append(links, l)
		}
	}
	if p := m.LinkPreview; p != nil {
		add(LinkPreview{URL: p.URL, Title: p.Title})
	}
	for _, u := range linkPattern.FindAllString(m.Text, -1) {
		add(LinkPreview{URL: trimLink(u)})
	}
	return links
}

// trimLink drops the punctuation that ends a sentence rather than the
// address, and a closing bracket the address did not open.
func trimLink(u string) string {
	for u != "" {
		last := u[len(u)-1]
		switch {
		case strings.IndexByte(".,;:!?'", last) >= 0:
		case last == ')' && strings.Count(u, "(") < strings.Count(u, ")"):
		case last == ']' && strings.Count(u, "[") < strings.Count(u, "]"):
		default:
			return u
		}
		u = u[:len(u)-1]
	}
	return u
}
