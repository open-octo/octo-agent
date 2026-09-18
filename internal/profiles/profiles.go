// Package profiles manages the user-data roots that --profile selects between:
// listing what exists on disk, creating an empty one, and removing one. The
// CLI (`octo profiles`) and the Web UI's data-management panel both go
// through it so the safety rules — never remove the default root, the one
// you are running under, or one whose backend is up — live in one place.
package profiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/serveproc"
)

var (
	// ErrInvalidName is returned for a name outside ^[A-Za-z0-9][A-Za-z0-9_-]*$.
	ErrInvalidName = errors.New("profile name must contain only letters, digits, '-' or '_', and start with a letter or digit")
	// ErrExists is returned by Create when the root already exists.
	ErrExists = errors.New("profile already exists")
	// ErrNotFound is returned by Remove when there is no such root.
	ErrNotFound = errors.New("profile not found")
	// ErrDefault is returned when asked to remove the default ~/.octo root. It
	// also holds machine-scoped state (helper binaries, the desktop app's
	// profile record), so it is never removed through this package.
	ErrDefault = errors.New("the default profile cannot be removed")
	// ErrCurrent is returned when asked to remove the profile this process runs
	// under: its own config, sessions and pid file are in use.
	ErrCurrent = errors.New("cannot remove the profile currently in use")
	// ErrRunning is returned when the profile's backend is alive; stop it first
	// (`octo serve --profile <name> stop`).
	ErrRunning = errors.New("profile has a running backend")
)

// Info describes one profile root on disk.
type Info struct {
	// Name is "" for the default ~/.octo root.
	Name string `json:"name"`
	Path string `json:"path"`
	// Current marks the profile this process runs under.
	Current bool `json:"current"`
	// Running reports a live backend recorded in the profile's serve.pid.
	Running bool `json:"running"`
	Pid     int  `json:"pid,omitempty"`
	// SizeBytes is the total size of regular files under the root. Best
	// effort: unreadable entries are skipped rather than failing the listing.
	SizeBytes int64 `json:"size_bytes"`
}

// Current returns the name of the profile this process runs under.
func Current() string { return datahome.Current() }

// List describes every profile root that exists on disk, default first, then
// named profiles sorted by name.
func List() ([]Info, error) {
	names, err := datahome.List()
	if err != nil {
		return nil, err
	}
	current := datahome.Current()
	out := make([]Info, 0, len(names))
	for _, name := range names {
		dir, err := datahome.DirFor(name)
		if err != nil {
			return nil, err
		}
		info := Info{Name: name, Path: dir, Current: name == current}
		info.Pid, info.Running = running(dir)
		info.SizeBytes = dirSize(dir)
		out = append(out, info)
	}
	return out, nil
}

// Create makes an empty root for a new named profile. The default root is
// created on first use like any other, so "" is rejected here as invalid.
func Create(name string) (Info, error) {
	if name == "" || !datahome.ValidName(name) {
		return Info{}, ErrInvalidName
	}
	dir, err := datahome.DirFor(name)
	if err != nil {
		return Info{}, err
	}
	if _, err := os.Stat(dir); err == nil {
		return Info{}, fmt.Errorf("%w: %s", ErrExists, dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Info{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, err
	}
	return Info{Name: name, Path: dir, Current: name == datahome.Current()}, nil
}

// Remove deletes a named profile's root and everything in it. It refuses the
// default root, the profile the caller runs under, and any profile whose
// backend is still alive; the caller must stop that backend first.
func Remove(name string) error {
	if name == "" {
		return ErrDefault
	}
	if !datahome.ValidName(name) {
		return ErrInvalidName
	}
	if name == datahome.Current() {
		return ErrCurrent
	}
	dir, err := datahome.DirFor(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return err
	}
	if pid, alive := running(dir); alive {
		return fmt.Errorf("%w (pid %d); stop it with: octo serve --profile %s stop", ErrRunning, pid, name)
	}
	return os.RemoveAll(dir)
}

// running reports the live backend recorded in dir's serve.pid, if any. The
// file name is serveproc's contract; a stale file (dead pid) counts as not
// running but is left alone — clearing it is the owner's business.
func running(dir string) (int, bool) {
	pid, err := serveproc.ReadPid(filepath.Join(dir, "serve.pid"))
	if err != nil || !serveproc.IsAlive(pid) {
		return 0, false
	}
	return pid, true
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() {
			total += fi.Size()
		}
		return nil
	})
	return total
}
