package sqlite

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The fault hook: a failed write changes nothing, a lost acknowledgement
// commits, and a full disk fails only a write that needs a new page.
func TestFaults(t *testing.T) {
	need(t)
	path := filepath.Join(t.TempDir(), "a.db")
	db := openTest(t, path, testKey(1), "PRAGMA journal_mode = WAL")
	if _, err := db.Exec(`CREATE TABLE t(v TEXT)`); err != nil {
		t.Fatal(err)
	}
	var next Fault
	var asked []FaultOp
	defer InjectFaults(func(p string, op FaultOp) Fault {
		if p != path {
			t.Errorf("asked about %s", p)
		}
		asked = append(asked, op)
		f := next
		next = FaultNone
		return f
	})()
	count := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	code := func(err error) int {
		var se *Error
		if !errors.As(err, &se) {
			t.Fatalf("not a SQLite error: %v", err)
		}
		return se.ExtendedCode
	}

	next = FaultIOErr
	if _, err := db.Exec(`INSERT INTO t VALUES('a')`); code(err) != codeIOErrWrite {
		t.Fatalf("insert: %v", err)
	}
	if n := count(); n != 0 {
		t.Fatalf("a failed write left %d rows", n)
	}

	// A failed COMMIT rolls its transaction back.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO t VALUES('b')`); err != nil {
		t.Fatal(err)
	}
	next = FaultIOErr
	if err := tx.Commit(); code(err) != codeIOErrFsync {
		t.Fatalf("commit: %v", err)
	}
	if n := count(); n != 0 {
		t.Fatalf("a failed commit left %d rows", n)
	}

	// A lost acknowledgement reports a failure for a commit that holds.
	tx, _ = db.Begin()
	tx.Exec(`INSERT INTO t VALUES('c')`)
	next = FaultLostAck
	if err := tx.Commit(); code(err) != codeIOErrFsync {
		t.Fatalf("commit: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("a commit whose acknowledgement was lost left %d rows", n)
	}

	// A full disk: a row that needs a page fails with SQLITE_FULL, and the
	// next write has room again.
	next = FaultFull
	_, err = db.Exec(`INSERT INTO t VALUES(?)`, strings.Repeat("x", 64<<10))
	if c := code(err); c&0xff != codeFull {
		t.Fatalf("full: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES(?)`, strings.Repeat("y", 64<<10)); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("%d rows", n)
	}
	if len(asked) == 0 || asked[0] != FaultExec {
		t.Fatalf("asked %v", asked)
	}
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity %q %v", check, err)
	}
}
