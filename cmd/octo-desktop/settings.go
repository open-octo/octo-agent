package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// desktopSettings holds the desktop app's per-machine preferences — the ones
// the server itself has no opinion about. Persisted to ~/.octo/desktop.json so
// they survive a relaunch. Read once at startup; the channels toggle is also
// written by the native bridge when the user flips it in the UI.
type desktopSettings struct {
	// ChannelsEnabled is the "run channels on this machine" toggle. Default
	// false: launching the GUI must never silently start an IM bridge.
	ChannelsEnabled bool `json:"channels_enabled"`
	// KeepRunningInBackground keeps the hub (and its clients) alive when the
	// window is closed, hiding to the tray instead of quitting. Default true —
	// closing the window shouldn't drop a VS Code / phone client's backend.
	KeepRunningInBackground bool `json:"keep_running_in_background"`
	// SeededOctoVersion records the version of the octo CLI this app last seeded
	// to ~/.local/bin (Linux only — mac/win ship the CLI through their
	// installers). It scopes the refresh-on-upgrade: an octo on ~/.local/bin
	// with no matching record here is the user's own and is never overwritten.
	SeededOctoVersion string `json:"seeded_octo_version,omitempty"`
}

// defaultDesktopSettings is what a first launch (no file yet) uses.
func defaultDesktopSettings() desktopSettings {
	return desktopSettings{ChannelsEnabled: false, KeepRunningInBackground: true}
}

func desktopSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "desktop.json"), nil
}

// loadDesktopSettings reads ~/.octo/desktop.json, falling back to defaults for
// a missing or unreadable file (a fresh install, or a hand-corrupted one — the
// defaults are safe either way).
func loadDesktopSettings() desktopSettings {
	s := defaultDesktopSettings()
	path, err := desktopSettingsPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s) // partial/corrupt JSON keeps the defaults it couldn't override
	return s
}

// saveDesktopSettings writes the settings back to ~/.octo/desktop.json.
func saveDesktopSettings(s desktopSettings) error {
	path, err := desktopSettingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
