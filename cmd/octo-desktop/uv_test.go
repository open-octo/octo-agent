package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/open-octo/octo-agent/internal/datahome"
)

func TestBundledUvTargetUsesSharedBinDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	name := "uv"
	if runtime.GOOS == "windows" {
		name = "uv.exe"
	}
	for _, tc := range []struct {
		name    string
		profile string
		root    string
	}{
		{"default profile", "", ".octo"},
		{"work profile", "work", ".octo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(datahome.ProfileEnv, tc.profile)
			got, err := bundledUvTarget()
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(home, tc.root, "bin", name)
			if got != want {
				t.Errorf("bundled uv target = %q, want %q", got, want)
			}
		})
	}
}
