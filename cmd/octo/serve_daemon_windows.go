//go:build windows

package main

import (
	"os"
	"os/exec"
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

// terminateProcess kills the process. Windows has no SIGTERM equivalent that
// provides a graceful shutdown via the standard API, so we use Kill which
// sends WM_CLOSE / TerminateProcess. The server's graceful shutdown on
// interrupt is still reachable via /api/restart or the web UI stop button.
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}
