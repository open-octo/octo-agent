package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fsListCap bounds how many entries a single listing returns so a pathological
// directory (node_modules) can't produce an unbounded response. The frontend
// shows a "list truncated" hint when the cap trips rather than implying the
// folder is small.
const fsListCap = 1000

// fsEntry is one row of a directory listing. Files are returned alongside
// directories for orientation ("yes, this is the repo, there's go.mod"), but
// only directories are pickable by the folder picker.
type fsEntry struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir"`
	IsSymlink bool   `json:"is_symlink"`
}

// GET /api/fs/list?path=<dir> — read-only directory listing that feeds the web
// folder picker. Selecting a directory sets the session working dir through the
// existing PATCH /api/sessions/{id}/working_dir; this endpoint only browses.
//
// Access-key authenticated callers (including remote peers) may browse any
// reachable directory. Unauthenticated callers are still restricted to loopback
// by requireAuth, like every other /api/* route. Unlike the native folder dialog
// (which literally opens a desktop window and stays loopback-only), there is
// no per-endpoint remote check here — the access key is the sole gate.
func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	// Empty path starts at the user's home directory (a native open-dialog
	// default). expandDir would resolve "" to the launch dir, which is less
	// useful as a starting point.
	raw := r.URL.Query().Get("path")
	if strings.TrimSpace(raw) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			raw = home
		}
	}

	// Resolve and validate with the same helpers as working_dir so error copy
	// stays consistent across the two endpoints.
	dir := expandDir(raw)
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("path does not exist: %s", dir))
		return
	case os.IsPermission(err):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("path is not accessible: %s (permission denied)", dir))
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid path: %s (%v)", dir, unwrapPathError(err)))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	dirents, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot read directory: %s (%v)", dir, unwrapPathError(err)))
		return
	}

	entries := make([]fsEntry, 0, len(dirents))
	for _, de := range dirents {
		isSymlink := de.Type()&os.ModeSymlink != 0
		isDir := de.IsDir()
		if isSymlink {
			// Resolve the link target so a symlink to a directory is navigable
			// and picks as a directory. A dangling link stays a non-dir entry.
			if target, serr := os.Stat(filepath.Join(dir, de.Name())); serr == nil {
				isDir = target.IsDir()
			}
		}
		entries = append(entries, fsEntry{Name: de.Name(), IsDir: isDir, IsSymlink: isSymlink})
	}

	// Directories first, then case-insensitive name — the order a picker wants.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	// Cap AFTER sorting, not during the readdir scan: directories sort first,
	// so a folder with thousands of files can't push its subdirectories — the
	// one thing this picker exists to show — past the cap and out of the list.
	truncated := false
	if len(entries) > fsListCap {
		entries = entries[:fsListCap]
		truncated = true
	}

	// parent lets the frontend offer "up" without guessing; empty at the
	// filesystem root, where Dir(dir) == dir.
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dir,
		"parent":    parent,
		"entries":   entries,
		"truncated": truncated,
	})
}
