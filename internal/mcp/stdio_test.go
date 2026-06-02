package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is the standard Go pattern for a self-spawning subprocess
// fixture: the test binary re-execs itself with GO_HELPER_PROCESS=1 and
// behaves like a mini MCP server in that mode. Lets us exercise the stdio
// transport end-to-end without shipping a separate binary or shell script.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)
	// Emit a marker to stderr at startup so a test can assert the transport
	// routes the child's stderr to the configured sink, not the terminal.
	if m := os.Getenv("GO_HELPER_STDERR"); m != "" {
		fmt.Fprint(os.Stderr, m)
	}
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	for {
		var req Message
		if err := dec.Decode(&req); err != nil {
			return
		}
		if req.IsNotification() {
			continue
		}
		switch req.Method {
		case MethodInitialize:
			result := InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "helper", Version: "0.1"},
			}
			b, _ := json.Marshal(result)
			_ = enc.Encode(Message{JSONRPC: "2.0", ID: req.ID, Result: b})
		case MethodToolsList:
			result := ListToolsResult{Tools: []Tool{
				{Name: "ping", Description: "responds pong"},
			}}
			b, _ := json.Marshal(result)
			_ = enc.Encode(Message{JSONRPC: "2.0", ID: req.ID, Result: b})
		case MethodToolsCall:
			result := CallToolResult{
				Content: []Content{{Type: "text", Text: "pong"}},
			}
			b, _ := json.Marshal(result)
			_ = enc.Encode(Message{JSONRPC: "2.0", ID: req.ID, Result: b})
		default:
			b, _ := json.Marshal(&RPCError{Code: -32601, Message: "not implemented"})
			_ = enc.Encode(Message{
				JSONRPC: "2.0", ID: req.ID,
				Error: &RPCError{Code: -32601, Message: "not implemented", Data: b},
			})
		}
	}
}

// helperCommand returns the StdioConfig that re-execs THIS test binary in
// helper-process mode. The "-test.run=TestHelperProcess" arg matches Go's
// convention; the GO_HELPER_PROCESS=1 env flag switches it from "run the
// real test" to "act as the helper".
func helperCommand() StdioConfig {
	return StdioConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess"},
		Env:     map[string]string{"GO_HELPER_PROCESS": "1"},
	}
}

// TestStdioTransport_StderrRoutedToConfig verifies the child's stderr goes to
// cfg.Stderr (a log file / discard under a TUI) instead of the terminal, so it
// can't corrupt a bubbletea frame.
func TestStdioTransport_StderrRoutedToConfig(t *testing.T) {
	if _, err := exec.LookPath(os.Args[0]); err != nil {
		t.Skip("self-binary not found; running outside a test binary")
	}
	const marker = "CHILD-STDERR-MARKER"
	cfg := helperCommand()
	cfg.Env["GO_HELPER_STDERR"] = marker
	var captured bytes.Buffer
	cfg.Stderr = &captured

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := NewStdioTransport(ctx, cfg)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	// Close stdin so the helper's decode loop exits; Close Wait()s the child,
	// which also flushes the exec stderr-copy goroutine into captured — so the
	// read below is race-free.
	if err := tx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := captured.String(); !strings.Contains(got, marker) {
		t.Errorf("child stderr not routed to cfg.Stderr; captured %q, want it to contain %q", got, marker)
	}
}

func TestStdioTransport_SubprocessRoundtrip(t *testing.T) {
	// Sanity: refuse to run if the helper isn't reachable through os.Args[0].
	if _, err := exec.LookPath(os.Args[0]); err != nil {
		t.Skip("self-binary not found; running outside a test binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := NewStdioTransport(ctx, helperCommand())
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer tx.Close()

	c := NewClient(tx, Implementation{Name: "octo-test", Version: "0.1"})
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if name := c.ServerInfo().Name; name != "helper" {
		t.Errorf("ServerInfo.Name = %q, want helper", name)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("ListTools = %v", tools)
	}
	res, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "pong" {
		t.Errorf("CallTool result = %+v", res)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestStdioTransport_EmptyCommandRejected(t *testing.T) {
	_, err := NewStdioTransport(context.Background(), StdioConfig{})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestStdioTransport_BadCommandSurfaces(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	// Random unlikely-to-exist command
	cfg := StdioConfig{Command: fmt.Sprintf("octo-this-binary-does-not-exist-%d", time.Now().UnixNano())}
	if _, err := NewStdioTransport(ctx, cfg); err == nil {
		t.Error("expected error for non-existent command")
	}
}
