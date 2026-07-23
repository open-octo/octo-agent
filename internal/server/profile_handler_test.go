package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestHandleGetProfileSoul_LegacyUppercase: pre-0.19 onboarding wrote
// ~/.octo/SOUL.md; the profile API must keep serving it. Content-based
// assertions keep the test meaningful on case-sensitive filesystems and
// trivially consistent elsewhere.
func TestHandleGetProfileSoul_LegacyUppercase(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".octo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".octo", "SOUL.md"), []byte("legacy soul"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/soul", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "legacy soul" {
		t.Errorf("content = %q, want %q", body["content"], "legacy soul")
	}
}

func TestHandleGetProfileUser_CanonicalLowercase(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".octo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".octo", "user.md"), []byte("me"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/user", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "me" {
		t.Errorf("content = %q, want %q", body["content"], "me")
	}
	if filepath.Base(body["path"]) != "user.md" {
		t.Errorf("path = %q, want canonical user.md basename", body["path"])
	}
}

// TestHandleGetProfileSoul_Missing: no soul.md yet is the normal
// not-customized-yet state, not an error — the endpoint must return 200 with
// empty content so the UI renders its empty state instead of an error toast.
func TestHandleGetProfileSoul_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/soul", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "" {
		t.Errorf("content = %q, want empty", body["content"])
	}
}

// TestHandleGetProfileUser_Missing mirrors the soul.md case for user.md.
func TestHandleGetProfileUser_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/user", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "" {
		t.Errorf("content = %q, want empty", body["content"])
	}
}

// TestHandleGetProfileSoul_IOError: a genuine read error — as opposed to
// "no soul.md yet" — must still surface as a 500, not be swallowed into the
// same empty-content response as the missing-file case. A regular file
// sitting where ~/.octo should be a directory trips ENOTDIR, which
// os.IsNotExist does not recognize (#1237), giving a portable way to force
// a non-not-exist error without relying on platform-specific chmod semantics.
func TestHandleGetProfileSoul_IOError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := os.WriteFile(filepath.Join(tmp, ".octo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/soul", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}
