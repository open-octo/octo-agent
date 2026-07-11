package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Session groups are a Web-UI-only organisation layer: a way to cluster the
// sidebar session list into named, collapsible groups when there are too many
// sessions to scan flat. They live entirely in one registry file
// (~/.octo/session-groups.json) and never touch the session transcript format
// — group membership is stored here as group→session-ID lists, so the CLI/TUI
// session listing is unaffected and no session field is added. A session
// belongs to at most one group; a session ID that no longer resolves to a real
// transcript is simply not rendered (the frontend cross-references the live
// session list), so stale IDs left by a deleted session are harmless.
//
// The desktop app (cmd/octo-desktop) runs this same server in-process against
// the same ~/.octo, so groups and their collapsed state are shared between the
// Web UI and the desktop shell with no extra wiring.

// sessionGroup is one named group in the registry.
type sessionGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SessionIDs []string `json:"session_ids"`
	// Collapsed is persisted server-side (rather than in browser localStorage)
	// so the folded/expanded state survives across reloads and is identical in
	// the Web UI and the Wails desktop webview, whose local storage may not
	// persist the same way.
	Collapsed bool `json:"collapsed,omitempty"`
}

// groupFile is the on-disk shape of the registry. Group order is array order.
type groupFile struct {
	Groups []sessionGroup `json:"groups"`
}

// groupMu serialises read-modify-write cycles on the registry. The path is
// process-global (it derives from HOME, fixed for the process lifetime), so a
// single package-level mutex is sufficient and correct.
var groupMu sync.Mutex

// sessionGroupsPath returns ~/.octo/session-groups.json, creating ~/.octo.
func sessionGroupsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("session groups: home dir: %w", err)
	}
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("session groups: mkdir %s: %w", dir, err)
	}
	return filepath.Join(dir, "session-groups.json"), nil
}

// loadSessionGroups reads the registry. A missing file is not an error — it
// means no groups yet. Caller should hold groupMu for read-modify-write cycles.
func loadSessionGroups() ([]sessionGroup, error) {
	path, err := sessionGroupsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session groups: read %s: %w", path, err)
	}
	var gf groupFile
	if err := json.Unmarshal(data, &gf); err != nil {
		return nil, fmt.Errorf("session groups: parse %s: %w", path, err)
	}
	return gf.Groups, nil
}

// saveSessionGroups writes the registry atomically (temp file + rename), the
// same pattern the scheduler uses. Caller must hold groupMu.
func saveSessionGroups(groups []sessionGroup) error {
	if groups == nil {
		groups = []sessionGroup{}
	}
	path, err := sessionGroupsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(groupFile{Groups: groups}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + "." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("session groups: write %s: %w", path, err)
	}
	return nil
}

// newGroupID returns a short random group id ("g-" + 8 hex chars).
func newGroupID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "g-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "g-" + hex.EncodeToString(b[:])
}

// ─── GET /api/session-groups ────────────────────────────────────────────────

func (s *Server) handleListSessionGroups(w http.ResponseWriter, r *http.Request) {
	groupMu.Lock()
	groups, err := loadSessionGroups()
	groupMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if groups == nil {
		groups = []sessionGroup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// ─── POST /api/session-groups ───────────────────────────────────────────────

type createSessionGroupRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateSessionGroup(w http.ResponseWriter, r *http.Request) {
	var req createSessionGroupRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	g := sessionGroup{ID: newGroupID(), Name: name, SessionIDs: []string{}}
	groups = append(groups, g)
	if err := saveSessionGroups(groups); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": g})
}

// ─── PATCH /api/session-groups/{id} ─────────────────────────────────────────

// updateSessionGroupRequest carries the editable group fields. Both are
// optional pointers so a request can rename, toggle collapsed, or both, without
// one clobbering the other.
type updateSessionGroupRequest struct {
	Name      *string `json:"name,omitempty"`
	Collapsed *bool   `json:"collapsed,omitempty"`
}

func (s *Server) handleUpdateSessionGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing group id")
		return
	}
	var req updateSessionGroupRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == nil && req.Collapsed == nil {
		writeError(w, http.StatusBadRequest, "name or collapsed is required")
		return
	}
	var name string
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	idx := -1
	for i := range groups {
		if groups[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if req.Name != nil {
		groups[idx].Name = name
	}
	if req.Collapsed != nil {
		groups[idx].Collapsed = *req.Collapsed
	}
	if err := saveSessionGroups(groups); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": groups[idx]})
}

// ─── DELETE /api/session-groups/{id} ────────────────────────────────────────

// handleDeleteSessionGroup removes a group. Its member sessions are not
// deleted — they fall back to "ungrouped".
func (s *Server) handleDeleteSessionGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing group id")
		return
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := groups[:0]
	found := false
	for _, g := range groups {
		if g.ID == id {
			found = true
			continue
		}
		out = append(out, g)
	}
	if !found {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if err := saveSessionGroups(out); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─── PUT /api/sessions/{id}/group ───────────────────────────────────────────

type setSessionGroupRequest struct {
	// GroupID is the target group. Empty removes the session from every group
	// (i.e. moves it to "ungrouped").
	GroupID string `json:"group_id"`
}

// handleSetSessionGroup moves a session into a group (or out of all groups).
// A session belongs to at most one group, so it is first removed from every
// group and then, if a non-empty target is given, appended to that group.
func (s *Server) handleSetSessionGroup(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("id")
	if sid == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	var req setSessionGroupRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Remove the session from every group first (enforces single membership).
	for i := range groups {
		ids := groups[i].SessionIDs[:0]
		for _, existing := range groups[i].SessionIDs {
			if existing != sid {
				ids = append(ids, existing)
			}
		}
		groups[i].SessionIDs = ids
	}

	// Add to the target group when one is requested.
	if req.GroupID != "" {
		idx := -1
		for i := range groups {
			if groups[i].ID == req.GroupID {
				idx = i
				break
			}
		}
		if idx < 0 {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		groups[idx].SessionIDs = append(groups[idx].SessionIDs, sid)
	}

	if err := saveSessionGroups(groups); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "group_id": req.GroupID})
}
