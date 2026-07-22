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
// session list), so stale IDs left by a deleted session are harmless. There is
// no GC pass for them — a stale ID is only dropped when its group is next
// rewritten (a move touching that group, or the frontend re-render filtering
// it out); the list grows at most by one dead entry per deleted session, which
// is negligible.
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
// PinnedSessionIDs is the Web-UI "pinned" set — sessions the user floated to a
// dedicated section at the top of the sidebar. It lives in this same file (the
// one web-only session-organisation layer) rather than on the session, so the
// CLI/TUI listing is unaffected. Array order is pin order; a pinned ID that no
// longer resolves to a live session is simply not rendered.
type groupFile struct {
	Groups           []sessionGroup `json:"groups"`
	PinnedSessionIDs []string       `json:"pinned_session_ids,omitempty"`
}

// groupMu serialises read-modify-write cycles on the registry within this
// process — the common case, since a given ~/.octo is normally served by one
// process. It does NOT coordinate across processes: if `octo serve` and the
// desktop shell run against the same ~/.octo at once, the atomic temp-file +
// rename in saveSessionGroups keeps the file from being corrupted, but two
// interleaved read-modify-write cycles can still lose one side's update
// (last writer wins). Acceptable for a single-user local tool; a cross-process
// lock would be overkill here.
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

// saveRegistry writes the whole registry (groups + pins) atomically (temp file
// + rename), the same pattern the scheduler uses. Caller must hold groupMu.
func saveRegistry(gf groupFile) error {
	if gf.Groups == nil {
		gf.Groups = []sessionGroup{}
	}
	path, err := sessionGroupsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(gf, "", "  ")
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

// saveSessionGroups persists the group list while preserving the pinned-session
// list, which shares the same file — a group edit must never clobber pins.
// Caller must hold groupMu.
func saveSessionGroups(groups []sessionGroup) error {
	pins, err := loadPinnedSessions()
	if err != nil {
		return err
	}
	return saveRegistry(groupFile{Groups: groups, PinnedSessionIDs: pins})
}

// loadPinnedSessions reads the pinned-session list from the registry. A missing
// file means nothing is pinned. Caller should hold groupMu for
// read-modify-write cycles.
func loadPinnedSessions() ([]string, error) {
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
	return gf.PinnedSessionIDs, nil
}

// savePinnedSessions persists the pinned-session list while preserving the
// group list, mirroring saveSessionGroups. Caller must hold groupMu.
func savePinnedSessions(pins []string) error {
	groups, err := loadSessionGroups()
	if err != nil {
		return err
	}
	return saveRegistry(groupFile{Groups: groups, PinnedSessionIDs: pins})
}

// newGroupID returns a short random group id ("g-" + 8 hex chars).
func newGroupID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "g-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "g-" + hex.EncodeToString(b[:])
}

// ─── Programmatic group helpers (used by the scheduler) ─────────────────────
//
// Cron tasks file each run's session under a per-task group (see
// internal/server/tasks_handlers.go). These helpers own groupMu themselves, so
// the scheduler path can create/rename/delete groups and add sessions without
// duplicating the load-modify-save cycle the HTTP handlers run inline.

// createSessionGroupNamed creates a new group with the given name and returns
// it. The caller records the group's ID on the task so later runs reuse it.
func createSessionGroupNamed(name string) (sessionGroup, error) {
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		return sessionGroup{}, err
	}
	g := sessionGroup{ID: newGroupID(), Name: name, SessionIDs: []string{}}
	groups = append(groups, g)
	if err := saveSessionGroups(groups); err != nil {
		return sessionGroup{}, err
	}
	return g, nil
}

// addSessionToGroup appends a session ID to a group, enforcing single
// membership (the session is first removed from every other group, matching
// handleSetSessionGroup). Returns an error if the target group no longer
// exists.
func addSessionToGroup(groupID, sessionID string) error {
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		return err
	}
	found := false
	for i := range groups {
		ids := groups[i].SessionIDs[:0]
		for _, existing := range groups[i].SessionIDs {
			if existing != sessionID {
				ids = append(ids, existing)
			}
		}
		groups[i].SessionIDs = ids
		if groups[i].ID == groupID {
			groups[i].SessionIDs = append(groups[i].SessionIDs, sessionID)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("session group %q not found", groupID)
	}
	return saveSessionGroups(groups)
}

// renameSessionGroup renames a group by ID. A no-op (nil) if the group is gone
// — a task's group may have been deleted manually in the UI.
func renameSessionGroup(groupID, name string) error {
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		return err
	}
	for i := range groups {
		if groups[i].ID == groupID {
			groups[i].Name = name
			return saveSessionGroups(groups)
		}
	}
	return nil
}

// deleteSessionGroup removes a group by ID, leaving its member sessions intact
// (they fall back to ungrouped). A no-op if the group is already gone.
func deleteSessionGroup(groupID string) error {
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		return err
	}
	out := groups[:0]
	found := false
	for _, g := range groups {
		if g.ID == groupID {
			found = true
			continue
		}
		out = append(out, g)
	}
	if !found {
		return nil
	}
	return saveSessionGroups(out)
}

// ─── GET /api/session-groups ────────────────────────────────────────────────

func (s *Server) handleListSessionGroups(w http.ResponseWriter, r *http.Request) {
	groupMu.Lock()
	groups, err := loadSessionGroups()
	var pins []string
	if err == nil {
		pins, err = loadPinnedSessions()
	}
	groupMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if groups == nil {
		groups = []sessionGroup{}
	}
	if pins == nil {
		pins = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "pinned_session_ids": pins})
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

// ─── PUT /api/session-groups/order ──────────────────────────────────────────

type reorderSessionGroupsRequest struct {
	// IDs is the full group list in the desired order. The frontend's up/down
	// controls submit the whole reordered sequence rather than a single move,
	// so the same endpoint also serves a future drag-to-reorder.
	IDs []string `json:"ids"`
}

// handleReorderSessionGroups rewrites the group order to match the given ID
// sequence. Unknown IDs are ignored; any existing group missing from the
// request is appended in its original relative order, so a stale client view
// can never drop a group.
func (s *Server) handleReorderSessionGroups(w http.ResponseWriter, r *http.Request) {
	var req reorderSessionGroupsRequest
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

	byID := make(map[string]sessionGroup, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	ordered := make([]sessionGroup, 0, len(groups))
	placed := make(map[string]bool, len(groups))
	for _, id := range req.IDs {
		if g, ok := byID[id]; ok && !placed[id] {
			ordered = append(ordered, g)
			placed[id] = true
		}
	}
	// Preserve any group the request omitted (append in original order).
	for _, g := range groups {
		if !placed[g.ID] {
			ordered = append(ordered, g)
		}
	}

	if err := saveSessionGroups(ordered); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "groups": ordered})
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

// ─── PUT /api/sessions/{id}/pin ─────────────────────────────────────────────

type setSessionPinRequest struct {
	Pinned bool `json:"pinned"`
}

// handleSetSessionPin pins or unpins a session. Pinning appends the ID to the
// end of the list (most-recently pinned last); unpinning removes it. The
// operation is idempotent — pinning an already-pinned session keeps its
// position, unpinning an absent one is a no-op.
func (s *Server) handleSetSessionPin(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("id")
	if sid == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	var req setSessionPinRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	pins, err := loadPinnedSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Drop any existing entry first, then re-add at the end when pinning. This
	// keeps the list free of duplicates and gives a stable "unpin" path.
	out := make([]string, 0, len(pins)+1)
	for _, id := range pins {
		if id != sid {
			out = append(out, id)
		}
	}
	if req.Pinned {
		out = append(out, sid)
	}

	if err := savePinnedSessions(out); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pinned": req.Pinned})
}
