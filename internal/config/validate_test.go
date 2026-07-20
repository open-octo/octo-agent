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
