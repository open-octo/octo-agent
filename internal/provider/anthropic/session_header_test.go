package anthropic

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
			_, _ = io.WriteString(w, canonicalStream)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_01","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = "https://opencode.ai/zen/go"
	c.HTTPClient = &http.Client{Transport: redirectTransport{target}}

	ctx := agent.WithUpstreamSessionID(context.Background(), "sess-1")
	req := provider.Request{Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}}
	if _, err := c.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := c.SendStream(ctx, req, provider.StreamCallbacks{}); err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if len(got) != 2 || got[0] == "" || got[0] != got[1] {
		t.Errorf("%s per request = %q, want the same non-empty value on both", provider.OpenCodeSessionHeader, got)
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
