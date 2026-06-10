package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
)

// newArtifactSession persists a session whose transcript wrote the given
// paths (one write_file tool_use per path) and returns its id.
func newArtifactSession(t *testing.T, paths ...string) string {
	t.Helper()
	sess := agent.NewSession("stub-model", "")
	for i, p := range paths {
		sess.Messages = append(sess.Messages, agent.Message{
			Role: agent.RoleAssistant,
			Blocks: []agent.ContentBlock{{
				Type:  "tool_use",
				ID:    "t" + string(rune('0'+i)),
				Name:  "write_file",
				Input: map[string]any{"path": p, "content": "x"},
			}},
		})
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func getArtifact(t *testing.T, srv *Server, sessionID, path string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"path": {path}}.Encode()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/artifacts?"+q, nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

func TestHandleGetArtifact(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	htmlPath := filepath.Join(artDir, "bundle.html")
	if err := os.WriteFile(htmlPath, []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Exists on disk but is NOT in any transcript — must stay unreachable.
	secretPath := filepath.Join(artDir, "secret.html")
	if err := os.WriteFile(secretPath, []byte("<h1>secret</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Written by the session but not a previewable type.
	goPath := filepath.Join(artDir, "main.go")
	if err := os.WriteFile(goPath, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	id := newArtifactSession(t, htmlPath, goPath)
	otherID := newArtifactSession(t /* wrote nothing */)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// Whitelisted write → 200 with explicit headers.
	w := getArtifact(t, srv, id, htmlPath)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), []byte("<h1>hi</h1>")) {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Error("missing CSP sandbox header")
	}

	// On-disk but unwritten by the session → 404.
	if w := getArtifact(t, srv, id, secretPath); w.Code != http.StatusNotFound {
		t.Errorf("unwritten path: status = %d, want 404", w.Code)
	}
	// Written by a different session → 404.
	if w := getArtifact(t, srv, otherID, htmlPath); w.Code != http.StatusNotFound {
		t.Errorf("other session: status = %d, want 404", w.Code)
	}
	// Written but not a previewable extension → 404.
	if w := getArtifact(t, srv, id, goPath); w.Code != http.StatusNotFound {
		t.Errorf("non-previewable ext: status = %d, want 404", w.Code)
	}
	// Unknown session → 404.
	if w := getArtifact(t, srv, "nope", htmlPath); w.Code != http.StatusNotFound {
		t.Errorf("unknown session: status = %d, want 404", w.Code)
	}
	// Missing path param → 400.
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id+"/artifacts", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing path: status = %d, want 400", rec.Code)
	}
}

func TestHandleGetArtifact_SizeCap(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	big := filepath.Join(t.TempDir(), "big.html")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), artifactMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	id := newArtifactSession(t, big)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	if w := getArtifact(t, srv, id, big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestHandleGetArtifact_EditCountsAsWrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	p := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(p, []byte("# doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("stub-model", "")
	sess.Messages = append(sess.Messages, agent.Message{
		Role: agent.RoleAssistant,
		Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t1", Name: "edit_file",
			Input: map[string]any{"path": p, "old_string": "a", "new_string": "b"},
		}},
	})
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := getArtifact(t, srv, sess.ID, p)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
}
