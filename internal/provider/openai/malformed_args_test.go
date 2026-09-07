package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

// Two argument fragments concatenate to a JSON string holding a raw newline —
// the classic broken edit_file call. The adapter must keep the parse failure on
// the block instead of handing the tool a nil map.
const malformedArgsStream = "" +
	`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"edit_file","arguments":"{\"path\":\"a.go\",\"old_string\":\"x"}}]}}]}` + "\n\n" +
	`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\ny\"}"}}]}}]}` + "\n\n" +
	`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
	"data: [DONE]\n\n"

func TestSendStream_MalformedToolArgumentsSurfaceOnBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, malformedArgsStream)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL
	resp, err := c.SendStream(context.Background(), provider.Request{
		Model: "m", Messages: []agent.Message{agent.NewUserMessage("edit it")},
	}, provider.StreamCallbacks{})
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	assertMalformedToolUse(t, resp.Blocks, "near byte")
}

// The non-streaming path parses the same way.
func TestSend_MalformedToolArgumentsSurfaceOnBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"edit_file","arguments":"{\"path\":\"a.go\",\"old_string\":\"func main() {"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
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
	assertMalformedToolUse(t, resp.Blocks, "incomplete")
}

func assertMalformedToolUse(t *testing.T, blocks []agent.ContentBlock, wantHint string) {
	t.Helper()
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name != "edit_file" {
			continue
		}
		if b.InputError == "" {
			t.Fatalf("expected InputError on the malformed tool_use, got none (Input=%v)", b.Input)
		}
		if !strings.Contains(b.InputError, wantHint) {
			t.Errorf("InputError %q lacks %q", b.InputError, wantHint)
		}
		if b.Input == nil || len(b.Input) != 0 {
			t.Errorf("Input must be an empty non-nil map, got %v (nil=%v)", b.Input, b.Input == nil)
		}
		return
	}
	t.Fatal("no edit_file tool_use block in the reply")
}
