package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/scheduler"
)

// groupNames returns the registry's group names, in order.
func groupNames(t *testing.T) []string {
	t.Helper()
	groupMu.Lock()
	groups, err := loadSessionGroups()
	groupMu.Unlock()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	out := make([]string, 0, len(groups))
	for i := range groups {
		out = append(out, groups[i].Name)
	}
	return out
}

// groupByName returns the named group, or fails.
func groupByName(t *testing.T, name string) sessionGroup {
	t.Helper()
	groupMu.Lock()
	groups, err := loadSessionGroups()
	groupMu.Unlock()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	for i := range groups {
		if groups[i].Name == name {
			return groups[i]
		}
	}
	t.Fatalf("group %q not found in %v", name, groupNames(t))
	return sessionGroup{}
}

// The plain group is gone and its sessions are tasks again — filed nowhere,
// which is what a task is.
func TestDissolvePlainGroups_PlainGroupGoesAway(t *testing.T) {
	setTestHome(t)
	sess := saveSessionIn(t, "")
	g := sessionGroup{ID: "g-plain", Name: "Group 2", SessionIDs: []string{sess.ID}}
	writeGroups(t, g)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.dissolvePlainGroups()

	if names := groupNames(t); len(names) != 0 {
		t.Errorf("groups after dissolving: %v, want none", names)
	}
	if p := projectForSession(sess.ID); p != nil {
		t.Errorf("session ended up in %q", p.Name)
	}
}

// A project is untouched: it has a directory, which is the whole point.
func TestDissolvePlainGroups_ProjectSurvives(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	writeGroups(t, sessionGroup{ID: "g-proj", Name: "alpha", SessionIDs: []string{}, WorkingDir: dir})

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.dissolvePlainGroups()

	if got := groupByName(t, "alpha"); got.WorkingDir != dir {
		t.Errorf("project working dir = %q, want %q", got.WorkingDir, dir)
	}
}

// A cron task's run cluster survives even with no directory, and the TaskID it
// predates is backfilled from the scheduler. Without the backfill the cluster
// looks exactly like a plain group and a scheduled task's whole run history
// would be dissolved.
func TestDissolvePlainGroups_CronClusterSurvivesAndIsBackfilled(t *testing.T) {
	setTestHome(t)
	run := saveSessionIn(t, "")
	writeGroups(t, sessionGroup{ID: "g-cron", Name: "daily report", SessionIDs: []string{run.ID}})

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	// A task whose group was created before groups recorded which task they
	// belonged to.
	srv.initScheduler()
	if srv.scheduler == nil {
		t.Skip("scheduler unavailable")
	}
	task := scheduler.Task{Name: "daily report", Cron: "0 0 9 * * *", Prompt: "report", Enabled: true}
	if err := srv.scheduler.Add(&task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := srv.scheduler.SetSessionGroup(task.ID, "g-cron"); err != nil {
		t.Fatalf("set session group: %v", err)
	}

	srv.dissolvePlainGroups()

	got := groupByName(t, "daily report")
	if got.TaskID != task.ID {
		t.Errorf("task id = %q, want %q backfilled from the scheduler", got.TaskID, task.ID)
	}
	if len(got.SessionIDs) != 1 || got.SessionIDs[0] != run.ID {
		t.Errorf("run history lost: %v", got.SessionIDs)
	}
}

// A cron cluster already carrying its TaskID needs no scheduler to survive —
// the field alone is enough, which is what makes the pass safe to run when the
// scheduler failed to start.
func TestDissolvePlainGroups_MarkedCronClusterSurvivesWithoutScheduler(t *testing.T) {
	setTestHome(t)
	writeGroups(t, sessionGroup{ID: "g-cron", Name: "nightly", SessionIDs: []string{}, TaskID: "task-9"})

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.dissolvePlainGroups()

	if got := groupByName(t, "nightly"); got.TaskID != "task-9" {
		t.Errorf("cron cluster lost its task id: %+v", got)
	}
}

// Running twice must be safe — it runs on every start.
func TestDissolvePlainGroups_Idempotent(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	writeGroups(t,
		sessionGroup{ID: "g-plain", Name: "Group 1", SessionIDs: []string{}},
		sessionGroup{ID: "g-proj", Name: "alpha", SessionIDs: []string{}, WorkingDir: dir},
	)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.dissolvePlainGroups()
	first := groupNames(t)
	srv.dissolvePlainGroups()
	second := groupNames(t)

	if len(first) != 1 || first[0] != "alpha" {
		t.Fatalf("after one pass: %v, want [alpha]", first)
	}
	if len(second) != len(first) || second[0] != first[0] {
		t.Errorf("second pass changed the registry: %v then %v", first, second)
	}
}

// The order the two reconciliation passes run in is load-bearing: dissolving
// releases a session that was organised into a plain group, and the adoption
// pass is what then files it under a project for the directory it was actually
// working in. Reversed, the session is still in the group when adoption looks
// at it and never gets its project.
func TestReconcileRegistry_DissolvedSessionIsThenAdopted(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	sess := saveSessionIn(t, dir)
	writeGroups(t, sessionGroup{ID: "g-plain", Name: "Group 1", SessionIDs: []string{sess.ID}})

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.reconcileRegistry()

	p := projectForSession(sess.ID)
	if p == nil {
		t.Fatal("session was released but never adopted into a project")
	}
	if p.WorkingDir != dir {
		t.Errorf("project dir = %q, want %q", p.WorkingDir, dir)
	}
	if p.Name == "Group 1" {
		t.Error("the plain group was renamed into a project instead of being dissolved")
	}
}

// writeGroups seeds the registry file directly, standing in for one written by
// an older version.
func writeGroups(t *testing.T, groups ...sessionGroup) {
	t.Helper()
	path, err := sessionGroupsPath()
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	if err := saveSessionGroups(groups); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
}
