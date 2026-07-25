package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleAgents_CreateListGetDelete(t *testing.T) {
	srv := agentsTestServer(t)

	// List (empty except builtins are excluded).
	w := doJSON(t, srv, http.MethodGet, "/api/agents", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	var listCheck []agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listCheck); err != nil {
		t.Fatal(err)
	}
	if len(listCheck) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(listCheck))
	}

	// Create.
	w = doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "Code Reviewer",
		"description": "Reviews code",
		"tools": ["read_file", "grep"],
		"mention_as": ["@review"]
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var created agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty id")
	}
	if created.Name != "Code Reviewer" || len(created.Tools) != 2 {
		t.Fatalf("unexpected profile: %+v", created)
	}

	// List now has 1.
	list := doAgents(t, srv)
	if len(list) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(list))
	}

	// Get single.
	got := doAgent(t, srv, created.ID)
	if got.ID != created.ID {
		t.Fatalf("get = %+v", got)
	}

	// Update.
	w = doJSON(t, srv, http.MethodPut, "/api/agents/"+created.ID, `{
		"name": "Code Reviewer v2",
		"description": "Updated",
		"tools": ["read_file"]
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	var updated agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Code Reviewer v2" || len(updated.Tools) != 1 {
		t.Fatalf("unexpected update: %+v", updated)
	}

	// Delete.
	w = doJSON(t, srv, http.MethodDelete, "/api/agents/"+created.ID, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", w.Code, w.Body.String())
	}
	list = doAgents(t, srv)
	if len(list) != 0 {
		t.Fatalf("expected 0 agents after delete, got %d", len(list))
	}
}

func TestHandleAgents_CreateDuplicate(t *testing.T) {
	srv := agentsTestServer(t)

	// Create once.
	w := doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "My Agent",
		"description": "d"
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create = %d: %s", w.Code, w.Body.String())
	}

	// Create again with same name-derived slug → 409.
	w = doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "My Agent",
		"description": "d2"
	}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestHandleAgents_BindUnbind(t *testing.T) {
	srv := agentsTestServer(t)

	// Create a profile.
	w := doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "Bound Agent",
		"description": "d"
	}`)
	var p agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}

	// Bind to a chat.
	w = doJSON(t, srv, http.MethodPost, "/api/agents/"+p.ID+"/bind", `{
		"platform": "weixin",
		"chat_id": "group-123"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("bind = %d: %s", w.Code, w.Body.String())
	}
	var bound agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.ChannelBindings) != 1 || bound.ChannelBindings[0].ChatID != "group-123" {
		t.Fatalf("unexpected bindings: %+v", bound.ChannelBindings)
	}

	// Unbind.
	w = doJSON(t, srv, http.MethodDelete, "/api/agents/"+p.ID+"/bind", `{
		"platform": "weixin",
		"chat_id": "group-123"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("unbind = %d: %s", w.Code, w.Body.String())
	}
	var unbound agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &unbound); err != nil {
		t.Fatal(err)
	}
	if len(unbound.ChannelBindings) != 0 {
		t.Fatalf("expected 0 bindings, got %+v", unbound.ChannelBindings)
	}
}

// agentsTestServer returns a server with an isolated ~/.octo/agents dir.
func agentsTestServer(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// Ensure the agents dir is empty (no user profiles).
	_ = os.MkdirAll(filepath.Join(tmp, ".octo", "agents"), 0o755)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	return srv
}

// doAgents calls GET /api/agents and decodes the response.
func doAgents(t *testing.T, srv *Server) []agentResponse {
	t.Helper()
	w := doJSON(t, srv, http.MethodGet, "/api/agents", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents = %d: %s", w.Code, w.Body.String())
	}
	var resp []agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// doAgent calls GET /api/agents/:id and decodes the response.
func doAgent(t *testing.T, srv *Server, id string) agentResponse {
	t.Helper()
	w := doJSON(t, srv, http.MethodGet, "/api/agents/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/%s = %d: %s", id, w.Code, w.Body.String())
	}
	var resp agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Code Reviewer":     "code-reviewer",
		"My Agent":          "my-agent",
		"  Spaces  ":        "spaces",
		"---":               "",
		"Agent_1":           "agent-1",
		"Multi   Spaces":    "multi-spaces",
		"Special!@#$Chars":  "specialchars",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
