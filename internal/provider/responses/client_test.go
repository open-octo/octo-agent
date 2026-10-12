package responses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

// newTestClient points a client at a handler and records each request body.
func newTestClient(t *testing.T, h func(w http.ResponseWriter, body map[string]any)) (*Client, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		h(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL
	return c, &paths
}

func TestEndpointURL(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.openai.com", "https://api.openai.com/v1/responses"},
		{"https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1/responses"},
		{"https://opencode.ai/zen/go/v1/", "https://opencode.ai/zen/go/v1/responses"},
		{"", "https://api.openai.com/v1/responses"},
	} {
		c := &Client{BaseURL: tc.base}
		if got := c.endpointURL(); got != tc.want {
			t.Errorf("endpointURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestSend_RequestShapeAndUsage(t *testing.T) {
	var got map[string]any
	c, paths := newTestClient(t, func(w http.ResponseWriter, body map[string]any) {
		got = body
		_, _ = w.Write([]byte(`{
			"model":"gpt-6-luna","status":"completed",
			"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":10},"output_tokens":7}
		}`))
	})
	resp, err := c.Send(context.Background(), provider.Request{
		Model:        "gpt-6-luna",
		SystemPrompt: "be brief",
		Messages:     []agent.Message{agent.NewUserMessage("hi")},
		Tools:        []agent.ToolDefinition{{Name: "read_file", Description: "read", Parameters: map[string]any{"type": "object"}}},
		CacheKey:     "sess-1",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if (*paths)[0] != "/v1/responses" {
		t.Errorf("path = %q", (*paths)[0])
	}
	if got["instructions"] != "be brief" {
		t.Errorf("instructions = %v", got["instructions"])
	}
	if got["store"] != false {
		t.Errorf("store = %v, want false", got["store"])
	}
	if _, ok := got["include"]; ok {
		t.Errorf("include must not be sent: %v", got["include"])
	}
	if _, ok := got["reasoning"]; ok {
		t.Errorf("reasoning must be omitted with no effort: %v", got["reasoning"])
	}
	if got["prompt_cache_key"] != "sess-1" {
		t.Errorf("prompt_cache_key = %v", got["prompt_cache_key"])
	}
	if got["max_output_tokens"] != float64(DefaultMaxTokens) {
		t.Errorf("max_output_tokens = %v", got["max_output_tokens"])
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "read_file" || tool["strict"] != false {
		t.Errorf("tool = %v, want flat function tool with strict:false", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Errorf("tool must not nest under function: %v", tool)
	}
	input := got["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %v", input)
	}
	if m := input[0].(map[string]any); m["role"] != "user" || m["content"] != "hi" {
		t.Errorf("input[0] = %v", m)
	}

	if resp.Content != "hello" || resp.StopReason != "end_turn" || resp.Model != "gpt-6-luna" {
		t.Errorf("resp = %+v", resp)
	}
	// input_tokens includes the cache buckets; InputTokens must not.
	if resp.InputTokens != 30 || resp.CacheReadTokens != 60 || resp.CacheWriteTokens != 10 || resp.OutputTokens != 7 {
		t.Errorf("usage = in %d read %d write %d out %d", resp.InputTokens, resp.CacheReadTokens, resp.CacheWriteTokens, resp.OutputTokens)
	}
}

func TestSend_ReasoningEffortAndMinTokens(t *testing.T) {
	var got map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, body map[string]any) {
		got = body
		_, _ = w.Write([]byte(`{"status":"completed","output":[]}`))
	})
	_, err := c.Send(context.Background(), provider.Request{
		Model:           "m",
		Messages:        []agent.Message{agent.NewUserMessage("hi")},
		MaxTokens:       1,
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["max_output_tokens"] != float64(minMaxOutputTokens) {
		t.Errorf("max_output_tokens = %v, want %d", got["max_output_tokens"], minMaxOutputTokens)
	}
	r, _ := got["reasoning"].(map[string]any)
	if r["effort"] != "high" {
		t.Errorf("reasoning = %v", got["reasoning"])
	}
	if _, ok := r["summary"]; ok {
		t.Errorf("reasoning.summary must not be requested: %v", r)
	}
}

func TestSend_ToolCallWithReasoning(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{
			"status":"completed",
			"output":[
				{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}"}
			]
		}`))
	})
	resp, err := c.Send(context.Background(), provider.Request{
		Model:    "gpt-6-luna",
		Messages: []agent.Message{agent.NewUserMessage("read a.go")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	if len(resp.Blocks) != 2 {
		t.Fatalf("blocks = %+v", resp.Blocks)
	}
	th := resp.Blocks[0]
	if th.Type != "thinking" || th.ID != "rs_1" || th.Signature != "ENC" || th.Model != "gpt-6-luna" {
		t.Errorf("thinking block = %+v", th)
	}
	tu := resp.Blocks[1]
	if tu.Type != "tool_use" || tu.ID != "call_1" || tu.Name != "read_file" || tu.Input["path"] != "a.go" {
		t.Errorf("tool_use block = %+v", tu)
	}
}

// A refusal part carries its text in "refusal", not "text"; it must still reach
// the user.
func TestSend_Refusal(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}`))
	})
	resp, err := c.Send(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "I can't help with that." {
		t.Errorf("Content = %q", resp.Content)
	}
}

func TestSend_IncompleteMaxTokens(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},
			"output":[{"type":"function_call","call_id":"c","name":"x","arguments":"{\"a\":"}]}`))
	})
	resp, err := c.Send(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "max_tokens" {
		t.Errorf("StopReason = %q, want max_tokens even with a (partial) tool call", resp.StopReason)
	}
}

func TestSend_Failed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"status":"failed","error":{"code":"invalid_prompt","message":"nope"},"output":[]}`))
	})
	_, err := c.Send(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestSend_HTTPError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"protocol not supported","param":null,"code":null}}`))
	})
	_, err := c.Send(context.Background(), provider.Request{Model: "m", Messages: []agent.Message{agent.NewUserMessage("hi")}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "protocol not supported") {
		t.Fatalf("err = %v", err)
	}
}
