package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// originFixture is one session that wrote an HTML entry into a directory
// holding the kinds of neighbours the asset rules must sort: a script, a
// model, a stylesheet in a subdirectory, a secret, a second HTML file, and
// (off to the side) a file the directory must never reach.
type originFixture struct {
	srv       *Server
	sessionID string
	root      string
	entry     string
	outside   string
}

func newOriginFixture(t *testing.T, entryHTML string) *originFixture {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	parent := t.TempDir()
	root := filepath.Join(parent, "report")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	entry := write("index.html", entryHTML)
	write("app.js", "console.log('app')")
	write("duck.glb", "glTF")
	write("sub/x.css", "body{}")
	write("secret.env", "KEY=1")
	write("other.html", "<h1>other</h1>")
	outside := filepath.Join(parent, "outside.js")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := newArtifactSession(t, entry)
	return &originFixture{srv: srv, sessionID: id, root: root, entry: entry, outside: outside}
}

// grant asks for the origin URL the way the panel does: an authenticated
// local browser posting JSON.
func (f *originFixture) grant(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+f.sessionID+"/artifacts/grant", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	return w
}

var grantURLRe = regexp.MustCompile(`^http://([0-9a-f]{32})\.artifacts\.localhost:8080/$`)

func (f *originFixture) token(t *testing.T) string {
	t.Helper()
	w := f.grant(t, f.entry)
	if w.Code != http.StatusOK {
		t.Fatalf("grant status = %d, body=%s", w.Code, w.Body.String())
	}
	var res struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	m := grantURLRe.FindStringSubmatch(res.URL)
	if m == nil {
		t.Fatalf("grant url = %q, want http://<32hex>.artifacts.localhost:8080/", res.URL)
	}
	return m[1]
}

// originGet fetches a path from the artifact host as the frame on this
// machine would: loopback peer, the token hostname, no forwarding headers.
func (f *originFixture) originGet(t *testing.T, token, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = token + ".artifacts.localhost:8080"
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	return w
}

func TestArtifactGrant_IssuesAndReusesToken(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	if again := f.token(t); again != tok {
		t.Errorf("re-issue minted a new token %s (was %s); the page would lose its origin and storage", again, tok)
	}
}

func TestArtifactGrant_RefusedForNonLocalClients(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	body, _ := json.Marshal(map[string]string{"path": f.entry})
	cases := []struct {
		name       string
		remoteAddr string
		hdr        map[string]string
	}{
		{"LAN peer", "192.168.1.9:50000", nil},
		{"octo tunnel", "127.0.0.1:50000", map[string]string{HeaderForwarded: "1"}},
		{"reverse proxy", "127.0.0.1:50000", map[string]string{"X-Forwarded-For": "203.0.113.5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+f.sessionID+"/artifacts/grant", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Access-Key", f.srv.accessKey)
			req.RemoteAddr = c.remoteAddr
			req.Host = "127.0.0.1:8080"
			for k, v := range c.hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			f.srv.http.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// frame-ancestors cannot name an IPv6 literal, so a UI reached over [::1] is
// told the origin is unavailable rather than handed a frame the browser will
// refuse to show.
func TestArtifactGrant_RefusedForIPv6LiteralHost(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	body, _ := json.Marshal(map[string]string{"path": f.entry})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+f.sessionID+"/artifacts/grant", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "[::1]:50000"
	req.Host = "[::1]:8080"
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("IPv6 literal host: status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestArtifactGrant_OnlyForHTMLTheSessionWrote(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	// Exists beside the entry but was never written by the session.
	if w := f.grant(t, filepath.Join(f.root, "other.html")); w.Code != http.StatusNotFound {
		t.Errorf("unwritten html: status = %d, want 404", w.Code)
	}
	// Written by another session entirely.
	other := newArtifactSession(t, filepath.Join(f.root, "other.html"))
	body, _ := json.Marshal(map[string]string{"path": f.entry})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+other+"/artifacts/grant", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("other session's entry: status = %d, want 404", w.Code)
	}
	// A markdown artifact renders in the panel, not on the origin.
	md := filepath.Join(f.root, "notes.md")
	if err := os.WriteFile(md, []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mdSession := newArtifactSession(t, md)
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+mdSession+"/artifacts/grant", strings.NewReader(`{"path":`+strconvQuote(md)+`}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("markdown: status = %d, want 404", w.Code)
	}
	// A denied write grants nothing — same rule as the artifact endpoint.
	denied := newArtifactSessionWith(t, "write_file", writeDenied, f.entry)
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+denied+"/artifacts/grant", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("denied write: status = %d, want 404", w.Code)
	}
	// Relative input never reaches the transcript lookup.
	if w := f.grant(t, "index.html"); w.Code != http.StatusBadRequest {
		t.Errorf("relative path: status = %d, want 400", w.Code)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestArtifactOrigin_ServesEntryAndAssets(t *testing.T) {
	f := newOriginFixture(t, `<!DOCTYPE html><html><head><script src="./app.js"></script></head><body><h1>hi</h1></body></html>`)
	tok := f.token(t)

	for _, target := range []string{"/", "/index.html"} {
		w := f.originGet(t, tok, target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body=%s", target, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("GET %s: Content-Type = %q", target, ct)
		}
		if !strings.Contains(w.Body.String(), `<script src="./app.js">`) {
			t.Errorf("GET %s: relative script reference was stripped: %s", target, w.Body.String())
		}
		if strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("GET %s: the origin must not sandbox its own page", target)
		}
		for h, want := range map[string]string{
			"Cache-Control":           "no-store",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "no-referrer",
			"Origin-Agent-Cluster":    "?1",
			"Content-Security-Policy": "frame-ancestors http://localhost:* http://127.0.0.1:*",
		} {
			if got := w.Header().Get(h); got != want {
				t.Errorf("GET %s: %s = %q, want %q", target, h, got, want)
			}
		}
	}

	assets := map[string]string{
		"/app.js":    "text/javascript; charset=utf-8",
		"/duck.glb":  "model/gltf-binary",
		"/sub/x.css": "text/css; charset=utf-8",
	}
	for target, ct := range assets {
		w := f.originGet(t, tok, target)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d", target, w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != ct {
			t.Errorf("GET %s: Content-Type = %q, want %q", target, got, ct)
		}
	}
	if w := f.originGet(t, tok, "/app.js"); w.Body.String() != "console.log('app')" {
		t.Errorf("asset body = %q", w.Body.String())
	}

	// Host header shapes the router must canonicalise: case, a trailing dot.
	for _, host := range []string{strings.ToUpper(tok) + ".Artifacts.LOCALHOST:8080", tok + ".artifacts.localhost.:8080"} {
		req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Host = host
		w := httptest.NewRecorder()
		f.srv.http.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Host %s: status = %d, want 200", host, w.Code)
		}
	}

	// HEAD works for the entry and for assets.
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = tok + ".artifacts.localhost:8080"
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("HEAD /: status = %d, body len = %d", w.Code, w.Body.Len())
	}
}

func TestArtifactOrigin_EntryGoesThroughTheGate(t *testing.T) {
	f := newOriginFixture(t, `<html><head><script src="https://evil.example.com/x.js"></script></head><body><h1>hi</h1></body></html>`)
	tok := f.token(t)
	w := f.originGet(t, tok, "/")
	if strings.Contains(w.Body.String(), "evil.example.com") {
		t.Errorf("external script survived: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "#fff8e1") {
		t.Errorf("light banner missing: %s", w.Body.String())
	}
	if w := f.originGet(t, tok, "/?theme=dark"); !strings.Contains(w.Body.String(), "#2b2111") {
		t.Errorf("dark banner missing: %s", w.Body.String())
	}
}

func TestArtifactOrigin_RefusesWhatIsNotAnAsset(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	for _, target := range []string{
		"/secret.env",        // not an asset type
		"/other.html",        // a second HTML — one entry per grant
		"/missing.js",        // not on disk
		"/sub",               // a directory
		"/../outside.js",     // traversal (cleaned, then not under root)
		"/api/health",        // no API on this host
		"/api/sessions",      // no API on this host
		"/assets/index.js",   // no UI on this host
		"/%2e%2e/outside.js", // encoded traversal
	} {
		if w := f.originGet(t, tok, target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404 (body=%s)", target, w.Code, w.Body.String())
		}
	}
}

func TestArtifactOrigin_SymlinkOutOfRootIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	f := newOriginFixture(t, "<h1>hi</h1>")
	if err := os.Symlink(f.outside, filepath.Join(f.root, "link.js")); err != nil {
		t.Skip("cannot create symlink:", err)
	}
	tok := f.token(t)
	if w := f.originGet(t, tok, "/link.js"); w.Code != http.StatusNotFound {
		t.Errorf("symlink out of root: status = %d, want 404", w.Code)
	}
}

func TestArtifactOrigin_UnknownTokenAndBareHostAre404(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	f.token(t)
	for _, host := range []string{
		"0123456789abcdef0123456789abcdef.artifacts.localhost:8080",
		"artifacts.localhost:8080",
		"a.b.artifacts.localhost:8080",
	} {
		for _, target := range []string{"/", "/api/health", "/api/version", "/ws"} {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.RemoteAddr = "127.0.0.1:1"
			req.Host = host
			w := httptest.NewRecorder()
			f.srv.http.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("Host %s GET %s: status = %d, want 404", host, target, w.Code)
			}
		}
	}
}

func TestArtifactOrigin_OnlyLocalPeersOnlyGET(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	host := tok + ".artifacts.localhost:8080"

	// A LAN client spoofing the artifact Host against a wide bind.
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.RemoteAddr = "192.168.1.9:50000"
	req.Host = host
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("LAN peer: status = %d, want 403", w.Code)
	}

	// A relayed request dials from loopback but carries the relay's marker.
	req = httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = host
	req.Header.Set(HeaderForwarded, "1")
	w = httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("forwarded: status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/app.js", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = host
	w = httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", w.Code)
	}
}

func TestArtifactOrigin_AssetSizeCap(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	big := filepath.Join(f.root, "big.bin")
	fh, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: no 65 MB actually hit the disk.
	if err := fh.Truncate(artifactAssetMaxBytes + 1); err != nil {
		fh.Close()
		t.Skip("cannot create sparse file:", err)
	}
	fh.Close()
	tok := f.token(t)
	if w := f.originGet(t, tok, "/big.bin"); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized asset: status = %d, want 413", w.Code)
	}
}

// The other half of the boundary: a page on the artifact origin calling the
// app origin. Its Origin header is not a local name, so the loopback exemption
// refuses it as CSRF; only a presented key still passes, exactly as for any
// other foreign origin.
func TestArtifactOrigin_OriginIsForeignToTheAppAPI(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	origin := "http://" + tok + ".artifacts.localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("artifact-origin page calling /api: status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if acao := w.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Errorf("CORS reflected the artifact origin: %q", acao)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", origin)
	req.Header.Set("X-Access-Key", f.srv.accessKey)
	w = httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("with the key: status = %d, want 200 (key precedence unchanged)", w.Code)
	}
}

// The WebSocket route sits behind the same gate as the JSON API, and a
// `--cors '*'` configuration must not widen it: the wildcard is never honoured
// by the auth predicates, only reflected as a CORS header.
func TestArtifactOrigin_OriginIsForeignToWSAndUnderWildcardCORS(t *testing.T) {
	f := newOriginFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	origin := "http://" + tok + ".artifacts.localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("artifact-origin page reaching /ws: status = %d, want 403", w.Code)
	}

	wild := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, CORSOrigins: []string{"*"}})
	req = httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", origin)
	w = httptest.NewRecorder()
	wild.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("--cors '*': artifact-origin page calling /api: status = %d, want 403", w.Code)
	}
}

func TestArtifactHostLabel(t *testing.T) {
	cases := []struct {
		host   string
		label  string
		isHost bool
	}{
		{"abc.artifacts.localhost:8080", "abc", true},
		{"ABC.Artifacts.LOCALHOST", "abc", true},
		{"abc.artifacts.localhost.", "abc", true},
		{"artifacts.localhost", "", true},
		{"a.b.artifacts.localhost", "", true},
		{"localhost:8080", "", false},
		{"127.0.0.1:8080", "", false},
		{"evil-artifacts.localhost", "", false},
		{"abc.artifacts.localhost.evil.com", "", false},
	}
	for _, c := range cases {
		label, isHost := artifactHostLabel(c.host)
		if label != c.label || isHost != c.isHost {
			t.Errorf("artifactHostLabel(%q) = (%q, %v), want (%q, %v)", c.host, label, isHost, c.label, c.isHost)
		}
	}
}
