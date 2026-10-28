package state_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"zapfast/state"
)

type riskyFile struct{ name, path string }

// riskyFiles lays out files that could run code when handed to the system,
// each named or made to pass for something else, and a PDF that may open.
func riskyFiles(t *testing.T) (risky []riskyFile, pdf string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho owned\n"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	command := write("run.command", 0o644)
	app := filepath.Join(dir, "Evil.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "Report.pdf")
	if err := os.Symlink(command, link); err != nil {
		t.Fatal(err)
	}
	risky = []riskyFile{
		{"terminal settings", write("Settings.terminal", 0o644)},
		{"command", command},
		{"web location", write("Site.webloc", 0o644)},
		{"app bundle", app},
		{"script with the exec bit", write("run.sh", 0o755)},
		{"symlink named pdf to a command", link},
	}
	return risky, write("Notes.pdf", 0o644)
}

// recShell records what it was asked to open and to show in its folder.
type recShell struct {
	mu               sync.Mutex
	opened, revealed []string
	done             chan struct{}
}

func newRecShell() *recShell { return &recShell{done: make(chan struct{}, 8)} }

func (r *recShell) Open(p string) error {
	r.mu.Lock()
	r.opened = append(r.opened, p)
	r.mu.Unlock()
	r.done <- struct{}{}
	return nil
}

func (r *recShell) Reveal(p []string) error {
	r.mu.Lock()
	r.revealed = append(r.revealed, p...)
	r.mu.Unlock()
	r.done <- struct{}{}
	return nil
}

func (r *recShell) wait(t *testing.T) (opened, revealed []string) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was opened or shown in its folder")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.opened...), append([]string(nil), r.revealed...)
}

const safetyNote = "For safety, open this file yourself from its folder"

// A file that could run code is never handed to its application, whatever
// its name or the sender's MIME type say: State.OpenFile, the one route,
// shows it in its folder with the note (zf:src/app.rs Action::OpenFile). A
// regular file of a known format opens.
func TestOnlyARegularFileOfAKnownFormatOpens(t *testing.T) {
	risky, pdf := riskyFiles(t)
	for _, f := range risky {
		t.Run(f.name, func(t *testing.T) {
			s := state.New()
			sh := newRecShell()
			s.Shell = sh
			s.OpenFile(f.path)
			opened, revealed := sh.wait(t)
			if len(opened) != 0 || len(revealed) != 1 || revealed[0] != f.path {
				t.Errorf("opened %q, revealed %q", opened, revealed)
			}
			if n := len(s.Toasts); n != 1 || string(s.Toasts[0].Text) != safetyNote || s.Toasts[0].Error {
				t.Errorf("toasts %+v", s.Toasts)
			}
		})
	}
	t.Run("a PDF", func(t *testing.T) {
		s := state.New()
		sh := newRecShell()
		s.Shell = sh
		s.OpenFile(pdf)
		opened, revealed := sh.wait(t)
		if len(opened) != 1 || opened[0] != pdf || len(revealed) != 0 || len(s.Toasts) != 0 {
			t.Errorf("opened %q, revealed %q, toasts %+v", opened, revealed, s.Toasts)
		}
	})
}
