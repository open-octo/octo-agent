package main

import "testing"

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
