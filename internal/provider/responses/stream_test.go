package responses

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
)

// sse renders events as an SSE body, each with an event: line like the
// official API sends (dispatch must not depend on it).
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("event: x\ndata: ")
		b.WriteString(e)
		b.WriteString("\n\n")
	}
	return b.String()
}

func streamClient(t *testing.T, body string) *Client {
	t.Helper()
	c, _ := newTestClient(t, func(w http.ResponseWriter, req map[string]any) {
		if req["stream"] != true {
			t.Errorf("stream = %v, want true", req["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	})
	return c
}

var streamReq = provider.Request{Model: "gpt-6-luna", Messages: []agent.Message{agent.NewUserMessage("hi")}}

const completedText = `{"type":"response.completed","response":{"model":"gpt-6-luna","status":"completed","output":[],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":4},"output_tokens":2}}}`

func TestSendStream_Text(t *testing.T) {
	for name, tail := range map[string]string{
		"eof after completed": "",
		"done sentinel":       "data: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			body := sse(
				`{"type":"response.created","response":{"status":"in_progress"}}`,
				`{"type":"response.output_text.delta","item_id":"msg_1","delta":"hel"}`,
				`{"type":"response.output_text.delta","item_id":"msg_1","delta":"lo"}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"hello"}]}}`,
				completedText,
			) + tail
			var chunks []string
			resp, err := streamClient(t, body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{
				OnText: func(s string) { chunks = append(chunks, s) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(chunks, "|") != "hel|lo" {
				t.Errorf("chunks = %v", chunks)
			}
			if resp.Content != "hello" || resp.StopReason != "end_turn" {
				t.Errorf("resp = %+v", resp)
			}
			if resp.InputTokens != 6 || resp.CacheReadTokens != 4 || resp.OutputTokens != 2 {
				t.Errorf("usage = %+v", resp)
			}
		})
	}
}

func TestSendStream_ToolCallFragments(t *testing.T) {
	body := sse(
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_1","delta":"think"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC-FULL"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"pa"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"th\":\"a.go\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	)
	var deltas []string
	var thinking strings.Builder
	resp, err := streamClient(t, body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{
		OnToolDelta: func(id, name, frag string) {
			if id != "call_1" || name != "read_file" {
				t.Errorf("tool delta id/name = %q/%q", id, name)
			}
			deltas = append(deltas, frag)
		},
		OnThinking: func(s string) { thinking.WriteString(s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 2 || thinking.String() != "think" {
		t.Errorf("deltas = %v thinking = %q", deltas, thinking.String())
	}
	if resp.StopReason != "tool_use" || len(resp.Blocks) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	th := resp.Blocks[0]
	if th.ID != "rs_1" || th.Signature != "ENC-FULL" || th.Thinking != "think" || th.Model != "gpt-6-luna" {
		t.Errorf("thinking block = %+v", th)
	}
	if tu := resp.Blocks[1]; tu.ID != "call_1" || tu.Input["path"] != "a.go" {
		t.Errorf("tool_use block = %+v", tu)
	}
}

// A backend that sends only deltas — no output_item.done, empty output on the
// terminal event — still yields the text and tool call.
func TestSendStream_SynthesizesFromDeltas(t *testing.T) {
	body := sse(
		`{"type":"response.output_text.delta","item_id":"m","delta":"ok"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"x"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{}"}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	)
	resp, err := streamClient(t, body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || resp.StopReason != "tool_use" || len(resp.Blocks) != 2 || resp.Blocks[1].ID != "call_1" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestSendStream_Incomplete(t *testing.T) {
	body := sse(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`)
	resp, err := streamClient(t, body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "max_tokens" {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestSendStream_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"error event":  {sse(`{"type":"error","code":"server_error","message":"boom"}`), "boom"},
		"failed event": {sse(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"bad"}}}`), "bad"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := streamClient(t, tc.body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSendStream_TruncatedIsTransient(t *testing.T) {
	body := sse(`{"type":"response.output_text.delta","item_id":"m","delta":"partial"}`)
	_, err := streamClient(t, body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{})
	var ts interface{ TransientStream() bool }
	if !errors.As(err, &ts) || !ts.TransientStream() {
		t.Fatalf("err = %v, want a transient stream error", err)
	}
}

// A backend that omits output_index must not lose items to collisions on 0.
func TestSendStream_DoneItemsWithoutOutputIndex(t *testing.T) {
	body := sse(
		`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"checking"}]}}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"x","arguments":"{}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	)
	resp, err := streamClient(t, body).SendStream(context.Background(), streamReq, provider.StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 2 || resp.Blocks[0].Text != "checking" || resp.Blocks[1].ID != "call_1" {
		t.Errorf("blocks = %+v, want text then tool_use", resp.Blocks)
	}
}
