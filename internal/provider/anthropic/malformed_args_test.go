package anthropic

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

// The endpoint reports stop_reason tool_use but the accumulated input_json_delta
// text stops mid-object — an incomplete call that must reach the agent as a
// block carrying the parse failure, with a non-nil Input so the follow-up
// request never serializes `"input": null` (which this protocol rejects).
const incompleteToolInputStream = "" +
	"event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"m","model":"x","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"edit_file","input":{}}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\",\"old_string\":"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":0,"output_tokens":3}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

func TestSendStream_IncompleteToolInputSurfacesOnBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, incompleteToolInputStream)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c, _ := New("test-key")
	c.BaseURL = srv.URL
	resp, err := c.SendStream(context.Background(), provider.Request{
		Model: "x", Messages: []agent.Message{agent.NewUserMessage("edit it")},
	}, provider.StreamCallbacks{})
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	for _, b := range resp.Blocks {
		if b.Type != "tool_use" {
			continue
		}
		if b.InputError == "" || !strings.Contains(b.InputError, "incomplete") {
			t.Errorf("InputError = %q, want an incomplete-arguments message", b.InputError)
		}
		if b.Input == nil || len(b.Input) != 0 {
			t.Errorf("Input must be an empty non-nil map, got %v (nil=%v)", b.Input, b.Input == nil)
		}
		return
	}
	t.Fatal("no tool_use block in the reply")
}
