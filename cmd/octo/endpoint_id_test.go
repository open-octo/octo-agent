package main

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// TestGenerateEndpointID verifies the human-readable endpoint id the wizard
// assigns for both named vendors and the Custom vendor, including the
// overwrite-reuse and suffix-on-conflict rules.
func TestGenerateEndpointID(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		baseURL  string
		existing []config.Endpoint
		want     string
	}{
		{
			name:     "named vendor, no existing endpoints",
			provider: "anthropic",
			baseURL:  "",
			existing: nil,
			want:     "anthropic",
		},
		{
			name:     "named vendor with base_url, empty existing",
			provider: "openai",
			baseURL:  "https://api.openai.com/v1",
			existing: nil,
			want:     "openai",
		},
		{
			name:     "custom vendor falls back to custom",
			provider: "custom",
			baseURL:  "https://relay.example.com",
			existing: nil,
			want:     "custom",
		},
		{
			name:     "empty provider falls back to custom",
			provider: "",
			baseURL:  "",
			existing: nil,
			want:     "custom",
		},
		{
			name:     "overwrite: same provider + base_url reuses existing id",
			provider: "anthropic",
			baseURL:  "",
			existing: []config.Endpoint{{ID: "anthropic", Provider: "anthropic", BaseURL: ""}},
			want:     "anthropic",
		},
		{
			name:     "overwrite: same provider + explicit base_url reuse",
			provider: "anthropic",
			baseURL:  "https://api.anthropic.com",
			existing: []config.Endpoint{{ID: "anthropic", Provider: "anthropic", BaseURL: "https://api.anthropic.com"}},
			want:     "anthropic",
		},
		{
			name:     "conflict: natural id taken by different provider gets suffix",
			provider: "anthropic",
			baseURL:  "https://relay.example.com",
			existing: []config.Endpoint{{ID: "anthropic", Provider: "openai", BaseURL: "https://api.openai.com"}},
			want:     "anthropic-1",
		},
		{
			name:     "conflict: natural id and -1 both taken",
			provider: "anthropic",
			baseURL:  "",
			existing: []config.Endpoint{
				{ID: "anthropic", Provider: "anthropic", BaseURL: "https://api.anthropic.com"},
				{ID: "anthropic-1", Provider: "anthropic", BaseURL: "https://relay1.example.com"},
			},
			want: "anthropic-2",
		},
		{
			name:     "custom conflict: custom taken gets custom-1",
			provider: "custom",
			baseURL:  "https://another.example.com",
			existing: []config.Endpoint{{ID: "custom", Provider: "custom", BaseURL: "https://relay.example.com"}},
			want:     "custom-1",
		},
		{
			name:     "different base_url same provider does not trigger overwrite",
			provider: "anthropic",
			baseURL:  "https://relay.example.com",
			existing: []config.Endpoint{{ID: "anthropic", Provider: "anthropic", BaseURL: ""}},
			want:     "anthropic-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateEndpointID(tt.provider, tt.baseURL, tt.existing)
			if got != tt.want {
				t.Errorf("generateEndpointID(%q, %q, ...) = %q, want %q", tt.provider, tt.baseURL, got, tt.want)
			}
		})
	}
}
