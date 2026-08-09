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

// ServerGuardEnvActive reports whether the current process was spawned from
// a guarded octo server's terminal tool — i.e. guardEnv armed it with
// OCTO_SERVER_PID (scrubGuardEnv guarantees the variable never leaks into
// plain CLI/TUI usage). cmd/octo's stopDaemon uses it to refuse
// `octo serve stop` issued by an agent-hosted shell: stopping the daemon
// from there kills the very server hosting the session, which must go
// through the restart_server tool instead.
func ServerGuardEnvActive() bool { return os.Getenv("OCTO_SERVER_PID") != "" }

// ServerGuardEnvMessage returns the refusal text the hosting server computed
// for its own build when it armed the guard env (OCTO_GUARD_MSG): it points
// at restart_server on serve and at the reload/restart alternatives on
// desktop — a distinction a nested CLI process cannot make on its own.
// Empty outside guarded-server shells.
func ServerGuardEnvMessage() string { return os.Getenv("OCTO_GUARD_MSG") }

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
	// reNegNum matches a negative pid argument inside a kill tail: `kill -- -PGID`
	// signals a whole process group, and a daemonized server is its own group
	// leader, so its pid doubles as a killable pgid. The digits are compared
	// against the exact protected pids, so signal specs (`-9`, `-15`) can never
	// collide — signal numbers stop far below real pid ranges. The leading
	// whitespace requirement keeps digits inside hyphenated words (a script
	// named kill-server-1234) from matching.
	reNegNum = regexp.MustCompile(`(?:^|\s)-(\d+)\b`)
	// `octo serve stop` / `octo serve --stop` — stopping the daemon through
	// the CLI instead of a kill command. The nested octo process reads the
	// pid file and terminates the daemon itself, so the kill shadows below
	// never see it. `\bocto\b` matches the binary however it is invoked
	// (bare, ./octo, absolute path, quoted inside sh -c '…') but not glued
	// inside another word ("octop"); `serve` and then `stop` must follow
	// within the same command segment.
	reServeStop = regexp.MustCompile(`(?i)\bocto\b[^|;&\n]*\bserve\b[^|;&\n]*\bstop\b`)
)

// guardServerSelfKill returns a non-nil error when command, run inside an octo
// server process, would terminate that server (or its supervisor). It is the
// first of two layers: a best-effort TEXTUAL guard over the common vectors —
// `pkill/killall octo`, `kill <server-pid>`, and `octo serve stop` — that also
// covers the --sandbox branch where no wrapper is injected. The second layer is the runtime shadow
// (posixKillGuardWrapper / windowsKillGuardWrapper), which sees target pids
// after shell expansion and so catches the indirections (`kill $P`,
// `pkill -f serve`) this textual pass cannot.
func guardServerSelfKill(command string) error {
	if !serverGuardOn.Load() {
		return nil
	}
	if reKillByName.MatchString(command) {
		return errServerSelfKill()
	}
	// `octo serve stop` terminates the daemon from inside the nested octo
	// process — no kill-family command ever appears, so it needs its own
	// pattern. stopDaemon independently refuses when it detects it was
	// spawned by a guarded server (ServerGuardEnvActive), which covers the
	// indirections (scripts, variable expansion) this textual pass cannot.
	if reServeStop.MatchString(command) {
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
		for _, m := range reNegNum.FindAllStringSubmatch(seg[1], -1) {
			if protected[m[1]] {
				return errServerSelfKill()
			}
		}
	}
	return nil
}

// guardEnv returns the environment variables that arm the runtime self-kill
// guard inside the injected shell wrappers: the protected pid set plus the
// refusal message the shadow prints. Empty when the guard is off (plain
// CLI/TUI usage), so the wrapper shadows stay inert no-ops there.
func guardEnv() []string {
	if !serverGuardOn.Load() {
		return nil
	}
	env := []string{
		"OCTO_SERVER_PID=" + strconv.Itoa(serverSelfPID),
		"OCTO_GUARD_MSG=" + errServerSelfKill().Error(),
	}
	// PPID 1 (reparented to init/launchd) is unkillable and not protected,
	// mirroring the textual guard.
	if serverSuperPID > 1 {
		env = append(env, "OCTO_SERVER_PPID="+strconv.Itoa(serverSuperPID))
	}
	return env
}

// scrubGuardEnv removes inherited guard variables from env. A terminal command
// spawned by a guarded server carries them, so a nested octo run from that
// command would otherwise arm its own wrapper shadows with the parent's
// (possibly stale, possibly pid-recycled) values. Scrubbing first makes
// guardEnv the only source of truth: guard off means inert shadows, guard on
// means this process's own pids.
func scrubGuardEnv(env []string) []string {
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "OCTO_SERVER_PID=") ||
			strings.HasPrefix(kv, "OCTO_SERVER_PPID=") ||
			strings.HasPrefix(kv, "OCTO_GUARD_MSG=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// posixKillGuardWrapper shadows kill/pkill/killall in the injected POSIX shell
// so the self-kill guard holds at RUNTIME — after the shell has expanded
// variables and command substitutions (`kill $P`, `kill $(cat pidfile)`,
// `pkill -f serve`) that the textual guardServerSelfKill cannot see. This is
// the same shadow-function mechanism as safeRmWrapper.
//
// Resolution does NOT use pgrep: on macOS pgrep/pkill deliberately cannot see
// their own ancestors (the octo server IS the wrapper's ancestor, so macOS
// pkill can't hit it anyway), while procps on Linux has no such blind spot —
// exactly where the guard matters. Instead each pkill/killall invocation is
// matched locally against `ps` output for just the protected pids. Only the
// simple invocation shapes ([signal] [-f] [-x] pattern / killall name) are
// resolved; richer matching flags (-u/-P/-t/-n/-o/-v/...) fail open, as does
// any lookup failure — the textual guard upstream remains as a backstop.
//
// Function shadows only intercept shell-function dispatch: a direct binary
// path (/bin/kill), `command kill`, dispatch via env/xargs, and nested shells
// (`sh -c 'kill …'`, commands written to a script file and executed) all
// bypass them. Accepted residual holes — this guards against reflexive model
// behavior, not an adversary.
//
// Not fmt.Sprintf'd — concatenated raw — so `%` needs no escaping. Requires
// the guardEnv() variables; with OCTO_SERVER_PID unset every shadow passes
// straight through to the real command. Every case pattern carries the
// optional leading paren: a bare ')' inside a pattern can prematurely close
// command substitutions on older bash (macOS /bin/sh is bash 3.2).
const posixKillGuardWrapper = `__octo_guard_match() {
  # Would the pattern match process $3? Matched against ps output for just
  # that pid. $1: name|namex|full|fullx  $2: ERE pattern  $3: pid
  local _mode="$1" _pat="$2" _pid="$3" _args _name _comm
  _args=$(ps -p "$_pid" -o args= 2>/dev/null)
  [ -z "$_args" ] && return 1
  _name="${_args%% *}"; _name="${_name##*/}"
  _comm=$(ps -p "$_pid" -o comm= 2>/dev/null)
  case "$_mode" in
    (name)  printf '%s\n%s\n' "$_name" "$_comm" | grep -E -e "$_pat" >/dev/null 2>&1 ;;
    (namex) [ "$_name" = "$_pat" ] || [ "$_comm" = "$_pat" ] ;;
    (full)  printf '%s\n' "$_args" | grep -E -e "$_pat" >/dev/null 2>&1 ;;
    (fullx) [ "$_args" = "$_pat" ] ;;
    (*) return 1 ;;
  esac
}
__octo_kill_guard() {
  # $1: op (kill|pkill|killall); rest: original args, already shell-expanded.
  local _op="$1"; shift
  local _self="${OCTO_SERVER_PID:-}" _super="${OCTO_SERVER_PPID:-}"
  [ -z "$_self" ] && return 0
  local _msg="${OCTO_GUARD_MSG:-refusing to kill the octo server process that is hosting this session}"
  local _a _t _skip=0
  case "$_op" in
  (kill)
    for _a in "$@"; do
      if [ "$_skip" = 1 ]; then
        _skip=0
      else
        case "$_a" in
          (-s|-n|--signal) _skip=1 ;;  # signal spec consumes the next arg
          (-[0-9]*)
            # Signal spec (-9) or negative pid (kill -- -PGID: a whole process
            # group, and a daemonized server leads its own group). Signal
            # numbers never reach pid range, so an exact digit match can only
            # be a group kill of a protected process.
            _t="${_a#-}"
            case "$_t" in
              (*[!0-9]*) : ;;
              (*)
                if [ "$_t" = "$_self" ]; then printf '%s\n' "$_msg" >&2; return 1; fi
                if [ -n "$_super" ] && [ "$_t" = "$_super" ]; then printf '%s\n' "$_msg" >&2; return 1; fi
                ;;
            esac
            ;;
          (-*|%*) : ;;                 # other flags and job specs carry no target pid
          (*[!0-9]*) : ;;              # non-numeric: not a pid
          (*)
            if [ "$_a" = "$_self" ]; then printf '%s\n' "$_msg" >&2; return 1; fi
            if [ -n "$_super" ] && [ "$_a" = "$_super" ]; then printf '%s\n' "$_msg" >&2; return 1; fi
            ;;
        esac
      fi
    done
    ;;
  (pkill)
    # Simple shapes only: [signal] [-f] [-x] pattern. Anything richer
    # (_bad=1) falls through unguarded.
    local _mode=name _pat= _bad=0
    for _a in "$@"; do
      if [ "$_skip" = 1 ]; then
        _skip=0
      else
        case "$_a" in
          (--signal|--queue) _skip=1 ;;
          (--signal=*|--queue=*|-e|--echo|-[0-9]*|-SIG*) : ;;
          (-HUP|-INT|-QUIT|-ILL|-TRAP|-ABRT|-BUS|-FPE|-KILL|-USR1|-SEGV|-USR2|-PIPE|-ALRM|-TERM|-STKFLT|-CHLD|-CONT|-STOP|-TSTP|-TTIN|-TTOU|-URG|-XCPU|-XFSZ|-VTALRM|-PROF|-WINCH|-IO|-PWR|-SYS|-POLL|-IOT|-CLD) : ;;
          (-f|--full)  case "$_mode" in (name) _mode=full ;; (namex) _mode=fullx ;; esac ;;
          (-x|--exact) case "$_mode" in (name) _mode=namex ;; (full) _mode=fullx ;; esac ;;
          (-*) _bad=1 ;;
          (*) if [ -n "$_pat" ]; then _bad=1; else _pat="$_a"; fi ;;
        esac
      fi
    done
    if [ "$_bad" = 0 ] && [ -n "$_pat" ]; then
      if __octo_guard_match "$_mode" "$_pat" "$_self"; then printf '%s\n' "$_msg" >&2; return 1; fi
      if [ -n "$_super" ] && __octo_guard_match "$_mode" "$_pat" "$_super"; then printf '%s\n' "$_msg" >&2; return 1; fi
    fi
    ;;
  (killall)
    # killall signals processes by exact name; flags carry no target.
    for _a in "$@"; do
      case "$_a" in
        (-*) : ;;
        (*)
          if __octo_guard_match namex "$_a" "$_self"; then printf '%s\n' "$_msg" >&2; return 1; fi
          if [ -n "$_super" ] && __octo_guard_match namex "$_a" "$_super"; then printf '%s\n' "$_msg" >&2; return 1; fi
          ;;
      esac
    done
    ;;
  esac
  return 0
}
kill() { if __octo_kill_guard kill "$@"; then command kill "$@"; else return 1; fi }
pkill() { if __octo_kill_guard pkill "$@"; then command pkill "$@"; else return 1; fi }
killall() { if __octo_kill_guard killall "$@"; then command killall "$@"; else return 1; fi }
`

// windowsKillGuardWrapper is the PowerShell counterpart of
// posixKillGuardWrapper: it shadows Stop-Process (whose aliases kill/spps
// resolve to the cmdlet name and so hit this function — functions take
// precedence) and taskkill (plus a taskkill.exe alias, so the
// extension-qualified spelling resolves here too — aliases outrank
// applications), resolving their -Id//PID arguments directly and their
// -Name//IM process names via Get-Process, then refusing when a guarded pid
// is among the targets. Variable-expanded targets (`Stop-Process -Id $p`) are
// already concrete by the time the function runs, which is what closes the
// holes the textual guard cannot see. Best-effort like the Remove-Item
// wrapper: pipeline input, /FI filter expressions, and abbreviated or
// colon-joined parameter forms (`-Nam octo`, `-Id:123`) are not resolved and
// fall through to the real command. Concatenated raw (not fmt.Sprintf'd).
const windowsKillGuardWrapper = `$__octoGuardPids = @()
if ($env:OCTO_SERVER_PID) { $__octoGuardPids += [int]$env:OCTO_SERVER_PID }
if ($env:OCTO_SERVER_PPID) { $__octoGuardPids += [int]$env:OCTO_SERVER_PPID }
$__octoGuardMsg = "$env:OCTO_GUARD_MSG"
if (-not $__octoGuardMsg) { $__octoGuardMsg = 'refusing to kill the octo server process that is hosting this session' }
function __octo-TestGuardedPid($pids) {
  foreach ($p in @($pids)) {
    $v = 0
    if ([int]::TryParse("$p", [ref]$v) -and ($__octoGuardPids -contains $v)) { return $true }
  }
  return $false
}
function Stop-Process {
  if ($__octoGuardPids.Count -gt 0) {
    $targets = @()
    $names = @()
    for ($i = 0; $i -lt $args.Count; $i++) {
      $a = $args[$i]
      if ($a -is [string]) {
        if (($a -ieq '-Id') -or ($a -ieq '-PID')) { if ($i + 1 -lt $args.Count) { $targets += $args[$i + 1]; $i++ } }
        elseif (($a -ieq '-Name') -or ($a -ieq '-ProcessName')) { if ($i + 1 -lt $args.Count) { $names += $args[$i + 1]; $i++ } }
        elseif (-not $a.StartsWith('-')) {
          $v = 0
          if ([int]::TryParse($a, [ref]$v)) { $targets += $v } else { $names += $a }
        }
      } elseif ($a -is [int]) { $targets += $a }
    }
    foreach ($n in $names) {
      Get-Process -Name "$n" -ErrorAction SilentlyContinue | ForEach-Object { $targets += $_.Id }
    }
    if (__octo-TestGuardedPid $targets) { throw $__octoGuardMsg }
  }
  Microsoft.PowerShell.Management\Stop-Process @args
}
function taskkill {
  if ($__octoGuardPids.Count -gt 0) {
    $targets = @()
    $names = @()
    for ($i = 0; $i -lt $args.Count; $i++) {
      $a = "$($args[$i])"
      if ($a -ieq '/PID') { if ($i + 1 -lt $args.Count) { $targets += $args[$i + 1]; $i++ } }
      elseif ($a -ieq '/IM') { if ($i + 1 -lt $args.Count) { $names += ("$($args[$i + 1])" -replace '\.exe$', ''); $i++ } }
    }
    foreach ($n in $names) {
      Get-Process -Name $n -ErrorAction SilentlyContinue | ForEach-Object { $targets += $_.Id }
    }
    if (__octo-TestGuardedPid $targets) { throw $__octoGuardMsg }
  }
  & "$env:SystemRoot\System32\taskkill.exe" @args
}
Set-Alias -Name taskkill.exe -Value taskkill
`

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
