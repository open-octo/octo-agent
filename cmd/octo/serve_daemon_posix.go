//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setSysProcAttrDetach creates a new session so the child is not attached to
// the parent's controlling terminal. The child becomes its own session leader.
func setSysProcAttrDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
