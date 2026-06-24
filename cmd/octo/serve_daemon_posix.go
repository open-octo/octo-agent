//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// setSysProcAttrDetach creates a new session so the child is not attached to
// the parent's controlling terminal. The child becomes its own session leader.
func setSysProcAttrDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// isProcessAlive sends signal 0 to the process — this checks whether the
// process exists without disturbing it.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// terminateProcess sends SIGTERM for a graceful shutdown.
func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
