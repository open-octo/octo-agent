//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachRelaunch puts the replacement process in its own session so it does not
// die with, or share a controlling terminal with, the one it is replacing.
func detachRelaunch(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
