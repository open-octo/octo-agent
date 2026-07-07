// Package pathutil holds small filesystem path helpers shared across
// packages that need to compare directories without being fooled by
// symlinks.
package pathutil

import "path/filepath"

// SameDir reports whether a and b name the same directory once symlinks are
// resolved. Byte-equal paths aren't enough: os.Getwd() can return a
// symlink-resolved path (its syscall fallback when $PWD is unset or stale,
// e.g. under a process supervisor with no controlling shell) while
// os.UserHomeDir() returns $HOME verbatim, so a home directory reached
// through a symlink compares unequal to itself with a raw string check.
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return resolveExisting(a) == resolveExisting(b)
}

// resolveExisting resolves symlinks in the longest existing ancestor of p and
// reattaches whatever trailing components don't exist yet (e.g. a config
// file that hasn't been created), since filepath.EvalSymlinks requires the
// full path to exist.
func resolveExisting(p string) string {
	clean := filepath.Clean(p)
	suffix := ""
	cur := clean
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		suffix = filepath.Join(filepath.Base(cur), suffix)
		cur = parent
	}
}
