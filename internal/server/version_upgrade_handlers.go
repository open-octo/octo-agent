package server

import (
	"net/http"

	"github.com/Leihb/octo-agent/internal/version"
)

// ─── POST /api/version/upgrade ──────────────────────────────────────────────

func (s *Server) handleVersionUpgrade(w http.ResponseWriter, r *http.Request) {
	// The Go rewrite is a single binary — upgrading it is outside the scope
	// of the server process. Return a stub that tells the user to use their
	// package manager or download a new release.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      false,
		"message": "Upgrade is not supported in the Go rewrite. Please install the latest release manually.",
	})
}

// ─── GET /api/version (extended) ────────────────────────────────────────────

func (s *Server) handleVersionExtended(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        version.Version,
		"needs_update":   false,
		"latest_version": version.Version,
	})
}
