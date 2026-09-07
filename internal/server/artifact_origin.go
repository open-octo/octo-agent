package server

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── The artifact origin ─────────────────────────────────────────────────────
//
// Agent-written HTML renders from its own origin, `<token>.artifacts.localhost`,
// rather than from a srcdoc frame inside the app's origin. A real origin gives
// the page every browser capability an opaque one withholds — localStorage,
// downloads, fullscreen, pointer lock, relative references to the files beside
// it — while the same-origin policy, not a sandbox flag, keeps it out of the
// app: the page is another site as far as the browser is concerned.
//
// Two independent rules keep the app origin unreachable from here, and either
// alone suffices:
//
//   - This host serves nothing but artifact files. hostRouter dispatches on the
//     Host header ahead of the mux, so /api, /ws and the UI do not exist on it.
//   - isLocalName accepts "localhost" and loopback literals only, never a
//     *.localhost subdomain — so a request the page makes to the app origin
//     carries an Origin that originAllowed rejects, and the loopback exemption
//     answers 403. The access-key cookie is host-only and is never sent here.
//
// The token is not a secret that matters: the hostname resolves to loopback
// only on this machine, so a CDN operator who reads it out of a Referer or
// Origin header has nowhere to use it, and local processes are inside the
// trust boundary already (SECURITY.md). Its TTL is hygiene. What the token
// does bound is *which directory* a page may read — the one holding the entry
// the session wrote — and only files of asset types (tools.ArtifactAssetContentType).
//
// dev-docs/artifact-origin-design.md is the full account.

const (
	artifactHostSuffix    = ".artifacts.localhost"
	artifactHostBare      = "artifacts.localhost"
	artifactGrantTTL      = 24 * time.Hour
	artifactAssetMaxBytes = 64 << 20
)

// artifactGrant ties a hostname label to the one entry document it serves and
// the directory the entry's relative references resolve in.
type artifactGrant struct {
	token     string
	sessionID string
	entry     string // absolute path of the HTML, as the transcript recorded it
	root      string // filepath.Dir(entry)
	lastUsed  time.Time
}

func (g *artifactGrant) expired(now time.Time) bool {
	return now.Sub(g.lastUsed) > artifactGrantTTL
}

// artifactHostLabel splits a Host header into the grant label when it names
// the artifact origin. The bare "artifacts.localhost" and any deeper subdomain
// still count as the artifact host (and 404 inside), so no shape of it ever
// falls through to the app's mux.
func artifactHostLabel(host string) (label string, isArtifactHost bool) {
	h := canonicalHost(host)
	if h == artifactHostBare {
		return "", true
	}
	if !strings.HasSuffix(h, artifactHostSuffix) {
		return "", false
	}
	label = strings.TrimSuffix(h, artifactHostSuffix)
	if label == "" || strings.Contains(label, ".") {
		return "", true
	}
	return label, true
}

// hostRouter sits outside the CORS middleware and the mux: a request for the
// artifact host never reaches either.
func (s *Server) hostRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := artifactHostLabel(r.Host); ok {
			s.serveArtifactOrigin(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalPeer is isLocalRequest minus the Host check, for the artifact host
// itself: its Host is by construction not a local name, so the remaining two
// signals — a loopback peer that no relay forwarded — decide. A LAN client
// that spoofs `Host: <token>.artifacts.localhost` against a wide bind fails
// the peer check; a tunnel fails the forwarding check.
func isLocalPeer(r *http.Request) bool {
	return isLoopbackRemote(r.RemoteAddr) && !isForwarded(r)
}

// ─── POST /api/sessions/{id}/artifacts/grant ────────────────────────────────

type artifactGrantRequest struct {
	Path string `json:"path"`
}

// handleGrantArtifactOrigin issues (or re-issues) the origin URL for one HTML
// artifact of the session. The path must pass the same three gates the
// artifact endpoint applies — previewable type (html only here), absolute,
// written by this session per the transcript — so a grant can never name a
// file the panel could not already serve.
//
// 409 rather than 403 for a non-local client: nothing is forbidden, the
// origin simply does not exist for a browser that cannot resolve *.localhost
// to this machine (a phone over the tunnel, a LAN browser). The panel shows
// "preview available on this machine only" and keeps the code view.
func (s *Server) handleGrantArtifactOrigin(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeError(w, http.StatusConflict, "artifact origin unavailable")
		return
	}
	id := r.PathValue("id")
	var req artifactGrantRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	reqPath := strings.TrimSpace(req.Path)
	if id == "" || reqPath == "" {
		writeError(w, http.StatusBadRequest, "missing session id or path")
		return
	}
	ctype, ok := tools.ArtifactContentType(reqPath)
	if !ok || !strings.HasPrefix(ctype, "text/html") {
		writeError(w, http.StatusNotFound, "not an html artifact")
		return
	}
	abs, ok := resolveArtifactPath(reqPath)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid artifact path")
		return
	}
	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	served, ok := sessionWrotePath(sess, abs)
	if !ok {
		writeError(w, http.StatusNotFound, "path was not written by this session")
		return
	}
	g, err := s.grantArtifact(id, served)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        artifactOriginURL(g.token, r.Host),
		"expires_at": g.lastUsed.Add(artifactGrantTTL).UTC().Format(time.RFC3339),
	})
}

// artifactOriginURL builds the page URL for a grant on the port the client is
// already talking to — the desktop's fixed 8088, an ephemeral test port, or
// whatever `--addr` chose — so nothing here needs configuring.
func artifactOriginURL(token, reqHost string) string {
	portSuffix := ""
	if _, port, err := net.SplitHostPort(reqHost); err == nil && port != "" {
		portSuffix = ":" + port
	}
	return "http://" + token + artifactHostSuffix + portSuffix + "/"
}

// grantArtifact returns the live grant for (session, entry), minting one when
// none exists. Reusing the token across re-issues is what lets a page keep its
// origin — and its localStorage — while the agent keeps rewriting the file.
// Expired grants are swept here rather than by a timer: the map only grows
// while someone is asking for grants.
func (s *Server) grantArtifact(sessionID, entry string) (*artifactGrant, error) {
	s.artifactGrantsMu.Lock()
	defer s.artifactGrantsMu.Unlock()
	now := time.Now()
	if s.artifactGrants == nil {
		s.artifactGrants = map[string]*artifactGrant{}
	}
	for tok, g := range s.artifactGrants {
		if g.expired(now) {
			delete(s.artifactGrants, tok)
			continue
		}
		if g.sessionID == sessionID && g.entry == entry {
			g.lastUsed = now
			return g, nil
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	g := &artifactGrant{
		token:     hex.EncodeToString(b),
		sessionID: sessionID,
		entry:     entry,
		root:      filepath.Dir(entry),
		lastUsed:  now,
	}
	s.artifactGrants[g.token] = g
	return g, nil
}

// lookupGrant resolves a hostname label to its grant, touching the TTL. An
// unknown or expired label is nil — the handler answers 404 either way, so a
// probe learns nothing about which it was.
func (s *Server) lookupGrant(token string) *artifactGrant {
	s.artifactGrantsMu.Lock()
	defer s.artifactGrantsMu.Unlock()
	g := s.artifactGrants[token]
	if g == nil {
		return nil
	}
	now := time.Now()
	if g.expired(now) {
		delete(s.artifactGrants, token)
		return nil
	}
	g.lastUsed = now
	return g
}

// ─── GET http://<token>.artifacts.localhost/… ───────────────────────────────

// setArtifactOriginHeaders applies to every response from the artifact host.
//
//   - no-store: the panel reloads the frame when the agent rewrites the file,
//     and a webview's heuristic caching of a GET 200 without policy is exactly
//     how stale previews used to happen (see Server.api).
//   - no-referrer: the token is harmless off this machine, but there is no
//     reason to hand it to every CDN the page loads from either.
//   - Origin-Agent-Cluster: asks the browser to key the agent cluster on the
//     full origin, so document.domain can never fold a subdomain back into
//     the site.
//   - frame-ancestors: only the app on this machine may embed the page —
//     keeps a hostile site from framing a Light App for clickjacking. A
//     top-level open in a new tab is a navigation, not an embedding, and is
//     unaffected. No IPv6 literal: CSP's host-source grammar has no bracket
//     form, and one malformed source would void the whole directive.
//   - Deliberately no `sandbox` directive: this is the page's own origin, and
//     scripts are the point. The `/api/…/artifacts` direct-open endpoint keeps
//     its CSP sandbox.
func setArtifactOriginHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Origin-Agent-Cluster", "?1")
	h.Set("Content-Security-Policy", "frame-ancestors http://localhost:* http://127.0.0.1:*")
}

func (s *Server) serveArtifactOrigin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !isLocalPeer(r) {
		writeError(w, http.StatusForbidden, "artifact origin is available only from the local machine")
		return
	}
	label, _ := artifactHostLabel(r.Host)
	g := s.lookupGrant(label)
	if label == "" || g == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	setArtifactOriginHeaders(w.Header())

	p := path.Clean("/" + r.URL.Path)
	if p == "/" || p == "/index.html" || p == "/"+filepath.Base(g.entry) {
		s.serveArtifactEntry(w, r, g)
		return
	}
	rel := strings.TrimPrefix(p, "/")
	ctype, ok := tools.ArtifactAssetContentType(rel)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	abs, ok := resolveWithinRoot(g.root, filepath.Join(g.root, filepath.FromSlash(rel)))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if fi.Size() > artifactAssetMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "asset exceeds the 64 MB cap")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ctype)
	// ServeContent handles HEAD and Range — a <video> or a large model file
	// seeks — and never sniffs: the type is already set above.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// serveArtifactEntry sends the entry document through the external-reference
// gate. Theme comes from the frame's own URL (?theme=dark) because the banner
// bakes its colours in and the origin has no other way to learn the app's
// theme.
func (s *Server) serveArtifactEntry(w http.ResponseWriter, r *http.Request, g *artifactGrant) {
	fi, err := os.Stat(g.entry)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if fi.Size() > artifactMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds the 10 MB preview cap")
		return
	}
	src, err := os.ReadFile(g.entry)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	dark := r.URL.Query().Get("theme") == "dark"
	out := gateArtifactHTML(src, dark)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(out)
}

// resolveWithinRoot follows symlinks on both sides and reports the real path
// only when it still lies under root — a link inside the artifact directory
// pointing at ~/.ssh does not make ~/.ssh part of the artifact.
func resolveWithinRoot(root, candidate string) (string, bool) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return real, true
}
