package main

import (
	_ "embed"
	"strconv"
	"time"

	"github.com/open-octo/octo-agent/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// petStateSleep is the one pet state with no matching server activity — the pet
// dozes off on its own after the hub has been idle for petSleepAfter. The other
// three are server.Activity* values, passed through as-is; petStateBusy is
// spelled out here because the hit test needs to name it too.
const (
	petStateSleep = "sleep"
	petStateBusy  = server.ActivityBusy
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
	// petPollInterval is how often the pet re-reads the hub's activity. The
	// pet is decorative, so a poll on a timer buys the same result as pushing
	// activity changes through the turn's hot path, for none of the coupling.
	petPollInterval = 500 * time.Millisecond
	// petSleepAfter is how long the hub stays idle before the pet dozes off.
	petSleepAfter = 5 * time.Minute
	// petPointerInterval is how often the cursor is checked against the pet's
	// silhouette. Fast enough that crossing onto the octopus feels immediate,
	// slow enough to stay invisible in a process that is mostly idle.
	petPointerInterval = 40 * time.Millisecond
)

// petHit reports whether a point, in window-local points, lands on the pet's
// artwork rather than the transparent space around it.
//
// Deliberately a couple of coarse shapes rather than the real silhouette: the
// window is only 200pt, the cost of being a few points generous is that a
// click near the edge still reaches the octopus, and the cost of being exact
// would be re-deriving every arm's swept path on each tick. What matters is
// that the corners — which is where a 200pt square overlaps things the user
// actually wanted to click — fall outside.
func petHit(state string, lx, ly float64) bool {
	// The body sits at a different height per state; .busy lifts the rig by 20
	// and .sleep drops it by 10 (see pet.html).
	headCY := 80.0
	switch state {
	case petStateBusy:
		headCY = 60
	case petStateSleep:
		headCY = 90
	}
	if dx, dy := lx-100, ly-headCY; dx*dx+dy*dy <= 54*54 {
		return true
	}
	// The skirt of arms hanging below the head.
	if lx >= 52 && lx <= 148 && ly >= headCY && ly <= headCY+88 {
		return true
	}
	// The keyboard, which is wider than the octopus and only exists while busy.
	if state == petStateBusy && lx >= 44 && lx <= 162 && ly >= 116 && ly <= 158 {
		return true
	}
	return false
}

// watchPetPointer gives the window shape-aware mouse pass-through, until this
// pet goes away.
//
// The window is a 200pt square and the octopus fills maybe half of it, so its
// empty corners were swallowing clicks meant for whatever sits behind them.
// IgnoreMouseEvents is a whole-window switch — turning it on makes the octopus
// itself unclickable — so the only way to get pass-through by shape is to flip
// it as the cursor crosses the artwork's edge. That in turn needs the cursor
// position from OUTSIDE the window: once a window ignores the mouse it never
// sees the cursor come back, so it cannot un-ignore itself from a DOM event.
func (b *nativeBridge) watchPetPointer(w *application.WebviewWindow) {
	if _, _, ok := petCursor(); !ok {
		return // no cursor source here; leave the window solid
	}
	t := time.NewTicker(petPointerInterval)
	defer t.Stop()

	// Mirrors the window's initial state (IgnoreMouseEvents is unset in the
	// options), so the first flip is only sent when it actually differs.
	ignoring := false
	// The webview builds its view tree as the page loads, so the first patch
	// has to wait for it; it is re-applied whenever the window becomes solid
	// again, since a view created later would still eat the first click.
	patchFirstMouse := func() {
		application.InvokeSync(func() { petAcceptFirstMouse(w.NativeWindow()) })
	}
	patched := false

	for range t.C {
		if b.pet.Load() != w {
			return
		}
		cx, cy, ok := petCursor()
		if !ok {
			continue
		}
		r := w.Bounds()
		if r.Width <= 0 || r.Height <= 0 {
			continue
		}
		// The SVG maps its 200-unit viewBox onto the window, so scaling the
		// local point by the same factor keeps the hit shapes in viewBox units
		// whatever size the window ends up.
		lx := float64(cx-r.X) * (petSize / float64(r.Width))
		ly := float64(cy-r.Y) * (petSize / float64(r.Height))

		var state string
		if s := b.petState.Load(); s != nil {
			state = *s
		}
		want := !petHit(state, lx, ly)
		if want != ignoring {
			ignoring = want
			w.SetIgnoreMouseEvents(want)
		}
		if !ignoring && !patched {
			patched = true
			patchFirstMouse()
		} else if ignoring {
			// Re-patch next time it goes solid: the cursor leaving and coming
			// back is exactly when a first click would be lost.
			patched = false
		}
	}
}

// petShown reports whether the pet is currently up — the tray item is a toggle
// and names what the next click will do.
func (b *nativeBridge) petShown() bool { return b.pet.Load() != nil }

// togglePet shows the pet, or dismisses it if it is already up. Either way the
// tray menu is rebuilt, so its label follows the pet instead of going stale
// until refreshTrayLoop's next tick.
func (b *nativeBridge) togglePet() {
	if w := b.pet.Swap(nil); w != nil {
		// Before the close, not after: the panel is released the moment it
		// closes, so the first-mouse hook must stop recognising that address
		// while it still belongs to the pet.
		petForgetFirstMouseWindow()
		w.Close()
		b.refreshTray()
		return
	}
	b.showPet()
	b.refreshTray()
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
				// The pet never wants the keyboard, so it should not take key
				// status away from whatever the user is typing into. (This is
				// NOT what makes a first click land — that needs
				// acceptsFirstMouse, see petAcceptFirstMouse.)
				BecomesKeyOnlyIfNeeded: true,
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
	// InitialPosition and opens centred anyway, so place it again once it is
	// up. The user's last placement wins when it is still on a connected
	// screen — the monitor it belonged to may be gone, in which case fall
	// back to the bottom-right default. Settings are read before any window
	// call: those marshal to the UI thread, which must never happen under
	// settingsMu.
	b.settingsMu.Lock()
	px, py, placed := b.settings.PetX, b.settings.PetY, b.settings.PetPositionSet
	b.settingsMu.Unlock()
	if placed && petPositionOnScreen(px, py, b.app.Screen.GetAll()) {
		w.SetPosition(px, py)
	} else if s != nil {
		w.SetPosition(s.WorkArea.X+s.WorkArea.Width-petSize-petMargin,
			s.WorkArea.Y+s.WorkArea.Height-petSize-petMargin)
	}
	go b.watchPetActivity(w)
	go b.watchPetPointer(w)
	go b.watchPetPosition(w)
}

// petPositionOnScreen reports whether the point (x, y) lies inside any
// connected screen's work area. A saved pet position that fails this — the
// monitor it was on has been unplugged, or the settings file was hand-edited —
// is ignored in favour of the default placement rather than leaving the pet
// stranded off-screen.
func petPositionOnScreen(x, y int, screens []*application.Screen) bool {
	for _, s := range screens {
		if s == nil {
			continue
		}
		wa := s.WorkArea
		if x >= wa.X && x < wa.X+wa.Width && y >= wa.Y && y < wa.Y+wa.Height {
			return true
		}
	}
	return false
}

// watchPetPosition persists the pet's placement until this pet window goes
// away, so a relaunch restores it where the user left it. It runs even on
// platforms without a cursor source (where the pass-through loop opts out):
// the drag itself is handled natively, so the bounds move all the same and
// are picked up here. Identity ends it, same contract as watchPetActivity.
func (b *nativeBridge) watchPetPosition(w *application.WebviewWindow) {
	t := time.NewTicker(petPollInterval)
	defer t.Stop()

	// The first bounds read is the placement showPet just chose, not a user
	// gesture — record it as the baseline instead of persisting it.
	havePos := false
	var lastX, lastY int

	for range t.C {
		if b.pet.Load() != w {
			return
		}
		r := w.Bounds()
		if r.Width <= 0 || r.Height <= 0 {
			continue
		}
		if !havePos {
			lastX, lastY, havePos = r.X, r.Y, true
			continue
		}
		if r.X != lastX || r.Y != lastY {
			lastX, lastY = r.X, r.Y
			b.rememberPetPosition(r.X, r.Y)
		}
	}
}

// rememberPetPosition captures the pet's position into settings and debounces
// the disk write — rememberWindowGeometry's pattern, on its own timer. The
// caller reads the bounds; no window method runs under settingsMu.
func (b *nativeBridge) rememberPetPosition(x, y int) {
	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	b.settings.PetX, b.settings.PetY = x, y
	b.settings.PetPositionSet = true
	if b.petGeomTimer != nil {
		b.petGeomTimer.Stop()
	}
	b.petGeomTimer = time.AfterFunc(400*time.Millisecond, b.persistSettings)
}

// watchPetActivity drives the pet from the hub's aggregate activity until this
// pet window goes away. Identity, not a flag, ends it: comparing against the
// window this loop was started for means a toggle-off — or a fast off/on that
// starts a second loop — retires the older one instead of leaving two loops
// writing to the same pet.
func (b *nativeBridge) watchPetActivity(w *application.WebviewWindow) {
	t := time.NewTicker(petPollInterval)
	defer t.Stop()

	// The page renders idle on load, so that is the state to diff against —
	// otherwise the first tick would push a redundant setState.
	last := server.ActivityIdle
	var idleSince time.Time

	for range t.C {
		if b.pet.Load() != w {
			return
		}
		srv := b.srv.Load()
		if srv == nil {
			continue
		}

		state := srv.Activity()
		// Doze off after a long enough quiet spell. Tracked here rather than in
		// the server because it is a property of the pet, not of the hub: the
		// hub is merely idle, the pet is the thing that gets bored.
		if state == server.ActivityIdle {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			if time.Since(idleSince) >= petSleepAfter {
				state = petStateSleep
			}
		} else {
			idleSince = time.Time{}
		}

		if state != last {
			last = state
			b.setPetState(state)
		}
	}
}

// setPetState drives the pet's animation: "idle", "busy", "ask" or "sleep".
// No-ops while the pet is down. The state is also published for the pointer
// loop, whose hit shapes depend on the pose.
func (b *nativeBridge) setPetState(state string) {
	b.petState.Store(&state)
	w := b.pet.Load()
	if w == nil {
		return
	}
	w.ExecJS("window.octoPet && window.octoPet.setState(" + strconv.Quote(state) + ")")
}
