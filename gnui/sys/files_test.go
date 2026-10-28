package sys

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"zapfast/core"
	"zapfast/state"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// FL-002: the configuration, state and cache folders are the user's alone;
// ones that exist wider are repaired without touching what is in them.
func TestTheFoldersAreRestrictedToTheUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the profile's permissions apply")
	}
	root := t.TempDir()
	p := state.Paths{Config: filepath.Join(root, "cfg"), Data: filepath.Join(root, "data", "app"), Cache: filepath.Join(root, "cache")}
	if err := os.MkdirAll(p.Cache, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(p.Cache, "kept.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Restrict(p); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Config, p.Data, p.Cache} {
		if got := mode(t, d); got != 0o700 {
			t.Errorf("%s is %o", d, got)
		}
	}
	if got := mode(t, keep); got != 0o644 {
		t.Errorf("the file inside was changed: %o", got)
	}
	if b, _ := os.ReadFile(keep); string(b) != "x" {
		t.Error("the file inside was changed")
	}
}

func TestStartupStopsWhenAFolderCannotBeMadePrivate(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "in-the-way")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Restrict(state.Paths{Config: filepath.Join(file, "cfg"), Data: root, Cache: root})
	if err == nil || !strings.Contains(err.Error(), "in-the-way") {
		t.Errorf("%v", err)
	}
}

// FL-001: each number has its own folders under the app's own roots.
func TestEachNumberHasItsOwnFoldersUnderTheAppsOwnRoots(t *testing.T) {
	d1, c1, err := state.AccountDirs("1")
	if err != nil {
		t.Skip(err)
	}
	d2, c2, _ := state.AccountDirs("2")
	if d1 == d2 || c1 == c2 || filepath.Base(d1) != "1" || filepath.Base(c2) != "2" {
		t.Errorf("%s %s %s %s", d1, c1, d2, c2)
	}
	for _, d := range []string{d1, c1} {
		if !strings.Contains(d, state.AppID) || strings.Contains(d, "rocks.zapfast") {
			t.Errorf("%s is not the app's own", d)
		}
	}
	if d1 == c1 {
		t.Error("the cache is the state")
	}
}

// FL-004: each run replaces the log, which holds warnings and errors.
func TestEachRunReplacesTheLogWhichHoldsWarningsAndErrors(t *testing.T) {
	dir := t.TempDir()
	l, err := StartLog(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	slog.Info("a detail")
	slog.Warn("the first run warns")
	l.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "zapfast.log"))
	if !strings.Contains(string(b), "the first run warns") || strings.Contains(string(b), "a detail") {
		t.Fatalf("%q", b)
	}
	l, err = StartLog(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	slog.Error("the second run fails")
	l.Close()
	b, _ = os.ReadFile(filepath.Join(dir, "zapfast.log"))
	if strings.Contains(string(b), "first") || !strings.Contains(string(b), "the second run fails") {
		t.Errorf("%q", b)
	}
	if got := mode(t, filepath.Join(dir, "zapfast.log")); runtime.GOOS != "windows" && got != 0o600 {
		t.Errorf("the log is %o", got)
	}
}

// FL-005: a clean run leaves no panic.log; an earlier crash's stays.
func TestACleanRunLeavesNoPanicLogAndKeepsAnEarlierOne(t *testing.T) {
	dir := t.TempDir()
	l, _ := StartLog(dir, nil)
	l.Close()
	if _, err := os.Stat(filepath.Join(dir, "panic.log")); !os.IsNotExist(err) {
		t.Errorf("a clean run left panic.log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "panic.log"), []byte("old crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, _ = StartLog(dir, nil)
	l.Close()
	if b, _ := os.ReadFile(filepath.Join(dir, "panic.log")); string(b) != "old crash" {
		t.Errorf("%q", b)
	}
}

// FL-005: a panic is written to panic.log, by a process that really panics.
func TestAPanicIsWrittenToPanicLog(t *testing.T) {
	if dir := os.Getenv("ZAPFAST_PANIC_HELPER"); dir != "" {
		StartLog(dir, nil)
		panic("the sky fell")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAPanicIsWrittenToPanicLog$")
	cmd.Env = append(os.Environ(), "ZAPFAST_PANIC_HELPER="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("the helper did not panic")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "panic.log"))
	if !strings.Contains(string(b), "the sky fell") || !strings.Contains(string(b), "goroutine") {
		t.Errorf("%q", b)
	}
}

// dupWriter is a core log that writes through a duplicate of the app's log
// descriptor, as the core library does.
type dupWriter struct {
	dup *os.File
	err error
}

func (d *dupWriter) SetLog(f *os.File, level slog.Level) error {
	if d.err != nil {
		return d.err
	}
	if f == nil {
		if d.dup != nil {
			d.dup.Close()
			d.dup = nil
		}
		return nil
	}
	fd, err := dupFd(int(f.Fd()))
	if err != nil {
		return err
	}
	d.dup = os.NewFile(uintptr(fd), "core-log")
	return nil
}

func TestTheCoresLinesAndTheAppsShareOneFileWithoutOverwritingEachOther(t *testing.T) {
	if !canDup {
		t.Skip("the core library shares the app's log on unix only")
	}
	dir := t.TempDir()
	core := &dupWriter{}
	l, err := StartLog(dir, func() (CoreLog, error) { return core, nil })
	if err != nil {
		t.Fatal(err)
	}
	l.core.Wait() // the core has the file
	for i := 0; i < 3; i++ {
		slog.Warn("app line", "n", i)
		if _, err := core.dup.WriteString("core line " + string(rune('0'+i)) + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	if core.dup != nil {
		t.Error("the core still writes after Close")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "zapfast.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 6 {
		t.Fatalf("%d lines:\n%s", len(lines), b)
	}
	for i := 0; i < 3; i++ {
		if !strings.Contains(lines[2*i], "app line") || lines[2*i+1] != "core line "+string(rune('0'+i)) {
			t.Fatalf("lines mixed or overwritten:\n%s", b)
		}
	}
}

func TestACoreLibraryWithoutALogIsSaidOnceAndNotAnError(t *testing.T) {
	dir := t.TempDir()
	l, err := StartLog(dir, func() (CoreLog, error) { return &dupWriter{err: core.ErrUnsupported}, nil })
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "zapfast.log"))
	if strings.Count(string(b), "older than its log") != 1 {
		t.Errorf("%q", b)
	}
	l, _ = StartLog(dir, func() (CoreLog, error) { return nil, errors.New("no library") })
	l.Close()
	b, _ = os.ReadFile(filepath.Join(dir, "zapfast.log"))
	if !strings.Contains(string(b), "the core's log is not wired") || !strings.Contains(string(b), "no library") {
		t.Errorf("%q", b)
	}
}

// FL-004 with the real library: the core's own line reaches the file between
// the app's, whole.
func TestTheRealCoreWritesIntoTheAppsLog(t *testing.T) {
	path, err := core.FindLibrary()
	if err == nil {
		_, err = os.Stat(path) // $ZAPFAST_CORE is taken as given
	}
	if err != nil {
		t.Skipf("no core library: %v", err)
	}
	dir := t.TempDir()
	l, err := StartLog(dir, func() (CoreLog, error) { return core.Load(path) })
	if err != nil {
		t.Fatal(err)
	}
	slog.Warn("app line before")
	l.core.Wait()
	slog.Warn("app line after")
	l.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "zapfast.log"))
	text := string(b)
	if strings.Contains(text, "older than its log") {
		t.Skipf("the library at %s has no zf_set_log", path)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var coreAt, afterAt = -1, -1
	for i, ln := range lines {
		switch {
		case strings.Contains(ln, "source=core") && strings.Contains(ln, "the core logs here, from WARN"):
			coreAt = i
		case strings.Contains(ln, "app line after"):
			afterAt = i
		}
		if !strings.HasPrefix(ln, "time=") {
			t.Errorf("a line was cut or overwritten: %q", ln)
		}
	}
	if len(lines) != 3 || coreAt < 0 || afterAt < coreAt || !strings.Contains(text, "app line before") {
		t.Errorf("the shared log:\n%s", text)
	}
}
