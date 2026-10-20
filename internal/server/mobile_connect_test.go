package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The bundled mobile app calls the server cross-origin from a fixed WebView
// origin, with the key in a header. Its preflight must be answered without any
// --cors flag configured.
func TestCORS_MobileAppOrigins(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, origin := range []string{"capacitor://localhost", "https://localhost"} {
		req := httptest.NewRequest(http.MethodOptions, "/api/sessions", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Headers", "x-access-key")
		req.RemoteAddr = "192.168.1.20:50000"
		w := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204", origin, w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("%s: CORS origin = %q, want it reflected", origin, got)
		}
	}
}

func TestCORS_MobileAppLookalikeRejected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, origin := range []string{"https://localhost:3000", "capacitor://evil.com", "https://localhost.evil.com"} {
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		serveLoopback(srv.http.Handler, w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: expected no CORS header, got %q", origin, got)
		}
	}
}

func TestHandleMobileConnect(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:8088", AccessKey: testAccessKey, Tools: false})

	// A keyless remote peer must not be handed the key.
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/connect", nil)
	req.RemoteAddr = "192.168.1.20:50000"
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("keyless remote: status = %d, want 401", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/mobile/connect", nil)
	w = httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("local: status = %d, want 200 (%s)", w.Code, strings.TrimSpace(w.Body.String()))
	}
	if !strings.Contains(w.Body.String(), `"urls":[]`) {
		t.Errorf("loopback bind: body = %s, want urls as an empty array, not null", strings.TrimSpace(w.Body.String()))
	}
	var got mobileConnectInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessKey != testAccessKey {
		t.Errorf("access_key = %q, want the server key", got.AccessKey)
	}
	if !got.LoopbackOnly || len(got.URLs) != 0 {
		t.Errorf("loopback bind: got loopback_only=%v urls=%v, want true and none", got.LoopbackOnly, got.URLs)
	}
}

func TestReachableURLs(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8088", "localhost:8088", "[::1]:8088"} {
		if urls, lo := reachableURLs(addr); !lo || len(urls) != 0 {
			t.Errorf("%s: got %v loopbackOnly=%v, want loopback only", addr, urls, lo)
		}
	}
	if urls, lo := reachableURLs("100.64.0.5:9000"); lo || len(urls) != 1 || urls[0] != "http://100.64.0.5:9000" {
		t.Errorf("specific bind: got %v loopbackOnly=%v", urls, lo)
	}
	urls, lo := reachableURLs("0.0.0.0:8088")
	if lo {
		t.Error("wildcard bind reported loopback only")
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, "http://") || !strings.HasSuffix(u, ":8088") || strings.Contains(u, "127.0.0.1") {
			t.Errorf("wildcard bind: unexpected URL %q", u)
		}
	}
}
