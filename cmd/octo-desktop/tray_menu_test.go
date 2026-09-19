package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The tray's profile submenu is rebuilt only when trayMenuSignature changes,
// so creating/deleting a profile from the Web UI's data-management panel or
// `octo profiles` must show up without an app restart — the on-disk profile
// list is part of the signature.
func TestTrayMenuSignatureFollowsProfileList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir reads %USERPROFILE%
	bridge := &nativeBridge{}

	before := trayMenuSignature(bridge)

	if err := os.Mkdir(filepath.Join(home, ".octo-work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if after := trayMenuSignature(bridge); after == before {
		t.Fatal("signature must change when a profile appears on disk")
	}

	if err := os.RemoveAll(filepath.Join(home, ".octo-work")); err != nil {
		t.Fatal(err)
	}
	if got := trayMenuSignature(bridge); got != before {
		t.Fatal("signature must return to its prior value once the profile is deleted")
	}
}
