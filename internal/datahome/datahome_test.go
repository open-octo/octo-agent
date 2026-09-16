package datahome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirUsesDefaultAndNamedProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(ProfileEnv, "")

	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".octo"); got != want {
		t.Errorf("default data dir = %q, want %q", got, want)
	}

	if err := Configure("work"); err != nil {
		t.Fatal(err)
	}
	got, err = Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".octo-work"); got != want {
		t.Errorf("profile data dir = %q, want %q", got, want)
	}
}

func TestConfigureFromArgsStripsGlobalProfile(t *testing.T) {
	t.Setenv(ProfileEnv, "")
	args, err := ConfigureFromArgs([]string{"serve", "--profile", "work", "--addr", ":8088"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(args), 3; got != want {
		t.Fatalf("argument count = %d, want %d: %#v", got, want, args)
	}
	if got, want := args[0], "serve"; got != want {
		t.Errorf("first argument = %q, want %q", got, want)
	}
	if got := os.Getenv(ProfileEnv); got != "work" {
		t.Errorf("profile environment = %q, want %q", got, "work")
	}
}

func TestConfigureFromArgsRejectsMissingOrRepeatedProfile(t *testing.T) {
	t.Setenv(ProfileEnv, "")
	for _, args := range [][]string{
		{"--profile"},
		{"--profile", "one", "--profile=two"},
	} {
		if _, err := ConfigureFromArgs(args); err == nil {
			t.Errorf("ConfigureFromArgs(%q) succeeded", args)
		}
	}
}

func TestConfigureFromArgsPreservesInheritedProfile(t *testing.T) {
	t.Setenv(ProfileEnv, "old")
	if _, err := ConfigureFromArgs([]string{"serve"}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(ProfileEnv); got != "old" {
		t.Errorf("profile environment = %q, want old", got)
	}
}

func TestConfigureRejectsUnsafeProfile(t *testing.T) {
	if err := Configure("../other"); err == nil {
		t.Fatal("Configure accepted a traversal profile")
	}
}
