//go:build !gnui_release

package main

import (
	"fmt"
	"os"
	"runtime"
)

// -bench -allocsites: profile every allocation and, if the measured frames
// allocate, print the allocating call stacks (cmd/pilot's diagnosis).

type allocSites struct {
	before map[[32]uintptr]int64
}

func (s *allocSites) start() {
	runtime.MemProfileRate = 1
	s.before = allocProfile()
}

func (s *allocSites) end() {
	after := allocProfile()
	first := true
	for stk, n := range after {
		d := n - s.before[stk]
		if d <= 0 {
			continue
		}
		pcs := stk[:]
		for i, pc := range pcs {
			if pc == 0 {
				pcs = pcs[:i]
				break
			}
		}
		if ownProfiling(pcs) {
			continue
		}
		if first {
			fmt.Fprintln(os.Stderr, "zapfast: allocations during the measured frames:")
			first = false
		}
		fmt.Fprintf(os.Stderr, "  %d object(s) at:\n", d)
		frames := runtime.CallersFrames(pcs)
		for {
			f, more := frames.Next()
			fmt.Fprintf(os.Stderr, "    %s\n        %s:%d\n", f.Function, f.File, f.Line)
			if !more {
				break
			}
		}
	}
}

// allocProfile returns cumulative allocation counts per call stack, as of
// the latest GC cycles (the runtime publishes profile data at GC).
func allocProfile() map[[32]uintptr]int64 {
	runtime.GC()
	runtime.GC()
	recs := make([]runtime.MemProfileRecord, 1024)
	n, ok := runtime.MemProfile(recs, true)
	for !ok {
		recs = make([]runtime.MemProfileRecord, n+256)
		n, ok = runtime.MemProfile(recs, true)
	}
	m := make(map[[32]uintptr]int64, n)
	for _, r := range recs[:n] {
		m[r.Stack0] += r.AllocObjects
	}
	return m
}

func ownProfiling(pcs []uintptr) bool {
	frames := runtime.CallersFrames(pcs)
	for {
		f, more := frames.Next()
		if f.Function == "main.allocProfile" {
			return true
		}
		if !more {
			return false
		}
	}
}
