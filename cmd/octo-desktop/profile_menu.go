package main

import (
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// addProfileMenu hangs a profile picker off the tray menu, listing the data
// roots that exist on disk. It is also the only place a profile can be
// discovered from the GUI — the CLI has no listing command either, and a shell
// that only takes a name is no help to someone who has forgotten the names.
//
// Omitted entirely when the default profile is the only one: a submenu with a
// single item is a menu that teaches nothing, and a user who has never made a
// second profile cannot make one from here anyway.
func addProfileMenu(m *application.Menu, bridge *nativeBridge) {
	profiles, err := datahome.List()
	if err != nil || len(profiles) < 2 {
		return
	}
	current := os.Getenv(datahome.ProfileEnv)
	sub := m.AddSubmenu(L().trayProfileMenu)
	for _, p := range profiles {
		profile := p // the click runs long after this loop
		item := sub.AddRadio(desktopProfileLabel(profile), profile == current)
		item.OnClick(func(*application.Context) { bridge.switchProfile(profile) })
	}
}
