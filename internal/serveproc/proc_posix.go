//go:build !windows

package serveproc

import (
	"os"
	"syscall"
)

// IsAlive sends signal 0 to the process — this checks whether the process
// exists without disturbing it.
func IsAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Terminate sends SIGTERM for a graceful shutdown.
func Terminate(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Signal(syscall.SIGTERM)
}
