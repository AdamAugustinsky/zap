//go:build !gnui_release

package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
)

// startCPUProfile writes a CPU profile to path until the returned function
// is called ("": none).
func startCPUProfile(path string) func() {
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		return func() {}
	}
	runtime.SetCPUProfileRate(1000) // StartCPUProfile keeps a rate set before it: 1 ms samples for a short startup
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		f.Close()
		return func() {}
	}
	return func() { pprof.StopCPUProfile(); f.Close() }
}

// writeHeapProfile writes the heap profile to path, after a collection.
func writeHeapProfile(path string) {
	runtime.GC()
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		return
	}
	defer f.Close()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
	}
}
