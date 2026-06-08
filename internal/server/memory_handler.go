package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/Leihb/octo-agent/internal/trash"
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
	p, ok := s.resolveMemoryPath(fname)
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
	p, ok := s.resolveMemoryPath(fname)
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
	if err := trash.Move(p, filepath.Dir(p)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// resolveMemoryPath looks for fname first in the project memory dir, then in
// the inherited (home) memory dir. The bool reports whether a valid dir was
// found (not whether the file exists).
func (s *Server) resolveMemoryPath(fname string) (string, bool) {
	for _, dir := range []string{s.memDir, s.homeMemDir} {
		if dir != "" {
			return filepath.Join(dir, fname), true
		}
	}
	return "", false
}
