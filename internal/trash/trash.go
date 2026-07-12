// Package trash provides a file-level trash (recycle bin) for octo.
//
// When the agent deletes or overwrites a file, the old copy is moved to
// ~/.octo/trash/<project_hash>/<trash_name>, with a sidecar .meta.json
// recording the original path and deletion timestamp. Entries can be listed,
// restored (never silently overwriting a file that now sits at the original
// path), or permanently removed.
package trash

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrRestoreConflict is returned by Restore under ConflictAbort when a file
// already exists at the entry's original path.
var ErrRestoreConflict = errors.New("a file already exists at the original path")

// ConflictPolicy selects how Restore behaves when the destination exists.
type ConflictPolicy int

const (
	// ConflictAbort refuses to restore and returns ErrRestoreConflict so the
	// caller can ask the user what to do. This is the safe default.
	ConflictAbort ConflictPolicy = iota
	// ConflictBackupExisting moves the current file into the trash first, then
	// restores. Lossless and symmetric.
	ConflictBackupExisting
	// ConflictRename restores alongside as <name>.restored-<timestamp>.
	ConflictRename
)

// Entry describes a single trashed file.
type Entry struct {
	ID        string `json:"id"`
	Original  string `json:"original"`
	TrashPath string `json:"trash_path"`
	DeletedAt string `json:"deleted_at"`
	Project   string `json:"project"`
	Size      int64  `json:"size"`
	// Label is a human-readable name for the entry, when one can be derived
	// (e.g. a session transcript's title). Empty means the UI should fall back
	// to the basename.
	Label string `json:"label,omitempty"`
	// Orphan is true when the original project directory no longer exists
	// (e.g. a test temp dir) — such entries are safe to permanently delete.
	Orphan bool `json:"orphan"`
}

// RestoreResult reports what Restore did.
type RestoreResult struct {
	// RestoredTo is the path the trashed bytes were written to. Equal to the
	// original path unless ConflictRename diverted it.
	RestoredTo string
	// BackedUpExisting is true when ConflictBackupExisting moved a pre-existing
	// file into the trash before restoring.
	BackedUpExisting bool
}

type meta struct {
	Original  string `json:"original"`
	DeletedAt string `json:"deleted_at"`
	Project   string `json:"project"`
}

// Dir returns the trash root directory.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".octo", "trash")
}

// ProjectDir returns the per-project trash subdirectory for projectDir.
func ProjectDir(projectDir string) string {
	return filepath.Join(Dir(), hashProject(projectDir))
}

// Backup copies a file or directory into the trash with a .meta.json sidecar,
// preserving its original path for later restoration, WITHOUT removing the
// original. It's the copy-only half of Move: the Windows safe-delete wrapper
// and the overwrite-protection path use it to make a delete/overwrite
// recoverable while the caller performs the actual removal.
func Backup(originalPath, projectDir string) error {
	fi, err := os.Stat(originalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found: %s", originalPath)
		}
		return err
	}

	trashPath, err := stageName(originalPath, projectDir)
	if err != nil {
		return err
	}

	if fi.IsDir() {
		if err := copyDir(originalPath, trashPath); err != nil {
			return err
		}
	} else {
		if err := copyFile(originalPath, trashPath); err != nil {
			return err
		}
	}

	if err := writeMeta(trashPath, originalPath, projectDir); err != nil {
		removePath(trashPath)
		return err
	}
	return nil
}

// Move backs a file or directory up to the trash and then removes the original.
// On success the original is gone but recoverable from the trash.
//
// When the original and the trash share a filesystem (the common case: both
// live under ~/.octo, or a project on the same volume as $HOME) the move is a
// single atomic rename — instant, and space-free even for large trees. Across
// filesystems it falls back to copy-then-remove.
func Move(originalPath, projectDir string) error {
	if _, err := os.Stat(originalPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found: %s", originalPath)
		}
		return err
	}

	trashPath, err := stageName(originalPath, projectDir)
	if err != nil {
		return err
	}

	if err := os.Rename(originalPath, trashPath); err == nil {
		// Renamed in; record meta. If meta can't be written, put the file back
		// so nothing is lost in a metadata-less limbo (List skips meta-less
		// entries, so they'd be invisible and unrecoverable).
		if err := writeMeta(trashPath, originalPath, projectDir); err != nil {
			_ = os.Rename(trashPath, originalPath)
			return err
		}
		return nil
	}

	// Cross-device (or any rename failure): copy, then remove. Backup writes
	// meta before returning, so the original is only removed once a recoverable
	// copy exists.
	if err := Backup(originalPath, projectDir); err != nil {
		return err
	}
	return os.RemoveAll(originalPath)
}

// Restore moves a trashed entry back to its original location under the given
// conflict policy. It never silently overwrites a file already at the original
// path — see ConflictPolicy.
func Restore(id string, policy ConflictPolicy) (RestoreResult, error) {
	entries, err := List()
	if err != nil {
		return RestoreResult{}, err
	}
	var e *Entry
	for i := range entries {
		if entries[i].ID == id {
			e = &entries[i]
			break
		}
	}
	if e == nil {
		return RestoreResult{}, fmt.Errorf("trash entry not found: %s", id)
	}

	dest := e.Original
	var res RestoreResult

	if _, err := os.Stat(dest); err == nil {
		// Something is already at the original path.
		switch policy {
		case ConflictAbort:
			return RestoreResult{}, ErrRestoreConflict
		case ConflictBackupExisting:
			if err := Move(dest, e.Project); err != nil {
				return RestoreResult{}, fmt.Errorf("back up existing file: %w", err)
			}
			res.BackedUpExisting = true
		case ConflictRename:
			dest = dest + ".restored-" + time.Now().Format("20060102-150405")
		}
	} else if !os.IsNotExist(err) {
		return RestoreResult{}, err
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return RestoreResult{}, err
	}
	if err := moveAcross(e.TrashPath, dest); err != nil {
		return RestoreResult{}, err
	}
	os.Remove(e.TrashPath + ".meta.json")
	res.RestoredTo = dest
	return res, nil
}

// Delete permanently removes one entry by ID and returns the bytes freed.
func Delete(id string) (int64, error) {
	entries, err := List()
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.ID == id {
			freed := e.Size
			removePath(e.TrashPath)
			os.Remove(e.TrashPath + ".meta.json")
			return freed, nil
		}
	}
	return 0, fmt.Errorf("trash entry not found: %s", id)
}

// List returns all trashed files sorted by deletion time (newest first).
func List() ([]Entry, error) {
	root := Dir()
	var entries []Entry

	dirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		projHash := d.Name()
		projDir := filepath.Join(root, projHash)
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".meta.json") {
				continue
			}
			trashPath := filepath.Join(projDir, f.Name())
			metaPath := trashPath + ".meta.json"
			m, err := readMeta(metaPath)
			if err != nil {
				continue
			}
			size := int64(0)
			if info, err := os.Stat(trashPath); err == nil {
				if info.IsDir() {
					size = dirSize(trashPath)
				} else {
					size = info.Size()
				}
			}
			orphan := false
			if m.Project != "" {
				if _, err := os.Stat(m.Project); os.IsNotExist(err) {
					orphan = true
				}
			}
			entries = append(entries, Entry{
				// <project_hash>.<trash_name> — a single URL-safe segment that
				// pins the exact file, so restore/delete never cross-project
				// first-match the wrong entry.
				ID:        projHash + "." + f.Name(),
				Original:  m.Original,
				TrashPath: trashPath,
				DeletedAt: m.DeletedAt,
				Project:   m.Project,
				Size:      size,
				Label:     deriveLabel(m.Original, trashPath),
				Orphan:    orphan,
			})
		}
	}

	sortEntries(entries)
	return entries, nil
}

// Empty removes trashed files. Modes: "all", "old" (>7 days), "orphans"
// (project directory no longer exists).
// Returns (deleted count, freed bytes, error).
func Empty(mode string) (int, int64, error) {
	entries, err := List()
	if err != nil {
		return 0, 0, err
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	count := 0
	var freed int64
	for _, e := range entries {
		remove := false
		switch mode {
		case "all":
			remove = true
		case "old":
			t, err := time.Parse(time.RFC3339, e.DeletedAt)
			if err == nil && t.Before(cutoff) {
				remove = true
			}
		case "orphans":
			if _, err := os.Stat(e.Project); os.IsNotExist(err) {
				remove = true
			}
		}
		if remove {
			removePath(e.TrashPath)
			os.Remove(e.TrashPath + ".meta.json")
			count++
			freed += e.Size
		}
	}
	return count, freed, nil
}

// Enforce bounds the trash: it first removes entries older than retention, then
// — if the remaining total still exceeds maxBytes — evicts oldest-first until
// it's under the cap. A zero (or negative) bound disables that half.
//
// Orphans (entries whose original project is gone) are skipped by the age-out
// pass — they're often exactly what a user wants back after a checkout moved a
// repo — but remain eligible for hard-cap eviction, since the size cap is a
// firm limit. Returns (entries removed, bytes freed).
func Enforce(maxBytes int64, retention time.Duration) (int, int64, error) {
	entries, err := List()
	if err != nil {
		return 0, 0, err
	}

	removed := 0
	var freed int64
	remove := func(e Entry) {
		removePath(e.TrashPath)
		os.Remove(e.TrashPath + ".meta.json")
		removed++
		freed += e.Size
	}

	// Age-out (non-orphans only).
	kept := entries[:0]
	if retention > 0 {
		cutoff := time.Now().Add(-retention)
		for _, e := range entries {
			if !e.Orphan {
				if t, perr := time.Parse(time.RFC3339, e.DeletedAt); perr == nil && t.Before(cutoff) {
					remove(e)
					continue
				}
			}
			kept = append(kept, e)
		}
	} else {
		kept = entries
	}

	// Size cap: evict oldest-first over the remaining set.
	if maxBytes > 0 {
		var total int64
		for _, e := range kept {
			total += e.Size
		}
		if total > maxBytes {
			// kept is newest-first (List order); walk from the oldest end.
			for i := len(kept) - 1; i >= 0 && total > maxBytes; i-- {
				total -= kept[i].Size
				remove(kept[i])
			}
		}
	}

	return removed, freed, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────

func hashProject(dir string) string {
	h := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%x", h[:8])
}

// stageName creates the per-project trash directory and returns a collision-free
// destination path for originalPath. The name is
// <timestamp>_<rand>_<basename>: the random token keeps two same-basename files
// deleted in the same second from colliding.
func stageName(originalPath, projectDir string) (string, error) {
	targetDir := ProjectDir(projectDir)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("%s_%s_%s", ts, randToken(), filepath.Base(originalPath))
	return filepath.Join(targetDir, name), nil
}

func writeMeta(trashPath, originalPath, projectDir string) error {
	m := meta{
		Original:  originalPath,
		DeletedAt: time.Now().UTC().Format(time.RFC3339),
		Project:   projectDir,
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(trashPath+".meta.json", data, 0600)
}

// randToken returns 4 hex chars of entropy.
func randToken() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Vanishingly unlikely; a fixed token still leaves the timestamp +
		// basename, and MkdirAll/Rename would surface any real collision.
		return "0000"
	}
	return hex.EncodeToString(b[:])
}

// moveAcross renames src to dst, falling back to copy+remove across
// filesystems (os.Rename fails with EXDEV).
func moveAcross(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := copyDir(src, dst); err != nil {
			return err
		}
	} else {
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return os.RemoveAll(src)
}

func removePath(p string) {
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		os.RemoveAll(p)
	} else {
		os.Remove(p)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		return copyFile(path, dstPath)
	})
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func readMeta(path string) (meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return meta{}, err
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return meta{}, err
	}
	return m, nil
}

// deriveLabel returns a human-readable name for a trashed entry, or "" to let
// the caller fall back to the basename. Session transcripts get their title.
func deriveLabel(original, trashPath string) string {
	if isSessionTranscript(original) {
		return sessionTitle(trashPath)
	}
	return ""
}

// isSessionTranscript reports whether original is an octo session JSONL, i.e.
// ~/.octo/sessions/<id>.jsonl.
func isSessionTranscript(original string) bool {
	return strings.HasSuffix(original, ".jsonl") &&
		filepath.Base(filepath.Dir(original)) == "sessions"
}

// sessionTitle parses the session title out of a trashed transcript. The first
// line is the meta record (authoritative title after a rewrite); a later
// type:"title" record overrides it. Bounded so listing stays cheap: reads at
// most sessionScanLimit bytes and returns the last title seen.
func sessionTitle(path string) string {
	// The first line is the meta record, which embeds the (possibly large)
	// system prompt; the limit must comfortably clear it so the meta title
	// isn't truncated, while still bounding the scan for a later title record.
	const sessionScanLimit = 2 * 1024 * 1024
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	r := bufio.NewReader(io.LimitReader(f, sessionScanLimit))
	var title string
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var rec struct {
				Type  string `json:"type"`
				Title string `json:"title"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Title != "" &&
				(rec.Type == "meta" || rec.Type == "title") {
				title = rec.Title
			}
		}
		if err != nil {
			break
		}
	}
	return title
}

func sortEntries(entries []Entry) {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].DeletedAt < entries[j].DeletedAt {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}
