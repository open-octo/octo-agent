package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
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

// The pet row names what the next click does, so a pet that goes away without
// the tray item being used must move the signature and get the menu rebuilt.
func TestTrayMenuSignatureFollowsThePet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir reads %USERPROFILE%
	bridge := &nativeBridge{}

	down := trayMenuSignature(bridge)

	bridge.pet.Store(&application.WebviewWindow{})
	if up := trayMenuSignature(bridge); up == down {
		t.Fatal("signature must change when the pet comes up")
	}

	bridge.pet.Store(nil)
	if got := trayMenuSignature(bridge); got != down {
		t.Fatal("signature must return to its prior value once the pet goes away")
	}
}
