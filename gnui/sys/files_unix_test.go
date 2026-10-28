//go:build unix

package sys

import "syscall"

// canDup: the core library shares the app's log through dup(2), on unix.
const canDup = true

func dupFd(fd int) (int, error) { return syscall.Dup(fd) }
