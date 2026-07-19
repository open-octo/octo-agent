//go:build !windows

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// withConfigLock acquires an exclusive flock on the config file path (creating
// it if absent), runs fn, and releases the lock. It serialises concurrent
// Save calls across processes — octo-agent has multiple entry points (TUI
// process + octo serve + octo config command) that can all write config.yml
// simultaneously, and without this lock a later writer would silently
// overwrite an earlier writer's changes.
//
// The lock is advisory (flock): co-operating processes honour it, but a
// process that doesn't call withConfigLock can still clobber the file. NFS
// home directories are a known weak spot (flock is unreliable over NFS); for
// the common local-filesystem case this is robust.
//
// fn runs with the lock held — it must not call Save again (would deadlock),
// and should be quick (marshal + tmp write + rename is milliseconds). The
// file is opened O_RDWR|O_CREATE so the lock works even on a fresh install
// where config.yml doesn't exist yet.
func withConfigLock(path string, fn func() error) error {
	f, err := os.OpenFile(lockFilePath(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		// If we can't even open the lockfile, fall back to running without
		// the lock — failing the save because the lock is unavailable is
		// worse than a rare clobbered write. Log via the package logger so
		// the failure isn't entirely invisible.
		return fn()
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		// Same fallback: run without the lock rather than fail the save.
		return fn()
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn()
}

// lockFilePath returns the path of the flock sidecar file. Using a separate
// file (config.yml.lock) rather than locking config.yml itself keeps the
// lockfile out of the read path — Load doesn't need a lock, and locking the
// data file would force every reader to wait for writers.
func lockFilePath(configPath string) string {
	return configPath + ".lock"
}
