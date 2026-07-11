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

	"github.com/open-octo/octo-agent/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	// Bind an ephemeral loopback port before anything else so the window URL is
	// known up front, then hand the very same listener to the server — no
	// second bind, no port race.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("octo-desktop: listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())

	bridge := &nativeBridge{}

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
		// Native notifications (v3/pkg/services/notifications) are intentionally
		// not registered yet: the service requires a real .app bundle identifier
		// and hard-fails app startup without one, so a bare `make desktop` binary
		// couldn't run. Wiring it (inside a wails3-built bundle) and routing the
		// server's notification triggers through NativeBridge.Notify is a
		// follow-up — see dev-docs/wails-desktop-design.md.
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
