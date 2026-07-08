package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/mcp"
)

// Web-side MCP OAuth: the browser-redirect Authorization Code flow needs a
// callback endpoint, but a browser can't block the POST that started the
// flow for however long the user takes to approve it. So the flow is split:
// POST …/oauth/start launches the connect in a goroutine with a prompt that
// builds the authorize URL and blocks waiting for GET …/oauth/callback to
// deliver the code, while the panel polls GET …/oauth/status until the state
// settles. The token lands in the usual ~/.octo/mcp-tokens/<server>.json
// cache, so every later connect (including `octo serve` restarts) reuses it
// without re-authorizing.

// authCodeTimeout bounds how long a flow waits for the callback before
// giving up — the user closed the tab, or the AS never redirected back. A
// var so tests can shrink it rather than waiting out the real 5 minutes.
var authCodeTimeout = 5 * time.Minute

// mcpOAuthFlow tracks one in-flight Authorization Code flow. It doubles as
// the mcp.OAuthPrompt handed to the connect path.
type mcpOAuthFlow struct {
	redirectBase string // e.g. "http://127.0.0.1:8088", from the request that started this flow
	name         string // server name; part of the callback path

	mu            sync.Mutex
	state         string // "starting" | "authorizing" | "connected" | "failed"
	authorizeURL  string
	expectedState string
	codeCh        chan codeResult
	err           string
}

// codeResult is what the callback handler delivers to a waiting
// AwaitAuthorizationCode call.
type codeResult struct {
	code string
	err  error
}

// RedirectURI implements mcp.OAuthPrompt.
func (f *mcpOAuthFlow) RedirectURI() string {
	return f.redirectBase + "/api/mcp/servers/" + url.PathEscape(f.name) + "/oauth/callback"
}

// AwaitAuthorizationCode implements mcp.OAuthPrompt: surface the authorize
// URL for the panel to open, then block until the callback handler delivers
// a code (or error) for the state generated for this attempt, or we time
// out / ctx is cancelled.
func (f *mcpOAuthFlow) AwaitAuthorizationCode(ctx context.Context, authorizeURL, state string) (string, error) {
	f.mu.Lock()
	f.state = "authorizing"
	f.authorizeURL = authorizeURL
	f.expectedState = state
	f.codeCh = make(chan codeResult, 1)
	ch := f.codeCh
	f.mu.Unlock()

	// clearIfCurrent drops the wait state on a give-up path (timeout/cancel)
	// so a callback that arrives after we've stopped waiting is correctly
	// rejected by deliver() as stale, rather than silently succeeding into a
	// channel nobody reads and showing the browser a misleading "received"
	// page for a flow the panel already reports as failed.
	clearIfCurrent := func() {
		f.mu.Lock()
		if f.codeCh == ch {
			f.codeCh = nil
			f.expectedState = ""
		}
		f.mu.Unlock()
	}

	timer := time.NewTimer(authCodeTimeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.code, res.err
	case <-timer.C:
		clearIfCurrent()
		return "", errors.New("timed out waiting for browser authorization")
	case <-ctx.Done():
		clearIfCurrent()
		return "", ctx.Err()
	}
}

func (f *mcpOAuthFlow) Done() {}

// deliver is called by the callback handler once the browser redirects back.
// Returns false if no attempt is currently waiting (stale/duplicate hit).
func (f *mcpOAuthFlow) deliver(state, code string, err error) bool {
	f.mu.Lock()
	ch := f.codeCh
	expected := f.expectedState
	f.codeCh = nil
	f.mu.Unlock()
	if ch == nil || state == "" || state != expected {
		return false
	}
	if err != nil {
		ch <- codeResult{err: err}
	} else {
		ch <- codeResult{code: code}
	}
	return true
}

func (f *mcpOAuthFlow) finish(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.state = "failed"
		f.err = err.Error()
		return
	}
	f.state = "connected"
}

func (f *mcpOAuthFlow) snapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{"state": f.state}
	if f.authorizeURL != "" && f.state == "authorizing" {
		out["authorize_url"] = f.authorizeURL
	}
	if f.err != "" {
		out["error"] = f.err
	}
	return out
}

// inFlight reports whether the flow is still running (a finished flow can be
// replaced by a fresh start).
func (f *mcpOAuthFlow) inFlight() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state == "starting" || f.state == "authorizing"
}

// ─── POST /api/mcp/servers/{name}/oauth/start ───────────────────────────────

func (s *Server) handleStartMCPOAuth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	if !s.cfg.Tools {
		writeError(w, http.StatusConflict, "tools are disabled on this server")
		return
	}

	managed, err := mcp.LoadManaged(s.curCwd())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var entry *mcp.ManagedServer
	for i := range managed {
		if managed[i].Name == name {
			entry = &managed[i]
			break
		}
	}
	switch {
	case entry == nil:
		writeError(w, http.StatusNotFound, "mcp server not found")
		return
	case entry.Invalid != "":
		writeError(w, http.StatusConflict, "cannot authorize an invalid entry: "+entry.Invalid)
		return
	case entry.Entry.Disabled:
		writeError(w, http.StatusConflict, "server is disabled; enable it first")
		return
	case entry.Entry.Auth != "oauth":
		writeError(w, http.StatusBadRequest, "server is not configured for oauth")
		return
	}

	if s.mcpOAuthFlows == nil {
		s.mcpOAuthFlows = map[string]*mcpOAuthFlow{}
	}
	// Idempotent while a flow is running: return its current snapshot so a
	// double-click (or a second tab) doesn't spawn a competing attempt.
	if existing := s.mcpOAuthFlows[name]; existing != nil && existing.inFlight() {
		writeJSON(w, http.StatusOK, existing.snapshot())
		return
	}

	// The callback's redirect_uri targets whatever host:port the caller's
	// browser is already using to reach this server (loopback, LAN IP, or
	// an SSH-tunneled port) — the one address guaranteed to route back here.
	// The scheme has to match what the browser actually used, not just
	// assume http: octo serve itself never terminates TLS, but a reverse
	// proxy or tunnel (Cloudflare Tunnel, nginx, etc.) in front of it often
	// does, forwarding plain HTTP to us — registering an http:// redirect_uri
	// in that case would send the browser back to a scheme the AS never
	// agreed to, or one the browser itself refuses to downgrade to.
	flow := &mcpOAuthFlow{state: "starting", redirectBase: requestScheme(r) + "://" + r.Host, name: name}
	s.mcpOAuthFlows[name] = flow
	serverEntry := entry.Entry
	go func() {
		// Background context: the flow outlives the start request by design.
		flow.finish(app.ConnectMCPServerAuth(context.Background(), name, serverEntry, flow, nil))
	}()

	writeJSON(w, http.StatusOK, flow.snapshot())
}

// ─── GET /api/mcp/servers/{name}/oauth/status ───────────────────────────────

func (s *Server) handleMCPOAuthStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mcpMu.Lock()
	flow := s.mcpOAuthFlows[name]
	s.mcpMu.Unlock()

	if flow == nil {
		writeError(w, http.StatusNotFound, "no oauth flow for this server")
		return
	}
	writeJSON(w, http.StatusOK, flow.snapshot())
}

// ─── GET /api/mcp/servers/{name}/oauth/callback ─────────────────────────────
//
// Reached by the user's browser via a top-level navigation from the
// authorization server, so it deliberately bypasses requireAuth (see
// server.go's registerRoutes, mounted with s.mux.HandleFunc like
// /api/health rather than s.api()): the request carries no access key and
// may arrive with a third-party Origin, which requireAuth would reject.
// The "state" param is this endpoint's only defense against a forged or
// stale hit — deliver() checks it against the value generated for the
// specific attempt that's currently waiting.
func (s *Server) handleMCPOAuthCallback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	q := r.URL.Query()

	s.mcpMu.Lock()
	flow := s.mcpOAuthFlows[name]
	s.mcpMu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if flow == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, oauthCallbackPage("No authorization is in progress for this server. You can close this tab."))
		return
	}

	var deliverErr error
	if errCode := q.Get("error"); errCode != "" {
		deliverErr = fmt.Errorf("%s: %s", errCode, q.Get("error_description"))
	}
	ok := flow.deliver(q.Get("state"), q.Get("code"), deliverErr)
	if !ok {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, oauthCallbackPage("This authorization link has expired or was already used. You can close this tab and try again from the panel."))
		return
	}
	fmt.Fprint(w, oauthCallbackPage("Authorization received — you can close this tab."))
}

func oauthCallbackPage(message string) string {
	return "<!doctype html><html><head><title>octo</title></head><body style=\"font: 14px system-ui; padding: 2rem;\">" + message + "</body></html>"
}

// requestScheme reports the scheme the browser actually used to reach this
// server. r.TLS is set when octo serve terminates TLS itself; otherwise a
// reverse proxy or tunnel in front of it is expected to set the standard
// X-Forwarded-Proto header when it forwards plain HTTP for an HTTPS client
// connection. Defaults to "http", matching every other place this codebase
// assumes plain HTTP for octo serve's own listener.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	return "http"
}
