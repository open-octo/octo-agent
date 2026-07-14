//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/open-octo/octo-agent/internal/executil"
)

// setProcessGroupOpts is a no-op on Windows, which has no POSIX process groups.
// Child-tree termination is handled in killProcessGroup via taskkill instead.
func setProcessGroupOpts() *syscall.SysProcAttr { return nil }

// Windows process-creation flags for a fully detached daemon. DETACHED_PROCESS
// gives the child no console; CREATE_NEW_PROCESS_GROUP makes it the root of a
// new group so it isn't tied to the harness's console/Ctrl events.
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// setDetachedProcessOpts starts the child detached from the harness console in
// its own process group, so it survives session exit — the Windows counterpart
// of setsid, used for terminal detached:true.
func setDetachedProcessOpts() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}

// processExists checks whether a process with the given PID is still running.
func processExists(pid int) bool {
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
	executil.SetNoWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

// killProcessGroup terminates the process AND its whole child tree.
// For SIGKILL (or any unknown signal) it forces termination with taskkill /F.
// For SIGTERM/SIGINT it first attempts a graceful shutdown (taskkill /T without
// /F, which sends WM_CLOSE), waits up to 5 seconds, and falls back to force if
// the process is still running.
func killProcessGroup(p *os.Process, sigName string) error {
	if p == nil {
		return nil
	}

	force := sigName == "SIGKILL"
	if !force {
		// Graceful attempt: send WM_CLOSE to the process tree.
		cmd := exec.Command("taskkill", "/T", "/PID", strconv.Itoa(p.Pid))
		executil.SetNoWindow(cmd)
		_ = cmd.Run()
		// Wait up to 5s for the process to exit.
		for i := 0; i < 50; i++ {
			if !processExists(p.Pid) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Fall through to force kill.
	}

	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	executil.SetNoWindow(cmd)
	if err := cmd.Run(); err != nil {
		return p.Kill()
	}
	return nil
}
