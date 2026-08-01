package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// curatedTestServer returns a server with an isolated ~/.octo/agents dir and
// one hand-placed curated (SourceDefault) expert in ~/.octo/agents-default —
// mustServer never calls agentprofile.MaterializeDefaults, so the curated
// content is planted directly rather than relying on the embedded set.
func curatedTestServer(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	_ = os.MkdirAll(filepath.Join(tmp, ".octo", "agents"), 0o755)
	defaultDir := filepath.Join(tmp, ".octo", "agents-default")
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ndescription: curated test expert\ntools: [read_file]\ncategory: content-creation\ntags: [writing]\nexample_prompts: [\"hi\"]\nicon: ant-design:edit-outlined\nname_en: Test Expert\n---\npersona body\n"
	if err := os.WriteFile(filepath.Join(defaultDir, "test-expert.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	return mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
}

func TestHandleAgents_ListIncludesCuratedDefaultWithSource(t *testing.T) {
	srv := curatedTestServer(t)

	list := doAgents(t, srv)
	if len(list) != 1 {
		t.Fatalf("expected 1 curated agent, got %d: %+v", len(list), list)
	}
	got := list[0]
	if got.ID != "test-expert" || got.Source != "default" {
		t.Fatalf("unexpected profile: %+v", got)
	}
	if got.Category != "content-creation" || len(got.Tags) != 1 || got.Tags[0] != "writing" {
		t.Fatalf("gallery metadata not surfaced: %+v", got)
	}
	if len(got.ExamplePrompts) != 1 || got.Icon != "ant-design:edit-outlined" || got.NameEN != "Test Expert" {
		t.Fatalf("gallery metadata not surfaced: %+v", got)
	}
}

func TestHandleAgents_ToggleHidesAndReshowsCuratedDefault(t *testing.T) {
	srv := curatedTestServer(t)

	// Hide it.
	w := doJSON(t, srv, http.MethodPatch, "/api/agents/test-expert/toggle", "")
	if w.Code != http.StatusOK {
		t.Fatalf("toggle = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["enabled"] != false {
		t.Fatalf("first toggle should disable: %+v", resp)
	}
	// The hidden expert must still be LISTED (with enabled: false) — GET
	// /api/agents uses Store.All(), not List(), specifically so the gallery
	// UI has a way to find and re-show it. It just must not be resolvable
	// via Get() (checked separately below).
	list := doAgents(t, srv)
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("expected 1 agent listed with enabled=false after hiding, got %+v", list)
	}
	// GET /api/agents/:id (single-item) goes through Store.Get(), which still
	// treats a hidden default as not-found — only the list endpoint (Store.All())
	// exposes it, since that's the only place the gallery UI needs to see it.
	if w := doJSON(t, srv, http.MethodGet, "/api/agents/test-expert", ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/agents/test-expert on a hidden default = %d, want 404: %s", w.Code, w.Body.String())
	}

	// Re-show it.
	w = doJSON(t, srv, http.MethodPatch, "/api/agents/test-expert/toggle", "")
	if w.Code != http.StatusOK {
		t.Fatalf("toggle = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["enabled"] != true {
		t.Fatalf("second toggle should re-enable: %+v", resp)
	}
	list = doAgents(t, srv)
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("expected 1 agent listed with enabled=true after re-showing, got %+v", list)
	}
}

func TestHandleAgents_ToggleRejectsUserAgent(t *testing.T) {
	srv := agentsTestServer(t)

	w := doJSON(t, srv, http.MethodPost, "/api/agents", `{"name": "My Agent", "description": "d"}`)
	var created agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	w = doJSON(t, srv, http.MethodPatch, "/api/agents/"+created.ID+"/toggle", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("toggle of a user agent = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestHandleAgents_ToggleNotFound(t *testing.T) {
	srv := agentsTestServer(t)

	w := doJSON(t, srv, http.MethodPatch, "/api/agents/ghost/toggle", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("toggle of unknown agent = %d, want 404: %s", w.Code, w.Body.String())
	}
}

// DeleteBlockedForCuratedDefault verifies the API surface, not just the Store
// layer (store_test.go's TestStore_DeleteBlockedForCuratedDefault) — the
// handler must translate the Store's error into a 409, not a 500.
func TestHandleAgents_DeleteBlockedForCuratedDefault(t *testing.T) {
	srv := curatedTestServer(t)

	w := doJSON(t, srv, http.MethodDelete, "/api/agents/test-expert", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("delete curated default = %d, want 409: %s", w.Code, w.Body.String())
	}
}
