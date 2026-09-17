package datahome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListReportsTheRootsThatExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{".octo", ".octo-work", ".octo-lab", ".octo-", ".octo-bad name", ".octopus"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A file that looks like a root is not one.
	if err := os.WriteFile(filepath.Join(home, ".octo-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "lab", "work"}
	if len(got) != len(want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List() = %#v, want %#v", got, want)
		}
	}
}

func TestListOmitsTheDefaultRootUntilItExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".octo-work"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "work" {
		t.Errorf("List() = %#v, want just [work]", got)
	}
}

func TestSharedPathIgnoresTheProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(ProfileEnv, "work")
	got, err := SharedPath("desktop-profile")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".octo", "desktop-profile"); got != want {
		t.Errorf("SharedPath = %q, want %q", got, want)
	}
}
