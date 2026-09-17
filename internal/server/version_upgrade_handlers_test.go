package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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

// TestVersionCheckBudgetCoversMoreThanOneAttempt: the server's budget must
// exceed one upgrade.Check attempt window, or a slow GitHub consumes it whole
// and the mirrors are never tried — the badge then reports "up to date" on a
// network where the desktop tray (which budgets more) finds the release.
func TestVersionCheckBudgetCoversMoreThanOneAttempt(t *testing.T) {
	if versionCheckTimeout <= upgrade.CheckAttemptWindow() {
		t.Errorf("versionCheckTimeout = %v, must exceed one attempt window (%v) so mirrors are reachable",
			versionCheckTimeout, upgrade.CheckAttemptWindow())
	}
}

// TestLatestVersion_FailureKeepsLastKnown: once a release has been seen, a
// later failed lookup must not downgrade the answer to "current is latest" —
// that is what let the badge claim up-to-date while the tray, which keeps its
// last answer, still offered the download.
func TestLatestVersion_FailureKeepsLastKnown(t *testing.T) {
	var fail atomic.Bool
	mux := http.NewServeMux()
	var fake *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "nope", http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, fake.URL+"/releases/tag/v9.9.9", http.StatusFound)
	})
	fake = httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	if latest, needs := srv.latestVersion(); latest != "9.9.9" || !needs {
		t.Fatalf("priming lookup = (%q, %v), want (9.9.9, true)", latest, needs)
	}

	// Age the cache past its TTL and start failing.
	fail.Store(true)
	srv.versionCheckMu.Lock()
	srv.versionCheckedAt = time.Now().Add(-2 * versionCheckTTL)
	srv.versionCheckMu.Unlock()

	if latest, needs := srv.latestVersion(); latest != "9.9.9" || !needs {
		t.Errorf("after a failed re-check = (%q, %v), want the last known (9.9.9, true)", latest, needs)
	}
	// And again, now inside the failure backoff window.
	if latest, needs := srv.latestVersion(); latest != "9.9.9" || !needs {
		t.Errorf("during backoff = (%q, %v), want the last known (9.9.9, true)", latest, needs)
	}
}

// TestRefreshLatestVersion_BypassesCacheAndSeedsIt: the tray's manual check
// must not be served a stale answer, and its result must land in the shared
// cache so the web badge agrees on its very next read.
func TestRefreshLatestVersion_BypassesCacheAndSeedsIt(t *testing.T) {
	tag := "9.9.9"
	var hits int32
	mux := http.NewServeMux()
	var fake *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Redirect(w, r, fake.URL+"/releases/tag/v"+tag, http.StatusFound)
	})
	fake = httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	origURL := upgrade.BaseURL
	upgrade.BaseURL = fake.URL
	t.Cleanup(func() { upgrade.BaseURL = origURL })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})

	// Warm the cache, then publish a newer release. A cached read still sees
	// the old one — that is the cache doing its job.
	if latest, _ := srv.latestVersion(); latest != "9.9.9" {
		t.Fatalf("priming lookup = %q, want 9.9.9", latest)
	}
	tag = "10.0.0"
	if latest, _ := srv.latestVersion(); latest != "9.9.9" {
		t.Fatalf("cached read = %q, want the cached 9.9.9", latest)
	}

	// The manual check ignores the TTL...
	latest, needs, err := srv.RefreshLatestVersion(context.Background())
	if err != nil {
		t.Fatalf("RefreshLatestVersion: %v", err)
	}
	if latest != "10.0.0" || !needs {
		t.Fatalf("RefreshLatestVersion = (%q, %v), want (10.0.0, true)", latest, needs)
	}
	// ...and seeds the cache, so the badge's next read matches the tray.
	if latest, needs := srv.latestVersion(); latest != "10.0.0" || !needs {
		t.Errorf("badge read after refresh = (%q, %v), want (10.0.0, true)", latest, needs)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("upstream hits = %d, want 2 (prime + forced refresh; the rest cached)", got)
	}
}

// TestLatestVersion_ExportedMatchesBadge: the tray reads LatestVersion, the
// badge reads the same cache through /api/version. They must never disagree.
func TestLatestVersion_ExportedMatchesBadge(t *testing.T) {
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
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})

	trayLatest, trayNeeds := srv.LatestVersion()

	w := doJSON(t, srv, http.MethodGet, "/api/version", "")
	var badge struct {
		Latest      string `json:"latest"`
		NeedsUpdate bool   `json:"needs_update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &badge); err != nil {
		t.Fatalf("decode /api/version: %v", err)
	}
	if badge.Latest != trayLatest || badge.NeedsUpdate != trayNeeds {
		t.Errorf("badge = (%q, %v), tray = (%q, %v) — must match",
			badge.Latest, badge.NeedsUpdate, trayLatest, trayNeeds)
	}
}
