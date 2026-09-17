package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// A desktop launch has no argv worth reading: Finder, the Dock and the login
// item all start the app with nothing. So the profile the shell opens can't be
// a flag — it has to be written down somewhere the app can read before it
// touches any profile data.
//
// That somewhere is ~/.octo/desktop-profile, machine-scoped rather than inside
// a profile root, because a file that says which profile to use obviously
// cannot live in the profile it selects.
const desktopProfileFile = "desktop-profile"

// selectDesktopProfile decides which profile this launch opens and configures
// datahome for it. An explicit --profile on the command line wins — someone
// starting the app from a terminal means it — and is not persisted, so a
// one-off launch doesn't silently redefine what double-clicking the icon does.
// Otherwise the remembered choice applies, and failing that the default profile.
func selectDesktopProfile(args []string) error {
	if _, err := datahome.ConfigureFromArgs(args); err != nil {
		return err
	}
	if os.Getenv(datahome.ProfileEnv) != "" {
		return nil
	}
	// An unreadable or since-deleted choice falls back to the default profile
	// rather than refusing to start: the app has to come up so the user can
	// pick again from the tray.
	saved := readDesktopProfile()
	if saved == "" {
		return nil
	}
	if err := datahome.Configure(saved); err != nil {
		return nil
	}
	return nil
}

// readDesktopProfile returns the remembered profile, or "" for the default one.
func readDesktopProfile() string {
	path, err := datahome.SharedPath(desktopProfileFile)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeDesktopProfile records which profile the shell should open from now on.
// The empty string means the default profile, written as an empty file rather
// than by deleting it so the choice stays visible on disk.
func writeDesktopProfile(profile string) error {
	path, err := datahome.SharedPath(desktopProfileFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(profile+"\n"), 0o644)
}

// desktopProfileLabel is how a profile is named in the tray. The default one
// has no name of its own, so it gets a word instead of an empty menu entry.
func desktopProfileLabel(profile string) string {
	if profile == "" {
		return L().profileDefault
	}
	return profile
}
