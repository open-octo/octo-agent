package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// TestSelectDesktopProfile covers the desktop shell's only CLI option. The
// "unknown argument" case is the interesting one: it must NOT fail, because
// this runs before setupCrashLog and a GUI launch has no stderr to explain
// itself on.
func TestSelectDesktopProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		profile string
		wantErr bool
	}{
		{"profile only", []string{"--profile", "work"}, "work", false},
		{"equals profile", []string{"--profile=work"}, "work", false},
		{"missing profile", []string{"--profile="}, "", true},
		{"invalid profile", []string{"--profile", "../escape"}, "", true},
		{"unknown argument is ignored", []string{"-psn_0_12345"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(datahome.ProfileEnv, "")
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			err := selectDesktopProfile(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("selectDesktopProfile(%q) error = %v, want error %v", tc.args, err, tc.wantErr)
			}
			if got := os.Getenv(datahome.ProfileEnv); got != tc.profile {
				t.Errorf("profile = %q, want %q", got, tc.profile)
			}
		})
	}
}

// TestDesktopSettings_WindowGeometryRoundTrip guards the on-disk contract for
// the remembered window geometry. A typo'd json tag would silently reset the
// window to its default size on every launch — the exact kind of quiet
// regression this test exists to catch.
func TestDesktopSettings_WindowGeometryRoundTrip(t *testing.T) {
	in := desktopSettings{
		WindowWidth:     1600,
		WindowHeight:    1000,
		WindowMaximised: true,
		PetX:            42,
		PetY:            900,
		PetPositionSet:  true,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out desktopSettings
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.WindowWidth != in.WindowWidth || out.WindowHeight != in.WindowHeight {
		t.Errorf("size not preserved: got %dx%d, want %dx%d",
			out.WindowWidth, out.WindowHeight, in.WindowWidth, in.WindowHeight)
	}
	if out.WindowMaximised != in.WindowMaximised {
		t.Errorf("maximised not preserved: got %v, want %v", out.WindowMaximised, in.WindowMaximised)
	}
	if !out.PetPositionSet || out.PetX != in.PetX || out.PetY != in.PetY {
		t.Errorf("pet position not preserved: got (%d,%d,set=%v), want (%d,%d,set=true)",
			out.PetX, out.PetY, out.PetPositionSet, in.PetX, in.PetY)
	}
}

// TestDefaultDesktopSettings_NoGeometry documents that a fresh install carries
// zero geometry, which showWindowAt reads as "use the built-in default size",
// and no pet position, which showPet reads as "use the bottom-right default".
func TestDefaultDesktopSettings_NoGeometry(t *testing.T) {
	s := defaultDesktopSettings()
	if s.WindowWidth != 0 || s.WindowHeight != 0 || s.WindowMaximised {
		t.Errorf("defaults should carry no saved geometry, got %+v", s)
	}
	if s.PetPositionSet || s.PetX != 0 || s.PetY != 0 {
		t.Errorf("defaults should carry no pet position, got %+v", s)
	}
}

func TestDesktopSettingsPathUsesProfileDataHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct {
		name    string
		profile string
		root    string
	}{
		{"default profile", "", ".octo"},
		{"work profile", "work", ".octo-work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(datahome.ProfileEnv, tc.profile)
			got, err := desktopSettingsPath()
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(home, tc.root, "desktop.json")
			if got != want {
				t.Errorf("desktop settings path = %q, want %q", got, want)
			}
		})
	}
}
