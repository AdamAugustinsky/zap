//go:build !darwin && !gnui_release

package main

import (
	"fmt"
	"runtime"
)

// machine names this computer, for the reports: its model and CPU are read
// on macOS only so far.
func machine() string {
	return fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}
