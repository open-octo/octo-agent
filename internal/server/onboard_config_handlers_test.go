package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	return tmp
}

func seedModels(t *testing.T, cfg config.Config) {
	t.Helper()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func getConfigResponse(t *testing.T, srv *Server) configResponse {
	t.Helper()
	w := doJSON(t, srv, http.MethodGet, "/api/config", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/config = %d: %s", w.Code, w.Body.String())
	}
	var resp configResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestConfig_CoauthorGlobalDefault(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// No config.yml value yet — GET reports the built-in default (true).
	resp := getConfigResponse(t, srv)
	if resp.Coauthor == nil || !*resp.Coauthor {
		t.Fatalf("initial coauthor = %+v, want true", resp.Coauthor)
	}

	// Set global default to false.
	if w := doJSON(t, srv, http.MethodPut, "/api/config/coauthor", `{"coauthor":false}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Coauthor == nil || *cfg.Coauthor {
		t.Fatalf("stored global = %+v, want false", cfg.Coauthor)
	}
	resp = getConfigResponse(t, srv)
	if resp.Coauthor == nil || *resp.Coauthor {
		t.Fatalf("response coauthor = %+v, want false", resp.Coauthor)
	}

	// OCTO_COAUTHOR overrides the stored config value — GET must reflect the
	// effective value the agent actually uses, not the raw config.yml field,
	// since Coauthor (unlike ShowReasoning) has an env-var layer above it.
	t.Setenv("OCTO_COAUTHOR", "1")
	resp = getConfigResponse(t, srv)
	if resp.Coauthor == nil || !*resp.Coauthor {
		t.Fatalf("response coauthor with OCTO_COAUTHOR=1 = %+v, want true (env should win over stored false)", resp.Coauthor)
	}
}

func TestConfig_ShowReasoningReloadsDefaultSender(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	before := srv.getSender()
	if before == nil {
		t.Fatal("server should have a default sender after startup")
	}

	// Toggle global show_reasoning off.
	if w := doJSON(t, srv, http.MethodPut, "/api/config/show_reasoning", `{"show_reasoning":false}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}

	// The default sender should have been rebuilt (new pointer).
	after := srv.getSender()
	if after == nil {
		t.Fatal("server default sender missing after reload")
	}
	if after == before {
		t.Error("default sender pointer did not change after show_reasoning toggle")
	}

	// An unbound session should resolve to the newly-reloaded default sender.
	sess := agent.NewSession("claude-sonnet-4-6", "")
	sender, model := srv.senderForSession(sess)
	if sender != after {
		t.Error("unbound session should use the reloaded default sender")
	}
	if model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", model)
	}

	// The effective show_reasoning broadcast value should reflect the new default.
	_, _, _, sr, _ := srv.sessionStatusFields(sess)
	if sr == nil || *sr {
		t.Fatalf("effective show_reasoning = %+v, want false", sr)
	}
}

func TestListProviders_MarksCustomCatchAll(t *testing.T) {
	presets := buildProviderPresets()
	byID := map[string]providerPreset{}
	for _, p := range presets {
		byID[p.ID] = p
	}
	p, ok := byID["custom"]
	if !ok {
		t.Fatal("custom missing from /api/providers presets")
	}
	if !p.CustomEndpoint || p.BaseURL != "" || p.API != "" {
		t.Errorf("custom: CustomEndpoint=%v BaseURL=%q API=%q, want true/empty/empty", p.CustomEndpoint, p.BaseURL, p.API)
	}
	// The retired compatible catch-alls must no longer appear.
	for _, id := range []string{"openai_compatible", "anthropic_compatible", "mistral"} {
		if _, ok := byID[id]; ok {
			t.Errorf("retired vendor %q still present in presets", id)
		}
	}
	if byID["deepseek"].CustomEndpoint {
		t.Error("deepseek must not be flagged custom_endpoint")
	}
}

func TestListProviders_IncludesModelVision(t *testing.T) {
	byID := map[string]providerPreset{}
	for _, p := range buildProviderPresets() {
		byID[p.ID] = p
	}
	mv := byID["bailian"].ModelVision
	if mv["qwen3.7-plus"] != true || mv["qwen3.7-max"] != false {
		t.Errorf("bailian model_vision = %v, want qwen3.7-plus:true qwen3.7-max:false", mv)
	}
	// The Custom vendor has no catalogue, so no model_vision map.
	if byID["custom"].ModelVision != nil {
		t.Errorf("custom model_vision = %v, want nil", byID["custom"].ModelVision)
	}
}

func TestCreateSession_EntryIDBindsSession(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-anthropic", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-kimi", Provider: "kimi", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-anthropic::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/sessions", `{"model":"kimi-k2.6"}`)
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
		t.Errorf("session model = %q, want kimi-k2.6 (entry id resolved)", resp.Session.Model)
	}
	sess, err := agent.LoadSession(resp.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ModelConfig != "kimi-k2.6" {
		t.Errorf("model_config = %q, want kimi-k2.6", sess.ModelConfig)
	}

	// A raw model string stays unbound.
	w = doJSON(t, srv, http.MethodPost, "/api/sessions", `{"model":"some-other-model"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST raw = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	sess, err = agent.LoadSession(resp.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ModelConfig != "" || sess.Model != "some-other-model" {
		t.Errorf("raw session = (%q, %q), want (some-other-model, \"\")", sess.Model, sess.ModelConfig)
	}
}

// Regression guard: with no workspace dir configured, a newly created
// session's WorkingDir stays empty — the server's launch directory keeps
// being the default, exactly like before workspace_dir existed.
func TestCreateSession_NoWorkspaceDir_WorkingDirEmpty(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}}},
		Default:   "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/sessions", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sessions = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	sess, err := agent.LoadSession(resp.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkingDir != "" {
		t.Errorf("WorkingDir = %q, want \"\" (no workspace_dir configured)", sess.WorkingDir)
	}
}

// With a configured workspace dir, a newly created session's WorkingDir
// defaults to it (the caller requested no working dir of its own).
func TestCreateSession_WorkspaceDirConfigured_SetsWorkingDir(t *testing.T) {
	home := setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}}},
		Default:   "ep-a::claude-sonnet-4-6",
	})
	wantDir := filepath.Join(home, "octo-workspace")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", WorkspaceDir: wantDir})

	w := doJSON(t, srv, http.MethodPost, "/api/sessions", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sessions = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	sess, err := agent.LoadSession(resp.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkingDir != wantDir {
		t.Errorf("WorkingDir = %q, want %q", sess.WorkingDir, wantDir)
	}
	if st, err := os.Stat(wantDir); err != nil || !st.IsDir() {
		t.Errorf("workspace dir %q was not created: %v", wantDir, err)
	}
}

// getEndpointsResponse calls GET /api/config/endpoints and decodes the body.
func getEndpointsResponse(t *testing.T, srv *Server) endpointsResponse {
	t.Helper()
	w := doJSON(t, srv, http.MethodGet, "/api/config/endpoints", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/config/endpoints = %d: %s", w.Code, w.Body.String())
	}
	var resp endpointsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode endpoints response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

// TestGetEndpoints_ReturnsTwoLevelShape pins the PR4b contract: a legacy flat
// models: config is served as the two-level { endpoints, default, lite } shape
// described in design §10.1, with has_api_key reporting key presence (never
// the key itself) and default/lite expressed as composite ids.
func TestGetEndpoints_ReturnsTwoLevelShape(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-anthropic", Provider: "anthropic", APIKey: "sk-main-12345678", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6", Vision: true}, {Model: "claude-haiku-4-5"}}},
			{ID: "ep-kimi", Provider: "kimi", BaseURL: "https://kimi.example", APIKey: "sk-kimi", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-anthropic::claude-sonnet-4-6",
		Lite:    "ep-anthropic::claude-haiku-4-5",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Capture the raw body too so we can assert the API keys never appear in
	// the response at all — the JSON shape has no api_key field, only the
	// has_api_key boolean.
	w := doJSON(t, srv, http.MethodGet, "/api/config/endpoints", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/config/endpoints = %d: %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if strings.Contains(raw, "sk-main-12345678") || strings.Contains(raw, "sk-kimi") {
		t.Errorf("response body leaks an API key: %s", raw)
	}

	var resp endpointsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode endpoints response: %v (body: %s)", err, w.Body.String())
	}

	// Two distinct (provider, base_url) groups → two endpoints. Both
	// anthropic entries have empty base_url, so they aggregate by
	// (anthropic, "") — the shared API key is NOT the grouping key (it's
	// (provider, base_url) per design §4.1). The kimi entry has a distinct
	// base_url, so it forms its own endpoint.
	if len(resp.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2 (one per distinct (provider, base_url)): %+v", len(resp.Endpoints), resp.Endpoints)
	}

	// Find the anthropic endpoint (two models) and the kimi endpoint (one).
	var anthropicEP, kimiEP *endpointConfigJSON
	for i := range resp.Endpoints {
		switch resp.Endpoints[i].Provider {
		case "anthropic":
			anthropicEP = &resp.Endpoints[i]
		case "kimi":
			kimiEP = &resp.Endpoints[i]
		}
	}
	if anthropicEP == nil || kimiEP == nil {
		t.Fatalf("missing expected providers: %+v", resp.Endpoints)
	}
	if len(anthropicEP.Models) != 2 {
		t.Errorf("anthropic endpoint models = %d, want 2: %+v", len(anthropicEP.Models), anthropicEP.Models)
	}
	if len(kimiEP.Models) != 1 {
		t.Errorf("kimi endpoint models = %d, want 1: %+v", len(kimiEP.Models), kimiEP.Models)
	}
	// Vision flag is per-model and migrates from the flat entry.
	var sonnetVision bool
	for _, m := range anthropicEP.Models {
		if m.Model == "claude-sonnet-4-6" {
			sonnetVision = m.Vision
		}
	}
	if !sonnetVision {
		t.Errorf("claude-sonnet-4-6 vision = false, want true (migrated from flat entry)")
	}

	// default + lite are composite ids whose endpoint prefix is the synthesised
	// legacy-<host>-<n> id.
	if resp.Default == "" || !strings.Contains(resp.Default, "::") {
		t.Errorf("default = %q, want a composite id <endpoint>::<model>", resp.Default)
	}
	if !strings.HasSuffix(resp.Default, "::claude-sonnet-4-6") {
		t.Errorf("default = %q, want suffix ::claude-sonnet-4-6", resp.Default)
	}
	if resp.Lite == "" || !strings.HasSuffix(resp.Lite, "::claude-haiku-4-5") {
		t.Errorf("lite = %q, want suffix ::claude-haiku-4-5", resp.Lite)
	}
	// default and lite should point at the same endpoint (anthropic group).
	if strings.Split(resp.Default, "::")[0] != strings.Split(resp.Lite, "::")[0] {
		t.Errorf("default endpoint %q != lite endpoint %q", strings.Split(resp.Default, "::")[0], strings.Split(resp.Lite, "::")[0])
	}

	// has_api_key reports presence; the response must NOT expose the key —
	// there is no api_key field in the JSON shape at all, only the boolean.
	if !anthropicEP.HasAPIKey {
		t.Errorf("anthropic endpoint has_api_key = false, want true (key seeded)")
	}
	if !kimiEP.HasAPIKey {
		t.Errorf("kimi endpoint has_api_key = false, want true (key seeded)")
	}
}

// TestGetEndpoints_NoKeyReportsAbsent covers the key-missing side of
// has_api_key so a frontend "未设置" badge is reachable.
func TestGetEndpoints_NoKeyReportsAbsent(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "deepseek", Models: []config.EndpointModel{{Model: "deepseek-v4-pro"}}}, // no APIKey
		},
		Default: "ep-a::deepseek-v4-pro",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getEndpointsResponse(t, srv)
	if len(resp.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1: %+v", len(resp.Endpoints), resp.Endpoints)
	}
	if resp.Endpoints[0].HasAPIKey {
		t.Errorf("has_api_key = true, want false (no key seeded)")
	}
}

// TestGetEndpoints_EmptyConfigReturnsEmptyShape pins the zero-config contract:
// a fresh install (no models, no endpoints) returns an empty (but well-formed)
// { endpoints: [], default: "", lite: "" } shape rather than a 404 or null.
func TestGetEndpoints_EmptyConfigReturnsEmptyShape(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getEndpointsResponse(t, srv)
	if resp.Endpoints == nil {
		t.Fatal("endpoints = nil, want an empty non-null array")
	}
	if len(resp.Endpoints) != 0 {
		t.Errorf("endpoints = %d, want 0: %+v", len(resp.Endpoints), resp.Endpoints)
	}
	if resp.Default != "" || resp.Lite != "" {
		t.Errorf("empty config: default=%q lite=%q, want both empty", resp.Default, resp.Lite)
	}
}

func TestBuildAgent_ImplicitLiteFromVendorRegistry(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.provider = "deepseek"
	srv.model = "deepseek-v4-pro"

	// Unbound session, no explicit lite entry → vendor's registry lite model
	// on the same (default) sender.
	sess := agent.NewSession("deepseek-v4-pro", "")
	a := srv.buildAgent(sess)
	if a.LiteModel != "deepseek-v4-flash" {
		t.Errorf("LiteModel = %q, want deepseek-v4-flash", a.LiteModel)
	}
	if a.LiteSender == nil || a.LiteSender != srv.sender {
		t.Error("implicit lite must reuse the session's own sender")
	}

	// Already on the lite model → no implicit lite.
	srv.model = "deepseek-v4-flash"
	sess2 := agent.NewSession("deepseek-v4-flash", "")
	if a2 := srv.buildAgent(sess2); a2.LiteSender != nil {
		t.Errorf("no implicit lite expected when primary IS the lite model, got %q", a2.LiteModel)
	}
}

func TestBuildAgent_ExplicitLiteBeatsImplicit(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-deepseek", Provider: "deepseek", APIKey: "sk-x", Models: []config.EndpointModel{{Model: "deepseek-v4-pro"}}},
			{ID: "ep-kimi", Provider: "kimi", APIKey: "sk-y", Models: []config.EndpointModel{{Model: "kimi-k2.5"}}},
		},
		Default: "ep-deepseek::deepseek-v4-pro",
		Lite:    "ep-kimi::kimi-k2.5",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.provider = "deepseek"
	srv.model = "deepseek-v4-pro"

	sess := agent.NewSession("deepseek-v4-pro", "")
	a := srv.buildAgent(sess)
	if a.LiteModel != "kimi-k2.5" {
		t.Errorf("LiteModel = %q, want the explicit entry kimi-k2.5", a.LiteModel)
	}
	if a.LiteSender == srv.sender {
		t.Error("explicit lite entry must get its own sender, not the default one")
	}
}

func TestBuildAgent_ImplicitLiteForBoundSession(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-anthropic", Provider: "anthropic", APIKey: "sk-a", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-deepseek", Provider: "deepseek", APIKey: "sk-d", Models: []config.EndpointModel{{Model: "deepseek-v4-pro"}}},
		},
		Default: "ep-anthropic::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.provider = "anthropic"
	srv.model = "claude-sonnet-4-6"

	sess := agent.NewSession("deepseek-v4-pro", "")
	sess.ModelConfig = "deepseek-v4-pro"
	a := srv.buildAgent(sess)
	if a.LiteModel != "deepseek-v4-flash" {
		t.Errorf("LiteModel = %q, want deepseek-v4-flash from the bound entry's vendor", a.LiteModel)
	}
	if a.LiteSender == nil || a.LiteSender != a.GetSender() {
		t.Error("bound session's implicit lite must reuse that session's sender")
	}
}

func TestGetConfig_WorkspaceDir(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints:    []config.Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}}},
		Default:      "ep-a::claude-sonnet-4-6",
		WorkspaceDir: "~/octo-projects",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getConfigResponse(t, srv)
	if resp.WorkspaceDir != "~/octo-projects" {
		t.Errorf("workspace_dir = %q, want %q", resp.WorkspaceDir, "~/octo-projects")
	}
}

func TestPutWorkspaceDir_UpdatesConfigAndServerDefault(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}}},
		Default:   "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	wantDir := filepath.Join(t.TempDir(), "workspace")
	reqBody, err := json.Marshal(map[string]string{"workspace_dir": wantDir})
	if err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, srv, http.MethodPut, "/api/config/workspace_dir", string(reqBody))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/workspace_dir = %d: %s", w.Code, w.Body.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceDir != wantDir {
		t.Errorf("config.WorkspaceDir = %q, want %q", cfg.WorkspaceDir, wantDir)
	}
	if srv.workspaceDir != wantDir {
		t.Errorf("server.workspaceDir = %q, want %q", srv.workspaceDir, wantDir)
	}
}
