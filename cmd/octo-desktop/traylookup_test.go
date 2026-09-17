package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/server"
	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
)

// fakeReleaseOrigin points upgrade at a server that reports ver, counting hits.
func fakeReleaseOrigin(t *testing.T, ver string, hits *int) {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			*hits++
		}
		http.Redirect(w, r, srv.URL+"/releases/tag/v"+ver, http.StatusFound)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	origURL, origMirrors := upgrade.BaseURL, upgrade.MirrorBaseURLs
	upgrade.BaseURL, upgrade.MirrorBaseURLs = srv.URL, nil
	t.Cleanup(func() { upgrade.BaseURL, upgrade.MirrorBaseURLs = origURL, origMirrors })
}

// pinReleaseBuild makes this test binary look like a shipped release, so
// upgrade.Eligible permits "you are behind".
func pinReleaseBuild(t *testing.T) {
	t.Helper()
	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })
}

// hubWith returns a bridge holding a real in-process server, the same thing
// the desktop shell binds at startup.
func hubWith(t *testing.T) *nativeBridge {
	t.Helper()
	srv, err := server.New(server.Config{Addr: "127.0.0.1:0", Tools: false, UpdateCheck: true})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	// Shutting the server down drains any in-flight background refresh, so the
	// goroutine can't still be reading upgrade.BaseURL when a later cleanup
	// restores it.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	b := &nativeBridge{}
	b.srv.Store(srv)
	return b
}

// awaitTray polls the auto path until the shared cache reports want.
func awaitTray(t *testing.T, b *nativeBridge, want string) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		latest, needs, _ := trayLookup(b, false)
		if latest == want || time.Now().After(deadline) {
			return latest, needs
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTrayLookup_AutoNeverLooksWithoutHub: the auto cadence runs every minute,
// so it must not reach the network on its own. Before the hub is bound there
// is no shared cache to read — and no web badge to stay in step with — so the
// tick does nothing rather than starting its own per-minute lookup.
func TestTrayLookup_AutoNeverLooksWithoutHub(t *testing.T) {
	hits := 0
	fakeReleaseOrigin(t, "9.9.9", &hits)
	pinReleaseBuild(t)

	b := &nativeBridge{} // srv never stored
	if _, _, err := trayLookup(b, false); err == nil {
		t.Error("auto lookup without a hub returned no error, want errHubNotBound")
	}
	if hits != 0 {
		t.Errorf("upstream hits = %d, want 0 — an auto tick must not look on its own", hits)
	}
}

// TestTrayLookup_AutoReadsSharedCache: the auto path reads the very cache
// /api/version serves, which is what makes the tray and the badge agree.
func TestTrayLookup_AutoReadsSharedCache(t *testing.T) {
	hits := 0
	fakeReleaseOrigin(t, "9.9.9", &hits)
	pinReleaseBuild(t)

	b := hubWith(t)
	latest, needs := awaitTray(t, b, "9.9.9")
	if latest != "9.9.9" || !needs {
		t.Fatalf("auto lookup = (%q, %v), want (9.9.9, true)", latest, needs)
	}

	// Many reads, one lookup: the cadence is affordable precisely because
	// reading is free.
	before := hits
	for i := 0; i < 20; i++ {
		if l, _, _ := trayLookup(b, false); l != "9.9.9" {
			t.Fatalf("cached auto read = %q, want 9.9.9", l)
		}
	}
	if hits != before {
		t.Errorf("upstream hits grew by %d over 20 reads, want 0", hits-before)
	}

	// And the server agrees — same cache, same answer.
	if sl, sn := b.srv.Load().LatestVersion(); sl != latest || sn != needs {
		t.Errorf("server = (%q, %v), tray = (%q, %v) — must match", sl, sn, latest, needs)
	}
}

// TestTrayLookup_ManualForcesFreshLookup: "Check for updates…" must not be
// answered from cache, and its result must seed the cache the badge reads.
func TestTrayLookup_ManualForcesFreshLookup(t *testing.T) {
	hits := 0
	fakeReleaseOrigin(t, "9.9.9", &hits)
	pinReleaseBuild(t)

	b := hubWith(t)
	if latest, _ := awaitTray(t, b, "9.9.9"); latest != "9.9.9" {
		t.Fatalf("priming read = %q, want 9.9.9", latest)
	}
	before := hits

	latest, needs, err := trayLookup(b, true)
	if err != nil {
		t.Fatalf("manual lookup: %v", err)
	}
	if latest != "9.9.9" || !needs {
		t.Errorf("manual lookup = (%q, %v), want (9.9.9, true)", latest, needs)
	}
	if hits != before+1 {
		t.Errorf("upstream hits grew by %d, want exactly 1 — a manual check must bypass the cache", hits-before)
	}
}

// TestTrayLookup_ManualWithoutHubLooksDirectly: a failed takeover leaves no
// hub, but the user still asked. The fallback must produce the same verdict
// the server would, which is why both go through upgrade.NeedsUpdate.
func TestTrayLookup_ManualWithoutHubLooksDirectly(t *testing.T) {
	hits := 0
	fakeReleaseOrigin(t, "9.9.9", &hits)
	pinReleaseBuild(t)

	b := &nativeBridge{}
	latest, needs, err := trayLookup(b, true)
	if err != nil {
		t.Fatalf("manual lookup without a hub: %v", err)
	}
	if latest != "9.9.9" || !needs {
		t.Errorf("manual lookup = (%q, %v), want (9.9.9, true)", latest, needs)
	}
	if needs != upgrade.NeedsUpdate("0.18.0", "9.9.9") {
		t.Error("fallback verdict diverges from upgrade.NeedsUpdate — the rule has been copied, not shared")
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d, want 1", hits)
	}
}

// TestTrayLookup_ManualDevBuildNeverNags: the eligibility rule reaches the
// tray's fallback too — a dev build is never told it is behind.
func TestTrayLookup_ManualDevBuildNeverNags(t *testing.T) {
	fakeReleaseOrigin(t, "9.9.9", nil)
	origV, origC := version.Version, version.Commit
	version.Version, version.Commit = "0.18.0-dev", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = origV, origC })

	b := &nativeBridge{}
	if _, needs, err := trayLookup(b, true); err != nil || needs {
		t.Errorf("dev build lookup = (needs %v, err %v), want (false, nil)", needs, err)
	}
}
