package server

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/memory"
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
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
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
	// Callers pass fname through Base(), but Base("..") is still ".." and
	// Base("") is "." — joined below, one escapes the memory directory and the
	// other addresses it directly. IsLocal rejects ".." (and absolute paths)
	// but accepts ".", so that one needs its own check.
	if fname == "." || !filepath.IsLocal(fname) {
		return "", false
	}
	var dir string
	switch source {
	case "", "inherited", "project":
		if s.homeMemDir == "" {
			return "", false
		}
		dir = s.homeMemDir
	default:
		root, err := memory.RootDir()
		if err != nil {
			return "", false
		}
		// Base can still yield "." or ".." (Base("../..") == ".."), which
		// would resolve to the root itself or its parent — reject those so a
		// slug is always exactly one component below the root. Same IsLocal
		// idiom as the fname check above: for a single component it rejects
		// exactly ".." (and absolute paths), and CodeQL recognizes it as a
		// path-injection barrier where a plain ".." comparison is not.
		slug := filepath.Base(source)
		if slug == "." || !filepath.IsLocal(slug) {
			return "", false
		}
		d := filepath.Join(root, slug)
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			return "", false
		}
		dir = d
	}
	// Belt and braces on top of the checks above: the resolved path must stay
	// strictly inside the memory directory. Also the containment proof static
	// analysis (CodeQL go/path-injection) recognizes at the read/remove sinks.
	p := filepath.Join(dir, fname)
	if !strings.HasPrefix(p, dir+string(os.PathSeparator)) {
		return "", false
	}
	return p, true
}
