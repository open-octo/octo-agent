//go:build darwin || linux

package tools

import (
	"os"
	"syscall"
)

// setProcessGroupOpts sets SysProcAttr so the child becomes a new process
// group leader. This lets killProcessGroup send a signal to the entire
// group (the shell and any processes it spawns).
func setProcessGroupOpts() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills the process group rooted at p.
// On POSIX it sends SIGKILL to -Pid (the whole group).
func killProcessGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}
