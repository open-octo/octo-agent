package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

// serverGuardOn, when set, means this process is an octo server (octo serve).
// The terminal tool then refuses shell commands that would kill the server
// itself — the model must use the restart_server tool for a graceful restart
// instead of terminating the process out from under its own turn (which also
// drops the user's web/IM session).
var serverGuardOn atomic.Bool

// serverSelfPID / serverSuperPID are the process and its parent (the restart
// supervisor, when present). Killing either takes the service down, so both
// are protected. Captured once at package init — a process never changes its
// own or parent PID.
var (
	serverSelfPID  = os.Getpid()
	serverSuperPID = os.Getppid()
)

// SetServerGuard enables or disables the octo-serve self-kill guard. The
// server turns it on at start and off at shutdown.
func SetServerGuard(on bool) { serverGuardOn.Store(on) }

var (
	// pkill/killall … octo — signalling octo processes by name. `\bocto\b`
	// matches "octo", "octo serve", and "octo-agent" but not unrelated names
	// like "octoprint"/"octopus". The `\bkill\b` word boundary does not match
	// inside "pkill"/"killall", so reKill below stays limited to a bare `kill`.
	reKillByName = regexp.MustCompile(`(?i)\b(pkill|killall)\b[^|;&\n]*\bocto\b`)
	// A bare `kill [-SIG] <pid> …` command; its argument tail is scanned for
	// the protected PIDs.
	reKill = regexp.MustCompile(`(?i)\bkill\b([^|;&\n]*)`)
	reNum  = regexp.MustCompile(`\b\d+\b`)
)

// guardServerSelfKill returns a non-nil error when command, run inside an octo
// server process, would terminate that server (or its supervisor). It is a
// best-effort textual guard over the common vectors — `pkill/killall octo` and
// `kill <server-pid>` — not a sandbox; it exists to stop the model from
// reflexively killing the process it is hosted by.
func guardServerSelfKill(command string) error {
	if !serverGuardOn.Load() {
		return nil
	}
	if reKillByName.MatchString(command) {
		return errServerSelfKill()
	}
	// `kill $(pgrep octo)` / `kill $(pidof octo)` — resolve-then-kill by name.
	if reKill.MatchString(command) && strings.Contains(command, "octo") &&
		(strings.Contains(command, "pgrep") || strings.Contains(command, "pidof")) {
		return errServerSelfKill()
	}
	self := strconv.Itoa(serverSelfPID)
	super := strconv.Itoa(serverSuperPID)
	for _, seg := range reKill.FindAllStringSubmatch(command, -1) {
		for _, n := range reNum.FindAllString(seg[1], -1) {
			if n == self || n == super {
				return errServerSelfKill()
			}
		}
	}
	return nil
}

// errServerSelfKill is returned by guardServerSelfKill when the model tries to
// kill the octo server process hosting the current session. The message
// branches on restarter availability: if a restarter is registered the
// restart_server tool works; otherwise (desktop build) it doesn't.
func errServerSelfKill() error {
	if restarterEnabled() {
		return errorServerSelfKillServe
	}
	return errorServerSelfKillDesktop
}

var (
	errorServerSelfKillServe = fmt.Errorf("refusing to kill the octo server process that is hosting this "+
		"session — use the restart_server tool for a graceful restart (it drains in-flight turns and "+
		"lets the supervisor respawn the server)")
	errorServerSelfKillDesktop = fmt.Errorf("refusing to kill the octo server process that is hosting this "+
		"session — the desktop build has no supervisor to restart via tool; reload channel configs "+
		"via POST /api/channels/<platform>/reload, or restart the app to apply other changes")
)
