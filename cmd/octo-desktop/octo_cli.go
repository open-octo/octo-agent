package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/open-octo/octo-agent/internal/version"
)

// octoInstallerMarker tags the PATH line this app adds to shell rc files so a
// later launch can find and refresh it and the uninstaller can strip it. It
// MUST stay byte-identical to the marker the macOS pkg's uninstall.sh greps for
// (packaging/macos/scripts/postinstall) — they coordinate across shell and Go.
const octoInstallerMarker = "# added by the octo installer"

// ensureBundledOcto seeds the octo CLI bundled with the app to ~/.local/bin/octo
// so a terminal has `octo` after installing the desktop app, on macOS and Linux
// alike. Keeping the CLI at a stable, writable path (rather than inside the
// app) means `octo upgrade` can replace it in place without touching the signed
// .app bundle, and the same path is used on both platforms.
//
// On macOS the shell needs a PATH entry — ~/.local/bin is not on the default
// PATH there. On Linux it is the XDG user bin dir, already on PATH, so no rc
// file is touched. Windows keeps its own installer-managed CLI on PATH.
//
// Refresh-on-upgrade without clobbering the user: settings.SeededOctoVersion
// records what we last wrote. An octo already at the target with no matching
// record is the user's own (or hand-placed) and is left untouched; one we
// seeded is replaced when the app version moved on. Best-effort throughout.
func ensureBundledOcto(settings *desktopSettings) {
	if runtime.GOOS == "windows" {
		return // the Windows installer owns the CLI and its PATH entry
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	src := bundledBinaryPath("octo")
	if src == "" {
		return // this build didn't bundle the CLI (dev run / plain make)
	}
	target := filepath.Join(home, ".local", "bin", "octo")
	cur := version.Version

	_, statErr := os.Stat(target)
	if shouldSeedOcto(statErr == nil, settings.SeededOctoVersion, cur) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
			if err := copyExecutable(src, target); err == nil {
				settings.SeededOctoVersion = cur
				_ = saveDesktopSettings(*settings)
			}
			// A copy failure leaves SeededOctoVersion unchanged so the next
			// launch retries.
		}
	}

	// Put ~/.local/bin on PATH (macOS only — Linux's XDG bin dir is already
	// there). This is deliberately NOT gated on the seed above: it's idempotent
	// and runs whenever the target CLI exists, so an rc write that failed on a
	// previous launch self-heals on the next one instead of being stranded by a
	// recorded SeededOctoVersion. No-op when the file already has the right line.
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(target); err == nil {
			ensureDirOnPath(home, filepath.Dir(target))
		}
	}
}

// shouldSeedOcto decides whether to (re)write ~/.local/bin/octo. Fresh target:
// yes. Target present but no seeded-version record: it's the user's own octo —
// leave it. Present and recorded: rewrite only when the recorded version no
// longer matches the running app (an upgrade).
func shouldSeedOcto(targetExists bool, seededVer, curVer string) bool {
	if !targetExists {
		return true
	}
	if seededVer == "" {
		return false
	}
	return seededVer != curVer
}

// ensureDirOnPath makes dir reachable from the shell by adding an `export PATH`
// line to the user's rc files. zsh (the macOS default) reads .zshrc for every
// interactive shell — login or not — so it's the reliable target; .zprofile
// (login shells) and .profile (sh, and bash login when no .bash_profile) cover
// the rest. .bash_profile is written only when it already exists: creating it
// would make bash login shells read it INSTEAD of ~/.profile, silently shadowing
// the user's existing setup there. Each write self-heals a prior octo-installer
// line, so an upgrade whose CLI path changed doesn't leave a stale, dead entry.
func ensureDirOnPath(home, dir string) {
	line := fmt.Sprintf(`export PATH="%s:$PATH"  %s`, dir, octoInstallerMarker)
	writeMarkedPathLine(filepath.Join(home, ".zshrc"), line)
	writeMarkedPathLine(filepath.Join(home, ".zprofile"), line)
	writeMarkedPathLine(filepath.Join(home, ".profile"), line)
	if bp := filepath.Join(home, ".bash_profile"); fileExists(bp) {
		writeMarkedPathLine(bp, line)
	}
}

// writeMarkedPathLine rewrites rc with any prior octo-installer line removed and
// the current one appended, creating the file if absent. Removing first keeps
// the entry idempotent and self-healing across upgrades. The write goes through
// a temp file + rename so an interrupted/failed write can't truncate the user's
// rc file. A no-op when the file already has exactly the desired content, so
// running this on every launch doesn't churn the file.
func writeMarkedPathLine(rc, line string) {
	data, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	var kept []string
	for _, l := range strings.Split(string(data), "\n") {
		if !strings.Contains(l, octoInstallerMarker) {
			kept = append(kept, l)
		}
	}
	// Drop trailing blank lines so repeated writes don't accumulate them.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	body := strings.Join(kept, "\n")
	if body != "" {
		body += "\n"
	}
	body += line + "\n"
	if string(data) == body {
		return // already correct — don't rewrite on every launch
	}
	_ = writeFileAtomic(rc, []byte(body), 0o644)
}

// writeFileAtomic writes data to path via a temp file + rename, so a crash or
// error mid-write can't leave a truncated file behind. The PID in the temp name
// keeps two app instances (launched before the single-instance lock is taken)
// from colliding on the same temp path.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// fileExists reports whether path exists and is a regular file (not a dir).
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
