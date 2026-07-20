package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

func TestNew_EmptyKeyRejected(t *testing.T) {
	for _, k := range []string{"", "   ", "\t\n"} {
		if _, err := New(k); err == nil {
			t.Errorf("New(%q) expected error, got nil", k)
		}
	}
}

func TestSend_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Headers — OpenAI uses Bearer auth, NOT x-api-key
		if got, want := r.Header.Get("Authorization"), "Bearer test-key"; got != want {
			t.Errorf("Authorization header = %q, want %q", got, want)
		}
		if r.Header.Get("x-api-key") != "" {
			t.Errorf("x-api-key should NOT be set for OpenAI: %q", r.Header.Get("x-api-key"))
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		// Body
		bodyBytes, _ := io.ReadAll(r.Body)
		var req apiRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if req.Model != "gpt-4o-mini" {
			t.Errorf("model = %q, want gpt-4o-mini", req.Model)
		}

		// OpenAI carries system prompt INSIDE the messages array — opposite
		// direction from Anthropic. Verify the adapter prepended it.
		if len(req.Messages) != 2 {
			t.Fatalf("messages len = %d, want 2 (system + user)", len(req.Messages))
		}
		if req.Messages[0].Role != "system" || req.Messages[0].Content != "you are octo" {
			t.Errorf("first message = %+v, want {system, 'you are octo'}", req.Messages[0])
		}
		if req.Messages[1].Role != "user" || req.Messages[1].Content != "hello" {
			t.Errorf("second message = %+v, want {user, hello}", req.Messages[1])
		}
		// prompt_cache_key is OpenAI-proprietary and omitted for non-official
		// endpoints (this httptest server). See TestPromptCacheKey_GatedByBaseURL.
		if req.PromptCacheKey != "" {
			t.Errorf("prompt_cache_key = %q, want omitted for a non-official BaseURL", req.PromptCacheKey)
		}

		// Response
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"model":"gpt-4o-mini",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"hi there"},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}
		}`))
	}))
	defer srv.Close()

	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL

	resp, err := c.Send(context.Background(), provider.Request{
		Model:        "gpt-4o-mini",
		SystemPrompt: "you are octo",
		Messages:     []agent.Message{agent.NewUserMessage("hello")},
		CacheKey:     "octo-test-key",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if resp.Content != "hi there" {
		t.Errorf("Content = %q, want %q", resp.Content, "hi there")
	}
	if resp.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q", resp.Model)
	}
	if resp.StopReason != "stop" {
		t.Errorf("StopReason = %q, want stop", resp.StopReason)
	}
	if resp.InputTokens != 12 || resp.OutputTokens != 7 {
		t.Errorf("Usage = (%d, %d), want (12, 7)", resp.InputTokens, resp.OutputTokens)
	}
}

// TestSend_LargeResponseBody verifies that successful responses larger than the
// error-body cap are read in full, not truncated mid-JSON.
func TestSend_LargeResponseBody(t *testing.T) {
	longText := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := fmt.Sprintf(`{
			"id":"chatcmpl-large",
			"object":"chat.completion",
			"model":"gpt-4o-mini",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":%q},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":100,"total_tokens":110}
		}`, longText)
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()

	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL

	resp, err := c.Send(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []agent.Message{agent.NewUserMessage("generate a long response")},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Content != longText {
		t.Errorf("Content length = %d, want %d", len(resp.Content), len(longText))
	}
}

func TestSend_SystemMessageInArrayDeduped(t *testing.T) {
	// If the caller stuffs role:"system" into Request.Messages AND sets
	// Request.SystemPrompt, the adapter must use SystemPrompt as the canonical
	// source and drop the in-array system entry. Otherwise OpenAI would see
	// two system messages and the second would override the agent's intent.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var req apiRequest
		_ = json.Unmarshal(bodyBytes, &req)

		systemCount := 0
		for _, m := range req.Messages {
			if m.Role == "system" {
				systemCount++
			}
		}
		if systemCount != 1 {
			t.Errorf("system count = %d, want exactly 1", systemCount)
		}
		if req.Messages[0].Role != "system" || req.Messages[0].Content != "canonical" {
			t.Errorf("first message = %+v, want canonical system", req.Messages[0])
		}

		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	_, err := c.Send(context.Background(), provider.Request{
		Model:        "gpt-4o-mini",
		SystemPrompt: "canonical",
		Messages: []agent.Message{
			agent.NewSystemMessage("ignored-stuffed-system"),
			agent.NewUserMessage("hi"),
		},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_SystemOmittedWhenEmpty(t *testing.T) {
	// When SystemPrompt is empty, no system message should be added.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var req apiRequest
		_ = json.Unmarshal(bodyBytes, &req)
		for _, m := range req.Messages {
			if m.Role == "system" {
				t.Errorf("system role should not be present: %+v", m)
			}
		}
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL
	_, err := c.Send(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSend_HTTPError_Wrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{
			"error":{"message":"Incorrect API key","type":"invalid_request_error","code":"invalid_api_key"}
		}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	_, err := c.Send(context.Background(), provider.Request{
		Model:    "x",
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	})
	if err == nil {
		t.Fatal("Send: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("error should mention HTTP 401: %v", err)
	}
	if !strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("error should include upstream message: %v", err)
	}
}

func TestSend_ValidatesRequest(t *testing.T) {
	c, _ := New("k")
	c.BaseURL = "http://invalid"

	if _, err := c.Send(context.Background(), provider.Request{}); err == nil {
		t.Error("empty request should error")
	}
	if _, err := c.Send(context.Background(), provider.Request{Model: "x"}); err == nil {
		t.Error("missing messages should error")
	}
}

func TestSend_AppendsChatCompletionsPath(t *testing.T) {
	// Mirror of the Anthropic test: BaseURL with or without trailing slash
	// must still land at /v1/chat/completions.
	cases := []struct {
		name string
	}{{"no trailing slash"}, {"trailing slash"}}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"id":"m","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
			}))
			defer srv.Close()

			cl, _ := New("k")
			cl.BaseURL = srv.URL
			if i == 1 {
				cl.BaseURL = srv.URL + "/"
			}

			_, err := cl.Send(context.Background(), provider.Request{
				Model:    "x",
				Messages: []agent.Message{agent.NewUserMessage("hi")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if gotPath != "/v1/chat/completions" {
				t.Errorf("path = %q, want /v1/chat/completions", gotPath)
			}
		})
	}
}

func TestSend_BaseAlreadyHasV1_StripsDuplicate(t *testing.T) {
	// Bailian (Alibaba) and OpenAI itself bake "/v1" into the documented base
	// (e.g. "https://dashscope.aliyuncs.com/compatible-mode/v1"). The client
	// must detect the trailing "/v1" and only append "/chat/completions" so the
	// final URL is not ".../v1/v1/chat/completions". See issue #1625.
	cases := []struct {
		name string
		base string
	}{
		{"base ends with /v1", "https://coding.dashscope.aliyuncs.com/v1"},
		{"base ends with /v1/", "https://coding.dashscope.aliyuncs.com/v1/"},
		{"base ends with longer path + /v1", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, _ := New("k")
			cl.BaseURL = c.base
			got := cl.endpointURL()

			// Must NOT contain "/v1/v1".
			if strings.Contains(got, "/v1/v1") {
				t.Errorf("endpointURL() = %q, must not contain /v1/v1", got)
			}
			// Must end with exactly "/v1/chat/completions".
			if !strings.HasSuffix(got, "/v1/chat/completions") {
				t.Errorf("endpointURL() = %q, must end with /v1/chat/completions", got)
			}
		})
	}
}

func TestSend_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"x","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL
	_, err := c.Send(context.Background(), provider.Request{
		Model:    "x",
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	})
	if err == nil {
		t.Error("expected error when response has no choices")
	}
}

// TestPromptCacheKey_GatedByBaseURL verifies prompt_cache_key is sent only to
// the official OpenAI endpoint. Compatible gateways that proxy to Bedrock reject
// the unknown field with a 400, so it must be omitted for any custom BaseURL.
func TestPromptCacheKey_GatedByBaseURL(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiRequest
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		gotKey = req.PromptCacheKey
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	// Custom (compatible) BaseURL → key omitted.
	c, _ := New("k")
	c.BaseURL = srv.URL
	if _, err := c.Send(context.Background(), provider.Request{
		Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}, CacheKey: "should-be-dropped",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotKey != "" {
		t.Errorf("custom BaseURL: prompt_cache_key = %q, want omitted", gotKey)
	}

	// The gate itself: official endpoint keeps the key, custom drops it.
	official := &Client{BaseURL: DefaultBaseURL}
	if official.promptCacheKey("k1") != "k1" {
		t.Error("official endpoint should forward prompt_cache_key")
	}
	empty := &Client{BaseURL: ""}
	if empty.promptCacheKey("k2") != "k2" {
		t.Error("empty BaseURL (defaults to official) should forward prompt_cache_key")
	}
	custom := &Client{BaseURL: "https://gateway.example/v1"}
	if custom.promptCacheKey("k3") != "" {
		t.Error("custom BaseURL should drop prompt_cache_key")
	}
	// Mistral requires the key to cache at all, so it's on the allowlist.
	mistral := &Client{BaseURL: MistralBaseURL}
	if mistral.promptCacheKey("k4") != "k4" {
		t.Error("Mistral endpoint should forward prompt_cache_key")
	}
	mistralSlash := &Client{BaseURL: MistralBaseURL + "/"}
	if mistralSlash.promptCacheKey("k5") != "k5" {
		t.Error("Mistral endpoint with trailing slash should forward prompt_cache_key")
	}
}

// TestSend_ToolCallWithStopFinishReason verifies a response carrying tool calls
// is treated as a tool-use turn even when the backend reports finish_reason
// "stop" (as a gateway proxying Gemini does) rather than "tool_calls".
func TestSend_ToolCallWithStopFinishReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"edit_file","arguments":"{\"path\":\"a.go\"}"}}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL
	resp, err := c.Send(context.Background(), provider.Request{
		Model: "m", Messages: []agent.Message{agent.NewUserMessage("edit it")},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use (tool calls present despite finish_reason stop)", resp.StopReason)
	}
	gotTool := false
	for _, b := range resp.Blocks {
		if b.Type == "tool_use" && b.Name == "edit_file" {
			gotTool = true
		}
	}
	if !gotTool {
		t.Error("expected an edit_file tool_use block")
	}
}

// Compile-time assertion: *Client implements provider.Provider.
var _ provider.Provider = (*Client)(nil)
