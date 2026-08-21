package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// TestRunSessions_ScopedToCwd: the list has to show what `octo -c` will
// actually resume from here. Offering another directory's session, which -c
// then refuses, would be worse than not listing it.
func TestRunSessions_ScopedToCwd(t *testing.T) {
	scopeTestHome(t)
	here, elsewhere := t.TempDir(), t.TempDir()
	mine := sessionIn(t, here)
	theirs := sessionIn(t, elsewhere)
	legacy := sessionIn(t, "")
	t.Chdir(here)

	var out, errOut bytes.Buffer
	if code := runSessions(nil, &out, &errOut); code != 0 {
		t.Fatalf("runSessions = %d, stderr: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, mine.ShortID()) {
		t.Errorf("listing omits this directory's session %s:\n%s", mine.ShortID(), got)
	}
	for _, hidden := range []*agent.Session{theirs, legacy} {
		if strings.Contains(got, hidden.ShortID()) {
			t.Errorf("listing offers out-of-scope session %s:\n%s", hidden.ShortID(), got)
		}
	}
}

// TestRunSessions_AllShowsEverything: --all is the way to find a session whose
// directory you've forgotten, so it must cross both boundaries — another
// directory, and no directory at all — AND name the directory each one belongs
// to. Without that it lists ids that -c refuses here and gives no clue where
// they do work.
func TestRunSessions_AllShowsEverything(t *testing.T) {
	scopeTestHome(t)
	here, elsewhere := t.TempDir(), t.TempDir()
	mine := sessionIn(t, here)
	theirs := sessionIn(t, elsewhere)
	legacy := sessionIn(t, "")
	t.Chdir(here)

	var out, errOut bytes.Buffer
	if code := runSessions([]string{"--all"}, &out, &errOut); code != 0 {
		t.Fatalf("runSessions --all = %d, stderr: %s", code, errOut.String())
	}
	got := out.String()
	for _, want := range []*agent.Session{mine, theirs, legacy} {
		if !strings.Contains(got, want.ShortID()) {
			t.Errorf("--all omits %s:\n%s", want.ShortID(), got)
		}
	}
	for _, dir := range []string{here, elsewhere} {
		if !strings.Contains(got, dir) {
			t.Errorf("--all does not name the directory %s its sessions belong to:\n%s", dir, got)
		}
	}
	// The directoryless ones cannot be resumed from any directory, so this
	// listing is where the user learns where they can be opened instead.
	if !strings.Contains(got, "Web UI") {
		t.Errorf("--all does not say where directoryless sessions can be opened:\n%s", got)
	}
}

// TestRunSessions_AllIsNotCapped: --all is a search, not a recap. It is the
// only way to find a session whose directory you've forgotten and the only place
// directoryless sessions appear at all, so a "10 most recent machine-wide" cut
// would come back empty for both on any real history.
func TestRunSessions_AllIsNotCapped(t *testing.T) {
	scopeTestHome(t)
	here := t.TempDir()
	t.Chdir(here)

	// More than any per-listing cap, spread over several directories so no one
	// of them dominates the recency order.
	var want []*agent.Session
	for i := 0; i < 25; i++ {
		want = append(want, sessionIn(t, t.TempDir()))
	}

	var out, errOut bytes.Buffer
	if code := runSessions([]string{"--all"}, &out, &errOut); code != 0 {
		t.Fatalf("runSessions --all = %d, stderr: %s", code, errOut.String())
	}
	got := out.String()
	missing := 0
	for _, s := range want {
		if !strings.Contains(got, s.ShortID()) {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("--all omitted %d of %d sessions; it must not be capped", missing, len(want))
	}
}

// TestRunSessions_EmptyScopePointsAtAll: an empty list in a fresh directory is
// the normal case now, so it has to say how to see the rest.
func TestRunSessions_EmptyScopePointsAtAll(t *testing.T) {
	scopeTestHome(t)
	sessionIn(t, t.TempDir())
	t.Chdir(t.TempDir())

	var out, errOut bytes.Buffer
	if code := runSessions(nil, &out, &errOut); code != 0 {
		t.Fatalf("runSessions = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "--all") {
		t.Errorf("empty listing does not mention --all:\n%s", out.String())
	}
}

func TestRunSessions_RejectsUnknownArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("runSessions --nope = %d, want 2", code)
	}
}

func TestSessionSelectItems(t *testing.T) {
	s := agent.NewSession("test-model", "")
	s.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "fix the bug"},
		{Role: agent.RoleAssistant, Content: "done"},
	}

	items := sessionSelectItems([]*agent.Session{s})
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	it := items[0]
	if it.value != s.ID {
		t.Errorf("value = %q, want the full session ID %q", it.value, s.ID)
	}
	if !strings.Contains(it.label, s.ShortID()) {
		t.Errorf("label should carry the short ID; got %q", it.label)
	}
	if !strings.Contains(it.desc, "test-model") {
		t.Errorf("desc should carry the model; got %q", it.desc)
	}
	if !strings.Contains(it.desc, "1 turn") {
		t.Errorf("desc should carry the turn count; got %q", it.desc)
	}
}
