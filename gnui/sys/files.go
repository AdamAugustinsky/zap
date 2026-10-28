package sys

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

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

// Log is the run's log and panic file.
type Log struct {
	log       *os.File
	crash     *os.File
	crashPath string
	crashSize int64
	prev      *slog.Logger
}

// File is the log file, open for append. Nil when the log could not be
// opened.
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
// goroutine dump to dir/panic.log. The backend logs through slog, so its
// lines come here too. The returned Log is closed at quit. The error says what could
// not be set up; the Log it returns still works for what could.
func StartLog(dir string) (*Log, error) {
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

// Close ends the run's log; a clean run leaves no panic.log it made.
func (l *Log) Close() {
	if l == nil {
		return
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
