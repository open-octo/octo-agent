package server

import (
	"net/http"

	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/skills"
)

// ─── PATCH /api/skills/{name}/toggle ────────────────────────────────────────

func (s *Server) handleToggleSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing skill name")
		return
	}

	// Verify the skill exists (including disabled ones).
	found := false
	for _, sk := range s.skillReg.All() {
		if sk.Name == name {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	// Load current config.
	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}

	// Determine whether the skill is currently disabled.
	currentlyDisabled := false
	for _, n := range cfg.Tools.DisabledSkills {
		if n == name {
			currentlyDisabled = true
			break
		}
	}

	if currentlyDisabled {
		// Enable: remove from the disabled list.
		newDisabled := make([]string, 0, len(cfg.Tools.DisabledSkills)-1)
		for _, n := range cfg.Tools.DisabledSkills {
			if n != name {
				newDisabled = append(newDisabled, n)
			}
		}
		cfg.Tools.DisabledSkills = newDisabled
	} else {
		// Disable: add to the disabled list.
		cfg.Tools.DisabledSkills = append(cfg.Tools.DisabledSkills, name)
	}

	// Persist.
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}

	// Update in-memory state so new sessions see the change immediately.
	s.skillReg.SetDisabled(cfg.Tools.DisabledSkills)
	s.skillsManifest = skills.RenderManifest(s.skillReg)

	writeJSON(w, http.StatusOK, map[string]any{
		"name":    name,
		"enabled": currentlyDisabled, // toggle: disabled → enabled, enabled → disabled
	})
}

// ─── DELETE /api/skills/{name} ──────────────────────────────────────────────

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing skill name")
		return
	}

	// Verify the skill exists (including disabled ones).
	found := false
	for _, sk := range s.skillReg.All() {
		if sk.Name == name {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	if err := s.skillReg.Delete(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Update in-memory manifest so new sessions see the change immediately.
	s.skillsManifest = skills.RenderManifest(s.skillReg)

	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}
