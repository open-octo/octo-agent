package config

import (
	"strings"
	"testing"
)

// TestConfigValidate_EndpointLevel covers the endpoint-level checks (PR5:
// the only Validate path now that Config.Models is deleted): endpoint id
// uniqueness/legality, each endpoint has at least one model, model names
// non-empty, no duplicate models within one endpoint, and Default/Lite
// composite ids resolve.
func TestConfigValidate_EndpointLevel(t *testing.T) {
	goodEP := Endpoint{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6", Vision: true}}}
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"good endpoint block", Config{Endpoints: []Endpoint{goodEP}, Default: "ep-a::claude-sonnet-4-6"}, ""},
		{"duplicate endpoint id", Config{Endpoints: []Endpoint{goodEP, goodEP}}, "duplicate endpoint id"},
		{"endpoint id with illegal chars (contains ::)", Config{Endpoints: []Endpoint{{ID: "bad::id", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}}}}, "illegal"},
		{"endpoint id with space", Config{Endpoints: []Endpoint{{ID: "has space", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}}}}, "illegal"},
		{"endpoint with no models", Config{Endpoints: []Endpoint{{ID: "ep-empty", Provider: "anthropic"}}}, "no models"},
		{"endpoint model with empty name", Config{Endpoints: []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{}}}}}, "no model name"},
		{"duplicate model within one endpoint", Config{Endpoints: []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}, {Model: "claude-sonnet-4-6"}}}}}, "duplicate model"},
		{"same model across different endpoints is allowed", Config{Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-b", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		}}, ""},
		{"dangling Default composite id", Config{Endpoints: []Endpoint{goodEP}, Default: "ghost::claude-sonnet-4-6"}, "default"},
		{"dangling Lite composite id", Config{Endpoints: []Endpoint{goodEP}, Lite: "ghost::claude-haiku-4-5"}, "lite"},
		{"Default points at existing endpoint but missing model", Config{Endpoints: []Endpoint{goodEP}, Default: "ep-a::ghost-model"}, "default"},
		{"endpoint header with empty key", Config{Endpoints: []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}, Headers: map[string]string{"": "x"}}}}, "empty"},
		{"endpoint header with non-empty key is fine", Config{Endpoints: []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}, Headers: map[string]string{"X-Tenant-Id": "abc"}}}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probs := tt.cfg.Validate()
			joined := strings.Join(probs, "; ")
			if tt.want == "" {
				if len(probs) != 0 {
					t.Errorf("want no problems, got %v", probs)
				}
				return
			}
			if !strings.Contains(joined, tt.want) {
				t.Errorf("want a problem containing %q, got %v", tt.want, probs)
			}
		})
	}
}

// TestConfigValidate_GlobalScalars covers the top-level scalar fields that moved
// from per-model to global (permission_mode, reasoning_effort, compact_auto_pct).
// Hand edits bypass the CLI/API write-path validation, so Validate is the only
// thing standing between a typo and a silent runtime fallback. These checks run
// independently of endpoints — a config with no endpoints must still flag them.
func TestConfigValidate_GlobalScalars(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty permission_mode is fine", Config{}, ""},
		{"valid permission_mode", Config{PermissionMode: "strict"}, ""},
		{"permission_mode case-insensitive", Config{PermissionMode: "Auto"}, ""},
		{"bad permission_mode", Config{PermissionMode: "strikt"}, "permission_mode"},
		{"empty reasoning_effort is fine", Config{}, ""},
		{"valid reasoning_effort off", Config{ReasoningEffort: "off"}, ""},
		{"valid reasoning_effort xhigh", Config{ReasoningEffort: "xhigh"}, ""},
		{"bad reasoning_effort", Config{ReasoningEffort: "ultra"}, "reasoning_effort"},
		{"zero compact_auto_pct is the default", Config{CompactAutoPct: 0}, ""},
		{"in-range compact_auto_pct", Config{CompactAutoPct: 75}, ""},
		{"negative compact_auto_pct", Config{CompactAutoPct: -1}, "compact_auto_pct"},
		{"over-100 compact_auto_pct", Config{CompactAutoPct: 150}, "compact_auto_pct"},
		{"empty language is fine", Config{}, ""},
		{"valid language zh", Config{Language: "zh"}, ""},
		{"bad language", Config{Language: "fr"}, "language"},
		{"valid tool_search enabled off", Config{Tools: ToolsConfig{ToolSearch: ToolSearchConfig{Enabled: "off"}}}, ""},
		{"bad tool_search enabled", Config{Tools: ToolsConfig{ToolSearch: ToolSearchConfig{Enabled: "yes"}}}, "tool_search.enabled"},
		{"in-range tool_search threshold_pct", Config{Tools: ToolsConfig{ToolSearch: ToolSearchConfig{ThresholdPct: 10}}}, ""},
		{"over-100 tool_search threshold_pct", Config{Tools: ToolsConfig{ToolSearch: ToolSearchConfig{ThresholdPct: 200}}}, "threshold_pct"},
		{"empty memory_backend is fine", Config{}, ""},
		{"valid memory_backend", Config{MemoryBackend: MemoryBackendConfig{Type: "hindsight", BaseURL: "http://localhost:8888"}}, ""},
		{"bad memory_backend type", Config{MemoryBackend: MemoryBackendConfig{Type: "pinecone", BaseURL: "http://x"}}, "memory_backend.type"},
		{"memory_backend missing base_url", Config{MemoryBackend: MemoryBackendConfig{Type: "mem0"}}, "base_url"},
		{"mem0 cloud mode needs no base_url", Config{MemoryBackend: MemoryBackendConfig{Type: "mem0", Mode: "cloud"}}, ""},
		{"hindsight cloud mode needs no base_url", Config{MemoryBackend: MemoryBackendConfig{Type: "hindsight", Mode: "cloud"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probs := tt.cfg.Validate()
			joined := strings.Join(probs, "; ")
			if tt.want == "" {
				if len(probs) != 0 {
					t.Errorf("want no problems, got %v", probs)
				}
				return
			}
			if !strings.Contains(joined, tt.want) {
				t.Errorf("want a problem containing %q, got %v", tt.want, probs)
			}
		})
	}
}

// TestResolveVisionHelper covers the vision_helper resolver. The critical case
// is a model name that isn't in the config at all: ModelVision would answer
// through the ModelSupportsVision heuristic (which errs toward true), so the
// resolver must go through EntryByModel and report false instead.
func TestResolveVisionHelper(t *testing.T) {
	cfg := Config{Endpoints: []Endpoint{
		{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{
			{Model: "claude-sonnet-4-6", Vision: true},
			{Model: "text-only-model", Vision: false},
		}},
		{ID: "ep-b", Provider: "custom", BaseURL: "http://x", Protocol: "openai", Models: []EndpointModel{
			{Model: "qwen-vl-max", Vision: true},
		}},
	}}

	tests := []struct {
		name         string
		helper       string
		wantOK       bool
		wantProvider string
	}{
		{"empty is disabled", "", false, ""},
		{"whitespace is disabled", "   ", false, ""},
		{"composite id to vision model", "ep-b::qwen-vl-max", true, "custom"},
		{"bare name to vision model", "qwen-vl-max", true, "custom"},
		{"composite id to text-only model", "ep-a::text-only-model", false, ""},
		{"bare name of text-only model", "text-only-model", false, ""},
		{"unknown endpoint", "ghost::qwen-vl-max", false, ""},
		{"unknown model name is not rescued by the heuristic", "gpt-4o", false, ""},
		{"typo'd model name is not rescued by the heuristic", "claude-sonnet-4-6-typo", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cfg
			c.VisionHelper = tt.helper
			entry, ok := c.ResolveVisionHelper()
			if ok != tt.wantOK {
				t.Fatalf("ResolveVisionHelper() ok = %v, want %v (entry %+v)", ok, tt.wantOK, entry)
			}
			if ok && entry.Provider != tt.wantProvider {
				t.Errorf("provider = %q, want %q", entry.Provider, tt.wantProvider)
			}
			if ok && !entry.Vision {
				t.Error("resolved entry must carry Vision: true")
			}
		})
	}
}

// TestConfigValidate_VisionHelper checks the validation message paths. A
// dangling or text-only vision_helper is reported (with the available vision
// models listed) rather than silently disabling the feature.
func TestConfigValidate_VisionHelper(t *testing.T) {
	eps := []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{
		{Model: "claude-sonnet-4-6", Vision: true},
		{Model: "qwen-plus", Vision: false},
	}}}
	textOnly := []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "qwen-plus", Vision: false}}}}

	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"unset is fine", Config{Endpoints: eps}, ""},
		{"resolves to a vision model", Config{Endpoints: eps, VisionHelper: "ep-a::claude-sonnet-4-6"}, ""},
		{"bare name resolves", Config{Endpoints: eps, VisionHelper: "claude-sonnet-4-6"}, ""},
		{"dangling reference", Config{Endpoints: eps, VisionHelper: "ghost::model"}, "vision_helper"},
		{"points at a text-only model", Config{Endpoints: eps, VisionHelper: "ep-a::qwen-plus"}, "vision_helper"},
		{"message lists available vision models", Config{Endpoints: eps, VisionHelper: "ghost::model"}, "ep-a::claude-sonnet-4-6"},
		{"message says so when none are configured", Config{Endpoints: textOnly, VisionHelper: "ghost::model"}, "no vision-capable model is configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probs := tt.cfg.Validate()
			joined := strings.Join(probs, "; ")
			if tt.want == "" {
				if len(probs) != 0 {
					t.Errorf("want no problems, got %v", probs)
				}
				return
			}
			if !strings.Contains(joined, tt.want) {
				t.Errorf("want a problem containing %q, got %v", tt.want, probs)
			}
		})
	}
}

// TestRenameEndpointRewritesVisionHelper pins that renaming an endpoint carries
// the vision_helper composite id along, the same as Default and Lite. Without
// it a rename silently disables the feature.
func TestRenameEndpointRewritesVisionHelper(t *testing.T) {
	c := &Config{
		Endpoints:    []Endpoint{{ID: "old", Provider: "anthropic", Models: []EndpointModel{{Model: "m", Vision: true}}}},
		VisionHelper: "old::m",
	}
	if err := c.RenameEndpoint("old", "new"); err != nil {
		t.Fatalf("RenameEndpoint: %v", err)
	}
	if c.VisionHelper != "new::m" {
		t.Errorf("VisionHelper = %q, want %q", c.VisionHelper, "new::m")
	}
	if _, ok := c.ResolveVisionHelper(); !ok {
		t.Error("vision helper should still resolve after the rename")
	}
}
