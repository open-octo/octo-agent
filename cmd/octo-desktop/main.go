// Command octo-desktop is octo's native desktop shell. It runs the same web
// server octo serve uses — in-process on a loopback port — and points a Wails
// window at it, so the Svelte frontend and every /api and /ws handler are
// reused unchanged. The only thing it adds is the native layer a browser
// can't reach: an OS folder dialog, a system tray, launch-at-login, and native
// notifications, wired into the server through server.NativeBridge.
//
// See dev-docs/wails-desktop-design.md.
package main

import (
	"fmt"
	"log"
	"net"

	"os"
	"strings"

	"github.com/open-octo/octo-agent/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

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
	// Bind an ephemeral loopback port before anything else so the window URL is
	// known up front, then hand the very same listener to the server — no
	// second bind, no port race.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("octo-desktop: listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())

	// Seed ~/.octo/bin/uv from the app's bundled copy on first run so skills
	// that need Python work even for a standalone download (no installer).
	ensureBundledUv()

	bridge := &nativeBridge{}
	// Native notifications only when bundled (see isBundled): the service needs
	// a bundle identifier. Registered as a Wails service so its ServiceStartup
	// runs; the bridge holds it to send notifications the frontend requests.
	var services []application.Service
	if isBundled() {
		notifier := notifications.New()
		bridge.notifier = notifier
		services = append(services, application.NewService(notifier))
	}

	srv, err := server.New(server.Config{
		Tools: true,
		// Desktop is a single local user: no IM channels, no outbound update
		// check. The native bridge is what makes this a desktop build.
		NoChannel:   true,
		UpdateCheck: false,
		Native:      bridge,
	})
	if err != nil {
		log.Fatalf("octo-desktop: build server: %v", err)
	}
	go func() {
		if err := srv.ServeOn(ln); err != nil {
			log.Printf("octo-desktop: server stopped: %v", err)
		}
	}()

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
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	// The bridge needs the app to raise dialogs and re-focus the window.
	bridge.app = app

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Octo",
		Width:  1280,
		Height: 860,
		URL:    url,
		// Hidden-inset title bar: no separate native title strip (that doubled
		// up with the web UI's own header). The traffic lights float over the
		// top-left of the page; the frontend insets its header past them in
		// desktop mode (nativeShell) so nothing is covered.
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})
	bridge.window = window

	// System tray: quick access to show the window or quit without hunting for
	// the dock icon.
	tray := app.SystemTray.New()
	trayMenu := app.NewMenu()
	trayMenu.Add("Show Octo").OnClick(func(*application.Context) { bridge.showWindow() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit Octo").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(trayMenu)

	if err := app.Run(); err != nil {
		log.Fatalf("octo-desktop: %v", err)
	}
}
