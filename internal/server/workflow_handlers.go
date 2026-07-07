package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── GET /api/workflows/{name} ──────────────────────────────────────────────

// handleGetWorkflow returns one workflow's full detail, including its script,
// for the web panel's view-source modal.
func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing workflow name")
		return
	}
	detail, ok := tools.GetNamedWorkflow(name)
	if !ok {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// ─── DELETE /api/workflows/{name} ───────────────────────────────────────────

// handleDeleteWorkflow removes a user- or project-level workflow file.
// Built-in (source=default) workflows can't be deleted.
func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing workflow name")
		return
	}
	if err := tools.DeleteWorkflow(name); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, tools.ErrWorkflowNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}

// ─── GET /api/workflows/{name}/export ───────────────────────────────────────

// handleExportWorkflow streams a workflow's script as a .rb download. Unlike
// a skill (a directory of files, hence a zip), a saved workflow is always a
// single file, so the raw script is the natural portable form.
func (s *Server) handleExportWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing workflow name")
		return
	}
	detail, ok := tools.GetNamedWorkflow(name)
	if !ok {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".rb"))
	_, _ = io.WriteString(w, strings.TrimSuffix(detail.Script, "\n")+"\n")
}
