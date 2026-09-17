package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
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

// TestVersionSelfUpdateFlag: /api/version reports self_update from the
// bridge's CanSelfUpdate, so the badge only offers "Update Now" on desktop
// builds that can actually swap themselves (false on a plain serve build,
// which has no bridge at all).
func TestVersionSelfUpdateFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	get := func(srv *Server) bool {
		req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		v, _ := body["self_update"].(bool)
		return v
	}

	if get(mustServer(t, Config{Addr: "127.0.0.1:0"})) {
		t.Error("serve build: self_update = true, want false")
	}
	if get(mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{}})) {
		t.Error("desktop build without in-place support: self_update = true, want false")
	}
	if !get(mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{canSelfUpdate: true}})) {
		t.Error("desktop build with in-place support: self_update = false, want true")
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

// TestLatestVersion_ConfigOptOut: with `update_check: false` in config, the
// server makes no outbound request at all and reports current-is-latest —
// this is the switch that makes the "no traffic but your model calls" claim
// literally true.
func TestLatestVersion_ConfigOptOut(t *testing.T) {
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

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	// Own HOME so the saved preference can't leak into the package's other
	// tests, which share the one TestMain pins.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	off := false
	if err := (config.Config{UpdateCheck: &off}).Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	latest, needs := srv.latestVersion()
	if latest != "0.18.0" || needs {
		t.Errorf("latestVersion with update_check off = (%q, %v), want (0.18.0, false)", latest, needs)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("upstream hits = %d, want 0 — the whole point is that nothing is sent", got)
	}
}

// TestPutUpdateCheck_PersistsAndSilences: the Settings toggle writes the
// preference, GET /api/config reports it back, and the very next
// /api/version honours it without a restart.
func TestPutUpdateCheck_PersistsAndSilences(t *testing.T) {
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

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})

	// Default on: the config endpoint says so before anything is written.
	var cfgResp struct {
		UpdateCheck *bool `json:"update_check"`
	}
	w := doJSON(t, srv, http.MethodGet, "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfgResp.UpdateCheck == nil || !*cfgResp.UpdateCheck {
		t.Fatalf("initial update_check = %v, want true", cfgResp.UpdateCheck)
	}

	if w := doJSON(t, srv, http.MethodPut, "/api/config/update_check", `{"update_check":false}`); w.Code != http.StatusOK {
		t.Fatalf("PUT update_check = %d, want 200 (%s)", w.Code, w.Body.String())
	}

	w = doJSON(t, srv, http.MethodGet, "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfgResp.UpdateCheck == nil || *cfgResp.UpdateCheck {
		t.Fatalf("update_check after PUT = %v, want false", cfgResp.UpdateCheck)
	}

	if w := doJSON(t, srv, http.MethodGet, "/api/version", ""); w.Code != http.StatusOK {
		t.Fatalf("GET /api/version = %d", w.Code)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("upstream hits after opting out = %d, want 0", got)
	}
}
