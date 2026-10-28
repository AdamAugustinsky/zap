package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"gonativeui/clib"
)

// KeySize is the length of a raw SQLCipher key.
const KeySize = 32

var (
	// ErrWrongKey means the key does not open the database: SQLCipher could
	// not authenticate its first page. Nothing was written.
	ErrWrongKey = errors.New("sqlite: the key does not open this database")
	// ErrPlaintext means a key was given for a file that is plain SQLite.
	// It is never encrypted in place.
	ErrPlaintext = errors.New("sqlite: the database is not encrypted")
	// ErrKeySize means the key is not KeySize bytes.
	ErrKeySize = errors.New("sqlite: the key must be 32 bytes")
)

// Result codes the caller may want to tell apart (Error.Code).
const (
	CodeBusy       = 5
	CodeLocked     = 6
	CodeInterrupt  = 9
	CodeReadOnly   = 8
	CodeConstraint = 19
	CodeNotADB     = 26
)

const (
	sqliteOK   = 0
	sqliteRow  = 100
	sqliteDone = 101

	typeInteger = 1
	typeFloat   = 2
	typeText    = 3
	typeBlob    = 4
	typeNull    = 5

	openReadOnly  = 0x00000001
	openReadWrite = 0x00000002
	openCreate    = 0x00000004
	openNoMutex   = 0x00008000
	openExResCode = 0x02000000

	utf8Encoding = 1
	// transient is SQLITE_TRANSIENT: SQLite copies a bound value before
	// the bind returns, so the Go memory is never kept.
	transient = -1
)

// Error is an error SQLite returned.
type Error struct {
	// Code is the primary result code (CodeBusy, …), ExtendedCode the
	// extended one.
	Code, ExtendedCode int
	Msg                string
}

func (e *Error) Error() string {
	return fmt.Sprintf("sqlite: %s (%d)", e.Msg, e.ExtendedCode)
}

// Config opens one database.
type Config struct {
	// Path is the database file. It is created (0600) when missing, unless
	// ReadOnly.
	Path string
	// Key is the raw 32-byte SQLCipher key, applied first on every
	// connection. Empty only with Plaintext.
	Key []byte
	// Plaintext opens an unencrypted SQLite file with no key, for reading
	// stores another program wrote (the Rust core's session.db). Never set
	// it for a database this backend owns.
	Plaintext bool
	// ReadOnly opens the file read-only: nothing is written, not even a
	// checkpoint on close.
	ReadOnly bool
	// BusyTimeout is how long a statement waits for another connection's
	// lock; zero: 5 s.
	BusyTimeout time.Duration
	// Pragmas run on every connection once its key is proven, after
	// foreign_keys = ON and temp_store = MEMORY: "PRAGMA journal_mode = WAL"
	// and the like.
	Pragmas []string
}

// Connector makes keyed connections; use it with sql.OpenDB.
type Connector struct {
	cfg Config
}

var _ driver.Connector = (*Connector)(nil)

// NewConnector checks cfg and returns its connector. The key is copied.
func NewConnector(cfg Config) (*Connector, error) {
	if cfg.Path == "" || cfg.Path == ":memory:" || strings.HasPrefix(cfg.Path, "file:") {
		return nil, errors.New("sqlite: an encrypted database needs a file path")
	}
	if cfg.Plaintext {
		if len(cfg.Key) != 0 {
			return nil, errors.New("sqlite: a plaintext database takes no key")
		}
	} else if len(cfg.Key) != KeySize {
		return nil, ErrKeySize
	}
	cfg.Key = bytes.Clone(cfg.Key)
	cfg.Pragmas = append([]string(nil), cfg.Pragmas...)
	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = 5 * time.Second
	}
	return &Connector{cfg: cfg}, nil
}

// Open opens a pool over cfg and proves the key on its first connection,
// so a wrong key, a plaintext file or a missing library fails here.
func Open(cfg Config) (*sql.DB, error) {
	c, err := NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	if !cfg.ReadOnly {
		if err := createPrivate(cfg.Path); err != nil {
			return nil, err
		}
	}
	db := sql.OpenDB(c)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// createPrivate makes a missing database file, readable by its owner only
// (SQLite gives its WAL and shared-memory files the same mode). A symlink
// is refused, so the key never encrypts a file somewhere else.
func createPrivate(path string) error {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sqlite: %s is a symlink", path)
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// Driver is the connector's driver. It opens nothing by name: keys do not
// travel in DSNs.
func (c *Connector) Driver() driver.Driver { return sqlDriver{} }

type sqlDriver struct{}

func (sqlDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("sqlite: open through sqlite.Open or a Connector, not a DSN")
}

// plaintextHeader starts every unencrypted SQLite file.
var plaintextHeader = []byte("SQLite format 3\x00")

func isPlaintext(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	var h [16]byte
	n, err := io.ReadFull(f, h[:])
	if n < len(h) {
		return false, nil // empty or new: SQLCipher writes it encrypted
	}
	return err == nil && bytes.Equal(h[:], plaintextHeader), err
}

// Connect opens a connection, applies the key and proves it.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	l, err := load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !c.cfg.Plaintext {
		plain, err := isPlaintext(c.cfg.Path)
		if err != nil {
			return nil, err
		}
		if plain {
			return nil, ErrPlaintext
		}
	}
	cn := &conn{l: l, call: clib.NewCall(), icall: clib.NewCall(), out: new(outParams)}
	flags := int32(openNoMutex | openExResCode)
	if c.cfg.ReadOnly {
		flags |= openReadOnly
	} else {
		flags |= openReadWrite | openCreate
	}
	name := clib.CStr(nil, c.cfg.Path)
	rc := cn.call.Ptr(unsafe.Pointer(&name[0])).Ptr(unsafe.Pointer(&cn.out.db)).Int32(flags).Ptr(nil).
		Do(l.open).Int32()
	cn.db = cn.out.db
	if rc != sqliteOK {
		err := cn.lastError(rc)
		if cn.db != nil {
			cn.call.Ptr(cn.db).Do(l.closeV2)
		}
		return nil, err
	}
	if err := cn.setup(ctx, &c.cfg); err != nil {
		cn.Close()
		return nil, err
	}
	return cn, nil
}

// setup keys the connection, proves the key and runs the pragmas.
func (cn *conn) setup(ctx context.Context, cfg *Config) error {
	cn.call.Ptr(cn.db).Int32(1).Do(cn.l.extendedResultCodes)
	cn.call.Ptr(cn.db).Int32(int32(min(cfg.BusyTimeout.Milliseconds(), 1<<31-1))).Do(cn.l.busyTimeout)
	if !cfg.Plaintext {
		// PRAGMA key = "x'<64 hex>'": the raw key, no KDF, as the Rust
		// archive keys it (src/archive/encryption.rs). The literal is wiped
		// once SQLite has parsed it.
		pragma := make([]byte, 0, 16+2*KeySize+4)
		pragma = append(pragma, `PRAGMA key = "x'`...)
		pragma = hex.AppendEncode(pragma, cfg.Key)
		pragma = append(pragma, `'"`...)
		_, err := cn.execBytes(ctx, pragma, nil)
		clear(pragma)
		if err != nil {
			return err
		}
	}
	version, err := cn.queryString(ctx, "PRAGMA cipher_version")
	if err != nil || version == "" {
		return &LibraryError{Path: cn.l.path, Err: errors.New("not a SQLCipher build: cipher_version is empty")}
	}
	// PRAGMA key alone proves nothing: the first page is read and
	// authenticated here, before any statement of the caller's.
	if _, err := cn.queryString(ctx, "SELECT count(*) FROM sqlite_master"); err != nil {
		var se *Error
		if errors.As(err, &se) && se.Code == CodeNotADB {
			return ErrWrongKey
		}
		return err
	}
	for _, p := range append([]string{"PRAGMA foreign_keys = ON", "PRAGMA temp_store = MEMORY"}, cfg.Pragmas...) {
		if _, err := cn.exec(ctx, p, nil); err != nil {
			return fmt.Errorf("sqlite: %s: %w", p, err)
		}
	}
	return nil
}

// outParams is where C writes the pointers a call returns through its
// arguments; it lives on the heap, apart from Go pointers.
type outParams struct {
	db, stmt, tail unsafe.Pointer
}

// conn is one SQLite connection. database/sql hands it to one goroutine at
// a time, which matches SQLITE_THREADSAFE=2; call is that goroutine's, and
// icall the interrupting goroutine's.
type conn struct {
	l      *lib
	db     unsafe.Pointer
	call   *clib.Call
	icall  *clib.Call
	out    *outParams
	sqlbuf []byte
	closed bool
}

var (
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.Validator          = (*conn)(nil)
)

func (cn *conn) lastError(rc int32) error {
	e := &Error{Code: int(rc & 0xff), ExtendedCode: int(rc)}
	if cn.db != nil {
		e.ExtendedCode = int(cn.call.Ptr(cn.db).Do(cn.l.extendedErrcode).Int32())
		e.Code = e.ExtendedCode & 0xff
		e.Msg = cString(cn.call.Ptr(cn.db).Do(cn.l.errmsg).Ptr())
	}
	if e.Msg == "" {
		e.Msg = cString(cn.call.Int32(rc).Do(cn.l.errstr).Ptr())
	}
	return e
}

func (cn *conn) Close() error {
	if cn.closed {
		return nil
	}
	cn.closed = true
	if rc := cn.call.Ptr(cn.db).Do(cn.l.closeV2).Int32(); rc != sqliteOK {
		return &Error{Code: int(rc & 0xff), ExtendedCode: int(rc), Msg: "close failed"}
	}
	cn.db = nil
	return nil
}

func (cn *conn) IsValid() bool { return !cn.closed }

func (cn *conn) Ping(context.Context) error {
	if cn.closed {
		return driver.ErrBadConn
	}
	return nil
}

// ResetSession refuses a connection returned with a transaction still
// open, which a later user would otherwise run inside.
func (cn *conn) ResetSession(context.Context) error {
	if cn.closed || cn.call.Ptr(cn.db).Do(cn.l.autocommit).Int32() == 0 {
		return driver.ErrBadConn
	}
	return nil
}

// watch interrupts the connection's statement when ctx ends, until the
// returned stop is called. sqlite3_interrupt is safe from another thread.
func (cn *conn) watch(ctx context.Context) (stop func()) {
	done := ctx.Done()
	if done == nil {
		return func() {}
	}
	quit, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-done:
			cn.icall.Ptr(cn.db).Do(cn.l.interrupt)
		case <-quit:
		}
	}()
	return func() { close(quit); <-finished }
}

// ctxError replaces SQLite's interrupt error with the context's.
func ctxError(ctx context.Context, err error) error {
	var se *Error
	if errors.As(err, &se) && se.Code == CodeInterrupt && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// prepareAt prepares the statement starting at off in cn.sqlbuf (which ends
// in a NUL) and returns it with the offset after it; a nil statement is
// whitespace or a comment.
func (cn *conn) prepareAt(off int) (unsafe.Pointer, int, error) {
	base := unsafe.Pointer(&cn.sqlbuf[0])
	start := unsafe.Add(base, off)
	n := len(cn.sqlbuf) - 1 - off
	cn.out.stmt, cn.out.tail = nil, nil
	rc := cn.call.Ptr(cn.db).Ptr(start).Int32(int32(n)).Ptr(unsafe.Pointer(&cn.out.stmt)).
		Ptr(unsafe.Pointer(&cn.out.tail)).Do(cn.l.prepare).Int32()
	stmt, tail := cn.out.stmt, cn.out.tail
	if rc != sqliteOK {
		return nil, 0, cn.lastError(rc)
	}
	next := len(cn.sqlbuf) - 1
	if tail != nil {
		next = int(uintptr(tail) - uintptr(base))
	}
	return stmt, next, nil
}

func (cn *conn) setSQL(query []byte) error {
	if len(query) > 1<<30 {
		return errors.New("sqlite: statement too long")
	}
	cn.sqlbuf = append(append(cn.sqlbuf[:0], query...), 0)
	return nil
}

// rest reports whether the SQL after off holds anything but blanks and
// semicolons.
func (cn *conn) rest(off int) bool {
	return len(bytes.Trim(cn.sqlbuf[off:len(cn.sqlbuf)-1], " \t\r\n;")) > 0
}

func (cn *conn) exec(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return cn.execBytes(ctx, []byte(query), args)
}

// execBytes runs every statement of query in turn, each bound from args
// (by name, by $N or ?N, or by position), and reports the last one's
// changes.
func (cn *conn) execBytes(ctx context.Context, query []byte, args []driver.NamedValue) (driver.Result, error) {
	if cn.closed {
		return nil, driver.ErrBadConn
	}
	if err := cn.setSQL(query); err != nil {
		return nil, err
	}
	defer clear(cn.sqlbuf) // the key pragma passes through here
	stop := cn.watch(ctx)
	defer stop()
	var res result
	for off := 0; off < len(cn.sqlbuf)-1; {
		h, next, err := cn.prepareAt(off)
		if err != nil {
			return nil, ctxError(ctx, err)
		}
		off = next
		if h == nil {
			continue
		}
		s := &stmt{cn: cn, h: h}
		s.mapParams()
		err = s.bind(args)
		if err == nil {
			res, err = s.run()
		}
		s.finalize()
		if err != nil {
			return nil, ctxError(ctx, err)
		}
	}
	return res, nil
}

// queryString returns the first column of the first row, "" for none.
func (cn *conn) queryString(ctx context.Context, query string) (string, error) {
	r, err := cn.QueryContext(ctx, query, nil)
	if err != nil {
		return "", err
	}
	defer r.Close()
	dest := make([]driver.Value, len(r.Columns()))
	if err := r.Next(dest); err != nil {
		if err == io.EOF {
			return "", nil
		}
		return "", err
	}
	switch v := dest[0].(type) {
	case string:
		return v, nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	}
	return fmt.Sprint(dest[0]), nil
}

func (cn *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return cn.exec(ctx, query, args)
}

func (cn *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s, err := cn.prepare(query)
	if err != nil {
		return nil, err
	}
	s.ownedByRows = true
	r, err := s.query(ctx, args)
	if err != nil {
		s.finalize()
		return nil, err
	}
	return r, nil
}

func (cn *conn) Prepare(query string) (driver.Stmt, error) {
	return cn.prepare(query)
}

func (cn *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cn.prepare(query)
}

// prepare prepares exactly one statement.
func (cn *conn) prepare(query string) (*stmt, error) {
	if cn.closed {
		return nil, driver.ErrBadConn
	}
	if err := cn.setSQL([]byte(query)); err != nil {
		return nil, err
	}
	h, next, err := cn.prepareAt(0)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("sqlite: no statement to prepare")
	}
	s := &stmt{cn: cn, h: h}
	if cn.rest(next) {
		s.finalize()
		return nil, errors.New("sqlite: a query takes one statement; Exec runs several")
	}
	s.mapParams()
	return s, nil
}

func (cn *conn) Begin() (driver.Tx, error) {
	return cn.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx starts a transaction: BEGIN IMMEDIATE, which takes the write lock
// at once (waiting up to the busy timeout) rather than failing with
// SQLITE_BUSY when a deferred one first writes; a read-only one is a plain
// BEGIN. SQLite's isolation is serializable.
func (cn *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	switch sql.IsolationLevel(opts.Isolation) {
	case sql.LevelDefault, sql.LevelSerializable:
	default:
		return nil, fmt.Errorf("sqlite: isolation level %v is not supported", sql.IsolationLevel(opts.Isolation))
	}
	begin := "BEGIN IMMEDIATE"
	if opts.ReadOnly {
		begin = "BEGIN"
	}
	if _, err := cn.exec(ctx, begin, nil); err != nil {
		return nil, err
	}
	return tx{cn}, nil
}

type tx struct{ cn *conn }

func (t tx) Commit() error {
	_, err := t.cn.exec(context.Background(), "COMMIT", nil)
	if err != nil && t.cn.call.Ptr(t.cn.db).Do(t.cn.l.autocommit).Int32() == 0 {
		// A failed COMMIT (SQLITE_BUSY) leaves the transaction open: end it,
		// so the connection goes back to the pool clean.
		t.cn.exec(context.Background(), "ROLLBACK", nil)
	}
	return err
}

func (t tx) Rollback() error {
	if t.cn.call.Ptr(t.cn.db).Do(t.cn.l.autocommit).Int32() != 0 {
		return nil // SQLite already rolled it back (an error did)
	}
	_, err := t.cn.exec(context.Background(), "ROLLBACK", nil)
	return err
}

type result struct{ id, changes int64 }

func (r result) LastInsertId() (int64, error) { return r.id, nil }
func (r result) RowsAffected() (int64, error) { return r.changes, nil }

// param is one parameter of a statement: bound from the argument with its
// name, or with its ordinal.
type param struct {
	ordinal int
	name    string
}

type stmt struct {
	cn     *conn
	h      unsafe.Pointer
	params []param
	// numInput is the number of arguments, -1 with named parameters.
	numInput int
	// ownedByRows: the statement exists for one query, and its rows
	// finalize it.
	ownedByRows bool
	rows        *rows
	closed      bool
}

var (
	_ driver.Stmt             = (*stmt)(nil)
	_ driver.StmtExecContext  = (*stmt)(nil)
	_ driver.StmtQueryContext = (*stmt)(nil)
)

// mapParams reads the statement's parameters: ? and ?N and $N are
// positional (?N and $N name argument N, wherever they appear), :name,
// @name and $name take the argument of that name.
func (s *stmt) mapParams() {
	c, l := s.cn.call, s.cn.l
	n := int(c.Ptr(s.h).Do(l.paramCount).Int32())
	s.params = make([]param, n)
	for i := range n {
		name := cString(c.Ptr(s.h).Int32(int32(i + 1)).Do(l.paramName).Ptr())
		p := param{ordinal: i + 1}
		if name != "" {
			if v, err := strconv.Atoi(name[1:]); err == nil && v > 0 && (name[0] == '?' || name[0] == '$') {
				p.ordinal = v
			} else {
				p.ordinal, p.name = 0, name[1:]
			}
		}
		s.params[i] = p
		if p.name != "" {
			s.numInput = -1
		} else if s.numInput >= 0 {
			s.numInput = max(s.numInput, p.ordinal)
		}
	}
}

func (s *stmt) NumInput() int { return s.numInput }

func (s *stmt) finalize() {
	if !s.closed {
		s.closed = true
		s.cn.call.Ptr(s.h).Do(s.cn.l.finalize)
	}
}

func (s *stmt) Close() error {
	if s.rows != nil {
		s.rows.stmtGone = true
	}
	if !s.cn.closed {
		s.finalize()
	}
	return nil
}

func (s *stmt) bind(args []driver.NamedValue) error {
	c, l := s.cn.call, s.cn.l
	for i, p := range s.params {
		v, ok := lookupArg(args, p)
		if !ok {
			if p.name != "" {
				return fmt.Errorf("sqlite: no argument named %q", p.name)
			}
			return fmt.Errorf("sqlite: no argument %d", p.ordinal)
		}
		idx := int32(i + 1)
		var rc int32
		switch v := v.(type) {
		case nil:
			rc = c.Ptr(s.h).Int32(idx).Do(l.bindNull).Int32()
		case int64:
			rc = c.Ptr(s.h).Int32(idx).Int(v).Do(l.bindInt64).Int32()
		case float64:
			rc = c.Ptr(s.h).Int32(idx).Float64(v).Do(l.bindDouble).Int32()
		case bool:
			var b int64
			if v {
				b = 1
			}
			rc = c.Ptr(s.h).Int32(idx).Int(b).Do(l.bindInt64).Int32()
		case string:
			rc = s.bindText(idx, v)
		case time.Time:
			rc = s.bindText(idx, v.Format(timeFormat))
		case []byte:
			if v == nil {
				rc = c.Ptr(s.h).Int32(idx).Do(l.bindNull).Int32()
			} else if len(v) == 0 {
				rc = c.Ptr(s.h).Int32(idx).Int32(0).Do(l.bindZeroblob).Int32()
			} else {
				rc = c.Ptr(s.h).Int32(idx).Ptr(unsafe.Pointer(&v[0])).Uint(uint64(len(v))).Int(transient).
					Do(l.bindBlob64).Int32()
			}
		default:
			return fmt.Errorf("sqlite: cannot bind %T", v)
		}
		if rc != sqliteOK {
			return s.cn.lastError(rc)
		}
	}
	return nil
}

// empty backs a bound empty string: SQLite needs a non-NULL pointer for
// text that is empty rather than NULL.
var empty = new([1]byte)

func (s *stmt) bindText(idx int32, v string) int32 {
	p := unsafe.Pointer(empty)
	if v != "" {
		// The arguments came through interfaces, so the bytes are on the
		// heap or static, never on a stack; SQLite copies them.
		p = unsafe.Pointer(unsafe.StringData(v))
	}
	return s.cn.call.Ptr(s.h).Int32(idx).Ptr(p).Uint(uint64(len(v))).Int(transient).Uint8(utf8Encoding).
		Do(s.cn.l.bindText64).Int32()
}

func lookupArg(args []driver.NamedValue, p param) (driver.Value, bool) {
	if p.name != "" {
		for _, a := range args {
			if a.Name == p.name {
				return a.Value, true
			}
		}
		return nil, false
	}
	if i := p.ordinal - 1; i < len(args) && args[i].Ordinal == p.ordinal && args[i].Name == "" {
		return args[i].Value, true
	}
	for _, a := range args {
		if a.Ordinal == p.ordinal && a.Name == "" {
			return a.Value, true
		}
	}
	return nil, false
}

// run steps the statement to its end and reports its changes.
func (s *stmt) run() (result, error) {
	c, l := s.cn.call, s.cn.l
	for {
		switch rc := c.Ptr(s.h).Do(l.step).Int32(); rc {
		case sqliteRow:
			continue
		case sqliteDone:
			return result{
				id:      c.Ptr(s.cn.db).Do(l.lastInsertRowid).Int(),
				changes: c.Ptr(s.cn.db).Do(l.changes64).Int(),
			}, nil
		default:
			c.Ptr(s.h).Do(l.reset)
			return result{}, s.cn.lastError(rc)
		}
	}
}

func (s *stmt) start(args []driver.NamedValue) error {
	if s.closed || s.cn.closed {
		return driver.ErrBadConn
	}
	if s.rows != nil {
		return errors.New("sqlite: the statement's rows are still open")
	}
	c, l := s.cn.call, s.cn.l
	c.Ptr(s.h).Do(l.reset)
	c.Ptr(s.h).Do(l.clearBindings)
	return s.bind(args)
}

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), named(args))
}

func named(args []driver.Value) []driver.NamedValue {
	nv := make([]driver.NamedValue, len(args))
	for i, v := range args {
		nv[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return nv
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.start(args); err != nil {
		return nil, err
	}
	stop := s.cn.watch(ctx)
	defer stop()
	r, err := s.run()
	if err != nil {
		return nil, ctxError(ctx, err)
	}
	return r, nil
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.query(ctx, args)
}

// query binds and returns the rows; database/sql closes them when ctx ends.
func (s *stmt) query(_ context.Context, args []driver.NamedValue) (*rows, error) {
	if err := s.start(args); err != nil {
		return nil, err
	}
	c, l := s.cn.call, s.cn.l
	n := int(c.Ptr(s.h).Do(l.columnCount).Int32())
	r := &rows{s: s, names: make([]string, n), decl: make([]string, n)}
	for i := range n {
		r.names[i] = cString(c.Ptr(s.h).Int32(int32(i)).Do(l.columnName).Ptr())
		r.decl[i] = strings.ToUpper(cString(c.Ptr(s.h).Int32(int32(i)).Do(l.columnDecltype).Ptr()))
	}
	s.rows = r
	return r, nil
}

type rows struct {
	s        *stmt
	names    []string
	decl     []string
	done     bool
	stmtGone bool
}

var (
	_ driver.Rows                           = (*rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
)

func (r *rows) Columns() []string { return r.names }

func (r *rows) ColumnTypeDatabaseTypeName(i int) string { return r.decl[i] }

func (r *rows) Close() error {
	s := r.s
	if s.rows != r {
		return nil
	}
	s.rows = nil
	if r.stmtGone || s.closed || s.cn.closed {
		return nil
	}
	if s.ownedByRows {
		s.finalize()
	} else {
		s.cn.call.Ptr(s.h).Do(s.cn.l.reset)
	}
	return nil
}

func (r *rows) Next(dest []driver.Value) error {
	s := r.s
	if r.done {
		return io.EOF
	}
	if r.stmtGone || s.closed || s.cn.closed {
		return driver.ErrBadConn
	}
	c, l := s.cn.call, s.cn.l
	switch rc := c.Ptr(s.h).Do(l.step).Int32(); rc {
	case sqliteRow:
	case sqliteDone:
		r.done = true
		return io.EOF
	default:
		r.done = true
		return s.cn.lastError(rc)
	}
	for i := range dest {
		col := int32(i)
		switch c.Ptr(s.h).Int32(col).Do(l.columnType).Int32() {
		case typeNull:
			dest[i] = nil
		case typeInteger:
			v := c.Ptr(s.h).Int32(col).Do(l.columnInt64).Int()
			dest[i] = v
			if isTimeType(r.decl[i]) {
				dest[i] = time.Unix(v, 0).UTC()
			}
		case typeFloat:
			dest[i] = c.Ptr(s.h).Int32(col).Do(l.columnDouble).Float64()
		case typeText:
			p := c.Ptr(s.h).Int32(col).Do(l.columnText).Ptr()
			n := int(c.Ptr(s.h).Int32(col).Do(l.columnBytes).Int32())
			var v string
			if p != nil && n > 0 {
				v = string(unsafe.Slice((*byte)(p), n))
			}
			dest[i] = v
			if isTimeType(r.decl[i]) {
				if t, ok := parseTime(v); ok {
					dest[i] = t
				}
			}
		default: // typeBlob
			p := c.Ptr(s.h).Int32(col).Do(l.columnBlob).Ptr()
			n := int(c.Ptr(s.h).Int32(col).Do(l.columnBytes).Int32())
			v := []byte{}
			if p != nil && n > 0 {
				v = bytes.Clone(unsafe.Slice((*byte)(p), n))
			}
			dest[i] = v
		}
	}
	return nil
}

// timeFormat is how a time.Time argument is stored: text that sorts in
// time order for one zone and that parseTime reads back.
const timeFormat = "2006-01-02 15:04:05.999999999-07:00"

var timeFormats = []string{
	timeFormat,
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// isTimeType reports whether a declared column type holds times.
func isTimeType(decl string) bool {
	return decl == "DATE" || decl == "DATETIME" || decl == "TIMESTAMP"
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSuffix(s, "Z")
	for _, f := range timeFormats {
		if t, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// cString copies a NUL-terminated C string; nil is "".
func cString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
