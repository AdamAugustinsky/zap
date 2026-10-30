package sqlite

import (
	"bytes"
	"context"
	"sync/atomic"
)

// Faults are for the simulation tests (gocore/wa's sim_test.go) only: a
// hook InjectFaults installs decides, for every write a connection makes,
// whether it fails as a failing disk would make it fail. Nothing in the app
// installs one; without a hook a write costs one atomic load more.

// FaultOp is the write a fault hook is asked about.
type FaultOp int

const (
	// FaultExec is a statement that may write: anything run through Exec
	// but BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE and PRAGMA.
	FaultExec FaultOp = iota
	// FaultCommit is a transaction's COMMIT.
	FaultCommit
)

// Fault is what the hook makes of one write.
type Fault int

const (
	// FaultNone lets the write run.
	FaultNone Fault = iota
	// FaultIOErr fails the write before SQLite sees it: SQLITE_IOERR_WRITE
	// for a statement, SQLITE_IOERR_FSYNC for a COMMIT, whose transaction
	// is then rolled back. Nothing changes on disk.
	FaultIOErr
	// FaultFull runs the statement on a full disk: the database may not
	// grow (max_page_count is its size), so SQLite fails it with
	// SQLITE_FULL if, and only if, it needs a new page.
	FaultFull
	// FaultLostAck, for a COMMIT, commits (durably) but reports
	// SQLITE_IOERR_FSYNC: the caller cannot tell it from a failed one, as
	// after a real fsync error. For a statement it is FaultIOErr.
	FaultLostAck
)

// Extended result codes the faults return.
const (
	codeIOErrWrite = 10 | 3<<8
	codeIOErrFsync = 10 | 4<<8
	codeFull       = 13
	// maxPages is SQLite's largest max_page_count, the limit lifted.
	maxPages = "4294967294"
)

var faultHook atomic.Pointer[func(path string, op FaultOp) Fault]

// InjectFaults installs hook, which every connection asks before each
// write to the database at path; nil removes it. It returns a function that
// puts back the hook it replaced. Tests only.
func InjectFaults(hook func(path string, op FaultOp) Fault) (restore func()) {
	var p *func(string, FaultOp) Fault
	if hook != nil {
		p = &hook
	}
	old := faultHook.Swap(p)
	return func() { faultHook.Store(old) }
}

// faultOf asks the hook about query, a statement or COMMIT of a set-up
// connection; FaultNone without a hook.
func (cn *conn) faultOf(query []byte) (FaultOp, Fault) {
	hook := faultHook.Load()
	if hook == nil || !cn.ready {
		return FaultExec, FaultNone
	}
	word := bytes.ToUpper(firstWord(query))
	op := FaultExec
	switch string(word) {
	case "COMMIT", "END":
		op = FaultCommit
	case "BEGIN", "ROLLBACK", "SAVEPOINT", "RELEASE", "PRAGMA", "SELECT", "":
		return FaultExec, FaultNone
	}
	return op, (*hook)(cn.path, op)
}

func firstWord(query []byte) []byte {
	query = bytes.TrimLeft(query, " \t\r\n;")
	end := bytes.IndexAny(query, " \t\r\n;(\x00")
	if end < 0 {
		end = len(query)
	}
	return query[:end]
}

// faultError is the error a fault returns in place of SQLite's.
func faultError(code int, msg string) error {
	return &Error{Code: code & 0xff, ExtendedCode: code, Msg: msg + " (injected)"}
}

// withFault runs run, a write the hook was asked about, as fault says.
func (cn *conn) withFault(op FaultOp, fault Fault, run func() error) error {
	switch fault {
	case FaultIOErr:
		if op == FaultCommit {
			return faultError(codeIOErrFsync, "disk I/O error")
		}
		return faultError(codeIOErrWrite, "disk I/O error")
	case FaultLostAck:
		if op != FaultCommit {
			return faultError(codeIOErrWrite, "disk I/O error")
		}
		if err := run(); err != nil {
			return err
		}
		return faultError(codeIOErrFsync, "disk I/O error")
	case FaultFull:
		pages, err := cn.queryString(context.Background(), "PRAGMA page_count")
		if err != nil {
			return err
		}
		cn.ready = false // the pragmas below are not asked about
		defer func() { cn.ready = true }()
		if _, err := cn.exec(context.Background(), "PRAGMA max_page_count = "+pages, nil); err != nil {
			return err
		}
		err = run()
		if _, lerr := cn.exec(context.Background(), "PRAGMA max_page_count = "+maxPages, nil); lerr != nil && err == nil {
			err = lerr
		}
		return err
	}
	return run()
}

// stmtFault asks the hook about a prepared statement's Exec.
func (cn *conn) stmtFault() Fault {
	hook := faultHook.Load()
	if hook == nil || !cn.ready {
		return FaultNone
	}
	return (*hook)(cn.path, FaultExec)
}
