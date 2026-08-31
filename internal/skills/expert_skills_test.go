package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// writeSkillDir drops a minimal skill under root/name.
func writeSkillDir(t *testing.T, root, name, desc string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: " + desc + "\n---\nbody of " + name
	if err := os.WriteFile(filepath.Join(dir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An expert skill is discovered with its own source, stays out of the global
// manifest, and appears in an expert's manifest only when tool_skills names it.
func TestExpertSkills_ScopedToNamingProfiles(t *testing.T) {
	// Isolate the user root too — Discover reads ~/.octo/skills via HOME.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	defRoot := filepath.Join(t.TempDir(), "skills-default")
	expRoot := filepath.Join(t.TempDir(), "skills-expert")
	useDefaultRoot(t, defRoot)
	useExpertRoot(t, expRoot)
	writeSkillDir(t, defRoot, "everyday", "for everyone")
	writeSkillDir(t, expRoot, "legal-lookup", "for the legal expert")

	r := Discover()
	s, ok := r.Get("legal-lookup")
	if !ok || s.Source != "expert" {
		t.Fatalf("expert skill not discovered as expert: ok=%v source=%q", ok, s.Source)
	}

	// Global manifest: default skill in, expert skill out.
	global := RenderManifest(r)
	if !strings.Contains(global, "everyday") {
		t.Errorf("global manifest should list the default skill:\n%s", global)
	}
	if strings.Contains(global, "legal-lookup") {
		t.Errorf("global manifest must not list an expert skill:\n%s", global)
	}

	// An expert whose tool_skills names it sees it.
	expert := &agentprofile.Profile{
		ID:          "legal-helper",
		Description: "d",
		CapabilitySpec: agentprofile.CapabilitySpec{
			ToolSkills: []string{"legal-lookup"},
		},
	}
	profiled := ManifestForProfile(r, expert)
	if !strings.Contains(profiled, "legal-lookup") {
		t.Errorf("naming expert should see the expert skill:\n%s", profiled)
	}
	if strings.Contains(profiled, "everyday") {
		t.Errorf("expert manifest should hold only named skills:\n%s", profiled)
	}
}

// A registry holding ONLY expert skills renders an empty global manifest, not
// a header with no entries.
func TestRenderManifest_OnlyExpertSkillsIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	expRoot := filepath.Join(t.TempDir(), "skills-expert")
	useDefaultRoot(t, filepath.Join(t.TempDir(), "none"))
	useExpertRoot(t, expRoot)
	writeSkillDir(t, expRoot, "niche", "expert only")

	if got := RenderManifest(Discover()); got != "" {
		t.Fatalf("expected empty manifest, got:\n%s", got)
	}
}

// Expert skills are octo-managed like the defaults and refuse deletion — with
// a worse failure mode: the version stamp still matches after a delete, so
// the root would not re-materialize until the next version bump, leaving the
// experts naming the skill silently crippled.
func TestRegistryDelete_RefusesExpertSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	expRoot := filepath.Join(t.TempDir(), "skills-expert")
	useDefaultRoot(t, filepath.Join(t.TempDir(), "none"))
	useExpertRoot(t, expRoot)
	writeSkillDir(t, expRoot, "legal-lookup", "for the legal expert")

	r := Discover()
	if err := r.Delete("legal-lookup"); err == nil {
		t.Fatal("deleting an expert skill should be refused")
	}
	if _, err := os.Stat(filepath.Join(expRoot, "legal-lookup", SkillFile)); err != nil {
		t.Fatalf("expert skill directory should be untouched: %v", err)
	}
	if _, ok := r.Get("legal-lookup"); !ok {
		t.Fatal("expert skill should still be in the registry")
	}
}

// The expert root scans after the defaults, so a same-named expert skill
// would shadow the default one and silently drop it from every global
// manifest. Keep the embedded sets disjoint.
func TestEmbeddedExpertSkills_NoNameCollisionWithDefaults(t *testing.T) {
	names := func(fsys fs.FS, prefix string) map[string]bool {
		entries, err := fs.ReadDir(fsys, prefix)
		if err != nil {
			t.Fatalf("read embedded %s: %v", prefix, err)
		}
		out := make(map[string]bool, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				out[e.Name()] = true
			}
		}
		return out
	}
	defaults := names(defaultsFS, "defaults")
	for name := range names(expertsFS, "experts") {
		if defaults[name] {
			t.Errorf("expert skill %q shares a name with a default skill — it would shadow it", name)
		}
	}
}

// MaterializeDefaults writes the expert root too (currently just its stamp
// and the embedded README placeholder — content lands here as experts gain
// bundled skills).
func TestMaterializeDefaults_WritesExpertRoot(t *testing.T) {
	useDefaultRoot(t, filepath.Join(t.TempDir(), "skills-default"))
	expRoot := filepath.Join(t.TempDir(), "skills-expert")
	useExpertRoot(t, expRoot)

	if err := MaterializeDefaults("v1"); err != nil {
		t.Fatalf("MaterializeDefaults: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(expRoot, defaultStampFile)); err != nil || strings.TrimSpace(string(b)) != "v1" {
		t.Fatalf("expert root stamp missing or wrong: %q err=%v", b, err)
	}
}
