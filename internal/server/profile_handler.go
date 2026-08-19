package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/prompt"
	"github.com/open-octo/octo-agent/internal/trash"
)

// ─── Profile API ──────────────────────────────────────────────────────────

func (s *Server) handleGetProfileSoul(w http.ResponseWriter, r *http.Request) {
	path := prompt.IdentityPath(octoDir(), "soul.md")
	content, err := os.ReadFile(path)
	if err != nil {
		// No soul.md yet is the normal not-customized-yet state, not an error the
		// UI should surface — the Profile view already renders an empty-state
		// message when content is blank.
		if !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		content = nil
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
		if !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		content = nil
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
	appendDir := func(dir, label string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
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
				Path:      filepath.Join(dir, e.Name()),
				Size:      info.Size(),
				UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
				Source:    label,
			})
		}
	}

	// Memory disabled — the panel stays empty rather than surfacing
	// directories no session can write to.
	if s.cfg.NoMemory {
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
		return
	}

	// The shared tier first, the row the UI has always shown at the top. There
	// is no longer a second server-level tier beside it: the server has no
	// project of its own, so every project directory below is reached through
	// the session-group registry instead.
	if s.homeMemDir != "" {
		appendDir(s.homeMemDir, "inherited")
	}

	// Then every other slug directory under the memories root: per-project
	// memories written by sessions working in those projects (sessionMemDir).
	// Each is labeled with its slug, which resolveMemoryPath accepts as the
	// `source` for GET/DELETE, so the rows stay addressable.
	if root, err := memory.RootDir(); err == nil {
		if entries, err := os.ReadDir(root); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dir := filepath.Join(root, e.Name())
				if dir == s.homeMemDir {
					continue
				}
				appendDir(dir, e.Name())
			}
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
	// on_conflict selects how to handle an occupied original path. Default is
	// the safe "abort": reply 409 so the UI can ask the user how to resolve it
	// (the inline overwrite-undo and the recall view pass "backup"/"rename").
	policy := trash.ConflictAbort
	switch r.URL.Query().Get("on_conflict") {
	case "backup":
		policy = trash.ConflictBackupExisting
	case "rename":
		policy = trash.ConflictRename
	}
	res, err := trash.Restore(id, policy)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "restored",
		"restored_to":        res.RestoredTo,
		"backed_up_existing": res.BackedUpExisting,
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
