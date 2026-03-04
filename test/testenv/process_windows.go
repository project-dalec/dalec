package testenv

import "os/exec"

// setSysProcAttr is a no-op on Windows; process-group isolation is not
// needed because Windows does not deliver console signals the same way.
func setSysProcAttr(cmd *exec.Cmd) {}

// killProcessGroup forcibly terminates the process on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.Process.Kill() //nolint:errcheck
}
