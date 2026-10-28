package conversation

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// recShell records what it was asked to open and to show in its folder.
type recShell struct {
	mu               sync.Mutex
	opened, revealed []string
	done             chan struct{}
}

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

// fileMessage is a document from Ada at path, which its sender says is a PDF.
func fileMessage(id string, ts int64, path string) abi.Message {
	m := msg(ada, id, ts, false, "")
	m.Content = abi.Content{Tag: abi.ContentTagDocument}
	d := &m.Content.Document
	d.FileName = []byte(filepath.Base(path))
	d.Media = abi.Media{Mime: []byte("application/pdf"), Size: 1234, Path: []byte(path), HasPath: true}
	return m
}

// The context menu's Open file hands the system nothing that could run code,
// whatever its name or MIME type say: it goes through state.OpenFile, which
// shows such a file in its folder with the note; a PDF opens.
func TestTheMenusOpenFileOpensOnlyARegularFileOfAKnownFormat(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho owned\n"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	command := write("run.command", 0o644)
	app, link := filepath.Join(dir, "Evil.app"), filepath.Join(dir, "Report.pdf")
	if os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755) != nil || os.Symlink(command, link) != nil {
		t.Fatal("the files")
	}
	pdf := write("Notes.pdf", 0o644)
	for _, c := range []struct{ name, path string }{
		{"terminal settings", write("Settings.terminal", 0o644)},
		{"command", command},
		{"web location", write("Site.webloc", 0o644)},
		{"app bundle", app},
		{"script with the exec bit", write("run.sh", 0o755)},
		{"symlink named pdf to a command", link},
		{"a PDF", pdf},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			sh := &recShell{done: make(chan struct{}, 4)}
			r.s.Shell = sh
			r.open(ada, fileMessage("f1", base0, c.path))
			r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2))
			r.must(ui.RoleButton, "Open file")
			r.Play(widgettest.Click(ui.RoleButton, "Open file"), widgettest.Idle(2))
			select {
			case <-sh.done:
			case <-time.After(5 * time.Second):
				t.Fatal("Open file did nothing")
			}
			sh.mu.Lock()
			opened, revealed := sh.opened, sh.revealed
			sh.mu.Unlock()
			if c.path == pdf {
				if len(opened) != 1 || opened[0] != pdf || len(revealed) != 0 {
					t.Errorf("opened %q, revealed %q", opened, revealed)
				}
				return
			}
			if len(opened) != 0 || len(revealed) != 1 || revealed[0] != c.path {
				t.Errorf("opened %q, revealed %q", opened, revealed)
			}
			if n := len(r.s.Toasts); n == 0 || string(r.s.Toasts[n-1].Text) != "For safety, open this file yourself from its folder" {
				t.Errorf("toasts %+v", r.s.Toasts)
			}
		})
	}
}
