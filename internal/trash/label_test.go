package trash

import (
	"os"
	"path/filepath"
	"testing"
)

// stageSession writes a session transcript under ~/.octo/sessions and moves it
// into the trash, returning its original path.
func stageSession(t *testing.T, name, jsonl string) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	sessions := filepath.Join(home, ".octo", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, name)
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(path, sessions); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLabel_SessionTitleFromMeta: a session's title lives in the first meta
// line and surfaces as the entry Label so the UI shows something legible
// instead of the raw id filename.
func TestLabel_SessionTitleFromMeta(t *testing.T) {
	isolateHome(t)
	orig := stageSession(t, "20260712-140705-abcd.jsonl",
		`{"type":"meta","id":"abcd","title":"Fix the login redirect bug"}
{"type":"message","role":"user"}
`)
	e := find(t, orig)
	if e.Label != "Fix the login redirect bug" {
		t.Errorf("Label = %q, want the meta title", e.Label)
	}
}

// TestLabel_TitleRecordOverridesMeta: an appended title record (meta title
// empty) is the authoritative title.
func TestLabel_TitleRecordOverridesMeta(t *testing.T) {
	isolateHome(t)
	orig := stageSession(t, "20260712-150000-ef01.jsonl",
		`{"type":"meta","id":"ef01","title":""}
{"type":"message","role":"user"}
{"type":"title","title":"Refactor the trash package"}
`)
	e := find(t, orig)
	if e.Label != "Refactor the trash package" {
		t.Errorf("Label = %q, want the appended title record", e.Label)
	}
}

// TestLabel_NonSessionEmpty: an ordinary file has no derived label; the UI
// falls back to the basename.
func TestLabel_NonSessionEmpty(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	e := find(t, f)
	if e.Label != "" {
		t.Errorf("Label = %q, want empty for a non-session file", e.Label)
	}
}
