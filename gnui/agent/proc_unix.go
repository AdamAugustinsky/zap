//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// ownGroup puts cmd in a process group of its own and makes its end (the
// run's time is up, the owner turned AI access off) the end of the whole
// group: what a runner started goes with it, a shell's pipeline included.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// endGroup ends what is left of cmd's group once cmd itself is gone: a
// child it left in the background.
func endGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
