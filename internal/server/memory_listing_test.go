package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
)

// The memory panel must list per-project slug directories written by sessions
// (sessionMemDir), not just the server-default pair — labeled by slug so
// GET/DELETE can address them via the `source` query param.
func TestHandleGetMemories_ListsPerProjectSlugDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	root, err := memory.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	slug := "someproj-0a1b2c3d"
	if err := os.MkdirAll(filepath.Join(root, slug), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, slug, "MEMORY.md"), []byte("# proj"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/memories", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Files []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range body.Files {
		if f.Source == slug && f.Name == "MEMORY.md" {
			found = true
		}
		if f.Source != "project" && f.Source != "inherited" && f.Source != slug {
			t.Errorf("unexpected source label %q", f.Source)
		}
	}
	if !found {
		t.Fatalf("slug dir %q not listed: %+v", slug, body.Files)
	}
}

// A slug source must resolve under the memories root, and a crafted source
// must not escape it or address directories that don't exist.
func TestHandleGetMemory_SlugSourceAndTraversal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	root, err := memory.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	slug := "otherproj-11223344"
	if err := os.MkdirAll(filepath.Join(root, slug), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, slug, "notes.md"), []byte("slug body"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A same-named file outside the root that a traversal would reach.
	if err := os.WriteFile(filepath.Join(tmp, "notes.md"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	get := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/memories/notes.md?source="+source, nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		return w
	}

	if w := get(slug); w.Code != http.StatusOK {
		t.Fatalf("slug source: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	} else {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Content != "slug body" {
			t.Fatalf("content = %q, want slug body", body.Content)
		}
	}

	// Base("../..") == ".." — resolveMemoryPath must reject it outright, or
	// the join would land on the root's PARENT (~/.octo) and read files there.
	for _, evil := range []string{"..%2F..", "..", "."} {
		if w := get(evil); w.Code != http.StatusNotFound {
			t.Fatalf("traversal source %q: status = %d, want 404", evil, w.Code)
		}
	}
	if w := get("no-such-slug"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown slug: status = %d, want 404", w.Code)
	}
}

// Forgetting a memory unlinks the file — the user asked for it and the panel
// warned that it can't be undone, so nothing is staged in the trash.
func TestHandleDeleteMemory_RemovesFilePermanently(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	root, err := memory.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	slug := "someproj-99887766"
	if err := os.MkdirAll(filepath.Join(root, slug), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, slug, "notes.md")
	if err := os.WriteFile(path, []byte("stale note"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/memories/notes.md?source="+slug, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("memory file still on disk after delete: err = %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(tmp, ".octo", "trash")); err == nil && len(entries) > 0 {
		t.Errorf("deleted memory was staged in the trash: %d entr(ies)", len(entries))
	}
}
