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
	if err := trash.Move(p, filepath.Dir(p)); err != nil {
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
