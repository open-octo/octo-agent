package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/datahome"
)

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(datahome.ProfileEnv, "")
	return home
}

// The remembered choice has to live outside every profile root — a file that
// says which profile to open cannot be inside the profile it selects.
func TestDesktopProfileFileIsMachineScoped(t *testing.T) {
	home := tempHome(t)
	t.Setenv(datahome.ProfileEnv, "work")
	if err := writeDesktopProfile("work"); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".octo", desktopProfileFile)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("choice should be recorded at %s: %v", want, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".octo-work", desktopProfileFile)); err == nil {
		t.Error("choice must not be written inside the profile it selects")
	}
}

func TestSelectDesktopProfile_UsesTheRememberedChoice(t *testing.T) {
	tempHome(t)
	if err := writeDesktopProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := selectDesktopProfile(nil); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(datahome.ProfileEnv); got != "work" {
		t.Errorf("profile = %q, want work", got)
	}
}

// Someone starting the app from a terminal means the flag, and a one-off launch
// shouldn't quietly redefine what double-clicking the icon opens.
func TestSelectDesktopProfile_FlagWinsAndIsNotPersisted(t *testing.T) {
	tempHome(t)
	if err := writeDesktopProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := selectDesktopProfile([]string{"--profile", "lab"}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(datahome.ProfileEnv); got != "lab" {
		t.Errorf("profile = %q, want lab", got)
	}
	if got := readDesktopProfile(); got != "work" {
		t.Errorf("recorded choice = %q, want it left at work", got)
	}
}

// A profile the user deleted must not stop the app from coming up — the tray is
// where they'd pick another one.
func TestSelectDesktopProfile_UnreadableChoiceFallsBackToDefault(t *testing.T) {
	home := tempHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".octo"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".octo", desktopProfileFile)
	if err := os.WriteFile(path, []byte("../escape\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := selectDesktopProfile(nil); err != nil {
		t.Fatalf("a bad recorded choice should not stop startup: %v", err)
	}
	if got := os.Getenv(datahome.ProfileEnv); got != "" {
		t.Errorf("profile = %q, want the default", got)
	}
}

func TestSelectDesktopProfile_InvalidFlagStillFails(t *testing.T) {
	tempHome(t)
	if err := selectDesktopProfile([]string{"--profile", "../escape"}); err == nil {
		t.Error("an invalid --profile should stop startup; it can only come from a terminal")
	}
}

func TestDesktopProfileLabel(t *testing.T) {
	if got := desktopProfileLabel(""); got == "" {
		t.Error("the default profile needs a name to show in the menu")
	}
	if got := desktopProfileLabel("work"); got != "work" {
		t.Errorf("label = %q, want work", got)
	}
}

func TestAwaitPredecessor_ClearsTheVariable(t *testing.T) {
	t.Setenv(awaitPidEnv, "")
	awaitPredecessor() // no pid: returns at once
	if got := os.Getenv(awaitPidEnv); got != "" {
		t.Errorf("%s = %q, want it cleared so a later relaunch can't inherit it", awaitPidEnv, got)
	}
}

// The replacement is told which process to outlive. Getting that wrong means it
// either starts too early (and is dismissed as a second instance) or waits for
// a pid that never existed.
func TestRelaunchCommandCarriesOurPid(t *testing.T) {
	cmd, err := relaunchCommand()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != exe {
		t.Errorf("relaunching %q, want this same binary %q", cmd.Path, exe)
	}
	want := awaitPidEnv + "=" + strconv.Itoa(os.Getpid())
	found := false
	for _, kv := range cmd.Env {
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Errorf("environment should carry %q", want)
	}
}

// A process running under a named profile has OCTO_PROFILE set (Configure put
// it there). If the replacement inherited it, selectDesktopProfile would take
// it as an explicit choice and never read the recorded switch — restarting
// into the profile it was asked to leave. Every switch away from a named
// profile would silently no-op.
func TestRelaunchCommandStripsTheInheritedProfile(t *testing.T) {
	t.Setenv(datahome.ProfileEnv, "work")
	cmd, err := relaunchCommand()
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, datahome.ProfileEnv+"=") {
			t.Errorf("relaunch env must not carry %s, got %q", datahome.ProfileEnv, kv)
		}
	}
}
