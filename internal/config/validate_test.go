package config

import (
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	good := ModelEntry{Provider: "openai", Model: "gpt-4o"}
	tests := []struct {
		name string
		cfg  Config
		want string // substring of an expected problem; "" = no problems
	}{
		{"empty pre-onboarding is valid", Config{}, ""},
		{"good", Config{Models: []ModelEntry{good}, DefaultModel: "gpt-4o"}, ""},
		{"floating default is valid", Config{Models: []ModelEntry{{Provider: "openai"}}}, ""},
		{"dangling default_model", Config{Models: []ModelEntry{good}, DefaultModel: "nope"}, "default_model"},
		{"dangling lite_model", Config{Models: []ModelEntry{good}, LiteModel: "nope"}, "lite_model"},
		{"missing provider", Config{Models: []ModelEntry{{Model: "gpt-4o"}}}, "no provider"},
		{"missing model name", Config{Models: []ModelEntry{good, {Provider: "openai"}}}, "no model name"},
		{"duplicate model", Config{Models: []ModelEntry{good, {Provider: "anthropic", Model: "gpt-4o"}}}, "duplicate"},
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
