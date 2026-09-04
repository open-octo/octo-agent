package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

// A client built with an empty key (a keyless local server such as Ollama)
// must send no Authorization header at all — not "Bearer " with nothing
// after it, which some gateways reject as a malformed credential.
func TestEmptyKey_SendsNoAuthorizationHeader(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["Authorization"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c, err := New("")
	if err != nil {
		t.Fatalf("New(\"\") = %v, want a client (empty key is the caller's policy)", err)
	}
	c.BaseURL = srv.URL
	if _, err := c.Send(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sawHeader {
		t.Error("Authorization header present on a keyless request")
	}
}

func TestEmptyKey_StreamSendsNoAuthorizationHeader(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["Authorization"]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, canonicalOpenAIStream)
	}))
	defer srv.Close()

	c, _ := New("")
	c.BaseURL = srv.URL
	if _, err := c.SendStream(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, provider.StreamCallbacks{}); err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if sawHeader {
		t.Error("Authorization header present on a keyless stream request")
	}
}
