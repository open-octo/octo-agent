package skills

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// discoverWithDefaults materializes the embedded default skills into a temp
// directory and returns a Registry that has them loaded. Required because
// TestMain redirects defaultSkillsRoot to an empty temp dir; tests that need
// the real defaults must opt in.
func discoverWithDefaults(t *testing.T) *Registry {
	t.Helper()
	root := filepath.Join(t.TempDir(), "skills-default")
	useDefaultRoot(t, root)
	useExpertRoot(t, t.TempDir())
	if err := MaterializeDefaults("test"); err != nil {
		t.Fatal(err)
	}
	return Discover()
}

func TestManifestForProfile_NoProfileReturnsEmpty(t *testing.T) {
	r := discoverWithDefaults(t)
	if profiled := ManifestForProfile(r, nil); profiled != "" {
		t.Fatalf("nil profile should return an empty manifest, got:\n%s", profiled)
	}
}

func TestManifestForProfile_FiltersByToolSkills(t *testing.T) {
	r := discoverWithDefaults(t)
	full := RenderManifest(r)
	if full == "" {
		t.Fatal("expected non-empty manifest")
	}
	allSkills := r.List()
	if len(allSkills) == 0 {
		t.Fatal("expected skills")
	}
	// Pick the first non-system skill — a system skill (e.g. skill-creator)
	// would be stripped from an expert agent's manifest and break the
	// one-skill-count assertion below.
	var target string
	for _, s := range allSkills {
		if !s.System {
			target = s.Name
			break
		}
	}
	if target == "" {
		t.Skip("no non-system skill available")
	}
	profiled := ManifestForProfile(r, &agentprofile.Profile{
		ID:          "test",
		Description: "d",
		CapabilitySpec: agentprofile.CapabilitySpec{
			ToolSkills: []string{target},
		},
	})
	if profiled == "" {
		t.Fatal("expected non-empty manifest")
	}
	// The profiled manifest should contain the target skill.
	if !containsSkill(profiled, target) {
		t.Fatalf("manifest missing target skill %q:\n%s", target, profiled)
	}
	// Count skill entries: profiled should have exactly 1, full should have all.
	// RenderManifest deliberately drops expert-scoped skills (they ship for
	// specific built-in experts), so the full count must exclude those too.
	fullLines := countSkillLines(full)
	profiledLines := countSkillLines(profiled)
	if profiledLines != 1 {
		t.Fatalf("expected exactly 1 skill in profiled manifest, got %d:\n%s", profiledLines, profiled)
	}
	nonExpert := 0
	for _, s := range allSkills {
		if s.Source != "expert" {
			nonExpert++
		}
	}
	if fullLines != nonExpert {
		t.Fatalf("expected %d skills in full manifest, got %d", nonExpert, fullLines)
	}
}

func countSkillLines(manifest string) int {
	n := 0
	for _, line := range strings.Split(manifest, "\n") {
		if strings.HasPrefix(line, "- ") {
			n++
		}
	}
	return n
}

func TestManifestForProfile_EmptyToolSkills(t *testing.T) {
	r := discoverWithDefaults(t)
	full := RenderManifest(r)
	// A builtin profile (the Default agent, or explore/general/code-review)
	// sees the full manifest including system skills — same "empty means
	// unrestricted" rule tools.DefaultToolsForProfile applies to Tools.
	profiled := ManifestForProfile(r, agentprofile.DefaultProfile())
	if full != profiled {
		t.Fatalf("empty ToolSkills should return full manifest for a builtin profile")
	}
	// Any non-builtin profile (curated expert or user-created agent) with no
	// ToolSkills declared sees NO skills — it must opt in explicitly, same as
	// the Tools allowlist rule for non-builtin profiles.
	unscopedProfiled := ManifestForProfile(r, &agentprofile.Profile{
		ID:          "test",
		Description: "d",
	})
	if unscopedProfiled != "" {
		t.Fatalf("non-builtin agent with empty ToolSkills should see no skills, got:\n%s", unscopedProfiled)
	}
}

func TestManifestForProfile_NoMatchReturnsEmpty(t *testing.T) {
	r := discoverWithDefaults(t)
	profiled := ManifestForProfile(r, &agentprofile.Profile{
		ID:          "test",
		Description: "d",
		CapabilitySpec: agentprofile.CapabilitySpec{
			ToolSkills: []string{"nonexistent-skill-xyz"},
		},
	})
	if profiled != "" {
		t.Fatalf("expected empty manifest for non-matching ToolSkills, got:\n%s", profiled)
	}
}

func containsSkill(manifest, name string) bool {
	return len(name) > 0 && strings.Contains(manifest, name)
}
