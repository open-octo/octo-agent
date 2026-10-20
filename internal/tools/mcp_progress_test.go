package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/mcp"
)

func TestProgressLines(t *testing.T) {
	total := 4.0
	cases := []struct {
		name string
		p    mcp.ProgressParams
		want []string
	}{
		{"message only", mcp.ProgressParams{Progress: 7, Message: "booting kernel"}, []string{"booting kernel"}},
		{"message with total", mcp.ProgressParams{Progress: 1, Total: &total, Message: "downloading"}, []string{"downloading (1/4)"}},
		{"no message", mcp.ProgressParams{Progress: 2.5, Total: &total}, []string{"progress 2.5/4"}},
		{"no message, no total", mcp.ProgressParams{Progress: 3}, []string{"progress 3"}},
		{"multi-line output", mcp.ProgressParams{Progress: 1, Message: "line one\r\nline two\n"}, []string{"line one", "line two"}},
		{"multi-line with total", mcp.ProgressParams{Progress: 2, Total: &total, Message: "a\nb"}, []string{"a", "b (2/4)"}},
		{"carriage-return redraw", mcp.ProgressParams{Progress: 1, Message: "10%\r50%\r90%"}, []string{"90%"}},
		{"escape sequences", mcp.ProgressParams{Progress: 1, Message: "\x1b[31mred\x1b[0m \x1b]0;pwned\x07ok"}, []string{"red ok"}},
	}
	for _, c := range cases {
		if got := progressLines(c.p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	huge := strings.Repeat("line\n", 10*maxProgressLines) + "last"
	got := progressLines(mcp.ProgressParams{Progress: 1, Message: huge})
	if len(got) != maxProgressLines || got[len(got)-1] != "last" {
		t.Errorf("huge message: %d lines ending %q, want the last %d", len(got), got[len(got)-1], maxProgressLines)
	}
}

// TestExecuteStream_MCPProgressOverSSE is the end-to-end shape from #2643: a
// Streamable HTTP server answers tools/call with an SSE stream that carries
// progress notifications ahead of the result. The registry's ExecuteStream
// must hand them to the progress callback and still return the real result.
func TestExecuteStream_MCPProgressOverSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in mcp.Message
		_ = json.Unmarshal(body, &in)
		if in.ID == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		frame := func(m mcp.Message) string {
			b, _ := json.Marshal(m)
			return "data: " + string(b) + "\n\n"
		}
		result := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
		switch in.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: result(mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.Implementation{Name: "vm"},
			})})
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: result(map[string]any{
				"tools": []mcp.Tool{{Name: "run", InputSchema: json.RawMessage(`{"type":"object"}`)}},
			})})
		case "tools/call":
			var p struct {
				Meta struct {
					ProgressToken string `json:"progressToken"`
				} `json:"_meta"`
			}
			_ = json.Unmarshal(in.Params, &p)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for i, line := range []string{"Starting build", "Compiling 3 files"} {
				_, _ = fmt.Fprint(w, frame(mcp.Message{JSONRPC: "2.0", Method: mcp.MethodProgress, Params: result(map[string]any{
					"progressToken": p.Meta.ProgressToken, "progress": i + 1, "message": line,
				})}))
				flusher.Flush()
			}
			_, _ = fmt.Fprint(w, frame(mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: result(mcp.CallToolResult{
				Content: []mcp.Content{{Type: "text", Text: "exit 0"}},
			})}))
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: result(map[string]any{})})
		}
	}))
	defer srv.Close()

	reg := mcp.NewRegistry()
	defer reg.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.Connect(ctx, "vm", mcp.ServerEntry{URL: srv.URL}, mcp.Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	SetMCPRegistry(reg)
	defer SetMCPRegistry(nil)

	var chunks []string
	out, err := DefaultRegistry{}.ExecuteStream(ctx, "mcp__vm__run", map[string]any{}, func(c string) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if out.Text != "exit 0" {
		t.Errorf("result = %q, want the tool's final output", out.Text)
	}
	if want := []string{"Starting build", "Compiling 3 files"}; !reflect.DeepEqual(chunks, want) {
		t.Errorf("progress chunks = %q, want %q", chunks, want)
	}

	// The mcp_call bridge (deferred MCP tools) must stream the same way.
	chunks = nil
	if _, err := (DefaultRegistry{}).ExecuteStream(ctx, toolCallName, map[string]any{"name": "mcp__vm__run"}, func(c string) { chunks = append(chunks, c) }); err != nil {
		t.Fatalf("mcp_call: %v", err)
	}
	if len(chunks) != 2 {
		t.Errorf("mcp_call progress chunks = %q, want 2", chunks)
	}
}
