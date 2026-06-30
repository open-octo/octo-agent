//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow gives the child its own console but with no visible window.
// We deliberately avoid DETACHED_PROCESS (0x8) here: a detached child has *no*
// console at all, so when it spawns its own console-app children (the daemon
// child is the supervisor, which spawns the serve worker) Windows allocates a
// fresh *visible* console for each grandchild. With a windowless console the
// grandchildren inherit it and stay invisible. Combined with stdout/stderr
// redirected to a log file, the whole tree runs silently in the background.
const createNoWindow = 0x08000000

// setSysProcAttrDetach creates the child with a windowless console so neither it
// nor any console-app grandchild it spawns pops a terminal window, and it does
// not receive Ctrl-C signals sent to the parent's terminal.
func setSysProcAttrDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
	}
}

// isProcessAlive opens the process with PROCESS_QUERY_LIMITED_INFORMATION and
// calls GetExitCodeProcess; a return code of STILL_ACTIVE (259) means the
// process is alive.
func isProcessAlive(pid int) bool {
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

// terminateProcess kills the process and its entire child tree. The serve
// daemon is a process tree — the tracked supervisor pid plus the worker it
// spawns (and anything the worker starts) — and Windows has no signal the
// supervisor could forward to bring the tree down on a stop: superviseLoop's
// forward() is a no-op on Windows, and a plain TerminateProcess on the
// supervisor pid would orphan the worker, leaving it bound to the port and
// holding octo.exe locked against an in-place upgrade. taskkill /T walks and
// kills the whole tree; /F forces it (a console worker has no message loop to
// act on the WM_CLOSE that /T alone sends). This matches the tree-kill the
// terminal tool already uses (internal/tools/terminal_kill_windows.go). Falls
// back to killing just the supervisor if taskkill is somehow unavailable.
//
// Windows still has no graceful-stop API here; the round-granularity session
// persistence and SSE replay buffer cap the loss of a hard cut at one round.
func terminateProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(proc.Pid)).Run(); err != nil {
		return proc.Kill()
	}
	return nil
}
