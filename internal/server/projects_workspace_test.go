package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Workspace-model creation (project = generated workspace + source folders) ──

// TestProject_CreateGeneratesWorkspace: creating a project no longer takes the
// user's directory as the project directory — the directory the sessions run
// in is generated under the workspace root, and the user's directory is
// mounted as a source folder.
func TestProject_CreateGeneratesWorkspace(t *testing.T) {
	srv := groupTestServer(t)
	src := t.TempDir()
	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{
		"name":        "订单重构",
		"source_dirs": []string{src},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	ws, _ := g["working_dir"].(string)
	if ws == "" || ws == src {
		t.Fatalf("working_dir = %q, want a generated workspace distinct from the source dir", ws)
	}
	root := srv.curWorkspaceDir()
	if root == "" || !strings.HasPrefix(ws, root+string(filepath.Separator)) {
		t.Errorf("workspace %q is not under the workspace root %q", ws, root)
	}
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		t.Errorf("workspace was not created on disk: %v", err)
	}
	dirs, _ := g["source_dirs"].([]any)
	if len(dirs) != 1 || dirs[0] != src {
		t.Errorf("source_dirs = %v, want [%q]", dirs, src)
	}
}

// A project with no source folders is a legitimate shape — writing projects
// and cron projects carry none.
func TestProject_CreateWithZeroFolders(t *testing.T) {
	srv := groupTestServer(t)
	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "写作"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	if ws, _ := g["working_dir"].(string); ws == "" {
		t.Error("zero-folder project got no workspace")
	}
}

// The legacy working_dir field used to BE the project directory; older callers
// sending it get its directory mounted as a source folder instead.
func TestProject_LegacyWorkingDirBecomesSourceFolder(t *testing.T) {
	srv := groupTestServer(t)
	dir := t.TempDir()
	rec, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{
		"name":        "Legacy",
		"working_dir": dir,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	if ws, _ := g["working_dir"].(string); ws == dir {
		t.Errorf("working_dir %q was taken verbatim; want a generated workspace", dir)
	}
	dirs, _ := g["source_dirs"].([]any)
	if len(dirs) != 1 || dirs[0] != dir {
		t.Errorf("source_dirs = %v, want the legacy dir mounted", dirs)
	}
}

// Mounting a directory under the workspace root would let one project's
// workspace become another's source folder and split the reverse lookup.
func TestProject_RejectsSourceDirUnderWorkspaceRoot(t *testing.T) {
	srv := groupTestServer(t)
	inside := filepath.Join(srv.curWorkspaceDir(), "sneaky")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{
		"name":        "套娃",
		"source_dirs": []string{inside},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("source dir under workspace root: expected 400, got %d", rec.Code)
	}
}

// The workspace root itself is just as much off limits as its children.
func TestProject_RejectsWorkspaceRootAsSourceDir(t *testing.T) {
	srv := groupTestServer(t)
	root := srv.curWorkspaceDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, _ := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{
		"name":        "根",
		"source_dirs": []string{root},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("workspace root as source dir: expected 400, got %d", rec.Code)
	}
}

// A session carrying the BUILT-IN default workspace (~/Octo) while the server
// is configured with a different one still reads as seeded: sessions written
// before a workspace_dir change never chose that value either.
func TestProject_BuiltinDefaultAlsoReadsAsSeeded(t *testing.T) {
	srv := groupTestServer(t)
	srv.setWorkspaceDir(t.TempDir()) // configured root differs from ~/Octo now
	builtin, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Skipf("cannot resolve the built-in workspace dir: %v", err)
	}
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := saveSessionWithDir(t, builtin)

	gid, ws := newProjectGroupWS(t, srv, "Work", t.TempDir())
	fileInProject(t, gid, sess.ID)

	if got := srv.sessionCwd(sess); got != ws {
		t.Errorf("cwd = %q, want the workspace %q (a stale built-in default is nobody's choice)", got, ws)
	}
}

// The workspace is fixed at creation; PATCH working_dir used to retarget the
// project and now has nothing legitimate to do.
func TestProject_WorkspaceIsImmutable(t *testing.T) {
	srv := groupTestServer(t)
	gid := newProjectGroup(t, srv, "Fixed", t.TempDir())
	rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"working_dir": t.TempDir(),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("retarget workspace: expected 400, got %d", rec.Code)
	}
}
