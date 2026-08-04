package mcp

import (
	"encoding/json"
	"testing"
)

func TestMessage_IsRequest(t *testing.T) {
	m := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if !m.IsRequest() {
		t.Error("expected IsRequest=true")
	}
	if m.IsNotification() {
		t.Error("expected IsNotification=false")
	}
	if m.IsResponse() {
		t.Error("expected IsResponse=false")
	}
}

func TestMessage_IsNotification(t *testing.T) {
	m := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if !m.IsNotification() {
		t.Error("expected IsNotification=true")
	}
	if m.IsRequest() {
		t.Error("expected IsRequest=false")
	}
	if m.IsResponse() {
		t.Error("expected IsResponse=false")
	}
}

func TestMessage_IsResponse(t *testing.T) {
	m := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)}
	if !m.IsResponse() {
		t.Error("expected IsResponse=true")
	}
	if m.IsRequest() {
		t.Error("expected IsRequest=false")
	}
}

func TestMessage_MarshalShape(t *testing.T) {
	// Request: id + method present, result/error/params optional.
	m := &Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"echo"}`),
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo"}}`
	if string(b) != want {
		t.Errorf("marshal:\n  got  %s\n  want %s", b, want)
	}
}

func TestRPCError_ErrorReturnsMessage(t *testing.T) {
	e := &RPCError{Code: -32601, Message: "Method not found"}
	if e.Error() != "Method not found" {
		t.Errorf("Error() = %q", e.Error())
	}
	var nilErr *RPCError
	if nilErr.Error() != "" {
		t.Errorf("nil RPCError.Error() should be empty")
	}
}

func TestProtocolVersion_IsSpecRevision(t *testing.T) {
	// Sanity guard against accidental version bumps: changing the requested
	// revision changes what the server is told we implement, so it should
	// never happen as a side effect of something else.
	//
	// 2025-03-26 is the revision that defines the transport in http.go
	// (Streamable HTTP, Mcp-Session-Id, DELETE, Last-Event-ID). It replaced
	// 2024-11-05's HTTP+SSE transport, which is why asking for 2024-11-05 here
	// was wrong: it named a revision with no Streamable HTTP.
	if ProtocolVersion != "2025-03-26" {
		t.Errorf("ProtocolVersion = %q, want 2025-03-26 — the revision whose transport this package implements", ProtocolVersion)
	}
	// Whatever it is, we must claim to support it ourselves.
	if !SupportedProtocolVersion(ProtocolVersion) {
		t.Errorf("ProtocolVersion %q is not in supportedProtocolVersions", ProtocolVersion)
	}
}

func TestSupportedProtocolVersion(t *testing.T) {
	for _, v := range []string{"2024-11-05", "2025-03-26"} {
		if !SupportedProtocolVersion(v) {
			t.Errorf("SupportedProtocolVersion(%q) = false, want true", v)
		}
	}
	// Empty means the server omitted it; assume the revision we asked for.
	if !SupportedProtocolVersion("") {
		t.Error("an omitted version should count as supported")
	}
	// Real revisions we deliberately don't claim: 2025-06-18 (elicitation,
	// structured output), 2025-11-25, and 2026-07-28, which replaces the
	// initialize handshake with per-request metadata entirely. See the
	// ProtocolVersion doc comment.
	for _, v := range []string{"2025-06-18", "2025-11-25", "2026-07-28", "2099-01-01", "garbage"} {
		if SupportedProtocolVersion(v) {
			t.Errorf("SupportedProtocolVersion(%q) = true, want false", v)
		}
	}
}
