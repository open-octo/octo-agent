package server

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/trash"
)

// ─── GET /api/memories/{filename} ───────────────────────────────────────────

func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	fname := r.PathValue("filename")
	if fname == "" {
		writeError(w, http.StatusBadRequest, "missing filename")
		return
	}

	// Security: prevent path traversal.
	fname = filepath.Base(fname)
	p, ok := s.resolveMemoryPath(fname, r.URL.Query().Get("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	data, err := os.ReadFile(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"filename": fname,
		"content":  string(data),
		"path":     p,
	})
}

// ─── DELETE /api/memories/{filename} ────────────────────────────────────────

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	fname := r.PathValue("filename")
	if fname == "" {
		writeError(w, http.StatusBadRequest, "missing filename")
		return
	}

	fname = filepath.Base(fname)
	p, ok := s.resolveMemoryPath(fname, r.URL.Query().Get("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "memory not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := trash.Move(p, filepath.Dir(p), trash.Options{DeletedBy: "memory", Kind: "delete"}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Update MEMORY.md in the same directory: remove any lines that reference
	// the deleted topic file, so the index stays consistent.
	memDir := filepath.Dir(p)
	indexPath := filepath.Join(memDir, memory.IndexFile)
	if idxData, err := os.ReadFile(indexPath); err == nil {
		var outLines []string
		sc := bufio.NewScanner(strings.NewReader(string(idxData)))
		for sc.Scan() {
			line := sc.Text()
			// Skip lines that reference the deleted filename (e.g.
			// "- [topic](topic.md)" or "topic.md inline reference").
			if strings.Contains(line, fname) {
				continue
			}
			outLines = append(outLines, line)
		}
		if err := sc.Err(); err == nil && len(outLines) > 0 {
			cleaned := strings.Join(outLines, "\n") + "\n"
			// Only write if we actually removed something.
			if cleaned != string(idxData) {
				_ = os.WriteFile(indexPath, []byte(cleaned), 0o600)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// resolveMemoryPath resolves fname to an absolute path. "inherited" targets
// the home directory, and so do "" and "project": the server has no project of
// its own, so the historical default is the shared tier. Any other source is a
// per-project slug from the expanded listing (handleGetMemories) and resolves
// under the memories root — Base() pins it to a single path segment and the
// directory must already exist, so a crafted source can't escape the root or
// conjure new directories.
func (s *Server) resolveMemoryPath(fname, source string) (string, bool) {
	switch source {
	case "inherited":
		if s.homeMemDir != "" {
			return filepath.Join(s.homeMemDir, fname), true
		}
	case "", "project":
		if s.homeMemDir != "" {
			return filepath.Join(s.homeMemDir, fname), true
		}
	default:
		root, err := memory.RootDir()
		if err != nil {
			return "", false
		}
		// Base can still yield "." or ".." (Base("../..") == ".."), which
		// would resolve to the root itself or its parent — reject those so a
		// slug is always exactly one component below the root.
		slug := filepath.Base(source)
		if slug == "." || slug == ".." {
			return "", false
		}
		dir := filepath.Join(root, slug)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, fname), true
		}
	}
	return "", false
}
