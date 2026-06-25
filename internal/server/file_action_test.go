package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleFileAction_Download(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.cwd = tmp

	w := doJSON(t, srv, http.MethodPost, "/api/file-action", `{"path": "note.txt", "action": "download"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "note.txt") {
		t.Errorf("Content-Disposition = %q", w.Header().Get("Content-Disposition"))
	}
	if w.Body.String() != "hello" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestHandleFileAction_RejectsPathEscape(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.cwd = tmp

	for _, path := range []string{"../secret.txt", "/etc/passwd", "sub/../../secret.txt"} {
		w := doJSON(t, srv, http.MethodPost, "/api/file-action", `{"path": "`+path+`", "action": "download"}`)
		if w.Code != http.StatusForbidden {
			t.Errorf("path %q: status = %d, want 403", path, w.Code)
		}
	}
}

func TestHandleFileAction_OpenOnlyOnLocalhost(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.cwd = tmp

	req := httptest.NewRequest(http.MethodPost, "/api/file-action", strings.NewReader(`{"path": "note.txt", "action": "open"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Access-Key", srv.AccessKey())
	req.Host = "evil.com"
	req.RemoteAddr = "1.2.3.4:56789"
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-localhost open: status = %d, want 403", w.Code)
	}
}
