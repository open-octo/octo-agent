package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
)

// Version-cache timings.
//
// versionRefreshInterval is how often a lookup may actually leave the machine.
// It is deliberately far longer than how often the UI reads the answer: reads
// are served from cache and cost nothing, so the tray and the web badge can
// both re-read every minute (which is what keeps them showing the same thing)
// while the network is touched four times a day.
//
// versionCheckBackoff holds off retrying after a failure.
//
// versionCheckTimeout budgets the WHOLE upgrade.Check call, not one attempt.
// Check walks GitHub plus the mirrors, giving each a sub-context bounded by
// this parent, so a budget below one attempt window means a slow primary eats
// it all and every mirror gets an already-expired context — the mirrors may as
// well not exist. It used to be 3s, which is exactly why the web badge could
// insist "up to date" on a network where the desktop tray (10s, so it reached
// dl.octo-agent.dev) had already found the release. 10s buys GitHub plus the
// project's own mirror; the remaining public proxies are best-effort and not
// budgeted for. Nothing waits on this: the lookup runs on its own goroutine.
const (
	versionRefreshInterval = 6 * time.Hour
	versionCheckBackoff    = 10 * time.Minute
	versionCheckTimeout    = 10 * time.Second
)

// ─── GET /api/version ────────────────────────────────────────────────────────

// handleVersion reports the running version plus update availability.
// `version` is the original field; `current`/`latest`/`needs_update`/
// `cli_command` are what the web badge reads. cli_command is a constant —
// the endpoint is unauthenticated, so the executable's filesystem path
// must not leak here.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	current := strings.TrimPrefix(version.Version, "v")
	latest, needs := s.LatestVersion()
	// The response includes the running binary version, which changes after an
	// upgrade/restart. Tell browsers and intermediaries not to cache it so the
	// badge reflects the currently running server immediately.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	response := map[string]any{
		"version":      version.Version,
		"current":      current,
		"latest":       latest,
		"needs_update": needs,
		"cli_command":  "octo",
		// native is true in the Wails desktop build (a NativeBridge is wired):
		// the frontend can use OS dialogs/notifications. local is true when the
		// browser is genuinely on this machine — desktop or localhost web —
		// telling the frontend to pick files/folders by real path instead of
		// uploading. A peer that merely reaches us over loopback because
		// something forwards the port (ngrok, a proxy, octo's own tunnel) is not
		// local and falls back to upload; see isLocalRequest.
		"native": s.cfg.Native != nil,
		"local":  isLocalRequest(r),
		// os lets the frontend gate platform-specific UI (e.g. the experimental
		// computer-use toggle is meaningful only on the macOS and Windows desktop).
		"os": runtime.GOOS,
		// os_version is the host's macOS product version (e.g. "26.5.2"; empty
		// elsewhere). The titlebar rows read it to sit on the traffic lights'
		// axis, which macOS 26 moved for windows stamped with the macOS 26 SDK.
		"os_version": OSVersion(),
		// upgrade_mode tells the badge which update mechanism this server offers:
		// "cli" — the in-place binary swap of POST /api/version/upgrade (octo
		// serve); "installer" — the desktop build, whose binary can't be swapped
		// in place (it would overwrite the GUI / break the macOS bundle), so the
		// UI offers a download link instead. Derived, not configured.
		"upgrade_mode": s.upgradeMode(),
		// download_url is where "Download update" sends the user — the marketing
		// site's per-platform installer buttons, not the raw releases listing — so
		// the frontend needn't hardcode it. Constant; this endpoint is unauthenticated.
		"download_url": upgrade.DownloadPageURL,
		// self_update: the desktop build can swap itself in place (POST
		// /api/native/self-update), so a loopback badge may offer "Update Now"
		// instead of only the download link. Remote peers still get the link —
		// the native route refuses them regardless.
		"self_update": s.cfg.Native != nil && s.cfg.Native.CanSelfUpdate(),
	}
	if profile := strings.TrimSpace(os.Getenv(datahome.ProfileEnv)); profile != "" {
		response["profile"] = profile
	}
	writeJSON(w, http.StatusOK, response)
}

// upgradeMode reports how this server updates: "installer" for the desktop
// build (a NativeBridge is wired and its binary is installer-managed), "cli"
// otherwise (octo serve's in-place swap).
func (s *Server) upgradeMode() string {
	if s.cfg.Native != nil {
		return "installer"
	}
	return "cli"
}

// LatestVersion reports the latest known release and whether this build is
// behind it. It reads the shared cache and never performs the lookup on the
// caller's goroutine — a stale cache instead kicks off one background refresh
// and this call answers with what is already known.
//
// It is the single source of truth for "is there an update": GET /api/version
// serves exactly this, and the desktop tray reads it too. Because a read is
// free, both surfaces can re-read often enough to display the same answer at
// all times, which is the whole point — they used to keep private caches on
// different cadences and drift apart.
func (s *Server) LatestVersion() (string, bool) {
	current := strings.TrimPrefix(version.Version, "v")
	if !s.cfg.UpdateCheck {
		return current, false
	}
	latest, needs, stale := s.versionSnapshot(current)
	if stale && s.updateCheckAllowed() {
		s.startVersionRefresh()
	}
	return latest, needs
}

// updateCheckAllowed reports the user's `update_check` preference — the switch
// that stops octo making the one request it makes without being asked.
//
// It is consulted only where a lookup would actually be started, never on the
// way to a cached answer: both the badge and the tray now read every minute,
// and a config.Load() on that path would be a disk read and a YAML parse per
// read (on an unauthenticated endpoint, at that). The switch promises that no
// request is sent, which this placement keeps exactly; the cost is that a
// cached `latest` can linger up to one refresh interval after it is flipped.
//
// LoadCached, not Load: a hand-edit that leaves config.yml unparseable must
// not silently re-enable the check. LoadCached keeps serving the last config
// that parsed, so `update_check: false` survives a broken edit — only a config
// that has never once loaded falls back to the built-in default (enabled).
func (s *Server) updateCheckAllowed() bool {
	cfg, err := config.LoadCached()
	return err != nil || cfg.UpdateCheckEnabled()
}

// RefreshLatestVersion performs the lookup on THIS goroutine, ignoring the
// refresh interval and the failure backoff, and seeds the shared cache with
// the result so every other surface agrees on its next read. The desktop
// tray's explicit "Check for updates…" calls this: the user asking is not a
// request to be served a cached answer, and the tray can afford to block
// where an HTTP handler cannot.
//
// Config.UpdateCheck still gates it — a build that never looks (the test
// suite, every non-serve constructor) must not be talked into looking.
func (s *Server) RefreshLatestVersion(ctx context.Context) (string, bool, error) {
	current := strings.TrimPrefix(version.Version, "v")
	if !s.cfg.UpdateCheck {
		return current, false, errUpdateCheckDisabled
	}
	return s.runVersionCheck(ctx, current)
}

// errUpdateCheckDisabled reports that this build performs no lookups at all,
// so the caller was handed no answer rather than a stale one. Returning a nil
// error with current-is-latest would have the tray cheerfully toast "you're on
// the newest version" on a server that never looked.
var errUpdateCheckDisabled = errors.New("update check is disabled for this server")

// versionSnapshot reads the cache without touching the network. stale reports
// that a refresh is due — the cache has aged out (or was never filled) and no
// failure backoff is in effect.
func (s *Server) versionSnapshot(current string) (latest string, needs bool, stale bool) {
	s.versionCacheMu.RLock()
	defer s.versionCacheMu.RUnlock()

	now := time.Now()
	fresh := s.versionLatest != "" && now.Sub(s.versionCheckedAt) < versionRefreshInterval
	backedOff := !s.versionFailedAt.IsZero() && now.Sub(s.versionFailedAt) < versionCheckBackoff

	// What a failed or not-yet-due read reports is the last release we
	// actually saw, not "current". A lookup failing does not un-release a
	// version — reporting current-is-latest is how the badge ended up claiming
	// "up to date" while the tray, which keeps its own last answer, still
	// offered the download. Only a server that has never once succeeded falls
	// back to current.
	if s.versionLatest == "" {
		return current, false, !backedOff
	}
	return s.versionLatest, upgrade.NeedsUpdate(current, s.versionLatest), !fresh && !backedOff
}

// startVersionRefresh runs one lookup in the background, at most one at a
// time. The caller does not wait: GET /api/version also carries
// native/local/os_version, which the frontend needs promptly, and a cold
// upgrade.Check can take the full versionCheckTimeout on a network where
// GitHub is slow. Whoever reads next picks the result up.
func (s *Server) startVersionRefresh() {
	if !s.versionChecking.CompareAndSwap(false, true) {
		return // one already in flight
	}
	current := strings.TrimPrefix(version.Version, "v")
	// Read the cache again now that the token is held. The caller judged it
	// stale BEFORE taking the token, and in between the refresh it lost the
	// CAS to can have published its result and released the token — so the
	// answer this lookup would fetch is already in hand. Holding the token
	// makes the re-read conclusive: a finishing refresh publishes under the
	// cache lock and only then releases the token (deferred in that order),
	// so nothing can be in flight and unpublished while we are here.
	//
	// Without this, every reader polling while a refresh is in flight had a
	// window in which it started a redundant second lookup — one extra
	// request to GitHub for an answer already cached, on the one path that
	// promises to leave the machine four times a day.
	if _, _, stale := s.versionSnapshot(current); !stale {
		s.versionChecking.Store(false)
		return
	}
	s.versionRefreshWG.Add(1)
	go func() {
		defer s.versionRefreshWG.Done()
		defer s.versionChecking.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), versionCheckTimeout)
		defer cancel()
		// Background-derived, not a request context: a client navigating away
		// mid-check would otherwise record a failure and degrade the answer
		// for every other reader for the whole backoff window.
		_, _, _ = s.checkAndStore(ctx, current)
	}()
}

// awaitVersionRefresh waits for an in-flight background lookup to finish, so
// the goroutine does not outlive the server. Bounded by ctx: the lookup has
// its own versionCheckTimeout, and a shutdown must not wait out a slow GitHub.
func (s *Server) awaitVersionRefresh(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.versionRefreshWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// runVersionCheck is the forced path: it waits for an in-flight background
// refresh rather than racing it, then performs its own lookup.
func (s *Server) runVersionCheck(ctx context.Context, current string) (string, bool, error) {
	for !s.versionChecking.CompareAndSwap(false, true) {
		select {
		case <-ctx.Done():
			latest, needs, _ := s.versionSnapshot(current)
			return latest, needs, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer s.versionChecking.Store(false)
	return s.checkAndStore(ctx, current)
}

// checkAndStore performs the lookup and records the outcome. The network call
// happens with NO lock held — the cache lock is taken only to publish the
// result, so a slow GitHub never blocks a reader. Callers hold the
// versionChecking token.
func (s *Server) checkAndStore(ctx context.Context, current string) (string, bool, error) {
	latest, err := upgrade.Check(ctx)
	now := time.Now()

	s.versionCacheMu.Lock()
	if err != nil {
		s.versionFailedAt = now
	} else {
		s.versionLatest, s.versionCheckedAt, s.versionFailedAt = latest, now, time.Time{}
	}
	known := s.versionLatest
	s.versionCacheMu.Unlock()

	if err != nil {
		if known == "" {
			return current, false, err
		}
		return known, upgrade.NeedsUpdate(current, known), err
	}
	return latest, upgrade.NeedsUpdate(current, latest), nil
}

// ─── POST /api/version/upgrade ──────────────────────────────────────────────

// handleVersionUpgrade starts a binary upgrade in the background,
// single-flight. Progress streams over WS as upgrade_log lines; every
// outcome — including refusals past this point — ends in an
// upgrade_complete broadcast, because the frontend ignores this response's
// status code and only a completion event unwedges its popover.
func (s *Server) handleVersionUpgrade(w http.ResponseWriter, r *http.Request) {
	// The desktop build updates through its installer — its running binary is
	// the GUI, not the octo CLI the release archive carries, so an in-place swap
	// would corrupt it (and break the macOS bundle signature). Refuse here as
	// defense in depth: the installer-mode frontend never posts this, but the
	// route is registered unconditionally, so a remote peer must not drive it.
	if s.cfg.Native != nil {
		writeError(w, http.StatusConflict, "this build updates through its installer; use the download link")
		return
	}

	s.upgradeMu.Lock()
	if s.upgradeRunning {
		s.upgradeMu.Unlock()
		writeError(w, http.StatusConflict, "an upgrade is already in progress")
		return
	}
	s.upgradeRunning = true
	s.upgradeMu.Unlock()

	go s.runBinaryUpgrade()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "upgrade started"})
}

// runBinaryUpgrade downloads outside the restart drain gate and holds the
// gate only for the swap: a restart requested mid-install drains behind
// the renames instead of exiting between them — which would leave no file
// at the target path and make the supervisor respawn fail outright.
func (s *Server) runBinaryUpgrade() {
	defer func() {
		s.upgradeMu.Lock()
		s.upgradeRunning = false
		s.upgradeMu.Unlock()
	}()

	logf := func(line string) {
		s.broadcastGlobal(map[string]any{"type": "upgrade_log", "line": line})
	}
	complete := func(ok bool) {
		s.broadcastGlobal(map[string]any{"type": "upgrade_complete", "success": ok})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// No Force over HTTP: a dev-build server refuses inside Prepare, and
	// the refusal (with its --force CLI hint) reaches the user as a log
	// line. The slow part (download/verify/extract) runs OUTSIDE the
	// drain gate — Restart only waits 30s for gate holders before
	// proceeding, so holding it across a download would reopen the
	// exit-between-renames window the gate exists to close.
	p, err := upgrade.Prepare(ctx, upgrade.Options{Log: logf})
	switch {
	case errors.Is(err, upgrade.ErrUpToDate):
		logf("already up to date")
		complete(false)
		return
	case err != nil:
		logf("upgrade failed: " + err.Error())
		complete(false)
		return
	}
	defer p.Close()

	// The swap itself is local renames — seconds, comfortably inside the
	// restart drain bound.
	if err := s.drain.begin(); err != nil {
		logf("the server is restarting — upgrade aborted before install; try again once it is back")
		complete(false)
		return
	}
	err = p.Install()
	s.drain.end()
	if err != nil {
		logf("upgrade failed: " + err.Error())
		complete(false)
		return
	}
	complete(true)
}

// broadcastGlobal sends an event to every WS connection. Nil-safe so
// Server constructions without a WS hub (tests) don't block on a nil
// channel.
func (s *Server) broadcastGlobal(event any) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.broadcast("", event)
}
