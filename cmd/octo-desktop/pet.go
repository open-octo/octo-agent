package main

import (
	_ "embed"
	"strconv"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// petHTML is self-contained (inline CSS/SVG/JS) and is handed to the webview
// as a literal page rather than served: the pet must not depend on the hub
// being up, and it has nothing to do with the web UI's vite bundle.
//
//go:embed pet.html
var petHTML string

const (
	// petSize is the pet window's edge in points. The SVG fits its own viewBox
	// to whatever size the window ends up, so this is a framing choice, not a
	// constraint the artwork depends on.
	petSize = 200
	// petMargin is the gap left between the pet and the work area's corner.
	petMargin = 28
)

// togglePet shows the pet, or dismisses it if it is already up.
func (b *nativeBridge) togglePet() {
	if w := b.pet.Swap(nil); w != nil {
		w.Close()
		return
	}
	b.showPet()
}

// showPet creates the pet window at the bottom-right of the primary screen's
// work area.
//
// Deliberately outside the main window's hide/probe/revive machinery: nothing
// depends on the pet being alive, so a pet that dies just goes away rather than
// being resurrected. On macOS it is an NSPanel — floating, joining every Space,
// and non-activating, so showing or clicking it leaves whatever app the user is
// working in still active.
func (b *nativeBridge) showPet() {
	opts := application.WebviewWindowOptions{
		Title:            "Octo",
		Width:            petSize,
		Height:           petSize,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.RGBA{},
		HTML:             petHTML,
		Mac: application.MacWindow{
			// Backdrop is what actually makes the window transparent on macOS:
			// the cross-platform BackgroundType above is never read by the
			// darwin backend, so without this the pet sits on an opaque white
			// card (invisible only while it happens to overlap a white window).
			Backdrop: application.MacBackdropTransparent,
			// The SVG draws its own soft shadow; AppKit's would outline the
			// square window around a transparent page.
			DisableShadow:      true,
			WindowLevel:        application.MacWindowLevelFloating,
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces,
			WindowClass:        application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{
				FloatingPanel: true,
				NonActivating: true,
			},
		},
	}
	// Bottom-right of the work area (so it clears the dock/taskbar). X/Y alone
	// are ignored — InitialPosition defaults to WindowCentered, which is what
	// put the first pet in the middle of the screen — so the placement mode and
	// the target screen have to be set with them. With Screen set, X/Y are
	// relative to that screen's work area. Without a screen, the OS default
	// placement is kept rather than a guessed coordinate that could land the
	// pet off-screen.
	s := b.app.Screen.GetPrimary()
	if s != nil {
		opts.Screen = s
		opts.InitialPosition = application.WindowXY
		opts.X = s.WorkArea.Width - petSize - petMargin
		opts.Y = s.WorkArea.Height - petSize - petMargin
	}
	w := b.app.Window.NewWithOptions(opts)
	b.pet.Store(w)
	w.Show()
	// The options above are not enough on their own: the NSPanel path ignores
	// InitialPosition and opens centred anyway, so place it again once it is up.
	if s != nil {
		w.SetPosition(s.WorkArea.X+s.WorkArea.Width-petSize-petMargin,
			s.WorkArea.Y+s.WorkArea.Height-petSize-petMargin)
	}
}

// setPetState drives the pet's animation: "idle", "busy", "ask" or "sleep".
// This is the seam the session events (a turn running, a question waiting, a
// background task finishing) get wired to; it no-ops while the pet is down.
func (b *nativeBridge) setPetState(state string) {
	w := b.pet.Load()
	if w == nil {
		return
	}
	w.ExecJS("window.octoPet && window.octoPet.setState(" + strconv.Quote(state) + ")")
}
