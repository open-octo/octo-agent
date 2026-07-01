package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/open-octo/octo-agent/internal/browser"
	"github.com/open-octo/octo-agent/internal/tools"
)

// Browser recordings = the editable YAML skills produced by record_stop and
// replayed by run_skill. The web "Browser" view manages them; recording itself
// stays in chat (the user demonstrates in their real Chrome).

// recordingSummary is the list-view shape: enough to show a card without the
// full step body.
type recordingSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Steps       int      `json:"steps"`
	Params      []string `json:"params,omitempty"`
}

// safeRecordingName rejects empty names and anything that could escape the
// skills dir. Recorded names may be non-ASCII (e.g. Chinese), so we don't
// allowlist characters — only block separators and traversal.
func safeRecordingName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}

func recordingPath(name string) string {
	return filepath.Join(tools.BrowserSkillsDir(), name+".yaml")
}

// GET /api/browser/recordings — list recorded workflows.
func (s *Server) handleListBrowserRecordings(w http.ResponseWriter, _ *http.Request) {
	dir := tools.BrowserSkillsDir()
	entries, err := os.ReadDir(dir)
	out := make([]recordingSummary, 0)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			sk, lerr := browser.LoadSkill(filepath.Join(dir, e.Name()))
			if lerr != nil {
				continue // skip unparseable files rather than failing the list
			}
			params := make([]string, 0, len(sk.Params))
			for _, p := range sk.Params {
				params = append(params, p.Name)
			}
			name := strings.TrimSuffix(e.Name(), ".yaml")
			out = append(out, recordingSummary{Name: name, Description: sk.Description, Steps: len(sk.Steps), Params: params})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"recordings": out})
}

// GET /api/browser/recordings/{name} — raw YAML for viewing/editing.
func (s *Server) handleGetBrowserRecording(w http.ResponseWriter, r *http.Request) {
	name, ok := safeRecordingName(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid recording name")
		return
	}
	data, err := os.ReadFile(recordingPath(name))
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("recording %q not found", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read recording: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "yaml": string(data)})
}

// PUT /api/browser/recordings/{name} — save an edited recording. The body's
// yaml must parse as a skill with at least one step; we keep the path's
// filename regardless of the name field inside.
func (s *Server) handleSaveBrowserRecording(w http.ResponseWriter, r *http.Request) {
	name, ok := safeRecordingName(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid recording name")
		return
	}
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sk, err := browser.ParseSkill([]byte(req.YAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid skill YAML: %v", err))
		return
	}
	if len(sk.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "a recording must have at least one step")
		return
	}
	dir := tools.BrowserSkillsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create skills dir: %v", err))
		return
	}
	if err := os.WriteFile(recordingPath(name), []byte(req.YAML), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write recording: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/browser/recordings/{name}
func (s *Server) handleDeleteBrowserRecording(w http.ResponseWriter, r *http.Request) {
	name, ok := safeRecordingName(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid recording name")
		return
	}
	if err := os.Remove(recordingPath(name)); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("recording %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("delete recording: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
