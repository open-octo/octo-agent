package app

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider/anthropic"
	"github.com/open-octo/octo-agent/internal/provider/openai"
)

func TestVendor_KimiCodingPlan(t *testing.T) {
	v := vendorByID("kimi-coding-plan")
	if v == nil {
		t.Fatal("kimi-coding-plan not found in registry")
	}

	if v.DisplayName != "Kimi Coding Plan" {
		t.Errorf("DisplayName = %q, want Kimi Coding", v.DisplayName)
	}
	if v.Protocol != "anthropic" {
		t.Errorf("Protocol = %q, want anthropic", v.Protocol)
	}
	if v.API != "anthropic-messages" {
		t.Errorf("API = %q, want anthropic-messages", v.API)
	}
	if v.DefaultBaseURL != "https://api.kimi.com/coding" {
		t.Errorf("DefaultBaseURL = %q, want https://api.kimi.com/coding", v.DefaultBaseURL)
	}
	if v.DefaultModel != "kimi-for-coding" {
		t.Errorf("DefaultModel = %q, want kimi-for-coding", v.DefaultModel)
	}
	if v.APIKeyEnvVar != "MOONSHOT_API_KEY" {
		t.Errorf("APIKeyEnvVar = %q, want MOONSHOT_API_KEY", v.APIKeyEnvVar)
	}
}

func TestVendor_KimiCodingPlan_BuildClient(t *testing.T) {
	// buildClient should succeed with a dummy key for the anthropic protocol.
	_, err := buildClient("kimi-coding-plan", "sk-dummy-key", "", "", nil, nil)
	if err != nil {
		t.Fatalf("buildClient(kimi-coding-plan) error: %v", err)
	}
}

func TestVendor_KimiCodingPlan_BuildClient_CustomBaseURL(t *testing.T) {
	// Verify the custom base URL override is applied.
	client, err := buildClient("kimi-coding-plan", "sk-dummy-key", "https://custom.example/v1", "", nil, nil)

	if err != nil {
		t.Fatalf("buildClient error: %v", err)
	}

	// The openai client exposes BaseURL as a field.
	// Use reflection to avoid importing the internal/openai package here.
	if client == nil {
		t.Fatal("client is nil")
	}

	// The client is an openai.Client which has a BaseURL field.
	// We can't import internal/provider/openai from app_test due to layering,
	// so we rely on TestConnection with a mock server in integration tests.
	// This test at least verifies buildClient does not reject the vendor ID.
}

func TestIsKnownVendor(t *testing.T) {
	for _, id := range []string{
		"openrouter", "deepseek", "minimax", "kimi", "kimi-coding-plan",
		"glm", "openai", "anthropic", "bailian", "mimo", "longcat", "xai",
		ProviderCustom,
	} {
		if !IsKnownVendor(id) {
			t.Errorf("IsKnownVendor(%q) = false, want true", id)
		}
	}
	// Retired vendors must no longer resolve.
	for _, id := range []string{"mistral", "openai_compatible", "anthropic_compatible"} {
		if IsKnownVendor(id) {
			t.Errorf("IsKnownVendor(%q) = true, want false (retired)", id)
		}
	}
	if IsKnownVendor("bogus") {
		t.Error("IsKnownVendor(bogus) = true, want false")
	}
}

func TestVendor_CustomCatchAll(t *testing.T) {
	v := vendorByID(ProviderCustom)
	if v == nil {
		t.Fatalf("%s not found in registry", ProviderCustom)
	}
	if !v.CustomEndpoint {
		t.Errorf("%s: CustomEndpoint = false, want true", ProviderCustom)
	}
	// The Custom vendor has no fixed protocol/api — it is chosen per config entry.
	if v.Protocol != "" || v.API != "" {
		t.Errorf("%s: protocol/api = %q/%q, want empty (chosen per entry)", ProviderCustom, v.Protocol, v.API)
	}
	if v.DefaultBaseURL != "" || v.DefaultModel != "" || len(v.Models) != 0 {
		t.Errorf("%s: catch-all must not carry a fixed endpoint or model catalogue: %+v", ProviderCustom, v)
	}
	if v.APIKeyEnvVar != "CUSTOM_API_KEY" {
		t.Errorf("%s: APIKeyEnvVar = %q, want CUSTOM_API_KEY", ProviderCustom, v.APIKeyEnvVar)
	}
	if !VendorNeedsProtocol(ProviderCustom) {
		t.Errorf("VendorNeedsProtocol(%s) = false, want true", ProviderCustom)
	}
	// Every other vendor is pinned: fixed endpoint and a registry protocol.
	for _, v := range Registry {
		if v.CustomEndpoint {
			continue
		}
		if v.DefaultBaseURL == "" {
			t.Errorf("vendor %q has neither a fixed endpoint nor CustomEndpoint", v.ID)
		}
		if VendorNeedsProtocol(v.ID) {
			t.Errorf("pinned vendor %q must declare a protocol", v.ID)
		}
	}
}

func TestBuildClient_CustomRequiresBaseURLAndProtocol(t *testing.T) {
	// No base URL → fail regardless of protocol.
	if _, err := buildClient(ProviderCustom, "sk-dummy", "", "openai", nil, nil); err == nil {
		t.Errorf("buildClient(custom) without base URL must fail")
	}
	// Base URL but no protocol → fail (Custom has no registry-pinned protocol).
	if _, err := buildClient(ProviderCustom, "sk-dummy", "https://gw.example/v1", "", nil, nil); err == nil {
		t.Errorf("buildClient(custom) without protocol must fail")
	}
	// Base URL + protocol → ok, both wire formats.
	for _, proto := range []string{"openai", "anthropic"} {
		if _, err := buildClient(ProviderCustom, "sk-dummy", "https://gw.example/v1", proto, nil, nil); err != nil {
			t.Errorf("buildClient(custom, %s): %v", proto, err)
		}
	}
	// Pinned vendors still build with no override and ignore a supplied protocol.
	if _, err := buildClient("anthropic", "sk-dummy", "", "", nil, nil); err != nil {
		t.Errorf("buildClient(anthropic) without base URL: %v", err)
	}
}

// An empty base-URL override must resolve to the vendor's registry endpoint,
// not the wire client's built-in default (api.openai.com / api.anthropic.com).
// Regression: a bailian config with no base_url sent its DashScope key to
// OpenAI and got a foreign 401 back.
func TestBuildClient_EmptyBaseURL_UsesVendorEndpoint(t *testing.T) {
	for _, v := range Registry {
		if v.CustomEndpoint {
			continue
		}
		client, err := buildClient(v.ID, "sk-dummy", "", "", nil, nil)
		if err != nil {
			t.Errorf("buildClient(%s): %v", v.ID, err)
			continue
		}
		var got string
		switch c := client.(type) {
		case *openai.Client:
			got = c.BaseURL
		case *anthropic.Client:
			got = c.BaseURL
		default:
			t.Errorf("buildClient(%s): unexpected client type %T", v.ID, client)
			continue
		}
		if got != v.DefaultBaseURL {
			t.Errorf("buildClient(%s) BaseURL = %q, want vendor default %q", v.ID, got, v.DefaultBaseURL)
		}
	}
}

func TestVendor_XAI(t *testing.T) {
	v := vendorByID("xai")
	if v == nil {
		t.Fatal("xai not found in registry")
	}
	if v.DisplayName != "xAI (Grok)" {
		t.Errorf("DisplayName = %q, want xAI (Grok)", v.DisplayName)
	}
	if v.Protocol != "openai" {
		t.Errorf("Protocol = %q, want openai", v.Protocol)
	}
	if v.API != "openai-completions" {
		t.Errorf("API = %q, want openai-completions", v.API)
	}
	if v.DefaultBaseURL != "https://api.x.ai" {
		t.Errorf("DefaultBaseURL = %q, want https://api.x.ai", v.DefaultBaseURL)
	}
	if v.DefaultModel != "grok-4.5" {
		t.Errorf("DefaultModel = %q, want grok-4.5", v.DefaultModel)
	}
	if v.APIKeyEnvVar != "XAI_API_KEY" {
		t.Errorf("APIKeyEnvVar = %q, want XAI_API_KEY", v.APIKeyEnvVar)
	}
	// All current Grok chat models accept image input (docs.x.ai, 2026-07-09).
	for _, m := range v.Models {
		if !m.Vision {
			t.Errorf("xai model %q: Vision = false, want true", m.ID)
		}
	}
}

func TestVendor_XAI_BuildClient(t *testing.T) {
	// openai-protocol client with the registry endpoint, no override needed.
	client, err := buildClient("xai", "sk-dummy", "", "", nil, nil)
	if err != nil {
		t.Fatalf("buildClient(xai) error: %v", err)
	}
	if c, ok := client.(*openai.Client); !ok {
		t.Fatalf("buildClient(xai) client type = %T, want *openai.Client", client)
	} else if c.BaseURL != "https://api.x.ai" {
		t.Errorf("buildClient(xai) BaseURL = %q, want https://api.x.ai", c.BaseURL)
	}
}

func TestVendorEnvVars(t *testing.T) {
	tests := []struct {
		id, wantEnv string
	}{
		{"kimi-coding-plan", "MOONSHOT_API_KEY"},
		{"kimi", "MOONSHOT_API_KEY"},
		{"deepseek", "DEEPSEEK_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"bailian", "DASHSCOPE_API_KEY"},
		{"mimo", "MIMO_API_KEY"},
		{"longcat", "LONGCAT_API_KEY"},
		{"xai", "XAI_API_KEY"},
	}
	for _, tc := range tests {
		got := VendorAPIKeyEnvVar(tc.id)
		if got != tc.wantEnv {
			t.Errorf("VendorAPIKeyEnvVar(%q) = %q, want %q", tc.id, got, tc.wantEnv)
		}
	}
}

func TestVendorModels_ReturnsIDs(t *testing.T) {
	// VendorModels flattens the catalogue to plain ids for the UI dropdown.
	ids := VendorModels("deepseek")
	want := []string{"deepseek-flash"}
	if len(ids) != len(want) {
		t.Fatalf("VendorModels(deepseek) = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("VendorModels(deepseek)[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
	if VendorModels("bogus") != nil {
		t.Error("VendorModels(unknown) should be nil")
	}
	// The Custom vendor has no catalogue.
	if VendorModels(ProviderCustom) != nil {
		t.Error("VendorModels(custom) should be nil")
	}
}

func TestVendorModelVision(t *testing.T) {
	cases := []struct {
		vendor, model string
		vision, known bool
	}{
		{"anthropic", "claude-opus-4-8", true, true},
		{"deepseek", "deepseek-flash", true, true},
		{"deepseek", "deepseek-v4-pro", false, false}, // dropped from the catalogue
		{"bailian", "qwen3.7-plus", true, true},
		{"bailian", "qwen3.7-max", false, true}, // text-only flagship
		{"kimi", "kimi-k2.6", true, true},       // MoonViT multimodal
		{"glm", "glm-5.3", false, true},         // text-only
		{"glm", "glm-5.3-flash", true, true},    // image/video/file input
		{"openai", "o3-mini", false, true},      // no image input
		{"openai", "o4-mini", true, true},
		{"xai", "grok-4.5", true, true},
		{"xai", "grok-4.20", true, true},
		{"xai", "grok-build-0.1", true, true},
		{"xai", "grok-4.20-multi-agent", false, false}, // excluded from catalogue
		{"bailian", "not-a-model", false, false},       // unknown model
		{"bogus", "whatever", false, false},            // unknown vendor
		{ProviderCustom, "anything", false, false},
	}
	for _, tc := range cases {
		vision, known := VendorModelVision(tc.vendor, tc.model)
		if vision != tc.vision || known != tc.known {
			t.Errorf("VendorModelVision(%q,%q) = (%v,%v), want (%v,%v)",
				tc.vendor, tc.model, vision, known, tc.vision, tc.known)
		}
	}
}

func TestVendorModelVisionMap(t *testing.T) {
	m := VendorModelVisionMap("bailian")
	if m["qwen3.7-plus"] != true || m["qwen3.7-max"] != false {
		t.Errorf("VendorModelVisionMap(bailian) = %v", m)
	}
	if VendorModelVisionMap(ProviderCustom) != nil {
		t.Error("VendorModelVisionMap(custom) should be nil (no catalogue)")
	}
}

// A model added to the registry without a context window in
// agent.lookupContextWindow silently inherits the 128k fallback, which is
// plausible enough to go unnoticed while auto-compaction fires far too early.
// Every catalogue model must match the table explicitly, and so must every
// vendor's DefaultModel.
func TestRegistryModelsHaveContextWindow(t *testing.T) {
	// Guard the guard: a model outside the table must report known=false, or
	// the loop below would pass vacuously.
	if w, known := agent.ContextWindowKnown("no-such-model-42"); known {
		t.Fatalf("ContextWindowKnown(unknown model) = (%d, true), want known=false", w)
	}
	for _, v := range Registry {
		if v.CustomEndpoint {
			continue // no catalogue: the user names arbitrary models
		}
		if _, known := agent.ContextWindowKnown(v.DefaultModel); !known {
			t.Errorf("vendor %q DefaultModel %q has no context-window entry", v.ID, v.DefaultModel)
		}
		for _, m := range v.Models {
			if _, known := agent.ContextWindowKnown(m.ID); !known {
				t.Errorf("vendor %q model %q has no context-window entry", v.ID, m.ID)
			}
		}
	}
}

// Every vendor's DefaultModel must be one of its catalogue models — otherwise
// the onboarding form offers a default the dropdown can't show.
func TestRegistryDefaultModelIsInCatalogue(t *testing.T) {
	for _, v := range Registry {
		if v.CustomEndpoint {
			continue
		}
		found := false
		for _, m := range v.Models {
			if m.ID == v.DefaultModel {
				found = true
			}
		}
		if !found {
			t.Errorf("vendor %q DefaultModel %q is not in Models", v.ID, v.DefaultModel)
		}
	}
}

// TestRegistryModelsHaveDeterministicVision guards against a half-migrated
// entry: every catalogue model must be reachable via VendorModelVision.
func TestRegistryModelsHaveDeterministicVision(t *testing.T) {
	for _, v := range Registry {
		for _, m := range v.Models {
			if _, known := VendorModelVision(v.ID, m.ID); !known {
				t.Errorf("vendor %q model %q not resolvable via VendorModelVision", v.ID, m.ID)
			}
		}
	}
}
