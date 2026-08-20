package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/scheduler"
)

func TestSeedSessionDirectory_SetsWorkingDir(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("m", "")
	if err := seedSessionDirectory(sess, dir); err != nil {
		t.Fatalf("seedSessionDirectory: %v", err)
	}
	if sess.WorkingDir != dir {
		t.Errorf("sess.WorkingDir = %q, want %q", sess.WorkingDir, dir)
	}
}

func TestSeedSessionDirectory_InvalidDirErrors(t *testing.T) {
	sess := agent.NewSession("m", "")

	// Missing directory.
	missing := filepath.Join(t.TempDir(), "nope")
	if err := seedSessionDirectory(sess, missing); err == nil {
		t.Error("expected an error for a missing directory")
	}

	// A file, not a directory.
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := seedSessionDirectory(sess, f); err == nil {
		t.Error("expected an error when the path is a file, not a directory")
	}

	// A failed seed must not have mutated WorkingDir.
	if sess.WorkingDir != "" {
		t.Errorf("WorkingDir mutated on error: %q", sess.WorkingDir)
	}
}

// task.Directory only seeds a NEW session's WorkingDir, once, at creation
// (see CreateSession's doc comment) — this pins that behavior end to end.
func TestCreateSession_SeedsWorkingDirFromTaskDirectory(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	dir := t.TempDir()
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: dir})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.WorkingDir != dir {
		t.Errorf("sess.WorkingDir = %q, want %q", sess.WorkingDir, dir)
	}
}

// A cron tick has nobody present to answer an ask prompt, so a freshly
// created task session must not inherit the web/CLI/IM interactive default —
// write_file/edit_file no longer blanket-allow $CWD, and interactive's
// implicit ask would time out to deny on every write.
func TestCreateSession_DefaultsToAutoPermissionModeWhenUnconfigured(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.PermissionMode != "auto" {
		t.Errorf("PermissionMode = %q, want %q", sess.PermissionMode, "auto")
	}
}

// An operator who explicitly configured a global permission_mode is
// respected as-is for new task sessions too — only the unconfigured case
// defaults differently from a web/CLI/IM session.
func TestCreateSession_HonorsExplicitGlobalPermissionMode(t *testing.T) {
	setTestHome(t)
	if err := (config.Config{PermissionMode: "strict"}).Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.PermissionMode != "strict" {
		t.Errorf("PermissionMode = %q, want %q", sess.PermissionMode, "strict")
	}
}

// No Directory set on the task → the session is created with no WorkingDir
// of its own, falling back to the server default like any other session.
func TestCreateSession_NoDirectoryLeavesWorkingDirEmpty(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.WorkingDir != "" {
		t.Errorf("sess.WorkingDir = %q, want empty", sess.WorkingDir)
	}
}

// An invalid task.Directory must fail session creation outright rather than
// silently falling back to the server default — the same standard
// applyTaskDirectory used to hold before this was moved to creation time.
func TestCreateSession_InvalidDirectoryErrors(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	missing := filepath.Join(t.TempDir(), "nope")
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: missing})
	if err == nil {
		t.Fatal("expected an error for a missing task directory")
	}
	// The failed seed means sess.Save() was never called — sess.ID names a
	// session that exists only in memory. Returning it anyway would let
	// scheduler.fire() (which persists task.SessionID unconditionally,
	// without checking RunTask's error) permanently dangle the task on a
	// session ID agent.LoadSession can never load.
	if sessionID != "" {
		t.Errorf("sessionID = %q, want empty on error (must not return an unsaved session's ID)", sessionID)
	}
}

// Every run creates a brand-new session — even when a SessionID from a prior
// run is set on the task, CreateSession never reuses it. The previous run's
// session is left on disk; each run starts from a clean transcript.
func TestCreateSession_AlwaysCreatesNewSession(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	dir := t.TempDir()
	firstID, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: dir})
	if err != nil {
		t.Fatalf("CreateSession (first): %v", err)
	}

	// Run again with the prior SessionID set — it must be ignored.
	secondID, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: dir, SessionID: firstID})
	if err != nil {
		t.Fatalf("CreateSession (second): %v", err)
	}
	if secondID == firstID {
		t.Fatalf("CreateSession reused the existing session %q; every run must be a NEW session", firstID)
	}
}

// Each run's session is titled by the run's local date and time, so a task's
// runs are distinguishable within its group.
func TestCreateSession_TitlesSessionByDate(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "daily report"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	// Title is time.Now().Format("2006-01-02 15:04") — check the shape rather
	// than an exact instant.
	if _, perr := time.Parse("2006-01-02 15:04", sess.Title); perr != nil {
		t.Errorf("sess.Title = %q, want a %q date-time (parse err: %v)", sess.Title, "2006-01-02 15:04", perr)
	}
}

// A run files its session under the task's existing session group.
func TestCreateSession_FilesSessionUnderTaskGroup(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	g, err := createSessionGroupNamed("daily report", "", "task-1")
	if err != nil {
		t.Fatalf("createSessionGroupNamed: %v", err)
	}
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "daily report", SessionGroupID: g.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("loadSessionGroups: %v", err)
	}
	found := false
	for _, grp := range groups {
		if grp.ID != g.ID {
			continue
		}
		for _, sid := range grp.SessionIDs {
			if sid == sessionID {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("session %q not filed under group %q; groups=%+v", sessionID, g.ID, groups)
	}
}

// A task with no group yet (predating grouping) gets one created lazily on its
// first run, and the run's session is filed under it.
func TestCreateSession_LazilyCreatesGroupForOldTask(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "legacy task"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("loadSessionGroups: %v", err)
	}
	found := false
	for _, grp := range groups {
		if grp.Name != "legacy task" {
			continue
		}
		for _, sid := range grp.SessionIDs {
			if sid == sessionID {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no lazily-created group holds session %q; groups=%+v", sessionID, groups)
	}
}

func TestCreateSession_PersistsAgentID(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t", AgentID: "code-review"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.AgentID != "code-review" {
		t.Errorf("sess.AgentID = %q, want %q", sess.AgentID, "code-review")
	}
	if got := sess.EffectiveAgentID(); got != "code-review" {
		t.Errorf("EffectiveAgentID() = %q, want %q", got, "code-review")
	}
}

func TestCreateSession_EmptyAgentID_DefaultsToDefault(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.AgentID != "" {
		t.Errorf("sess.AgentID = %q, want empty", sess.AgentID)
	}
	if got := sess.EffectiveAgentID(); got != "default" {
		t.Errorf("EffectiveAgentID() = %q, want %q", got, "default")
	}
}

// TestTaskToResponse_TimestampsAreUTC guards the timestamp layout: the API
// stamps used to be formatted with a bare trailing "Z", which Go treats as a
// literal rather than the UTC designator, so a task created at 19:25 +08:00
// was published as "19:25Z" and every client rendered it 8 hours late.
func TestTaskToResponse_TimestampsAreUTC(t *testing.T) {
	shanghai := time.FixedZone("CST", 8*60*60)
	srv := &Server{}
	got := srv.taskToResponse(scheduler.Task{
		ID:        "task_1",
		Name:      "n",
		CreatedAt: time.Date(2026, 8, 19, 19, 25, 59, 0, shanghai),
		LastRun:   time.Date(2026, 8, 19, 19, 42, 35, 0, shanghai),
	})
	if want := "2026-08-19T11:25:59Z"; got.CreatedAt != want {
		t.Errorf("created_at = %q, want %q", got.CreatedAt, want)
	}
	if want := "2026-08-19T11:42:35Z"; got.LastRun != want {
		t.Errorf("last_run = %q, want %q", got.LastRun, want)
	}
}

// TestTaskToResponse_NextRunIsTheSchedulerInstant: next_run comes from the live
// cron entry (a local-zone time), so it must survive serialisation as the same
// instant rather than as local wall-clock relabelled UTC.
func TestTaskToResponse_NextRunIsTheSchedulerInstant(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initScheduler()

	task := scheduler.Task{Name: "next-run", Cron: "0 */5 * * * *", Prompt: "p", Enabled: true}
	if err := srv.scheduler.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}

	want := srv.scheduler.NextRun(task.ID)
	if want.IsZero() {
		t.Fatal("scheduler reported no next run for an enabled task")
	}
	got, err := time.Parse(time.RFC3339, srv.taskToResponse(task).NextRun)
	if err != nil {
		t.Fatalf("parse next_run: %v", err)
	}
	if !got.Equal(want.Truncate(time.Second)) {
		t.Errorf("next_run = %v, want the scheduler instant %v", got, want.Truncate(time.Second))
	}
}
