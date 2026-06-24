//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedProcess tells Windows to create the child without inheriting the
// parent's console. Combined with stdout/stderr redirected to a log file, the
// process runs silently in the background with no terminal window.
const detachedProcess = 0x00000008

// setSysProcAttrDetach creates the child as a detached process so it does not
// inherit the parent console and does not receive Ctrl-C signals sent to the
// terminal.
func setSysProcAttrDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess,
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
