package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

const keylessMessageJSON = `{
	"id": "msg_01",
	"type": "message",
	"role": "assistant",
	"model": "m",
	"content": [{"type": "text", "text": "hi"}],
	"stop_reason": "end_turn",
	"usage": {"input_tokens": 1, "output_tokens": 1}
}`

// A client built with an empty key (a keyless self-hosted gateway) must send
// no x-api-key header at all rather than an empty one.
func TestEmptyKey_SendsNoAPIKeyHeader(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["X-Api-Key"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(keylessMessageJSON))
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
		t.Error("x-api-key header present on a keyless request")
	}
}

func TestEmptyKey_StreamSendsNoAPIKeyHeader(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["X-Api-Key"]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, canonicalStream)
	}))
	defer srv.Close()

	c, _ := New("")
	c.BaseURL = srv.URL
	if _, err := c.SendStream(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, provider.StreamCallbacks{}); err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if sawHeader {
		t.Error("x-api-key header present on a keyless stream request")
	}
}
