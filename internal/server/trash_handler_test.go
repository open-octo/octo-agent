package server

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
)

// stageConflict trashes a file, then recreates a different file at the original
// path, so a restore has to deal with an occupied destination. Returns the
// entry id and the original path.
func stageConflict(t *testing.T) (string, string) {
	t.Helper()
	project := t.TempDir()
	orig := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(orig, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trash.Move(orig, project, trash.Options{DeletedBy: "session", Kind: "delete"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orig, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, _ := trash.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 trashed entry, got %d", len(entries))
	}
	return entries[0].ID, orig
}

// TestRestoreTrash_ConflictAbortThenBackup: the default restore 409s on an
// occupied path; on_conflict=backup resolves it losslessly.
func TestRestoreTrash_ConflictAbortThenBackup(t *testing.T) {
	// Fresh HOME so the shared test-binary trash (main_test pins HOME) can't
	// leak entries between trash tests.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id, orig := stageConflict(t)

	if w := doJSON(t, srv, "POST", "/api/trash/"+id+"/restore", ""); w.Code != 409 {
		t.Fatalf("expected 409 on conflict, got %d (body %s)", w.Code, w.Body.String())
	}
	// The current file is untouched by the aborted restore.
	if b, _ := os.ReadFile(orig); string(b) != "new" {
		t.Fatalf("aborted restore must not touch the current file, got %q", b)
	}

	w := doJSON(t, srv, "POST", "/api/trash/"+id+"/restore?on_conflict=backup", "")
	if w.Code != 200 {
		t.Fatalf("on_conflict=backup should succeed, got %d (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		BackedUpExisting bool `json:"backed_up_existing"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.BackedUpExisting {
		t.Error("expected backed_up_existing=true")
	}
	if b, _ := os.ReadFile(orig); string(b) != "old" {
		t.Fatalf("restore should have put the old version back, got %q", b)
	}
}

// TestRestoreTrash_SpecialCharFilename: an entry whose id ends in a filename
// with URL-significant characters restores when the client percent-encodes the
// id (as the web api.ts does) — the server route round-trips it.
func TestRestoreTrash_SpecialCharFilename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	project := t.TempDir()
	orig := filepath.Join(project, "weird #name%.txt")
	if err := os.WriteFile(orig, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trash.Move(orig, project); err != nil {
		t.Fatal(err)
	}
	entries, _ := trash.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	id := entries[0].ID

	w := doJSON(t, srv, "POST", "/api/trash/"+url.PathEscape(id)+"/restore", "")
	if w.Code != 200 {
		t.Fatalf("special-char id restore failed: %d (body %s)", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(orig); string(b) != "payload" {
		t.Fatalf("file not restored, got %q", b)
	}
}
