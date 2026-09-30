package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestSetSessionHeader(t *testing.T) {
	stamped := agent.WithUpstreamSessionID(context.Background(), "sess-1")
	cases := []struct {
		name string
		ctx  context.Context
		url  string
		want string
	}{
		{"opencode go openai", stamped, "https://opencode.ai/zen/go/v1/chat/completions", "sess-1"},
		{"opencode go anthropic", stamped, "https://opencode.ai/zen/go/v1/messages", "sess-1"},
		{"opencode subdomain", stamped, "https://api.opencode.ai/v1/messages", "sess-1"},
		{"host case-insensitive", stamped, "https://OpenCode.AI/zen/go/v1/messages", "sess-1"},
		{"other vendor gets nothing", stamped, "https://api.deepseek.com/v1/chat/completions", ""},
		{"lookalike host gets nothing", stamped, "https://notopencode.ai/v1/messages", ""},
		{"unstamped ctx gets nothing", context.Background(), "https://opencode.ai/zen/go/v1/messages", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			SetSessionHeader(tc.ctx, h, tc.url)
			if got := h.Get(OpenCodeSessionHeader); got != tc.want {
				t.Errorf("%s = %q, want %q", OpenCodeSessionHeader, got, tc.want)
			}
		})
	}
}
