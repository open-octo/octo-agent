package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// newProjectGroup creates a project mounting dir as a source folder ("" for a
// zero-folder project) and returns its id.
func newProjectGroup(t *testing.T, srv *Server, name, dir string) string {
	t.Helper()
	id, _ := newProjectGroupWS(t, srv, name, dir)
	return id
}

// newProjectGroupWS is newProjectGroup returning the generated workspace too,
// for tests that assert where the project's sessions run.
func newProjectGroupWS(t *testing.T, srv *Server, name, dir string) (id, workspace string) {
	t.Helper()
	body := map[string]any{"name": name}
	if dir != "" {
		body["source_dirs"] = []string{dir}
	}
	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create project: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	if g == nil {
		t.Fatalf("create project: no group in response %s", rec.Body.String())
	}
	workspace, _ = g["working_dir"].(string)
	if workspace == "" || workspace == dir {
		t.Fatalf("create project: working_dir = %q, want a generated workspace", workspace)
	}
	id, _ = g["id"].(string)
	return id, workspace
}

// saveSessionWithDir persists a session carrying its own working dir.
func saveSessionWithDir(t *testing.T, own string) *agent.Session {
	t.Helper()
	sess := agent.NewSession("test-model", "")
	sess.WorkingDir = own
	if err := sess.Save(); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return sess
}

// TestProject_DirPrecedence covers the resolution order under the workspace
// model: a directory the session chose itself wins over its project's
// workspace, which wins over the configured workspace root. A session whose
// own value is the seeded default workspace never chose anything, so it does
// not outrank its project.
//
// The last resort is the workspace and NOT the server's launch directory: where
// `octo serve` was started from is nobody's choice, and letting it decide meant
// a session with no directory of its own ran its tools somewhere that changed
// with how the server was invoked.
func TestProject_DirPrecedence(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	ownDir := t.TempDir()

	cliBorn := saveSessionWithDir(t, ownDir)
	seeded := saveSessionWithDir(t, srv.curWorkspaceDir())
	inProject := saveSessionWithDir(t, "")
	standalone := saveSessionWithDir(t, ownDir)
	bare := saveSessionWithDir(t, "")

	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)
	fileInProject(t, gid, cliBorn.ID)
	fileInProject(t, gid, seeded.ID)
	fileInProject(t, gid, inProject.ID)

	if got := srv.sessionCwd(cliBorn); got != ownDir {
		t.Errorf("session with its own dir in a project: cwd = %q, want its own %q", got, ownDir)
	}
	if got := srv.sessionCwd(seeded); got != ws {
		t.Errorf("seeded-default session in a project: cwd = %q, want the workspace %q (the seed is nobody's choice)", got, ws)
	}
	if got := srv.sessionCwd(inProject); got != ws {
		t.Errorf("dirless session in a project: cwd = %q, want the workspace %q", got, ws)
	}
	if got := srv.sessionCwd(standalone); got != ownDir {
		t.Errorf("session outside project: cwd = %q, want own dir %q", got, ownDir)
	}
	if want := srv.curWorkspaceDir(); want == "" {
		t.Fatal("test server resolved no workspace dir; the fallback under test cannot be observed")
	} else if got := srv.sessionCwd(bare); got != want {
		t.Errorf("session with no dir: cwd = %q, want the workspace %q (not the launch dir %q)",
			got, want, srv.curCwd())
	}
}

// TestProject_WorkspaceFallbackCreatesTheDirectory: a session that falls back to
// the workspace may be the first thing to need it, and tools cannot run in a
// directory that does not exist. Created on the turn path rather than at
// startup, so this checks the turn path (sessionCwdEnv) and not just resolution.
func TestProject_WorkspaceFallbackCreatesTheDirectory(t *testing.T) {
	srv := groupTestServer(t)
	workspace := srv.curWorkspaceDir()
	if workspace == "" {
		t.Skip("test server resolved no workspace dir")
	}
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("clear the workspace: %v", err)
	}

	bare := saveSessionWithDir(t, "")
	dir, envCtx := srv.sessionCwdEnv(bare)
	if dir != workspace {
		t.Fatalf("turn cwd = %q, want the workspace %q", dir, workspace)
	}
	if info, err := os.Stat(workspace); err != nil {
		t.Errorf("workspace was not created for the turn: %v", err)
	} else if !info.IsDir() {
		t.Errorf("%s exists but is not a directory", workspace)
	}
	if !strings.Contains(envCtx, workspace) {
		t.Errorf("env context does not name the directory the turn runs in:\n%s", envCtx)
	}
}

// TestProject_ResolutionAgreesAcrossSurfaces pins the invariant that the
// directory shown in the UI is the one turns actually run in. These used to be
// three separate lookups; a project that only one of them knew about would
// display one path and execute in another.
func TestProject_ResolutionAgreesAcrossSurfaces(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	sess := saveSessionWithDir(t, "")

	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)
	fileInProject(t, gid, sess.ID)

	byID := srv.sessionCwdByID(sess.ID)
	bySession := srv.sessionCwd(sess)
	turnDir, envCtx := srv.sessionCwdEnv(sess)
	if byID != ws || bySession != ws || turnDir != ws {
		t.Fatalf("cwd disagreement: byID=%q bySession=%q turn=%q, want the workspace %q", byID, bySession, turnDir, ws)
	}
	// The env context is what the model reads; it must name the same dir.
	if !strings.Contains(envCtx, ws) {
		t.Errorf("env context does not mention the workspace %q:\n%s", ws, envCtx)
	}
}

// A seeded-default own dir is masked by the project rather than overwritten on
// disk. Deleting the project is what reveals the difference, and it is also
// the only way a session leaves one: membership is fixed at creation, so there
// is no move-out to test. (A directory the session genuinely chose is never
// masked at all — see TestProject_DirPrecedence.)
func TestProject_ShadowsOwnDirAndRestoresOnDelete(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	seededDir := srv.curWorkspaceDir()
	sess := saveSessionWithDir(t, seededDir)

	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)
	fileInProject(t, gid, sess.ID)
	if got := srv.sessionCwd(sess); got != ws {
		t.Fatalf("in project: cwd = %q, want the workspace %q", got, ws)
	}

	if rec, _ := doGroupReq(t, srv, http.MethodDelete, "/api/session-groups/"+gid, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete project: status %d", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != seededDir {
		t.Errorf("after the project was deleted: cwd = %q, want the seeded dir %q back", got, seededDir)
	}

	// Reloading from disk must show the own dir was never clobbered.
	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.WorkingDir != seededDir {
		t.Errorf("persisted WorkingDir = %q, want %q untouched", reloaded.WorkingDir, seededDir)
	}
}

// Deleting a project with ?sessions=delete takes its sessions with it: they were
// work on that directory, and leaving them as unattached tasks is a mess nobody
// asked for. One request, so it cannot half-happen.
func TestProject_DeleteWithSessions(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	keep := saveSessionWithDir(t, "")
	doomed := saveSessionWithDir(t, "")

	gid := newProjectGroup(t, srv, "Work", projectDir)
	fileInProject(t, gid, doomed.ID)

	rec, _ := doGroupReq(t, srv, http.MethodDelete, "/api/session-groups/"+gid+"?sessions=delete", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := agent.LoadSession(doomed.ID); err == nil {
		t.Error("the project's session survived")
	}
	if _, err := agent.LoadSession(keep.ID); err != nil {
		t.Errorf("a session outside the project was deleted: %v", err)
	}
	groupMu.Lock()
	groups, _ := loadSessionGroups()
	groupMu.Unlock()
	for i := range groups {
		if groups[i].ID == gid {
			t.Error("the project itself survived")
		}
	}
}

// Without the flag the sessions stay and become tasks — the old behaviour, kept
// so a client that does not ask cannot destroy transcripts.
func TestProject_DeleteKeepsSessionsByDefault(t *testing.T) {
	srv := groupTestServer(t)
	sess := saveSessionWithDir(t, "")
	gid := newProjectGroup(t, srv, "Work", t.TempDir())
	fileInProject(t, gid, sess.ID)

	if rec, _ := doGroupReq(t, srv, http.MethodDelete, "/api/session-groups/"+gid, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d", rec.Code)
	}
	if _, err := agent.LoadSession(sess.ID); err != nil {
		t.Errorf("session was deleted without being asked for: %v", err)
	}
	if p := projectForSession(sess.ID); p != nil {
		t.Errorf("session is still in a project: %+v", p)
	}
}

// A project's directory cannot be cleared. Clearing it used to demote the
// project to a plain group, and there is no such thing to demote to — the
// request has to be refused rather than quietly producing a row that is neither
// a task nor a project.
func TestProject_DirCannotBeCleared(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	sess := saveSessionWithDir(t, "")

	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)
	fileInProject(t, gid, sess.ID)
	rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"working_dir": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("clearing the dir: status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	// And the project still governs its session.
	if got := srv.sessionCwd(sess); got != ws {
		t.Errorf("cwd = %q, want the project workspace %q", got, ws)
	}
}

// TestProject_RetargetDirIsPickedUp verifies the read cache does not serve a
// stale directory after a rejected edit — and that the rejection really left
// the workspace alone.
func TestProject_RetargetIsRejected(t *testing.T) {
	srv := groupTestServer(t)
	first := t.TempDir()
	second := t.TempDir()
	sess := saveSessionWithDir(t, "")

	gid, ws := newProjectGroupWS(t, srv, "Work", first)
	fileInProject(t, gid, sess.ID)
	if got := srv.sessionCwd(sess); got != ws {
		t.Fatalf("before: cwd = %q, want %q", got, ws)
	}

	if rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"working_dir": second}); rec.Code != http.StatusBadRequest {
		t.Fatalf("retarget: status %d, want 400 (workspace is fixed at creation)", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != ws {
		t.Errorf("after rejected retarget: cwd = %q, want the workspace %q unchanged", got, ws)
	}
}

// TestProject_RejectsBadDir keeps the project directory held to the same
// validation as the per-session one.
func TestProject_RejectsBadDir(t *testing.T) {
	srv := groupTestServer(t)
	missing := filepath.Join(t.TempDir(), "nope")

	rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "Work", "working_dir": missing})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with missing dir: status %d, want 400", rec.Code)
	}

	// A file, not a directory.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, _ = doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "Work", "working_dir": f})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with file as dir: status %d, want 400", rec.Code)
	}
}

// TestProject_BlocksPerSessionDirOverride: a session in a project must not
// accept a per-session dir that the resolver would ignore anyway.
func TestProject_BlocksPerSessionDirOverride(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	sess := saveSessionWithDir(t, "")

	gid := newProjectGroup(t, srv, "Work", projectDir)
	fileInProject(t, gid, sess.ID)

	rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/sessions/"+sess.ID+"/working_dir", map[string]any{"working_dir": t.TempDir()})
	if rec.Code != http.StatusConflict {
		t.Errorf("per-session dir inside a project: status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// TestProject_LegacyRegistryLoadsAsPlainGroup: an existing session-groups.json
// written before projects existed must keep behaving as plain groups.
func TestProject_LegacyRegistryLoadsAsPlainGroup(t *testing.T) {
	srv := groupTestServer(t)
	ownDir := t.TempDir()
	sess := saveSessionWithDir(t, ownDir)

	path, err := sessionGroupsPath()
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"groups":[{"id":"g-old","name":"Old","session_ids":["` + sess.ID + `"]}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if p := projectForSession(sess.ID); p != nil {
		t.Errorf("legacy group resolved as a project: %+v", p)
	}
	if got := srv.sessionCwd(sess); got != ownDir {
		t.Errorf("legacy group member: cwd = %q, want own dir %q", got, ownDir)
	}
}

// TestProject_NewSessionNotSeededWithDefaultDir: a session created inside a
// project must not get the default workspace dir stamped onto it, or moving it
// out later would strand a directory the user never chose.
func TestProject_NewSessionNotSeededWithDefaultDir(t *testing.T) {
	srv := groupTestServer(t)
	workspace := t.TempDir()
	srv.setWorkspaceDir(workspace)
	projectDir := t.TempDir()

	inProject := saveSessionWithDir(t, "")
	outside := saveSessionWithDir(t, "")
	gid := newProjectGroup(t, srv, "Work", projectDir)
	fileInProject(t, gid, inProject.ID)

	srv.applyDefaultWorkspaceDir(inProject)
	srv.applyDefaultWorkspaceDir(outside)

	if inProject.WorkingDir != "" {
		t.Errorf("session in project was seeded with %q, want no own dir", inProject.WorkingDir)
	}
	if want := filepath.Join(workspace, "tasks", outside.ID); outside.WorkingDir != want {
		t.Errorf("session outside project: WorkingDir = %q, want its task workspace %q", outside.WorkingDir, want)
	}
}

// TestProject_ExportedDirLookup covers the CLI/TUI entry point: it must report
// a project's directory and nothing else — notably not the session's own
// working dir, which is seeded on every web session and would otherwise pull
// `octo -c` out of the directory the user is standing in.
func TestProject_ExportedDirLookup(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	ownDir := t.TempDir()

	inProject := saveSessionWithDir(t, ownDir)
	outside := saveSessionWithDir(t, ownDir)

	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)
	fileInProject(t, gid, inProject.ID)

	if got := ProjectDirForSession(inProject.ID); got != ws {
		t.Errorf("session in project: %q, want the workspace %q", got, ws)
	}
	if got := ProjectDirForSession(outside.ID); got != "" {
		t.Errorf("session outside a project must report no dir, got %q (its own dir must not leak)", got)
	}
	if got := ProjectDirForSession(""); got != "" {
		t.Errorf("empty session id: %q, want \"\"", got)
	}
	if got := ProjectDirForSession("does-not-exist"); got != "" {
		t.Errorf("unknown session id: %q, want \"\"", got)
	}
}

// createSessionInGroupViaAPI POSTs /api/sessions with a group_id and returns
// the new session's id.
// fileInProject puts an existing session in a project the way the server does
// it internally at creation time. Quicker than the HTTP move endpoint (which
// also insists the session exists on disk), so tests that need a session
// already in a project reach for this rather than a request.
func fileInProject(t *testing.T, groupID, sessionID string) {
	t.Helper()
	if err := addSessionToGroup(groupID, sessionID); err != nil {
		t.Fatalf("file %s into %s: %v", sessionID, groupID, err)
	}
}

func createSessionInGroupViaAPI(t *testing.T, srv *Server, groupID string) string {
	t.Helper()
	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/sessions", map[string]any{"group_id": groupID})
	if rec.Code != http.StatusOK {
		t.Fatalf("create session in group: status %d body %s", rec.Code, rec.Body.String())
	}
	sess, _ := out["session"].(map[string]any)
	if sess == nil {
		t.Fatalf("create session: no session in response %s", rec.Body.String())
	}
	id, _ := sess["id"].(string)
	if id == "" {
		t.Fatalf("create session: empty id in %s", rec.Body.String())
	}
	return id
}

// TestProject_CreateSessionDirectlyInProject covers the sidebar's per-group
// "+" button: the session is filed under the project at creation, gets NO
// seeded default workspace dir (membership lands before
// applyDefaultWorkspaceDir), and resolves to the project directory. This is
// the flow that makes the seed guard reachable end-to-end.
func TestProject_CreateSessionDirectlyInProject(t *testing.T) {
	srv := groupTestServer(t)
	srv.setWorkspaceDir(t.TempDir()) // a default that WOULD be seeded without the guard
	projectDir := t.TempDir()
	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)

	sid := createSessionInGroupViaAPI(t, srv, gid)

	if p := projectForSession(sid); p == nil || p.ID != gid {
		t.Fatalf("session not filed under the project: %+v", p)
	}
	loaded, err := agent.LoadSession(sid)
	if err != nil {
		t.Fatalf("load created session: %v", err)
	}
	if loaded.WorkingDir != "" {
		t.Errorf("session born in a project was seeded with %q, want no own dir", loaded.WorkingDir)
	}
	if got := srv.sessionCwd(loaded); got != ws {
		t.Errorf("cwd = %q, want the project workspace %q", got, ws)
	}
}

// A group with no directory — the one kind left, a cron task's run cluster —
// files the session but seeding proceeds as usual. Only a project suppresses it,
// because only a project has a directory to override it with.
func TestProject_CreateSessionInDirlessGroup(t *testing.T) {
	srv := groupTestServer(t)
	workspace := t.TempDir()
	srv.setWorkspaceDir(workspace)
	g, err := createSessionGroupNamed("nightly", "", "task-1")
	if err != nil {
		t.Fatalf("create cron cluster: %v", err)
	}
	gid := g.ID

	sid := createSessionInGroupViaAPI(t, srv, gid)

	loaded, err := agent.LoadSession(sid)
	if err != nil {
		t.Fatalf("load created session: %v", err)
	}
	if want := filepath.Join(workspace, "tasks", sid); loaded.WorkingDir != want {
		t.Errorf("session in a dirless group: WorkingDir = %q, want its task workspace %q", loaded.WorkingDir, want)
	}
}

// TestProject_CreateSessionInUnknownGroup rejects a bad group id up front
// rather than creating an orphan session.
func TestProject_CreateSessionInUnknownGroup(t *testing.T) {
	srv := groupTestServer(t)
	rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/sessions", map[string]any{"group_id": "g-nope"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown group: status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestProject_RegistryWritesNotifyTabs: every registry write must fire the
// groups-changed notification — it is what keeps other tabs' sidebar headers
// and composer chips from lying about where a project's tools run. Hooked at
// saveRegistry, so one test can sweep every write path.
func TestProject_RegistryWritesNotifyTabs(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	sess := saveSessionWithDir(t, "")

	notified := 0
	prev := notifyGroupsChanged
	notifyGroupsChanged = func() { notified++ }
	t.Cleanup(func() { notifyGroupsChanged = prev })

	steps := []struct {
		name string
		do   func() *httptest.ResponseRecorder
	}{
		{"create project", func() *httptest.ResponseRecorder {
			rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "Work", "working_dir": projectDir})
			return rec
		}},
		{"create session in project", func() *httptest.ResponseRecorder {
			gid := projectIDByName(t, srv, "Work")
			rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/sessions", map[string]any{"group_id": gid})
			return rec
		}},
		{"collapse project", func() *httptest.ResponseRecorder {
			gid := projectIDByName(t, srv, "Work")
			rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"collapsed": true})
			return rec
		}},
		{"pin session", func() *httptest.ResponseRecorder {
			rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/pin", map[string]any{"pinned": true})
			return rec
		}},
		{"delete group", func() *httptest.ResponseRecorder {
			gid := projectIDByName(t, srv, "Work")
			rec, _ := doGroupReq(t, srv, http.MethodDelete, "/api/session-groups/"+gid, nil)
			return rec
		}},
	}
	for _, step := range steps {
		before := notified
		if rec := step.do(); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", step.name, rec.Code, rec.Body.String())
		}
		if notified <= before {
			t.Errorf("%s: registry write did not notify", step.name)
		}
	}
}

// projectIDByName finds a group id by name via the list endpoint.
func projectIDByName(t *testing.T, srv *Server, name string) string {
	t.Helper()
	rec, _ := doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list groups: status %d", rec.Code)
	}
	var resp struct {
		Groups []sessionGroup `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, g := range resp.Groups {
		if g.Name == name {
			return g.ID
		}
	}
	t.Fatalf("group %q not found", name)
	return ""
}

// TestProject_ListReportsProjectFields makes sure the Web UI can tell the two
// kinds of group apart from the list alone, without a second request: a project
// by its working_dir, a cron cluster by its task_id.
func TestProject_ListReportsProjectFields(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	newProjectGroup(t, srv, "Work", projectDir)
	if _, err := createSessionGroupNamed("nightly", "", "task-1"); err != nil {
		t.Fatalf("create cron cluster: %v", err)
	}

	rec, _ := doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d", rec.Code)
	}
	var resp struct {
		Groups []sessionGroup `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(resp.Groups))
	}
	if !resp.Groups[0].isProject() || len(resp.Groups[0].SourceDirs) != 1 || resp.Groups[0].SourceDirs[0] != projectDir {
		t.Errorf("first group should be a project mounting %q, got %+v", projectDir, resp.Groups[0])
	}
	if resp.Groups[1].isProject() || !resp.Groups[1].isCronCluster() {
		t.Errorf("second group should be a cron cluster with no dir, got %+v", resp.Groups[1])
	}
}

// TestProject_BranchStaysInProject covers what Branch has to carry across for a
// session inside a project. A project session deliberately holds no WorkingDir
// of its own (applyDefaultWorkspaceDir skips it — the project's dir governs),
// so a fork that isn't filed under the same project resolves its dir from the
// server default instead and quietly runs somewhere else, with the global
// memory tier instead of the project's.
func TestProject_BranchStaysInProject(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()

	src := agent.NewSession("test-model", "")
	src.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "hello"},
		{Role: agent.RoleAssistant, Content: "hi"},
	}
	if err := src.Save(); err != nil {
		t.Fatalf("save source: %v", err)
	}
	gid, ws := newProjectGroupWS(t, srv, "Work", projectDir)
	fileInProject(t, gid, src.ID)

	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/sessions/"+src.ID+"/branch",
		map[string]any{"message_index": 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("branch: status %d body %s", rec.Code, rec.Body.String())
	}
	s, _ := out["session"].(map[string]any)
	branchID, _ := s["id"].(string)
	if branchID == "" {
		t.Fatalf("branch: no session id in %s", rec.Body.String())
	}

	if p := projectForSession(branchID); p == nil || p.ID != gid {
		t.Errorf("branch filed under %+v, want project %s", p, gid)
	}
	branch, err := agent.LoadSession(branchID)
	if err != nil {
		t.Fatalf("load branch: %v", err)
	}
	if got := srv.resolveSessionDir(branch.ID, branch.WorkingDir); got != ws {
		t.Errorf("branch working dir = %q, want the project workspace %q", got, ws)
	}
}
