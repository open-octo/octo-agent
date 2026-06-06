package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/Leihb/octo-agent/internal/trash"
)

// ─── Profile API ──────────────────────────────────────────────────────────

func (s *Server) handleGetProfileSoul(w http.ResponseWriter, r *http.Request) {
	content, err := readProfileFile("SOUL.md")
	if err != nil {
		writeError(w, http.StatusNotFound, "SOUL.md not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"content": content,
		"path":    filepath.Join(octoDir(), "SOUL.md"),
	})
}

func (s *Server) handleGetProfileUser(w http.ResponseWriter, r *http.Request) {
	content, err := readProfileFile("USER.md")
	if err != nil {
		writeError(w, http.StatusNotFound, "USER.md not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"content": content,
		"path":    filepath.Join(octoDir(), "USER.md"),
	})
}

func (s *Server) handleGetMemories(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(octoDir(), "memories")
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"files": []any{}})
		return
	}

	type memFile struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	files := make([]memFile, 0)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		files = append(files, memFile{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handleGetTrash(w http.ResponseWriter, r *http.Request) {
	entries, err := trash.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []trash.Entry{}
	}
	var totalSize int64
	for _, e := range entries {
		totalSize += e.Size
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":       entries,
		"total_count": len(entries),
		"total_size":  totalSize,
	})
}

type emptyTrashRequest struct {
	Mode string `json:"mode"` // "all", "old", "orphans"
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	var req emptyTrashRequest
	if err := readBodyJSON(r, &req); err != nil {
		req.Mode = "all"
	}
	count, freed, err := trash.Empty(req.Mode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"removed":    count,
		"freed_size": freed,
	})
}

func (s *Server) handleRestoreTrash(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing file id")
		return
	}
	if err := trash.Restore(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (s *Server) handleDeleteTrash(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing file id")
		return
	}
	entries, err := trash.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, e := range entries {
		if e.ID == id {
			var freed int64
			if info, err := os.Stat(e.TrashPath); err == nil {
				freed = info.Size()
			}
			os.Remove(e.TrashPath)
			os.Remove(e.TrashPath + ".meta.json")
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":         true,
				"freed_size": freed,
			})
			return
		}
	}
	writeError(w, http.StatusNotFound, "trash entry not found")
}

func readProfileFile(name string) (string, error) {
	p := filepath.Join(octoDir(), name)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func octoDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".octo")
	}
	return filepath.Join(home, ".octo")
}
