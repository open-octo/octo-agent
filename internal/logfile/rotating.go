// Package logfile provides a size-bounded, self-rotating log file for the
// backends that write ~/.octo/serve.log directly with no service manager in
// front of them: the desktop hub (in-process) and `octo serve -d`. systemd
// users are unaffected — they capture stderr into the journal, which rotates
// itself; this exists for the paths that don't have that.
//
// Two entry points cover the two ways that file is consumed:
//
//   - Rotating wraps the file as an io.WriteCloser and rotates inline, so a
//     long-running in-process writer (the desktop hub) stays bounded without
//     ever restarting. Used as the slog handler's output.
//   - RotateIfLarger rotates once at open time for callers that hand the
//     file's fd to a child process and so can't intercept its writes
//     (`octo serve -d` pipes the fd in as the worker's stderr). It bounds
//     growth per daemon generation rather than continuously.
//
// Both share one rename-chain (rotate), which is Windows-safe: os.Rename onto
// an existing file fails on Windows, so every destination is removed first.
package logfile

import (
	"fmt"
	"os"
	"sync"
)

// Default size bound and backup count. Worst-case on-disk footprint is
// DefaultMaxBytes * (DefaultBackups + 1) — the live file plus its history.
const (
	DefaultMaxBytes = 10 << 20 // 10 MiB
	DefaultBackups  = 3
)

// Rotating is an io.WriteCloser that caps a log file at maxBytes, keeping up to
// `backups` rotated generations (name, name.1, … name.<backups>). It rotates
// just before a write that would cross the cap, so the live file exceeds
// maxBytes by at most one record. Writes are serialized, which also makes it
// safe for the concurrent use an slog handler may make of it.
type Rotating struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	f        *os.File
	size     int64
}

// Open opens path for appending (creating it if absent). If the existing file
// is already at or over maxBytes — e.g. a log grown unbounded before rotation
// existed — it is rotated at open so appending starts on a fresh file. The
// parent directory must already exist.
func Open(path string, maxBytes int64, backups int) (*Rotating, error) {
	if fi, err := os.Stat(path); err == nil && maxBytes > 0 && fi.Size() >= maxBytes {
		if err := rotate(path, backups); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Rotating{path: path, maxBytes: maxBytes, backups: backups, f: f, size: fi.Size()}, nil
}

// Write implements io.Writer. It rotates before writing when the record would
// push the file past maxBytes, except when the file is empty — a single record
// larger than maxBytes is written as-is rather than triggering an endless
// rotate-with-nothing-to-move.
func (r *Rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxBytes > 0 && r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the underlying file. Further writes will fail.
func (r *Rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// rotateLocked closes the current file, shifts the rename chain, and reopens a
// fresh file. It reopens even if the shift failed so logging keeps working; on
// that path r.size is reset from the reopened file so accounting stays correct
// whether or not the current file was actually renamed away.
func (r *Rotating) rotateLocked() error {
	_ = r.f.Close()
	rotErr := rotate(r.path, r.backups)
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.f = nil
		return err
	}
	r.f = f
	r.size = 0
	if fi, err := f.Stat(); err == nil {
		r.size = fi.Size()
	}
	return rotErr
}

// RotateIfLarger rotates path (keeping `backups` generations) if it exists and
// is at least maxBytes, then returns without installing any further rotation.
// It is the fd-compatible, open-time cap for `octo serve -d`, which pipes the
// file's fd into its worker child and so can't wrap it in a Rotating writer. A
// missing file or maxBytes<=0 is a no-op.
func RotateIfLarger(path string, maxBytes int64, backups int) error {
	if maxBytes <= 0 {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < maxBytes {
		return nil
	}
	return rotate(path, backups)
}

// rotate shifts path -> path.1 -> … -> path.<backups>, dropping the oldest
// generation. It renames oldest-first and removes each destination before the
// rename so it works on Windows (where renaming onto an existing file fails).
// A generation that doesn't exist yet is skipped. With backups <= 0 the file
// is simply removed (no history kept). A missing source is not an error.
func rotate(path string, backups int) error {
	if backups <= 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	// Drop the oldest generation, then shift the rest up by one.
	_ = os.Remove(fmt.Sprintf("%s.%d", path, backups))
	for i := backups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := fmt.Sprintf("%s.%d", path, i+1)
		_ = os.Remove(dst)
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	if _, err := os.Stat(path); err != nil {
		return nil // nothing to rotate
	}
	dst := path + ".1"
	_ = os.Remove(dst)
	return os.Rename(path, dst)
}
