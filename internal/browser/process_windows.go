//go:build windows

package browser

import "os/exec"

// setProcessGroup is a no-op on Windows; process-tree handling differs and the
// plain Kill below is the pragmatic fallback.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the launched Chrome process.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
