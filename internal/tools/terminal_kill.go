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

// setDetachedProcessOpts starts the child in a NEW session (setsid), making it
// a session+group leader detached from the harness. Unlike a Setpgid child, the
// harness's kill(-pgid) can't reach it, so it survives session exit — used for
// deliberately detached daemons (terminal detached:true).
func setDetachedProcessOpts() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
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
