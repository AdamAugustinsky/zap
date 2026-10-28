package kinds_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/ui/conversation/kinds"
)

// riskyDocuments lays out files that could run code when handed to the
// system, each passing for a document, and a PDF that may open.
func riskyDocuments(t *testing.T) (risky map[string]string, pdf string) {
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
	link := filepath.Join(dir, "Report.pdf")
	if os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755) != nil || os.Symlink(command, link) != nil {
		t.Fatal("the files")
	}
	return map[string]string{
		"terminal settings":              write("Settings.terminal", 0o644),
		"command":                        command,
		"web location":                   write("Site.webloc", 0o644),
		"app bundle":                     app,
		"script with the exec bit":       write("run.sh", 0o755),
		"symlink named pdf to a command": link,
	}, write("Notes.pdf", 0o644)
}

// A click on a document that could run code (whatever its name or MIME type
// say) never hands it to the system: it is shown in its folder with the note;
// a PDF opens (state.OpenFile).
func TestAClickOpensOnlyARegularDocumentOfAKnownFormat(t *testing.T) {
	risky, pdf := riskyDocuments(t)
	run := func(t *testing.T, path string) (opened string, revealed []string, toasts []string) {
		t.Helper()
		openedC, revealedC := make(chan string, 1), make(chan []string, 1)
		defer kinds.SetOpeners(func(s string) error { openedC <- s; return nil }, nil,
			func(p []string) error { revealedC <- p; return nil })()
		r := newRig(t, document("x", path, abi.MediaStateTagIdle))
		r.Play(widgettest.Click(ui.RoleButton, "Document"))
		select {
		case opened = <-openedC:
		case revealed = <-revealedC:
		case <-time.After(5 * time.Second):
			t.Fatal("the click did nothing")
		}
		for _, ts := range r.s.Toasts {
			toasts = append(toasts, string(ts.Text))
		}
		return opened, revealed, toasts
	}
	for name, path := range risky {
		t.Run(name, func(t *testing.T) {
			opened, revealed, toasts := run(t, path)
			if opened != "" || len(revealed) != 1 || revealed[0] != path {
				t.Errorf("opened %q, revealed %q", opened, revealed)
			}
			if len(toasts) != 1 || toasts[0] != "For safety, open this file yourself from its folder" {
				t.Errorf("toasts %q", toasts)
			}
		})
	}
	t.Run("a PDF", func(t *testing.T) {
		if opened, revealed, toasts := run(t, pdf); opened != pdf || revealed != nil || toasts != nil {
			t.Errorf("opened %q, revealed %q, toasts %q", opened, revealed, toasts)
		}
	})
}
