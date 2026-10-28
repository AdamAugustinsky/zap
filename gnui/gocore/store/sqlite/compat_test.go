package sqlite

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The Rust core's archive (src/archive/encryption.rs and src/archive.rs at
// upstream a95d8f0, rusqlite on SQLCipher 4.14.0) opens a connection like
// this: the raw key, cipher_version, a schema read, temp_store, then WAL and
// synchronous = NORMAL. The cipher settings are SQLCipher 4's defaults.
var rustPragmas = []string{"PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL"}

// fixtureKey is the synthetic key of testdata/rust-compatible.db.
var fixtureKey = bytes.Repeat([]byte{0x5a}, KeySize)

const fixtureSchema = `
CREATE TABLE chats (id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, last_activity INTEGER NOT NULL DEFAULT 0);
CREATE TABLE messages (chat TEXT NOT NULL, id TEXT NOT NULL, sender TEXT NOT NULL, from_me INTEGER NOT NULL,
	timestamp INTEGER NOT NULL, content TEXT NOT NULL, raw BLOB, PRIMARY KEY (chat, id));
CREATE INDEX messages_by_time ON messages (chat, timestamp);
INSERT INTO chats VALUES ('15550001111@s.whatsapp.net', 'Synthetic Ada', 'direct', 1700000100);
INSERT INTO messages VALUES ('15550001111@s.whatsapp.net', 'SYNTH-1', '15550001111@s.whatsapp.net', 0, 1700000000,
	'{"kind":"text","text":"synthetic fixture olá"}', x'0a0b0c');
INSERT INTO messages VALUES ('15550001111@s.whatsapp.net', 'SYNTH-2', '', 1, 1700000100,
	'{"kind":"text","text":"second"}', NULL);
`

func writeFixture(t *testing.T, path string) {
	t.Helper()
	db, err := Open(Config{Path: path, Key: fixtureKey, Pragmas: rustPragmas})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fixtureSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func checkFixture(t *testing.T, path string) {
	t.Helper()
	db, err := Open(Config{Path: path, Key: fixtureKey, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var text string
	var raw []byte
	err = db.QueryRow(`SELECT json_extract(content, '$.text'), raw FROM messages WHERE id = 'SYNTH-1'`).Scan(&text, &raw)
	if err != nil || text != "synthetic fixture olá" || !bytes.Equal(raw, []byte{10, 11, 12}) {
		t.Fatalf("fixture row: %q %x %v", text, raw, err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM messages m JOIN chats c ON c.id = m.chat`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	var ok string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatal(ok, err)
	}
}

// TestSQLCipher4Format decrypts page 1 of a database the driver wrote, with
// Go's own crypto and SQLCipher 4's documented defaults: salt in the first
// 16 bytes, AES-256-CBC, an 80-byte reserve holding the IV and an
// HMAC-SHA512 over the ciphertext, the IV and the little-endian page number,
// keyed by PBKDF2-HMAC-SHA512(key, salt ^ 0x3a, 2 rounds). That is the
// format the Rust archive writes with the same raw key.
func TestSQLCipher4Format(t *testing.T) {
	need(t)
	path := filepath.Join(t.TempDir(), "archive.db")
	writeFixture(t, path)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const pageSize, reserve, ivSize = 4096, 80, 16
	if len(b) < pageSize || len(b)%pageSize != 0 {
		t.Fatalf("file size %d", len(b))
	}
	page := b[:pageSize]
	salt := page[:16]
	mask := make([]byte, len(salt))
	for i := range salt {
		mask[i] = salt[i] ^ 0x3a
	}
	macKey, err := pbkdf2.Key(sha512.New, string(fixtureKey), mask, 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	data := page[16 : pageSize-reserve]
	iv := page[pageSize-reserve : pageSize-reserve+ivSize]
	mac := hmac.New(sha512.New, macKey)
	mac.Write(data)
	mac.Write(iv)
	binary.Write(mac, binary.LittleEndian, uint32(1))
	if want := page[pageSize-reserve+ivSize : pageSize-reserve+ivSize+64]; !hmac.Equal(mac.Sum(nil), want) {
		t.Fatal("page 1 HMAC-SHA512 does not match SQLCipher 4's layout")
	}
	block, err := aes.NewCipher(fixtureKey)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	// plain starts at file offset 16 of a SQLite header.
	if got := binary.BigEndian.Uint16(plain[0:2]); got != pageSize {
		t.Fatalf("page size %d", got)
	}
	if plain[2] != 2 || plain[3] != 2 {
		t.Fatalf("file format versions %d/%d, want 2/2 (WAL)", plain[2], plain[3])
	}
	if plain[4] != reserve {
		t.Fatalf("reserved bytes %d, want %d", plain[4], reserve)
	}
	checkFixture(t, path)
}

// TestRustCompatibleFixture reads the committed fixture, a database written
// by the driver with the Rust archive's settings (see writeFixture). It
// catches a library rebuilt with settings that no longer read files already
// on disk. ZAPFAST_WRITE_FIXTURE=1 rewrites it. A fixture written by the
// Rust core itself comes with the migration work (WP6).
func TestRustCompatibleFixture(t *testing.T) {
	need(t)
	src := filepath.Join("testdata", "rust-compatible.db")
	if os.Getenv("ZAPFAST_WRITE_FIXTURE") == "1" {
		os.Remove(src)
		writeFixture(t, src)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "archive.db")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	checkFixture(t, path)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, b) {
		t.Fatal("reading the fixture read-only changed it")
	}
}
