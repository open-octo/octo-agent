package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

// redirectTransport sends every request to target while leaving the request's
// URL (and so the host the client thinks it's talking to) untouched.
type redirectTransport struct{ target *url.URL }

func (rt redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rt.target.Scheme, rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestOpenCodeSessionHeader_SendAndStream(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(provider.OpenCodeSessionHeader))
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, canonicalOpenAIStream)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = "https://opencode.ai/zen/go/v1"
	c.HTTPClient = &http.Client{Transport: redirectTransport{target}}

	ctx := agent.WithUpstreamSessionID(context.Background(), "sess-1")
	req := provider.Request{Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}}
	if _, err := c.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := c.SendStream(ctx, req, provider.StreamCallbacks{}); err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if len(got) != 2 || got[0] != "sess-1" || got[1] != "sess-1" {
		t.Errorf("%s per request = %q, want both \"sess-1\"", provider.OpenCodeSessionHeader, got)
	}

	// A configured header of the same name (any casing) overrides it.
	got = nil
	c.Headers = map[string]string{"X-OpenCode-Session": "mine"}
	if _, err := c.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(got) != 1 || got[0] != "mine" {
		t.Errorf("%s with configured override = %q, want [\"mine\"]", provider.OpenCodeSessionHeader, got)
	}
}
