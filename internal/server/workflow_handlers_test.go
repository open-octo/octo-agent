package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeUserWorkflow drops a saved workflow .rb file under home/.octo/workflows,
// mirroring how WorkflowSaveTool would persist one at "user" scope.
func writeUserWorkflow(t *testing.T, home, name, content string) {
	t.Helper()
	dir := filepath.Join(home, ".octo", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".rb"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandleGetWorkflow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeUserWorkflow(t, tmp, "my-flow", "# @description A test workflow\n\"ok\"\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	req := httptest.NewRequest(http.MethodGet, "/api/workflows/my-flow", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "my-flow" || body["source"] != "user" || body["description"] != "A test workflow" {
		t.Errorf("body = %+v", body)
	}
	if body["script"] == "" || body["script"] == nil {
		t.Errorf("script missing from body: %+v", body)
	}
}

func TestHandleGetWorkflow_NotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/workflows/nonexistent", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleDeleteWorkflow_RefusesBuiltin(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodDelete, "/api/workflows/batch-migrate", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteWorkflow_NotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodDelete, "/api/workflows/nonexistent", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteWorkflow_RemovesUserFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeUserWorkflow(t, tmp, "scratch", "# @description Throwaway\n\"ok\"\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	req := httptest.NewRequest(http.MethodDelete, "/api/workflows/scratch", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	// Gone from a subsequent GET.
	req = httptest.NewRequest(http.MethodGet, "/api/workflows/scratch", nil)
	w = httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status after delete = %d, want 404", w.Code)
	}

	// Trashed, not hard-deleted.
	if _, err := os.Stat(filepath.Join(tmp, ".octo", "workflows", "scratch.rb")); !os.IsNotExist(err) {
		t.Errorf("file still present on disk: err = %v", err)
	}
}

func TestHandleExportWorkflow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeUserWorkflow(t, tmp, "my-flow", "# @description A test workflow\n\"ok\"\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/workflows/my-flow/export", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="my-flow.rb"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if body := w.Body.String(); body != "# @description A test workflow\n\"ok\"\n" {
		t.Errorf("body = %q", body)
	}
}

func TestHandleExportWorkflow_NotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/workflows/nonexistent/export", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
