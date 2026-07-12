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
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/logfile"
	"github.com/open-octo/octo-agent/internal/serveproc"
	"github.com/open-octo/octo-agent/internal/server"
	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
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

func main() {
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
			if runtime.GOOS == "darwin" {
				return true
			}
			return bridge.allowQuit.Load()
		},
		Mac: application.MacOptions{
			// Closing the window must not quit the hub when the user wants it to
			// keep serving other clients in the background; the tray keeps it
			// reachable. When they opt out, last-window-close quits as usual.
			ApplicationShouldTerminateAfterLastWindowClosed: !settings.KeepRunningInBackground,
		},
	})
	bridge.app = app

	// Interacting with the "update available" toast opens the download page;
	// every other notification raises the window. Match the category (which all
	// three platform notifiers echo back) and then only the "Open" action or a
	// tap on the body — so dismissing the toast, whatever identifier a platform
	// reports for that, never opens a browser.
	if bridge.notifier != nil {
		bridge.notifier.OnNotificationResponse(func(res notifications.NotificationResult) {
			if res.Response.CategoryID == updateNotifyCategoryID {
				switch res.Response.ActionIdentifier {
				case updateNotifyOpenActionID, notifications.DefaultActionIdentifier:
					_ = bridge.OpenExternal(upgrade.DownloadPageURL)
				}
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
		startHub(app, bridge, settings)
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

	// Channels start only when the per-machine toggle is on (default off): the
	// hub is a hub for the UI/sessions immediately, but launching the GUI never
	// silently starts an IM bridge. The toggle is flipped at runtime via
	// /api/native/channels.
	channelsOn := settings.ChannelsEnabled
	srv, err := server.New(server.Config{
		Tools: true,
		// On: the version badge needs the latest-release lookup to know an update
		// exists. It reports upgrade_mode "installer" (Native is set), so the UI
		// offers a download link, not the in-place swap this build can't do.
		UpdateCheck:     true,
		Native:          bridge,
		ChannelsEnabled: &channelsOn,
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

// checkForUpdates is the tray "Check for updates…" action. It runs on a
// background goroutine (never the UI thread) so the network round-trip can't
// freeze the menu, then reports via a non-intrusive OS toast rather than a
// modal dialog: a failed lookup and the already-current case are plain toasts;
// a newer release raises the actionable "update available" toast whose button
// (and body tap) opens the download page. Toasts are best-effort — on a
// platform/build without the notification service (an unbundled macOS binary)
// they no-op, matching the version badge's own silence there.
func checkForUpdates(bridge *nativeBridge) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	latest, err := upgrade.Check(ctx)
	if err != nil {
		bridge.Notify(L().updTitle, L().updFailed)
		return
	}
	current := strings.TrimPrefix(version.Version, "v")
	// Eligible() != nil means a dev/unbundled build that never claims to be
	// behind (matching the badge); report status without offering a download.
	if upgrade.Eligible() != nil || upgrade.CompareVersions(current, latest) >= 0 {
		bridge.Notify(L().updTitle, fmt.Sprintf(L().updLatestFmt, current))
		return
	}
	bridge.NotifyUpdateAvailable(L().updTitle, fmt.Sprintf(L().updAvailableFmt, latest))
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
// doing right now — where it's serving, whether it's bridging IM channels
// (and which), and how many clients are attached.
func trayStatusLines(bridge *nativeBridge) []string {
	srv := bridge.srv.Load()
	if srv == nil {
		return []string{L().trayStarting}
	}
	lines := []string{fmt.Sprintf(L().trayBackendFmt, hubAddr)}
	if srv.ChannelsEnabled() {
		if running := srv.RunningChannels(); len(running) > 0 {
			lines = append(lines, fmt.Sprintf(L().trayChannelsOnFmt, len(running), strings.Join(running, ", ")))
		} else {
			lines = append(lines, L().trayChannelsOnNone)
		}
	} else {
		lines = append(lines, L().trayChannelsOff)
	}
	lines = append(lines, fmt.Sprintf(L().trayClientsFmt, srv.ConnectedClients()))
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
	m.Add(L().traySettings).OnClick(func(*application.Context) { bridge.openSettings() })
	m.Add(L().trayCheckUpdates).OnClick(func(*application.Context) { go checkForUpdates(bridge) })
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
		return strings.Join(trayStatusLines(bridge), "|")
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
