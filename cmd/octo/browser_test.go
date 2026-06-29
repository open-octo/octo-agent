package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/config"
)

// withFakeProbe swaps attachProbe for the duration of a test.
func withFakeProbe(t *testing.T, fn func(port int) (bool, string)) {
	t.Helper()
	orig := attachProbe
	attachProbe = fn
	t.Cleanup(func() { attachProbe = orig })
}

// isolateHome points config.Load/Save at a temp HOME (and USERPROFILE on
// Windows, where os.UserHomeDir reads that instead).
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestBrowserSetup_SuccessSavesPort(t *testing.T) {
	home := isolateHome(t)
	withFakeProbe(t, func(port int) (bool, string) { return true, "2 open tab(s)" })

	var out strings.Builder
	code := runBrowser([]string{"setup"}, strings.NewReader(""), &out, &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "✓ Connected") {
		t.Errorf("missing success line; got:\n%s", out.String())
	}

	// The connect port must be persisted so the tool reuses this Chrome.
	cfgPath := filepath.Join(home, ".octo", "config.yml")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Browser.ConnectPort != defaultDebugPort {
		t.Errorf("ConnectPort = %d, want %d (config at %s)", cfg.Browser.ConnectPort, defaultDebugPort, cfgPath)
	}
}

func TestBrowserSetup_RetryThenQuit(t *testing.T) {
	isolateHome(t)
	calls := 0
	withFakeProbe(t, func(port int) (bool, string) {
		calls++
		return false, "no Chrome with remote debugging"
	})

	var out strings.Builder
	// First probe fails → prompt → user presses Enter (retry) → fails again →
	// "q" quits.
	code := runBrowser([]string{"setup"}, strings.NewReader("\nq\n"), &out, &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; output:\n%s", code, out.String())
	}
	if calls != 2 {
		t.Errorf("probe calls = %d, want 2 (initial + one retry)", calls)
	}
	if !strings.Contains(out.String(), "Setup paused") {
		t.Errorf("missing paused line; got:\n%s", out.String())
	}
}

func TestBrowserSetup_EOFDoesNotLoopForever(t *testing.T) {
	isolateHome(t)
	withFakeProbe(t, func(port int) (bool, string) { return false, "nope" })

	var out strings.Builder
	// Empty stdin: the first probe fails, the prompt read hits EOF → quit.
	code := runBrowser([]string{"setup"}, strings.NewReader(""), &out, &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestBrowserSetup_HonorsConfiguredPort(t *testing.T) {
	isolateHome(t)
	cfg := config.Config{Browser: config.BrowserConfig{ConnectPort: 9333}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var gotPort int
	withFakeProbe(t, func(port int) (bool, string) { gotPort = port; return true, "1 open tab(s)" })

	var out strings.Builder
	if code := runBrowser([]string{"setup"}, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if gotPort != 9333 {
		t.Errorf("probed port = %d, want 9333 (from config)", gotPort)
	}
}

func TestBrowser_UnknownSubcommand(t *testing.T) {
	var out strings.Builder
	if code := runBrowser([]string{"frobnicate"}, strings.NewReader(""), &out, &out); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestBrowser_Help(t *testing.T) {
	var out strings.Builder
	if code := runBrowser([]string{"--help"}, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "chrome://inspect") {
		t.Errorf("help should mention chrome://inspect; got:\n%s", out.String())
	}
}
