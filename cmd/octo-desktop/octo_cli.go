package main

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/open-octo/octo-agent/internal/version"
)

// ensureBundledOcto seeds the octo CLI bundled with the app to ~/.local/bin/octo
// on Linux, so a terminal has `octo` after installing the desktop app — matching
// the macOS and Windows installers, which put the CLI on PATH themselves. The
// AppImage has no installer step to do that, so the app does it on launch.
//
// ~/.local/bin is the XDG user bin dir, already on PATH on current desktops, so
// no shell rc is touched. mac/win are skipped: their installers own the CLI and
// its PATH entry, and on macOS the bundled octo lives inside the signed .app.
//
// Refresh-on-upgrade without clobbering the user: settings.SeededOctoVersion
// records what we last wrote. An octo already at the target with no matching
// record is the user's own (or hand-placed) and is left untouched; one we
// seeded is replaced when the app version moved on. Best-effort throughout.
func ensureBundledOcto(settings *desktopSettings) {
	if runtime.GOOS != "linux" {
		return
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
