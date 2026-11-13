//go:build !unix

package agent

import "os/exec"

// ownGroup and endGroup do nothing here: the door is a Unix socket, and
// no run starts where there is none.
func ownGroup(*exec.Cmd) {}
func endGroup(*exec.Cmd) {}
