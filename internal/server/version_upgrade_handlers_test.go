package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
)

// TestLatestVersion_ChecksAndCaches points the upgrade base URL at a fake
// release origin: with UpdateCheck on, the first lookup goes upstream, the
// second is served from cache (one upstream hit total), and needs_update
// reflects a release build behind latest.
func TestLatestVersion_ChecksAndCaches(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	var fake *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Redirect(w, r, fake.URL+"/releases/tag/v9.9.9", http.StatusFound)
	})
	fake = httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	origURL := upgrade.BaseURL
	upgrade.BaseURL = fake.URL
	t.Cleanup(func() { upgrade.BaseURL = origURL })

	// Pin a release-like build so needsUpdate can fire (the test binary is
	// otherwise a dev build with no commit).
	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})

	latest, needs := srv.latestVersion()
	if latest != "9.9.9" || !needs {
		t.Fatalf("latestVersion = (%q, %v), want (9.9.9, true)", latest, needs)
	}
	if latest2, _ := srv.latestVersion(); latest2 != "9.9.9" {
		t.Fatalf("second lookup = %q, want cached 9.9.9", latest2)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (second lookup must be cached)", got)
	}
}

// TestLatestVersion_DevBuildNeverNags: even with an update available, a
// dev build reports needs_update=false — the badge must not offer an
// upgrade the backend would refuse.
func TestLatestVersion_DevBuildNeverNags(t *testing.T) {
	mux := http.NewServeMux()
	var fake *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fake.URL+"/releases/tag/v9.9.9", http.StatusFound)
	})
	fake = httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	origURL := upgrade.BaseURL
	upgrade.BaseURL = fake.URL
	t.Cleanup(func() { upgrade.BaseURL = origURL })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0-dev", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	if latest, needs := srv.latestVersion(); needs {
		t.Errorf("dev build reported needs_update for latest %q", latest)
	}
}

// TestVersionUpgradeMode: a plain serve build reports upgrade_mode "cli"; the
// desktop build (a NativeBridge wired) reports "installer". download_url is
// always present so the badge needn't hardcode the download landing page.
func TestVersionUpgradeMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	get := func(srv *Server) (mode, downloadURL string) {
		req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		m, _ := body["upgrade_mode"].(string)
		u, _ := body["download_url"].(string)
		return m, u
	}

	if mode, url := get(mustServer(t, Config{Addr: "127.0.0.1:0"})); mode != "cli" || url == "" {
		t.Errorf("serve build: got mode=%q download_url=%q, want cli / non-empty", mode, url)
	}
	if mode, _ := get(mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{}})); mode != "installer" {
		t.Errorf("desktop build: got mode=%q, want installer", mode)
	}
}

// TestVersionUpgradeRefusedInInstallerMode: the in-place swap endpoint refuses
// with 409 when a NativeBridge is wired, so a remote peer can't drive a desktop
// binary swap.
func TestVersionUpgradeRefusedInInstallerMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{}})
	req := httptest.NewRequest(http.MethodPost, "/api/version/upgrade", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("installer-mode upgrade: got %d, want 409", w.Code)
	}
}
