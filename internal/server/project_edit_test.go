package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/scheduler"
)

// PATCH source_dirs replaces the mount set (validated like creation), and
// output_dir marks one of them; both feed the freeze identity so the next
// turn re-composes.
func TestProject_PatchSourceDirsAndOutputDir(t *testing.T) {
	srv := groupTestServer(t)
	first := t.TempDir()
	gid, _ := newProjectGroupWS(t, srv, "Work", first)

	second := t.TempDir()
	rec, out := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"source_dirs": []string{first, second},
		"output_dir":  second,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	dirs, _ := g["source_dirs"].([]any)
	if len(dirs) != 2 {
		t.Fatalf("source_dirs = %v, want 2 mounts", dirs)
	}
	if od, _ := g["output_dir"].(string); od != second {
		t.Errorf("output_dir = %q, want %q", od, second)
	}

	// output_dir must be one of the mounts.
	rec, _ = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"output_dir": t.TempDir(),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("output_dir outside the mounts: status %d, want 400", rec.Code)
	}

	// Clearing the marker is legitimate.
	rec, out = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"output_dir": "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear output_dir: status %d body %s", rec.Code, rec.Body.String())
	}
	if g, _ = out["group"].(map[string]any); g["output_dir"] != nil && g["output_dir"] != "" {
		t.Errorf("output_dir not cleared: %v", g["output_dir"])
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

	// Removing a mount that was the output folder drops the marker with it.
	rec, out = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"source_dirs": []string{second},
		"output_dir":  second,
	})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec, out = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{
		"source_dirs": []string{first},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("shrink mounts: status %d body %s", rec.Code, rec.Body.String())
	}
	if g, _ = out["group"].(map[string]any); g["output_dir"] != nil && g["output_dir"] != "" {
		t.Errorf("stale output_dir survived its mount's removal: %v", g["output_dir"])
	}
}

// A scheduled task with an explicit directory gets that directory as its
// output mount — the workspace is generated like every other project's, so
// cron and regular projects share one shape.
func TestCronProject_ExplicitDirectoryBecomesOutputMount(t *testing.T) {
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
	if len(g.SourceDirs) != 1 || g.SourceDirs[0] != dir || g.OutputDir != dir {
		t.Errorf("explicit directory not mounted as output: dirs=%v out=%q", g.SourceDirs, g.OutputDir)
	}

	// Without a directory: zero mounts, same generated workspace shape.
	g2, err := srv.createCronProject(scheduler.Task{ID: "cron-10", Name: "周报"})
	if err != nil {
		t.Fatalf("createCronProject: %v", err)
	}
	if len(g2.SourceDirs) != 0 || g2.OutputDir != "" || g2.WorkingDir == "" {
		t.Errorf("dirless cron project shape wrong: %+v", g2)
	}
	if g2.TaskID != "cron-10" {
		t.Errorf("task id not recorded: %+v", g2)
	}
}
