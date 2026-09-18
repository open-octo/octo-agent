package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/open-octo/octo-agent/internal/profiles"
)

// Profile management — the HTTP side of `octo profiles`, behind 设置 → 数据管理
// in the Web UI. A profile is one ~/.octo-<name> data root; this server runs
// under exactly one of them, and internal/profiles refuses to remove that one
// (or the default root, or any root whose backend is alive), so the routes
// only need to translate its errors into status codes.

// handleListProfiles GET /api/profiles
func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	infos, err := profiles.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profiles": infos,
		"current":  profiles.Current(),
	})
}

// handleCreateProfile POST /api/profiles {"name": "work"}
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	info, err := profiles.Create(strings.TrimSpace(req.Name))
	if err != nil {
		writeError(w, profileErrStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

// handleDeleteProfile DELETE /api/profiles/{name}
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing profile name")
		return
	}
	if err := profiles.Remove(name); err != nil {
		writeError(w, profileErrStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func profileErrStatus(err error) int {
	switch {
	case errors.Is(err, profiles.ErrInvalidName):
		return http.StatusBadRequest
	case errors.Is(err, profiles.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, profiles.ErrExists),
		errors.Is(err, profiles.ErrDefault),
		errors.Is(err, profiles.ErrCurrent),
		errors.Is(err, profiles.ErrRunning):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
