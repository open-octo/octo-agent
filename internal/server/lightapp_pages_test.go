package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newLightAppFixture saves one Light App under the scratch HOME the way the
// agent does — a directory with manifest.json and index.html — plus a script
// beside it.
func newLightAppFixture(t *testing.T, cfg Config, indexHTML string) *Server {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	dir := filepath.Join(lightAppsDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"manifest.json": `{"slug":"demo","name":"Demo","mount":"view","future_field":{"a":1}}`,
		"index.html":    indexHTML,
		"app.js":        "console.log('demo')",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mustServer(t, cfg)
}

// cleanedRedirect reports whether the mux answered a path holding `..` by
// redirecting to its cleaned form — 301 on Go 1.25, 307 on later releases —
// which then 404s on its own. The target must no longer contain `..`.
func cleanedRedirect(w *httptest.ResponseRecorder) bool {
	if w.Code != http.StatusMovedPermanently && w.Code != http.StatusTemporaryRedirect {
		return false
	}
	loc := w.Header().Get("Location")
	return loc != "" && !strings.Contains(loc, "..")
}

func localGet(srv *Server, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	return w
}

func remoteGet(srv *Server, target string, cookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "abc.ngrok-free.app"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	if cookie {
		req.AddCookie(&http.Cookie{Name: accessKeyCookie, Value: srv.accessKey})
	}
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	return w
}

func TestLightAppPage_ServesEntryWithShimAndFiles(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false},
		`<!DOCTYPE html><html><head><script src="./app.js"></script></head><body><h1>demo</h1></body></html>`)

	for _, target := range []string{"/_apps/demo/", "/_apps/demo/index.html"} {
		w := localGet(srv, target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body=%s", target, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, "<h1>demo</h1>") || !strings.Contains(body, `<script src="./app.js">`) {
			t.Errorf("GET %s: page content or relative script lost: %s", target, body)
		}
		// The slug is the namespace; the download half is off under plain serve.
		cfg := `window.__octoPage={"download":false,"ns":"demo"};`
		if i := strings.Index(body, cfg); i < 0 || i > strings.Index(body, `<script src="./app.js">`) {
			t.Errorf("GET %s: shim missing or after the page scripts: %s", target, body)
		}
		if !strings.Contains(body, "'octo.page.' + encodeURIComponent(NS) + ':'") {
			t.Errorf("GET %s: shim script not injected", target)
		}
	}
	if w := localGet(srv, "/_apps/demo/app.js"); w.Code != http.StatusOK || w.Body.String() != "console.log('demo')" {
		t.Errorf("GET app.js: status = %d, body = %q", w.Code, w.Body.String())
	}
	if w := localGet(srv, "/_apps/demo/manifest.json"); w.Code != http.StatusOK {
		t.Errorf("GET manifest.json: status = %d; any file in the directory is served", w.Code)
	}
	for _, target := range []string{"/_apps/nope/", "/_apps/demo/missing.js", "/_apps/demo/../other/index.html"} {
		// The mux cleans `..` with a redirect to the cleaned path, which then 404s.
		if w := localGet(srv, target); w.Code != http.StatusNotFound && !cleanedRedirect(w) {
			t.Errorf("GET %s: status = %d, want 404", target, w.Code)
		}
	}
	if w := localGet(srv, "/_apps/demo?v=2"); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/_apps/demo/?v=2" {
		t.Errorf("no slash: status = %d, Location = %q", w.Code, w.Header().Get("Location"))
	}
}

func TestLightAppPage_DesktopTurnsOnTheDownloadBridge(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false, Native: &fakeNative{}}, "<h1>demo</h1>")
	w := localGet(srv, "/_apps/demo/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// No <head>, no doctype: the shim goes first.
	if !strings.HasPrefix(w.Body.String(), `<script>window.__octoPage={"download":true,"ns":"demo"};</script>`) {
		t.Errorf("desktop must turn the download bridge on: %s", w.Body.String())
	}
}

// A Light App that inlines its data can run well past 10 MB.
func TestLightAppPage_LargeEntryIsServed(t *testing.T) {
	big := strings.Repeat(" ", 12<<20) + "<h1>big</h1>"
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, big)
	w := localGet(srv, "/_apps/demo/")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<h1>big</h1>") {
		t.Fatalf("12 MB entry: status = %d", w.Code)
	}
	over := strings.Repeat(" ", artifactEntryMaxBytes+1)
	srv2 := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, over)
	if w := localGet(srv2, "/_apps/demo/"); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over-cap entry: status = %d, want 413", w.Code)
	}
}

// Through a tunnel the page is served like /api: the cookie the UI already
// holds authenticates the frame.
func TestLightAppPage_RemoteNeedsTheKey(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	if w := remoteGet(srv, "/_apps/demo/", false); w.Code != http.StatusUnauthorized {
		t.Errorf("keyless remote: status = %d, want 401", w.Code)
	}
	if w := remoteGet(srv, "/_apps/demo/app.js", true); w.Code != http.StatusOK {
		t.Errorf("remote with cookie: status = %d, want 200", w.Code)
	}
}

func setPublic(t *testing.T, srv *Server, public bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"public":false}`
	if public {
		body = `{"public":true}`
	}
	req := httptest.NewRequest(http.MethodPut, "/api/light-apps/demo/public", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	return w
}

func TestLightAppPage_PublicSkipsAuth(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")

	w := setPublic(t, srv, true)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT public: status = %d, body=%s", w.Code, w.Body.String())
	}
	var m lightAppManifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || !m.Public || m.Name != "Demo" || m.Mount != "view" {
		t.Errorf("PUT response = %+v (err %v)", m, err)
	}
	for _, target := range []string{"/_apps/demo/", "/_apps/demo/app.js"} {
		if w := remoteGet(srv, target, false); w.Code != http.StatusOK {
			t.Errorf("public %s, keyless remote: status = %d, want 200", target, w.Code)
		}
	}

	// Every other key of the manifest survives the rewrite, unknown ones too.
	data, err := os.ReadFile(filepath.Join(lightAppsDir(), "demo", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["public"] != true || raw["name"] != "Demo" || raw["mount"] != "view" || raw["future_field"] == nil {
		t.Errorf("manifest after PUT = %s", data)
	}

	// Off again takes effect on the next request.
	if w := setPublic(t, srv, false); w.Code != http.StatusOK {
		t.Fatalf("PUT private: status = %d", w.Code)
	}
	if w := remoteGet(srv, "/_apps/demo/", false); w.Code != http.StatusUnauthorized {
		t.Errorf("after turning public off: status = %d, want 401", w.Code)
	}
	data, _ = os.ReadFile(filepath.Join(lightAppsDir(), "demo", "manifest.json"))
	if strings.Contains(string(data), `"public"`) {
		t.Errorf("public:false should drop the key: %s", data)
	}
}

// Making one app public opens that app's directory and nothing else: not a
// sibling app, not a path that climbs out, not a link that points out.
func TestLightAppPage_PublicStaysInsideItsApp(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	other := filepath.Join(lightAppsDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"manifest.json": `{"slug":"other","name":"Other"}`, "index.html": "<h1>other</h1>"} {
		if err := os.WriteFile(filepath.Join(other, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(other, "index.html"), filepath.Join(lightAppsDir(), "demo", "peek.html")); err != nil {
			t.Fatal(err)
		}
	}
	if w := setPublic(t, srv, true); w.Code != http.StatusOK {
		t.Fatalf("PUT public: status = %d", w.Code)
	}

	for _, target := range []string{
		"/_apps/other/",
		"/_apps/demo%2F..%2Fother/",
		"/_apps/demo/..%2Fother%2Findex.html",
		"/_apps/demo/peek.html",
	} {
		w := remoteGet(srv, target, false)
		if w.Code == http.StatusOK {
			t.Errorf("public demo, keyless %s: status 200, body %q", target, w.Body.String())
		}
	}
	// The mux cleans a literal `..` into a redirect; following it lands on the
	// private app, which still wants the key.
	if w := remoteGet(srv, "/_apps/demo/../other/", false); w.Code == http.StatusOK {
		t.Errorf("literal .. escaped the public app: %q", w.Body.String())
	}

	// Typed without its trailing slash, a public link still reaches the app.
	w := remoteGet(srv, "/_apps/demo", false)
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/_apps/demo/" {
		t.Errorf("slashless public link: status = %d, Location = %q", w.Code, w.Header().Get("Location"))
	}
}

func TestRedirectToSlash_KeepsTheSegmentEscaped(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/_apps/a%23b?v=1", nil)
	w := httptest.NewRecorder()
	redirectToSlash(w, req)
	if got := w.Header().Get("Location"); got != "/_apps/a%23b/?v=1" {
		t.Errorf("Location = %q", got)
	}
}

func putLightApp(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	return w
}

// The mount switch writes the same field the agent does, and only that field.
func TestSetLightAppMount(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	manifest := func() map[string]any {
		data, err := os.ReadFile(filepath.Join(lightAppsDir(), "demo", "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// The fixture starts mounted; unmounting drops the key.
	w := putLightApp(t, srv, "/api/light-apps/demo/mount", `{"mount":""}`)
	if w.Code != http.StatusOK {
		t.Fatalf("unmount: status = %d, body=%s", w.Code, w.Body.String())
	}
	var m lightAppManifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.Mount != "" || m.Name != "Demo" {
		t.Errorf("unmount response = %+v (err %v)", m, err)
	}
	if raw := manifest(); raw["mount"] != nil || raw["future_field"] == nil || raw["name"] != "Demo" {
		t.Errorf("manifest after unmount = %v", raw)
	}

	if w := putLightApp(t, srv, "/api/light-apps/demo/mount", `{"mount":"view"}`); w.Code != http.StatusOK {
		t.Fatalf("mount: status = %d", w.Code)
	}
	if raw := manifest(); raw["mount"] != "view" || raw["future_field"] == nil {
		t.Errorf("manifest after mount = %v", raw)
	}

	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/light-apps/demo/mount", `{}`, http.StatusBadRequest},
		{"/api/light-apps/demo/mount", `{"mount":"panel"}`, http.StatusBadRequest},
		{"/api/light-apps/nope/mount", `{"mount":"view"}`, http.StatusNotFound},
	} {
		if w := putLightApp(t, srv, c.path, c.body); w.Code != c.want {
			t.Errorf("PUT %s %s: status = %d, want %d", c.path, c.body, w.Code, c.want)
		}
	}
}

func TestSetLightAppPublic_Validation(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/light-apps/demo/public", `{}`, http.StatusBadRequest},
		{"/api/light-apps/nope/public", `{"public":true}`, http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodPut, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		serveLoopback(srv.http.Handler, w, req)
		if w.Code != c.want {
			t.Errorf("PUT %s %s: status = %d, want %d", c.path, c.body, w.Code, c.want)
		}
	}
	// A remote caller without the key cannot flip it.
	req := httptest.NewRequest(http.MethodPut, "/api/light-apps/demo/public", strings.NewReader(`{"public":true}`))
	req.RemoteAddr = "203.0.113.9:1"
	req.Host = "octo.example.com"
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("keyless remote PUT: status = %d, want 401", w.Code)
	}
}

// The retired apps host serves only the storage export page, only to local
// peers, and only this machine's UI may frame it.
func TestLightAppExport(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	get := func(host, target, remote string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = remote
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(w, req)
		return w
	}
	w := get("demo.apps.localhost:8080", "/__octo_export", "127.0.0.1:1", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `op:'export',ns:"demo"`) {
		t.Fatalf("export: status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors http://localhost:* http://127.0.0.1:*" {
		t.Errorf("export CSP = %q", got)
	}
	for _, c := range []struct {
		name, host, target, remote string
		hdr                        map[string]string
	}{
		{"old page path", "demo.apps.localhost:8080", "/", "127.0.0.1:1", nil},
		{"api on the apps host", "demo.apps.localhost:8080", "/api/health", "127.0.0.1:1", nil},
		{"bare host", "apps.localhost:8080", "/__octo_export", "127.0.0.1:1", nil},
		{"LAN peer", "demo.apps.localhost:8080", "/__octo_export", "192.168.1.9:1", nil},
		{"forwarded", "demo.apps.localhost:8080", "/__octo_export", "127.0.0.1:1", map[string]string{HeaderForwarded: "1"}},
	} {
		if w := get(c.host, c.target, c.remote, c.hdr); w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", c.name, w.Code)
		}
	}
}
