package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
)

// awaitVersion polls the cache-only read until it reports want (or the
// deadline passes). LatestVersion never blocks on the network any more — a
// stale cache kicks a background refresh — so a test that wants the looked-up
// value has to let that goroutine land.
func awaitVersion(t *testing.T, srv *Server, want string) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		latest, needs := srv.LatestVersion()
		if latest == want || time.Now().After(deadline) {
			return latest, needs
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settleVersionRefresh waits for any in-flight background lookup to finish, so
// a test can assert on upstream hit counts without racing the goroutine.
func settleVersionRefresh(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for srv.versionChecking.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

// TestLatestVersion_ChecksAndCaches points the upgrade base URL at a fake
// release origin. The read itself never goes upstream — a stale cache kicks a
// background refresh and the read answers from what it has — so the first call
// reports the cold answer, the refresh lands, and every later call is served
// from cache with no further upstream traffic.
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

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

	// Pin a release-like build so NeedsUpdate can fire (the test binary is
	// otherwise a dev build with no commit).
	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})

	// Cold: answers immediately with what it knows (nothing), having started
	// the refresh. This is the property that keeps the network off the request
	// path.
	if latest, needs := srv.LatestVersion(); latest != "0.18.0" || needs {
		t.Fatalf("cold read = (%q, %v), want (0.18.0, false) — it must not block on the lookup", latest, needs)
	}

	latest, needs := awaitVersion(t, srv, "9.9.9")
	if latest != "9.9.9" || !needs {
		t.Fatalf("after refresh = (%q, %v), want (9.9.9, true)", latest, needs)
	}
	for i := 0; i < 5; i++ {
		if l, _ := srv.LatestVersion(); l != "9.9.9" {
			t.Fatalf("cached read = %q, want 9.9.9", l)
		}
	}
	settleVersionRefresh(t, srv)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (everything after the refresh is cached)", got)
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

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0-dev", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	srv.LatestVersion()
	settleVersionRefresh(t, srv)
	if latest, needs := srv.LatestVersion(); needs {
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
	srv.LatestVersion()
	if latest, needs := awaitVersion(t, srv, "9.9.9"); latest != "9.9.9" || !needs {
		t.Fatalf("priming read = (%q, %v), want (9.9.9, true)", latest, needs)
	}

	// Force a failing lookup through the same path the tray's manual check
	// uses, so the failure is recorded rather than merely scheduled.
	fail.Store(true)
	latest, needs, err := srv.RefreshLatestVersion(context.Background())
	if err == nil {
		t.Fatal("forced refresh succeeded against a failing origin")
	}
	if latest != "9.9.9" || !needs {
		t.Errorf("failed refresh = (%q, %v), want the last known (9.9.9, true)", latest, needs)
	}
	// And the cache-only read, now inside the failure backoff window.
	if latest, needs := srv.LatestVersion(); latest != "9.9.9" || !needs {
		t.Errorf("during backoff = (%q, %v), want the last known (9.9.9, true)", latest, needs)
	}
}

// TestLatestVersion_NeverSucceededFallsBackToCurrent is the other half of
// lastKnown: with nothing ever looked up, a failure reports current-is-latest
// rather than inventing a release.
func TestLatestVersion_NeverSucceededFallsBackToCurrent(t *testing.T) {
	fakeFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	t.Cleanup(fakeFail.Close)

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fakeFail.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	latest, needs, err := srv.RefreshLatestVersion(context.Background())
	if err == nil {
		t.Fatal("forced refresh succeeded against a failing origin")
	}
	if latest != "0.18.0" || needs {
		t.Errorf("never-succeeded failure = (%q, %v), want (0.18.0, false)", latest, needs)
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

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})

	// Warm the cache, then publish a newer release. A cached read still sees
	// the old one — that is the refresh interval doing its job.
	srv.LatestVersion()
	if latest, _ := awaitVersion(t, srv, "9.9.9"); latest != "9.9.9" {
		t.Fatalf("priming read = %q, want 9.9.9", latest)
	}
	tag = "10.0.0"
	if latest, _ := srv.LatestVersion(); latest != "9.9.9" {
		t.Fatalf("cached read = %q, want the cached 9.9.9", latest)
	}

	// The manual check ignores the refresh interval...
	latest, needs, err := srv.RefreshLatestVersion(context.Background())
	if err != nil {
		t.Fatalf("RefreshLatestVersion: %v", err)
	}
	if latest != "10.0.0" || !needs {
		t.Fatalf("RefreshLatestVersion = (%q, %v), want (10.0.0, true)", latest, needs)
	}
	// ...and seeds the cache, so the badge's next read matches the tray.
	if latest, needs := srv.LatestVersion(); latest != "10.0.0" || !needs {
		t.Errorf("badge read after refresh = (%q, %v), want (10.0.0, true)", latest, needs)
	}
	settleVersionRefresh(t, srv)
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("upstream hits = %d, want 2 (one background refresh + one forced; the rest cached)", got)
	}
}

// TestRefreshLatestVersion_DisabledReportsError: a server that performs no
// lookups hands back an error rather than a confident "you're up to date" the
// tray would happily toast.
func TestRefreshLatestVersion_DisabledReportsError(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: false})
	if _, _, err := srv.RefreshLatestVersion(context.Background()); err == nil {
		t.Error("RefreshLatestVersion on a no-lookup build returned no error")
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

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	srv.LatestVersion()
	if _, _ = awaitVersion(t, srv, "9.9.9"); true {
		settleVersionRefresh(t, srv)
	}

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
	if badge.Latest != "9.9.9" || !badge.NeedsUpdate {
		t.Errorf("both surfaces = (%q, %v), want the looked-up (9.9.9, true)", badge.Latest, badge.NeedsUpdate)
	}
}

// TestLatestVersion_ConfigOptOut: with `update_check: false` in config, the
// server starts no lookup at all and reports current-is-latest — this is the
// switch that makes the "no traffic but your model calls" claim literally
// true. Reading the cache is still allowed; it costs nothing.
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

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

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
	latest, needs := srv.LatestVersion()
	if latest != "0.18.0" || needs {
		t.Errorf("LatestVersion with update_check off = (%q, %v), want (0.18.0, false)", latest, needs)
	}
	settleVersionRefresh(t, srv)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("upstream hits = %d, want 0 — the whole point is that nothing is sent", got)
	}
}

// TestLatestVersion_BrokenConfigKeepsOptOut: a hand-edit that leaves
// config.yml unparseable must not silently re-enable the check. Reading
// through config.LoadCached keeps the last config that parsed, so the switch
// survives a typo instead of failing open.
func TestLatestVersion_BrokenConfigKeepsOptOut(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	var fake *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
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

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	off := false
	if err := (config.Config{UpdateCheck: &off}).Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	// One good read seeds LoadCached's last-known-good.
	if _, needs := srv.LatestVersion(); needs {
		t.Fatal("needs_update with update_check off")
	}

	path, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path: %v", err)
	}
	if err := os.WriteFile(path, []byte("update_check: false\n  bogus indent: [\n"), 0o600); err != nil {
		t.Fatalf("corrupt config: %v", err)
	}

	if _, needs := srv.LatestVersion(); needs {
		t.Error("needs_update after breaking config.yml — the switch failed open")
	}
	settleVersionRefresh(t, srv)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("upstream hits = %d, want 0 — a broken config must not re-enable the check", got)
	}
}

// TestRefreshLatestVersion_IgnoresConfigOptOut: the switch silences the
// automatic cadence, never an explicit request. The tray's "Check for
// updates…" routes here, and the user clicking it is the consent — same
// contract `octo upgrade --check` holds on the CLI side.
func TestRefreshLatestVersion_IgnoresConfigOptOut(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	var fake *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
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

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	off := false
	if err := (config.Config{UpdateCheck: &off}).Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	latest, needs, err := srv.RefreshLatestVersion(context.Background())
	if err != nil {
		t.Fatalf("RefreshLatestVersion: %v", err)
	}
	if latest != "9.9.9" || !needs {
		t.Errorf("explicit refresh = (%q, %v), want (9.9.9, true) — the switch must not gag it", latest, needs)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hits = %d, want 1", got)
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

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = fake.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })

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
	settleVersionRefresh(t, srv)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("upstream hits after opting out = %d, want 0", got)
	}
}
