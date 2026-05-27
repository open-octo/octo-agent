package tools

import (
	"fmt"
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

// CheckWritable reports whether absPath may be written/edited right now.
//
// Rules:
//   - A path that does NOT exist on disk is always writable (creating a new
//     file needs no prior read — you can't read what isn't there).
//   - An existing path must have been read this session, else the LLM is
//     writing blind → refuse.
//   - An existing, previously-read path whose mtime advanced since the read
//     was changed out-of-band → refuse and force a re-read.
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
		return fmt.Errorf("File has not been read yet. Read it first before writing to it.")
	}
	if info.ModTime().After(readMtime) {
		return fmt.Errorf("File has been modified since it was last read. Read it again before writing to it.")
	}
	return nil
}
