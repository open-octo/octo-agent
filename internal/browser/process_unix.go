//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the launched Chrome in its own process group so the whole
// tree (renderers, gpu, …) can be signalled together.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills the entire Chrome process group, not just the parent —
// otherwise child processes are orphaned and keep holding the profile dir.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
