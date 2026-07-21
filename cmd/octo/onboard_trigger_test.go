package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSoulMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir() reads %USERPROFILE% on Windows, not $HOME.
	t.Setenv("USERPROFILE", home)

	if !soulMissing() {
		t.Fatal("expected soulMissing=true when no soul.md exists")
	}
	octo := filepath.Join(home, ".octo")
	if err := os.MkdirAll(octo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(octo, "soul.md"), []byte("# Soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	if soulMissing() {
		t.Fatal("expected soulMissing=false once soul.md exists")
	}
}

func TestOnboardAttempted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir() reads %USERPROFILE% on Windows, not $HOME.
	t.Setenv("USERPROFILE", home)

	if onboardAttempted() {
		t.Fatal("expected onboardAttempted=false before markOnboardAttempted")
	}
	markOnboardAttempted()
	if !onboardAttempted() {
		t.Fatal("expected onboardAttempted=true after markOnboardAttempted, even with soul.md still missing (#1660)")
	}
	if !soulMissing() {
		t.Fatal("markOnboardAttempted must not itself create soul.md")
	}
}

// TestShouldAutoOnboard_MarksWhenLaunching locks the #1660 invariant: deciding
// to auto-launch /onboard must, in the same step, record the one-shot marker —
// otherwise an interrupted first run re-nudges on the next startup (the web's
// FirstRunSetup drift). It also covers idempotency and the identity gate.
func TestShouldAutoOnboard_MarksWhenLaunching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Fresh install: no sessions, no soul.md/user.md, marker absent → onboard.
	if !shouldAutoOnboard() {
		t.Fatal("shouldAutoOnboard = false on a fresh install, want true")
	}
	// The decision MUST have written the marker (the invariant).
	if !onboardAttempted() {
		t.Fatal("shouldAutoOnboard launched onboard but did not record the attempt marker (#1660 invariant)")
	}
	// Idempotent: the marker now suppresses a repeat launch.
	if shouldAutoOnboard() {
		t.Fatal("shouldAutoOnboard = true after the marker was set, want false (no repeat nudge)")
	}
}

// TestShouldAutoOnboard_SkipsWhenIdentityExists covers the gate: an existing
// identity file means the user is set up, so no nudge and no marker written.
func TestShouldAutoOnboard_SkipsWhenIdentityExists(t *testing.T) {
	for _, name := range []string{"soul.md", "user.md"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			octo := filepath.Join(home, ".octo")
			if err := os.MkdirAll(octo, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(octo, name), []byte("# identity"), 0o644); err != nil {
				t.Fatal(err)
			}
			if shouldAutoOnboard() {
				t.Fatalf("shouldAutoOnboard = true with %s present, want false", name)
			}
			if onboardAttempted() {
				t.Fatalf("marker written despite %s present — should not touch it when not launching", name)
			}
		})
	}
}

func TestOfferOnboarding(t *testing.T) {
	cases := map[string]bool{
		"\n":     true,  // default (Enter) → onboard
		"y\n":    true,  // explicit yes
		"yes\n":  true,  // anything non-skip → onboard
		"n\n":    false, // decline
		"skip\n": false, // skip
		"s\n":    false, // skip short
	}
	for input, want := range cases {
		r := newScannerLineReader(strings.NewReader(input), io.Discard)
		if got := offerOnboarding(r, io.Discard); got != want {
			t.Errorf("offerOnboarding(%q) = %v, want %v", input, got, want)
		}
	}
	// EOF (no TTY / closed stdin) declines rather than blocking.
	r := newScannerLineReader(strings.NewReader(""), io.Discard)
	if offerOnboarding(r, io.Discard) {
		t.Error("offerOnboarding on EOF should decline")
	}
}
