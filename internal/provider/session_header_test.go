package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestSetSessionHeader(t *testing.T) {
	stamped := agent.WithUpstreamSessionID(context.Background(), "sess-1")
	hashed := opaqueSessionID("sess-1")
	cases := []struct {
		name string
		ctx  context.Context
		url  string
		want string
	}{
		{"opencode go openai", stamped, "https://opencode.ai/zen/go/v1/chat/completions", hashed},
		{"opencode go anthropic", stamped, "https://opencode.ai/zen/go/v1/messages", hashed},
		{"opencode subdomain", stamped, "https://api.opencode.ai/v1/messages", hashed},
		{"host case-insensitive", stamped, "https://OpenCode.AI/zen/go/v1/messages", hashed},
		{"other vendor gets nothing", stamped, "https://api.deepseek.com/v1/chat/completions", ""},
		{"lookalike host gets nothing", stamped, "https://notopencode.ai/v1/messages", ""},
		{"userinfo trick gets nothing", stamped, "https://opencode.ai@evil.com/v1/messages", ""},
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

// An IM session's ID embeds the platform chat and user IDs; none of it may
// reach the header, while the value stays stable per conversation.
func TestSetSessionHeader_HashesIdentifiers(t *testing.T) {
	const id = "im-feishu-oc_chat42-ou_user7-1a2b3c4d/agent-deadbeef"
	ctx := agent.WithUpstreamSessionID(context.Background(), id)
	get := func() string {
		h := http.Header{}
		SetSessionHeader(ctx, h, "https://opencode.ai/zen/go/v1/messages")
		return h.Get(OpenCodeSessionHeader)
	}
	got := get()
	for _, part := range []string{"feishu", "oc_chat42", "ou_user7"} {
		if strings.Contains(got, part) {
			t.Errorf("%s = %q leaks %q", OpenCodeSessionHeader, got, part)
		}
	}
	if got == "" || got != get() {
		t.Errorf("%s = %q, want a non-empty value stable across requests", OpenCodeSessionHeader, got)
	}
}
