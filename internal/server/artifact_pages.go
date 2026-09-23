package server

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Artifact and Light App pages ───────────────────────────────────────────
//
// Agent-written HTML — a session artifact or a saved Light App — renders in an
// iframe loaded by src from a path on the app's own origin: /_artifacts/<token>/
// and /_apps/<slug>/ (lightapp_pages.go). Same origin on purpose: the page
// works however the server is reached — this machine, the desktop shell, a
// tunnel, a domain, a bare IP — with no second hostname to resolve, and its
// relative references load the files beside it.
//
// What the page must not do is interfere with the UI. The iframe is its own
// document, so styles and globals stay apart; the one thing the two share by
// accident is localStorage, and page_shim.js — injected ahead of every page
// script — wraps it under a per-page key prefix. The page is otherwise NOT
// isolated from the app: same-origin script can read the access key and call
// the API. That is the accepted trade (dev-docs/same-origin-artifacts-design.md,
// SECURITY.md).

const (
	artifactGrantTTL = 24 * time.Hour
	// The entry document is read whole so the shim can be spliced in; every
	// other file is streamed and carries no cap.
	artifactEntryMaxBytes = 64 << 20
)

// artifactGrant ties a URL token to the one entry document it serves and the
// directory the entry's relative references resolve in.
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

// artifactNamespace is the localStorage namespace of a session artifact:
// derived from the session and the entry path rather than the token, so the
// page keeps its storage when the grant is re-issued or the server restarts.
func artifactNamespace(sessionID, entry string) string {
	sum := sha256.Sum256([]byte(sessionID + "\n" + entry))
	return hex.EncodeToString(sum[:])[:16]
}

// ─── POST /api/sessions/{id}/artifacts/grant ────────────────────────────────

type artifactGrantRequest struct {
	Path string `json:"path"`
}

// handleGrantArtifactOrigin issues (or re-issues) the page URL for one HTML
// artifact of the session. The path must pass the same three gates the
// artifact endpoint applies — previewable type (html only here), absolute,
// written by this session per the transcript — so a grant can never name a
// file the panel could not already serve.
func (s *Server) handleGrantArtifactOrigin(w http.ResponseWriter, r *http.Request) {
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
		"url":        "/_artifacts/" + g.token + "/",
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

// grantArtifact returns the live grant for (session, entry), minting one when
// none exists, together with its current expiry. Reusing the token across
// re-issues keeps the frame's URL stable while the agent keeps rewriting the
// file. Expired grants are swept here rather than by a timer: the map only
// grows while someone is asking for grants.
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

// lookupGrant resolves a token to its grant, touching the TTL. An unknown or
// expired token is nil — the handler answers 404 either way.
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

// ─── GET /_artifacts/{token}/{path...} ──────────────────────────────────────

func (s *Server) handleArtifactPage(w http.ResponseWriter, r *http.Request) {
	g := s.lookupGrant(r.PathValue("token"))
	if g == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rel := r.PathValue("path")
	shim := s.pageShim(artifactNamespace(g.sessionID, g.entry), false)
	if rel == "" || rel == "index.html" || rel == filepath.Base(g.entry) {
		servePageEntry(w, r, g.entry, shim)
		return
	}
	servePageFile(w, r, g.root, rel, shim)
}

// redirectToSlash sends /_artifacts/<token> and /_apps/<slug> to the same path
// with a trailing slash, so the page's relative references resolve under it.
func redirectToSlash(w http.ResponseWriter, r *http.Request) {
	// Escaped, so a `#` or `?` in the segment stays part of the path.
	target := r.URL.EscapedPath() + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// ─── Serving a page's files ─────────────────────────────────────────────────

// setPageHeaders applies to every page response. no-store because the panel
// reloads the frame when the agent rewrites the file, and a webview's
// heuristic caching of a GET 200 without policy is exactly how stale previews
// used to happen (see Server.api).
func setPageHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
}

// servePageFile serves one file from under root by its relative path. Any
// regular file is served — the page may read whatever sits in its directory —
// typed by extension; an HTML file gets the shim like the entry does, so a
// second page linked from the first is just as well-behaved. No size cap: the
// bytes are streamed, and a model or a recording can run to hundreds of MB.
func servePageFile(w http.ResponseWriter, r *http.Request, root, rel string, shim []byte) {
	abs, ok := resolveAssetPath(root, path.Clean("/" + rel)[1:])
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if ext := strings.ToLower(filepath.Ext(abs)); ext == ".html" || ext == ".htm" {
		servePageEntry(w, r, abs, shim)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	setPageHeaders(w.Header())
	ctype := mime.TypeByExtension(filepath.Ext(abs))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	// ServeContent handles HEAD and Range — a <video> or a large model file
	// seeks — and never sniffs: the type is already set above.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// servePageEntry sends an HTML document with the shim spliced in ahead of the
// page's own scripts.
func servePageEntry(w http.ResponseWriter, r *http.Request, entry string, shim []byte) {
	fi, err := os.Stat(entry)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// A ceiling only because the file is read whole for the splice; a Light
	// App that inlines its data runs past 10 MB in the wild, and anything
	// larger belongs beside the page.
	if fi.Size() > artifactEntryMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "page exceeds the 64 MB cap")
		return
	}
	src, err := os.ReadFile(entry)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	out := injectAtHead(src, shim)
	setPageHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(out)
}

//go:embed page_shim.js
var pageShimJS string

// pageShim is the script pair spliced into every page: the configuration
// literal, then the embedded shim. json.Marshal escapes `<`, `>` and `&`, so
// a namespace can never close the script element early. download turns on the
// desktop download bridge (Light Apps in the desktop shell only).
func (s *Server) pageShim(ns string, download bool) []byte {
	cfg, _ := json.Marshal(map[string]any{
		"ns":       ns,
		"download": download,
	})
	return []byte("<script>window.__octoPage=" + string(cfg) + ";</script><script>" + pageShimJS + "</script>")
}

var (
	headOpenRe = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)
	doctypeRe  = regexp.MustCompile(`(?i)<!doctype\b[^>]*>`)
)

// injectAtHead places script right after the <head> start tag, so it runs
// before any script the page declares; without one, right after the doctype,
// and only for a bare fragment at the very top — anything ahead of the doctype
// would drop the page into quirks mode. A byte scan rather than a parse on
// purpose: the page goes out as its own bytes. The one shape it gets wrong: a
// `<head>` inside a comment or a script string ahead of the real tag; accepted
// as a corner the parse-free approach pays for.
func injectAtHead(doc, script []byte) []byte {
	if len(script) == 0 {
		return doc
	}
	for _, re := range []*regexp.Regexp{headOpenRe, doctypeRe} {
		if loc := re.FindIndex(doc); loc != nil {
			out := append([]byte{}, doc[:loc[1]]...)
			out = append(out, script...)
			return append(out, doc[loc[1]:]...)
		}
	}
	return append(append([]byte{}, script...), doc...)
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
// only when it still lies under root — a link in the page's directory pointing
// elsewhere does not make elsewhere part of the page's directory.
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
