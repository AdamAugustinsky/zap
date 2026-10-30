package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Need skips a test when libsqlcipher.dylib is not built.
func need(t testing.TB) {
	t.Helper()
	if _, err := Load(); err != nil {
		if errors.Is(err, ErrNoLibrary) {
			t.Skipf("%v; build it with packaging/macos/sqlcipher/build.sh", err)
		}
		t.Fatal(err)
	}
}

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

func openTest(t *testing.T, path string, key []byte, pragmas ...string) *sql.DB {
	t.Helper()
	db, err := Open(Config{Path: path, Key: key, Pragmas: pragmas})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func TestVersionAndCipher(t *testing.T) {
	need(t)
	v, err := Version()
	if err != nil || v == "" {
		t.Fatalf("version %q %v", v, err)
	}
	db := openTest(t, filepath.Join(t.TempDir(), "a.db"), testKey(1))
	var cipher, provider string
	if err := db.QueryRow("PRAGMA cipher_version").Scan(&cipher); err != nil || cipher == "" {
		t.Fatalf("cipher_version %q %v", cipher, err)
	}
	if err := db.QueryRow("PRAGMA cipher_provider").Scan(&provider); err != nil || provider != "commoncrypto" {
		t.Fatalf("cipher_provider %q %v", provider, err)
	}
	t.Logf("SQLite %s, SQLCipher %s on %s", v, cipher, provider)
}

func TestRoundTripTypes(t *testing.T) {
	need(t)
	db := openTest(t, filepath.Join(t.TempDir(), "a.db"), testKey(1))
	if _, err := db.Exec(`CREATE TABLE v(i INTEGER, f REAL, s TEXT, b BLOB, n, at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 28, 12, 30, 45, 123456789, time.UTC)
	rows := []struct {
		i int64
		f float64
		s string
		b []byte
	}{
		{-1 << 63, 3.5, "", []byte{}},
		{1<<63 - 1, -0.25, "olá, 世界 🙂", []byte{0, 1, 2, 0}},
		{0, 0, "a\x00b", bytes.Repeat([]byte{7}, 100_000)},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO v VALUES(?,?,?,?,NULL,?)`, r.i, r.f, r.s, r.b, at); err != nil {
			t.Fatal(err)
		}
	}
	q, err := db.Query(`SELECT i,f,s,b,n,at,typeof(b) FROM v ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	k := 0
	for q.Next() {
		var i int64
		var f float64
		var s string
		var b []byte
		var n sql.NullString
		var gotAt time.Time
		var kind string
		if err := q.Scan(&i, &f, &s, &b, &n, &gotAt, &kind); err != nil {
			t.Fatal(err)
		}
		r := rows[k]
		if i != r.i || f != r.f || s != r.s || !bytes.Equal(b, r.b) || n.Valid || !gotAt.Equal(at) || kind != "blob" {
			t.Fatalf("row %d: %v %v %q %d %v %v %s", k, i, f, s, len(b), n, gotAt, kind)
		}
		k++
	}
	if err := q.Err(); err != nil || k != len(rows) {
		t.Fatal(k, err)
	}
	var null []byte
	if err := db.QueryRow(`SELECT NULL`).Scan(&null); err != nil || null != nil {
		t.Fatal("NULL", null, err)
	}
	var yes bool
	if err := db.QueryRow(`SELECT ?`, true).Scan(&yes); err != nil || !yes {
		t.Fatal("bool", err)
	}
}

func TestParameters(t *testing.T) {
	need(t)
	db := openTest(t, filepath.Join(t.TempDir(), "a.db"), testKey(1))
	var a, b string
	// $N names argument N wherever it appears, as whatsmeow writes them.
	if err := db.QueryRow(`SELECT $2, $1`, "one", "two").Scan(&a, &b); err != nil || a != "two" || b != "one" {
		t.Fatal(a, b, err)
	}
	if err := db.QueryRow(`SELECT ?, ?`, "x", "y").Scan(&a, &b); err != nil || a != "x" || b != "y" {
		t.Fatal(a, b, err)
	}
	if err := db.QueryRow(`SELECT :b || @a, $a`, sql.Named("a", "A"), sql.Named("b", "B")).Scan(&a, &b); err != nil || a != "BA" || b != "A" {
		t.Fatal(a, b, err)
	}
	if err := db.QueryRow(`SELECT ?, ?`, "only one").Scan(&a, &b); err == nil {
		t.Fatal("missing argument accepted")
	}
	if _, err := db.Query(`SELECT 1; SELECT 2`); err == nil {
		t.Fatal("a query ran two statements")
	}
}

func TestExecScriptAndTransactions(t *testing.T) {
	need(t)
	db := openTest(t, filepath.Join(t.TempDir(), "a.db"), testKey(1))
	res, err := db.Exec(`
		-- a script, as schema upgrades run them
		CREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT NOT NULL);
		CREATE TABLE child(id INTEGER REFERENCES t(id));
		INSERT INTO t(v) VALUES ('a');
		INSERT INTO t(v) VALUES ($1);
	`, "b")
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 2 {
		t.Fatalf("last insert id %d", id)
	}
	if _, err := db.Exec(`INSERT INTO child VALUES (99)`); err == nil {
		t.Fatal("foreign keys are off")
	}
	var se *Error
	if _, err := db.Exec(`INSERT INTO t(id, v) VALUES (1, 'dup')`); !errors.As(err, &se) || se.Code != CodeConstraint {
		t.Fatalf("constraint error: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO t(v) VALUES ('rolled back')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO t(v) VALUES (?)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"c", "d"} {
		if _, err := stmt.Exec(v); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if _, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadUncommitted}); err == nil {
		t.Fatal("unsupported isolation accepted")
	}
}

func TestWrongKeyFailsClosed(t *testing.T) {
	need(t)
	path := filepath.Join(t.TempDir(), "a.db")
	db := openTest(t, path, testKey(1), "PRAGMA journal_mode = WAL")
	if _, err := db.Exec(`CREATE TABLE secret(v TEXT); INSERT INTO secret VALUES ('marker 4711')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal"} {
		b, _ := os.ReadFile(name)
		if bytes.Contains(b, []byte("marker 4711")) || bytes.HasPrefix(b, plaintextHeader) {
			t.Fatalf("plaintext in %s", filepath.Base(name))
		}
	}
	db.Close()
	before := fileHash(t, path)
	if other, err := Open(Config{Path: path, Key: testKey(2)}); !errors.Is(err, ErrWrongKey) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := Open(Config{Path: path, Plaintext: true}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("no key: %v", err)
	}
	if _, err := Open(Config{Path: path, Key: testKey(1)[:31]}); !errors.Is(err, ErrKeySize) {
		t.Fatalf("short key: %v", err)
	}
	if _, err := Open(Config{Path: ":memory:", Key: testKey(1)}); err == nil {
		t.Fatal("memory database accepted")
	}
	if fileHash(t, path) != before {
		t.Fatal("a failed open changed the file")
	}
	again := openTest(t, path, testKey(1))
	var v string
	if err := again.QueryRow(`SELECT v FROM secret`).Scan(&v); err != nil || v != "marker 4711" {
		t.Fatal(v, err)
	}
}

func TestPlaintextIsRefusedWithAKey(t *testing.T) {
	need(t)
	path := filepath.Join(t.TempDir(), "plain.db")
	plain, err := Open(Config{Path: path, Plaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`CREATE TABLE x(v); INSERT INTO x VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	plain.Close()
	before := fileHash(t, path)
	if _, err := Open(Config{Path: path, Key: testKey(1)}); !errors.Is(err, ErrPlaintext) {
		t.Fatalf("plaintext with a key: %v", err)
	}
	if fileHash(t, path) != before {
		t.Fatal("the plaintext file changed")
	}
	ro, err := Open(Config{Path: path, Plaintext: true, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.Exec(`INSERT INTO x VALUES (2)`); err == nil {
		t.Fatal("read-only connection wrote")
	}
}

func TestWALConcurrentReadersAndWriter(t *testing.T) {
	need(t)
	path := filepath.Join(t.TempDir(), "a.db")
	db := openTest(t, path, testKey(3), "PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL")
	db.SetMaxOpenConns(6)
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatal(mode, err)
	}
	if _, err := db.Exec(`CREATE TABLE n(v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	const writes = 200
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range writes {
			tx, err := db.Begin()
			if err != nil {
				errs <- err
				return
			}
			if _, err := tx.Exec(`INSERT INTO n VALUES (?)`, i); err != nil {
				tx.Rollback()
				errs <- err
				return
			}
			if err := tx.Commit(); err != nil {
				errs <- err
				return
			}
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := -1
			for last < writes {
				var n int
				if err := db.QueryRow(`SELECT count(*) FROM n`).Scan(&n); err != nil {
					errs <- err
					return
				}
				if n < last {
					errs <- errors.New("a reader went back in time")
					return
				}
				last = n
				if n == writes {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("no WAL file: %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v %v", st.Mode(), err)
	}
}

func TestContextCancelInterrupts(t *testing.T) {
	need(t)
	db := openTest(t, filepath.Join(t.TempDir(), "a.db"), testKey(1))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := db.ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the statement was not interrupted")
	}
	var n int
	if err := db.QueryRow(`SELECT 1`).Scan(&n); err != nil || n != 1 {
		t.Fatal("connection unusable after an interrupt", err)
	}
}

func TestLibraryPolicy(t *testing.T) {
	dir := t.TempDir()
	noEnv := func(string) string { return "" }
	_, err := findLibrary("", dir, noEnv)
	var le *LibraryError
	if !errors.As(err, &le) || !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("missing library: %v", err)
	}
	dev := filepath.Join(dir, devDir, LibName)
	os.MkdirAll(filepath.Dir(dev), 0o755)
	os.WriteFile(dev, []byte("x"), 0o644)
	sub := filepath.Join(dir, "gnui", "gocore")
	os.MkdirAll(sub, 0o755)
	if p, err := findLibrary("", sub, noEnv); err != nil || p != dev {
		t.Fatalf("development search: %q %v", p, err)
	}
	other := filepath.Join(dir, "other.dylib")
	os.WriteFile(other, []byte("x"), 0o644)
	env := func(k string) string {
		if k == EnvLibrary {
			return other
		}
		return ""
	}
	if p, err := findLibrary("", sub, env); err != nil || p != other {
		t.Fatalf("environment: %q %v", p, err)
	}
	// A bundle loads its own Frameworks' library only, whatever the
	// environment says, and fails closed without it.
	exe := filepath.Join(dir, "Zap.app", "Contents", "MacOS", "zapfast")
	if _, err := findLibrary(exe, sub, env); !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("bundle without its library: %v", err)
	}
	bundled := filepath.Join(dir, "Zap.app", "Contents", "Frameworks", LibName)
	os.MkdirAll(filepath.Dir(bundled), 0o755)
	os.WriteFile(bundled, []byte("x"), 0o644)
	if p, err := findLibrary(exe, sub, env); err != nil || p != bundled {
		t.Fatalf("bundle: %q %v", p, err)
	}
}
