package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubPrompt fakes the browser-redirect side of the Authorization Code flow:
// AwaitAuthorizationCode records the authorize URL/state it was given and
// immediately returns a canned code, as if the user had completed the
// browser flow instantly.
type stubPrompt struct {
	mu               sync.Mutex
	authorizations   int
	done             int
	lastAuthorizeURL string
	lastState        string

	redirectURI string // defaults to a fixed fake loopback URL if empty
	fakeCode    string // defaults to "fake-code-12345" if empty
	awaitErr    error  // if set, AwaitAuthorizationCode returns this instead of a code
}

func (p *stubPrompt) RedirectURI() string {
	if p.redirectURI != "" {
		return p.redirectURI
	}
	return "http://127.0.0.1:0/callback"
}

func (p *stubPrompt) AwaitAuthorizationCode(_ context.Context, authorizeURL, state string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorizations++
	p.lastAuthorizeURL = authorizeURL
	p.lastState = state
	if p.awaitErr != nil {
		return "", p.awaitErr
	}
	if p.fakeCode != "" {
		return p.fakeCode, nil
	}
	return "fake-code-12345", nil
}

func (p *stubPrompt) Done() { p.mu.Lock(); p.done++; p.mu.Unlock() }

// fakeAuthServer wires up the RFC 9728 / 8414 / 7591 endpoints plus a token
// endpoint against an httptest.Server. There's no /authorize handler: the
// stub prompt fakes the browser round trip instead of actually hitting it,
// so tests assert against the authorize URL string (captured by stubPrompt)
// rather than a real HTTP request to it.
type fakeAuthServer struct {
	t   *testing.T
	mux *http.ServeMux
	srv *httptest.Server

	mu                   sync.Mutex
	registrations        int
	tokenIssued          string
	refreshIssued        string
	lastTokenForm        url.Values
	lastRefreshForm      url.Values
	lastRegisterScope    string
	lastRegisterRedirect string
	lastRegisterGrants   []string
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	f := &fakeAuthServer{
		t:             t,
		mux:           http.NewServeMux(),
		tokenIssued:   "access-12345",
		refreshIssued: "refresh-12345",
	}
	f.srv = httptest.NewServer(f.mux)
	f.wire()
	return f
}

func (f *fakeAuthServer) URL(path string) string { return f.srv.URL + path }

func (f *fakeAuthServer) wire() {
	// 1. The protected resource itself (the MCP endpoint).
	f.mux.HandleFunc("/mcp_server/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.tokenIssued {
			w.Header().Set("WWW-Authenticate",
				`Bearer resource_metadata="`+f.URL("/.well-known/oauth-protected-resource")+`"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		// Authenticated: echo back a synthesized MCP response (just enough
		// to exercise the OAuth-on-401 path; the OAuth tests don't care
		// about MCP semantics).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Result:  json.RawMessage(`{"ok":true}`),
		})
	})

	// 2. Protected-resource metadata (RFC 9728).
	f.mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prMetadata{
			Resource:             f.URL("/mcp_server/v1"),
			AuthorizationServers: []string{f.URL("/auth")},
			ScopesSupported:      []string{"mcp:read", "mcp:write"},
		})
	})

	// 3. Authorization-server metadata (RFC 8414).
	f.mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(asMetadata{
			Issuer:                f.srv.URL,
			AuthorizationEndpoint: f.URL("/authorize"),
			TokenEndpoint:         f.URL("/token"),
			RegistrationEndpoint:  f.URL("/register"),
		})
	})

	// 4. Dynamic client registration (RFC 7591).
	f.mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		scope, _ := body["scope"].(string)
		var redirect string
		if uris, ok := body["redirect_uris"].([]any); ok && len(uris) > 0 {
			redirect, _ = uris[0].(string)
		}
		var grants []string
		if gs, ok := body["grant_types"].([]any); ok {
			for _, g := range gs {
				if s, ok := g.(string); ok {
					grants = append(grants, s)
				}
			}
		}
		f.mu.Lock()
		f.registrations++
		f.lastRegisterScope = scope
		f.lastRegisterRedirect = redirect
		f.lastRegisterGrants = grants
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":   "client-registered-1",
			"client_name": "octo-test",
		})
	})

	// 5. Token endpoint (authorization_code exchange + refresh).
	f.mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant := r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")

		switch grant {
		case "authorization_code":
			f.mu.Lock()
			f.lastTokenForm = r.Form
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(tokenResponse{
				AccessToken:  f.tokenIssued,
				RefreshToken: f.refreshIssued,
				TokenType:    "Bearer",
				ExpiresIn:    3600,
			})
		case "refresh_token":
			// Rotate the access token so we can detect that a refresh
			// happened.
			f.mu.Lock()
			f.tokenIssued = "access-rotated-67890"
			f.lastRefreshForm = r.Form
			tok := f.tokenIssued
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(tokenResponse{
				AccessToken: tok,
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(tokenResponse{Error: "unsupported_grant_type"})
		}
	})
}

func (f *fakeAuthServer) close() { f.srv.Close() }

func TestOAuth_EndToEnd_AuthCodeFlow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fs := newFakeAuthServer(t)
	defer fs.close()

	prompt := &stubPrompt{}
	oc, err := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := oc.Token(ctx)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != fs.tokenIssued {
		t.Errorf("token = %q", tok)
	}
	if prompt.authorizations != 1 {
		t.Errorf("AwaitAuthorizationCode called %d times", prompt.authorizations)
	}
	if prompt.done != 1 {
		t.Errorf("Done called %d times", prompt.done)
	}
	if fs.registrations != 1 {
		t.Errorf("registration calls = %d, want 1", fs.registrations)
	}
	if prompt.lastState == "" {
		t.Error("authorize URL flow should have generated a non-empty state")
	}
}

func TestOAuth_TokenCacheReused(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	ctx := context.Background()
	if _, err := oc.Token(ctx); err != nil {
		t.Fatal(err)
	}

	// Build a SECOND client against the same store path; should hit the cache.
	oc2, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc2.Token(ctx); err != nil {
		t.Fatal(err)
	}
	// Prompt was only used for the first one.
	if prompt.authorizations != 1 {
		t.Errorf("expected exactly one authorization prompt, got %d", prompt.authorizations)
	}
}

func TestOAuth_RefreshOnExpiry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Force-expire the cached access token; refresh token still valid.
	oc.state.ExpiresAt = time.Now().Add(-1 * time.Hour)

	tok, err := oc.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	// The fake rotates the token on refresh; verify we got the rotated one
	// and no second authorization prompt happened.
	if tok != "access-rotated-67890" {
		t.Errorf("expected refreshed access token, got %q", tok)
	}
	if prompt.authorizations != 1 {
		t.Errorf("refresh path should not re-prompt user; got %d prompts", prompt.authorizations)
	}
}

// TestOAuth_SendsResourceAndScope guards against the audience-binding gap
// that caused a fresh, otherwise-valid token to be rejected as "401
// unauthorized" by an audience-checking authorization server (RFC 8707):
// the client must echo the protected-resource metadata's "resource" and
// "scopes_supported" back to the AS on the authorization request, the token
// exchange, and every refresh — not just parse them and discard them.
func TestOAuth_SendsResourceAndScope(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	wantResource := fs.URL("/mcp_server/v1")
	wantScope := "mcp:read mcp:write"

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(wantResource, "fake", "octo-test", prompt)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}

	authorizeQuery, err := url.Parse(prompt.lastAuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL %q: %v", prompt.lastAuthorizeURL, err)
	}
	q := authorizeQuery.Query()
	if got := q.Get("resource"); got != wantResource {
		t.Errorf("authorize URL resource = %q, want %q", got, wantResource)
	}
	if got := q.Get("scope"); got != wantScope {
		t.Errorf("authorize URL scope = %q, want %q", got, wantScope)
	}
	if got := fs.lastTokenForm.Get("resource"); got != wantResource {
		t.Errorf("token exchange resource = %q, want %q", got, wantResource)
	}
	if got := fs.lastTokenForm.Get("scope"); got != wantScope {
		t.Errorf("token exchange scope = %q, want %q", got, wantScope)
	}
	if got := fs.lastRegisterScope; got != wantScope {
		t.Errorf("registration scope = %q, want %q", got, wantScope)
	}

	// Force a refresh and check the same params are re-sent.
	oc.state.ExpiresAt = time.Now().Add(-1 * time.Hour)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fs.lastRefreshForm.Get("resource"); got != wantResource {
		t.Errorf("refresh resource = %q, want %q", got, wantResource)
	}
	if got := fs.lastRefreshForm.Get("scope"); got != wantScope {
		t.Errorf("refresh scope = %q, want %q", got, wantScope)
	}
}

// TestOAuth_PKCE_ChallengeMatchesVerifier makes sure the code_challenge sent
// in the authorize URL is the RFC 7636 S256 hash of the code_verifier later
// sent in the token exchange — the two are generated together but travel
// through completely separate requests, so a copy/paste slip between them
// would silently defeat PKCE without failing the fake server (which doesn't
// validate it) or any other assertion.
func TestOAuth_PKCE_ChallengeMatchesVerifier(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}

	authorizeQuery, err := url.Parse(prompt.lastAuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := authorizeQuery.Query()
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	challenge := q.Get("code_challenge")
	verifier := fs.lastTokenForm.Get("code_verifier")
	if challenge == "" || verifier == "" {
		t.Fatalf("challenge=%q verifier=%q, want both non-empty", challenge, verifier)
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Errorf("code_challenge = %q, want S256(code_verifier) = %q", challenge, want)
	}
}

// TestOAuth_RedirectURIMismatch_ForcesReregistration covers a CLI session's
// loopback listener binding a fresh random port on every run: a cached
// ClientID registered against last run's redirect_uri can't be reused this
// run (the authorization server would reject the mismatch), so authorize()
// must detect the change and register a fresh client instead of reusing the
// stale one.
func TestOAuth_RedirectURIMismatch_ForcesReregistration(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	promptA := &stubPrompt{redirectURI: "http://127.0.0.1:11111/callback"}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", promptA)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fs.registrations != 1 {
		t.Fatalf("registrations = %d, want 1 after first authorize", fs.registrations)
	}

	// Simulate a fresh CLI run: no refresh token available (as if the store
	// only kept the access token), a different loopback port this time.
	oc.state.RefreshToken = ""
	oc.state.ExpiresAt = time.Now().Add(-1 * time.Hour)
	promptB := &stubPrompt{redirectURI: "http://127.0.0.1:22222/callback"}
	oc.prompt = promptB

	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fs.registrations != 2 {
		t.Errorf("registrations = %d, want 2 (redirect_uri mismatch must force re-registration)", fs.registrations)
	}
	if fs.lastRegisterRedirect != promptB.redirectURI {
		t.Errorf("last registered redirect_uri = %q, want %q", fs.lastRegisterRedirect, promptB.redirectURI)
	}
}

// TestOAuth_StaleCacheWithoutResource_ForcesFreshAuth guards the upgrade
// path: a cache written before RFC 8707 resource-binding was added has no
// "resource" field, so its cached/refreshed access token was never bound
// to this resource — exactly the token that produced the original 401.
// Reusing or refreshing it after the fix ships would just reproduce the
// bug for anyone who already hit it; the client must instead treat a
// resource-less cache as stale and re-run the full authorization flow.
func TestOAuth_StaleCacheWithoutResource_ForcesFreshAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	prompt := &stubPrompt{}
	oc, err := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-fix cache shape: valid-looking access + refresh token, no resource/scope.
	legacy := `{
		"resource_url": "` + fs.URL("/mcp_server/v1") + `",
		"client_id": "legacy-client",
		"access_token": "legacy-access-token",
		"refresh_token": "legacy-refresh-token",
		"expires_at": "` + time.Now().Add(1*time.Hour).Format(time.RFC3339) + `",
		"token_url": "` + fs.URL("/token") + `"
	}`
	if err := os.WriteFile(oc.storePath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	tok, err := oc.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != fs.tokenIssued {
		t.Errorf("token = %q, want fresh token %q (legacy cache should not be reused)", tok, fs.tokenIssued)
	}
	if prompt.authorizations != 1 {
		t.Errorf("expected a fresh authorization prompt for stale legacy cache, got %d", prompt.authorizations)
	}
	if oc.state.Resource == "" {
		t.Error("expected Resource to be populated after re-authorize")
	}
}

// TestOAuth_MissingResourceInMetadata_FallsBackToConfiguredURL covers the
// case oauth.go's authorize() explicitly handles: a protected-resource
// metadata document that omits "resource" (non-compliant but seen in the
// wild). The client must fall back to the resource URL it was configured
// with rather than sending an empty resource= or skipping the parameter.
func TestOAuth_MissingResourceInMetadata_FallsBackToConfiguredURL(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	var mu sync.Mutex
	var tokenForm url.Values

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Deliberately omit "resource".
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_servers": []string{srv.URL + "/auth"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(asMetadata{
			AuthorizationEndpoint: srv.URL + "/authorize",
			TokenEndpoint:         srv.URL + "/token",
			RegistrationEndpoint:  srv.URL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "client-1"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		tokenForm = r.Form
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "tok-1",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		})
	})

	resourceURL := srv.URL + "/mcp_server/v1"
	prompt := &stubPrompt{}
	oc, err := NewOAuthClient(resourceURL, "fake-noresource", "octo-test", prompt)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := oc.Token(ctx); err != nil {
		t.Fatalf("Token: %v", err)
	}

	authorizeQuery, err := url.Parse(prompt.lastAuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := authorizeQuery.Query().Get("resource"); got != resourceURL {
		t.Errorf("authorize URL resource = %q, want fallback %q", got, resourceURL)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := tokenForm.Get("resource"); got != resourceURL {
		t.Errorf("token exchange resource = %q, want fallback %q", got, resourceURL)
	}
	if oc.state.Resource != resourceURL {
		t.Errorf("cached Resource = %q, want fallback %q", oc.state.Resource, resourceURL)
	}
}

// TestOAuth_NilPrompt_FailsFast guards the fast-fail path for a connection
// that needs interactive re-authorization but has no OAuthPrompt — every
// background connect path (startup, reload, panel enable-toggle) constructs
// its OAuthClient this way, since nobody is present to complete a browser
// redirect. Before this check, authorize() would still run
// discovery/registration and then hang waiting on a prompt that can never
// resolve.
func TestOAuth_NilPrompt_FailsFast(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	oc, err := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake-noprompt", "octo-test", nil)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = oc.Token(context.Background())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("Token error = %v, want ErrReauthRequired", err)
	}
	if elapsed > time.Second {
		t.Errorf("Token took %v, want a near-instant fast-fail", elapsed)
	}
	if fs.registrations != 0 {
		t.Errorf("registrations = %d, want 0 — no prompt means no point attempting discovery/registration", fs.registrations)
	}
}

func TestOAuth_InvalidateForces_FreshAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	oc.Invalidate()
	// Invalidate clears access token but keeps refresh token: next Token
	// will refresh (not re-prompt).
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if prompt.authorizations != 1 {
		t.Errorf("invalidate should refresh, not re-prompt; got %d", prompt.authorizations)
	}
}

func TestHTTPTransport_OAuthRetryOn401(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t)
	defer fs.close()

	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", &stubPrompt{})
	tx, _ := NewHTTPTransport(HTTPConfig{
		URL:   fs.URL("/mcp_server/v1"),
		OAuth: oc,
	})
	defer tx.Close()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tx.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Errorf("result = %s", resp.Result)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func TestParseWWWAuthenticate(t *testing.T) {
	cases := map[string]string{
		`Bearer resource_metadata="https://x/.well-known/y"`:  "https://x/.well-known/y",
		`Bearer realm="oauth", resource_metadata="https://x"`: "https://x",
		`Bearer resource_metadata=https://x`:                  "https://x",
		`Basic realm="x"`:                                     "",
		``:                                                    "",
	}
	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			got := parseWWWAuthenticate(header)
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestBuildPRMetadataURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com/mcp_server/v1":    "https://example.com/.well-known/oauth-protected-resource",
		"https://example.com:8080/mcp?token=x": "https://example.com:8080/.well-known/oauth-protected-resource",
		"https://example.com":                  "https://example.com/.well-known/oauth-protected-resource",
	}
	for in, want := range cases {
		got, err := buildPRMetadataURL(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestBuildASMetadataURL_RFC8414(t *testing.T) {
	// Real Lark path: https://project.larksuite.com/b/auth/mcp
	// → https://project.larksuite.com/.well-known/oauth-authorization-server/b/auth/mcp
	got, err := buildASMetadataURL("https://project.larksuite.com/b/auth/mcp")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://project.larksuite.com/.well-known/oauth-authorization-server/b/auth/mcp"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestOAuth_NoRegistrationEndpointFails(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// Server WITHOUT registration_endpoint — must error out before
	// touching the authorization flow, since we have no client_id.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "/mcp",
				"authorization_servers": []string{"http://" + r.Host + "/auth"},
			})
		case "/.well-known/oauth-authorization-server/auth":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(asMetadata{
				AuthorizationEndpoint: "http://" + r.Host + "/authorize",
				TokenEndpoint:         "http://" + r.Host + "/token",
			})
		}
	}))
	defer srv.Close()
	oc, _ := NewOAuthClient(srv.URL+"/mcp", "noreg", "octo-test", &stubPrompt{})
	_, err := oc.Token(context.Background())
	if err == nil {
		t.Fatal("expected error for server with no registration_endpoint")
	}
	if !strings.Contains(err.Error(), "registration_endpoint") {
		t.Errorf("error should mention registration_endpoint: %v", err)
	}
}

// sanity: parse error path
func TestPostTokenEndpoint_ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"slow_down","error_description":"easy there"}`)
	}))
	defer srv.Close()
	oc := &OAuthClient{hc: &http.Client{}}
	_, err := oc.postTokenEndpoint(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("expected error")
	}
	var oerr *oauthError
	if !errors.As(err, &oerr) {
		t.Fatalf("expected *oauthError, got %T", err)
	}
	if oerr.code != "slow_down" {
		t.Errorf("code = %q", oerr.code)
	}
}

// TestPostTokenEndpoint_HTTP200WithErrorBody covers the Lark-style
// non-spec behaviour: OAuth errors land in the body even on HTTP 200.
// Without explicit handling of this we'd treat the response as success,
// cache an empty access token, and then fail every subsequent request.
func TestPostTokenEndpoint_HTTP200WithErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer srv.Close()
	oc := &OAuthClient{hc: &http.Client{}}
	_, err := oc.postTokenEndpoint(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("expected error for 200 + error body (non-spec but common)")
	}
	var oerr *oauthError
	if !errors.As(err, &oerr) {
		t.Fatalf("expected *oauthError, got %T: %v", err, err)
	}
	if oerr.code != "authorization_pending" {
		t.Errorf("code = %q, want authorization_pending", oerr.code)
	}
}

// TestPostTokenEndpoint_HTTP200WithoutAccessToken guards against the
// silent-success bug where a 200 OK with no body fields would be treated
// as a valid token, cached, and then failed on every reuse.
func TestPostTokenEndpoint_HTTP200WithoutAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	oc := &OAuthClient{hc: &http.Client{}}
	_, err := oc.postTokenEndpoint(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("expected error for 200 + empty body")
	}
	if !strings.Contains(err.Error(), "missing access_token") {
		t.Errorf("error should mention missing access_token, got: %v", err)
	}
}
