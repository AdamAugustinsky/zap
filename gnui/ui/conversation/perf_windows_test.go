//go:build windows

package conversation

import "syscall"

// cpuNs is the process's CPU time (kernel and user), in nanoseconds.
func cpuNs() int64 {
	var created, exited, kernel, user syscall.Filetime
	p, _ := syscall.GetCurrentProcess()
	if syscall.GetProcessTimes(p, &created, &exited, &kernel, &user) != nil {
		return 0
	}
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return (ticks(kernel) + ticks(user)) * 100 // 100 ns units
}
