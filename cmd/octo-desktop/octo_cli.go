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
	if !shouldSeedOcto(statErr == nil, settings.SeededOctoVersion, cur) {
		return
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return
	}
	if err := copyExecutable(src, target); err != nil {
		return // leave SeededOctoVersion unchanged so the next launch retries
	}
	// macOS: put ~/.local/bin on PATH via the shell rc files. Linux's XDG bin
	// dir is already on PATH, so it needs no rc edit.
	if runtime.GOOS == "darwin" {
		ensureDirOnPath(home, filepath.Dir(target))
	}
	settings.SeededOctoVersion = cur
	_ = saveDesktopSettings(*settings)
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
// (login shells), .bash_profile, and .profile cover the rest. Each write heals
// any previous octo-installer line first, so an upgrade whose CLI path changed
// doesn't leave a stale, dead entry. Best-effort: unreadable/unwritable rc
// files are skipped.
func ensureDirOnPath(home, dir string) {
	line := fmt.Sprintf(`export PATH="%s:$PATH"  %s`, dir, octoInstallerMarker)
	for _, name := range []string{".zshrc", ".zprofile", ".bash_profile", ".profile"} {
		writeMarkedPathLine(filepath.Join(home, name), line)
	}
}

// writeMarkedPathLine rewrites rc with any prior octo-installer line removed and
// the current one appended, creating the file if absent. Removing first keeps
// the entry idempotent and self-healing across upgrades.
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
	_ = os.WriteFile(rc, []byte(body), 0o644)
}
