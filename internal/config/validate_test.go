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
