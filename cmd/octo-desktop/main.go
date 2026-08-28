// Command octo-desktop is octo's native desktop shell — and, while it runs, the
// single backend every other octo interface shares. It binds the fixed loopback
// port octo serve uses (127.0.0.1:8088), runs the same web server in-process,
// and points a Wails window at it, so the Svelte frontend and every /api and
// /ws handler are reused unchanged and the Web UI / VS Code / Obsidian / CLI all
// connect to this one instance. On top of the server it adds the native layer a
// browser can't reach — OS folder dialog, tray, launch-at-login, notifications —
// wired in through server.NativeBridge.
//
// Only one backend owns the port at a time: the app joins the ~/.octo/serve.pid
// protocol (internal/serveproc) that `octo serve -d` uses, offering to take over
// a running daemon rather than fighting for the port.
//
// See dev-docs/desktop-hub-design.md and dev-docs/wails-desktop-design.md.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/open-octo/octo-agent/internal/crashlog"
	"github.com/open-octo/octo-agent/internal/logfile"
	"github.com/open-octo/octo-agent/internal/serveenv"
	"github.com/open-octo/octo-agent/internal/serveproc"
	"github.com/open-octo/octo-agent/internal/server"
	"github.com/open-octo/octo-agent/internal/shellpath"
	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// Tray icons. macOS wants a monochrome template image (auto-tinted for the
// light/dark menu bar); Windows/Linux want the regular color icon. A tray with
// neither an icon nor a label is invisible on macOS, which is why one must be
// set explicitly.
//
//go:embed build/darwin/tray-icon.png
var trayTemplateIcon []byte

//go:embed build/linux/icon.png
var trayColorIcon []byte

// hubAddr is the fixed loopback address the hub owns — the same default
// `octo serve` binds, so every existing client (Web, VS Code, Obsidian, CLI)
// finds it without configuration. LAN exposure stays a CLI concern.
const hubAddr = "127.0.0.1:8088"

// isBundled reports whether we're running inside a .app. The Wails
// notifications service needs a bundle identifier and hard-fails startup
// without one, so it's registered only when bundled — a bare `make desktop`
// binary still runs, just without native notifications.
func isBundled() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// notificationsAvailable reports whether the OS-native notification service can
// be created on this platform. macOS's UNUserNotificationCenter needs a bundle
// identifier, so it only works from a .app bundle; Windows (a registered COM
// toast activator) and Linux (D-Bus) work for any running executable, so the
// tray update toast reaches all three platforms uniformly.
func notificationsAvailable() bool {
	if runtime.GOOS == "darwin" {
		return isBundled()
	}
	return true
}

// homeIfRootLaunch returns the directory the hub should switch to given its
// inherited working dir and the user's home, or "" to keep the inherited dir.
// A GUI launcher drops a double-clicked app at a filesystem root — "/" for
// Finder/LaunchServices on macOS and several Linux .desktop launchers, a drive
// root like "C:\" on Windows — detected here as a path that is its own parent;
// an os.Getwd error surfaces as an empty wd. In those cases home is the sane
// default. A meaningful cwd (a terminal launch from a project dir) is left
// untouched. Not covered: a Windows launch that inherits a system directory
// such as C:\Windows\System32 — that is not a root, so this leaves it in place.
func homeIfRootLaunch(wd, home string) string {
	if home == "" {
		return ""
	}
	if wd == "" || filepath.Dir(wd) == wd {
		return home
	}
	return ""
}

// ensureWorkingDir moves the process out of a root/unknown launch directory
// into the user's home. The server's launch dir still seeds skill discovery
// and the project-memory root (server.go), so a Finder-launched app left at
// "/" would otherwise run those from the filesystem root — the reported bug.
// Best effort: a chdir failure leaves the inherited dir in place.
func ensureWorkingDir() {
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	if dir := homeIfRootLaunch(wd, home); dir != "" {
		_ = os.Chdir(dir)
	}
}

// ensureValidTempDir unsets $TMPDIR when it doesn't point at a usable
// directory (missing, or a file rather than a directory), so Go's
// os.TempDir() (and the Wails updater's os.MkdirTemp("", ...) on top of it)
// falls back to the platform default instead of failing outright.
func ensureValidTempDir() {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		os.Unsetenv("TMPDIR")
	}
}

func main() {
	// macOS's postinstall script launches the app with `open` from inside
	// installd's ephemeral PKInstallSandbox.*; the launched process can inherit
	// that sandbox's $TMPDIR. The desktop app then runs for days as a tray
	// resident, long after the sandbox is torn down, so a later update check's
	// os.MkdirTemp fails with "no such file or directory" against a $TMPDIR
	// that hasn't existed since install. Clear it before anything (including
	// the updater helper below) can use it.
	ensureValidTempDir()

	// Point stderr at ~/.octo/crash.log before anything that can die runs. This
	// process has no usable stderr of its own (Windows: built with -H windowsgui,
	// so no console; macOS: launched from Finder), and the runtime writes panic
	// traces straight to the descriptor — below the slog/log redirection
	// setupHubLog installs later. Without this, an unrecovered panic in any
	// goroutine closes the window and leaves nothing behind to report. Ahead of
	// the helper mode below on purpose: swapping a staged update over a running
	// install is exactly the kind of file work whose failures need a record.
	setupCrashLog()

	// When spawned as the updater's helper child (sentinel env vars set), swap
	// the staged update over the installed app and exit — before any of the
	// launch side effects below (chdir, CLI/uv seeding, settings) run in a
	// process that only exists to copy files. No-op on a normal launch;
	// application.New would also catch it, just later.
	updater.HandleHelperMode()

	// A GUI launch inherits "/" as the working directory; move to the user's
	// home before anything reads it (the in-process server records its launch
	// dir as the skill-discovery and project-memory root).
	ensureWorkingDir()

	// A GUI launch also inherits a minimal PATH (macOS: no ~/.local/bin,
	// /opt/homebrew/bin; Linux: a .desktop/systemd launch may skip the login
	// profile entirely). The server runs in-process here, so stdio MCP children
	// and shell tools inherit this process's PATH directly — sync it to the login
	// shell's before server.New below, mirroring the `octo serve` binary.
	shellpath.SyncToLoginShell()

	// Load ~/.octo/serve.env for variables a GUI launch can't inherit from a
	// login shell (e.g. TAVILY_API_KEY, provider keys). Best-effort — missing
	// file is a no-op, explicit env always wins. Mirrors the `octo serve` CLI
	// path so both backends resolve the same set of environment variables.
	serveenv.Load()

	// Pick the language for native dialogs/tray from the system UI language.

	// Pick the language for native dialogs/tray from the system UI language.
	applyLang()

	settings := loadDesktopSettings()

	// Seed ~/.octo/bin/uv from the app's bundled copy on first run so skills
	// that need Python work even for a standalone download (no installer).
	ensureBundledUv()

	// Seed the octo CLI to ~/.local/bin (macOS + Linux) so a terminal has `octo`,
	// and on macOS put that dir on PATH. May update settings.SeededOctoVersion, so
	// it runs before the bridge takes its copy of settings below.
	ensureBundledOcto(&settings)

	bridge := &nativeBridge{settings: settings, url: "http://" + hubAddr}
	// On Windows/Linux a window close would otherwise quit the app; start with
	// quit allowed only when the user opted out of keep-running-in-background.
	bridge.allowQuit.Store(!settings.KeepRunningInBackground)

	// Native notifications where the platform supports them (see
	// notificationsAvailable — macOS requires a bundle, Windows/Linux don't).
	// Registered as a Wails service so its ServiceStartup runs; the bridge holds
	// it to send notifications the frontend and the tray update check request.
	var services []application.Service
	if notificationsAvailable() {
		notifier := notifications.New()
		bridge.notifier = notifier
		services = append(services, application.NewService(notifier))
	}

	app := application.New(application.Options{
		Name:        "Octo",
		Description: "Octo Agent",
		Services:    services,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.octo-agent.desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				bridge.showWindow()
			},
		},
		// ShouldQuit is consulted on every termination attempt. On Windows/Linux
		// that includes closing the last window, so returning allowQuit there
		// keeps the hub alive in the tray (reopen via "Show Octo" or relaunch)
		// until the user picks "Quit Octo". macOS never quits on window close
		// thanks to the option below and handles real quits (Cmd-Q) itself, so
		// it always allows the quit.
		ShouldQuit: func() bool {
			allow := func() bool {
				if runtime.GOOS == "darwin" {
					return true
				}
				// A confirmed update restart quits so the updater's helper (which
				// waits for this process to exit) can swap the binary. Vetoing
				// that quit (keep-running-in-background) would deadlock the
				// update. Keyed on the user's restart action, not on updater
				// state: a staged-but-deferred update ("remind me later") must
				// not quietly disable keep-running-in-background.
				if bridge.updateRestart.Load() {
					return true
				}
				return bridge.allowQuit.Load()
			}()
			// The shutdown that follows closes the window; the close hook must
			// let that destroy it rather than hide it (see closeShouldHide).
			if allow {
				bridge.quitting.Store(true)
			}
			return allow
		},
		Mac: application.MacOptions{
			// Closing the window must not quit the hub when the user wants it to
			// keep serving other clients in the background; the tray keeps it
			// reachable. When they opt out, last-window-close quits as usual.
			ApplicationShouldTerminateAfterLastWindowClosed: !settings.KeepRunningInBackground,
		},
		Windows: application.WindowsOptions{
			// Wails' Windows backend posts a quit message the moment its window
			// map empties (unregisterWindow), without consulting ShouldQuit —
			// so closing the window terminated the whole hub even when the user
			// asked it to keep running in the tray, and it also raced the
			// webview revive's window swap. Suppress that and let octo's own
			// ShouldQuit be the only authority, as it already is on mac/Linux;
			// the close path quits explicitly when the user opted out of
			// background running (see closeShouldQuit).
			DisableQuitOnLastWindowClosed: true,
			// Chromium's native-window occlusion tracker stops rendering a
			// WebView2 whose window is minimised or fully covered; on some
			// machines the compositor never paints again after restore,
			// leaving a permanently black window (WebView2Feedback#5171).
			// Disable the tracker so a backgrounded window keeps its render
			// pipeline alive. Wails appends this to its own disabled-feature
			// defaults. The cost is a still-rendering hidden webview, which
			// is acceptable for a tray-resident hub.
			DisabledFeatures: []string{"CalculateNativeWinOcclusion"},
		},
	})
	bridge.app = app

	// Configure the in-place updater when this build can swap itself (bundled
	// release build — see canInplaceUpdate). The flag routes the tray item and
	// the update toast through app.Updater instead of the download page.
	bridge.inplaceUpdate.Store(canInplaceUpdate() && initInplaceUpdater(app))

	// The updater window's Restart action quits the app so the helper can
	// swap the binary — flag it for ShouldQuit above. Registered here, before
	// any updater session opens its own (goroutine-spawning) restart handler,
	// so the flag is set by the time the quit is dispatched.
	app.Event.On(updater.EventUserRestart, func(*application.CustomEvent) {
		bridge.updateRestart.Store(true)
	})

	// Interacting with the "update available" toast starts the update flow;
	// session notifications focus and route to the relevant session; every other
	// notification just raises the window. Match the category (which all three
	// platform notifiers echo back) and then only the "Open" action or a tap
	// on the body — so dismissing the toast never triggers an action.
	if bridge.notifier != nil {
		bridge.notifier.OnNotificationResponse(func(res notifications.NotificationResult) {
			if res.Response.CategoryID == updateNotifyCategoryID {
				switch res.Response.ActionIdentifier {
				case updateNotifyOpenActionID, notifications.DefaultActionIdentifier:
					go startUpdateFlow(bridge)
				}
				return
			}
			if sid, ok := res.Response.UserInfo["session_id"].(string); ok && sid != "" {
				bridge.showWindowAt("chat/" + url.PathEscape(sid))
				return
			}
			bridge.showWindow()
		})
	}

	// System tray: reach the window or fully quit without hunting for the dock
	// icon. Quit goes through requestQuit so it can warn when stopping the hub
	// would disconnect other clients. An icon is required — a status item with
	// neither icon nor label doesn't render on macOS.
	tray := app.SystemTray.New()
	bridge.tray.Store(tray) // let update checks refresh the menu on their own goroutine
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayColorIcon)
	}
	tray.SetTooltip(L().takeoverTitle)
	tray.SetMenu(buildTrayMenu(app, bridge))
	// Keep the tray's status lines (backend, channels, connected clients) fresh
	// while the app runs — macOS doesn't refresh a status menu on open.
	go refreshTrayLoop(app, tray, bridge)

	// Bind + serve + open the window once the event loop is up, so the takeover
	// prompt (a modal dialog) can run. Doing it here rather than before Run lets
	// us ask the user before stopping someone else's backend.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Register the update-toast action category once the notification
		// service has started (Windows/Linux drop the action buttons if a
		// notification is sent before its category is registered).
		bridge.registerUpdateNotifyCategory()
		// Prompt for notification permission (macOS blocks until answered, so
		// off the UI thread) — without it every toast silently no-ops.
		go bridge.requestNotificationAuthorization()
		startHub(app, bridge, settings)
		// Surface a newer release in the tray without the user asking: a delayed
		// first check, then daily. Foreground-suppressed toasts don't matter here
		// — the tray item is the durable signal.
		go autoUpdateLoop(bridge)
	})

	// macOS: clicking the dock icon after the window was closed (hidden to the
	// tray) fires "reopen" — re-create/show the window instead of no-op'ing.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		bridge.showWindow()
	})

	err := app.Run()

	// The app has quit: release our pid-file entry (only if it's still ours —
	// a successor that took the port over must keep its own) and shut the
	// server down cleanly.
	serveproc.ReleaseOwned(os.Getpid())
	if srv := bridge.srv.Load(); srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	if closeLog := bridge.closeLog.Load(); closeLog != nil {
		(*closeLog)()
	}
	if err != nil {
		log.Fatalf("octo-desktop: %v", err)
	}
}

// setupCrashLog redirects the process's stderr to ~/.octo/crash.log so a panic
// that kills the app leaves a trace behind. Best-effort: if it fails there is
// nowhere to report that failure to (that being the whole problem), so the app
// starts anyway.
func setupCrashLog() {
	// Started from a terminal (a developer running the binary directly): that
	// terminal is a better place for a crash than a file nobody is tailing, and
	// taking stderr away would leave them staring at a silent window. The
	// shipped app never gets here — a -H windowsgui build has no console even
	// when launched from one, and a Finder/launchd launch has no terminal.
	if isatty.IsTerminal(os.Stderr.Fd()) {
		return
	}
	path, err := serveproc.CrashLogPath()
	if err != nil {
		return
	}
	banner := fmt.Sprintf("octo-desktop %s (%s/%s)", version.Version, runtime.GOOS, runtime.GOARCH)
	_ = crashlog.Install(path, banner)
}

// setupHubLog routes slog and the stdlib logger to a self-rotating
// ~/.octo/serve.log and returns a close func (nil if setup failed, leaving the
// default stderr in place). The stdlib logger is redirected too so the channel
// adapters' error/retry lines — still on `log` — land in the same file.
func setupHubLog() func() {
	logPath, err := serveproc.LogPath()
	if err != nil {
		return nil
	}
	lw, err := logfile.Open(logPath, logfile.DefaultMaxBytes, logfile.DefaultBackups)
	if err != nil {
		return nil
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(lw, &slog.HandlerOptions{Level: hubLogLevel()})))
	log.SetOutput(lw)
	return func() { _ = lw.Close() }
}

// hubLogLevel reads OCTO_LOG_LEVEL (debug|info|warn|error), defaulting to info —
// matching `octo serve`'s level handling so the two backends behave alike.
func hubLogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCTO_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// startHub takes ownership of the loopback port (offering to take over a running
// daemon), starts the in-process server, and opens the window. It runs inside
// the ApplicationStarted hook so its dialogs have a live event loop.
func startHub(app *application.App, bridge *nativeBridge, settings desktopSettings) {
	// If another backend already owns the port, ask before displacing it.
	tookOver := false
	if pid, ok := serveproc.Running(); ok {
		if !bridge.confirmTakeover(pid) {
			app.Quit()
			return
		}
		if _, err := serveproc.Stop(); err != nil {
			bridge.showError(L().errTitle, fmt.Sprintf(L().errStopFmt, err))
			app.Quit()
			return
		}
		tookOver = true
	}

	// After a takeover, the stopped daemon needs a moment to release the port —
	// serveproc.Stop only signals it. Retry the bind for a few seconds so the
	// handoff is seamless; a cold start with a genuine conflict fails at once.
	grace := time.Duration(0)
	if tookOver {
		grace = 8 * time.Second
	}
	ln, err := listenHub(hubAddr, grace)
	if err != nil {
		bridge.showError(L().errTitle, fmt.Sprintf(L().errBindFmt, hubAddr, err))
		app.Quit()
		return
	}
	if path, perr := serveproc.PidPath(); perr == nil {
		_ = serveproc.WritePid(path, os.Getpid())
	}

	// Only now, having taken over any prior daemon and bound the port, are we the
	// sole backend — so it's safe to open the shared ~/.octo/serve.log. A prior
	// `octo serve -d` that was stopped above has since exited (listenHub only
	// succeeds once the port is free), releasing the fd it held on the file;
	// opening/rotating earlier (e.g. in main, before the takeover) could rotate a
	// file a live daemon still holds open — on Windows the rename would fail
	// outright and drop us to a console for the whole session. Set up before
	// server.New so the hub's own startup logs are captured too.
	if closeLog := setupHubLog(); closeLog != nil {
		bridge.closeLog.Store(&closeLog)
	}

	// Also now-we're-the-sole-backend: age out old upload/attachment files
	// (#2004). The desktop hub never goes through cmd/octo's runServe, so
	// without this call here it would silently never run — this IS the
	// long-running "desktop hub" instance the housekeeping is for.
	server.StartUploadsHousekeeping()

	srv, err := server.New(server.Config{
		Tools: true,
		// On: the version badge needs the latest-release lookup to know an update
		// exists. It reports upgrade_mode "installer" (Native is set), so the web
		// UI offers a download link; the desktop shell's own in-place update flow
		// lives in the tray + update toast (see startUpdateFlow), not the badge.
		UpdateCheck: true,
		Native:      bridge,
		// The desktop server runs in-process — there is no supervisor to
		// respawn it after a restart, so the restart_server tool would just
		// shut the HTTP server down and leave the GUI window attached to a
		// dead backend. Omit the tool; the desktop shell owns its own update
		// lifecycle (Check for Updates → installer).
		DisableRestart: true,
	})
	if err != nil {
		bridge.showError(L().errTitle, fmt.Sprintf(L().errStartFmt, err))
		app.Quit()
		return
	}
	bridge.srv.Store(srv)
	go func() {
		if err := srv.ServeOn(ln); err != nil {
			log.Printf("octo-desktop: server stopped: %v", err)
		}
	}()

	bridge.showWindow()
}

// checkForUpdates is the tray "Check for updates…" action — a manual check that
// always reports its result.
func checkForUpdates(bridge *nativeBridge) { runUpdateCheck(bridge, true) }

// autoUpdateLoop checks for a newer release on its own, so the tray can show one
// without the user asking. A delayed first check keeps startup uncontended, then
// a daily cadence. Auto checks are silent unless they turn up a new version, and
// even then only when it differs from the one already surfaced — the tray item,
// not a daily toast, is the standing reminder.
func autoUpdateLoop(bridge *nativeBridge) {
	time.Sleep(30 * time.Second)
	runUpdateCheck(bridge, false)
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for range t.C {
		runUpdateCheck(bridge, false)
	}
}

// runUpdateCheck performs one update lookup and records the outcome on the
// bridge so the tray can show a persistent, clickable "download" item — the
// durable signal, since macOS suppresses the toast while the app is foreground
// (exactly when a manual check runs). It runs on a background goroutine (never
// the UI thread) so the network round-trip can't freeze the menu.
//
// manual checks always report via an OS toast (failure, already-current, or the
// actionable "update available" toast whose button and body tap open the
// download page). auto checks stay silent except when they surface a version
// not already shown, so the daily cadence doesn't nag. Toasts are best-effort —
// on a build without the notification service (an unbundled macOS binary) they
// no-op, matching the version badge's own silence there.
func runUpdateCheck(bridge *nativeBridge, manual bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	latest, err := upgrade.Check(ctx)
	if err != nil {
		if manual {
			bridge.Notify(L().updTitle, L().updFailed)
		}
		return
	}
	current := strings.TrimPrefix(version.Version, "v")
	// Eligible() != nil means a dev/unbundled build that never claims to be
	// behind (matching the badge); report status without offering a download.
	if upgrade.Eligible() != nil || upgrade.CompareVersions(current, latest) >= 0 {
		bridge.updateAvailable.Store(nil)
		bridge.refreshTray()
		if manual {
			bridge.Notify(L().updTitle, fmt.Sprintf(L().updLatestFmt, current))
		}
		return
	}
	prev := bridge.updateAvailable.Load()
	bridge.updateAvailable.Store(&latest)
	bridge.refreshTray()
	if manual || prev == nil || *prev != latest {
		bridge.NotifyUpdateAvailable(L().updTitle, fmt.Sprintf(L().updAvailableFmt, latest))
	}
}

// listenHub binds addr, retrying for up to grace so a just-stopped daemon has
// time to release the port (SIGTERM only signals it; the listener closes a
// beat later). grace of 0 means a single attempt.
func listenHub(addr string, grace time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(grace)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// trayStatusLines is the (info-only) top of the tray menu: what the hub is
// doing right now — where it's serving and how many clients are attached.
func trayStatusLines(bridge *nativeBridge) []string {
	srv := bridge.srv.Load()
	if srv == nil {
		return []string{L().trayStarting}
	}
	lines := []string{fmt.Sprintf(L().trayBackendFmt, hubAddr)}
	lines = append(lines, fmt.Sprintf(L().trayClientsFmt, srv.ConnectedClients()))
	// Only when > 0 — keeps the menu clean before any channel is configured.
	if n := srv.ConfiguredChannelCount(); n > 0 {
		lines = append(lines, fmt.Sprintf(L().trayChannelsFmt, n))
	}
	return lines
}

// buildTrayMenu assembles the tray menu: disabled status lines on top, then the
// Show/Quit actions. Rebuilt (not mutated in place) so a refresh is one
// SetMenu call, which Wails marshals to the UI thread.
func buildTrayMenu(app *application.App, bridge *nativeBridge) *application.Menu {
	m := app.NewMenu()
	for _, line := range trayStatusLines(bridge) {
		m.Add(line).SetEnabled(false)
	}
	m.AddSeparator()
	m.Add(L().trayShow).OnClick(func(*application.Context) { bridge.showWindow() })
	m.Add(L().trayNewSession).OnClick(func(*application.Context) { bridge.openNewSession() })
	m.Add(L().traySettings).OnClick(func(*application.Context) { bridge.openSettings() })
	// A known-newer release replaces the "check" item with a one-click update
	// (in-place when this build supports it, else the download page) — the
	// durable prompt when the toast was suppressed. Otherwise the manual check.
	if v := bridge.updateAvailable.Load(); v != nil {
		m.Add(fmt.Sprintf(L().trayUpdateAvailFmt, *v)).OnClick(func(*application.Context) {
			go startUpdateFlow(bridge)
		})
	} else {
		m.Add(L().trayCheckUpdates).OnClick(func(*application.Context) { go checkForUpdates(bridge) })
	}
	m.AddSeparator()
	m.Add(L().trayQuit).OnClick(func(*application.Context) { bridge.requestQuit() })
	return m
}

// refreshTrayLoop re-publishes the tray menu whenever its status text changes,
// so the counts stay live without rebuilding on every tick.
func refreshTrayLoop(app *application.App, tray *application.SystemTray, bridge *nativeBridge) {
	sigOf := func() string {
		applyLang() // follow a language switch made in onboarding / Settings
		// The status lines are language-dependent, so a language switch changes
		// this signature and triggers a rebuild (which re-reads L() for labels).
		// The update state is folded in too, so a flip rebuilds even if the
		// immediate refreshTray call raced with a concurrent rebuild.
		upd := ""
		if v := bridge.updateAvailable.Load(); v != nil {
			upd = *v
		}
		return strings.Join(trayStatusLines(bridge), "|") + "\x00" + upd
	}
	last := sigOf()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for range t.C {
		sig := sigOf()
		if sig == last {
			continue
		}
		last = sig
		tray.SetMenu(buildTrayMenu(app, bridge))
	}
}
