package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every route registered through s.api must answer with no-store: the desktop
// webview (WKWebView) heuristically caches GET 200s that carry no cache
// policy, and each stale-data incident of that class (#1660 onboard phase,
// stale Light Apps, stale artifact previews) was one endpoint forgetting the
// header. The api wrapper is the single place it is set — these routes never
// set it themselves.
func TestAPIRoutesSendNoStore(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, target := range []string{
		"/api/sessions",
		"/api/tools",
		"/api/config",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.http.Handler, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", target, w.Code)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("%s: Cache-Control = %q, want it to contain no-store", target, cc)
		}
	}
}

// Uploads are the one API surface that must NOT be no-store: their names are
// UnixNano-prefixed so the bytes behind a name never change, and transcripts
// re-request these images on every open — a tunneled mobile client should pull
// them once. handleGetUpload overrides the wrapper's blanket policy.
func TestGetUploadOverridesNoStore(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir := filepath.Join(tmp, ".octo", "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1_pic.png"), []byte{0x89, 'P', 'N', 'G'}, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	req := httptest.NewRequest(http.MethodGet, "/api/uploads/1_pic.png", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want immutable and not no-store", cc)
	}
}
