package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/provider"
)

// captureRequest stands up a server that records the decoded request body and
// returns a minimal valid chat.completion so Send succeeds.
func captureRequest(t *testing.T, dialect, effort string, stream bool) apiRequest {
	t.Helper()
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`+"\n\n")
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c, err := New("k")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL
	c.Dialect = dialect

	req := provider.Request{
		Model:           "deepseek-v4-pro",
		Messages:        []agent.Message{agent.NewUserMessage("hi")},
		ReasoningEffort: effort,
	}
	if stream {
		if _, err := c.SendStream(context.Background(), req, provider.StreamCallbacks{}); err != nil {
			t.Fatalf("SendStream: %v", err)
		}
	} else {
		if _, err := c.Send(context.Background(), req); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	return got
}

// DeepSeek separates enabling thinking from tuning its effort, so an effort
// must travel with thinking.type=enabled, and "off" must send
// thinking.type=disabled explicitly (DeepSeek leaves thinking on by default).
func TestDeepSeekDialect_ThinkingToggle(t *testing.T) {
	for _, stream := range []bool{false, true} {
		got := captureRequest(t, DialectDeepSeek, "high", stream)
		if got.Thinking == nil || got.Thinking.Type != "enabled" {
			t.Errorf("stream=%v: thinking = %+v, want type=enabled", stream, got.Thinking)
		}
		if got.ReasoningEffort != "high" {
			t.Errorf("stream=%v: reasoning_effort = %q, want high", stream, got.ReasoningEffort)
		}

		off := captureRequest(t, DialectDeepSeek, "", stream)
		if off.Thinking == nil || off.Thinking.Type != "disabled" {
			t.Errorf("stream=%v: off thinking = %+v, want type=disabled", stream, off.Thinking)
		}
		if off.ReasoningEffort != "" {
			t.Errorf("stream=%v: off reasoning_effort = %q, want empty", stream, off.ReasoningEffort)
		}
	}
}

// DeepSeek accepts "max" natively, so it must pass through verbatim with
// thinking enabled.
func TestDeepSeekDialect_MaxPassesThrough(t *testing.T) {
	got := captureRequest(t, DialectDeepSeek, "max", false)
	if got.ReasoningEffort != "max" {
		t.Errorf("reasoning_effort = %q, want max", got.ReasoningEffort)
	}
	if got.Thinking == nil || got.Thinking.Type != "enabled" {
		t.Errorf("thinking = %+v, want type=enabled", got.Thinking)
	}
}

// The OpenAI dialect maps "max" → "xhigh" (gpt-5.x's real top tier; it has no
// "max"), passes "xhigh" and lower through, and never sends the thinking toggle.
func TestOpenAIDialect_MaxMapsToXHigh(t *testing.T) {
	cases := map[string]string{"low": "low", "high": "high", "xhigh": "xhigh", "max": "xhigh"}
	for in, want := range cases {
		got := captureRequest(t, DialectOpenAI, in, false)
		if got.ReasoningEffort != want {
			t.Errorf("OpenAI effort %q → %q, want %q", in, got.ReasoningEffort, want)
		}
		if got.Thinking != nil {
			t.Errorf("OpenAI effort %q: thinking = %+v, want omitted", in, got.Thinking)
		}
	}
}

// A generic (unknown) backend must never see the thinking toggle, and tops out
// at "high" — both "xhigh" and "max" clamp down since it rejects unknown enums.
func TestGenericDialect_OmitsThinkingAndClampsHigh(t *testing.T) {
	got := captureRequest(t, "", "high", false)
	if got.Thinking != nil {
		t.Errorf("thinking = %+v, want omitted for generic OpenAI", got.Thinking)
	}
	if got.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort = %q, want high", got.ReasoningEffort)
	}

	// Both "xhigh" and "max" clamp down to "high" for generic backends.
	for _, in := range []string{"xhigh", "max"} {
		clamped := captureRequest(t, "", in, false)
		if clamped.ReasoningEffort != "high" {
			t.Errorf("%q reasoning_effort = %q, want clamped to high", in, clamped.ReasoningEffort)
		}
		if clamped.Thinking != nil {
			t.Errorf("%q thinking = %+v, want omitted", in, clamped.Thinking)
		}
	}

	// Empty effort on a generic backend omits both fields entirely.
	off := captureRequest(t, "", "", false)
	if off.Thinking != nil || off.ReasoningEffort != "" {
		t.Errorf("off: thinking=%+v effort=%q, want both omitted", off.Thinking, off.ReasoningEffort)
	}
}
