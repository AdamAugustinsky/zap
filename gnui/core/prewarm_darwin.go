//go:build darwin

package core

import "syscall"

// prewarm makes macOS do the expensive part of a library's first load
// outside dyld's lock. The first executable mapping of a newly written file
// costs about 220 ms in the kernel (once per file, then cached), and inside
// dlopen it holds dyld's lock all that time, which stalls the main thread's
// own loading (opening the window took 435 ms instead of 87 in a cold
// start). Attempting a one-page executable mapping here pays that cost on
// this goroutine alone: the attempt is refused (EPERM, the file's signature
// is not registered yet), but the check is done, and dlopen then takes
// about 4 ms. It is only a hint: whatever it returns, dlopen follows.
func prewarm(path string) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	defer syscall.Close(fd)
	if b, err := syscall.Mmap(fd, 0, 16384, syscall.PROT_READ|syscall.PROT_EXEC, syscall.MAP_PRIVATE); err == nil {
		syscall.Munmap(b)
	}
}
