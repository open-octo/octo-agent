package main

import (
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The corners are the whole reason shape-aware pass-through exists: a 200pt
// square dropped on the desktop overlaps things the user meant to click, and
// the octopus is nowhere near them.
func TestPetHitLetsTheCornersThrough(t *testing.T) {
	corners := []struct{ x, y float64 }{
		{4, 4}, {196, 4}, {4, 196}, {196, 196},
		{12, 20}, {188, 20},
	}
	for _, state := range []string{"idle", petStateBusy, petStateSleep, "ask"} {
		for _, c := range corners {
			if petHit(state, c.x, c.y) {
				t.Errorf("petHit(%q, %v, %v) = true, want false (corner must pass through)", state, c.x, c.y)
			}
		}
	}
}

func TestPetHitCoversTheBody(t *testing.T) {
	cases := []struct {
		state  string
		x, y   float64
		reason string
	}{
		{"idle", 100, 80, "head centre"},
		{"idle", 100, 130, "arms below the head"},
		{"ask", 100, 80, "head centre"},
		{petStateBusy, 100, 60, "head centre, lifted to the keyboard"},
		{petStateBusy, 100, 135, "the keyboard itself"},
		{petStateBusy, 50, 135, "the keyboard is wider than the octopus"},
		{petStateSleep, 100, 90, "head centre, sunk down"},
	}
	for _, c := range cases {
		if !petHit(c.state, c.x, c.y) {
			t.Errorf("petHit(%q, %v, %v) = false, want true (%s)", c.state, c.x, c.y, c.reason)
		}
	}
}

// The head moves per state, so a point can be on the body in one pose and off
// it in another — if this stopped being true the state would not be worth
// passing in at all.
func TestPetHitFollowsThePose(t *testing.T) {
	// Well above the idle head, but inside it once busy lifts the body.
	const x, y = 100.0, 14.0
	if petHit("idle", x, y) {
		t.Errorf("petHit(idle, %v, %v) = true, want false", x, y)
	}
	if !petHit(petStateBusy, x, y) {
		t.Errorf("petHit(busy, %v, %v) = false, want true", x, y)
	}
}

// Only the busy pose has a keyboard; in any other state that band is empty
// space and has to pass clicks through.
func TestPetHitKeyboardOnlyWhenBusy(t *testing.T) {
	const x, y = 50.0, 140.0
	if !petHit(petStateBusy, x, y) {
		t.Errorf("petHit(busy, %v, %v) = false, want true", x, y)
	}
	if petHit("idle", x, y) {
		t.Errorf("petHit(idle, %v, %v) = true, want false", x, y)
	}
}

// A saved position is restored only when it still lands on a connected
// screen — otherwise the pet would be stranded off-screen after a monitor
// is unplugged, invisible and ungrabbable.
func TestPetPositionOnScreen(t *testing.T) {
	primary := &application.Screen{WorkArea: application.Rect{X: 0, Y: 0, Width: 1920, Height: 1080}}
	// A monitor to the left of the primary has negative X coordinates.
	left := &application.Screen{WorkArea: application.Rect{X: -1280, Y: 0, Width: 1280, Height: 1024}}
	screens := []*application.Screen{primary, left}

	cases := []struct {
		name string
		x, y int
		want bool
	}{
		{"inside primary", 100, 100, true},
		{"primary's top-left corner", 0, 0, true},
		{"on the second monitor", -640, 512, true},
		{"beyond the right edge", 2000, 100, false},
		{"below the bottom edge", 100, 1200, false},
		{"between the monitors' gap", -2000, 100, false},
		{"negative beyond the left monitor", -1300, 100, false},
	}
	for _, c := range cases {
		if got := petPositionOnScreen(c.x, c.y, screens); got != c.want {
			t.Errorf("petPositionOnScreen(%d, %d) = %v, want %v (%s)", c.x, c.y, got, c.want, c.name)
		}
	}

	// No screens at all — nothing can be on-screen.
	if petPositionOnScreen(0, 0, nil) {
		t.Errorf("petPositionOnScreen(0, 0, nil) = true, want false")
	}
	// A nil entry in the list (a screen that vanished mid-query) is skipped,
	// not crashed on.
	if !petPositionOnScreen(10, 10, []*application.Screen{nil, primary}) {
		t.Errorf("petPositionOnScreen with a nil screen entry = false, want true")
	}
}

// rememberPetPosition is the write path the position loop feeds: it must set
// the settings fields synchronously — the exit path snapshots settings without
// waiting for the debounce — and land them in desktop.json once the debounce
// settles. HOME and USERPROFILE both point at a temp dir because the save goes
// through os.UserHomeDir, which ignores HOME on Windows.
func TestRememberPetPositionPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	b := &nativeBridge{}
	b.rememberPetPosition(120, 340)
	t.Cleanup(func() {
		b.settingsMu.Lock()
		if b.petGeomTimer != nil {
			b.petGeomTimer.Stop()
		}
		b.settingsMu.Unlock()
	})

	// The fields are set synchronously, ahead of the debounced disk write.
	if !b.settings.PetPositionSet || b.settings.PetX != 120 || b.settings.PetY != 340 {
		t.Fatalf("settings after rememberPetPosition = %+v, want PetX=120 PetY=340 PetPositionSet=true",
			b.settings)
	}

	// Once the debounce fires, the position is on disk.
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := loadDesktopSettings()
		if s.PetPositionSet && s.PetX == 120 && s.PetY == 340 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pet position not persisted to desktop.json, loaded %+v", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
