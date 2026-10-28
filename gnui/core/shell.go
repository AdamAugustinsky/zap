package core

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"zapfast/abi"
	"zapfast/internal/testbin"
)

// Shell opens files for the user in their default application. The core
// never opens files itself; it asks with an OpenPath event. In the app it is
// cmd/zapfast's opener (app/shell for links, ExecShell for files); anywhere
// else it is a RefusingShell or a test's fake.
type Shell interface {
	Open(path string) error
}

// Revealer is a Shell that can also show files in their folder (Finder):
// what is not to be opened (a document that could run code) is shown
// there. A Shell without it shows nothing.
type Revealer interface {
	Reveal(paths []string) error
}

// ErrOpenRefused is a refusing opener's answer: nothing was opened.
var ErrOpenRefused = errors.New("zapfast: opening refused")

// RefusingShell is the Shell state.New starts with, until the app installs
// the real opener (cmd/zapfast): it records what it was asked to open, logs
// a line, and opens nothing, so a test without a fake never opens a real
// file, app or link.
type RefusingShell struct {
	mu       sync.Mutex
	attempts []string
}

// Open records path and refuses.
func (r *RefusingShell) Open(path string) error {
	r.mu.Lock()
	r.attempts = append(r.attempts, path)
	r.mu.Unlock()
	slog.Warn("zapfast: refused to open: no opener is installed", "path", path)
	return ErrOpenRefused
}

// Reveal records paths and refuses.
func (r *RefusingShell) Reveal(paths []string) error {
	r.mu.Lock()
	r.attempts = append(r.attempts, paths...)
	r.mu.Unlock()
	slog.Warn("zapfast: refused to show in its folder: no opener is installed", "paths", paths)
	return ErrOpenRefused
}

// Attempts is what it was asked to open or show, oldest first.
func (r *RefusingShell) Attempts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.attempts...)
}

// GuardOpen is ErrOpenRefused inside a Go test binary (its name ends in
// .test) unless ZAPFAST_ALLOW_OPEN=1: the real openers ask it first, so a
// test that reaches one still opens nothing.
func GuardOpen() error {
	if testbin.Is() && os.Getenv("ZAPFAST_ALLOW_OPEN") != "1" {
		return fmt.Errorf("%w: a test binary (set ZAPFAST_ALLOW_OPEN=1 to allow)", ErrOpenRefused)
	}
	return nil
}

// ExecShell opens a file with macOS's open(1), and when nothing opens it,
// shows it in its folder (open -R), as upstream's opener does.
type ExecShell struct{}

// runOpen runs open(1) with args; tests replace it.
var runOpen = func(args ...string) error { return exec.Command("/usr/bin/open", args...).Run() }

// Open opens path, or reveals it in Finder. It blocks until open(1) exits.
// It refuses inside a test binary (GuardOpen).
func (ExecShell) Open(path string) error {
	if err := GuardOpen(); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.ErrUnsupported
	}
	if err := runOpen("--", path); err == nil {
		return nil
	}
	if err := runOpen("-R", "--", path); err != nil {
		return fmt.Errorf("could not open %s or show it in its folder: %w", path, err)
	}
	return nil
}

// OpenPath answers an OpenPath event: it copies the path (the event's bytes
// are only valid during the Handler call) and opens it with sh on a
// goroutine, since opening can take seconds. done, if not nil, is called
// there with the path and the result.
func OpenPath(sh Shell, e *abi.EventOpenPath, done func(path string, err error)) {
	path := string(e.Path)
	go func() {
		err := sh.Open(path)
		if done != nil {
			done(path, err)
		}
	}()
}
