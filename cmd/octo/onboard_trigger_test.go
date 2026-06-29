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
