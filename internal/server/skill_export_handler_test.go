package server

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A skill symlinked in from a shared folder exports its real files, not an
// empty archive.
func TestHandleExportSkill_SymlinkedDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	shared := filepath.Join(tmp, ".agents", "skills", "linked")
	if err := os.MkdirAll(filepath.Join(shared, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"SKILL.md":       "---\nname: linked\ndescription: shared across agents\n---\nbody",
		"scripts/run.sh": "echo hi",
	} {
		if err := os.WriteFile(filepath.Join(shared, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userRoot := filepath.Join(tmp, ".octo", "skills")
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(userRoot, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/skills/linked/export?access_key="+srv.AccessKey(), nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	if got, want := strings.Join(names, ","), "linked/SKILL.md,linked/scripts/run.sh"; got != want {
		t.Errorf("archive entries = %s, want %s", got, want)
	}
}
