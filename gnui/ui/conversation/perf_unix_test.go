//go:build unix

package conversation

import "syscall"

// cpuNs is the process's CPU time (user and system), in nanoseconds.
func cpuNs() int64 {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return ru.Utime.Nano() + ru.Stime.Nano()
}
