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
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/serveproc"
	"github.com/open-octo/octo-agent/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

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
	// would disconnect other clients.
	tray := app.SystemTray.New()
	trayMenu := app.NewMenu()
	trayMenu.Add("Show Octo").OnClick(func(*application.Context) { bridge.showWindow() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit Octo").OnClick(func(*application.Context) { bridge.requestQuit() })
	tray.SetMenu(trayMenu)

	// Bind + serve + open the window once the event loop is up, so the takeover
	// prompt (a modal dialog) can run. Doing it here rather than before Run lets
	// us ask the user before stopping someone else's backend.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		startHub(app, bridge, settings)
	})

	err := app.Run()

	// The app has quit: release our pid-file entry (only if it's still ours —
	// a successor that took the port over must keep its own) and shut the
	// server down cleanly.
	serveproc.ReleaseOwned(os.Getpid())
	if bridge.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = bridge.srv.Shutdown(ctx)
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
	if pid, ok := serveproc.Running(); ok {
		if !bridge.confirmTakeover(pid) {
			app.Quit()
			return
		}
		if _, err := serveproc.Stop(); err != nil {
			bridge.showError("Octo", fmt.Sprintf("Couldn't stop the running backend: %v", err))
			app.Quit()
			return
		}
	}

	ln, err := net.Listen("tcp", hubAddr)
	if err != nil {
		bridge.showError("Octo",
			fmt.Sprintf("Couldn't bind %s — another program may be using it.\n\n%v", hubAddr, err))
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
		bridge.showError("Octo", fmt.Sprintf("Couldn't start the backend: %v", err))
		app.Quit()
		return
	}
	bridge.srv = srv
	go func() {
		if err := srv.ServeOn(ln); err != nil {
			log.Printf("octo-desktop: server stopped: %v", err)
		}
	}()

	bridge.showWindow()
}
