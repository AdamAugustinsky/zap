package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A wrong key changes nothing, even with the data still in the WAL, as a
// crash leaves it: the connection that failed the proof used to checkpoint
// the WAL into the database, and delete it, as it closed (found by the
// simulation, gocore/wa's sim_test.go, seed 1).
func TestWrongKeyLeavesTheWALAlone(t *testing.T) {
	need(t)
	dir, crashed := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "a.db")
	db := openTest(t, path, testKey(1), "PRAGMA journal_mode = WAL", "PRAGMA wal_autocheckpoint = 0")
	if _, err := db.Exec(`CREATE TABLE t(v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if _, err := db.Exec(`INSERT INTO t VALUES('row')`); err != nil {
			t.Fatal(err)
		}
	}
	// What a crash leaves on disk: the database and its WAL, no index.
	for _, name := range []string{"a.db", "a.db-wal"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(crashed, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyPath := filepath.Join(crashed, "a.db")
	dbHash, walHash := fileHash(t, copyPath), fileHash(t, copyPath+"-wal")

	if _, err := Open(Config{Path: copyPath, Key: testKey(2), Pragmas: []string{"PRAGMA journal_mode = WAL"}}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("a wrong key: %v", err)
	}
	if fileHash(t, copyPath) != dbHash || fileHash(t, copyPath+"-wal") != walHash {
		t.Fatal("a wrong key changed the database or its WAL")
	}
	// The right key still finds every row.
	right := openTest(t, copyPath, testKey(1), "PRAGMA journal_mode = WAL")
	var n int
	if err := right.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 50 {
		t.Fatalf("%d rows, %v", n, err)
	}
}
