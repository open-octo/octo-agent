package main

import (
	"fmt"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// addProfileRow puts the profile into the tray's status block, right under the
// backend address: which data root this backend runs under is the same kind of
// fact as which port it answers on. The row is itself the picker — its title
// carries the current name, and opening it lists the data roots that exist on
// disk, so the menu never says "Profile" twice. It is the only place a profile
// can be switched from the GUI; listing, creating and removing them is
// `octo profiles` and the Web UI's 设置 → 数据管理 panel.
//
// With the default profile alone there is no row at all: a submenu with a
// single item is a menu that teaches nothing, and a user who has never made a
// second profile cannot make one from here anyway. A named profile with
// nothing to switch to still gets its (disabled) status line.
func addProfileRow(m *application.Menu, bridge *nativeBridge) {
	current := os.Getenv(datahome.ProfileEnv)
	title := fmt.Sprintf(L().trayProfileFmt, desktopProfileLabel(current))
	profiles, err := datahome.List()
	if err != nil || len(profiles) < 2 {
		if current != "" {
			m.Add(title).SetEnabled(false)
		}
		return
	}
	sub := m.AddSubmenu(title)
	for _, p := range profiles {
		profile := p // the click runs long after this loop
		item := sub.AddRadio(desktopProfileLabel(profile), profile == current)
		item.OnClick(func(*application.Context) { bridge.switchProfile(profile) })
	}
}
