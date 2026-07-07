package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeMCPServer speaks just enough JSON-RPC-over-HTTP for connectOne: the
// initialize handshake, the initialized notification, and the three surface
// listings.
func fakeMCPServer(t *testing.T, toolNames ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in Message
		_ = json.Unmarshal(body, &in)
		if in.ID == nil { // notification — 204 per the Streamable HTTP spec
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var result any
		switch in.Method {
		case "initialize":
			result = InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "fake", Version: "0"},
			}
		case "tools/list":
			tools := make([]Tool, 0, len(toolNames))
			for _, n := range toolNames {
				tools = append(tools, Tool{Name: n, Description: n, InputSchema: json.RawMessage(`{"type":"object"}`)})
			}
			result = map[string]any{"tools": tools}
		case "resources/list":
			result = map[string]any{"resources": []Resource{}}
		case "prompts/list":
			result = map[string]any{"prompts": []Prompt{}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{JSONRPC: "2.0", ID: in.ID, Result: raw})
	}))
}

func TestRegistryConnect_AddAndReplace(t *testing.T) {
	srv := fakeMCPServer(t, "ping", "pong")
	defer srv.Close()

	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Connect(ctx, "fake", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	conn := r.Get("fake")
	if conn == nil {
		t.Fatal("connection not installed")
	}
	if len(conn.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(conn.Tools))
	}
	if _, ok := r.ConnectError("fake"); ok {
		t.Error("successful connect must not leave an error")
	}

	// Reconnect under the same name replaces the connection.
	if err := r.Connect(ctx, "fake", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
}

func TestRegistryConnect_FailureRecordsError(t *testing.T) {
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Nothing listens here; the dial fails fast.
	err := r.Connect(ctx, "dead", ServerEntry{URL: "http://127.0.0.1:1"}, Implementation{Name: "test"}, nil, nil)
	if err == nil {
		t.Fatal("expected connect error")
	}
	if msg, ok := r.ConnectError("dead"); !ok || msg == "" {
		t.Fatalf("ConnectError = (%q, %v), want recorded error", msg, ok)
	}
	if r.Get("dead") != nil {
		t.Error("failed connect must not leave a connection")
	}

	// A later successful connect clears the recorded error.
	srv := fakeMCPServer(t)
	defer srv.Close()
	if err := r.Connect(ctx, "dead", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("recovery connect: %v", err)
	}
	if _, ok := r.ConnectError("dead"); ok {
		t.Error("recovered connect must clear the error")
	}
}

func TestRegistryRemove(t *testing.T) {
	srv := fakeMCPServer(t, "ping")
	defer srv.Close()

	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Connect(ctx, "fake", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	r.Remove("fake")
	if r.Get("fake") != nil || r.Len() != 0 {
		t.Error("Remove must drop the connection")
	}
	r.Remove("fake") // unknown name: no-op, no panic
}

func TestConnection_ReauthRequired_RoundTrip(t *testing.T) {
	srv := fakeMCPServer(t, "ping")
	defer srv.Close()

	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Connect(ctx, "fake", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	conn := r.Get("fake")
	if _, ok := conn.ReauthRequired(); ok {
		t.Fatal("freshly connected connection must not report reauth required")
	}

	conn.MarkReauthRequired(ErrReauthRequired)
	msg, ok := conn.ReauthRequired()
	if !ok || msg == "" {
		t.Fatalf("ReauthRequired = (%q, %v), want recorded error", msg, ok)
	}

	// A brand-new connection (as installed by a successful re-authorize)
	// starts clean regardless of what the old one recorded.
	if err := r.Connect(ctx, "fake", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if _, ok := r.Get("fake").ReauthRequired(); ok {
		t.Error("a fresh connection replacing a flagged one must not inherit the flag")
	}
}

func TestRegistryConnect_AfterCloseRejected(t *testing.T) {
	srv := fakeMCPServer(t)
	defer srv.Close()

	r := NewRegistry()
	r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Connect(ctx, "late", ServerEntry{URL: srv.URL}, Implementation{Name: "test"}, nil, nil); err == nil {
		t.Fatal("Connect on a closed registry must error")
	}
}

func TestConnectAll_RecordsPerServerErrors(t *testing.T) {
	srv := fakeMCPServer(t, "ok-tool")
	defer srv.Close()

	cfg := &Config{Servers: map[string]ServerEntry{
		"good": {URL: srv.URL},
		"bad":  {URL: "http://127.0.0.1:1"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	r := ConnectAll(ctx, cfg, Implementation{Name: "test"}, nil, io.Discard, nil)
	defer r.Close()

	if r.Get("good") == nil {
		t.Error("good server should connect")
	}
	if msg, ok := r.ConnectError("bad"); !ok || msg == "" {
		t.Error("bad server's connect error should be recorded")
	}
}
