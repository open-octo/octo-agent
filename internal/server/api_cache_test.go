package server

import (
	"net/http"
	"net/http/httptest"
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
