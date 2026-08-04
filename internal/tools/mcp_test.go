package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/mcp"
)

func TestMCPToolName_HappyPath(t *testing.T) {
	got := mcpToolName("filesystem", "read_file")
	if got != "mcp__filesystem__read_file" {
		t.Errorf("mcpToolName = %q", got)
	}
}

func TestSanitizeMCPName_StripsForbidden(t *testing.T) {
	cases := map[string]string{
		"simple":            "simple",
		"with-dash":         "with-dash",
		"with_underscore":   "with_underscore",
		"with.dot":          "with_dot",       // dot replaced
		"with:colon":        "with_colon",     // colon replaced
		"with space":        "with_space",     // space replaced
		"with/slash":        "with_slash",     // slash replaced
		"chars!@#$multiple": "chars_multiple", // consecutive forbidden collapse to one _
		"valid42":           "valid42",
	}
	for in, want := range cases {
		if got := sanitizeMCPName(in); got != want {
			t.Errorf("sanitizeMCPName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMCPName_RoundTrip(t *testing.T) {
	server, tool, ok := parseMCPName("mcp__filesystem__read_file")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if server != "filesystem" || tool != "read_file" {
		t.Errorf("got (%q, %q)", server, tool)
	}
}

func TestParseMCPName_ToolWithUnderscores(t *testing.T) {
	// SplitN(s, "__", 2) preserves further "__" inside the tool segment.
	server, tool, ok := parseMCPName("mcp__github__list__issues")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if server != "github" || tool != "list__issues" {
		t.Errorf("got (%q, %q)", server, tool)
	}
}

func TestParseMCPName_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"mcp__only-server",    // missing tool segment
		"mcp____empty-server", // empty server  (SplitN gives ["", "empty-server"], parts[0]==""→ok=false)
		"not-mcp__a__b",       // wrong prefix
		"",
	} {
		if _, _, ok := parseMCPName(bad); ok {
			t.Errorf("parseMCPName(%q) ok=true, want false", bad)
		}
	}
}

func TestFormatToolResult_MixedContent(t *testing.T) {
	// A real PNG signature: the image block is built by sniffing the bytes, so
	// an arbitrary payload is (correctly) refused no matter what the server
	// calls it — see TestFormatToolResult_UnsupportedImageFormat.
	imgBytes := []byte("\x89PNG\r\n\x1a\nIHDR-and-the-rest")
	r := &mcp.CallToolResult{Content: []mcp.Content{
		{Type: "text", Text: "first"},
		{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(imgBytes)},
		{Type: "resource", Resource: &mcp.EmbeddedResource{
			URI: "file:///x", MIMEType: "text/plain", Text: "embedded",
		}},
	}}
	out := formatToolResult(r)
	for _, want := range []string{
		"first",
		"[image: image/png",
		"[resource file:///x]",
		"embedded",
	} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("missing %q in:\n%s", want, out.Text)
		}
	}
	// The point of the change: the image reaches the model as a real block,
	// not just as the text summary above.
	if len(out.Blocks) != 1 {
		t.Fatalf("Blocks = %d, want 1 image block", len(out.Blocks))
	}
	blk := out.Blocks[0]
	if blk.Type != "image" || blk.Image == nil {
		t.Fatalf("block = %+v, want an image block with data", blk)
	}
	if string(blk.Image.Data) != string(imgBytes) {
		t.Errorf("block data = %q, want the decoded payload %q", blk.Image.Data, imgBytes)
	}
}

// TestFormatToolResult_UnsupportedImageFormat: an MCP server names its own
// media type and can name anything. A format the providers reject must degrade
// to a text line — building the block anyway fails the entire turn at the
// provider, which is worse than the placeholder this used to produce.
func TestFormatToolResult_UnsupportedImageFormat(t *testing.T) {
	for _, c := range []struct {
		name  string
		mime  string
		bytes []byte
	}{
		{"svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		{"bmp", "image/bmp", []byte("BM\x00\x00\x00\x00\x00\x00")},
		{"tiff", "image/tiff", []byte("II*\x00\x08\x00\x00\x00")},
		{"empty media type with junk bytes", "", []byte("nonsense")},
		{"png label, non-png bytes", "image/png", []byte("lying about this")},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := formatToolResult(&mcp.CallToolResult{Content: []mcp.Content{
				{Type: "image", MIMEType: c.mime, Data: base64.StdEncoding.EncodeToString(c.bytes)},
			}})
			if len(out.Blocks) != 0 {
				t.Errorf("Blocks = %d, want none for a format no provider accepts", len(out.Blocks))
			}
			if !strings.Contains(out.Text, "not supported") {
				t.Errorf("Text = %q, want it to say the format isn't supported", out.Text)
			}
		})
	}
}

// TestFormatToolResult_UndecodableImage: a server sending a payload that
// isn't valid base64 must not produce a broken image block — say so in text
// instead.
func TestFormatToolResult_UndecodableImage(t *testing.T) {
	r := &mcp.CallToolResult{Content: []mcp.Content{
		{Type: "image", MIMEType: "image/png", Data: "<not base64 …>"},
	}}
	out := formatToolResult(r)
	if len(out.Blocks) != 0 {
		t.Errorf("Blocks = %d, want none for an undecodable payload", len(out.Blocks))
	}
	if !strings.Contains(out.Text, "undecodable") {
		t.Errorf("Text = %q, want it to mention the undecodable payload", out.Text)
	}
}

// TestFormatToolResult_TextOnlyHasNoBlocks keeps the common case allocation-
// free of blocks, so text results look exactly as they did before.
func TestFormatToolResult_TextOnlyHasNoBlocks(t *testing.T) {
	out := formatToolResult(&mcp.CallToolResult{Content: []mcp.Content{
		{Type: "text", Text: "plain"},
	}})
	if out.Text != "plain" {
		t.Errorf("Text = %q, want plain", out.Text)
	}
	if out.Blocks != nil {
		t.Errorf("Blocks = %v, want nil for a text-only result", out.Blocks)
	}
}

func TestMCPToolDefs_NilRegistryYieldsNone(t *testing.T) {
	SetMCPRegistry(nil)
	defer SetMCPRegistry(nil)
	if defs := mcpToolDefs(); len(defs) != 0 {
		t.Errorf("expected empty defs with no registry, got %d", len(defs))
	}
}

func TestMarkIfReauthRequired_FlagsOnlyReauthErrors(t *testing.T) {
	conn := &mcp.Connection{Name: "fake"}

	markIfReauthRequired(conn, errors.New("some unrelated tool error"))
	if _, ok := conn.ReauthRequired(); ok {
		t.Error("a plain error must not flag the connection")
	}

	markIfReauthRequired(conn, mcp.ErrReauthRequired)
	msg, ok := conn.ReauthRequired()
	if !ok || msg == "" {
		t.Fatalf("ReauthRequired = (%q, %v), want recorded error", msg, ok)
	}

	// A properly %w-wrapped error (matching how http.go's doRequest reports
	// it: "mcp: oauth token: %w") must still be detected via errors.Is.
	conn2 := &mcp.Connection{Name: "fake2"}
	wrapped := fmt.Errorf("mcp: oauth token: %w", mcp.ErrReauthRequired)
	markIfReauthRequired(conn2, wrapped)
	if _, ok := conn2.ReauthRequired(); !ok {
		t.Error("a %w-wrapped ErrReauthRequired must flag the connection")
	}

	// A merely string-matching but non-wrapped error must NOT match —
	// guards against a naive strings.Contains-based implementation.
	conn3 := &mcp.Connection{Name: "fake3"}
	stringMatch := errors.New("mcp: oauth token: " + mcp.ErrReauthRequired.Error())
	markIfReauthRequired(conn3, stringMatch)
	if _, ok := conn3.ReauthRequired(); ok {
		t.Error("a string-matching but non-wrapped error must not flag the connection")
	}
}

// fakeMCPServerForExecuteMCP speaks just enough JSON-RPC-over-HTTP for
// executeMCP's default (tools/call) branch to succeed.
func fakeMCPServerForExecuteMCP(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in mcp.Message
		_ = json.Unmarshal(body, &in)
		if in.ID == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var result any
		switch in.Method {
		case "initialize":
			result = mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.Implementation{Name: "fake", Version: "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []mcp.Tool{{Name: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
		case "tools/call":
			result = mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: "pong"}}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: raw})
	}))
}

// TestExecuteMCP_SuccessfulCallClearsReauthFlag guards the self-heal half of
// the reauth-status fix: a connection previously flagged by
// MarkReauthRequired (e.g. from a transient refresh hiccup, not a
// genuinely revoked grant) must not be stuck reporting "error" forever —
// a subsequent successful call through the same *Connection has to clear
// the flag, or the panel would show a falsely unhealthy connection
// indefinitely even after the underlying issue resolved on its own.
func TestExecuteMCP_SuccessfulCallClearsReauthFlag(t *testing.T) {
	srv := fakeMCPServerForExecuteMCP(t)
	defer srv.Close()

	reg := mcp.NewRegistry()
	defer reg.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.Connect(ctx, "srv", mcp.ServerEntry{URL: srv.URL}, mcp.Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	SetMCPRegistry(reg)
	defer SetMCPRegistry(nil)

	conn := reg.Get("srv")
	conn.MarkReauthRequired(mcp.ErrReauthRequired)
	if _, ok := conn.ReauthRequired(); !ok {
		t.Fatal("setup: expected connection to be pre-flagged")
	}

	out, ok, err := executeMCP(ctx, "mcp__srv__ping", map[string]any{})
	if !ok {
		t.Fatal("expected executeMCP to recognize the mcp__ tool name")
	}
	if err != nil {
		t.Fatalf("executeMCP: %v (out=%q)", err, out.Text)
	}
	if _, ok := conn.ReauthRequired(); ok {
		t.Error("a successful call through the connection must clear the reauth flag")
	}
}
