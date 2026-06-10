package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillZip drops a zip containing a minimal skill into dir and returns
// the file's basename.
func writeSkillZip(t *testing.T, dir, base string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"SKILL.md":            "---\nname: zipped\ndescription: imported from zip\n---\nbody",
		"scripts/__init__.py": "",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, base), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

func postImport(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/import?access_key="+srv.AccessKey(),
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

func TestHandleImportSkill_UploadedZip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	base := writeSkillZip(t, filepath.Join(tmp, ".octo", "uploads"), "123_zipped.zip")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := postImport(t, srv, `{"source":"/api/uploads/`+base+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "zipped" {
		t.Fatalf("name = %v, want zipped", body["name"])
	}
	for _, f := range []string{"SKILL.md", "scripts/__init__.py"} {
		if _, err := os.Stat(filepath.Join(tmp, ".octo", "skills", "zipped", f)); err != nil {
			t.Errorf("expected %s installed: %v", f, err)
		}
	}
	// The manifest must be refreshed so new sessions see the skill.
	if !strings.Contains(srv.skillsManifest, "zipped") {
		t.Error("skillsManifest not refreshed after import")
	}

	// Same import again: 409 without force, 200 with force.
	if w := postImport(t, srv, `{"source":"/api/uploads/`+base+`"}`); w.Code != http.StatusConflict {
		t.Fatalf("re-import status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if w := postImport(t, srv, `{"source":"/api/uploads/`+base+`","force":true}`); w.Code != http.StatusOK {
		t.Fatalf("forced re-import status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleImportSkill_LocalDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	src := filepath.Join(tmp, "my-skill")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"),
		[]byte("---\nname: local-dir\ndescription: from dir\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	// json.Marshal, not string concatenation — Windows paths contain
	// backslashes that must be escaped to survive as JSON.
	body, err := json.Marshal(map[string]any{"source": src})
	if err != nil {
		t.Fatal(err)
	}
	w := postImport(t, srv, string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(tmp, ".octo", "skills", "local-dir", "SKILL.md")); err != nil {
		t.Errorf("skill not installed: %v", err)
	}
}

func TestHandleImportSkill_BadSources(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	cases := []string{
		`{"source":""}`,
		`{"source":"justonename"}`,
		`{"source":"/api/uploads/missing.zip"}`,
		`{"source":"/nonexistent/path"}`,
		`not json`,
	}
	for _, body := range cases {
		if w := postImport(t, srv, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400; resp=%s", body, w.Code, w.Body.String())
		}
	}
}
