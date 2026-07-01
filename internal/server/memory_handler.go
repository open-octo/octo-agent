package server

import (
	"net/http"
	"os"
	"path/filepath"

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
	if err := trash.Move(p, filepath.Dir(p)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// resolveMemoryPath resolves fname to an absolute path. If source is
// "inherited" it looks only in homeMemDir; otherwise it looks first in
// memDir then in homeMemDir.
func (s *Server) resolveMemoryPath(fname, source string) (string, bool) {
	if source == "inherited" && s.homeMemDir != "" {
		return filepath.Join(s.homeMemDir, fname), true
	}
	for _, dir := range []string{s.memDir, s.homeMemDir} {
		if dir != "" {
			return filepath.Join(dir, fname), true
		}
	}
	return "", false
}
