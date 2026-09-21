package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// artifactDeliverCall records one deliverer invocation.
type artifactDeliverCall struct {
	session, artifactPath, filePath, note string
}

// installArtifactDeliverer stubs the server's half of delivery and returns a
// spy. The cleanup restores the nil state, the CLI's normal one.
func installArtifactDeliverer(t *testing.T) *[]artifactDeliverCall {
	t.Helper()
	calls := &[]artifactDeliverCall{}
	SetArtifactDeliverer(func(session, artifactPath, filePath, note string) error {
		*calls = append(*calls, artifactDeliverCall{session, artifactPath, filePath, note})
		return nil
	})
	t.Cleanup(func() { SetArtifactDeliverer(nil) })
	return calls
}

// TestInsertArtifact_NoDeliverer: a CLI session has no browser to deliver
// into — the tool says so instead of pretending.
func TestInsertArtifact_NoDeliverer(t *testing.T) {
	resetArtifactMirror(t)
	SetArtifactDeliverer(nil)
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runToolInSession(t, ArtifactInsertTool{}, "s1", map[string]any{"path": path})
	if !strings.Contains(out, "no") || !strings.Contains(strings.ToLower(out), "web ui") {
		t.Errorf("expected the unreachable answer, got: %s", out)
	}
}

// TestInsertArtifact_NothingOpen: no reporting artifact in this session means
// there is nowhere to deliver — ask, don't guess.
func TestInsertArtifact_NothingOpen(t *testing.T) {
	resetArtifactMirror(t)
	installArtifactDeliverer(t)
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runToolInSession(t, ArtifactInsertTool{}, "s1", map[string]any{"path": path})
	if !strings.Contains(out, "nowhere to put it") {
		t.Errorf("expected the ask-to-open guidance, got: %s", out)
	}
}

// TestInsertArtifact_SingleDefault: one reporting page needs no path argument.
func TestInsertArtifact_SingleDefault(t *testing.T) {
	resetArtifactMirror(t)
	calls := installArtifactDeliverer(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/board.html", Digest: "a board"})
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runToolInSession(t, ArtifactInsertTool{}, "s1", map[string]any{"path": path, "note": "a sketch"})
	if !strings.Contains(out, "/tmp/board.html") {
		t.Errorf("the answer should name the destination: %s", out)
	}
	if len(*calls) != 1 || (*calls)[0].artifactPath != "/tmp/board.html" || (*calls)[0].filePath != path || (*calls)[0].note != "a sketch" {
		t.Fatalf("deliverer got %+v", *calls)
	}
	if (*calls)[0].session != "s1" {
		t.Errorf("delivery must stay inside the caller's session, got %q", (*calls)[0].session)
	}
}

// TestInsertArtifact_AmbiguousWithoutPath: two reporting pages — the model has
// to say which one rather than the tool picking arbitrarily.
func TestInsertArtifact_AmbiguousWithoutPath(t *testing.T) {
	resetArtifactMirror(t)
	installArtifactDeliverer(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html"})
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/b.html"})
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runToolInSession(t, ArtifactInsertTool{}, "s1", map[string]any{"path": path})
	if !strings.Contains(out, "/tmp/a.html") || !strings.Contains(out, "/tmp/b.html") {
		t.Errorf("both candidates should be named: %s", out)
	}
}

// TestInsertArtifact_UnknownPath: a page this session has no snapshot for is a
// named refusal — and never a peek at another session's.
func TestInsertArtifact_UnknownPath(t *testing.T) {
	resetArtifactMirror(t)
	calls := installArtifactDeliverer(t)
	PutArtifact(ArtifactSnapshot{Session: "s2", Path: "/tmp/theirs.html"})
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runToolInSession(t, ArtifactInsertTool{}, "s1", map[string]any{"path": path, "artifact": "/tmp/theirs.html"})
	if !strings.Contains(out, "artifact_state") {
		t.Errorf("expected a named refusal pointing at artifact_state: %s", out)
	}
	if len(*calls) != 0 {
		t.Error("nothing may be delivered across sessions")
	}
}

// TestInsertArtifact_RequiresPath: the one required argument.
func TestInsertArtifact_RequiresPath(t *testing.T) {
	resetArtifactMirror(t)
	installArtifactDeliverer(t)
	_, err := ArtifactInsertTool{}.Execute(WithSessionID(context.Background(), "s1"), "insert_into_artifact", nil)
	if err == nil {
		t.Fatal("a missing path must be an error, not a silent no-op")
	}
}

// TestArtifactInsert_Registered: a tool the model cannot see does not exist.
func TestArtifactInsert_Registered(t *testing.T) {
	for _, tl := range allTools {
		if tl.Definition().Name == "insert_into_artifact" {
			return
		}
	}
	t.Error("insert_into_artifact is not in allTools")
}
