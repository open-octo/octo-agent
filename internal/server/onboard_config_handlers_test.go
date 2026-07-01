package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/config"
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

func TestConfigModels_GetListsAllEntries(t *testing.T) {
	setTestHome(t)
	tru := true
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6", APIKey: "sk-main-12345678"},
			{Provider: "kimi", Model: "kimi-k2.6", BaseURL: "https://kimi.example", ShowReasoning: &tru},
			{Provider: "deepseek", Model: "deepseek-chat"},
		},
		DefaultModel: "kimi-k2.6",
		LiteModel:    "deepseek-chat",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getConfigResponse(t, srv)
	if len(resp.Models) != 3 {
		t.Fatalf("models = %d, want 3: %+v", len(resp.Models), resp.Models)
	}
	if resp.DefaultModelIdx != 1 {
		t.Errorf("default_model_idx = %d, want 1", resp.DefaultModelIdx)
	}
	byID := map[string]modelConfig{}
	for _, m := range resp.Models {
		byID[m.ID] = m
	}
	if byID["kimi-k2.6"].Type != "default" || byID["deepseek-chat"].Type != "lite" || byID["claude-sonnet-4-6"].Type != "" {
		t.Errorf("type badges wrong: %+v", resp.Models)
	}
	if byID["claude-sonnet-4-6"].APIKeyMasked == "" || byID["claude-sonnet-4-6"].APIKeyMasked == "sk-main-12345678" {
		t.Errorf("api_key_masked = %q, want masked non-empty", byID["claude-sonnet-4-6"].APIKeyMasked)
	}
	if !byID["claude-sonnet-4-6"].AnthropicFormat || byID["deepseek-chat"].AnthropicFormat {
		t.Errorf("anthropic_format flags wrong: %+v", resp.Models)
	}
}

func TestConfig_ShowReasoningGlobalDefault(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		},
		DefaultModel: "claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	resp := getConfigResponse(t, srv)
	if resp.ShowReasoning != nil {
		t.Fatalf("initial global show_reasoning = %+v, want nil", resp.ShowReasoning)
	}

	// Set global default to false.
	if w := doJSON(t, srv, http.MethodPut, "/api/config/show_reasoning", `{"show_reasoning":false}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.ShowReasoning == nil || *cfg.ShowReasoning {
		t.Fatalf("stored global = %+v, want false", cfg.ShowReasoning)
	}
	resp = getConfigResponse(t, srv)
	if resp.ShowReasoning == nil || *resp.ShowReasoning {
		t.Fatalf("response global = %+v, want false", resp.ShowReasoning)
	}

	// Toggle back to true.
	if w := doJSON(t, srv, http.MethodPut, "/api/config/show_reasoning", `{"show_reasoning":true}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ = config.Load()
	if cfg.ShowReasoning == nil || !*cfg.ShowReasoning {
		t.Fatalf("stored global = %+v, want true", cfg.ShowReasoning)
	}
}

func TestConfig_ShowReasoningReloadsDefaultSender(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6", APIKey: "sk-test"},
		},
		DefaultModel: "claude-sonnet-4-6",
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
	_, _, _, sr, _ := srv.sessionStatusFields()
	if sr == nil || *sr {
		t.Fatalf("effective show_reasoning = %+v, want false", sr)
	}
}

func TestConfigModels_PostAppendsEntry(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "anthropic", Model: "claude-sonnet-4-6"}},
		DefaultModel: "claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/models",
		`{"model":"kimi-k2.6","base_url":"https://api.moonshot.cn","api_key":"sk-kimi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.ID == "" {
		t.Fatalf("response = %+v", resp)
	}

	cfg, _ := config.Load()
	if len(cfg.Models) != 2 {
		t.Fatalf("entries = %d, want 2 — the second card must not overwrite the first", len(cfg.Models))
	}
	if cfg.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("default moved to %q, want claude-sonnet-4-6", cfg.DefaultModel)
	}
	added, ok := cfg.EntryByModel(resp.ID)
	if !ok {
		t.Fatalf("entry %q not stored", resp.ID)
	}
	if added.APIKey != "sk-kimi" || added.BaseURL != "https://api.moonshot.cn" {
		t.Errorf("stored entry = %+v", added)
	}
	// base_url matches the kimi vendor preset → provider inferred.
	if added.Provider != "kimi" {
		t.Errorf("provider = %q, want kimi (inferred from base_url)", added.Provider)
	}
}

func TestConfigModels_PostUnknownEndpointBecomesCustomVendor(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// A hand-typed endpoint that matches no vendor preset lands on the Custom
	// catch-all, with the wire format recorded on the entry's Protocol field.
	w := doJSON(t, srv, http.MethodPost, "/api/config/models",
		`{"model":"my-model","base_url":"https://gw.example/v1","api_key":"sk-x","provider":"custom"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if got := cfg.Models[0].Provider; got != "custom" {
		t.Errorf("provider = %q, want custom", got)
	}
	if got := cfg.Models[0].Protocol; got != "openai" {
		t.Errorf("protocol = %q, want openai (default)", got)
	}

	w = doJSON(t, srv, http.MethodPost, "/api/config/models",
		`{"model":"my-model-2","base_url":"https://gw2.example","api_key":"sk-x","provider":"custom","anthropic_format":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ = config.Load()
	if got := cfg.Models[1].Provider; got != "custom" {
		t.Errorf("anthropic_format provider = %q, want custom", got)
	}
	if got := cfg.Models[1].Protocol; got != "anthropic" {
		t.Errorf("anthropic_format protocol = %q, want anthropic", got)
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

func TestApplyModelRequest_ProtocolOnlyForCustom(t *testing.T) {
	// Selecting Custom records the protocol from the anthropic_format toggle.
	var e config.ModelEntry
	applyModelRequestToEntry(saveModelRequest{
		Provider: "custom", Model: "m", BaseURL: "https://gw.example", AnthropicFormat: true,
	}, &e)
	if e.Provider != "custom" || e.Protocol != "anthropic" {
		t.Fatalf("custom+anthropic: got provider=%q protocol=%q, want custom/anthropic", e.Provider, e.Protocol)
	}

	// Switching the same entry to a named vendor clears the stale protocol.
	applyModelRequestToEntry(saveModelRequest{
		Provider: "openai", Model: "gpt-5.4", BaseURL: "",
	}, &e)
	if e.Provider != "openai" || e.Protocol != "" {
		t.Fatalf("switch to named vendor: got provider=%q protocol=%q, want openai/(empty)", e.Provider, e.Protocol)
	}
}

func TestConfigModels_PostFirstEntryBecomesDefault(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/config/models",
		`{"model":"claude-sonnet-4-6","base_url":"https://api.anthropic.com","api_key":"sk-x","provider":"anthropic"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if len(cfg.Models) != 1 || cfg.DefaultModel != cfg.Models[0].Model {
		t.Fatalf("first entry must become default: %+v", cfg)
	}
	if cfg.Models[0].Provider != "anthropic" {
		t.Errorf("explicit provider must win: %q", cfg.Models[0].Provider)
	}
}

func TestConfigModels_PatchUpdatesAndKeepsKey(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6", APIKey: "sk-keep-me"},
		},
		DefaultModel: "claude-sonnet-4-6",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// No api_key in the request → stored key survives. A still-masked echo is
	// also treated as "no change".
	w := doJSON(t, srv, http.MethodPatch, "/api/config/models/claude-sonnet-4-6",
		`{"model":"claude-opus-4-7","base_url":"","api_key":"sk-k****1234"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	e, _ := cfg.EntryByModel("claude-opus-4-7")
	if e.Model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7", e.Model)
	}
	if e.APIKey != "sk-keep-me" {
		t.Errorf("api_key = %q, want preserved sk-keep-me", e.APIKey)
	}
	// The panel hardcodes anthropic_format=false and sends no provider; an
	// update with no vendor signal must not clobber the stored provider.
	if e.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic preserved", e.Provider)
	}
	// Editing the default entry must rebuild the runtime default sender/model,
	// or new unbound sessions keep using the stale startup model.
	if srv.model != "claude-opus-4-7" {
		t.Errorf("runtime default model = %q, want claude-opus-4-7 after editing default entry", srv.model)
	}

	if w := doJSON(t, srv, http.MethodPatch, "/api/config/models/ghost", `{"model":"x"}`); w.Code != http.StatusNotFound {
		t.Errorf("PATCH unknown id = %d, want 404", w.Code)
	}
}

func TestConfigModels_DeleteRepairsReferences(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "m1"},
			{Provider: "kimi", Model: "m2"},
		},
		DefaultModel: "m1",
		LiteModel:    "m2",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Deleting the default reassigns it to the first remaining entry.
	if w := doJSON(t, srv, http.MethodDelete, "/api/config/models/m1", ""); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if len(cfg.Models) != 1 || cfg.DefaultModel != "m2" {
		t.Fatalf("after delete: %+v", cfg)
	}

	// Deleting the lite entry clears the lite reference.
	if w := doJSON(t, srv, http.MethodDelete, "/api/config/models/m2", ""); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ = config.Load()
	if len(cfg.Models) != 0 || cfg.LiteModel != "" || cfg.DefaultModel != "" {
		t.Fatalf("after second delete: %+v", cfg)
	}

	if w := doJSON(t, srv, http.MethodDelete, "/api/config/models/ghost", ""); w.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown id = %d, want 404", w.Code)
	}
}

func TestConfigModels_SetDefault(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "m1"},
			{Provider: "kimi", Model: "m2"},
		},
		DefaultModel: "m1",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	if w := doJSON(t, srv, http.MethodPost, "/api/config/models/m2/default", ""); w.Code != http.StatusOK {
		t.Fatalf("set-default = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.DefaultModel != "m2" {
		t.Fatalf("default = %q, want m2", cfg.DefaultModel)
	}
	if srv.model != "m2" {
		t.Errorf("runtime model = %q, want m2", srv.model)
	}

	if w := doJSON(t, srv, http.MethodPost, "/api/config/models/ghost/default", ""); w.Code != http.StatusNotFound {
		t.Errorf("set-default unknown id = %d, want 404", w.Code)
	}
}

func TestConfigModels_DuplicateModelRejected(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Model is the entry id, so a second entry for the same model is a conflict.
	if w := doJSON(t, srv, http.MethodPost, "/api/config/models",
		`{"model":"kimi-k2.6","api_key":"sk-1"}`); w.Code != http.StatusOK {
		t.Fatalf("first POST = %d: %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, srv, http.MethodPost, "/api/config/models",
		`{"model":"kimi-k2.6","api_key":"sk-2"}`); w.Code != http.StatusConflict {
		t.Fatalf("duplicate POST = %d, want 409: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if len(cfg.Models) != 1 {
		t.Fatalf("entries = %d, want 1 (duplicate rejected)", len(cfg.Models))
	}
}

func TestCreateSession_EntryIDBindsSession(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			{Provider: "kimi", Model: "kimi-k2.6"},
		},
		DefaultModel: "claude-sonnet-4-6",
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

func TestConfigModels_SetLiteToggles(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "m1"},
			{Provider: "deepseek", Model: "m2"},
		},
		DefaultModel: "m1",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Set.
	if w := doJSON(t, srv, http.MethodPost, "/api/config/models/m2/lite", ""); w.Code != http.StatusOK {
		t.Fatalf("set-lite = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.LiteModel != "m2" {
		t.Fatalf("lite = %q, want m2", cfg.LiteModel)
	}
	// The badge surfaces in GET /api/config.
	resp := getConfigResponse(t, srv)
	for _, m := range resp.Models {
		if m.ID == "m2" && m.Type != "lite" {
			t.Errorf("cheap type = %q, want lite", m.Type)
		}
	}

	// Toggle off.
	if w := doJSON(t, srv, http.MethodPost, "/api/config/models/m2/lite", ""); w.Code != http.StatusOK {
		t.Fatalf("unset-lite = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ = config.Load()
	if cfg.LiteModel != "" {
		t.Fatalf("lite = %q, want cleared", cfg.LiteModel)
	}

	if w := doJSON(t, srv, http.MethodPost, "/api/config/models/ghost/lite", ""); w.Code != http.StatusNotFound {
		t.Errorf("set-lite unknown id = %d, want 404", w.Code)
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
		Models: []config.ModelEntry{
			{Provider: "deepseek", Model: "deepseek-v4-pro", APIKey: "sk-x"},
			{Provider: "kimi", Model: "kimi-k2.5", APIKey: "sk-y"},
		},
		DefaultModel: "deepseek-v4-pro",
		LiteModel:    "kimi-k2.5",
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
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6", APIKey: "sk-a"},
			{Provider: "deepseek", Model: "deepseek-v4-pro", APIKey: "sk-d"},
		},
		DefaultModel: "claude-sonnet-4-6",
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
	if a.LiteSender == nil || a.LiteSender != a.Sender {
		t.Error("bound session's implicit lite must reuse that session's sender")
	}
}

// TestConfigModels_PatchModelChangeRepairsDefault: model is the entry id, so
// changing it re-keys the entry and repairs the default_model reference that
// pointed at the old model.
func TestConfigModels_PatchModelChangeRepairsDefault(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models: []config.ModelEntry{
			{Provider: "openai", Model: "qwen3.7-max", APIKey: "sk-x"},
		},
		DefaultModel: "qwen3.7-max",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPatch, "/api/config/models/qwen3.7-max",
		`{"model":"qwen3.7-plus","base_url":"","api_key":"sk-x****","provider":"openai"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load()
	if _, ok := cfg.EntryByModel("qwen3.7-plus"); !ok {
		t.Fatalf("entry should be re-keyed to new model qwen3.7-plus: %+v", cfg.Models)
	}
	if _, ok := cfg.EntryByModel("qwen3.7-max"); ok {
		t.Errorf("stale model qwen3.7-max should be gone")
	}
	if cfg.DefaultModel != "qwen3.7-plus" {
		t.Errorf("default_model = %q, want qwen3.7-plus (repaired)", cfg.DefaultModel)
	}
}
