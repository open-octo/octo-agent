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

// pageFixture is one session that wrote an HTML entry into a directory
// holding neighbours of several kinds — a script, a model, a stylesheet in a
// subdirectory, a data file, a second HTML page — and, off to the side, a
// file the directory must never reach.
type pageFixture struct {
	srv       *Server
	sessionID string
	root      string
	entry     string
	outside   string
}

func newPageFixture(t *testing.T, entryHTML string) *pageFixture {
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
	write("data.env", "KEY=1")
	write("other.html", "<html><head></head><body><h1>other</h1></body></html>")
	outside := filepath.Join(parent, "outside.js")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := newArtifactSession(t, entry)
	return &pageFixture{srv: srv, sessionID: id, root: root, entry: entry, outside: outside}
}

// grant asks for the page URL the way the panel does: an authenticated local
// browser posting JSON.
func (f *pageFixture) grant(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+f.sessionID+"/artifacts/grant", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	return w
}

var grantURLRe = regexp.MustCompile(`^/_artifacts/([0-9a-f]{32})/$`)

func (f *pageFixture) token(t *testing.T) string {
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
		t.Fatalf("grant url = %q, want /_artifacts/<32hex>/", res.URL)
	}
	return m[1]
}

// get fetches a page path as the local frame would.
func (f *pageFixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	return w
}

func TestArtifactGrant_IssuesAndReusesToken(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	if again := f.token(t); again != tok {
		t.Errorf("re-issue minted a new token %s (was %s); the frame URL would change under the panel", again, tok)
	}
}

// Every client that passes auth gets a grant: a remote browser behind a
// tunnel or a proxy, a LAN client with the key, a UI on an IPv6 literal.
func TestArtifactGrant_IssuedToRemoteClients(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	body, _ := json.Marshal(map[string]string{"path": f.entry})
	cases := []struct {
		name       string
		remoteAddr string
		host       string
		hdr        map[string]string
	}{
		{"LAN peer", "192.168.1.9:50000", "192.168.1.2:8088", nil},
		{"tunnel", "127.0.0.1:50000", "abc.ngrok-free.app", map[string]string{"X-Forwarded-For": "203.0.113.5"}},
		{"IPv6 literal", "[::1]:50000", "[::1]:8088", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+f.sessionID+"/artifacts/grant", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Access-Key", f.srv.accessKey)
			req.RemoteAddr = c.remoteAddr
			req.Host = c.host
			for k, v := range c.hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			f.srv.http.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusOK || !grantURLRe.MatchString(jsonField(t, w, "url")) {
				t.Errorf("status = %d, body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func jsonField(t *testing.T, w *httptest.ResponseRecorder, key string) string {
	t.Helper()
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	s, _ := m[key].(string)
	return s
}

func TestArtifactGrant_OnlyForHTMLTheSessionWrote(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
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
	// A markdown artifact renders in the panel, not as a page.
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

func TestArtifactPage_ServesEntryWithShimAndFiles(t *testing.T) {
	f := newPageFixture(t, `<!DOCTYPE html><html><head><script src="./app.js"></script><script src="https://example.com/lib.js"></script></head><body><h1>hi</h1></body></html>`)
	tok := f.token(t)
	base := "/_artifacts/" + tok + "/"
	ns := artifactNamespace(f.sessionID, f.entry)

	for _, target := range []string{base, base + "index.html"} {
		w := f.get(t, target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body=%s", target, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("GET %s: Content-Type = %q", target, ct)
		}
		body := w.Body.String()
		// The page goes out as written — relative and external references alike.
		if !strings.Contains(body, `<script src="./app.js">`) || !strings.Contains(body, `https://example.com/lib.js`) {
			t.Errorf("GET %s: page references were altered: %s", target, body)
		}
		// The shim sits right after <head>, ahead of the page's own scripts.
		cfg := `window.__octoPage={"download":false,"ns":"` + ns + `"};`
		if i, j := strings.Index(body, cfg), strings.Index(body, `<script src="./app.js">`); i < 0 || i > j || !strings.HasPrefix(body, "<!DOCTYPE html><html><head><script>") {
			t.Errorf("GET %s: shim missing or after the page scripts: %s", target, body)
		}
		if w.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("GET %s: pages carry no CSP", target)
		}
		for h, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff"} {
			if got := w.Header().Get(h); got != want {
				t.Errorf("GET %s: %s = %q, want %q", target, h, got, want)
			}
		}
	}

	// Any file in the directory is served, typed by extension; a second HTML
	// page gets the shim too.
	for target, want := range map[string]string{
		base + "app.js":    "console.log('app')",
		base + "duck.glb":  "glTF",
		base + "sub/x.css": "body{}",
		base + "data.env":  "KEY=1",
	} {
		w := f.get(t, target)
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("GET %s: status = %d, body = %q", target, w.Code, w.Body.String())
		}
	}
	if w := f.get(t, base+"app.js"); !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("app.js Content-Type = %q", w.Header().Get("Content-Type"))
	}
	if w := f.get(t, base+"other.html"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "window.__octoPage=") {
		t.Errorf("second page: status = %d, body = %s", w.Code, w.Body.String())
	}

	// No trailing slash: redirected, so relative references resolve under it.
	w := f.get(t, "/_artifacts/"+tok+"?theme=dark")
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/_artifacts/"+tok+"/?theme=dark" {
		t.Errorf("no slash: status = %d, Location = %q", w.Code, w.Header().Get("Location"))
	}

	// HEAD works for the entry.
	req := httptest.NewRequest(http.MethodHead, base, nil)
	w = httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("HEAD: status = %d, body len = %d", w.Code, w.Body.Len())
	}
}

func TestArtifactPage_NamespaceOutlivesTheToken(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	ns := artifactNamespace(f.sessionID, f.entry)
	f.token(t)
	// A restart forgets every grant; the page's storage namespace stays.
	f.srv.artifactGrants = nil
	tok := f.token(t)
	if w := f.get(t, "/_artifacts/"+tok+"/"); !strings.Contains(w.Body.String(), `"ns":"`+ns+`"`) {
		t.Errorf("namespace changed across grants: %s", w.Body.String())
	}
	if artifactNamespace(f.sessionID, f.entry+"x") == ns || artifactNamespace(f.sessionID+"x", f.entry) == ns {
		t.Error("distinct pages must not share a namespace")
	}
}

func TestArtifactPage_NotFound(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	base := "/_artifacts/" + tok + "/"
	for _, target := range []string{
		base + "missing.js",
		base + "sub",
		base + "../outside.js",
		base + "%2e%2e/outside.js",
		"/_artifacts/0123456789abcdef0123456789abcdef/",
	} {
		if w := f.get(t, target); w.Code != http.StatusNotFound && w.Code != http.StatusTemporaryRedirect {
			t.Errorf("GET %s: status = %d, want 404 (body=%s)", target, w.Code, w.Body.String())
		}
	}
}

func TestArtifactPage_RequiresAuthRemotely(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	tok := f.token(t)
	req := httptest.NewRequest(http.MethodGet, "/_artifacts/"+tok+"/", nil)
	req.RemoteAddr = "203.0.113.9:50000"
	req.Host = "octo.example.com"
	w := httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("keyless remote: status = %d, want 401", w.Code)
	}
	// The frame is same-origin with the UI, so its requests carry the cookie.
	req = httptest.NewRequest(http.MethodGet, "/_artifacts/"+tok+"/app.js", nil)
	req.RemoteAddr = "203.0.113.9:50000"
	req.Host = "octo.example.com"
	req.AddCookie(&http.Cookie{Name: accessKeyCookie, Value: f.srv.accessKey})
	w = httptest.NewRecorder()
	f.srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("remote with cookie: status = %d, want 200", w.Code)
	}
}

// The asset path is assembled from directory entries, so it can only ever
// name something that exists under the root by exact name.
func TestResolveAssetPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.js", "sub/x.css"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(filepath.Dir(root), "outside-"+filepath.Base(root)+".js")
	if err := os.WriteFile(outside, []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	for rel, want := range map[string]string{
		"a.js":      filepath.Join(root, "a.js"),
		"sub/x.css": filepath.Join(root, "sub", "x.css"),
	} {
		got, ok := resolveAssetPath(root, rel)
		if !ok {
			t.Errorf("%q: not resolved", rel)
			continue
		}
		wantReal, _ := filepath.EvalSymlinks(want)
		if got != wantReal {
			t.Errorf("%q: got %q, want %q", rel, got, wantReal)
		}
	}
	for _, rel := range []string{
		"", ".", "..", "../" + filepath.Base(outside), "sub/../a.js", "sub//x.css", "A.js", "a.js/x", "missing.js",
	} {
		if got, ok := resolveAssetPath(root, rel); ok {
			t.Errorf("%q: resolved to %q, want refusal", rel, got)
		}
	}
}

func TestArtifactPage_SymlinkOutOfRootIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	f := newPageFixture(t, "<h1>hi</h1>")
	if err := os.Symlink(f.outside, filepath.Join(f.root, "link.js")); err != nil {
		t.Skip("cannot create symlink:", err)
	}
	tok := f.token(t)
	if w := f.get(t, "/_artifacts/"+tok+"/link.js"); w.Code != http.StatusNotFound {
		t.Errorf("symlink out of root: status = %d, want 404", w.Code)
	}
}

// Files beside the page have no size ceiling: a 3D model or a recording is
// streamed as-is, with Range support for players that seek.
func TestArtifactPage_LargeFilesStream(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	big := filepath.Join(f.root, "big.bin")
	fh, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: nothing near 200 MB actually hits the disk.
	const size = 200 << 20
	if err := fh.Truncate(size); err != nil {
		fh.Close()
		t.Skip("cannot create sparse file:", err)
	}
	fh.Close()
	tok := f.token(t)

	req := httptest.NewRequest(http.MethodHead, "/_artifacts/"+tok+"/big.bin", nil)
	w := httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	if w.Code != http.StatusOK || w.Header().Get("Content-Length") != "209715200" {
		t.Errorf("HEAD 200 MB file: status = %d, Content-Length = %q", w.Code, w.Header().Get("Content-Length"))
	}

	req = httptest.NewRequest(http.MethodGet, "/_artifacts/"+tok+"/big.bin", nil)
	req.Header.Set("Range", "bytes=0-15")
	w = httptest.NewRecorder()
	serveLoopback(f.srv.http.Handler, w, req)
	if w.Code != http.StatusPartialContent || w.Body.Len() != 16 {
		t.Errorf("ranged GET: status = %d, body = %d bytes, want 206 / 16", w.Code, w.Body.Len())
	}
}

func TestInjectAtHead(t *testing.T) {
	s := []byte("<script>x</script>")
	cases := map[string]string{
		`<!DOCTYPE html><html><HEAD lang="en"><title>t</title></head></html>`: `<!DOCTYPE html><html><HEAD lang="en"><script>x</script><title>t</title></head></html>`,
		// <header> is not <head>.
		`<!doctype html><body><header>h</header></body>`: `<!doctype html><script>x</script><body><header>h</header></body>`,
		`<p>a</p>`: `<script>x</script><p>a</p>`,
	}
	for in, want := range cases {
		if got := string(injectAtHead([]byte(in), s)); got != want {
			t.Errorf("injectAtHead(%q) = %q, want %q", in, got, want)
		}
	}
	if got := string(injectAtHead([]byte("<p>a</p>"), nil)); got != "<p>a</p>" {
		t.Errorf("nil script must be a no-op: %s", got)
	}
}
