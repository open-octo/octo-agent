package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/memory"
)

// The sidebar has exactly two concepts the user can create: a task (one loose
// session) and a project (a named directory plus the sessions working in it).
// This registry is where projects live, and it also holds the one group the
// system makes on its own: a cron task's per-run cluster. There is no third
// user-facing concept — a "plain group", a name with sessions under it and no
// directory, used to exist and no longer does: it split the sidebar into three
// kinds of row for two kinds of thing, and offered organisation that carried
// none of what a project carries (a directory for the tools, a memory tier, a
// notes block). dissolvePlainGroups retires the ones already on disk.
//
// The registry lives entirely in one file
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

// sessionGroup is one project in the registry: a working directory plus the
// sessions working in it. WorkingDir being set is what makes it one, and a group
// without one is a plain group — a concept that no longer exists, dissolved at
// startup.
//
// A scheduled task's runs live in a project too, created by the scheduler and
// named after the task, working in <workspace>/<task name>. TaskID records which
// task that was: it is what lets the startup pass repair such a project instead
// of dissolving it, and it survives a scheduler that failed to start. It carries
// no UI or permission meaning — the project behaves like any other, because it is
// one.
type sessionGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SessionIDs []string `json:"session_ids"`
	// Collapsed is persisted server-side (rather than in browser localStorage)
	// so the folded/expanded state survives across reloads and is identical in
	// the Web UI and the Wails desktop webview, whose local storage may not
	// persist the same way.
	Collapsed bool `json:"collapsed,omitempty"`
	// WorkingDir, when set, makes this group a project (see above). Stored
	// already expanded and absolute — the write paths resolve and validate it,
	// so every reader can use it verbatim.
	WorkingDir string `json:"working_dir,omitempty"`
	// Notes is project-level context injected into the system prompt of every
	// session in the project, alongside the project-memory layer. Only
	// meaningful on a project.
	Notes string `json:"notes,omitempty"`
	// TaskID, when set, is the scheduled task this project was created for. It
	// is what lets the startup pass repair such a project (backfilling the
	// directory a task written before this had none) rather than dissolving it.
	TaskID string `json:"task_id,omitempty"`
}

// isProject reports whether this group carries project settings.
func (g sessionGroup) isProject() bool { return g.WorkingDir != "" }

// isCronCluster reports whether this project was created for a scheduled task.
// Used by the startup repair pass, not to gate anything the user can do.
func (g sessionGroup) isCronCluster() bool { return g.TaskID != "" }

// maxProjectNotes caps the notes field. Notes are injected verbatim into the
// system prompt of EVERY session in the project, so an oversized value would
// silently eat context across all of them; 16 KiB is far beyond any sane
// project brief while still an obvious mistake-catcher.
const maxProjectNotes = 16 * 1024

// validateProjectNotes trims and bounds a user-supplied notes value. The
// returned error is user-facing.
func validateProjectNotes(raw string) (string, error) {
	notes := strings.TrimSpace(raw)
	if len(notes) > maxProjectNotes {
		return "", fmt.Errorf("notes too long: %d bytes (max %d) — notes are injected into every session's system prompt, keep them brief", len(notes), maxProjectNotes)
	}
	return notes, nil
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
	// CollapsedSessionIDs is the Web-UI "collapsed" set — sessions the user
	// explicitly tucked into a folded panel at the bottom of the sidebar to keep
	// the main list short. Mutually exclusive with pinning and group membership
	// (a collapsed session belongs to neither); the write handlers enforce that.
	// Array order is collapse order; an ID that no longer resolves to a live
	// session is simply not rendered.
	CollapsedSessionIDs []string `json:"collapsed_session_ids,omitempty"`
}

// notifyGroupsChanged, when set (Server.New), is called after every successful
// registry write so open Web/desktop tabs can refetch the groups snapshot —
// without it, a project's working directory changed in one tab stays stale in
// every other until reload, and a stale project directory misleads about
// where a session's tools run. One hook at the single write point
// (saveRegistry) rather than a broadcast per HTTP handler: the programmatic
// writers (cron's group helpers, create-session-in-group) are covered too,
// and no future write path can forget it. Called under groupMu — the hub's
// buffered events channel makes that non-blocking in practice, same as every
// other broadcast issued under a lock.
//
// Package-level like groupMu/regCache: the registry is per-~/.octo process
// state, and the last Server to start owns the notification. A no-op when nil
// (tests that never construct a Server).
var notifyGroupsChanged func()

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

// loadRegistryFile reads and parses the whole registry file. A missing file is
// not an error — it means an empty registry. The field-scoped load/save
// helpers below all go through it, so a save of one field can never clobber
// the others. Caller should hold groupMu for read-modify-write cycles.
func loadRegistryFile() (groupFile, error) {
	var gf groupFile
	path, err := sessionGroupsPath()
	if err != nil {
		return gf, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return gf, nil
		}
		return gf, fmt.Errorf("session groups: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &gf); err != nil {
		return gf, fmt.Errorf("session groups: parse %s: %w", path, err)
	}
	return gf, nil
}

// loadSessionGroups reads the registry. A missing file is not an error — it
// means no groups yet. Caller should hold groupMu for read-modify-write cycles.
func loadSessionGroups() ([]sessionGroup, error) {
	gf, err := loadRegistryFile()
	if err != nil {
		return nil, err
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
	invalidateRegistryCache()
	if notifyGroupsChanged != nil {
		notifyGroupsChanged()
	}
	return nil
}

// saveSessionGroups persists the group list while preserving every other list
// sharing the same file (pins, collapsed) — a group edit must never clobber
// them. Caller must hold groupMu.
func saveSessionGroups(groups []sessionGroup) error {
	gf, err := loadRegistryFile()
	if err != nil {
		return err
	}
	gf.Groups = groups
	return saveRegistry(gf)
}

// loadPinnedSessions reads the pinned-session list from the registry. A missing
// file means nothing is pinned. Caller should hold groupMu for
// read-modify-write cycles.
func loadPinnedSessions() ([]string, error) {
	gf, err := loadRegistryFile()
	if err != nil {
		return nil, err
	}
	return gf.PinnedSessionIDs, nil
}

// loadCollapsedSessions reads the collapsed-session list from the registry. A
// missing file means nothing is collapsed. Caller should hold groupMu for
// read-modify-write cycles.
func loadCollapsedSessions() ([]string, error) {
	gf, err := loadRegistryFile()
	if err != nil {
		return nil, err
	}
	return gf.CollapsedSessionIDs, nil
}

// ─── Read-side cache + session→project reverse index ────────────────────────
//
// Resolving a session's working directory now has to answer "which project
// owns this session?", and the session-list endpoint asks that once per
// session. Re-reading and re-parsing session-groups.json N times per request
// would be silly, so reads go through a small cache holding the parsed
// registry plus a sessionID→project index built alongside it.
//
// Invalidation is belt-and-braces: saveRegistry drops the cache outright
// (exact for this process's own writes), and every load re-stats the file so a
// write by another process serving the same ~/.octo (the desktop shell) is
// picked up too. Callers must treat the returned data as READ-ONLY — it is the
// cached copy, not a clone. The read-modify-write paths deliberately keep
// using loadSessionGroups, which always re-reads from disk and hands back
// data they may mutate freely.

type registryCache struct {
	// path is part of the key, not just modTime/size: HOME is redirected
	// per-test, and without it a cache entry from one temp dir could be served
	// for another whose file happens to match in size and timestamp.
	path     string
	modTime  time.Time
	size     int64
	file     groupFile
	projects map[string]*sessionGroup // session ID → the project owning it
}

// regCache is guarded by groupMu, like every other access to the registry.
var regCache *registryCache

// invalidateRegistryCache drops the cached registry. Caller must hold groupMu.
func invalidateRegistryCache() { regCache = nil }

// cachedRegistry returns the parsed registry and the session→project index,
// re-reading the file only when it has changed on disk. The returned values
// are read-only. Caller must hold groupMu.
func cachedRegistry() (groupFile, map[string]*sessionGroup, error) {
	path, err := sessionGroupsPath()
	if err != nil {
		return groupFile{}, nil, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			// No registry yet — nothing is grouped. Cache nothing; a stat per
			// call is already the cheap path.
			return groupFile{}, nil, nil
		}
		return groupFile{}, nil, fmt.Errorf("session groups: stat %s: %w", path, statErr)
	}
	if regCache != nil && regCache.path == path && regCache.modTime.Equal(info.ModTime()) && regCache.size == info.Size() {
		return regCache.file, regCache.projects, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return groupFile{}, nil, fmt.Errorf("session groups: read %s: %w", path, err)
	}
	var gf groupFile
	if err := json.Unmarshal(data, &gf); err != nil {
		return groupFile{}, nil, fmt.Errorf("session groups: parse %s: %w", path, err)
	}
	projects := make(map[string]*sessionGroup)
	for i := range gf.Groups {
		g := &gf.Groups[i]
		if !g.isProject() {
			continue
		}
		for _, sid := range g.SessionIDs {
			projects[sid] = g
		}
	}
	regCache = &registryCache{path: path, modTime: info.ModTime(), size: info.Size(), file: gf, projects: projects}
	return gf, projects, nil
}

// projectForSession returns the project owning sessionID, or nil when the
// session is ungrouped or its group is a plain group. The result is read-only.
// Registry errors resolve to "no project" rather than propagating: a session's
// working directory must still resolve (to its own dir, or the server default)
// when the group file is unreadable.
func projectForSession(sessionID string) *sessionGroup {
	if sessionID == "" {
		return nil
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	_, projects, err := cachedRegistry()
	if err != nil {
		slog.Warn("resolve project for session", "session", sessionID, "err", err)
		return nil
	}
	return projects[sessionID]
}

// projectDirByGroupID returns the working directory of the project with this
// group id, or "" when no group has that id or the group is not a project
// (no working dir). Read-only, like projectForSession.
func projectDirByGroupID(id string) string {
	if id == "" {
		return ""
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		slog.Warn("resolve project dir by group", "group", id, "err", err)
		return ""
	}
	for _, g := range groups {
		if g.ID == id {
			return g.WorkingDir
		}
	}
	return ""
}

// ProjectDirForSession returns the working directory of the project owning
// sessionID, or "" when the session is not in one. Exported for the CLI/TUI,
// which have no server to ask: a session filed under a project should run in
// the project's directory there too, wherever octo happened to be launched
// from. Read-only by design — the CLI offers no way to change a working
// directory, so a project's setting can only be edited where it was made.
func ProjectDirForSession(sessionID string) string {
	if p := projectForSession(sessionID); p != nil {
		return p.WorkingDir
	}
	return ""
}

// ProjectExistsForDir reports whether some project already owns dir. Exported
// for the CLI, which treats the directory it runs in as the project and so
// always has memory of its own there — but under `octo serve` that same
// directory only gets project memory once it IS a project, so notes written
// from the CLI would go unread there. `octo memory` uses this to say so rather
// than let the difference stay invisible.
func ProjectExistsForDir(dir string) bool {
	if dir == "" {
		return false
	}
	target := memory.NormalizeDir(dir)
	groupMu.Lock()
	defer groupMu.Unlock()
	gf, _, err := cachedRegistry()
	if err != nil {
		return false
	}
	for i := range gf.Groups {
		if wd := gf.Groups[i].WorkingDir; wd != "" && memory.NormalizeDir(wd) == target {
			return true
		}
	}
	return false
}

// projectNotesFor returns the project notes that apply to sessionID, or "".
func projectNotesFor(sessionID string) string {
	if p := projectForSession(sessionID); p != nil {
		return p.Notes
	}
	return ""
}

// projectNotesAndDir returns both facts a turn needs from the project owning
// sessionID — its notes and its working directory — in one registry lookup.
// Both are empty when the session belongs to no project. The notes go into the
// prompt; the directory is what scopes the session's memory (see
// Server.sessionMemDir).
func projectNotesAndDir(sessionID string) (notes, dir string) {
	if p := projectForSession(sessionID); p != nil {
		return p.Notes, p.WorkingDir
	}
	return "", ""
}

// renderProjectNotes wraps project notes for the system prompt's memory layer.
// The heading gives the model a frame for what it is reading — otherwise a
// bare paragraph of project context reads as an instruction from the user.
func renderProjectNotes(notes string) string {
	return "# Project notes\n\nContext for the project this session belongs to:\n\n" + notes
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
//
// taskID marks the group as that cron task's run cluster, which is what lets it
// exist without a directory — the one group the user did not create and cannot
// edit. Pass "" from any other caller, in which case workingDir must be set or
// the group would be a plain group and dissolved at the next start.
//
// workingDir, when set, makes the group a project — which is what scopes its
// sessions' memory (see Server.sessionMemDir). A cron task with a directory is
// working on that directory every run, so its notes belong to it rather than to
// the tier every session on the machine reads. An unusable directory is dropped
// rather than failing the call: for a cron task the cluster still works without
// one, and a task must run regardless. Callers that need the directory to have
// landed check WorkingDir on the returned group.
func createSessionGroupNamed(name, workingDir, taskID string) (sessionGroup, error) {
	if workingDir != "" {
		dir, verr := validateWorkingDir(workingDir)
		if verr != nil {
			slog.Warn("group working dir unusable; dropping it",
				"group", name, "dir", workingDir, "err", verr)
			workingDir = ""
		} else {
			workingDir = dir
		}
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		return sessionGroup{}, err
	}
	g := sessionGroup{ID: newGroupID(), Name: name, SessionIDs: []string{}, WorkingDir: workingDir, TaskID: taskID}
	groups = append(groups, g)
	if err := saveSessionGroups(groups); err != nil {
		return sessionGroup{}, err
	}
	return g, nil
}

// addSessionToGroup prepends a session ID to a group — a newly created
// session shows at the top of its group, matching the newest-first session
// list — enforcing single membership (the session is first removed from every
// other group). Returns an error if the target group no longer exists.
//
// This is a creation-time path only: the session-creation handler and the
// scheduler. There is no move-between-projects path (see
// handleSetSessionGroup).
func addSessionToGroup(groupID, sessionID string) error {
	groupMu.Lock()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		return err
	}
	groups := gf.Groups
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
			groups[i].SessionIDs = append([]string{sessionID}, groups[i].SessionIDs...)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("session group %q not found", groupID)
	}
	// Group membership and collapsing are mutually exclusive (handleSetSessionCollapse
	// refuses to collapse a session in a group). Since this is the only path that
	// files a session anywhere, clearing a stale collapsed entry has to happen here
	// for that guard to hold from both directions.
	gf.Groups = groups
	gf.CollapsedSessionIDs = removeID(gf.CollapsedSessionIDs, sessionID)
	return saveRegistry(gf)
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
	gf, err := loadRegistryFile()
	groupMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if gf.Groups == nil {
		gf.Groups = []sessionGroup{}
	}
	if gf.PinnedSessionIDs == nil {
		gf.PinnedSessionIDs = []string{}
	}
	if gf.CollapsedSessionIDs == nil {
		gf.CollapsedSessionIDs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups":                gf.Groups,
		"pinned_session_ids":    gf.PinnedSessionIDs,
		"collapsed_session_ids": gf.CollapsedSessionIDs,
	})
}

// ─── POST /api/session-groups ───────────────────────────────────────────────

type createSessionGroupRequest struct {
	Name string `json:"name"`
	// WorkingDir is required: a group the user creates is a project, and a
	// project is a directory. Creating without one used to be how a plain group
	// was made, and that concept is gone.
	WorkingDir string `json:"working_dir"`
	Notes      string `json:"notes,omitempty"`
}

func (s *Server) handleCreateSessionGroup(w http.ResponseWriter, r *http.Request) {
	var req createSessionGroupRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(req.WorkingDir) == "" {
		writeError(w, http.StatusBadRequest, "working_dir is required: a project is a directory, and a group without one no longer exists")
		return
	}
	workingDir, verr := validateWorkingDir(req.WorkingDir)
	if verr != nil {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}
	notes, nerr := validateProjectNotes(req.Notes)
	if nerr != nil {
		writeError(w, http.StatusBadRequest, nerr.Error())
		return
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	g := sessionGroup{ID: newGroupID(), Name: name, SessionIDs: []string{}, WorkingDir: workingDir, Notes: notes}
	groups = append(groups, g)
	if err := saveSessionGroups(groups); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": g})
}

// ─── PATCH /api/session-groups/{id} ─────────────────────────────────────────

// updateSessionGroupRequest carries the editable group fields. All are
// optional pointers so a request can rename, toggle collapsed, retarget the
// project directory, or edit the notes, without one clobbering the others.
type updateSessionGroupRequest struct {
	Name      *string `json:"name,omitempty"`
	Collapsed *bool   `json:"collapsed,omitempty"`
	// WorkingDir retargets the project's directory. It cannot be cleared: that
	// used to demote the project to a plain group, and there is nothing left to
	// demote to — deleting the project is the way to break it up, which leaves
	// its sessions as tasks.
	WorkingDir *string `json:"working_dir,omitempty"`
	Notes      *string `json:"notes,omitempty"`
}

func (s *Server) handleUpdateSessionGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing group id")
		return
	}
	var req updateSessionGroupRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	if req.Name == nil && req.Collapsed == nil && req.WorkingDir == nil && req.Notes == nil {
		writeError(w, http.StatusBadRequest, "name, collapsed, working_dir or notes is required")
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
	// Validate the directory before touching the registry, so a bad path
	// leaves the group untouched.
	var workingDir string
	if req.WorkingDir != nil {
		if strings.TrimSpace(*req.WorkingDir) == "" {
			writeError(w, http.StatusBadRequest, "working_dir cannot be cleared: delete the project instead, which leaves its sessions as tasks")
			return
		}
		dir, verr := validateWorkingDir(*req.WorkingDir)
		if verr != nil {
			writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
		workingDir = dir
	}
	var notes string
	if req.Notes != nil {
		n, nerr := validateProjectNotes(*req.Notes)
		if nerr != nil {
			writeError(w, http.StatusBadRequest, nerr.Error())
			return
		}
		notes = n
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
	if req.WorkingDir != nil {
		groups[idx].WorkingDir = workingDir
	}
	if req.Notes != nil {
		groups[idx].Notes = notes
	}
	if err := saveSessionGroups(groups); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": groups[idx]})
}

// ─── DELETE /api/session-groups/{id} ────────────────────────────────────────

// handleDeleteSessionGroup removes a project. With ?sessions=delete its member
// sessions are deleted along with it; without it they are left on disk and become
// tasks.
//
// Deleting the sessions is what the UI asks for, and it is one request rather
// than "delete these ids, then delete the project" so a failure halfway cannot
// leave a project standing over sessions that are already gone. The sessions go
// first: a project whose sessions were deleted but which survived a crash is a
// visible empty row the user can delete again, while the reverse is a set of
// sessions with no row to reach them from.
func (s *Server) handleDeleteSessionGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing group id")
		return
	}
	withSessions := r.URL.Query().Get("sessions") == "delete"
	if withSessions {
		groupMu.Lock()
		groups, lerr := loadSessionGroups()
		var members []string
		if lerr == nil {
			for i := range groups {
				if groups[i].ID == id {
					members = append(members, groups[i].SessionIDs...)
					break
				}
			}
		}
		groupMu.Unlock()
		if lerr != nil {
			writeError(w, http.StatusInternalServerError, lerr.Error())
			return
		}
		if _, failed := s.deleteSessionsByID(members); len(failed) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":  "some sessions could not be deleted; the project was left in place",
				"failed": failed,
			})
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
		writeInvalidJSONBody(w, err)
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

// handleSetSessionGroup refuses every attempt to move a session between
// projects. Where a session lives is decided when it is created — by picking a
// directory on the landing page, or by the "+" on a project — and is fixed after
// that.
//
// It is fixed because moving is not one change but four, and they cannot be
// made to agree after the fact: the tools' directory, the memory tier, the
// project notes baked into the system prompt, and the hooks/sandbox root all
// derive from the project. A moved session would keep a transcript half of which
// ran somewhere else and read another project's notes, and the freeze that keeps
// the prompt cache warm cannot tell that apart from an ordinary turn (it is
// keyed on cwd + notes, so a session whose own directory already equals the
// project's would not even re-compose). Deciding at creation makes the four
// facts true for the whole life of the session.
func (s *Server) handleSetSessionGroup(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusConflict,
		"a session's project is decided when it is created — start a new session in the project instead")
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
		writeInvalidJSONBody(w, err)
		return
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Drop any existing entry first, then re-add at the end when pinning. This
	// keeps the list free of duplicates and gives a stable "unpin" path.
	out := make([]string, 0, len(gf.PinnedSessionIDs)+1)
	for _, id := range gf.PinnedSessionIDs {
		if id != sid {
			out = append(out, id)
		}
	}
	if req.Pinned {
		out = append(out, sid)
		// Pinning and collapsing are mutually exclusive; pinning wins over a
		// stale collapsed entry (e.g. set from another tab) instead of leaving
		// the session in both lists.
		gf.CollapsedSessionIDs = removeID(gf.CollapsedSessionIDs, sid)
	}
	gf.PinnedSessionIDs = out

	if err := saveRegistry(gf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pinned": req.Pinned})
}

// removeID returns ids without sid, preserving order.
func removeID(ids []string, sid string) []string {
	out := ids[:0]
	for _, id := range ids {
		if id != sid {
			out = append(out, id)
		}
	}
	return out
}

// ─── PUT /api/sessions/{id}/collapse ────────────────────────────────────────

type setSessionCollapsedRequest struct {
	Collapsed bool `json:"collapsed"`
}

// handleSetSessionCollapsed archives a session (hides it from the sidebar's
// Tasks/Projects sections, moves it into Settings' 数据管理), or restores it.
// Archiving keeps whatever project membership the session already has —
// unlike pin, which still can't coexist with it — so restoring puts a project's
// session back exactly where it was, with no second write needed. Idempotent
// like pin: collapsing an already-collapsed session keeps its position,
// restoring an absent one is a no-op.
func (s *Server) handleSetSessionCollapsed(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("id")
	if sid == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	var req setSessionCollapsedRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Collapsed {
		for _, id := range gf.PinnedSessionIDs {
			if id == sid {
				writeError(w, http.StatusConflict, "session is pinned; unpin it before collapsing")
				return
			}
		}
	}

	out := removeID(gf.CollapsedSessionIDs, sid)
	if req.Collapsed {
		out = append(out, sid)
	}
	gf.CollapsedSessionIDs = out

	if err := saveRegistry(gf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "collapsed": req.Collapsed})
}
