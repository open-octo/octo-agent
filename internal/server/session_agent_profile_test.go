package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// seedSessionModels gives POST /api/sessions a default model to resolve.
func seedSessionModels(t *testing.T) {
	t.Helper()
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-kimi", Provider: "kimi", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-kimi::kimi-k2.6",
	})
}

func createSessionWithProfile(t *testing.T, srv *Server, profile string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"agent_profile": profile})
	w := doJSON(t, srv, http.MethodPost, "/api/sessions", string(body))
	var resp struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp.Session.ID
}

// explore/general/code-review shape a sub-agent for a delegated task; a
// top-level session must not be bound to one. An API client that passed its
// subagent_type through as agent_profile used to have it stored verbatim and
// rendered as an expert badge in the sidebar.
func TestCreateSession_RejectsBuiltinSubAgentProfile(t *testing.T) {
	setTestHome(t)
	seedSessionModels(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, id := range []string{"general", "explore", "code-review", "no-such-agent"} {
		code, _ := createSessionWithProfile(t, srv, id)
		if code != http.StatusBadRequest {
			t.Errorf("POST /api/sessions agent_profile=%q = %d, want 400", id, code)
		}
	}
}

func TestCreateSession_AcceptsDefaultAndUserOverride(t *testing.T) {
	home := setTestHome(t)
	seedSessionModels(t)
	userDir := filepath.Join(home, ".octo", "agents")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A user file at one of the built-in names is a real, user-authored
	// profile (Source "user"), so it stays a valid session profile.
	md := "---\ndescription: my own general\n---\npersona body\n"
	if err := os.WriteFile(filepath.Join(userDir, "general.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, id := range []string{"", "default", "general"} {
		code, sid := createSessionWithProfile(t, srv, id)
		if code != http.StatusOK {
			t.Fatalf("POST /api/sessions agent_profile=%q = %d", id, code)
		}
		want := id
		if want == "" {
			want = "default"
		}
		sess, err := agent.LoadSession(sid)
		if err != nil {
			t.Fatal(err)
		}
		if sess.AgentID != want {
			t.Errorf("session AgentID = %q, want %q", sess.AgentID, want)
		}
	}
}

func TestHandleUpdateSessionAgentProfile_RejectsBuiltinSubAgentProfile(t *testing.T) {
	setTestHome(t)
	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	body, _ := json.Marshal(updateSessionAgentProfileRequest{AgentProfile: "general"})
	w := doJSON(t, srv, http.MethodPatch, "/api/sessions/"+sess.ID+"/agent_profile", string(body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	got, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID == "general" {
		t.Error("rejected profile was persisted anyway")
	}
}
