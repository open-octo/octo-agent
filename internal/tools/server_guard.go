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
	// reNum matches a PID argument inside a kill tail: a digit run that is NOT
	// immediately preceded by '-'. A leading '-' marks a signal spec (-9) or a
	// negative process-group argument (-1), not a target PID, so `kill -9 -1`
	// (a phrase that shows up verbatim in commit messages and docs) no longer
	// has its "1" scanned. RE2 has no lookbehind, so the preceding char is
	// consumed by (?:^|[^-\w]) and the PID is submatch[1]. `[^-\w]` (rather than
	// a bare boundary) also keeps digits glued to a word — e.g. "octo123" — from
	// matching, matching the old `\b\d+\b` behavior for that case.
	reNum = regexp.MustCompile(`(?:^|[^-\w])(\d+)\b`)
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
	// Protected PIDs: this process and its supervisor parent. A PPID of 1 means
	// the server was reparented to init/launchd (daemonized, or GUI-launched by
	// the desktop app) rather than run under a real restart supervisor. Such a
	// parent is unkillable and, worse, matching a bare "1" false-positives on
	// any command text that happens to contain the digit — so PPID 1 is not
	// protected.
	protected := map[string]bool{strconv.Itoa(serverSelfPID): true}
	if serverSuperPID > 1 {
		protected[strconv.Itoa(serverSuperPID)] = true
	}
	for _, seg := range reKill.FindAllStringSubmatch(command, -1) {
		for _, m := range reNum.FindAllStringSubmatch(seg[1], -1) {
			if protected[m[1]] {
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
	errorServerSelfKillServe = fmt.Errorf("refusing to kill the octo server process that is hosting this " +
		"session — use the restart_server tool for a graceful restart (it drains in-flight turns and " +
		"lets the supervisor respawn the server)")
	errorServerSelfKillDesktop = fmt.Errorf("refusing to kill the octo server process that is hosting this " +
		"session — the desktop build has no supervisor to restart via tool; reload channel configs " +
		"via POST /api/channels/<platform>/reload, or restart the app to apply other changes")
)
