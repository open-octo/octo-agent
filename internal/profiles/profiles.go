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
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/serveproc"
)

var (
	// ErrInvalidName is returned for a name outside ^[A-Za-z0-9][A-Za-z0-9_-]*$.
	ErrInvalidName = errors.New("profile name must contain only letters, digits, '-' or '_', and start with a letter or digit")
	// ErrReserved is returned by Create for "default": that is how listings
	// label the unnamed ~/.octo root, and the CLI accepts it as an alias for
	// it, so a real profile by that name would be indistinguishable.
	ErrReserved = errors.New(`"default" is reserved for the default profile`)
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
	// Running reports a live backend: a live pid in the profile's serve.pid
	// (daemon or desktop hub), or the profile's pinned address answering (a
	// foreground `octo serve` records no pid). Pid is 0 in the latter case.
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

// DefaultLabel is how listings name the unnamed ~/.octo root. Create refuses
// it as a profile name and Remove treats it as the default root.
const DefaultLabel = "default"

// IsDefaultLabel reports whether name is the listing label for the default
// root (case-insensitively, so "Default" cannot sneak past the reservation).
func IsDefaultLabel(name string) bool { return strings.EqualFold(name, DefaultLabel) }

// Create makes an empty root for a new named profile. The default root is
// created on first use like any other, so "" is rejected here as invalid.
func Create(name string) (Info, error) {
	if name == "" || !datahome.ValidName(name) {
		return Info{}, ErrInvalidName
	}
	if IsDefaultLabel(name) {
		return Info{}, ErrReserved
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

// CheckRemovable reports why Remove would refuse name, or nil if it would
// proceed. Callers that ask the user to confirm first use it so the refusal
// comes before the confirmation rather than after it.
func CheckRemovable(name string) error {
	_, err := removable(name)
	return err
}

// removable runs Remove's guards and returns the root they cleared.
func removable(name string) (string, error) {
	if name == "" || IsDefaultLabel(name) {
		return "", ErrDefault
	}
	if !datahome.ValidName(name) {
		return "", ErrInvalidName
	}
	if name == datahome.Current() {
		return "", ErrCurrent
	}
	dir, err := datahome.DirFor(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return "", err
	}
	if pid, alive := running(dir); alive {
		if pid == 0 {
			return "", fmt.Errorf("%w (its address is answering); stop it first, e.g. octo serve --profile %s stop", ErrRunning, name)
		}
		return "", fmt.Errorf("%w (pid %d); stop it with: octo serve --profile %s stop", ErrRunning, pid, name)
	}
	return dir, nil
}

// Remove deletes a named profile's root and everything in it. It refuses the
// default root, the profile the caller runs under, and any profile whose
// backend is still alive; the caller must stop that backend first.
//
// Only backends are detected. An interactive `octo --profile <name>` session
// records nothing under its root, so it is the caller's confirmation text
// that has to tell the user to close those. The check-then-remove is also
// not atomic: a backend starting in the same instant may have pinned its
// address but not yet bound it, and would recreate an empty root on its next
// write. There is no lock primitive shared with the backend to close that
// window; it is accepted as the cost of `rm` staying a plain directory
// removal.
func Remove(name string) error {
	dir, err := removable(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// DefaultAddr is where the default profile's backend listens; named profiles
// pin theirs in serve.addr. It is the `octo serve -addr` default, and the
// only address probed for the default root: `ResolveAddr` never pins the
// default profile, so a default backend moved with an explicit -addr lists
// as not running. That only affects the status column — the default root is
// never removable. A variable so tests can point it at a closed port instead
// of whatever is on 8088 on the developer's machine.
var DefaultAddr = "127.0.0.1:8088"

// probeTimeout bounds the connect attempt to a profile's address. Loopback
// either answers at once or refuses at once; the timeout only matters for a
// pin on a LAN interface that is down, which then reads as not running even
// if the process still holds the socket. A pin hand-edited to a remote host
// makes the listing dial that host once; the connection is closed unused.
const probeTimeout = 300 * time.Millisecond

// running reports whether dir's backend is up: first by the pid in serve.pid
// (written by `octo serve -d` and the desktop hub; a stale file with a dead
// pid is left alone — clearing it is the owner's business), then by dialling
// the profile's pinned address, because a foreground `octo serve` records no
// pid at all. The second signal has no pid to report.
func running(dir string) (int, bool) {
	if pid, err := serveproc.ReadPid(filepath.Join(dir, "serve.pid")); err == nil && serveproc.IsAlive(pid) {
		return pid, true
	}
	if addr, ok := pinnedAddr(dir); ok && listening(addr) {
		return 0, true
	}
	return 0, false
}

// pinnedAddr returns the address a profile's backend binds: serve.addr when
// present (serveproc's contract for named profiles), else the default
// profile's fixed address for the default root.
func pinnedAddr(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "serve.addr"))
	if err == nil {
		addr := strings.TrimSpace(string(data))
		if _, _, err := net.SplitHostPort(addr); err == nil {
			return addr, true
		}
		return "", false
	}
	if filepath.Base(dir) == ".octo" {
		return DefaultAddr, true
	}
	return "", false
}

func listening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
