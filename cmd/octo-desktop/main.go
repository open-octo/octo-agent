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
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/serveproc"
	"github.com/open-octo/octo-agent/internal/server"
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

func main() {
	// Pick the language for native dialogs/tray from the system UI language.
	detectLang()

	settings := loadDesktopSettings()

	// Seed ~/.octo/bin/uv from the app's bundled copy on first run so skills
	// that need Python work even for a standalone download (no installer).
	ensureBundledUv()

	bridge := &nativeBridge{settings: settings, url: "http://" + hubAddr}

	// Native notifications only when bundled (see isBundled): the service needs
	// a bundle identifier. Registered as a Wails service so its ServiceStartup
	// runs; the bridge holds it to send notifications the frontend requests.
	var services []application.Service
	if isBundled() {
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
		Mac: application.MacOptions{
			// Closing the window must not quit the hub when the user wants it to
			// keep serving other clients in the background; the tray keeps it
			// reachable. When they opt out, last-window-close quits as usual.
			ApplicationShouldTerminateAfterLastWindowClosed: !settings.KeepRunningInBackground,
		},
	})
	bridge.app = app

	// Clicking a notification raises the window — that's its whole point.
	if bridge.notifier != nil {
		bridge.notifier.OnNotificationResponse(func(notifications.NotificationResult) {
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
	tray.SetTooltip(L.takeoverTitle)
	tray.SetMenu(buildTrayMenu(app, bridge))
	// Keep the tray's status lines (backend, channels, connected clients) fresh
	// while the app runs — macOS doesn't refresh a status menu on open.
	go refreshTrayLoop(app, tray, bridge)

	// Bind + serve + open the window once the event loop is up, so the takeover
	// prompt (a modal dialog) can run. Doing it here rather than before Run lets
	// us ask the user before stopping someone else's backend.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
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
	if err != nil {
		log.Fatalf("octo-desktop: %v", err)
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
			bridge.showError(L.errTitle, fmt.Sprintf(L.errStopFmt, err))
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
		bridge.showError(L.errTitle, fmt.Sprintf(L.errBindFmt, hubAddr, err))
		app.Quit()
		return
	}
	if path, perr := serveproc.PidPath(); perr == nil {
		_ = serveproc.WritePid(path, os.Getpid())
	}

	// Channels start only when the per-machine toggle is on (default off): the
	// hub is a hub for the UI/sessions immediately, but launching the GUI never
	// silently starts an IM bridge. The toggle is flipped at runtime via
	// /api/native/channels.
	channelsOn := settings.ChannelsEnabled
	srv, err := server.New(server.Config{
		Tools:           true,
		UpdateCheck:     false,
		Native:          bridge,
		ChannelsEnabled: &channelsOn,
	})
	if err != nil {
		bridge.showError(L.errTitle, fmt.Sprintf(L.errStartFmt, err))
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
		return []string{L.trayStarting}
	}
	lines := []string{fmt.Sprintf(L.trayBackendFmt, hubAddr)}
	if srv.ChannelsEnabled() {
		if running := srv.RunningChannels(); len(running) > 0 {
			lines = append(lines, fmt.Sprintf(L.trayChannelsOnFmt, len(running), strings.Join(running, ", ")))
		} else {
			lines = append(lines, L.trayChannelsOnNone)
		}
	} else {
		lines = append(lines, L.trayChannelsOff)
	}
	lines = append(lines, fmt.Sprintf(L.trayClientsFmt, srv.ConnectedClients()))
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
	m.Add(L.trayShow).OnClick(func(*application.Context) { bridge.showWindow() })
	m.Add(L.traySettings).OnClick(func(*application.Context) { bridge.openSettings() })
	m.AddSeparator()
	m.Add(L.trayQuit).OnClick(func(*application.Context) { bridge.requestQuit() })
	return m
}

// refreshTrayLoop re-publishes the tray menu whenever its status text changes,
// so the counts stay live without rebuilding on every tick.
func refreshTrayLoop(app *application.App, tray *application.SystemTray, bridge *nativeBridge) {
	last := strings.Join(trayStatusLines(bridge), "|")
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for range t.C {
		sig := strings.Join(trayStatusLines(bridge), "|")
		if sig == last {
			continue
		}
		last = sig
		tray.SetMenu(buildTrayMenu(app, bridge))
	}
}
