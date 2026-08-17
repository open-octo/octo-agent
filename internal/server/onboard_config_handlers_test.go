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

// TestOnboardAttempt_StopsSoulSetupNudge covers #1660: once /api/onboard/attempt
// has fired, detectOnboardPhase must not keep reporting soul_setup even
// though soul.md is still missing (the user interrupted the first attempt) —
// the Profile page's manual "Update" buttons remain the way back in.
func TestOnboardAttempt_StopsSoulSetupNudge(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	if got := detectOnboardPhase(); got != "soul_setup" {
		t.Fatalf("detectOnboardPhase = %q before any attempt, want soul_setup", got)
	}

	w := doJSON(t, srv, http.MethodPost, "/api/onboard/attempt", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/onboard/attempt = %d: %s", w.Code, w.Body.String())
	}

	if !config.OnboardAttempted() {
		t.Fatal("config.OnboardAttempted() = false after POST /api/onboard/attempt")
	}
	if got := detectOnboardPhase(); got != "" {
		t.Fatalf("detectOnboardPhase = %q after attempt (identity still missing), want \"\" (no repeat nudge)", got)
	}

	// The regression this fix targets: a config.yml rewrite AFTER the marker was
	// set (first-run key save, language, the /onboard skill) must NOT re-open the
	// nudge. The marker lives in its own file, so a full config Save can't clobber
	// it — detectOnboardPhase still returns "" (#1660 follow-up).
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Language = "zh"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if got := detectOnboardPhase(); got != "" {
		t.Fatalf("detectOnboardPhase = %q after a later config.yml Save, want \"\" (marker must survive)", got)
	}
}

// TestDetectOnboardPhase_ExistingIdentitySkipsNudge covers the trigger gate:
// the soul_setup nudge fires only when the user has NO identity at all. If
// either soul.md or user.md already exists — even without the attempt marker —
// detectOnboardPhase must return "".
func TestDetectOnboardPhase_ExistingIdentitySkipsNudge(t *testing.T) {
	for _, name := range []string{"soul.md", "user.md"} {
		t.Run(name, func(t *testing.T) {
			home := setTestHome(t)
			seedModels(t, config.Config{
				Endpoints: []config.Endpoint{
					{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
				},
				Default: "ep-a::claude-sonnet-4-6",
			})
			octo := filepath.Join(home, ".octo")
			if err := os.MkdirAll(octo, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(octo, name), []byte("# identity"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := detectOnboardPhase(); got != "" {
				t.Fatalf("detectOnboardPhase = %q with %s present, want \"\" (no nudge when identity exists)", got, name)
			}
		})
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
// session's WorkingDir defaults to ~/Octo — the global default every user
// gets unless they explicitly override it.
func TestCreateSession_NoWorkspaceDir_DefaultsToOcto(t *testing.T) {
	home := setTestHome(t)
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
	wantDir := filepath.Join(home, "Octo")
	if sess.WorkingDir != wantDir {
		t.Errorf("WorkingDir = %q, want %q (default ~/Octo)", sess.WorkingDir, wantDir)
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

// TestGetEndpoints_HeadersRoundTrip pins that an endpoint with custom Headers
// echoes them back under the "headers" key, while an endpoint with none
// reports an empty/absent map (design's Server API DTO extension).
func TestGetEndpoints_HeadersRoundTrip(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-with-headers", Provider: "custom", BaseURL: "https://gw.example", APIKey: "sk-a", Headers: map[string]string{"X-Tenant-Id": "abc"}, Models: []config.EndpointModel{{Model: "m1"}}},
			{ID: "ep-no-headers", Provider: "custom", BaseURL: "https://plain.example", APIKey: "sk-b", Models: []config.EndpointModel{{Model: "m2"}}},
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getEndpointsResponse(t, srv)
	var withHeaders, noHeaders *endpointConfigJSON
	for i := range resp.Endpoints {
		switch resp.Endpoints[i].ID {
		case "ep-with-headers":
			withHeaders = &resp.Endpoints[i]
		case "ep-no-headers":
			noHeaders = &resp.Endpoints[i]
		}
	}
	if withHeaders == nil || noHeaders == nil {
		t.Fatalf("missing expected endpoints: %+v", resp.Endpoints)
	}
	if got := withHeaders.Headers["X-Tenant-Id"]; got != "abc" {
		t.Errorf("ep-with-headers headers[X-Tenant-Id] = %q, want %q", got, "abc")
	}
	if len(noHeaders.Headers) != 0 {
		t.Errorf("ep-no-headers headers = %+v, want empty/absent", noHeaders.Headers)
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

// GET /api/config always reports the resolved effective default too, so the
// Settings UI can show it instead of a bare, easily-misread "auto" — even
// when the raw config value is empty and resolves to ~/Octo.
func TestGetConfig_WorkspaceDirDefault_ResolvesToOcto(t *testing.T) {
	home := setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}}},
		Default:   "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getConfigResponse(t, srv)
	wantDefault := filepath.Join(home, "Octo")
	if resp.WorkspaceDirDefault != wantDefault {
		t.Errorf("workspace_dir_default = %q, want %q", resp.WorkspaceDirDefault, wantDefault)
	}
	if resp.WorkspaceDir != "" {
		t.Errorf("workspace_dir = %q, want \"\" (not customized)", resp.WorkspaceDir)
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

func TestPutReasoningEffort_ValidLevel_SavesAndReturns(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	reqBody, err := json.Marshal(map[string]string{"reasoning_effort": "xhigh"})
	if err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, srv, http.MethodPut, "/api/config/reasoning_effort", string(reqBody))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/reasoning_effort = %d: %s", w.Code, w.Body.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReasoningEffort != "xhigh" {
		t.Errorf("config.ReasoningEffort = %q, want %q", cfg.ReasoningEffort, "xhigh")
	}
}

func TestPutReasoningEffort_InvalidLevel_Rejects(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	reqBody, err := json.Marshal(map[string]string{"reasoning_effort": "ultra"})
	if err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, srv, http.MethodPut, "/api/config/reasoning_effort", string(reqBody))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/config/reasoning_effort (invalid) = %d, want %d", w.Code, http.StatusBadRequest)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReasoningEffort != "" {
		t.Errorf("config.ReasoningEffort = %q, want empty (reject should not save)", cfg.ReasoningEffort)
	}
}

func TestPutPermissionMode_ValidMode_SavesAndReturns(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	reqBody, err := json.Marshal(map[string]string{"permission_mode": "strict"})
	if err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, srv, http.MethodPut, "/api/config/permission_mode", string(reqBody))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/permission_mode = %d: %s", w.Code, w.Body.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PermissionMode != "strict" {
		t.Errorf("config.PermissionMode = %q, want %q", cfg.PermissionMode, "strict")
	}
}

func TestPutPermissionMode_InvalidMode_Rejects(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	reqBody, err := json.Marshal(map[string]string{"permission_mode": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, srv, http.MethodPut, "/api/config/permission_mode", string(reqBody))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/config/permission_mode (invalid) = %d, want %d", w.Code, http.StatusBadRequest)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PermissionMode != "" {
		t.Errorf("config.PermissionMode = %q, want empty (reject should not save)", cfg.PermissionMode)
	}
}

// TestCreateEndpoint_DocumentedModelsShape sends the exact request body
// documented in config-setup/SKILL.md's "Create an Endpoint" example — a
// models array of {"model": ..., "vision": ...} objects, matching
// createEndpointRequest.Models []endpointModelIn. Regression guard for #1941,
// where the doc and the wire format had drifted apart.
func TestCreateEndpoint_DocumentedModelsShape(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	body := `{
		"id": "my-relay",
		"name": "My Relay",
		"provider": "custom",
		"api_key": "sk-test",
		"base_url": "https://api.example.com",
		"protocol": "openai",
		"models": [{"model": "gpt-4o", "vision": false}]
	}`
	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/config/endpoints = %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp endpointJSONOut
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "gpt-4o" {
		t.Fatalf("models = %+v, want one gpt-4o entry", resp.Models)
	}
}

// TestCreateEndpoint_HeadersPersisted covers a create request that includes a
// headers object — it must be persisted onto the new config.Endpoint and
// echoed back both in the create response and a subsequent GET.
func TestCreateEndpoint_HeadersPersisted(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	body := `{
		"id": "my-relay",
		"provider": "custom",
		"base_url": "https://api.example.com",
		"headers": {"X-Tenant-Id": "abc", "X-Trace": "1"}
	}`
	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/config/endpoints = %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	var resp endpointJSONOut
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	if resp.Headers["X-Tenant-Id"] != "abc" || resp.Headers["X-Trace"] != "1" {
		t.Fatalf("create response headers = %+v, want both custom headers echoed", resp.Headers)
	}

	// Also verify persistence via a fresh GET.
	cfg, _ := config.Load()
	var saved *config.Endpoint
	for i := range cfg.Endpoints {
		if cfg.Endpoints[i].ID == "my-relay" {
			saved = &cfg.Endpoints[i]
		}
	}
	if saved == nil {
		t.Fatalf("endpoint my-relay not found in saved config: %+v", cfg.Endpoints)
	}
	if saved.Headers["X-Tenant-Id"] != "abc" || saved.Headers["X-Trace"] != "1" {
		t.Errorf("saved config headers = %+v, want both custom headers persisted", saved.Headers)
	}
}

// TestCreateEndpoint_StringModelsRejectedWithDetail covers the failure mode
// #1941 actually hit: a plain string models array (the shape SKILL.md wrongly
// documented before this fix) doesn't unmarshal into []endpointModelIn. The
// 400 must explain why instead of the old bare "invalid JSON body".
func TestCreateEndpoint_StringModelsRejectedWithDetail(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	body := `{"id": "my-relay", "provider": "custom", "models": ["gpt-4o"]}`
	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/config/endpoints with string models = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot unmarshal") {
		t.Errorf("error body = %q, want the underlying decode error, not a bare message", w.Body.String())
	}
}

func TestSetEndpointDefault_WithModelQuery_SetsSpecificModel(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{
				{Model: "claude-sonnet-4-6"},
				{Model: "claude-haiku-4-5"},
			}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/default?model=claude-haiku-4-5", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST default?model = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Default != "ep-a::claude-haiku-4-5" {
		t.Errorf("default = %q, want ep-a::claude-haiku-4-5", cfg.Default)
	}
}

func TestSetEndpointDefault_WithoutModelQuery_FallsBackToFirstModel(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{
				{Model: "claude-haiku-4-5"},
				{Model: "claude-sonnet-4-6"},
			}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/default", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST default = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Default != "ep-a::claude-haiku-4-5" {
		t.Errorf("default = %q, want ep-a::claude-haiku-4-5 (first model)", cfg.Default)
	}
}

func TestSetEndpointDefault_MissingModel_ReturnsBadRequest(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/default?model=missing-model", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	cfg, _ := config.Load()
	if cfg.Default != "ep-a::claude-sonnet-4-6" {
		t.Errorf("default changed to %q, want unchanged", cfg.Default)
	}
}

func TestSetEndpointLite_WithModelQuery_SetsSpecificModel(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{
				{Model: "claude-sonnet-4-6"},
				{Model: "claude-haiku-4-5"},
			}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/lite?model=claude-haiku-4-5", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST lite?model = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Lite != "ep-a::claude-haiku-4-5" {
		t.Errorf("lite = %q, want ep-a::claude-haiku-4-5", cfg.Lite)
	}
}

func TestSetEndpointLite_WithoutModelQuery_FallsBackToFirstModel(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{
				{Model: "claude-haiku-4-5"},
				{Model: "claude-sonnet-4-6"},
			}},
		},
		Default: "ep-a::claude-haiku-4-5",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/lite", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST lite = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Lite != "ep-a::claude-haiku-4-5" {
		t.Errorf("lite = %q, want ep-a::claude-haiku-4-5 (first model)", cfg.Lite)
	}
}

func TestSetEndpointLite_MissingModel_ReturnsBadRequest(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/lite?model=missing-model", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	cfg, _ := config.Load()
	if cfg.Lite != "" {
		t.Errorf("lite = %q, want empty", cfg.Lite)
	}
}

func TestUnsetEndpointLite_ClearsLite(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{
				{Model: "claude-sonnet-4-6"},
				{Model: "claude-haiku-4-5"},
			}},
		},
		Default: "ep-a::claude-sonnet-4-6",
		Lite:    "ep-a::claude-haiku-4-5",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodDelete, "/api/config/endpoints/ep-a/lite", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE lite = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Lite != "" {
		t.Errorf("lite = %q, want empty", cfg.Lite)
	}
}

func TestUnsetEndpointLite_UnknownEndpoint_ReturnsNotFound(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodDelete, "/api/config/endpoints/unknown/lite", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSetEndpointDefault_UnknownEndpoint_ReturnsNotFound(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/unknown/default", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSetEndpointLite_UnknownEndpoint_ReturnsNotFound(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/unknown/lite", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSetEndpointDefault_EmptyEndpoint_ReturnsBadRequest(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-empty", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{}},
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-empty/default", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	cfg, _ := config.Load()
	if cfg.Default != "ep-a::claude-sonnet-4-6" {
		t.Errorf("default changed to %q, want unchanged", cfg.Default)
	}
}

func TestSetEndpointLite_EmptyEndpoint_ReturnsBadRequest(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-empty", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{}},
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-empty/lite", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	cfg, _ := config.Load()
	if cfg.Lite != "" {
		t.Errorf("lite = %q, want empty", cfg.Lite)
	}
}

func TestUnsetEndpointLite_DifferentEndpointHoldsLite_IsNoOp(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-b", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{{Model: "claude-haiku-4-5"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
		Lite:    "ep-b::claude-haiku-4-5",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodDelete, "/api/config/endpoints/ep-a/lite", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE lite = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.Lite != "ep-b::claude-haiku-4-5" {
		t.Errorf("lite = %q, want ep-b::claude-haiku-4-5 (unchanged)", cfg.Lite)
	}
}

// TestPutReasoningEffort_Off_PersistsEmptyAndDisablesShowReasoning pins the
// global PUT to the same "off is a wire sentinel" contract as the per-session
// PATCH: persist "" (a stored literal "off" reads as thinking-ON at the
// provider layer) and drop show_reasoning, which has nothing to show.
func TestPutReasoningEffort_Off_PersistsEmptyAndDisablesShowReasoning(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPut, "/api/config/reasoning_effort", `{"reasoning_effort":"off"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/reasoning_effort = %d: %s", w.Code, w.Body.String())
	}
	// The response echoes the wire sentinel, not the stored "".
	var resp struct {
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ReasoningEffort != "off" {
		t.Errorf("response reasoning_effort = %q, want \"off\"", resp.ReasoningEffort)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReasoningEffort != "" {
		t.Errorf("config.ReasoningEffort = %q, want \"\" (off persists as empty)", cfg.ReasoningEffort)
	}
	if cfg.ShowReasoning == nil || *cfg.ShowReasoning {
		t.Errorf("config.ShowReasoning = %v, want false when reasoning is off", cfg.ShowReasoning)
	}

	// The broadcast/status surface applies the same sentinel: "" must never
	// reach session_update consumers (ChatView drops empty-string updates).
	if _, _, re, _, _ := srv.sessionStatusFields(nil); re != "off" {
		t.Errorf("sessionStatusFields reasoning_effort = %q, want \"off\"", re)
	}
}

// TestGetConfig_ReasoningEffortOffSentinel: "" (the stored form of off) must
// surface as "off" on the wire — through omitempty the field used to vanish,
// and the frontend defaulted an absent value to "medium".
func TestGetConfig_ReasoningEffortOffSentinel(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodGet, "/api/config", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/config = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ReasoningEffort != "off" {
		t.Errorf("reasoning_effort = %q, want \"off\"", resp.ReasoningEffort)
	}
}

// visionSeed is an endpoint carrying one vision model and one text-only model.
func visionSeed() config.Config {
	return config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "anthropic", APIKey: "sk-test", Models: []config.EndpointModel{
				{Model: "text-model", Vision: false},
				{Model: "vision-model", Vision: true},
			}},
		},
		Default: "ep-a::text-model",
	}
}

func TestSetEndpointVisionHelper(t *testing.T) {
	setTestHome(t)
	seedModels(t, visionSeed())
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/vision_helper?model=vision-model", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST vision_helper = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.VisionHelper != "ep-a::vision-model" {
		t.Errorf("vision_helper = %q, want ep-a::vision-model", cfg.VisionHelper)
	}
	if _, ok := cfg.ResolveVisionHelper(); !ok {
		t.Error("the value written by the API must resolve")
	}
}

// Omitting ?model picks the first vision-capable model, not simply the first
// model — the endpoint's first entry here is text-only.
func TestSetEndpointVisionHelper_WithoutModelPicksFirstVisionModel(t *testing.T) {
	setTestHome(t)
	seedModels(t, visionSeed())
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/vision_helper", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST vision_helper = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.VisionHelper != "ep-a::vision-model" {
		t.Errorf("vision_helper = %q, want ep-a::vision-model", cfg.VisionHelper)
	}
}

func TestSetEndpointVisionHelper_RejectsTextOnlyModel(t *testing.T) {
	setTestHome(t)
	seedModels(t, visionSeed())
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/endpoints/ep-a/vision_helper?model=text-model", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST vision_helper (text-only) = %d, want 400: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.VisionHelper != "" {
		t.Errorf("vision_helper = %q, want it left unset", cfg.VisionHelper)
	}
}

func TestUnsetEndpointVisionHelper(t *testing.T) {
	setTestHome(t)
	seed := visionSeed()
	seed.VisionHelper = "ep-a::vision-model"
	seedModels(t, seed)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodDelete, "/api/config/endpoints/ep-a/vision_helper", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE vision_helper = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.VisionHelper != "" {
		t.Errorf("vision_helper = %q, want cleared", cfg.VisionHelper)
	}
}

// A dangling vision_helper would silently disable the feature and show up in
// `octo doctor`, so deleting the model it names must clear it.
func TestDeleteEndpointModel_ClearsVisionHelper(t *testing.T) {
	setTestHome(t)
	seed := visionSeed()
	seed.VisionHelper = "ep-a::vision-model"
	seedModels(t, seed)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodDelete, "/api/config/endpoints/ep-a/models/vision-model", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE model = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.VisionHelper != "" {
		t.Errorf("vision_helper = %q, want cleared with the model it named", cfg.VisionHelper)
	}
}

func TestDeleteEndpoint_ClearsVisionHelper(t *testing.T) {
	setTestHome(t)
	seed := visionSeed()
	seed.VisionHelper = "ep-a::vision-model"
	seed.Endpoints = append(seed.Endpoints, config.Endpoint{
		ID: "ep-b", Provider: "anthropic", APIKey: "sk-test",
		Models: []config.EndpointModel{{Model: "other"}},
	})
	seed.Default = "ep-b::other"
	seedModels(t, seed)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodDelete, "/api/config/endpoints/ep-a", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE endpoint = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.VisionHelper != "" {
		t.Errorf("vision_helper = %q, want cleared with its endpoint", cfg.VisionHelper)
	}
}

func TestListEndpoints_ExposesVisionHelper(t *testing.T) {
	setTestHome(t)
	seed := visionSeed()
	seed.VisionHelper = "ep-a::vision-model"
	seedModels(t, seed)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodGet, "/api/config/endpoints", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET endpoints = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"vision_helper":"ep-a::vision-model"`) {
		t.Errorf("response should carry vision_helper so the UI can mark the chip: %s", w.Body.String())
	}
}
