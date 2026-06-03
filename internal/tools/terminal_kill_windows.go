//go:build windows

package tools

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroupOpts is a no-op on Windows, which has no POSIX process groups.
// Child-tree termination is handled in killProcessGroup via taskkill instead.
func setProcessGroupOpts() *syscall.SysProcAttr { return nil }

// killProcessGroup terminates the process AND its whole child tree. A bare
// p.Kill() ends only the top-level shell (PowerShell), orphaning anything it
// spawned — a dev server, a test runner, a watch process. `taskkill /T` walks
// the tree and `/F` forces termination, matching the POSIX kill(-pgid) the
// darwin/linux build does. Falls back to a direct kill if taskkill can't run
// (not on PATH, or the process already exited), so termination stays
// best-effort either way.
func killProcessGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	if err := cmd.Run(); err != nil {
		return p.Kill()
	}
	return nil
}
