package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/mcp"
	"github.com/open-octo/octo-agent/internal/tools"

	"gopkg.in/yaml.v3"
)

// fakeMCPServerForHandlers speaks the minimal JSON-RPC-over-HTTP surface the
// connect path needs, advertising a single tool.
func fakeMCPServerForHandlers(t *testing.T) *httptest.Server {
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

// mcpTestHome points HOME at a temp dir and seeds ~/.octo/mcp.json.
func mcpTestHome(t *testing.T, mcpJSON string) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if mcpJSON != "" {
		if err := os.MkdirAll(filepath.Join(tmp, ".octo"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, ".octo", "mcp.json"), []byte(mcpJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tmp
}

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	return w
}

func decodeServers(t *testing.T, w *httptest.ResponseRecorder) []mcpServerInfo {
	t.Helper()
	var resp struct {
		Servers []mcpServerInfo `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	return resp.Servers
}

func TestHandleListMCPServers(t *testing.T) {
	mcpTestHome(t, `{"mcpServers": {
		"alpha": {"command": "echo"},
		"off":   {"url": "https://x.example", "disabled": true},
		"bad":   {}
	}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodGet, "/api/mcp/servers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	servers := decodeServers(t, w)
	if len(servers) != 3 {
		t.Fatalf("got %d servers, want 3", len(servers))
	}
	byName := map[string]mcpServerInfo{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if byName["alpha"].Status != "disconnected" || byName["alpha"].Transport != "stdio" {
		t.Errorf("alpha = %+v", byName["alpha"])
	}
	if byName["off"].Status != "disabled" || !byName["off"].Disabled {
		t.Errorf("off = %+v", byName["off"])
	}
	if byName["bad"].Status != "invalid" || byName["bad"].Invalid == "" {
		t.Errorf("bad = %+v", byName["bad"])
	}
}

func TestHandleCreateMCPServer_BulkImport(t *testing.T) {
	mcpTestHome(t, "")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers",
		`{"mcpServers": {"a": {"command": "npx", "args": ["-y", "server-a"]}, "b": {"url": "https://b.example"}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk: status = %d: %s", w.Code, w.Body.String())
	}
	if servers := decodeServers(t, w); len(servers) != 2 {
		t.Fatalf("servers = %+v", servers)
	}

	// A half-bad paste lands nothing.
	w = doJSON(t, srv, http.MethodPost, "/api/mcp/servers",
		`{"mcpServers": {"c": {"command": "npx"}, "bad name": {"command": "x"}}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("half-bad bulk: status = %d, want 400", w.Code)
	}
	w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers", "")
	for _, s := range decodeServers(t, w) {
		if s.Name == "c" {
			t.Error("validation must reject the whole paste, not half of it")
		}
	}
}

// Bulk import never accepts a non-allowlisted command — there's no per-call
// opt-in for it (unlike the removed single-add endpoint). Adding a server
// with an arbitrary command now only happens through the mcp-creator skill
// editing the config file directly.
func TestHandleCreateMCPServer_ArbitraryCommandRejected(t *testing.T) {
	mcpTestHome(t, "")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers",
		`{"mcpServers": {"custom": {"command": "echo"}}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Dangerous paths are always rejected too.
	for _, cmd := range []string{"/bin/echo", "../../bin/echo", "echo; rm -rf /"} {
		w = doJSON(t, srv, http.MethodPost, "/api/mcp/servers",
			`{"mcpServers": {"bad": {"command": "`+cmd+`"}}}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("command %q: expected 400, got %d", cmd, w.Code)
		}
	}
}

func TestHandleDeleteMCPServer(t *testing.T) {
	mcpTestHome(t, `{"mcpServers": {"a": {"command": "echo"}}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodDelete, "/api/mcp/servers/a", "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d: %s", w.Code, w.Body.String())
	}
	if servers := decodeServers(t, w); len(servers) != 0 {
		t.Fatalf("servers = %+v, want empty", servers)
	}

	w = doJSON(t, srv, http.MethodDelete, "/api/mcp/servers/a", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete again: status = %d, want 404", w.Code)
	}
}

func TestHandleToggleMCPServer(t *testing.T) {
	mcpTestHome(t, `{"mcpServers": {"a": {"command": "echo"}}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodPatch, "/api/mcp/servers/a/toggle", "")
	if w.Code != http.StatusOK {
		t.Fatalf("toggle: status = %d: %s", w.Code, w.Body.String())
	}
	if servers := decodeServers(t, w); !servers[0].Disabled || servers[0].Status != "disabled" {
		t.Fatalf("after disable: %+v", servers)
	}

	w = doJSON(t, srv, http.MethodPatch, "/api/mcp/servers/a/toggle", "")
	if servers := decodeServers(t, w); servers[0].Disabled {
		t.Fatalf("after re-enable: %+v", servers)
	}
}

func TestMCPHandlers_ProjectEntriesReadOnly(t *testing.T) {
	mcpTestHome(t, "")
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".octo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".octo", "mcp.json"),
		[]byte(`{"mcpServers": {"proj": {"command": "echo"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.cwd = proj

	for _, c := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/mcp/servers/proj", ""},
		{http.MethodPatch, "/api/mcp/servers/proj/toggle", ""},
	} {
		w := doJSON(t, srv, c.method, c.path, c.body)
		if w.Code != http.StatusConflict {
			t.Errorf("%s %s: status = %d, want 409", c.method, c.path, w.Code)
		}
	}
}

func TestMCPLifecycle_LiveConnect(t *testing.T) {
	mcpTestHome(t, "")
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)

	fake := fakeMCPServerForHandlers(t)
	defer fake.Close()

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	// Create with tools on → connects immediately.
	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers",
		`{"mcpServers": {"live": {"url": "`+fake.URL+`"}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: status = %d: %s", w.Code, w.Body.String())
	}
	servers := decodeServers(t, w)
	if servers[0].Status != "connected" || servers[0].Tools != 1 {
		t.Fatalf("after create: %+v", servers[0])
	}

	// Disable → connection dropped.
	w = doJSON(t, srv, http.MethodPatch, "/api/mcp/servers/live/toggle", "")
	if servers = decodeServers(t, w); servers[0].Status != "disabled" {
		t.Fatalf("after disable: %+v", servers[0])
	}
	if reg := tools.ActiveMCPRegistry(); reg != nil && reg.Get("live") != nil {
		t.Fatal("disable should disconnect the live server")
	}

	// Re-enable → reconnects.
	w = doJSON(t, srv, http.MethodPatch, "/api/mcp/servers/live/toggle", "")
	if servers = decodeServers(t, w); servers[0].Status != "connected" {
		t.Fatalf("after enable: %+v", servers[0])
	}

	// Reconnect endpoint works too.
	w = doJSON(t, srv, http.MethodPost, "/api/mcp/servers/live/reconnect", "")
	if servers = decodeServers(t, w); servers[0].Status != "connected" {
		t.Fatalf("after reconnect: %+v", servers[0])
	}

	// Reload from disk keeps it connected.
	w = doJSON(t, srv, http.MethodPost, "/api/mcp/reload", "")
	if w.Code != http.StatusOK {
		t.Fatalf("reload: status = %d: %s", w.Code, w.Body.String())
	}
	if servers = decodeServers(t, w); servers[0].Status != "connected" {
		t.Fatalf("after reload: %+v", servers[0])
	}

	// Delete → gone from config and registry.
	w = doJSON(t, srv, http.MethodDelete, "/api/mcp/servers/live", "")
	if servers = decodeServers(t, w); len(servers) != 0 {
		t.Fatalf("after delete: %+v", servers)
	}
	if reg := tools.ActiveMCPRegistry(); reg != nil && reg.Get("live") != nil {
		t.Fatal("delete should disconnect the live server")
	}
}

func TestMCPLiveConnect_FailureSurfacesAsStatus(t *testing.T) {
	mcpTestHome(t, "")
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	// Nothing listens on this port — the entry is saved, the status says why
	// it isn't connected, and the HTTP call still succeeds.
	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers",
		`{"mcpServers": {"dead": {"url": "http://127.0.0.1:1"}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: status = %d: %s", w.Code, w.Body.String())
	}
	servers := decodeServers(t, w)
	if servers[0].Status != "error" || servers[0].Error == "" {
		t.Fatalf("after failed connect: %+v", servers[0])
	}
}

func TestHandleGetMCPServer(t *testing.T) {
	mcpTestHome(t, `{"mcpServers": {
		"alpha": {"command": "echo"},
		"off":   {"url": "https://x.example", "disabled": true}
	}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, http.MethodGet, "/api/mcp/servers/alpha", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var detail mcpServerDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if detail.Name != "alpha" || detail.Status != "disconnected" || detail.Tools != 0 {
		t.Errorf("alpha detail = %+v", detail)
	}
	if len(detail.ToolList) != 0 {
		t.Errorf("disconnected server should have no tool list, got %d", len(detail.ToolList))
	}

	w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing: status = %d, want 404", w.Code)
	}
}

func TestToolSearchSettings(t *testing.T) {
	home := mcpTestHome(t, "")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// Defaults backfilled on GET.
	w := doJSON(t, srv, http.MethodGet, "/api/config/toolsearch", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d", w.Code)
	}
	var got toolSearchSettings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled != "auto" || got.ThresholdPct != 10 || got.SearchDefaultLimit != 5 || got.MaxSearchLimit != 20 {
		t.Fatalf("defaults = %+v", got)
	}

	// PUT persists and echoes.
	w = doJSON(t, srv, http.MethodPut, "/api/config/toolsearch", `{"enabled": "on"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put: status = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled != "on" {
		t.Fatalf("after put: %+v", got)
	}

	// Written to ~/.octo/config.yml.
	raw, err := os.ReadFile(filepath.Join(home, ".octo", "config.yml"))
	if err != nil {
		t.Fatalf("config.yml not written: %v", err)
	}
	var y struct {
		Tools struct {
			ToolSearch struct {
				Enabled string `yaml:"enabled"`
			} `yaml:"tool_search"`
		} `yaml:"tools"`
	}
	if err := yaml.Unmarshal(raw, &y); err != nil {
		t.Fatal(err)
	}
	if y.Tools.ToolSearch.Enabled != "on" {
		t.Fatalf("persisted enabled = %q, want on", y.Tools.ToolSearch.Enabled)
	}

	// Invalid value rejected.
	w = doJSON(t, srv, http.MethodPut, "/api/config/toolsearch", `{"enabled": "sometimes"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid put: status = %d, want 400", w.Code)
	}
}
