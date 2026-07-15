package server

import (
	"os"
	"path/filepath"
	"testing"

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

// session_mode "fresh" creates a new session every time, even when one
// already exists — it overrides the default "shared" reuse behavior.
func TestCreateSession_FreshMode_IgnoresExistingSession(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	dir := t.TempDir()
	firstID, err := srv.CreateSession(scheduler.Task{
		Name:        "t",
		Directory:   dir,
		SessionMode: "fresh",
	})
	if err != nil {
		t.Fatalf("CreateSession (first): %v", err)
	}

	// Run again with the SAME SessionID set — "fresh" must ignore it.
	secondID, err := srv.CreateSession(scheduler.Task{
		Name:        "t",
		Directory:   dir,
		SessionID:   firstID,
		SessionMode: "fresh",
	})
	if err != nil {
		t.Fatalf("CreateSession (second): %v", err)
	}
	if secondID == firstID {
		t.Fatalf("fresh mode reused the existing session %q; want a NEW session", firstID)
	}
}

// session_mode "" (empty) preserves legacy shared behavior — reuse the
// existing session if one is set on the task.
func TestCreateSession_DefaultMode_ReusesExistingSession(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	firstID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession (first): %v", err)
	}

	secondID, err := srv.CreateSession(scheduler.Task{Name: "t", SessionID: firstID})
	if err != nil {
		t.Fatalf("CreateSession (reuse): %v", err)
	}
	if secondID != firstID {
		t.Fatalf("default mode created a new session %q instead of reusing %q", secondID, firstID)
	}
}

// Switching session_mode between runs takes effect on the very next run —
// not whatever mode the task started with.
func TestCreateSession_ModeSwitchBetweenRuns(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// First run: shared mode, creates session A.
	sharedID, err := srv.CreateSession(scheduler.Task{Name: "t", SessionMode: "shared"})
	if err != nil {
		t.Fatalf("CreateSession (shared): %v", err)
	}

	// Shared run with SessionID set reuses A.
	reuseID, err := srv.CreateSession(scheduler.Task{Name: "t", SessionID: sharedID, SessionMode: "shared"})
	if err != nil {
		t.Fatalf("CreateSession (shared reuse): %v", err)
	}
	if reuseID != sharedID {
		t.Fatalf("shared mode did not reuse: got %q, want %q", reuseID, sharedID)
	}

	// Switch to fresh — must create a NEW session, ignoring sharedID.
	freshID, err := srv.CreateSession(scheduler.Task{Name: "t", SessionID: sharedID, SessionMode: "fresh"})
	if err != nil {
		t.Fatalf("CreateSession (fresh after shared): %v", err)
	}
	if freshID == sharedID {
		t.Fatalf("fresh mode reused the shared session %q; want a NEW session", sharedID)
	}

	// Switch back to shared without a SessionID — new shared session created.
	newSharedID, err := srv.CreateSession(scheduler.Task{Name: "t", SessionMode: "shared"})
	if err != nil {
		t.Fatalf("CreateSession (shared after fresh): %v", err)
	}
	if newSharedID == freshID || newSharedID == sharedID {
		t.Fatalf("shared mode after fresh should create yet another session; got %q", newSharedID)
	}
}
