package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/sandbox"
	"github.com/open-octo/octo-agent/internal/trash"
)

// activeSandbox, when non-nil, confines every terminal command (foreground and
// background) to the given policy. nil means no OS sandbox — the default.
// Set once at startup via SetSandbox; mirrors the package-level defaultBg.
var activeSandbox *sandbox.Policy

// SetSandbox enables OS-level command confinement for the terminal tools.
// Pass nil to disable. cmd/octo calls this when --sandbox is requested.
func SetSandbox(p *sandbox.Policy) { activeSandbox = p }

// NetworkAllowed reports whether the active sandbox policy permits network
// access. When no sandbox is active (nil policy) network is allowed.
func NetworkAllowed() bool {
	if activeSandbox == nil {
		return true
	}
	return activeSandbox.AllowNetwork
}

// safeRmWrapper is injected before every POSIX shell command so that direct
// `rm` invocations move files to the project-scoped trash instead of
// permanently deleting them.  It reads $OCTO_TRASH_DIR (set by shellCommand)
// and writes .meta.json sidecars compatible with the trash package.
//
// Each existing target is hard-linked into the trash when possible (instant and
// space-free even for large trees like node_modules), falling back to a real
// copy across filesystems, and only then does the real `rm` delete it — so the
// wrapper preserves rm's own exit code and output (staging by *moving* would
// leave rm operating on a missing file).
const safeRmWrapper = `__octo_safe_rm() {
  local _trash_dir="$OCTO_TRASH_DIR"
  [ -z "$_trash_dir" ] && return
  local _arg _n=0
  for _arg in "$@"; do
    case "$_arg" in -*) continue ;; esac
    if [ -e "$_arg" ] || [ -L "$_arg" ]; then
      local _ts _base _dest _orig
      _n=$((_n+1))
      _ts=$(date +%%Y%%m%%d-%%H%%M%%S)
      _base=$(basename "$_arg")
      # Absolute-qualify the argument once and use it as BOTH the meta
      # "original" and the copy source. (A prior version prefixed $PWD onto the
      # copy source unconditionally, so an absolute-path argument copied from a
      # non-existent $PWD/abs/path, silently staged nothing, and the real rm
      # still deleted it — absolute-path deletes were unprotected.)
      case "$_arg" in
        /*) _orig="$_arg" ;;
        *)  _orig="$PWD/$_arg" ;;
      esac
      # $$ + a per-invocation counter keep same-second same-basename deletes
      # from colliding on one name.
      _dest="$_trash_dir/${_ts}_$$_${_n}_${_base}"
      mkdir -p "$_trash_dir"
      cp -al "$_orig" "$_dest" 2>/dev/null || cp -R "$_orig" "$_dest" 2>/dev/null || continue
      printf '{"original":"%%s","deleted_at":"%%s","project":"%%s","deleted_by":"rm","kind":"delete"}\n' \
        "$(printf '%%s' "$_orig" | sed 's/\\/\\\\/g; s/"/\\"/g')" \
        "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)" \
        "$(printf '%%s' "$PWD" | sed 's/\\/\\\\/g; s/"/\\"/g')" \
        > "$_dest.meta.json"
    fi
  done
}
rm() { __octo_safe_rm "$@"; command rm "$@"; }
%s
`

// windowsSafeRmWrapper is the PowerShell counterpart of safeRmWrapper. It
// shadows Remove-Item (and therefore its aliases rm / del / ri / rd / erase,
// which resolve to the cmdlet name and so hit this function — functions take
// precedence over cmdlets) with one that first copies any existing filesystem
// paths in the arguments into the trash (via `octo __trash-backup`), then calls
// the real cmdlet to perform the delete. Best-effort, mirroring the POSIX rm
// wrapper: only literal/globbed filesystem paths are backed up; provider paths
// (Env:, Registry), pipeline input, and any backup failure fall straight
// through to the real delete unchanged. The module-qualified cmdlet name avoids
// recursing into this function. First %s is the octo binary, second is the
// user command.
const windowsSafeRmWrapper = `function Remove-Item {
  foreach ($a in $args) {
    if (($a -is [string]) -and (-not $a.StartsWith('-'))) {
      foreach ($rp in (Microsoft.PowerShell.Management\Resolve-Path -Path $a -ErrorAction SilentlyContinue)) {
        if ($rp.Provider.Name -eq 'FileSystem') { & '%s' __trash-backup -- $rp.Path 2>$null }
      }
    }
  }
  Microsoft.PowerShell.Management\Remove-Item @args
}
%s`

// shellCommand builds the *exec.Cmd that runs `command` via the shell, wrapped
// in the active sandbox when one is set. Both TerminalTool and the background
// manager route through here so confinement is uniform.
//
// Shell by platform: POSIX `sh -c` on macOS/Linux; PowerShell on Windows (the
// OS sandbox is macOS/Linux-only, so the Windows branch is never sandboxed).
func shellCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	if err := guardServerSelfKill(command); err != nil {
		return nil, err
	}
	if activeSandbox != nil {
		cmd, err := sandbox.Command(ctx, command, *activeSandbox)
		if err == nil && cmd != nil {
			applyWorkingDir(ctx, cmd)
			env := cmd.Env
			if env == nil {
				env = os.Environ()
			}
			cmd.Env = withBundledBinPath(env)
		}
		return cmd, err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// -NoProfile: reproducible env (don't run the user's $PROFILE).
		// -NonInteractive: never block on a PowerShell prompt mid-command.
		ps := resolvePowerShell()
		projectDir := WorkingDirOrCWD(ctx)
		// Wrap Remove-Item to copy to trash first (parity with the POSIX rm
		// wrapper), but only when we can locate the octo binary and a project
		// dir; otherwise run the command bare (no protection, but never broken).
		if exe, err := os.Executable(); err == nil && projectDir != "" {
			wrapped := fmt.Sprintf(windowsSafeRmWrapper, strings.ReplaceAll(exe, "'", "''"), command)
			cmd = exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", wrapped)
			cmd.Env = withBundledBinPath(append(os.Environ(), "OCTO_TRASH_PROJECT="+projectDir))
		} else {
			cmd = exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", command)
			cmd.Env = withBundledBinPath(os.Environ())
		}
	} else {
		projectDir := WorkingDirOrCWD(ctx)
		if projectDir != "" {
			trashDir := trash.ProjectDir(projectDir)
			wrapped := fmt.Sprintf(safeRmWrapper, command)
			cmd = exec.CommandContext(ctx, "sh", "-c", wrapped)
			cmd.Env = withBundledBinPath(append(os.Environ(), "OCTO_TRASH_DIR="+trashDir))
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
			cmd.Env = withBundledBinPath(os.Environ())
		}
	}
	if attr := setProcessGroupOpts(); attr != nil {
		cmd.SysProcAttr = attr
	}
	applyWorkingDir(ctx, cmd)
	return cmd, nil
}

// bundledBinDir returns ~/.octo/bin if it exists on disk, or "" otherwise.
// The Windows/macOS installers stage helper binaries there (bundled uv — see
// the Makefile's bundle-tools-windows/-macos targets and
// packaging/windows/octo.iss + packaging/macos/scripts/postinstall;
// also where internal/tools/rgembed extracts its ripgrep fallback). go
// install / build-from-source / Linux-without-an-installer users never get
// this directory, so the empty-string case is the normal, silent no-op path
// for them — not an error.
func bundledBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := filepath.Join(home, ".octo", "bin")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// withBundledBinPath returns env with ~/.octo/bin appended to the PATH entry
// (or a new PATH entry added if none exists), so a child process can resolve
// octo-bundled uv as a last resort. Appended, not prepended: a system
// install of uv already on PATH is found first and takes precedence — the
// bundled copy is a fallback, never a shadow. No-op (returns env unchanged)
// when ~/.octo/bin doesn't exist, e.g. on non-installer installs. The caller
// owns env's backing array (typically a fresh os.Environ() call), so this
// mutates in place rather than reallocating on the common no-op path.
func withBundledBinPath(env []string) []string {
	dir := bundledBinDir()
	if dir == "" {
		return env
	}
	for i, kv := range env {
		if len(kv) >= 5 && strings.EqualFold(kv[:5], "path=") {
			env[i] = kv + string(os.PathListSeparator) + dir
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// detachedCommand builds an *exec.Cmd that runs `command` fully detached from
// the agent harness, while otherwise behaving exactly like a normal terminal
// command — same shell wrapping, same OS-sandbox confinement when one is
// active. It differs from shellCommand in only two ways:
//   - it builds on context.WithoutCancel(ctx), so the daemon keeps the working
//     dir but a turn ending (ctx cancellation) can't kill it;
//   - it overrides SysProcAttr to start the process in a NEW session (setsid on
//     POSIX, DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP on Windows), so the
//     harness's kill(-pgid) / taskkill can't reach it.
//
// The caller wires stdio (a detached daemon must not hold the harness's pipes)
// and must not track it in the BackgroundManager: it is fire-and-forget. The
// sandbox sets confinement via the wrapper exe/profile, not SysProcAttr, so
// overriding the latter doesn't weaken it.
func detachedCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	cmd, err := shellCommand(context.WithoutCancel(ctx), command)
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = setDetachedProcessOpts()
	return cmd, nil
}

// applyWorkingDir roots the command in a context-stamped working directory when
// one is present in ctx. No-op otherwise, so every other caller keeps running in
// the process CWD.
func applyWorkingDir(ctx context.Context, cmd *exec.Cmd) {
	if dir := WorkingDir(ctx); dir != "" {
		cmd.Dir = dir
	}
}

// resolvePowerShell picks the Windows shell once: PowerShell 7+ (`pwsh`) when
// present — it's the modern, cross-platform build and supports `&&`/`||`
// pipeline chaining — else Windows PowerShell 5.1 (`powershell`), which ships
// with every supported Windows and is always available as the fallback.
var resolvePowerShell = sync.OnceValue(func() string {
	if path, err := exec.LookPath("pwsh"); err == nil {
		return path
	}
	return "powershell"
})
