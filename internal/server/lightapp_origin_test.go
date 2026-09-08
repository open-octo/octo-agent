package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLightAppFixture saves one Light App under the scratch HOME the way the
// agent does — a directory with manifest.json and index.html — plus a script
// beside it and a file no page should reach.
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
		"manifest.json": `{"slug":"demo","name":"Demo"}`,
		"index.html":    indexHTML,
		"app.js":        "console.log('demo')",
		"notes.env":     "SECRET=1",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mustServer(t, cfg)
}

func lightAppGet(srv *Server, host, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = host
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	return w
}

func TestLightAppOrigin_ServesEntryWithBridgeAndAssets(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false},
		`<!DOCTYPE html><html><head><script src="./app.js"></script></head><body><h1>demo</h1></body></html>`)
	host := "demo.apps.localhost:8080"

	for _, target := range []string{"/", "/index.html"} {
		w := lightAppGet(srv, host, target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body=%s", target, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, "<h1>demo</h1>") || !strings.Contains(body, `<script src="./app.js">`) {
			t.Errorf("GET %s: page content or relative script lost: %s", target, body)
		}
		// The bridge rides just before </body>, configured for this slug, with
		// the download half off under plain serve (a browser downloads itself).
		if !strings.Contains(body, `window.__octoLightApp={"download":false,"ns":"demo"};`) {
			t.Errorf("GET %s: bridge config missing or wrong: %s", target, body)
		}
		if !strings.Contains(body, "op: 'migrate-ready'") {
			t.Errorf("GET %s: bridge script not injected", target)
		}
		if idx := strings.Index(body, "__octoLightApp"); idx < 0 || idx > strings.Index(body, "</body>") {
			t.Errorf("GET %s: bridge must sit before </body>", target)
		}
		if strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("GET %s: the origin must not sandbox its own page", target)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("GET %s: Referrer-Policy = %q", target, got)
		}
	}

	w := lightAppGet(srv, host, "/app.js")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Errorf("GET /app.js: status = %d, type = %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, target := range []string{"/notes.env", "/missing.js", "/../outside.js", "/api/health", "/api/light-apps", "/assets/index.js", "/ws"} {
		if w := lightAppGet(srv, host, target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", target, w.Code)
		}
	}
}

func TestLightAppOrigin_DesktopInjectsTheDownloadBridge(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false, Native: &fakeNative{}}, "<h1>demo</h1>")
	w := lightAppGet(srv, "demo.apps.localhost:8080", "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `window.__octoLightApp={"download":true,"ns":"demo"};`) {
		t.Errorf("desktop must turn the download bridge on: %s", w.Body.String())
	}
	// No <body> tag at all: the bridge still lands, at the end.
	if !strings.HasSuffix(strings.TrimSpace(w.Body.String()), "</script>") {
		t.Errorf("bridge not appended to a body-less page: %s", w.Body.String())
	}
}

// A Light App that inlines its data can run well past 10 MB; the old JSON
// endpoint served it without a cap and the origin must not regress that.
func TestLightAppOrigin_LargeEntryIsServed(t *testing.T) {
	big := strings.Repeat(" ", 12<<20) + "<h1>big</h1>"
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, big)
	w := lightAppGet(srv, "demo.apps.localhost:8080", "/")
	if w.Code != http.StatusOK {
		t.Fatalf("12 MB entry: status = %d, body=%s", w.Code, w.Body.String()[:min(200, w.Body.Len())])
	}
	if !strings.Contains(w.Body.String(), "<h1>big</h1>") || !strings.Contains(w.Body.String(), "__octoLightApp") {
		t.Errorf("large entry lost content or bridge")
	}
	over := strings.Repeat(" ", artifactEntryMaxBytes+1)
	srv2 := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, over)
	if w := lightAppGet(srv2, "demo.apps.localhost:8080", "/"); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over-cap entry: status = %d, want 413", w.Code)
	}
}

func TestLightAppOrigin_EntryGoesThroughTheGate(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false},
		`<html><head><script src="https://evil.example.com/x.js"></script></head><body><h1>demo</h1></body></html>`)
	w := lightAppGet(srv, "demo.apps.localhost:8080", "/?theme=dark")
	if strings.Contains(w.Body.String(), "evil.example.com") || !strings.Contains(w.Body.String(), "#2b2111") {
		t.Errorf("gate did not run on the Light App entry: %s", w.Body.String())
	}
}

func TestLightAppOrigin_UnknownSlugAndBareHostAre404(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	for _, host := range []string{"nope.apps.localhost:8080", "apps.localhost:8080", "a.demo.apps.localhost:8080"} {
		for _, target := range []string{"/", "/api/health", "/api/version"} {
			if w := lightAppGet(srv, host, target); w.Code != http.StatusNotFound {
				t.Errorf("Host %s GET %s: status = %d, want 404", host, target, w.Code)
			}
		}
	}
}

func TestLightAppOrigin_OnlyLocalPeersOnlyGET(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	host := "demo.apps.localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.9:50000"
	req.Host = host
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("LAN peer: status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = host
	req.Header.Set(HeaderForwarded, "1")
	w = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("forwarded: status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = host
	w = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", w.Code)
	}
}

// Same boundary as the artifact origin: a Light App's Origin is foreign to
// the app API.
func TestLightAppOrigin_OriginIsForeignToTheAppAPI(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	req := httptest.NewRequest(http.MethodGet, "/api/light-apps", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://demo.apps.localhost:8080")
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Light App origin calling /api: status = %d, want 403", w.Code)
	}
}

func TestInjectBeforeBody(t *testing.T) {
	s := []byte("<script>x</script>")
	if got := string(injectBeforeBody([]byte("<html><body><p>a</p></BODY ></html>"), s)); got != "<html><body><p>a</p><script>x</script></BODY ></html>" {
		t.Errorf("case-insensitive close tag: %s", got)
	}
	if got := string(injectBeforeBody([]byte("<p>a</p>"), s)); got != "<p>a</p><script>x</script>" {
		t.Errorf("no body tag: %s", got)
	}
	if got := string(injectBeforeBody([]byte("<p>a</p>"), nil)); got != "<p>a</p>" {
		t.Errorf("nil script must be a no-op: %s", got)
	}
	// A `</body>` inside the page's own script is text, not the tag; splicing
	// there would cut that script in half.
	page := "<body><script>el.innerHTML = '<b>x</b></body>';</script><p>a</p></body>"
	if got := string(injectBeforeBody([]byte(page), s)); got != "<body><script>el.innerHTML = '<b>x</b></body>';</script><p>a</p><script>x</script></body>" {
		t.Errorf("must use the last close tag: %s", got)
	}
}
