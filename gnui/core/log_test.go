package core

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetLogSharesTheAppsLogFile(t *testing.T) {
	l := loadLib(t)
	path := filepath.Join(t.TempDir(), "zapfast.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("time=x level=WARN msg=\"the app's own line\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetLog(f, slog.LevelWarn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.SetLog(nil, slog.LevelWarn) })
	if _, err := f.WriteString("time=y level=WARN msg=\"the app again\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetLog(nil, slog.LevelWarn); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "the app's own line") ||
		!strings.Contains(lines[1], `level=INFO source=core target=zapfast_core msg="the core logs here, from WARN"`) ||
		!strings.Contains(lines[2], "the app again") {
		t.Fatalf("the shared log:\n%s", b)
	}
	if err := (&Lib{}).SetLog(f, slog.LevelWarn); err != ErrUnsupported {
		t.Fatalf("an old library: %v", err)
	}
	if coreLevel(slog.LevelDebug) != 4 || coreLevel(slog.LevelInfo) != 3 || coreLevel(slog.LevelWarn) != 2 || coreLevel(slog.LevelError) != 1 || coreLevel(slog.LevelDebug-4) != 5 {
		t.Fatal("levels")
	}
}
