package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// newProjectGroup creates a group carrying dir and returns its id.
func newProjectGroup(t *testing.T, srv *Server, name, dir string) string {
	t.Helper()
	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{
		"name":        name,
		"working_dir": dir,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create project: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	if g == nil {
		t.Fatalf("create project: no group in response %s", rec.Body.String())
	}
	if got, _ := g["working_dir"].(string); got != dir {
		t.Fatalf("create project: working_dir = %q, want %q", got, dir)
	}
	id, _ := g["id"].(string)
	return id
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

// TestProject_DirPrecedence covers the three-way resolution: a project wins
// over the session's own dir, which wins over the server default.
func TestProject_DirPrecedence(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	ownDir := t.TempDir()

	inProject := saveSessionWithDir(t, ownDir)
	standalone := saveSessionWithDir(t, ownDir)
	bare := saveSessionWithDir(t, "")

	gid := newProjectGroup(t, srv, "Work", projectDir)
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+inProject.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move into project: status %d", rec.Code)
	}

	if got := srv.sessionCwd(inProject); got != projectDir {
		t.Errorf("session in project: cwd = %q, want project dir %q", got, projectDir)
	}
	if got := srv.sessionCwd(standalone); got != ownDir {
		t.Errorf("session outside project: cwd = %q, want own dir %q", got, ownDir)
	}
	if got := srv.sessionCwd(bare); got != srv.curCwd() {
		t.Errorf("session with no dir: cwd = %q, want server default %q", got, srv.curCwd())
	}
}

// TestProject_ResolutionAgreesAcrossSurfaces pins the invariant that the
// directory shown in the UI is the one turns actually run in. These used to be
// three separate lookups; a project that only one of them knew about would
// display one path and execute in another.
func TestProject_ResolutionAgreesAcrossSurfaces(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	sess := saveSessionWithDir(t, t.TempDir())

	gid := newProjectGroup(t, srv, "Work", projectDir)
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move into project: status %d", rec.Code)
	}

	byID := srv.sessionCwdByID(sess.ID)
	bySession := srv.sessionCwd(sess)
	turnDir, envCtx := srv.sessionCwdEnv(sess)
	if byID != projectDir || bySession != projectDir || turnDir != projectDir {
		t.Fatalf("cwd disagreement: byID=%q bySession=%q turn=%q, want %q", byID, bySession, turnDir, projectDir)
	}
	// The env context is what the model reads; it must name the same dir.
	if !strings.Contains(envCtx, projectDir) {
		t.Errorf("env context does not mention project dir %q:\n%s", projectDir, envCtx)
	}
}

// TestProject_MoveOutRestoresOwnDir verifies the project shadows the session's
// own working dir rather than overwriting it on disk.
func TestProject_MoveOutRestoresOwnDir(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	ownDir := t.TempDir()
	sess := saveSessionWithDir(t, ownDir)

	gid := newProjectGroup(t, srv, "Work", projectDir)
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move in: status %d", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != projectDir {
		t.Fatalf("in project: cwd = %q, want %q", got, projectDir)
	}

	// Out of every group again.
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/group", map[string]any{"group_id": ""}); rec.Code != http.StatusOK {
		t.Fatalf("move out: status %d", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != ownDir {
		t.Errorf("after leaving project: cwd = %q, want own dir %q restored", got, ownDir)
	}

	// Reloading from disk must show the own dir was never clobbered.
	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.WorkingDir != ownDir {
		t.Errorf("persisted WorkingDir = %q, want %q untouched", reloaded.WorkingDir, ownDir)
	}
}

// TestProject_DemoteToPlainGroup covers clearing a project's directory.
func TestProject_DemoteToPlainGroup(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	ownDir := t.TempDir()
	sess := saveSessionWithDir(t, ownDir)

	gid := newProjectGroup(t, srv, "Work", projectDir)
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move in: status %d", rec.Code)
	}
	if rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"working_dir": ""}); rec.Code != http.StatusOK {
		t.Fatalf("demote: status %d", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != ownDir {
		t.Errorf("after demote: cwd = %q, want own dir %q", got, ownDir)
	}
}

// TestProject_RetargetDirIsPickedUp verifies the read cache does not serve a
// stale directory after the project is edited.
func TestProject_RetargetDirIsPickedUp(t *testing.T) {
	srv := groupTestServer(t)
	first := t.TempDir()
	second := t.TempDir()
	sess := saveSessionWithDir(t, "")

	gid := newProjectGroup(t, srv, "Work", first)
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move in: status %d", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != first {
		t.Fatalf("before retarget: cwd = %q, want %q", got, first)
	}

	if rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"working_dir": second}); rec.Code != http.StatusOK {
		t.Fatalf("retarget: status %d", rec.Code)
	}
	if got := srv.sessionCwd(sess); got != second {
		t.Errorf("after retarget: cwd = %q, want %q (stale cache?)", got, second)
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
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sess.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move in: status %d", rec.Code)
	}

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

// TestProject_NotesReachTheSystemPrompt checks the notes are rendered into the
// prompt's memory layer for a session in the project, and only for it.
func TestProject_NotesReachTheSystemPrompt(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	inProject := saveSessionWithDir(t, "")
	outside := saveSessionWithDir(t, "")

	gid := newProjectGroup(t, srv, "Work", projectDir)
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+inProject.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move in: status %d", rec.Code)
	}
	const notes = "Ship behind a flag; never touch the billing tables."
	if rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"notes": notes}); rec.Code != http.StatusOK {
		t.Fatalf("set notes: status %d", rec.Code)
	}

	if got := projectNotesFor(inProject.ID); got != notes {
		t.Errorf("notes for session in project = %q, want %q", got, notes)
	}
	if got := projectNotesFor(outside.ID); got != "" {
		t.Errorf("notes leaked to a session outside the project: %q", got)
	}
	if rendered := renderProjectNotes(notes); !strings.Contains(rendered, notes) {
		t.Errorf("rendered notes lost the text: %s", rendered)
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
	if rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+inProject.ID+"/group", map[string]any{"group_id": gid}); rec.Code != http.StatusOK {
		t.Fatalf("move in: status %d", rec.Code)
	}

	srv.applyDefaultWorkspaceDir(inProject)
	srv.applyDefaultWorkspaceDir(outside)

	if inProject.WorkingDir != "" {
		t.Errorf("session in project was seeded with %q, want no own dir", inProject.WorkingDir)
	}
	if outside.WorkingDir != workspace {
		t.Errorf("session outside project: WorkingDir = %q, want workspace default %q", outside.WorkingDir, workspace)
	}
}

// TestProject_ListReportsProjectFields makes sure the Web UI can tell a project
// from a plain group without a second request.
func TestProject_ListReportsProjectFields(t *testing.T) {
	srv := groupTestServer(t)
	projectDir := t.TempDir()
	newProjectGroup(t, srv, "Work", projectDir)
	if rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "Just a group"}); rec.Code != http.StatusOK {
		t.Fatalf("create plain group: status %d", rec.Code)
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
	if !resp.Groups[0].isProject() || resp.Groups[0].WorkingDir != projectDir {
		t.Errorf("first group should be a project at %q, got %+v", projectDir, resp.Groups[0])
	}
	if resp.Groups[1].isProject() {
		t.Errorf("second group should be plain, got %+v", resp.Groups[1])
	}
}
