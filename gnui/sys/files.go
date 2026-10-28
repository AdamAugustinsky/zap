package sys

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"

	"zapfast/state"
)

// The process's own files (zf:src/paths.rs ensure, zf:src/main.rs logging):
// the folders are the user's alone, the log of the last run is zapfast.log
// and a crash is written to panic.log. Both files sit in the data folder
// (Settings > Files > Log opens it there). The core keeps the account
// folders under it.

// Restrict makes the folders and restricts them to the user (0700), even
// ones that already exist with wider permissions, without touching what is
// in them. Startup stops when it cannot (the error says which folder).
func Restrict(p state.Paths) error {
	for _, dir := range []string{p.Config, p.Data, p.Cache} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("cannot make %s private to you: %w", dir, err)
		}
		if runtime.GOOS == "windows" {
			continue // the permissions of the user's profile apply
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("cannot make %s private to you: %w", dir, err)
		}
	}
	return nil
}

const (
	logName   = "zapfast.log"
	panicName = "panic.log"
)

// CoreLog is the core library's log switch (*core.Lib).
type CoreLog interface {
	SetLog(f *os.File, level slog.Level) error
}

// Log is the run's log and panic file.
type Log struct {
	log       *os.File
	crash     *os.File
	crashPath string
	crashSize int64
	prev      *slog.Logger

	core sync.WaitGroup // the goroutine that gives the core the file
	mu   sync.Mutex     // guards lib
	lib  CoreLog        // set once the core writes here
}

// File is the log file, open for append: the core writes to a duplicate of
// its descriptor, so its lines and this process's share one offset and never
// overwrite each other. Nil when the log could not be opened.
func (l *Log) File() *os.File {
	if l == nil {
		return nil
	}
	return l.log
}

// StartLog replaces dir/zapfast.log with this run's log, which records
// warnings and errors (never message contents, numbers or keys: the code
// that logs names none, and every record is redacted on its way in,
// RedactHandler), makes it the default logger, and sends a crash's
// goroutine dump to dir/panic.log. When load is not nil, the core library's
// own log (warnings and errors) goes to the same file: load runs on a
// goroutine, since loading the library takes a while, and may be the loader
// the app opens its cores with (an older library without the log is fine, and
// said once). The returned Log is closed at quit. The error says what could
// not be set up; the Log it returns still works for what could.
func StartLog(dir string, load func() (CoreLog, error)) (*Log, error) {
	l := &Log{prev: slog.Default()}
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, logName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o600)
	if err != nil {
		note(err)
	} else {
		l.log = f
		slog.SetDefault(slog.New(RedactHandler(slog.NewTextHandler(io.MultiWriter(f, os.Stderr), &slog.HandlerOptions{Level: slog.LevelWarn}))))
		if load != nil {
			l.core.Add(1)
			go l.attachCore(load)
		}
	}
	// The crash file is kept between runs (a crash is read after it); a
	// clean run leaves it as it found it, and removes one it made empty.
	l.crashPath = filepath.Join(dir, panicName)
	c, err := os.OpenFile(l.crashPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		note(err)
		return l, first
	}
	if fi, err := c.Stat(); err == nil {
		l.crashSize = fi.Size()
	}
	if err := debug.SetCrashOutput(c, debug.CrashOptions{}); err != nil {
		note(err)
		c.Close()
		return l, first
	}
	l.crash = c
	return l, first
}

// attachCore gives the core the log file, from warnings up.
func (l *Log) attachCore(load func() (CoreLog, error)) {
	defer l.core.Done()
	lib, err := load()
	if err != nil {
		slog.Warn("the core's log is not wired", "err", err)
		return
	}
	switch err := lib.SetLog(l.log, slog.LevelWarn); {
	case err == nil:
		l.mu.Lock()
		l.lib = lib
		l.mu.Unlock()
	case errors.Is(err, errors.ErrUnsupported):
		slog.Warn("the core library is older than its log: its warnings are not recorded")
	default:
		slog.Warn("the core's log is not wired", "err", err)
	}
}

// Close ends the run's log: the core stops writing to it, and a clean run
// leaves no panic.log it made.
func (l *Log) Close() {
	if l == nil {
		return
	}
	l.core.Wait()
	l.mu.Lock()
	lib := l.lib
	l.lib = nil
	l.mu.Unlock()
	if lib != nil {
		_ = lib.SetLog(nil, slog.LevelWarn)
	}
	if l.crash != nil {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		if fi, err := l.crash.Stat(); err == nil && fi.Size() == l.crashSize && l.crashSize == 0 {
			os.Remove(l.crashPath)
		}
		l.crash.Close()
		l.crash = nil
	}
	if l.log != nil {
		slog.SetDefault(l.prev)
		l.log.Close()
		l.log = nil
	}
}
