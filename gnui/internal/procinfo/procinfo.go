// Package procinfo reads how long ago the kernel created this process, for
// exec-relative startup times (the framework keeps its own reader internal).
package procinfo

import "time"

// Usage is the process's resource usage so far (Read).
type Usage struct {
	// Footprint is phys_footprint, Activity Monitor's "Memory"; Resident
	// the resident size, shared pages included.
	Footprint, Resident uint64
	// CPUNs is user and system CPU time.
	CPUNs int64
	// IdleWakeups counts the wakeups that took a CPU package out of idle
	// (Activity Monitor's "Idle Wake Ups"); Wakeups counts every interrupt
	// wakeup of the process's threads.
	IdleWakeups, Wakeups uint64
}

// ExecTime returns when the process was created, on the monotonic clock of
// time.Now, so that t.Sub(ExecTime()) is exec → t. ok is false where the
// age cannot be read.
func ExecTime() (t time.Time, ok bool) {
	now := time.Now()
	age := AgeNs()
	if age < 0 {
		return now, false
	}
	return now.Add(-time.Duration(age)), true
}
