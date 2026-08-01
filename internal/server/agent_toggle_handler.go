package server

import (
	"net/http"

	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/config"
)

// ─── PATCH /api/agents/{id}/toggle ──────────────────────────────────────────

// handleToggleAgent hides or re-shows a curated (SourceDefault) expert in the
// gallery. Mirrors handleToggleSkill: only curated experts can be toggled —
// user-created agents have no visibility switch (they're just deleted), and
// builtins never reach the store's public surface at all.
func (s *Server) handleToggleAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing agent id")
		return
	}

	store, ok := s.agentStoreIfReady()
	if !ok {
		_ = s.agentRouter()
		store = s.agentStore
	}

	p, ok := store.LookupAny(id)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if p.Source != agentprofile.SourceDefault {
		writeError(w, http.StatusBadRequest, "only curated default experts can be hidden/shown")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}

	currentlyDisabled := false
	for _, n := range cfg.Agents.DisabledDefaults {
		if n == id {
			currentlyDisabled = true
			break
		}
	}

	if currentlyDisabled {
		newDisabled := make([]string, 0, len(cfg.Agents.DisabledDefaults)-1)
		for _, n := range cfg.Agents.DisabledDefaults {
			if n != id {
				newDisabled = append(newDisabled, n)
			}
		}
		cfg.Agents.DisabledDefaults = newDisabled
	} else {
		cfg.Agents.DisabledDefaults = append(cfg.Agents.DisabledDefaults, id)
	}

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}

	store.SetDisabledDefaults(cfg.Agents.DisabledDefaults)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"enabled": currentlyDisabled, // toggle: disabled → enabled, enabled → disabled
	})
}
