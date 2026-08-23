package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/scheduler"
)

// PATCH source_dirs replaces the mount set (validated like creation); it
// feeds the freeze identity so the next turn re-composes.
func TestProject_PatchSourceDirs(t *testing.T) {
	srv := groupTestServer(t)
	first := t.TempDir()
	gid, _ := newProjectGroupWS(t, srv, "Work", first)

	second := t.TempDir()
	rec, out := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"source_dirs": []string{first, second},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	dirs, _ := g["source_dirs"].([]any)
	if len(dirs) != 2 {
		t.Fatalf("source_dirs = %v, want 2 mounts", dirs)
	}
	// A mount under the workspace root is rejected on edit exactly as on
	// creation.
	inside := filepath.Join(srv.curWorkspaceDir(), "sneaky-edit")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, _ = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"source_dirs": []string{inside},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mount under workspace root: status %d, want 400", rec.Code)
	}
}

// A scheduled task with an explicit directory gets that directory as a source
// mount — the workspace is generated like every other project's, so cron and
// regular projects share one shape.
func TestCronProject_ExplicitDirectoryBecomesSourceMount(t *testing.T) {
	srv := groupTestServer(t)
	dir := t.TempDir()

	g, err := srv.createCronProject(scheduler.Task{ID: "cron-9", Name: "日报", Directory: dir})
	if err != nil {
		t.Fatalf("createCronProject: %v", err)
	}
	root := srv.curWorkspaceDir()
	if g.WorkingDir == dir || !strings.HasPrefix(g.WorkingDir, root+string(filepath.Separator)) {
		t.Errorf("cron workspace = %q, want generated under %q, not the explicit dir", g.WorkingDir, root)
	}
	if len(g.SourceDirs) != 1 || g.SourceDirs[0] != dir {
		t.Errorf("explicit directory not mounted as a source folder: dirs=%v", g.SourceDirs)
	}

	// Without a directory: zero mounts, same generated workspace shape.
	g2, err := srv.createCronProject(scheduler.Task{ID: "cron-10", Name: "周报"})
	if err != nil {
		t.Fatalf("createCronProject: %v", err)
	}
	if len(g2.SourceDirs) != 0 || g2.WorkingDir == "" {
		t.Errorf("dirless cron project shape wrong: %+v", g2)
	}
	if g2.TaskID != "cron-10" {
		t.Errorf("task id not recorded: %+v", g2)
	}
}

// Mounting a folder is the hooks trust grant: the create and edit gestures
// record each mount's hooks.yml at its current fingerprint, and a later edit
// of that file drops it out of the trusted set until re-granted. The
// cwd-equal mount never double-loads.
func TestMountedHooksTrust(t *testing.T) {
	srv := groupTestServer(t)
	writeHooks := func(dir, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".octo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".octo", "hooks.yml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	src := t.TempDir()
	writeHooks(src, "hooks: []\n")
	if hooksTrustedAt(src) {
		t.Fatal("an unmounted folder's hooks are trusted before any gesture")
	}

	rec, resp := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{
		"name":        "Work",
		"source_dirs": []string{src},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := resp["group"].(map[string]any)
	gid, _ := g["id"].(string)
	if !hooksTrustedAt(src) {
		t.Error("the create gesture did not record the mount's trust grant")
	}

	proj := &sessionGroup{ID: gid, WorkingDir: t.TempDir(), SourceDirs: []string{src}}
	if got := sourceHookDirs(proj.WorkingDir, proj); len(got) != 1 || got[0] != src {
		t.Errorf("sourceHookDirs = %v, want [%s]", got, src)
	}
	// A migrated session running IN the old project dir (= a mount) must not
	// load that mount's rules or hooks a second time.
	if got := sourceRuleDirs(src, proj); len(got) != 0 {
		t.Errorf("sourceRuleDirs with cwd == mount = %v, want none", got)
	}
	if got := sourceHookDirs(src, proj); len(got) != 0 {
		t.Errorf("sourceHookDirs with cwd == mount = %v, want none", got)
	}

	// Content changing after the grant must be re-granted, same as everywhere.
	writeHooks(src, "hooks: [changed]\n")
	if hooksTrustedAt(src) {
		t.Error("a changed hooks.yml stayed trusted")
	}
	if got := sourceHookDirs(proj.WorkingDir, proj); len(got) != 0 {
		t.Errorf("sourceHookDirs after content change = %v, want none", got)
	}

	// The edit gesture re-grants at the new fingerprint.
	rec, _ = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"source_dirs": []string{src},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d body %s", rec.Code, rec.Body.String())
	}
	if !hooksTrustedAt(src) {
		t.Error("the edit gesture did not re-grant the changed hooks.yml")
	}
}
