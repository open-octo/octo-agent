package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/server"
)

// scopeTestHome pins HOME so the sessions and the project registry these tests
// write are their own.
func scopeTestHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
}

// sessionIn saves a session recording dir as its working directory, the way a
// fresh TUI session does.
func sessionIn(t *testing.T, dir string) *agent.Session {
	t.Helper()
	sess := agent.NewSession("test-model", "")
	if dir != "" {
		if err := sess.SetWorkingDir(dir); err != nil {
			t.Fatalf("set working dir: %v", err)
		}
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return sess
}

// TestSessionsForDir_OnlyThisDirectory: the picker offers this directory's
// sessions and nothing else — that scoping is what lets the TUI honour a
// session's recorded directory at all.
func TestSessionsForDir_OnlyThisDirectory(t *testing.T) {
	scopeTestHome(t)
	here, elsewhere := t.TempDir(), t.TempDir()

	mine := sessionIn(t, here)
	theirs := sessionIn(t, elsewhere)

	got, err := sessionsForDir(here, 0)
	if err != nil {
		t.Fatalf("sessionsForDir: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("sessions for %s = %v, want just %s", here, ids(got), mine.ID)
	}
	if got[0].ID == theirs.ID {
		t.Error("a session from another directory was offered")
	}
}

// TestSessionsForDir_NoDirectoryBelongsNowhere: sessions written before the TUI
// recorded a directory (and web sessions seeded with the workspace default)
// belong to no directory and are not offered. They are still on disk, and still
// resumable on the web.
func TestSessionsForDir_NoDirectoryBelongsNowhere(t *testing.T) {
	scopeTestHome(t)
	dir := t.TempDir()
	sessionIn(t, "")

	got, err := sessionsForDir(dir, 0)
	if err != nil {
		t.Fatalf("sessionsForDir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("sessions = %v, want none", ids(got))
	}
}

// TestSessionsForDir_ProjectWinsOverOwnDir: a session filed under a project
// belongs to the project's directory, not to whatever it recorded for itself —
// the precedence the server resolves tool cwd with: a directory the session
// chose itself outranks its project's workspace, so a terminal session filed
// into a project keeps being offered in the directory it was started in.
func TestSessionsForDir_OwnDirOutranksProject(t *testing.T) {
	scopeTestHome(t)
	ownDir, projectDir := t.TempDir(), t.TempDir()

	sess := sessionIn(t, ownDir)
	if err := server.EnsureProjectForDir(projectDir, sess.ID); err != nil {
		t.Fatalf("EnsureProjectForDir: %v", err)
	}

	inOwn, err := sessionsForDir(ownDir, 0)
	if err != nil {
		t.Fatalf("sessionsForDir(own): %v", err)
	}
	if len(inOwn) != 1 || inOwn[0].ID != sess.ID {
		t.Errorf("sessions for the own dir = %v, want %s", ids(inOwn), sess.ID)
	}

	inProject, err := sessionsForDir(projectDir, 0)
	if err != nil {
		t.Fatalf("sessionsForDir(project): %v", err)
	}
	if len(inProject) != 0 {
		t.Errorf("sessions for the project's source dir = %v, want none (the session belongs where it was started)", ids(inProject))
	}
}

// TestSessionsForDir_CapAppliesAfterFiltering: the cap must be applied to this
// directory's sessions, not to the machine-wide list — otherwise the n most
// recent sessions anywhere could contain none of this directory's.
func TestSessionsForDir_CapAppliesAfterFiltering(t *testing.T) {
	scopeTestHome(t)
	here, elsewhere := t.TempDir(), t.TempDir()

	oldest := sessionIn(t, here)
	for i := 0; i < 5; i++ {
		sessionIn(t, elsewhere)
	}
	newest := sessionIn(t, here)
	// Order comes from file mtime, and same-instant writes are ordered
	// arbitrarily — space the two apart so "the newest one here" is a fact
	// rather than a coin flip.
	touch(t, oldest, -time.Hour)

	got, err := sessionsForDir(here, 1)
	if err != nil {
		t.Fatalf("sessionsForDir: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions = %v, want exactly 1", ids(got))
	}
	if got[0].ID != newest.ID {
		t.Errorf("capped list = %s, want the newest session here (%s)", got[0].ID, newest.ID)
	}
}

// TestResolveSessionInDir_Last: "last" means this directory's most recent
// session, not the machine's.
func TestResolveSessionInDir_Last(t *testing.T) {
	scopeTestHome(t)
	here, elsewhere := t.TempDir(), t.TempDir()

	mine := sessionIn(t, here)
	sessionIn(t, elsewhere) // newer, but somewhere else

	got, err := resolveSessionInDir("last", here)
	if err != nil {
		t.Fatalf("resolve last: %v", err)
	}
	if got != mine.ID {
		t.Errorf("last = %s, want %s", got, mine.ID)
	}
}

// TestResolveSessionInDir_FullAndShortID: both the pasted full id and the short
// form resolve, as long as the session belongs here.
func TestResolveSessionInDir_FullAndShortID(t *testing.T) {
	scopeTestHome(t)
	dir := t.TempDir()
	sess := sessionIn(t, dir)

	for _, input := range []string{sess.ID, sess.ShortID()} {
		got, err := resolveSessionInDir(input, dir)
		if err != nil {
			t.Fatalf("resolve %q: %v", input, err)
		}
		if got != sess.ID {
			t.Errorf("resolve %q = %s, want %s", input, got, sess.ID)
		}
	}
}

// TestResolveSessionInDir_ElsewhereNamesTheDirectory: refusing an id that
// belongs to another directory has to say which, or the user is told "no match"
// about a session they can see in `octo sessions --all`.
func TestResolveSessionInDir_ElsewhereNamesTheDirectory(t *testing.T) {
	scopeTestHome(t)
	here, elsewhere := t.TempDir(), t.TempDir()
	sess := sessionIn(t, elsewhere)

	_, err := resolveSessionInDir(sess.ID, here)
	if err == nil {
		t.Fatal("resolving another directory's session: want error, got nil")
	}
	if !strings.Contains(err.Error(), elsewhere) {
		t.Errorf("error %q does not name the owning directory %s", err, elsewhere)
	}
}

// TestResolveSessionInDir_DirectorylessIsRefusedEverywhere: a session with no
// directory of its own belongs to no directory, so no directory resumes it —
// not even by full id. Doing so would run half of one session's turns in
// whatever directory the user happened to be in, which is the drift this
// scoping removes. The error says where it CAN be opened, so it is not a dead
// end.
func TestResolveSessionInDir_DirectorylessIsRefusedEverywhere(t *testing.T) {
	scopeTestHome(t)
	sess := sessionIn(t, "")

	for _, input := range []string{sess.ID, sess.ShortID()} {
		_, err := resolveSessionInDir(input, t.TempDir())
		if err == nil {
			t.Fatalf("resolving a directoryless session by %q: want error, got nil", input)
		}
		if !strings.Contains(err.Error(), "Web UI") {
			t.Errorf("error for %q does not say where it can be opened: %v", input, err)
		}
	}
}

// TestResolveSessionInDir_DirectorylessNotOfferedByPickerOrLast: they are absent
// from the scoped surfaces too, not just refused by an explicit id — the picker
// and `last` only ever offer what belongs here.
func TestResolveSessionInDir_DirectorylessNotOfferedByPickerOrLast(t *testing.T) {
	scopeTestHome(t)
	here := t.TempDir()
	mine := sessionIn(t, here)
	sessionIn(t, "") // newer, but belongs nowhere
	t.Chdir(here)

	listed, err := sessionsForDir(here, 0)
	if err != nil {
		t.Fatalf("sessionsForDir: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != mine.ID {
		t.Errorf("picker offers %v, want only this directory's session %s", ids(listed), mine.ID)
	}
	got, err := resolveSessionInDir("last", here)
	if err != nil {
		t.Fatalf("resolve last: %v", err)
	}
	if got != mine.ID {
		t.Errorf("last = %s, want this directory's session %s", got, mine.ID)
	}
}

// touch shifts a session file's mtime by delta, which is what orders the
// listing.
func touch(t *testing.T, sess *agent.Session, delta time.Duration) {
	t.Helper()
	path, err := sess.SavePath()
	if err != nil {
		t.Fatalf("save path: %v", err)
	}
	when := time.Now().Add(delta)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func ids(sessions []*agent.Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}
