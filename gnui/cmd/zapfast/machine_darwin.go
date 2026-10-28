//go:build darwin && !gnui_release

package main

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
)

// machine names this computer, for the reports.
func machine() string {
	model, _ := syscall.Sysctl("hw.model")
	cpu, _ := syscall.Sysctl("machdep.cpu.brand_string")
	ver, _ := syscall.Sysctl("kern.osproductversion")
	return strings.TrimSpace(fmt.Sprintf("%s, %s, macOS %s, %s/%s, %d CPUs", model, cpu, ver, runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))
}
