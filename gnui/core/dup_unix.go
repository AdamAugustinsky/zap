//go:build unix

package core

import "syscall"

// dupCloexec duplicates fd, close-on-exec.
func dupCloexec(fd int) (int, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	dup, err := syscall.Dup(fd)
	if err == nil {
		syscall.CloseOnExec(dup)
	}
	return dup, err
}
