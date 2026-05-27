package main

import (
	"strings"
	"testing"
)

func TestBuildEnvContext_IncludesCoreFields(t *testing.T) {
	out := buildEnvContext("/some/dir")
	for _, want := range []string{"# Environment", "/some/dir", "Today's date:", "OS/arch:"} {
		if !strings.Contains(out, want) {
			t.Errorf("env context missing %q:\n%s", want, out)
		}
	}
}

func TestBuildEnvContext_GitStateForRepo(t *testing.T) {
	// The octo-agent repo itself is a git repo, so a branch line should appear
	// when we point at a path inside it (the test's own working dir).
	out := buildEnvContext(".")
	if !strings.Contains(out, "Git branch:") {
		t.Errorf("expected a git branch line for a repo cwd:\n%s", out)
	}
}

func TestBuildEnvContext_NonRepoOmitsGit(t *testing.T) {
	// A temp dir is not a git repo → no git line, but the block still renders.
	out := buildEnvContext(t.TempDir())
	if strings.Contains(out, "Git branch:") {
		t.Errorf("non-repo dir should not produce a git line:\n%s", out)
	}
	if !strings.Contains(out, "# Environment") {
		t.Errorf("env block should still render without git:\n%s", out)
	}
}

func TestNewCacheKey_StableAndPrefixed(t *testing.T) {
	k := newCacheKey()
	if !strings.HasPrefix(k, "octo-") {
		t.Errorf("cache key should be prefixed: %q", k)
	}
	if k == newCacheKey() {
		t.Error("two cache keys should differ (random)")
	}
}
