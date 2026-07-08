package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OAuth flow for MCP servers that protect their endpoint with RFC 9728
// metadata pointers (the path the 2024-11-05 MCP spec standardised on).
//
// What this implements
//   - RFC 9728: parse WWW-Authenticate to find protected-resource metadata.
//   - RFC 8414: discover authorization-server metadata (endpoints + grant
//     types) via /.well-known/oauth-authorization-server/<path>.
//   - RFC 7591: dynamic client registration (public client, no secret,
//     PKCE-friendly), registering the redirect_uri the current OAuthPrompt
//     will receive the callback on.
//   - RFC 6749 Authorization Code grant + RFC 7636 PKCE (S256). The
//     redirect target differs by context: octo serve's web panel reuses its
//     already-running HTTP server (so the callback works through whatever
//     port/tunnel the user is already using to reach the panel); a bare CLI
//     session has no listener of its own, so it spins up a one-shot
//     loopback HTTP server for the duration of the flow (see
//     cmd/octo/mcp_prompt.go). Device Code (RFC 8628) was tried first for
//     the CLI case specifically to avoid that local listener, but real
//     authorization servers vary in how well they support it (e.g. an
//     audience-checking Lark/meegle app rejected the device-authorization
//     request outright) — Authorization Code is the flow every OAuth
//     server is expected to support, so it's used everywhere instead.
//   - RFC 8707: bind the token to the resource named in the protected-
//     resource metadata (falling back to the configured URL if the server
//     omits it) via a "resource" param on the authorization request, token
//     exchange, and refresh request. Without this, an audience-checking
//     authorization server issues a token that authenticates but isn't
//     authorized for this specific MCP endpoint — every request 401s even
//     with a brand-new token.
//   - Refresh-token flow on near-expiry + cached token persistence.
//
// What this skips
//   - Discovery via /.well-known/openid-configuration (we only do the
//     OAuth variant).
//   - Mutual TLS / client_secret_post / etc. — public client, no secret.

// OAuthPrompt drives the transport side of the Authorization Code + PKCE
// flow: getting the user's browser to the authorize URL and receiving the
// resulting code back. Implementations differ by context — see the package
// doc comment above.
type OAuthPrompt interface {
	// RedirectURI returns the redirect_uri to register and to use in the
	// authorization request. Called once per authorize() attempt, before
	// the authorization URL is built.
	RedirectURI() string
	// AwaitAuthorizationCode is called once the authorize URL is ready. The
	// implementation is responsible for getting the user to open it
	// (auto-launching a browser for CLI/TUI, surfacing it for the web panel
	// to window.open) and for blocking until the resulting code arrives via
	// whatever channel backs RedirectURI, or ctx is done.
	AwaitAuthorizationCode(ctx context.Context, authorizeURL, state string) (code string, err error)
	// Done is called once after a successful authorization.
	Done()
}

// OAuthProvider is the interface HTTPTransport sees. It hands back the
// current bearer token, refreshing or re-authenticating as needed. The
// Invalidate path is used when a 401 is observed against what was
// otherwise a valid-looking cached token — the next Token call re-runs the
// full flow.
type OAuthProvider interface {
	Token(ctx context.Context) (string, error)
	Invalidate()
}

// OAuthClient implements OAuthProvider against a single MCP resource URL.
// Persists state under ~/.octo/mcp-tokens/<server>.json so a fresh
// `octo` session reuses the access token + refresh token from the
// previous run.
type OAuthClient struct {
	resourceURL string
	storePath   string
	clientName  string
	prompt      OAuthPrompt
	hc          *http.Client

	mu       sync.Mutex
	state    *oauthState // loaded lazily on first Token call
	loadOnce sync.Once
}

// oauthState is the persisted token cache shape. Stored as plain JSON at
// storePath — the file is per-user and mode 0600 so other users on the
// host can't read it. Refresh-token rotation is handled by overwriting
// this file on each refresh.
type oauthState struct {
	ResourceURL  string    `json:"resource_url"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenURL     string    `json:"token_url"`
	// Resource is the RFC 8707 audience value echoed back on every token
	// request (device authorization, token exchange, refresh) so an
	// audience-checking authorization server issues a token scoped to this
	// MCP endpoint rather than a generic one. Comes from the protected-
	// resource metadata's "resource" field, falling back to ResourceURL if
	// the server omits it.
	Resource string `json:"resource,omitempty"`
	// Scope is the space-separated scope string requested alongside the
	// token, derived from the protected-resource metadata's
	// "scopes_supported". Re-sent on refresh so a rotated token keeps the
	// same grant.
	Scope string `json:"scope,omitempty"`
	// RedirectURI is the redirect_uri ClientID was registered with. A CLI
	// session's loopback listener binds a fresh random port on every run,
	// so a cached ClientID is only reusable when the current OAuthPrompt
	// reports the same RedirectURI it was registered with — otherwise the
	// authorization server will reject the mismatch, so authorize()
	// re-registers instead of reusing it.
	RedirectURI string `json:"redirect_uri,omitempty"`
}

// NewOAuthClient builds a provider for the given MCP resource URL.
// serverName names the cache file (and feeds RFC 7591's client_name).
// prompt drives the user-visible parts of the OAuth flow.
func NewOAuthClient(resourceURL, serverName, clientName string, prompt OAuthPrompt) (*OAuthClient, error) {
	if resourceURL == "" {
		return nil, errors.New("oauth: empty resourceURL")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("oauth: resolve home: %w", err)
	}
	storeDir := filepath.Join(home, ".octo", "mcp-tokens")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		return nil, fmt.Errorf("oauth: mkdir %s: %w", storeDir, err)
	}
	return &OAuthClient{
		resourceURL: resourceURL,
		storePath:   filepath.Join(storeDir, sanitizeFilename(serverName)+".json"),
		clientName:  clientName,
		prompt:      prompt,
		hc:          &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Token returns a valid bearer token, refreshing or re-authenticating as
// needed. Safe for concurrent callers — one in-flight token negotiation
// at a time via mu.
func (o *OAuthClient) Token(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.loadOnce.Do(func() {
		if st, err := loadOAuthState(o.storePath); err == nil {
			o.state = st
		}
	})

	// A cache written before RFC 8707 resource-binding was added has no
	// Resource recorded. Its access token (and any token a refresh of it
	// would yield) was never bound to this specific resource, so it's the
	// same non-audience-bound token that caused the original 401 — reusing
	// or refreshing it just reproduces the bug. Skip straight to a full
	// re-authorize, which records Resource going forward.
	stale := o.state != nil && o.state.Resource == "" &&
		(o.state.AccessToken != "" || o.state.RefreshToken != "")

	// 1. Cached + still valid (with a 60s slack for clock skew + round-trip).
	if !stale && o.state != nil && o.state.AccessToken != "" &&
		o.state.ExpiresAt.After(time.Now().Add(60*time.Second)) {
		return o.state.AccessToken, nil
	}
	// 2. Cached refresh token + token URL — try to refresh without bothering the user.
	if !stale && o.state != nil && o.state.RefreshToken != "" && o.state.TokenURL != "" {
		if err := o.refresh(ctx); err == nil {
			return o.state.AccessToken, nil
		}
		// Refresh failed (revoked / expired). Fall through to full flow.
	}
	// 3. Full flow: discover, register if needed, run device grant.
	if err := o.authorize(ctx); err != nil {
		return "", err
	}
	return o.state.AccessToken, nil
}

// Invalidate forgets the current access token without throwing away the
// refresh token / client_id. The next Token call will try refresh first;
// if refresh fails, it falls into the full flow.
func (o *OAuthClient) Invalidate() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state != nil {
		o.state.AccessToken = ""
		o.state.ExpiresAt = time.Time{}
	}
}

// ErrReauthRequired is returned by authorize (and therefore Token) when the
// cached/refreshed token is unusable and completing the OAuth flow needs a
// human — but no OAuthPrompt was supplied. Background connections (startup,
// reload, panel-triggered reconnect) all construct their OAuthClient with a
// nil prompt, since nobody is present to complete a browser redirect.
// Callers can check for this sentinel via errors.Is to detect "this
// connection needs an interactive re-authorization" without waiting.
var ErrReauthRequired = errors.New("oauth: interactive re-authorization required")

// authorize runs the full discovery + (maybe register) + Authorization Code
// + PKCE flow. Caller holds o.mu.
func (o *OAuthClient) authorize(ctx context.Context) error {
	if o.prompt == nil {
		return ErrReauthRequired
	}
	asMeta, prMeta, err := o.discover(ctx)
	if err != nil {
		return err
	}
	// RFC 8707 audience binding: bind the token to the resource the
	// protected-resource metadata names, falling back to the configured
	// URL if the server omits "resource" (non-compliant but seen in the
	// wild). Without this, an audience-checking authorization server
	// issues a token that authenticates fine but isn't authorized for this
	// specific MCP endpoint, and every request 401s.
	resource := prMeta.Resource
	if resource == "" {
		resource = o.resourceURL
	}
	scope := strings.Join(prMeta.ScopesSupported, " ")
	redirectURI := o.prompt.RedirectURI()

	clientID := ""
	clientSecret := ""
	if o.state != nil && o.state.ClientID != "" && o.state.RedirectURI == redirectURI {
		clientID = o.state.ClientID
		clientSecret = o.state.ClientSecret
	} else if asMeta.RegistrationEndpoint != "" {
		// Public client + PKCE-friendly: no secret expected back.
		id, secret, err := o.register(ctx, asMeta.RegistrationEndpoint, scope, redirectURI)
		if err != nil {
			return fmt.Errorf("oauth: dynamic client registration: %w", err)
		}
		clientID = id
		clientSecret = secret
	} else {
		return errors.New("oauth: server has no registration_endpoint and no cached client_id")
	}

	tok, err := o.authCodeFlow(ctx, asMeta, clientID, clientSecret, resource, scope, redirectURI)
	if err != nil {
		return fmt.Errorf("oauth: authorization code flow: %w", err)
	}
	o.state = &oauthState{
		ResourceURL:  o.resourceURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    expiryFromSeconds(tok.ExpiresIn),
		TokenURL:     asMeta.TokenEndpoint,
		Resource:     resource,
		Scope:        scope,
		RedirectURI:  redirectURI,
	}
	if err := saveOAuthState(o.storePath, o.state); err != nil {
		// Persist failure is non-fatal — we still have the token in
		// memory for this session.
		fmt.Fprintf(os.Stderr, "oauth: cache write failed: %v\n", err)
	}
	o.prompt.Done()
	return nil
}

// refresh tries to renew the access token via grant_type=refresh_token.
// Caller holds o.mu. Returns an error if the server rejects the refresh
// (e.g. revoked); caller falls back to full flow.
func (o *OAuthClient) refresh(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", o.state.RefreshToken)
	form.Set("client_id", o.state.ClientID)
	if o.state.ClientSecret != "" {
		form.Set("client_secret", o.state.ClientSecret)
	}
	if o.state.Resource != "" {
		form.Set("resource", o.state.Resource)
	}
	if o.state.Scope != "" {
		form.Set("scope", o.state.Scope)
	}
	tok, err := o.postTokenEndpoint(ctx, o.state.TokenURL, form)
	if err != nil {
		return err
	}
	o.state.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		// Some servers rotate refresh tokens on each use; honor the rotation.
		o.state.RefreshToken = tok.RefreshToken
	}
	o.state.ExpiresAt = expiryFromSeconds(tok.ExpiresIn)
	if err := saveOAuthState(o.storePath, o.state); err != nil {
		fmt.Fprintf(os.Stderr, "oauth: cache write failed: %v\n", err)
	}
	return nil
}

// ── discovery (RFC 9728 + 8414) ──────────────────────────────────────────

type asMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

type prMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

// discover does the two-step RFC 9728 → 8414 hop. The 9728 metadata lives
// at the protected resource's well-known path; it points at one or more
// authorization servers. We pick the first. The protected-resource
// metadata is also returned since its "resource"/"scopes_supported"
// fields feed the RFC 8707 audience binding on every subsequent token
// request.
func (o *OAuthClient) discover(ctx context.Context) (*asMetadata, *prMetadata, error) {
	prURL, err := buildPRMetadataURL(o.resourceURL)
	if err != nil {
		return nil, nil, err
	}
	var pr prMetadata
	if err := o.getJSON(ctx, prURL, &pr); err != nil {
		return nil, nil, fmt.Errorf("protected-resource metadata: %w", err)
	}
	if len(pr.AuthorizationServers) == 0 {
		return nil, nil, errors.New("oauth: no authorization_servers in protected-resource metadata")
	}

	asURL, err := buildASMetadataURL(pr.AuthorizationServers[0])
	if err != nil {
		return nil, nil, err
	}
	var as asMetadata
	if err := o.getJSON(ctx, asURL, &as); err != nil {
		return nil, nil, fmt.Errorf("authorization-server metadata: %w", err)
	}
	if as.AuthorizationEndpoint == "" {
		return nil, nil, errors.New("oauth: server does not support the authorization code grant")
	}
	if as.TokenEndpoint == "" {
		return nil, nil, errors.New("oauth: server has no token endpoint")
	}
	return &as, &pr, nil
}

// buildPRMetadataURL constructs the RFC 9728 well-known URL by inserting
// /.well-known/oauth-protected-resource just after the host:
//
//	https://host/path  →  https://host/.well-known/oauth-protected-resource
func buildPRMetadataURL(resourceURL string) (string, error) {
	u, err := url.Parse(resourceURL)
	if err != nil {
		return "", err
	}
	// MCP spec says the well-known lives at the resource's origin; suffixing
	// the resource path is non-standard. Reset the path to the well-known.
	u.Path = "/.well-known/oauth-protected-resource"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// buildASMetadataURL constructs the RFC 8414 well-known URL by inserting
// /.well-known/oauth-authorization-server BETWEEN host and the original
// path:
//
//	https://host/b/auth/mcp  →  https://host/.well-known/oauth-authorization-server/b/auth/mcp
//
// Servers may also accept the suffix variant, but the RFC 8414 form is the
// canonical one. We try the canonical form only.
func buildASMetadataURL(issuerURL string) (string, error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", err
	}
	origPath := strings.TrimSuffix(u.Path, "/")
	u.Path = "/.well-known/oauth-authorization-server" + origPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// ── dynamic client registration (RFC 7591) ───────────────────────────────

type registrationResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (o *OAuthClient) register(ctx context.Context, endpoint, scope, redirectURI string) (clientID, clientSecret string, err error) {
	body := map[string]any{
		"client_name":                o.clientName,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "none",
		"application_type":           "native",
	}
	if scope != "" {
		body["scope"] = scope
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(raw)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.hc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("registration HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out registrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.ClientID == "" {
		return "", "", errors.New("registration response missing client_id")
	}
	return out.ClientID, out.ClientSecret, nil
}

// ── authorization code + PKCE (RFC 6749 + RFC 7636) ─────────────────────

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	// Error fields: some servers (e.g. Lark) return these with a 200
	// instead of a 4xx status; postTokenEndpoint checks Error regardless of
	// HTTP status.
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// authCodeFlow runs the browser-redirect Authorization Code grant with PKCE:
// build the authorize URL, hand it to the prompt (which gets the user's
// browser there and blocks until the resulting code arrives via its own
// redirect handling — a local callback route for octo serve, a one-shot
// loopback listener for a bare CLI session), then exchange the code for a
// token.
func (o *OAuthClient) authCodeFlow(ctx context.Context, as *asMetadata, clientID, clientSecret, resource, scope, redirectURI string) (*tokenResponse, error) {
	state, err := randomURLSafe(32)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, fmt.Errorf("generate code_verifier: %w", err)
	}

	authorizeURL, err := buildAuthorizeURL(as.AuthorizationEndpoint, clientID, redirectURI, state, codeChallengeS256(verifier), resource, scope)
	if err != nil {
		return nil, err
	}

	code, err := o.prompt.AwaitAuthorizationCode(ctx, authorizeURL, state)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	form.Set("code_verifier", verifier)
	if resource != "" {
		form.Set("resource", resource)
	}
	if scope != "" {
		form.Set("scope", scope)
	}
	return o.postTokenEndpoint(ctx, as.TokenEndpoint, form)
}

// buildAuthorizeURL appends the RFC 6749 + RFC 7636 (+ RFC 8707) query
// params to the authorization-server's authorization_endpoint.
func buildAuthorizeURL(endpoint, clientID, redirectURI, state, codeChallenge, resource, scope string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if resource != "" {
		q.Set("resource", resource)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// randomURLSafe returns n cryptographically random bytes, base64url-encoded
// (no padding) — used for both the PKCE code_verifier and the CSRF state
// param.
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallengeS256 derives the RFC 7636 S256 code_challenge from a
// code_verifier.
func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// postTokenEndpoint POSTs to the token endpoint and decodes either a
// successful tokenResponse or an *oauthError, so callers can branch on the
// RFC 6749 error code without parsing strings.
//
// Tolerance: per RFC 6749 OAuth errors should come back as 4xx, but
// servers in the wild (Lark, GitHub Apps, …) routinely return 200 with
// an `{"error":"..."}` body instead. We treat a non-empty `error` field
// as an oauthError regardless of HTTP status, and only declare success
// when `access_token` is set and `error` is empty.
func (o *OAuthClient) postTokenEndpoint(ctx context.Context, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var out tokenResponse
	if jerr := json.Unmarshal(body, &out); jerr != nil {
		// Body wasn't JSON at all — fall through to the HTTP-status error.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, fmt.Errorf("token endpoint returned non-JSON body: %s", strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out.Error != "" {
		return nil, &oauthError{code: out.Error, desc: out.ErrorDescription, status: resp.StatusCode}
	}
	if out.AccessToken == "" {
		// Non-error response but no token — usually means the server
		// returned 200 with an empty body or partial payload. Surface as
		// a parse-shaped error so the caller doesn't loop on it forever.
		return nil, fmt.Errorf("token endpoint HTTP %d: missing access_token in response", resp.StatusCode)
	}
	return &out, nil
}

type oauthError struct {
	code   string
	desc   string
	status int
}

func (e *oauthError) Error() string {
	if e.desc != "" {
		return fmt.Sprintf("oauth error %q: %s", e.code, e.desc)
	}
	return fmt.Sprintf("oauth error %q", e.code)
}

// ── helpers ──────────────────────────────────────────────────────────────

func (o *OAuthClient) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// parseWWWAuthenticate extracts the resource_metadata URL from a Bearer
// challenge, e.g.:
//
//	Bearer resource_metadata="https://host/.well-known/oauth-protected-resource"
//
// Returns "" if no resource_metadata key is present. Tolerant of multiple
// Bearer params and varied whitespace.
func parseWWWAuthenticate(header string) string {
	// Find resource_metadata="..."
	const key = "resource_metadata="
	i := strings.Index(header, key)
	if i < 0 {
		return ""
	}
	rest := header[i+len(key):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return ""
		}
		return rest[1 : 1+end]
	}
	// Unquoted: take up to comma / whitespace.
	end := strings.IndexAny(rest, ", \t\r\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func expiryFromSeconds(expiresIn int) time.Time {
	if expiresIn <= 0 {
		return time.Now().Add(1 * time.Hour) // sensible default for servers that omit it
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func loadOAuthState(path string) (*oauthState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s oauthState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveOAuthState(path string, s *oauthState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// 0600 because the file holds an active access token + refresh token.
	return os.WriteFile(path, b, 0o600)
}
