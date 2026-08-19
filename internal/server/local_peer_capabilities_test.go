package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The three capabilities gated on being the local machine, each checked against
// a request that reaches the server over loopback but did not originate there —
// the shape octo's own tunnel bridge sends. Each of these was reachable by a
// paired phone before the marker existed.

// A relayed peer must not drive the host's OS dialogs, however well it
// authenticates. Authenticating is what gets it past requireAuth to the
// handler's own guard, which is the thing under test.
func TestNativeRoutes_RefuseARelayedPeer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{retPath: "/x"}})

	// One per kind of capability rather than all fourteen: they share the
	// predicate, and these cover a dialog, a window action, and self-update.
	for _, path := range []string{
		"/api/native/pick-folder",
		"/api/native/pick-file",
		"/api/native/window/minimise",
		"/api/native/self-update",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.RemoteAddr = "127.0.0.1:50000" // the bridge dials loopback
			req.Host = "127.0.0.1:8088"        // …under a loopback Host
			req.Header.Set(HeaderForwarded, "relay")
			req.Header.Set("Authorization", "Bearer "+srv.AccessKey())
			w := httptest.NewRecorder()
			srv.mux.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("relayed peer: got %d, want 403 (%s)", w.Code, strings.TrimSpace(w.Body.String()))
			}
		})
	}
}

// The same routes still work for the desktop shell and a localhost browser,
// which is the whole point of not simply removing them.
func TestNativeRoutes_AllowAGenuinelyLocalPeer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{retPath: "/picked"}})
	req := httptest.NewRequest(http.MethodPost, "/api/native/pick-folder", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:8088"
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("local peer: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
}

// conn.loopback decides whether a message may name a file by its real path on
// the host's disk — the heaviest of the three, since it reads the host's
// filesystem on behalf of whoever sent the message. A relayed WebSocket must
// not carry it.
func TestWSUpgrade_RelayedConnectionIsNotLoopback(t *testing.T) {
	cases := []struct {
		name string
		hdr  map[string]string
		host string
		want bool
	}{
		{"direct localhost browser", nil, "127.0.0.1:8088", true},
		{"octo tunnel", map[string]string{HeaderForwarded: "relay"}, "127.0.0.1:8088", false},
		{"behind a proxy", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "127.0.0.1:8088", false},
		{"public host passed through", nil, "x.ngrok-free.app", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			req.RemoteAddr = "127.0.0.1:50000"
			req.Host = tc.host
			for k, v := range tc.hdr {
				req.Header.Set(k, v)
			}
			// The upgrade path stamps this onto the connection; assert the
			// predicate it stamps rather than standing up a real WebSocket,
			// which would need a hijackable ResponseWriter.
			if got := isLocalRequest(req); got != tc.want {
				t.Errorf("conn.loopback would be %v, want %v", got, tc.want)
			}
		})
	}
}

// And the consequence of that flag, at the layer that acts on it: a real local
// path is honoured only for a local peer. Without this the relayed case would
// hand the agent an absolute path on the host's disk.
func TestParseUserFiles_LocalPathOnlyForALocalPeer(t *testing.T) {
	files := []wsUserFile{{Name: "notes.md", LocalPath: "/etc/hosts"}}

	local := parseUserFiles(files, true, false)
	if len(local.notes) == 0 && len(local.blocks) == 0 {
		t.Error("a local peer's real path should be honoured")
	}

	relayed := parseUserFiles(files, false, false)
	for _, n := range relayed.notes {
		if strings.Contains(n, "/etc/hosts") {
			t.Errorf("relayed peer got the host path through: %q", n)
		}
	}
}
