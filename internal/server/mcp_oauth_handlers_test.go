package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/mcp"
	"github.com/open-octo/octo-agent/internal/tools"
)

// fakeOAuthMCPServer is an MCP endpoint behind an Authorization Code + PKCE
// OAuth gate: unauthenticated JSON-RPC posts get 401 + resource metadata,
// the OAuth discovery/registration/token endpoints all live on the same
// mux, and an authorized post gets real initialize/tools-list responses.
// There's no /authorize handler: the test plays the browser's role itself,
// extracting state/redirect_uri from the authorize_url the panel would
// otherwise open, and hitting the callback directly — see
// TestMCPOAuth_WebAuthCodeFlow_EndToEnd.
type fakeOAuthMCPServer struct {
	srv   *httptest.Server
	token string
}

func newFakeOAuthMCPServer(t *testing.T) *fakeOAuthMCPServer {
	t.Helper()
	f := &fakeOAuthMCPServer{token: "access-xyz"}
	mux := http.NewServeMux()

	writeJSONBody := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.Header().Set("WWW-Authenticate",
				`Bearer resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		var in mcp.Message
		_ = json.NewDecoder(r.Body).Decode(&in)
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
				ServerInfo:      mcp.Implementation{Name: "fake-oauth", Version: "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []mcp.Tool{{Name: "secure_ping", Description: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
		case "resources/list":
			result = map[string]any{"resources": []mcp.Resource{}}
		case "prompts/list":
			result = map[string]any{"prompts": []mcp.Prompt{}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		writeJSONBody(w, mcp.Message{JSONRPC: "2.0", ID: in.ID, Result: raw})
	})

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string]any{
			"resource":              f.srv.URL + "/mcp",
			"authorization_servers": []string{f.srv.URL + "/auth"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string]any{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint":         f.srv.URL + "/token",
			"registration_endpoint":  f.srv.URL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "client-1"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string]any{
			"access_token": f.token,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	f.srv = httptest.NewServer(mux)
	return f
}

func TestMCPOAuth_WebAuthCodeFlow_EndToEnd(t *testing.T) {
	fake := newFakeOAuthMCPServer(t)
	defer fake.srv.Close()

	home := mcpTestHome(t, `{"mcpServers": {"secure": {"url": "`+fake.srv.URL+`/mcp", "auth": "oauth"}}}`)
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers/secure/oauth/start", "")
	if w.Code != http.StatusOK {
		t.Fatalf("start: status = %d: %s", w.Code, w.Body.String())
	}

	// Poll until the flow surfaces an authorize_url — the panel would open
	// this in a new tab; the test plays the browser's role instead.
	var authorizeURL string
	deadline := time.Now().Add(5 * time.Second)
	for authorizeURL == "" {
		if time.Now().After(deadline) {
			t.Fatal("flow never reached authorizing with an authorize_url")
		}
		w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/secure/oauth/status", "")
		var status map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status["state"] == "failed" {
			t.Fatalf("flow failed before authorizing: %+v", status)
		}
		if u, _ := status["authorize_url"].(string); u != "" {
			authorizeURL = u
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Simulate the browser completing the AS's consent screen and being
	// redirected back with the code + state.
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("authorize_url %q: %v", authorizeURL, err)
	}
	q := parsed.Query()
	state, redirectURI := q.Get("state"), q.Get("redirect_uri")
	if state == "" || redirectURI == "" {
		t.Fatalf("authorize_url missing state/redirect_uri: %q", authorizeURL)
	}
	callback, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("redirect_uri %q: %v", redirectURI, err)
	}
	callback.RawQuery = url.Values{"state": {state}, "code": {"fake-code-1"}}.Encode()

	w = doJSON(t, srv, http.MethodGet, callback.RequestURI(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("callback: status = %d: %s", w.Code, w.Body.String())
	}

	// Poll until the flow settles.
	deadline = time.Now().Add(5 * time.Second)
	var last map[string]any
	for {
		if time.Now().After(deadline) {
			t.Fatalf("flow did not settle; last status: %+v", last)
		}
		w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/secure/oauth/status", "")
		last = map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &last); err != nil {
			t.Fatal(err)
		}
		state := last["state"]
		if state == "connected" {
			break
		}
		if state == "failed" {
			t.Fatalf("flow failed: %+v", last)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The connection must be live in the registry with its tools listed.
	reg := tools.ActiveMCPRegistry()
	if reg == nil || reg.Get("secure") == nil {
		t.Fatal("authorized server should be connected in the registry")
	}
	if n := len(reg.Get("secure").Tools); n != 1 {
		t.Fatalf("tools = %d, want 1", n)
	}

	// The token must be cached for future non-interactive connects.
	if _, err := os.Stat(filepath.Join(home, ".octo", "mcp-tokens", "secure.json")); err != nil {
		t.Fatalf("token cache not written: %v", err)
	}

	// The panel list reflects the connected state.
	w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers", "")
	servers := decodeServers(t, w)
	if len(servers) != 1 || servers[0].Status != "connected" {
		t.Fatalf("panel list after oauth: %+v", servers)
	}
}

// TestMCPOAuth_Callback_RejectsBadState covers the callback's only defense
// against a forged or stale hit: a state that doesn't match the in-flight
// attempt must not resolve it (a real attempt could still be waiting).
func TestMCPOAuth_Callback_RejectsBadState(t *testing.T) {
	fake := newFakeOAuthMCPServer(t)
	defer fake.srv.Close()

	mcpTestHome(t, `{"mcpServers": {"secure": {"url": "`+fake.srv.URL+`/mcp", "auth": "oauth"}}}`)
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers/secure/oauth/start", "")
	if w.Code != http.StatusOK {
		t.Fatalf("start: status = %d: %s", w.Code, w.Body.String())
	}

	w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/secure/oauth/callback?state=bogus&code=x", "")
	if w.Code != http.StatusConflict {
		t.Errorf("callback with bad state: status = %d, want 409", w.Code)
	}

	// The real flow must still be waiting, unaffected by the bogus hit.
	w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/secure/oauth/status", "")
	var status map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["state"] == "failed" {
		t.Fatalf("bogus callback must not fail the real attempt: %+v", status)
	}
}

// TestMCPOAuth_Callback_BypassesAuth confirms the callback route is reachable
// without an access key — required since the browser's top-level navigation
// back from the authorization server carries none. A non-loopback caller
// with no key would be rejected by requireAuth on every other route.
func TestMCPOAuth_Callback_BypassesAuth(t *testing.T) {
	fake := newFakeOAuthMCPServer(t)
	defer fake.srv.Close()

	mcpTestHome(t, `{"mcpServers": {"secure": {"url": "`+fake.srv.URL+`/mcp", "auth": "oauth"}}}`)
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/servers/secure/oauth/callback?state=x&code=y", nil)
	req.RemoteAddr = "203.0.113.5:1234" // non-loopback, no access key
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Fatalf("callback must bypass requireAuth, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMCPOAuth_StartValidation(t *testing.T) {
	mcpTestHome(t, `{"mcpServers": {
		"plain": {"url": "https://x.example"},
		"off":   {"url": "https://y.example", "auth": "oauth", "disabled": true}
	}}`)
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	cases := []struct {
		path string
		want int
	}{
		{"/api/mcp/servers/missing/oauth/start", http.StatusNotFound},
		{"/api/mcp/servers/plain/oauth/start", http.StatusBadRequest}, // not oauth
		{"/api/mcp/servers/off/oauth/start", http.StatusConflict},     // disabled
	}
	for _, c := range cases {
		w := doJSON(t, srv, http.MethodPost, c.path, "")
		if w.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.path, w.Code, c.want)
		}
	}

	// No flow started → status is 404.
	w := doJSON(t, srv, http.MethodGet, "/api/mcp/servers/plain/oauth/status", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status without flow: %d, want 404", w.Code)
	}
}

func TestRequestScheme(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*http.Request)
		want  string
	}{
		{"plain http", func(r *http.Request) {}, "http"},
		{"terminates TLS itself", func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, "https"},
		{"forwarded by a TLS-terminating proxy", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, "https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8088/x", nil)
			c.setup(r)
			if got := requestScheme(r); got != c.want {
				t.Errorf("requestScheme = %q, want %q", got, c.want)
			}
		})
	}
}

// TestMCPOAuth_LateCallbackAfterTimeout_Rejected covers a browser completing
// the redirect after the server already gave up waiting: the flow must stay
// "failed" and the late callback must be rejected as stale (409), not
// silently accepted into an abandoned channel and shown a misleading
// "authorization received" page.
func TestMCPOAuth_LateCallbackAfterTimeout_Rejected(t *testing.T) {
	orig := authCodeTimeout
	authCodeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { authCodeTimeout = orig })

	fake := newFakeOAuthMCPServer(t)
	defer fake.srv.Close()

	mcpTestHome(t, `{"mcpServers": {"secure": {"url": "`+fake.srv.URL+`/mcp", "auth": "oauth"}}}`)
	t.Cleanup(app.ShutdownMCP)
	tools.SetMCPRegistry(nil)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})

	w := doJSON(t, srv, http.MethodPost, "/api/mcp/servers/secure/oauth/start", "")
	if w.Code != http.StatusOK {
		t.Fatalf("start: status = %d: %s", w.Code, w.Body.String())
	}

	// Capture the state/redirect_uri before the flow times out.
	var state, redirectURI string
	deadline := time.Now().Add(2 * time.Second)
	for state == "" {
		if time.Now().After(deadline) {
			t.Fatal("flow never reached authorizing with an authorize_url")
		}
		w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/secure/oauth/status", "")
		var status map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if u, _ := status["authorize_url"].(string); u != "" {
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatal(err)
			}
			state = parsed.Query().Get("state")
			redirectURI = parsed.Query().Get("redirect_uri")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for the (shrunk) timeout to elapse and the flow to fail.
	deadline = time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("flow never timed out")
		}
		w = doJSON(t, srv, http.MethodGet, "/api/mcp/servers/secure/oauth/status", "")
		var status map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status["state"] == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The late callback must now be rejected, not accepted.
	callback, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	callback.RawQuery = url.Values{"state": {state}, "code": {"too-late"}}.Encode()
	w = doJSON(t, srv, http.MethodGet, callback.RequestURI(), "")
	if w.Code != http.StatusConflict {
		t.Errorf("late callback: status = %d, want 409 (stale)", w.Code)
	}
}
