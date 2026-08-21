package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// Two endpoints exposing the SAME model name: binding a session to the
// non-default one must survive verbatim as the composite id. Collapsing it to
// the bare model name (as both write paths once did) made every later
// EntryByModel re-resolution land on whichever endpoint scans first — the
// session silently ran on the wrong endpoint/key.
func sameModelTwoEndpointsConfig(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	seed := config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-official", Provider: "kimi", BaseURL: "https://official.example", APIKey: "sk-a", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
			{ID: "ep-proxy", Provider: "custom", BaseURL: "https://proxy.example", APIKey: "sk-b", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-official::kimi-k2.6",
	}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleUpdateSessionModel_CompositeIDKeepsEndpoint(t *testing.T) {
	sameModelTwoEndpointsConfig(t)

	sess := agent.NewSession("kimi-k2.6", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	payload, _ := json.Marshal(updateSessionModelRequest{ModelID: "ep-proxy::kimi-k2.6"})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sess.ID+"/model", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	got, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelConfig != "ep-proxy::kimi-k2.6" {
		t.Fatalf("ModelConfig = %q, want the composite id ep-proxy::kimi-k2.6", got.ModelConfig)
	}
	cfg, _ := config.Load()
	entry, ok := cfg.EntryByModel(got.ModelConfig)
	if !ok || entry.BaseURL != "https://proxy.example" {
		t.Fatalf("re-resolved entry = (%+v, %v), want the ep-proxy endpoint", entry, ok)
	}
}

func TestHandleCreateSession_CompositeIDKeepsEndpoint(t *testing.T) {
	sameModelTwoEndpointsConfig(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodPost, "/api/sessions", `{"model":"ep-proxy::kimi-k2.6"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sessions = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Session struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Session.Model != "kimi-k2.6" {
		t.Errorf("display model = %q, want the bare model name kimi-k2.6", resp.Session.Model)
	}

	sess, err := agent.LoadSession(resp.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ModelConfig != "ep-proxy::kimi-k2.6" {
		t.Fatalf("ModelConfig = %q, want the composite id ep-proxy::kimi-k2.6", sess.ModelConfig)
	}
	cfg, _ := config.Load()
	entry, ok := cfg.EntryByModel(sess.ModelConfig)
	if !ok || entry.BaseURL != "https://proxy.example" {
		t.Fatalf("re-resolved entry = (%+v, %v), want the ep-proxy endpoint", entry, ok)
	}
}

// The session list must surface the binding as model_id: the composer's model
// menu highlights the current row by composite id, and two endpoints exposing
// the same model name are indistinguishable by the bare name alone.
func TestHandleListSessions_SurfacesModelID(t *testing.T) {
	sameModelTwoEndpointsConfig(t)

	sess := agent.NewSession("kimi-k2.6", "")
	sess.ModelConfig = "ep-proxy::kimi-k2.6"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodGet, "/api/sessions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Sessions []struct {
			ID      string `json:"id"`
			ModelID string `json:"model_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, s := range resp.Sessions {
		if s.ID == sess.ID {
			if s.ModelID != "ep-proxy::kimi-k2.6" {
				t.Fatalf("model_id = %q, want the composite id ep-proxy::kimi-k2.6", s.ModelID)
			}
			return
		}
	}
	t.Fatalf("session %s not in list", sess.ID)
}
