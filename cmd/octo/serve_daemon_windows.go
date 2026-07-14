//go:build windows

package main

import (
	"os/exec"

	"github.com/open-octo/octo-agent/internal/executil"
)

// setSysProcAttrDetach creates the child with a windowless console so neither it
// nor any console-app grandchild it spawns pops a terminal window, and it does
// not receive Ctrl-C signals sent to the parent's terminal.
//
// We deliberately avoid DETACHED_PROCESS (0x8) here: a detached child has *no*
// console at all, so when it spawns its own console-app children (the daemon
// child is the supervisor, which spawns the serve worker) Windows allocates a
// fresh *visible* console for each grandchild. executil.SetNoWindow (which
// uses CREATE_NO_WINDOW) gives the child its own console but with no visible
// window, so grandchildren inherit it and stay invisible. Combined with
// stdout/stderr redirected to a log file, the whole tree runs silently in the
// background.
func setSysProcAttrDetach(cmd *exec.Cmd) {
	executil.SetNoWindow(cmd)
}
