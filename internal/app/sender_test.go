package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/provider"
)

func TestSender_PassesRequestThrough(t *testing.T) {
	fake := &mockProvider{reply: provider.Response{
		Content: "pong", Model: "m", StopReason: "end_turn",
	}}
	a := agent.New(sender{p: fake}, "claude-haiku-4-5-20251001")
	reply, err := a.Turn(context.Background(), "ping")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Content != "pong" {
		t.Errorf("Content = %q, want pong", reply.Content)
	}
	if fake.gotReq.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("model passed through = %q", fake.gotReq.Model)
	}
	if len(fake.gotReq.Messages) != 1 || fake.gotReq.Messages[0].Content != "ping" {
		t.Errorf("messages passed through = %+v", fake.gotReq.Messages)
	}
}

// NewSender must derive a thinking budget from ReasoningEffort when no explicit
// ThinkingBudget is given (the server path), so Anthropic-protocol legacy models
// (Kimi-for-coding, older Claude) actually enable thinking. An explicit budget
// wins.
func TestNewSender_DerivesThinkingBudgetFromEffort(t *testing.T) {
	cases := []struct {
		name          string
		effort        string
		explicitBudg  int
		wantThinkBudg int
	}{
		{"effort max, no explicit budget", "max", 0, 64000},
		{"effort high, no explicit budget", "high", 0, 32768},
		{"effort off → no thinking", "", 0, 0},
		{"explicit budget wins over effort", "low", 50000, 50000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewSender(SenderOptions{
				Provider:        "anthropic",
				APIKey:          "test-key",
				ReasoningEffort: c.effort,
				ThinkingBudget:  c.explicitBudg,
			})
			if err != nil {
				t.Fatalf("NewSender: %v", err)
			}
			got := s.(sender).thinkingBudget
			if got != c.wantThinkBudg {
				t.Errorf("thinkingBudget = %d, want %d", got, c.wantThinkBudg)
			}
		})
	}
}

func TestSender_NilProvider(t *testing.T) {
	s := sender{p: nil}
	if _, err := s.SendMessages(context.Background(), "m", "", nil, 0); err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestSender_ProviderError_Surfaces(t *testing.T) {
	fake := &mockProvider{err: errors.New("upstream boom")}
	s := sender{p: fake}
	_, err := s.SendMessages(context.Background(), "m", "", []agent.Message{agent.NewUserMessage("hi")}, 0)
	if err == nil || !strings.Contains(err.Error(), "upstream boom") {
		t.Errorf("expected upstream error, got: %v", err)
	}
}

// mockProvider implements provider.Provider for tests.
type mockProvider struct {
	reply  provider.Response
	err    error
	gotReq provider.Request
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Send(_ context.Context, req provider.Request) (provider.Response, error) {
	m.gotReq = req
	return m.reply, m.err
}

// streamingMockProvider also implements provider.StreamingProvider so we can
// verify sender.StreamMessages picks the streaming path when the underlying
// provider supports it.
type streamingMockProvider struct {
	mockProvider
	deltas       []string
	thinkDeltas  []string
	streamReply  provider.Response
	streamCalled bool
	// onThinkingSet records whether the caller wired an OnThinking callback —
	// sender drops it when reasoning display is off.
	onThinkingSet bool
}

func (m *streamingMockProvider) SendStream(_ context.Context, req provider.Request, cb provider.StreamCallbacks) (provider.Response, error) {
	m.streamCalled = true
	m.gotReq = req
	m.onThinkingSet = cb.OnThinking != nil
	for _, d := range m.thinkDeltas {
		if cb.OnThinking != nil {
			cb.OnThinking(d)
		}
	}
	for _, d := range m.deltas {
		if cb.OnText != nil {
			cb.OnText(d)
		}
	}
	if m.err != nil {
		return provider.Response{}, m.err
	}
	return m.streamReply, nil
}

func TestSender_ReasoningSink_Gating(t *testing.T) {
	cases := []struct {
		name          string
		showReasoning bool
		onThinking    func(string)
		wantForwarded bool // whether the provider received a non-nil OnThinking
	}{
		{"on + handler → forwarded", true, func(string) {}, true},
		{"off → dropped", false, func(string) {}, false},
		{"on but nil handler → nil", true, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &streamingMockProvider{
				thinkDeltas: []string{"reasoning"},
				streamReply: provider.Response{Content: "ok", StopReason: "end_turn"},
			}
			s := sender{p: fake, showReasoning: c.showReasoning}
			_, err := s.StreamMessages(
				context.Background(), "m", "",
				[]agent.Message{agent.NewUserMessage("hi")}, 0,
				func(string) {},
				c.onThinking,
			)
			if err != nil {
				t.Fatal(err)
			}
			if fake.onThinkingSet != c.wantForwarded {
				t.Errorf("provider OnThinking set = %v, want %v", fake.onThinkingSet, c.wantForwarded)
			}
		})
	}
}

func TestSender_StreamingPathPreferred(t *testing.T) {
	fake := &streamingMockProvider{
		deltas:      []string{"hi ", "there"},
		streamReply: provider.Response{Content: "hi there", Model: "m", StopReason: "end_turn"},
	}
	s := sender{p: fake}

	var got []string
	reply, err := s.StreamMessages(
		context.Background(), "m", "",
		[]agent.Message{agent.NewUserMessage("hi")}, 0,
		func(d string) { got = append(got, d) },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !fake.streamCalled {
		t.Error("expected SendStream to be called when provider supports it")
	}
	if reply.Content != "hi there" {
		t.Errorf("Content = %q", reply.Content)
	}
	if len(got) != 2 || got[0] != "hi " || got[1] != "there" {
		t.Errorf("chunks = %v", got)
	}
}

func TestSender_StreamingFallback_NonStreamingProvider(t *testing.T) {
	// mockProvider only implements provider.Provider — not StreamingProvider.
	// sender.StreamMessages must fall back to Send and synthesise a single
	// onChunk call with the full content.
	fake := &mockProvider{reply: provider.Response{Content: "buffered"}}
	s := sender{p: fake}

	var got []string
	reply, err := s.StreamMessages(
		context.Background(), "m", "",
		[]agent.Message{agent.NewUserMessage("hi")}, 0,
		func(d string) { got = append(got, d) },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Content != "buffered" {
		t.Errorf("Content = %q", reply.Content)
	}
	if len(got) != 1 || got[0] != "buffered" {
		t.Errorf("fallback should emit one chunk with full content; got %v", got)
	}
}

func TestNewSender_UnknownProvider(t *testing.T) {
	if _, err := NewSender(SenderOptions{Provider: "nope", APIKey: "k"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestTestConnection_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", auth)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.Unmarshal(bodyBytes, &req)
		if req.Model != "gpt-4o-mini" {
			t.Errorf("model = %q, want gpt-4o-mini", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "hi" {
			t.Errorf("messages = %+v, want single user 'hi'", req.Messages)
		}
		if req.MaxTokens != 1 {
			t.Errorf("max_tokens = %d, want 1", req.MaxTokens)
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := TestConnection(ctx, ProviderOpenAI, "test-key", srv.URL, "gpt-4o-mini", ""); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
}

func TestTestConnection_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := TestConnection(ctx, ProviderOpenAI, "bad-key", srv.URL, "gpt-4o-mini", "")
	if err == nil {
		t.Fatal("expected error for bad key")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestTestConnection_InvalidModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := TestConnection(ctx, ProviderOpenAI, "test-key", srv.URL, "unknown-model", "")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestTestConnection_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response to trigger timeout
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := TestConnection(ctx, ProviderOpenAI, "test-key", srv.URL, "gpt-4o-mini", "")
	if err == nil {
		t.Fatal("expected error for timeout")
	}
}

func TestTestConnection_EmptyKey(t *testing.T) {
	ctx := context.Background()
	if err := TestConnection(ctx, ProviderOpenAI, "", "http://localhost", "x", ""); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestTestConnection_UnknownProvider(t *testing.T) {
	ctx := context.Background()
	if err := TestConnection(ctx, "nope", "k", "http://localhost", "x", ""); err == nil {
		t.Error("expected error for unknown provider")
	}
}
