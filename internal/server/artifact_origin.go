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
		if _, ok := lightAppHostLabel(r.Host); ok {
			s.serveLightAppOrigin(w, r)
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
	// CSP's host-source grammar has no bracket form, so frame-ancestors
	// (setArtifactOriginHeaders) cannot name an IPv6 literal: a UI opened at
	// http://[::1]:8088 would obtain a grant whose frame the browser then
	// refuses to display, silently. Treat that host as one the origin is
	// unavailable to, and the panel shows the local-only notice instead.
	if ip := net.ParseIP(canonicalHost(r.Host)); ip != nil && ip.To4() == nil {
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
	g, expires, err := s.grantArtifact(id, served)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        artifactOriginURL(g.token, r.Host),
		"expires_at": expires.UTC().Format(time.RFC3339),
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
// none exists, together with its current expiry. Reusing the token across
// re-issues is what lets a page keep its origin — and its localStorage — while
// the agent keeps rewriting the file. Expired grants are swept here rather than
// by a timer: the map only grows while someone is asking for grants.
//
// The expiry is computed under the lock: lastUsed is also written by
// lookupGrant on the request goroutines of the frame, so a caller must not read
// it once the lock is released. The other fields never change after minting.
func (s *Server) grantArtifact(sessionID, entry string) (*artifactGrant, time.Time, error) {
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
			return g, now.Add(artifactGrantTTL), nil
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, time.Time{}, err
	}
	g := &artifactGrant{
		token:     hex.EncodeToString(b),
		sessionID: sessionID,
		entry:     entry,
		root:      filepath.Dir(entry),
		lastUsed:  now,
	}
	s.artifactGrants[g.token] = g
	return g, now.Add(artifactGrantTTL), nil
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
//   - Content-Security-Policy (artifactCSP): the page may load from and talk
//     to its own origin and the allowlisted CDNs, nothing else — the run-time
//     half of the allowlist the gate applies to static references. Its
//     frame-ancestors clause lets only the app on this machine embed the page,
//     which keeps a hostile site from framing a Light App for clickjacking; a
//     top-level open in a new tab is a navigation, not an embedding, and is
//     unaffected. Deliberately no `sandbox` directive: this is the page's own
//     origin, and scripts are the point. The `/api/…/artifacts` direct-open
//     endpoint keeps its CSP sandbox.
func setArtifactOriginHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Origin-Agent-Cluster", "?1")
	h.Set("Content-Security-Policy", artifactCSP)
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
		serveArtifactEntry(w, r, g.entry, nil)
		return
	}
	serveArtifactAsset(w, r, g.root, strings.TrimPrefix(p, "/"))
}

// serveArtifactAsset serves one file from under root by its cleaned relative
// path: asset types only, no escaping the root through `..` or a symlink, a
// size cap, and the type set explicitly so nothing is sniffed. Shared by the
// artifact origin and the Light App origin.
func serveArtifactAsset(w http.ResponseWriter, r *http.Request, root, rel string) {
	ctype, ok := tools.ArtifactAssetContentType(rel)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	abs, ok := resolveAssetPath(root, rel)
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

// serveArtifactEntry sends an entry document through the external-reference
// gate, with an optional script appended before </body> (the Light App
// bridge; nil for session artifacts). Theme comes from the frame's own URL
// (?theme=dark) because the banner bakes its colours in and the origin has no
// other way to learn the app's theme.
func serveArtifactEntry(w http.ResponseWriter, r *http.Request, entry string, inject []byte) {
	fi, err := os.Stat(entry)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// The same ceiling as an asset, not the 10 MB of the srcdoc-era preview
	// endpoint: that cap paid for base64 inflation and a copy in the srcdoc
	// attribute, neither of which applies here, and a Light App that embeds
	// its data or a model inline (12 MB in the wild) used to load fine from
	// the old JSON endpoint, which had no cap at all. The file is still read
	// whole, since the gate parses it.
	if fi.Size() > artifactAssetMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "page exceeds the 64 MB cap")
		return
	}
	src, err := os.ReadFile(entry)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	dark := r.URL.Query().Get("theme") == "dark"
	out := injectBeforeBody(gateArtifactHTML(src, dark), inject)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(out)
}

// resolveAssetPath turns a cleaned, slash-separated relative path into the
// on-disk path of that file under root, or reports that no such file is
// served. It walks the request one segment at a time and, at each step, takes
// the directory entry whose name matches — so the path it returns is assembled
// from names the filesystem reported, never from request bytes, and a segment
// that is not a real entry (`..`, `.`, an empty one, a name with a separator
// inside) simply matches nothing. The walked path is then checked against the
// root through symlinks like everything else (resolveWithinRoot).
func resolveAssetPath(root, rel string) (string, bool) {
	cur := root
	segs := strings.Split(rel, "/")
	for i, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return "", false
		}
		found := ""
		for _, e := range entries {
			if e.Name() == seg {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return "", false
		}
		cur = filepath.Join(cur, found)
		if i < len(segs)-1 {
			// An intermediate segment has to be a directory (or a link to one).
			if fi, err := os.Stat(cur); err != nil || !fi.IsDir() {
				return "", false
			}
		}
	}
	return resolveWithinRoot(root, cur)
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
