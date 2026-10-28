// Package sqlite is a database/sql driver over SQLCipher, loaded at run time
// from libsqlcipher.dylib with gonativeui/clib (purego underneath): no cgo.
//
// Every connection is encrypted. It applies its 32-byte raw key first
// (PRAGMA key = "x'…'", as the Rust archive does, no KDF), then proves the
// key by reading the schema; a wrong key, or a plaintext file, fails closed
// with ErrWrongKey or ErrPlaintext before any statement of the caller's runs.
// Open a pool with Open (or NewConnector and sql.OpenDB): keys never travel
// in a DSN, so the driver is not registered with database/sql by name.
//
// The library is built by packaging/macos/sqlcipher/build.sh.
package sqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gonativeui/clib"
)

// LibName is the library's file name, in the bundle's Frameworks.
const LibName = "libsqlcipher.dylib"

// EnvLibrary names an environment variable that, when set, is the path of
// the library to load. Unbundled development runs and tests only: a bundled
// app ignores it.
const EnvLibrary = "ZAPFAST_SQLCIPHER"

// devDir is where build.sh puts the library by default, under the
// repository root.
const devDir = "target/sqlcipher"

// ErrNoLibrary is matched (errors.Is) by the *LibraryError of a library that
// is not there.
var ErrNoLibrary = errors.New("sqlite: " + LibName + " not found")

// LibraryError is returned when the library cannot be found or loaded.
type LibraryError struct {
	// Path is the library looked for or loaded; empty when nothing was
	// found.
	Path string
	// Searched lists the places looked in, for a library not found.
	Searched []string
	// Err is ErrNoLibrary for a missing library, else the loader's error.
	Err error
}

func (e *LibraryError) Error() string {
	switch {
	case e.Path != "" && errors.Is(e.Err, ErrNoLibrary):
		return fmt.Sprintf("sqlite: %s is missing", e.Path)
	case e.Path != "":
		return fmt.Sprintf("sqlite: loading %s: %v", e.Path, e.Err)
	default:
		return fmt.Sprintf("sqlite: %s not found: %s is unset and no %s/%s is above %v (packaging/macos/sqlcipher/build.sh)",
			LibName, EnvLibrary, devDir, LibName, e.Searched)
	}
}

func (e *LibraryError) Unwrap() error { return e.Err }

// The loading policy is the core library's (gnui/core/lib.go). A bundled app
// (its executable in X.app/Contents/MacOS) loads only its own
// Contents/Frameworks/libsqlcipher.dylib and fails closed without it: the
// library sees every key and every message, so nothing outside the signed
// bundle may choose it. An unbundled run (go run, go test) is development:
// $ZAPFAST_SQLCIPHER, then target/sqlcipher/ above the working directory or
// the executable.

// FindLibrary returns the library to load under the loading policy.
func FindLibrary() (string, error) {
	exe, _ := executable()
	wd, _ := os.Getwd()
	return findLibrary(exe, wd, os.Getenv)
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// bundleLibrary returns where the bundle keeps the library, and whether exe
// is an app bundle's executable at all.
func bundleLibrary(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	if exe == "" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" ||
		filepath.Ext(filepath.Dir(contents)) != ".app" {
		return "", false
	}
	return filepath.Join(contents, "Frameworks", LibName), true
}

func findLibrary(exe, wd string, getenv func(string) string) (string, error) {
	if lib, bundled := bundleLibrary(exe); bundled {
		if !isFile(lib) {
			return "", &LibraryError{Path: lib, Err: ErrNoLibrary}
		}
		return lib, nil
	}
	if p := getenv(EnvLibrary); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", &LibraryError{Path: p, Err: err}
		}
		if !isFile(abs) {
			return "", &LibraryError{Path: abs, Err: ErrNoLibrary}
		}
		return abs, nil
	}
	var starts []string
	if wd != "" {
		starts = append(starts, wd)
	}
	if exe != "" {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, dir := range starts {
		for {
			p := filepath.Join(dir, devDir, LibName)
			if isFile(p) {
				return p, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", &LibraryError{Searched: starts, Err: ErrNoLibrary}
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// lib is the loaded library's functions, looked up once.
type lib struct {
	path string

	libversion, threadsafe                                  clib.Func
	open, closeV2, errmsg, extendedErrcode, errstr          clib.Func
	busyTimeout, extendedResultCodes, interrupt, autocommit clib.Func
	prepare, finalize, reset, clearBindings, step           clib.Func
	paramCount, paramName                                   clib.Func
	bindInt64, bindDouble, bindText64, bindBlob64, bindNull clib.Func
	bindZeroblob                                            clib.Func
	columnCount, columnName, columnDecltype, columnType     clib.Func
	columnInt64, columnDouble, columnText, columnBlob       clib.Func
	columnBytes, changes64, lastInsertRowid                 clib.Func
}

var (
	loadMu sync.Mutex
	loaded *lib
)

// load loads the library under the policy, once per process. A failure is
// not remembered, so a library built after a failed attempt is found by the
// next one.
func load() (*lib, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded != nil {
		return loaded, nil
	}
	path, err := FindLibrary()
	if err != nil {
		return nil, err
	}
	cl, err := clib.Open(path)
	if err != nil {
		return nil, &LibraryError{Path: path, Err: err}
	}
	l := &lib{path: path}
	for _, f := range [...]struct {
		dst  *clib.Func
		name string
	}{
		{&l.libversion, "sqlite3_libversion"},
		{&l.threadsafe, "sqlite3_threadsafe"},
		{&l.open, "sqlite3_open_v2"},
		{&l.closeV2, "sqlite3_close_v2"},
		{&l.errmsg, "sqlite3_errmsg"},
		{&l.extendedErrcode, "sqlite3_extended_errcode"},
		{&l.errstr, "sqlite3_errstr"},
		{&l.busyTimeout, "sqlite3_busy_timeout"},
		{&l.extendedResultCodes, "sqlite3_extended_result_codes"},
		{&l.interrupt, "sqlite3_interrupt"},
		{&l.autocommit, "sqlite3_get_autocommit"},
		{&l.prepare, "sqlite3_prepare_v2"},
		{&l.finalize, "sqlite3_finalize"},
		{&l.reset, "sqlite3_reset"},
		{&l.clearBindings, "sqlite3_clear_bindings"},
		{&l.step, "sqlite3_step"},
		{&l.paramCount, "sqlite3_bind_parameter_count"},
		{&l.paramName, "sqlite3_bind_parameter_name"},
		{&l.bindInt64, "sqlite3_bind_int64"},
		{&l.bindDouble, "sqlite3_bind_double"},
		{&l.bindText64, "sqlite3_bind_text64"},
		{&l.bindBlob64, "sqlite3_bind_blob64"},
		{&l.bindNull, "sqlite3_bind_null"},
		{&l.bindZeroblob, "sqlite3_bind_zeroblob"},
		{&l.columnCount, "sqlite3_column_count"},
		{&l.columnName, "sqlite3_column_name"},
		{&l.columnDecltype, "sqlite3_column_decltype"},
		{&l.columnType, "sqlite3_column_type"},
		{&l.columnInt64, "sqlite3_column_int64"},
		{&l.columnDouble, "sqlite3_column_double"},
		{&l.columnText, "sqlite3_column_text"},
		{&l.columnBlob, "sqlite3_column_blob"},
		{&l.columnBytes, "sqlite3_column_bytes"},
		{&l.changes64, "sqlite3_changes64"},
		{&l.lastInsertRowid, "sqlite3_last_insert_rowid"},
	} {
		if *f.dst, err = cl.Func(f.name); err != nil {
			return nil, &LibraryError{Path: path, Err: err}
		}
	}
	// SQLCipher's own entry point: a plain SQLite would take PRAGMA key
	// without a word and write plaintext.
	if _, err = cl.Func("sqlite3_key"); err != nil {
		return nil, &LibraryError{Path: path, Err: fmt.Errorf("not a SQLCipher build: %w", err)}
	}
	call := clib.NewCall()
	if call.Do(l.threadsafe).Int32() == 0 {
		return nil, &LibraryError{Path: path, Err: errors.New("built with SQLITE_THREADSAFE=0")}
	}
	loaded = l
	return l, nil
}

// Load loads the library now, under the loading policy, and returns its
// path: a check that the library is there, for tests and startup. Opening a
// connection loads it too.
func Load() (string, error) {
	l, err := load()
	if err != nil {
		return "", err
	}
	return l.path, nil
}

// Version is the loaded library's SQLite version, such as "3.51.3".
func Version() (string, error) {
	l, err := load()
	if err != nil {
		return "", err
	}
	return cString(clib.NewCall().Do(l.libversion).Ptr()), nil
}
