package tools

import (
	"strings"
	"testing"
)

func TestBuildEnvContext_RendersSharedLines(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "zh_CN.UTF-8")

	out := BuildEnvContext("/w", "", false, false)
	for _, want := range []string{"# Environment", "/w", "Home directory:", "Today's date:", "Timezone:", "OS/arch:", "Locale:"} {
		if !strings.Contains(out, want) {
			t.Errorf("env context missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Git branch:") {
		t.Errorf("no git line expected when ok=false:\n%s", out)
	}
}

func TestBuildEnvContext_GitLineOnlyWhenOK(t *testing.T) {
	out := BuildEnvContext("/w", "main", true, true)
	if !strings.Contains(out, "Git branch: main (uncommitted changes)") {
		t.Errorf("expected dirty git line:\n%s", out)
	}
	out = BuildEnvContext("/w", "main", false, true)
	if !strings.Contains(out, "Git branch: main (clean)") {
		t.Errorf("expected clean git line:\n%s", out)
	}
	// ok=true with an empty branch must not render a malformed "Git branch:  ()".
	if got := BuildEnvContext("/w", "", true, true); strings.Contains(got, "Git branch:") {
		t.Errorf("no git line expected when ok=true but branch is empty:\n%s", got)
	}
}

func TestFormatUTCOffset(t *testing.T) {
	cases := []struct {
		off  int
		want string
	}{
		{0, "UTC+00:00"},
		{28800, "UTC+08:00"},
		{-18000, "UTC-05:00"},
		{-34200, "UTC-09:30"},
		{19800, "UTC+05:30"},
	}
	for _, c := range cases {
		if got := formatUTCOffset(c.off); got != c.want {
			t.Errorf("formatUTCOffset(%d) = %q, want %q", c.off, got, c.want)
		}
	}
}

func TestLocale(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	if got := Locale(); got != "zh_CN.UTF-8" {
		t.Errorf("Locale() with only LANG = %q, want zh_CN.UTF-8", got)
	}
	t.Setenv("LC_ALL", "C")
	if got := Locale(); got != "C" {
		t.Errorf("Locale() with LC_ALL set = %q, want C", got)
	}
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "")
	if got := Locale(); got != "" {
		t.Errorf("Locale() with neither set = %q, want empty", got)
	}
}
