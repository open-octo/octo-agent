package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/open-octo/octo-agent/internal/prompt"
	"github.com/open-octo/octo-agent/internal/trash"
)

// ─── Profile API ──────────────────────────────────────────────────────────

func (s *Server) handleGetProfileSoul(w http.ResponseWriter, r *http.Request) {
	path := prompt.IdentityPath(octoDir(), "soul.md")
	content, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "soul.md not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"content": string(content),
		"path":    path,
	})
}

func (s *Server) handleGetProfileUser(w http.ResponseWriter, r *http.Request) {
	path := prompt.IdentityPath(octoDir(), "user.md")
	content, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "user.md not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"content": string(content),
		"path":    path,
	})
}

func (s *Server) handleGetMemories(w http.ResponseWriter, r *http.Request) {
	type memFile struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Size      int64  `json:"size"`
		UpdatedAt string `json:"updated_at"`
		Source    string `json:"source"`
	}
	files := make([]memFile, 0)

	// If the project memory directory coincides with the inherited (home)
	// directory — e.g. the server was started from a non-repo path — listing
	// both would emit duplicate paths. The front-end keys rows by path, so
	// duplicates freeze the UI on "Loading memories…". Skip the inherited
	// directory when it is the same as the project one, matching the dedupe
	// already done in memory.RenderInjection.
	homeMemDir := s.homeMemDir
	if homeMemDir == s.memDir {
		homeMemDir = ""
	}

	for _, src := range []struct {
		dir   string
		label string
	}{{
		s.memDir, "project",
	}, {
		homeMemDir, "inherited",
	}} {
		if src.dir == "" {
			continue
		}
		entries, err := os.ReadDir(src.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, memFile{
				Name:      e.Name(),
				Path:      filepath.Join(src.dir, e.Name()),
				Size:      info.Size(),
				UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
				Source:    src.label,
			})
		}
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
	orphanCount := 0
	for _, e := range entries {
		totalSize += e.Size
		if e.Orphan {
			orphanCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":        entries,
		"total_count":  len(entries),
		"total_size":   totalSize,
		"orphan_count": orphanCount,
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
	// Default to the safe policy: never silently overwrite a file already at
	// the original path. On a conflict, reply 409 with the details so the UI
	// can ask the user how to resolve it.
	res, err := trash.Restore(id, trash.ConflictAbort)
	if err != nil {
		if errors.Is(err, trash.ErrRestoreConflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"conflict": true,
				"error":    err.Error(),
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "restored",
		"restored_to": res.RestoredTo,
	})
}

func (s *Server) handleDeleteTrash(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing file id")
		return
	}
	freed, err := trash.Delete(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"freed_size": freed,
	})
}

func octoDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".octo")
	}
	return filepath.Join(home, ".octo")
}
