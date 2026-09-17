package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ReadTracker enforces the read-before-write discipline within a session:
// the agent may only write to (or edit) a file it has already read, and only
// while its on-disk mtime still matches what was seen at read time. This
// stops the LLM from blindly overwriting a file it half-remembers, or
// clobbering an edit made out-of-band since it last looked.
//
// State is per-session (one tracker per Registry). All methods are safe for
// concurrent use, though the agent loop dispatches tools sequentially.
type ReadTracker struct {
	mu    sync.Mutex
	reads map[string]time.Time // absolute path → mtime observed at read time
}

// NewReadTracker returns an empty tracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{reads: map[string]time.Time{}}
}

// RecordRead notes that absPath was read (or written) and stamps it with the
// file's current mtime. A failed stat is silently ignored — if we can't tell
// the file's mtime there's nothing to enforce against later, and recording a
// zero time would wrongly trip the "modified since read" guard.
func (rt *ReadTracker) RecordRead(absPath string) {
	info, err := os.Stat(absPath)
	if err != nil {
		return
	}
	rt.mu.Lock()
	rt.reads[absPath] = info.ModTime()
	rt.mu.Unlock()
}

// RefreshTarget re-stamps target's recorded mtime to the file's current value.
// It adopts the session's OWN out-of-tool write (a formatter or redirect run
// through the terminal tool): without it the bumped mtime would trip
// CheckWritable's "modified since read" guard on the next edit even though the
// change was ours.
//
// It only ever re-stamps a path the tracker already recorded, and only that
// exact path — never a directory's children, never a new path. So a file the
// session never read stays unwritable, and a file changed by an external editor
// (which never passes through the terminal tool, and is never named as this
// command's exact write target) keeps its stale stamp: the guard still fires
// for a genuine out-of-band edit.
func (rt *ReadTracker) RefreshTarget(target string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, tracked := rt.reads[target]; !tracked {
		return
	}
	if info, err := os.Stat(target); err == nil {
		rt.reads[target] = info.ModTime()
	}
}

// CheckWritable reports whether absPath may be written/edited right now.
//
// Rules:
//   - A path that does NOT exist on disk is always writable (creating a new
//     file needs no prior read — you can't read what isn't there).
//   - An existing path must have been read this session, else the LLM is
//     writing blind → refuse.
//   - An existing, previously-read path whose mtime advanced since the read
//     was changed out-of-band → refuse and force a re-read.
//   - An existing path never read under THIS name still passes when its bytes
//     are identical to a file that was read (see matchesReadContent) — the
//     same file reached through a second path, most often another git
//     worktree of the same repository.
//
// The returned error text mirrors Claude Code's wording so the LLM reacts
// the way it's been trained to (re-read, then retry).
func (rt *ReadTracker) CheckWritable(absPath string) error {
	info, err := os.Stat(absPath)
	if err != nil {
		// Treat any stat failure (most commonly "not found") as "new file" —
		// writable without a prior read. write_file will surface real I/O
		// errors itself when it actually tries to create the file.
		return nil
	}

	rt.mu.Lock()
	readMtime, wasRead := rt.reads[absPath]
	rt.mu.Unlock()

	if !wasRead {
		if rt.matchesReadContent(absPath, info) {
			return nil
		}
		return fmt.Errorf("File has not been read yet. Read it first before writing to it.")
	}
	if info.ModTime().After(readMtime) {
		return fmt.Errorf("File has been modified since it was last read. Read it again before writing to it.")
	}
	return nil
}

// maxContentMatchBytes caps the file size matchesReadContent will digest. The
// fallback only runs on the refusal path, but a stray write to a huge binary
// shouldn't turn into hashing it — past this size an unread path stays unread.
const maxContentMatchBytes = 10 << 20

// matchesReadContent reports whether absPath currently holds the very same
// bytes as a file this tracker already recorded as read. That makes the read
// gate track file identity by content rather than by name, which is what
// unblocks the cross-worktree case: the agent reads internal/app/provider.go
// in the main checkout and then edits the same file in a linked worktree — a
// different absolute path, but byte-for-byte the content it just read.
//
// It is deliberately narrow. A candidate only counts while its own mtime still
// matches what was stamped at read time, so a file changed since the read
// can't launder unseen bytes onto another path; and the match is on the full
// content, so the moment the worktree's copy diverges (a different branch, a
// local edit) the guard fires again and forces a real read.
func (rt *ReadTracker) matchesReadContent(absPath string, info os.FileInfo) bool {
	if info.Size() > maxContentMatchBytes {
		return false
	}

	rt.mu.Lock()
	candidates := make(map[string]time.Time, len(rt.reads))
	for path, mtime := range rt.reads {
		candidates[path] = mtime
	}
	rt.mu.Unlock()

	// Digested lazily: most calls find no same-size candidate and so never
	// read the target at all.
	var want string
	for path, readMtime := range candidates {
		if path == absPath {
			continue
		}
		cand, err := os.Stat(path)
		if err != nil || cand.IsDir() || cand.Size() != info.Size() || cand.ModTime().After(readMtime) {
			continue
		}
		if want == "" {
			if want, err = fileDigest(absPath); err != nil {
				return false
			}
		}
		if got, err := fileDigest(path); err == nil && got == want {
			return true
		}
	}
	return false
}

// fileDigest returns the hex SHA-256 of path's contents, streamed so a large
// file never lands in memory whole.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
