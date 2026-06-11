package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/config"
)

const testAccessKey = "test-access-key-0123456789abcdef"

// authRequest dispatches through the real mux with explicit network identity.
// remoteAddr is the peer, target carries the Host the client sent.
func authRequest(t *testing.T, srv *Server, method, target, remoteAddr string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remoteAddr
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

func TestRequireAuth_LoopbackExemption(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})

	cases := []struct {
		name       string
		target     string
		remoteAddr string
		hdr        map[string]string
		want       int
	}{
		{"loopback local host", "http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000", nil, http.StatusOK},
		{"loopback localhost host", "http://localhost:8080/api/sessions", "127.0.0.1:50000", nil, http.StatusOK},
		{"loopback IPv6", "http://[::1]:8080/api/sessions", "[::1]:50000", nil, http.StatusOK},
		{"loopback IPv4-mapped peer", "http://127.0.0.1:8080/api/sessions", "[::ffff:127.0.0.1]:50000", nil, http.StatusOK},
		{"loopback uppercase host", "http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000", map[string]string{"Host": "LOCALHOST:8080"}, http.StatusOK},
		{"loopback local origin", "http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000", map[string]string{"Origin": "http://localhost:8080"}, http.StatusOK},
		// CSRF: cross-site browser request reaches loopback.
		{"loopback foreign origin", "http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"loopback null origin", "http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000", map[string]string{"Origin": "null"}, http.StatusForbidden},
		// DNS rebinding: attacker domain resolves to 127.0.0.1.
		{"loopback foreign host", "http://attacker.example:8080/api/sessions", "127.0.0.1:50000", nil, http.StatusForbidden},
		// Non-loopback peers need the key, and spoofable headers don't help.
		{"non-loopback no key", "http://192.168.1.5:8080/api/sessions", "192.168.1.9:50000", nil, http.StatusUnauthorized},
		{"non-loopback XFF spoof", "http://192.168.1.5:8080/api/sessions", "192.168.1.9:50000", map[string]string{"X-Forwarded-For": "127.0.0.1"}, http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.hdr {
				if k == "Host" {
					req.Host = v
					continue
				}
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			srv.mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestRequireAuth_KeySources(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})
	const target = "http://192.168.1.5:8080/api/sessions"
	const remote = "192.168.1.9:50000"

	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"bearer", map[string]string{"Authorization": "Bearer " + testAccessKey}, http.StatusOK},
		{"x-access-key", map[string]string{"X-Access-Key": testAccessKey}, http.StatusOK},
		{"cookie", map[string]string{"Cookie": accessKeyCookie + "=" + testAccessKey}, http.StatusOK},
		{"wrong bearer", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"wrong cookie", map[string]string{"Cookie": accessKeyCookie + "=nope"}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := authRequest(t, srv, http.MethodGet, target, remote, tc.hdr)
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}

	// Key-authenticated requests skip the Host/Origin gates entirely — a
	// LAN browser's same-origin request carries a non-local Host.
	w := authRequest(t, srv, http.MethodGet, target, remote, map[string]string{
		"Cookie": accessKeyCookie + "=" + testAccessKey,
		"Origin": "http://192.168.1.5:8080",
	})
	if w.Code != http.StatusOK {
		t.Errorf("key + non-local host/origin: got %d, want 200", w.Code)
	}
}

func TestRequireAuth_QueryParamOnlyOnWS(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})

	// Rejected on a normal API route: the query source is /ws-only.
	w := authRequest(t, srv, http.MethodGet,
		"http://192.168.1.5:8080/api/sessions?access_key="+testAccessKey, "192.168.1.9:50000", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("query key on /api: got %d, want 401", w.Code)
	}

	// Accepted on /ws: auth passes, then the upgrade fails on the plain
	// recorder — anything but 401/403 means the gate let it through.
	w = authRequest(t, srv, http.MethodGet,
		"http://192.168.1.5:8080/ws?access_key="+testAccessKey, "192.168.1.9:50000", nil)
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Errorf("query key on /ws: got %d, want auth to pass", w.Code)
	}
}

func TestRequireAuth_EmptyKeyMatchesNothing(t *testing.T) {
	srv := mustServer(t, Config{})
	srv.accessKey = ""

	w := authRequest(t, srv, http.MethodGet,
		"http://192.168.1.5:8080/api/sessions", "192.168.1.9:50000",
		map[string]string{"Authorization": "Bearer "})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("empty key + empty bearer: got %d, want 401", w.Code)
	}
}

func TestRequireAuth_CORSAllowlist(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey, CORSOrigins: []string{"http://app.example:3000"}})

	// Allowlisted origin from loopback passes the Origin gate.
	w := authRequest(t, srv, http.MethodGet,
		"http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000",
		map[string]string{"Origin": "http://app.example:3000"})
	if w.Code != http.StatusOK {
		t.Errorf("allowlisted origin: got %d, want 200", w.Code)
	}

	// Allowlisted host passes the Host gate.
	req := httptest.NewRequest(http.MethodGet, "http://app.example:3000/api/sessions", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("allowlisted host: got %d, want 200", rec.Code)
	}
}

func TestRequireAuth_CORSWildcardNeverWidensGates(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey, CORSOrigins: []string{"*"}})

	w := authRequest(t, srv, http.MethodGet,
		"http://127.0.0.1:8080/api/sessions", "127.0.0.1:50000",
		map[string]string{"Origin": "https://evil.example"})
	if w.Code != http.StatusForbidden {
		t.Errorf("--cors '*' + foreign origin: got %d, want 403", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "http://attacker.example:8080/api/sessions", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("--cors '*' + foreign host: got %d, want 403", rec.Code)
	}
}

// TestRequireAuth_RouteCoverage asserts every route registered through api()
// rejects a keyless non-loopback request. A future route that bypasses the
// api() helper (and thus the requireAuth wrapper) fails the count check.
func TestRequireAuth_RouteCoverage(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})
	if len(srv.apiRoutes) < 50 {
		t.Fatalf("expected ≥50 authenticated routes, got %d — routes registered around api()?", len(srv.apiRoutes))
	}

	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	for _, pat := range srv.apiRoutes {
		method, path := http.MethodGet, pat
		if i := strings.IndexByte(pat, ' '); i >= 0 {
			method, path = pat[:i], pat[i+1:]
		}
		path = placeholder.ReplaceAllString(path, "x")
		req := httptest.NewRequest(method, "http://203.0.113.7:8080"+path, nil)
		req.RemoteAddr = "203.0.113.9:50000"
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: keyless non-loopback got %d, want 401", pat, w.Code)
		}
	}
}

func TestHealthVersionUnauthenticated(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})
	for _, path := range []string{"/api/health", "/api/version"} {
		w := authRequest(t, srv, http.MethodGet, "http://203.0.113.7:8080"+path, "203.0.113.9:50000", nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200 without key", path, w.Code)
		}
	}
}

func TestResolveAccessKey_Precedence(t *testing.T) {
	t.Setenv("OCTO_ACCESS_KEY", "env-key")

	if k, gen := resolveAccessKey("flag-key", config.Config{AccessKey: "file-key"}); k != "flag-key" || gen {
		t.Errorf("flag should win: got (%q, %v)", k, gen)
	}
	if k, gen := resolveAccessKey("", config.Config{AccessKey: "file-key"}); k != "env-key" || gen {
		t.Errorf("env should beat file: got (%q, %v)", k, gen)
	}

	t.Setenv("OCTO_ACCESS_KEY", "")
	if k, gen := resolveAccessKey("", config.Config{AccessKey: "file-key"}); k != "file-key" || gen {
		t.Errorf("file should win when flag/env empty: got (%q, %v)", k, gen)
	}
	k, gen := resolveAccessKey("", config.Config{})
	if !gen || len(k) != 64 {
		t.Errorf("expected generated 64-hex key, got (%q, %v)", k, gen)
	}
	k2, _ := resolveAccessKey("", config.Config{})
	if k == k2 {
		t.Error("generated keys should be random, got the same twice")
	}
}

func TestWSCheckOrigin(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})

	mkReq := func(host, origin, cookie string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/ws", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != "" {
			req.Header.Set("Cookie", accessKeyCookie+"="+cookie)
		}
		return req
	}

	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"no origin", mkReq("127.0.0.1:8080", "", ""), true},
		{"local origin", mkReq("127.0.0.1:8080", "http://localhost:8080", ""), true},
		{"same origin non-local", mkReq("192.168.1.5:8080", "http://192.168.1.5:8080", ""), true},
		{"foreign origin", mkReq("127.0.0.1:8080", "https://evil.example", ""), false},
		{"foreign origin with key", mkReq("127.0.0.1:8080", "https://evil.example", testAccessKey), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := srv.wsCheckOrigin(tc.req); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
