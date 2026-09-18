package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/profiles"
)

func profilesTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(datahome.ProfileEnv, "")
	// Keep the default root's liveness probe off the developer's real 8088.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	prev := profiles.DefaultAddr
	profiles.DefaultAddr = ln.Addr().String()
	ln.Close()
	t.Cleanup(func() { profiles.DefaultAddr = prev })
	return home
}

func rp(args ...string) (string, int) {
	var b bytes.Buffer
	code := runProfiles(args, &b, &b)
	return b.String(), code
}

func TestProfilesCLI_ListShowsDefaultLabelAndCurrent(t *testing.T) {
	home := profilesTestHome(t)
	for _, d := range []string{".octo", ".octo-work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(datahome.ProfileEnv, "work")
	out, code := rp()
	if code != 0 {
		t.Fatalf("list exit %d: %s", code, out)
	}
	if !strings.Contains(out, "default") || !strings.Contains(out, filepath.Join(home, ".octo-work")) {
		t.Errorf("list output missing rows:\n%s", out)
	}
	if !strings.Contains(out, "current") {
		t.Errorf("current profile not marked:\n%s", out)
	}
}

func TestProfilesCLI_CreateThenPath(t *testing.T) {
	home := profilesTestHome(t)
	out, code := rp("create", "lab")
	if code != 0 {
		t.Fatalf("create exit %d: %s", code, out)
	}
	want := filepath.Join(home, ".octo-lab")
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Fatalf("root not created: %v", err)
	}
	out, code = rp("path", "lab")
	if code != 0 || strings.TrimSpace(out) != want {
		t.Errorf("path = %q (exit %d), want %q", out, code, want)
	}
	// "default" is the listing's label for the unnamed root.
	out, _ = rp("path", "default")
	if strings.TrimSpace(out) != filepath.Join(home, ".octo") {
		t.Errorf("path default = %q", out)
	}
	if out, code := rp("create", "lab"); code == 0 || !strings.Contains(out, "already exists") {
		t.Errorf("duplicate create: exit %d, %s", code, out)
	}
	if out, code := rp("create", "bad name"); code == 0 || !strings.Contains(out, "letters, digits") {
		t.Errorf("invalid create: exit %d, %s", code, out)
	}
	if out, code := rp("create", "default"); code == 0 || !strings.Contains(out, "reserved") {
		t.Errorf("reserved create: exit %d, %s", code, out)
	}
}

func TestProfilesCLI_RmNeedsYesAndHonoursGuards(t *testing.T) {
	home := profilesTestHome(t)
	for _, d := range []string{".octo", ".octo-old", ".octo-work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Without --yes nothing happens and the exit code says "not done".
	out, code := rp("rm", "old")
	if code == 0 || !strings.Contains(out, "--yes") {
		t.Fatalf("rm without --yes: exit %d, %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".octo-old")); err != nil {
		t.Fatalf("rm without --yes deleted the root")
	}
	if out, code := rp("rm", "default", "--yes"); code == 0 || !strings.Contains(out, "default profile cannot be removed") {
		t.Errorf("rm default: exit %d, %s", code, out)
	}
	t.Setenv(datahome.ProfileEnv, "work")
	if out, code := rp("rm", "work", "--yes"); code == 0 || !strings.Contains(out, "currently in use") {
		t.Errorf("rm current: exit %d, %s", code, out)
	}
	// Guards run before the confirmation: no "--yes" hint for a refused root.
	if out, code := rp("rm", "work"); code == 0 || strings.Contains(out, "Re-run with --yes") {
		t.Errorf("rm current without --yes should refuse outright: exit %d, %s", code, out)
	}
	out, code = rp("rm", "old", "--yes")
	if code != 0 {
		t.Fatalf("rm --yes exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".octo-old")); !os.IsNotExist(err) {
		t.Errorf("root still present: %v", err)
	}
	if out, code := rp("rm", "old", "--yes"); code == 0 || !strings.Contains(out, "not found") {
		t.Errorf("rm missing: exit %d, %s", code, out)
	}
}

func TestProfilesCLI_UnknownSubcommand(t *testing.T) {
	profilesTestHome(t)
	out, code := rp("frobnicate")
	if code != 2 || !strings.Contains(out, "unknown subcommand") {
		t.Errorf("exit %d, %s", code, out)
	}
}

func TestCompletion_ProfilesSubcommandsAndNames(t *testing.T) {
	home := profilesTestHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".octo-work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := completionCandidates([]string{"octo", "profiles", ""}); strings.Join(got, ",") != "list,create,rm,path" {
		t.Errorf("subcommands = %v", got)
	}
	if got := completionCandidates([]string{"octo", "profiles", "rm", ""}); strings.Join(got, ",") != "work" {
		t.Errorf("rm names = %v", got)
	}
	if got := completionCandidates([]string{"octo", "--profile", ""}); strings.Join(got, ",") != "work" {
		t.Errorf("--profile names = %v", got)
	}
	found := false
	for _, c := range topLevelCommands {
		if c == "profiles" {
			found = true
		}
	}
	if !found {
		t.Errorf("profiles missing from topLevelCommands")
	}
}
