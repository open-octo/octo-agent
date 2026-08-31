package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/skills"
)

// discoverSkills builds a registry from a temp $HOME holding the given user
// skill (name → SKILL.md content) and restores the package state after.
func setSkillsFor(t *testing.T, name, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".octo", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skills.SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	SetSkills(skills.Discover())
	t.Cleanup(func() { SetSkills(nil) })
}

func TestSkillTool_Execute(t *testing.T) {
	setSkillsFor(t, "greet", "---\ndescription: say hi\n---\nStep 1: be nice.")

	out, err := SkillTool{}.Execute(context.Background(), "skill", map[string]any{"name": "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The body comes through, prefixed with a location header so the model can
	// resolve files the skill references.
	if !strings.Contains(out.Text, "Step 1: be nice.") {
		t.Errorf("body missing from result: %q", out.Text)
	}
	if !strings.Contains(out.Text, "bundled files live in") || !strings.Contains(out.Text, filepath.Join(".octo", "skills", "greet")) {
		t.Errorf("expected a skill-directory header; got: %q", out.Text)
	}
}

func TestSkillTool_Errors(t *testing.T) {
	SetSkills(nil)
	t.Cleanup(func() { SetSkills(nil) })

	// No name.
	if _, err := (SkillTool{}).Execute(context.Background(), "skill", map[string]any{}); err == nil {
		t.Error("expected error for missing name")
	}
	// Name given but no skills configured.
	if _, err := (SkillTool{}).Execute(context.Background(), "skill", map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), "no skills") {
		t.Errorf("expected 'no skills' error, got %v", err)
	}

	// Unknown skill when some exist.
	setSkillsFor(t, "real", "---\ndescription: d\n---\nbody")
	if _, err := (SkillTool{}).Execute(context.Background(), "skill", map[string]any{"name": "ghost"}); err == nil || !strings.Contains(err.Error(), "unknown skill") {
		t.Errorf("expected 'unknown skill' error, got %v", err)
	}
}

func TestDefaultTools_SkillToolGatedOnRegistry(t *testing.T) {
	SetSkills(nil)
	t.Cleanup(func() { SetSkills(nil) })

	has := func() bool {
		for _, d := range DefaultTools() {
			if d.Name == "skill" {
				return true
			}
		}
		return false
	}

	if has() {
		t.Error("skill tool should be absent when no skills are discovered")
	}

	setSkillsFor(t, "x", "---\ndescription: d\n---\nbody")
	if !has() {
		t.Error("skill tool should be present once a skill is discovered")
	}
}

// expertCtx builds a context resolving to a user-created expert whose profile
// enables exactly the given tool_skills.
func expertCtx(t *testing.T, toolSkills []string) context.Context {
	t.Helper()
	store := agentprofile.New(t.TempDir())
	if err := store.Create(&agentprofile.Profile{
		ID:          "helper",
		Description: "expert under test",
		CapabilitySpec: agentprofile.CapabilitySpec{
			SystemPrompt: "You help.",
			ToolSkills:   toolSkills,
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithProfileStore(context.Background(), store)
	return WithSessionAgentID(ctx, "helper")
}

// A non-builtin expert may only load skills its profile names — the manifest
// hides the rest, and the load path must hold the same line against a guessed
// or injected name.
func TestSkillTool_ExpertLoadRestrictedToToolSkills(t *testing.T) {
	setSkillsFor(t, "greet", "---\ndescription: say hi\n---\nStep 1: be nice.")

	// Not in tool_skills: refused with a pointer at the configuration surface.
	if _, err := (SkillTool{}).Execute(expertCtx(t, nil), "skill", map[string]any{"name": "greet"}); err == nil || !strings.Contains(err.Error(), "not enabled for this agent") {
		t.Errorf("expected 'not enabled' error, got %v", err)
	}

	// Named in tool_skills: loads.
	out, err := (SkillTool{}).Execute(expertCtx(t, []string{"greet"}), "skill", map[string]any{"name": "greet"})
	if err != nil {
		t.Fatalf("enabled skill should load: %v", err)
	}
	if !strings.Contains(out.Text, "Step 1: be nice.") {
		t.Errorf("body missing from result: %q", out.Text)
	}

	// Builtin profile: unrestricted.
	store := agentprofile.New(t.TempDir())
	ctx := WithSessionAgentID(WithProfileStore(context.Background(), store), "general")
	if _, err := (SkillTool{}).Execute(ctx, "skill", map[string]any{"name": "greet"}); err != nil {
		t.Errorf("builtin profile should load any skill: %v", err)
	}

	// No profile context (CLI/TUI): unrestricted.
	if _, err := (SkillTool{}).Execute(context.Background(), "skill", map[string]any{"name": "greet"}); err != nil {
		t.Errorf("context-less session should load any skill: %v", err)
	}
}

// A system skill stays with the Default agent even when an expert's
// tool_skills names it (matching ManifestForProfile, which hides it too).
func TestSkillTool_ExpertCannotLoadSystemSkill(t *testing.T) {
	setSkillsFor(t, "mgmt", "---\ndescription: manage\nsystem: true\n---\nbody")

	if _, err := (SkillTool{}).Execute(expertCtx(t, []string{"mgmt"}), "skill", map[string]any{"name": "mgmt"}); err == nil || !strings.Contains(err.Error(), "system skill") {
		t.Errorf("expected 'system skill' error, got %v", err)
	}
}
