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

// killProcessGroup sends the named signal to the process group rooted at p.
// Supported names: SIGTERM, SIGINT, SIGKILL (default). On POSIX it sends the
// signal to -Pid (the whole group).
func killProcessGroup(p *os.Process, sigName string) error {
	var sig syscall.Signal
	switch sigName {
	case "SIGTERM":
		sig = syscall.SIGTERM
	case "SIGINT":
		sig = syscall.SIGINT
	default: // SIGKILL
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-p.Pid, sig)
}
