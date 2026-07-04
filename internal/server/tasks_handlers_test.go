package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
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
	if _, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: missing}); err == nil {
		t.Error("expected an error for a missing task directory")
	}
}

// Once a session exists for a task, re-running CreateSession (as every
// subsequent cron fire does) must reuse it untouched — task.Directory plays
// no further role, even if it was edited via PATCH /api/tasks/{id} in the
// meantime. This is the behavior change from the old "apply task.Directory
// fresh on every run" design: editing a task's directory only affects the
// NEXT session created for it, never one that's already running.
func TestCreateSession_ReusesExistingSession_LaterDirectoryEditIgnored(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	firstDir := t.TempDir()
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: firstDir})
	if err != nil {
		t.Fatalf("CreateSession (first): %v", err)
	}

	// Simulate the task being edited (PATCH /api/tasks/{id}) to point at a
	// different directory, then firing again with the existing SessionID —
	// exactly what every real cron trigger after the first does.
	secondDir := t.TempDir()
	sessionID2, err := srv.CreateSession(scheduler.Task{Name: "t", Directory: secondDir, SessionID: sessionID})
	if err != nil {
		t.Fatalf("CreateSession (reuse): %v", err)
	}
	if sessionID2 != sessionID {
		t.Fatalf("expected the existing session to be reused, got a new id %q", sessionID2)
	}

	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.WorkingDir != firstDir {
		t.Errorf("sess.WorkingDir = %q, want unchanged %q (task.Directory edits shouldn't touch an existing session)", sess.WorkingDir, firstDir)
	}
}
