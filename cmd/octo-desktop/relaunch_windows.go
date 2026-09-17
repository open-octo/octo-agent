package main

import (
	"os/exec"

	"github.com/open-octo/octo-agent/internal/executil"
)

// detachRelaunch keeps the replacement windowless. The desktop binary is built
// -H windowsgui and has no console to inherit, so there is no session to break
// away from the way there is on POSIX.
func detachRelaunch(cmd *exec.Cmd) {
	executil.SetNoWindow(cmd)
}
