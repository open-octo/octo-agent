package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// A Custom endpoint tested with no key at all (local Ollama/vLLM) must reach
// the server rather than being refused with "no API key provided", and the
// probe must carry no Authorization header.
func TestHandleTestConfig_CustomKeyless(t *testing.T) {
	setTestHome(t)
	t.Setenv("CUSTOM_API_KEY", "")

	var sawAuth bool
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"qwen3-coder:30b","choices":[{"index":0,"message":{"role":"assistant","content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer mock.Close()

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	w := doJSON(t, srv, http.MethodPost, "/api/config/test",
		`{"model":"qwen3-coder:30b","base_url":"`+mock.URL+`/v1","provider":"custom"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true for a keyless custom endpoint; msg=%v", body["ok"], body["message"])
	}
	if sawAuth {
		t.Error("Authorization header sent on a keyless probe")
	}
}

// A named vendor still needs a key: the keyless allowance is Custom-only.
func TestHandleTestConfig_NamedVendorStillNeedsKey(t *testing.T) {
	setTestHome(t)
	t.Setenv("OPENAI_API_KEY", "")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	w := doJSON(t, srv, http.MethodPost, "/api/config/test",
		`{"model":"gpt-4o-mini","base_url":"http://127.0.0.1:9/v1","provider":"openai"}`)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false || body["message"] != "no API key provided" {
		t.Fatalf("body = %v, want ok=false / no API key provided", body)
	}
}

// A config whose only endpoint is a keyless Custom one is fully set up: the
// first-run flow must not park the user on the key_setup step.
func TestDetectOnboardPhase_CustomKeylessIsConfigured(t *testing.T) {
	setTestHome(t)
	t.Setenv("CUSTOM_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ollama", Provider: "custom", Protocol: "openai", BaseURL: "http://localhost:11434/v1",
				Models: []config.EndpointModel{{Model: "qwen3-coder:30b"}}},
		},
		Default: "ollama::qwen3-coder:30b",
	})
	if got := detectOnboardPhase(); got == "key_setup" {
		t.Fatalf("detectOnboardPhase = %q; a keyless custom endpoint has no key to set up", got)
	}
}

// senderForEntry (the /model switch and lite/vision helper path) builds a
// sender for a keyless Custom entry instead of failing with "no API key".
func TestSenderForEntry_CustomKeyless(t *testing.T) {
	setTestHome(t)
	t.Setenv("CUSTOM_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := senderForEntry(config.ModelEntry{
		Provider: "custom", Protocol: "openai", BaseURL: "http://localhost:11434/v1", Model: "qwen3-coder:30b",
	}); err != nil {
		t.Fatalf("senderForEntry keyless custom: %v", err)
	}
	if _, err := senderForEntry(config.ModelEntry{Provider: "openai", Model: "gpt-4o-mini"}); err == nil {
		t.Fatal("senderForEntry: a keyless named vendor must still error")
	}
}
