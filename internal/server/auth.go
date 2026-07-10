package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/config"
)

// Access-key authentication. Every /api/* route except /api/health and
// /api/version, plus /ws, is gated by requireAuth: a request passes by
// presenting the access key, or by the loopback exemption — a loopback peer
// with a local Host header and a local (or absent) Origin. The Host check
// stops DNS rebinding, the Origin check stops CSRF; both reach loopback from
// the user's own browser, which is why the bare exemption is not enough.
// See dev-docs/serve-auth-design.md.

// accessKeyCookie is the cookie the web UI seeds after a successful key
// prompt; SameSite=Strict (set client-side) keeps it off cross-site requests.
const accessKeyCookie = "octo_access_key"

// resolveAccessKey picks the access key by precedence: explicit server
// config (CLI flag), OCTO_ACCESS_KEY env var, config.yml, else a freshly
// generated 256-bit key. generated reports that the key is new this start —
// the caller persists it so browser logins survive restarts.
func resolveAccessKey(cfgValue string, fileCfg config.Config) (key string, generated bool) {
	if cfgValue != "" {
		return cfgValue, false
	}
	if env := os.Getenv("OCTO_ACCESS_KEY"); env != "" {
		return env, false
	}
	if fileCfg.AccessKey != "" {
		return fileCfg.AccessKey, false
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is essentially unheard of; a timestamp key
		// keeps the server usable rather than refusing to start.
		return fmt.Sprintf("octo-%d", time.Now().UnixNano()), true
	}
	return hex.EncodeToString(b), true
}

// AccessKey returns the shared secret that authenticates Web UI and API
// requests from non-loopback clients.
func (s *Server) AccessKey() string {
	return s.accessKey
}

// keyFromRequest extracts the presented key. The access_key query parameter
// is honored only on /ws (the browser WebSocket API cannot set headers);
// keeping it off every other route keeps the key out of request URLs.
func keyFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if k := r.Header.Get("X-Access-Key"); k != "" {
		return k
	}
	if c, err := r.Cookie(accessKeyCookie); err == nil && c.Value != "" {
		// PathUnescape, not QueryUnescape: the frontend encodes with
		// encodeURIComponent (%XX only), and QueryUnescape would corrupt a
		// raw '+' in a user-chosen key set by a non-browser client.
		if v, uerr := url.PathUnescape(c.Value); uerr == nil {
			return v
		}
		return c.Value
	}
	if r.URL.Path == "/ws" {
		return r.URL.Query().Get("access_key")
	}
	return ""
}

// validateAccessKey reports whether the request presents the configured
// access key. An empty configured key matches nothing, so a failed key
// resolution can never fail open.
func (s *Server) validateAccessKey(r *http.Request) bool {
	if s.accessKey == "" {
		return false
	}
	key := keyFromRequest(r)
	if key == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(s.accessKey)) == 1
}

// isLoopbackRemote reports whether the request's peer address is loopback.
// net.ParseIP + IsLoopback covers 127.0.0.0/8, ::1, and the IPv4-mapped
// ::ffff:127.0.0.1 — forms a string comparison would miss. X-Forwarded-For
// is deliberately never consulted: a spoofable header must not widen the
// exemption.
func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// canonicalHost lowercases a Host header (or origin host) and strips the
// port, IPv6 brackets, and any trailing dot.
func canonicalHost(hostport string) string {
	h := strings.ToLower(strings.TrimSpace(hostport))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	return strings.TrimSuffix(h, ".")
}

// isLocalName reports whether a canonical host names the local machine's
// loopback: "localhost" or a loopback IP literal. "0.0.0.0" parses but is
// not loopback and does not qualify.
func isLocalName(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isVSCodeWebviewOrigin reports whether origin was sent by a VS Code webview
// panel. Each webview gets a fresh vscode-webview://<uuid> origin per window
// and reload, so — unlike a fixed app scheme origin — it can never be pinned
// as a literal --cors allowlist entry; the scheme itself is the only stable
// signal. A hostile web page cannot forge this: browsers control the Origin
// header, and only an actual local VS Code webview process can send this
// scheme, so treating it as always-allowed is no wider than the existing
// loopback exemption.
func isVSCodeWebviewOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "vscode-webview"
}

// hostAllowed is the DNS-rebinding gate for the loopback exemption: the
// Host header must name the local machine or a --cors allowlisted host. A
// rebound page's request reaches 127.0.0.1 but carries Host: attacker.com.
// A literal "*" CORS origin is never honored here — it would void the gate
// for anyone who set --cors '*' to silence a CORS error.
func (s *Server) hostAllowed(host string) bool {
	h := canonicalHost(host)
	if isLocalName(h) {
		return true
	}
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" {
			continue
		}
		if u, err := url.Parse(o); err == nil && u.Host != "" && canonicalHost(u.Host) == h {
			return true
		}
	}
	return false
}

// originAllowed is the CSRF gate for the loopback exemption: a browser-sent
// Origin must be local or an exact --cors allowlist entry (never "*").
// "null" (sandboxed iframes, some redirect chains) is rejected. Non-browser
// clients send no Origin and are not gated here.
func (s *Server) originAllowed(origin string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "vscode-webview" || isLocalName(canonicalHost(u.Host)) {
		return true
	}
	for _, o := range s.cfg.CORSOrigins {
		if o != "*" && o == origin {
			return true
		}
	}
	return false
}

// requireAuth gates a handler: a valid key always passes; otherwise only
// the hardened loopback exemption does. Routes register through Server.api,
// which applies this wrapper in one place.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.validateAccessKey(r) {
			next(w, r)
			return
		}
		if !isLoopbackRemote(r.RemoteAddr) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusForbidden, "forbidden host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
			writeError(w, http.StatusForbidden, "forbidden origin")
			return
		}
		next(w, r)
	}
}

// wsCheckOrigin is the WebSocket upgrader's CheckOrigin. A key-authenticated
// dial passes regardless of Origin (same precedence as HTTP); otherwise the
// loopback Origin predicate decides — the same gate requireAuth already
// applied to the upgrade request. Absent Origin (non-browser client) passes.
func (s *Server) wsCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if s.validateAccessKey(r) {
		return true
	}
	return s.originAllowed(origin)
}
