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
	if err := MaterializeDefaults("test"); err != nil {
		t.Fatal(err)
	}
	return Discover(t.TempDir())
}

func TestManifestForProfile_NoProfileReturnsAll(t *testing.T) {
	r := discoverWithDefaults(t)
	full := RenderManifest(r)
	profiled := ManifestForProfile(r, nil)
	if full != profiled {
		t.Fatalf("nil profile should return full manifest.\nfull:\n%s\nprofiled:\n%s", full, profiled)
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
	target := allSkills[0].Name
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
	fullLines := countSkillLines(full)
	profiledLines := countSkillLines(profiled)
	if profiledLines != 1 {
		t.Fatalf("expected exactly 1 skill in profiled manifest, got %d:\n%s", profiledLines, profiled)
	}
	if fullLines != len(allSkills) {
		t.Fatalf("expected %d skills in full manifest, got %d", len(allSkills), fullLines)
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

func TestManifestForProfile_EmptyToolSkillsReturnsAll(t *testing.T) {
	r := discoverWithDefaults(t)
	full := RenderManifest(r)
	// Default agent sees the full manifest including system skills.
	profiled := ManifestForProfile(r, agentprofile.DefaultProfile())
	if full != profiled {
		t.Fatalf("empty ToolSkills should return full manifest for default agent")
	}
	// Non-default agent with empty ToolSkills gets manifest minus system skills.
	expertProfiled := ManifestForProfile(r, &agentprofile.Profile{
		ID:          "test",
		Description: "d",
	})
	if expertProfiled == full {
		t.Fatalf("non-default agent with empty ToolSkills should strip system skills")
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
