package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVendorKeyOptional(t *testing.T) {
	if !VendorKeyOptional(ProviderCustom) {
		t.Error("custom vendor should not require an API key")
	}
	for _, id := range []string{ProviderOpenAI, "anthropic", "deepseek", "nope"} {
		if VendorKeyOptional(id) {
			t.Errorf("VendorKeyOptional(%q) = true, want false", id)
		}
	}
}

// A named cloud vendor with no key must still fail at construction — with the
// env var named — so the CLI/server keep their setup guidance. Only the
// Custom vendor (local Ollama/vLLM) may be keyless.
func TestBuildClient_NamedVendorRequiresKey(t *testing.T) {
	_, err := NewSender(SenderOptions{Provider: ProviderOpenAI, APIKey: "  "})
	if err == nil {
		t.Fatal("expected error for a keyless named vendor")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("error should name the env var: %v", err)
	}
}

func TestTestConnection_CustomKeyless_OpenAIProtocol(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"qwen3-coder:30b","choices":[{"index":0,"message":{"role":"assistant","content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := TestConnection(ctx, ProviderCustom, "", srv.URL+"/v1", "qwen3-coder:30b", "openai"); err != nil {
		t.Fatalf("TestConnection keyless custom: %v", err)
	}
	if sawAuth {
		t.Error("Authorization header sent for a keyless custom endpoint")
	}
}

func TestTestConnection_CustomKeyless_AnthropicProtocol(t *testing.T) {
	var sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawKey = r.Header["X-Api-Key"]
		_, _ = w.Write([]byte(`{"id":"msg_01","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"!"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := TestConnection(ctx, ProviderCustom, "", srv.URL, "m", "anthropic"); err != nil {
		t.Fatalf("TestConnection keyless custom (anthropic): %v", err)
	}
	if sawKey {
		t.Error("x-api-key header sent for a keyless custom endpoint")
	}
}
