package main

import (
	"os"
	"path/filepath"
	"testing"

	"zapfast/state"
	"zapfast/sys"
)

// FL-005 at quit: macOS ends the process after the Terminate event, so
// main's deferred Close never runs. The hook ends the log itself, and a clean
// run leaves no panic.log.
func TestTerminateLeavesNoEmptyPanicLog(t *testing.T) {
	dir := t.TempDir()
	lg, err := sys.StartLog(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	terminate(state.New(), lg)()
	if _, err := os.Stat(filepath.Join(dir, "panic.log")); !os.IsNotExist(err) {
		t.Errorf("quitting left panic.log: %v", err)
	}
}
