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
}
