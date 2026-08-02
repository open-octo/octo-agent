package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/skills"
)

// setupTestSkills points skill discovery at a temp HOME holding one skill per
// map entry (name → system flag) and registers it as the active registry.
func setupTestSkills(t *testing.T, specs map[string]bool) {
	t.Helper()
	home := t.TempDir()
	for name, system := range specs {
		dir := filepath.Join(home, ".octo", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		fm := fmt.Sprintf("---\nname: %s\ndescription: test skill %s\n", name, name)
		if system {
			fm += "system: true\n"
		}
		fm += "---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, skills.SkillFile), []byte(fm), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	SetSkills(skills.Discover())
	t.Cleanup(func() { SetSkills(nil) })
}

func newExpertStore(t *testing.T, id string, toolNames []string) *agentprofile.Store {
	t.Helper()
	store := agentprofile.New(t.TempDir())
	if err := store.Create(&agentprofile.Profile{
		ID:          id,
		Description: "test expert",
		CapabilitySpec: agentprofile.CapabilitySpec{
			Tools: toolNames,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func enableOwnCtx(store *agentprofile.Store, agentID string) context.Context {
	return WithSessionAgentID(WithProfileStore(context.Background(), store), agentID)
}

func TestEnableOwnSkill_EnablesInstalledSkill(t *testing.T) {
	setupTestSkills(t, map[string]bool{"test-skill": false})
	store := newExpertStore(t, "expert", []string{"skill", "enable_own_skill"})
	tool := EnableOwnSkillTool{}

	res, err := tool.Execute(enableOwnCtx(store, "expert"), "enable_own_skill", map[string]any{"name": "test-skill"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "Enabled skill") || !strings.Contains(res.Text, "next message") {
		t.Fatalf("unexpected result text: %q", res.Text)
	}
	p, ok := store.Get("expert")
	if !ok || len(p.ToolSkills) != 1 || p.ToolSkills[0] != "test-skill" {
		t.Fatalf("profile ToolSkills = %v, ok = %v", p.ToolSkills, ok)
	}
}

func TestEnableOwnSkill_AlreadyEnabledIsNoOp(t *testing.T) {
	setupTestSkills(t, map[string]bool{"test-skill": false})
	store := newExpertStore(t, "expert", []string{"skill", "enable_own_skill"})
	tool := EnableOwnSkillTool{}
	ctx := enableOwnCtx(store, "expert")

	if _, err := tool.Execute(ctx, "enable_own_skill", map[string]any{"name": "test-skill"}); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(ctx, "enable_own_skill", map[string]any{"name": "test-skill"})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if !strings.Contains(res.Text, "already enabled") {
		t.Fatalf("expected already-enabled no-op, got %q", res.Text)
	}
	p, _ := store.Get("expert")
	if len(p.ToolSkills) != 1 {
		t.Fatalf("no-op duplicated the skill: ToolSkills = %v", p.ToolSkills)
	}
}

func TestEnableOwnSkill_UnknownSkill(t *testing.T) {
	setupTestSkills(t, map[string]bool{"test-skill": false})
	store := newExpertStore(t, "expert", []string{"skill", "enable_own_skill"})

	_, err := EnableOwnSkillTool{}.Execute(enableOwnCtx(store, "expert"), "enable_own_skill", map[string]any{"name": "ghost-skill"})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected not-installed error, got %v", err)
	}
}

func TestEnableOwnSkill_SystemSkillRejected(t *testing.T) {
	setupTestSkills(t, map[string]bool{"sys-skill": true})
	store := newExpertStore(t, "expert", []string{"skill", "enable_own_skill"})

	_, err := EnableOwnSkillTool{}.Execute(enableOwnCtx(store, "expert"), "enable_own_skill", map[string]any{"name": "sys-skill"})
	if err == nil || !strings.Contains(err.Error(), "system skill") {
		t.Fatalf("expected system-skill rejection, got %v", err)
	}
}

func TestEnableOwnSkill_BuiltinRejected(t *testing.T) {
	setupTestSkills(t, map[string]bool{"test-skill": false})
	store := agentprofile.New(t.TempDir())

	_, err := EnableOwnSkillTool{}.Execute(enableOwnCtx(store, agentprofile.DefaultID), "enable_own_skill", map[string]any{"name": "test-skill"})
	if err == nil || !strings.Contains(err.Error(), "builtin") {
		t.Fatalf("expected builtin rejection, got %v", err)
	}
}

func TestEnableOwnSkill_NoProfileContext(t *testing.T) {
	setupTestSkills(t, map[string]bool{"test-skill": false})

	_, err := EnableOwnSkillTool{}.Execute(context.Background(), "enable_own_skill", map[string]any{"name": "test-skill"})
	if err == nil || !strings.Contains(err.Error(), "no agent profile context") {
		t.Fatalf("expected missing-context error, got %v", err)
	}
}

func TestEnableOwnSkill_Advertising(t *testing.T) {
	has := func(defs []agent.ToolDefinition, name string) bool {
		for _, d := range defs {
			if d.Name == name {
				return true
			}
		}
		return false
	}

	// Expert with the tool in its allowlist: advertised.
	store := newExpertStore(t, "expert", []string{"read_file", "enable_own_skill"})
	defs := DefaultToolsForProfile(enableOwnCtx(store, "expert"), "test-model")
	if !has(defs, "enable_own_skill") {
		t.Fatal("enable_own_skill should be advertised for an expert whose allowlist includes it")
	}

	// Expert without it in the allowlist: filtered out by the allowlist.
	other := newExpertStore(t, "expert-no-tool", []string{"read_file"})
	defs = DefaultToolsForProfile(enableOwnCtx(other, "expert-no-tool"), "test-model")
	if has(defs, "enable_own_skill") {
		t.Fatal("enable_own_skill should be filtered out when absent from the profile's allowlist")
	}

	// Builtin profile: gated off — builtins already see every installed skill.
	defs = DefaultToolsForProfile(enableOwnCtx(store, agentprofile.DefaultID), "test-model")
	if has(defs, "enable_own_skill") {
		t.Fatal("enable_own_skill should be hidden from builtin profiles")
	}

	// No profile context (CLI/TUI): gated off.
	defs = DefaultToolsForProfile(context.Background(), "test-model")
	if has(defs, "enable_own_skill") {
		t.Fatal("enable_own_skill should be hidden without a profile context")
	}
}
