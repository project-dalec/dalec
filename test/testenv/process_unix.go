//go:build !windows

package testenv

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr puts the child process into its own process group so it does
// not receive SIGINT from the terminal on Ctrl+C.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup force-kills the entire process group (negative PID) since
// Setpgid puts the child and any subprocesses it spawns into their own group.
func killProcessGroup(cmd *exec.Cmd) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
}
