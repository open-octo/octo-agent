package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/mcp"
	"github.com/Leihb/octo-agent/internal/tools"
)

// fakeMCPHTTPServer speaks the minimal JSON-RPC-over-HTTP surface the connect
// path needs (initialize, initialized notification, surface listings).
func fakeMCPHTTPServer(t *testing.T) *httptest.Server {
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
			result = map[string]any{"tools": []mcp.Tool{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
		case "resources/list":
			result = map[string]any{"resources": []mcp.Resource{}}
		case "prompts/list":
			result = map[string]any{"prompts": []mcp.Prompt{}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: raw})
	}))
}

func writeUserMCPConfig(t *testing.T, home, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".octo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".octo", "mcp.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSwapMCP_InstallsThenClears(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	defer ShutdownMCP()

	srv := fakeMCPHTTPServer(t)
	defer srv.Close()

	writeUserMCPConfig(t, home, `{"mcpServers": {"fake": {"url": "`+srv.URL+`"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := SwapMCP(ctx, t.TempDir(), io.Discard); err != nil {
		t.Fatalf("SwapMCP: %v", err)
	}
	reg := tools.ActiveMCPRegistry()
	if reg == nil || reg.Get("fake") == nil {
		t.Fatal("registry with fake server should be installed")
	}

	// A server that fails to connect must still leave the registry installed
	// (carrying the error) — that's what the management UI reads.
	writeUserMCPConfig(t, home, `{"mcpServers": {"dead": {"url": "http://127.0.0.1:1"}}}`)
	if err := SwapMCP(ctx, t.TempDir(), io.Discard); err != nil {
		t.Fatalf("SwapMCP: %v", err)
	}
	reg = tools.ActiveMCPRegistry()
	if reg == nil {
		t.Fatal("registry must stay installed when all servers fail")
	}
	if msg, ok := reg.ConnectError("dead"); !ok || msg == "" {
		t.Error("connect error should be recorded on the swapped registry")
	}

	// Zero servers configured clears the registry.
	writeUserMCPConfig(t, home, `{"mcpServers": {}}`)
	if err := SwapMCP(ctx, t.TempDir(), io.Discard); err != nil {
		t.Fatalf("SwapMCP: %v", err)
	}
	if tools.ActiveMCPRegistry() != nil {
		t.Error("empty config should clear the active registry")
	}
}

func TestSwapMCP_ConfigErrorKeepsOldRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	defer ShutdownMCP()

	srv := fakeMCPHTTPServer(t)
	defer srv.Close()

	writeUserMCPConfig(t, home, `{"mcpServers": {"fake": {"url": "`+srv.URL+`"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := SwapMCP(ctx, t.TempDir(), io.Discard); err != nil {
		t.Fatalf("SwapMCP: %v", err)
	}
	old := tools.ActiveMCPRegistry()

	writeUserMCPConfig(t, home, `not json`)
	if err := SwapMCP(ctx, t.TempDir(), io.Discard); err == nil {
		t.Fatal("expected config parse error")
	}
	if tools.ActiveMCPRegistry() != old {
		t.Error("config error must leave the old registry untouched")
	}
	if old.Get("fake") == nil {
		t.Error("old registry's connections must survive a failed swap")
	}
}

func TestConnectMCPServer_InstallsRegistryOnDemand(t *testing.T) {
	defer ShutdownMCP()
	tools.SetMCPRegistry(nil)

	srv := fakeMCPHTTPServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ConnectMCPServer(ctx, "first", mcp.ServerEntry{URL: srv.URL}, nil); err != nil {
		t.Fatalf("ConnectMCPServer: %v", err)
	}
	reg := tools.ActiveMCPRegistry()
	if reg == nil || reg.Get("first") == nil {
		t.Fatal("registry should be created and hold the connection")
	}

	DisconnectMCPServer("first")
	if reg.Get("first") != nil {
		t.Error("DisconnectMCPServer should drop the connection")
	}
	DisconnectMCPServer("never-existed") // no-op, no panic

	ShutdownMCP()
	if tools.ActiveMCPRegistry() != nil {
		t.Error("ShutdownMCP should clear the registry")
	}
}
