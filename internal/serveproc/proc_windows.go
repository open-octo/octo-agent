//go:build windows

package serveproc

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/open-octo/octo-agent/internal/executil"
)

// IsAlive opens the process with PROCESS_QUERY_LIMITED_INFORMATION and calls
// GetExitCodeProcess; a return code of STILL_ACTIVE (259) means the process is
// alive.
func IsAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	const stillActive = 259
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}

// Terminate kills the process and its entire child tree. The serve daemon is a
// process tree — the tracked supervisor pid plus the worker it spawns (and
// anything the worker starts) — and Windows has no signal the supervisor could
// forward to bring the tree down on a stop: a plain TerminateProcess on the
// supervisor pid would orphan the worker, leaving it bound to the port and
// holding octo.exe locked against an in-place upgrade. taskkill /T walks and
// kills the whole tree; /F forces it (a console worker has no message loop to
// act on the WM_CLOSE that /T alone sends). Falls back to killing just the
// tracked process if taskkill is somehow unavailable.
func Terminate(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(proc.Pid))
	executil.SetNoWindow(cmd)
	if err := cmd.Run(); err != nil {
		return proc.Kill()
	}
	return nil
}
