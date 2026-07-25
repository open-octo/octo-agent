package skills

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

func TestManifestForProfile_NoProfileReturnsAll(t *testing.T) {
	r := Discover(t.TempDir())
	if r.Len() == 0 {
		t.Fatal("expected at least one skill from defaults")
	}
	full := RenderManifest(r)
	profiled := ManifestForProfile(r, nil)
	if full != profiled {
		t.Fatalf("nil profile should return full manifest.\nfull:\n%s\nprofiled:\n%s", full, profiled)
	}
}

func TestManifestForProfile_FiltersByToolSkills(t *testing.T) {
	r := Discover(t.TempDir())
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
		ID:         "test",
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
	r := Discover(t.TempDir())
	full := RenderManifest(r)
	profiled := ManifestForProfile(r, &agentprofile.Profile{
		ID:          "test",
		Description: "d",
		CapabilitySpec: agentprofile.CapabilitySpec{
			ToolSkills: []string{},
		},
	})
	if full != profiled {
		t.Fatalf("empty ToolSkills should return full manifest")
	}
}

func TestManifestForProfile_NoMatchReturnsEmpty(t *testing.T) {
	r := Discover(t.TempDir())
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
