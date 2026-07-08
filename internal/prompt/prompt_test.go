package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompose_BaseAlwaysPresent(t *testing.T) {
	out := Compose("", t.TempDir(), "", "", "", "", false) // empty user/env/skills/mcp, no .octorules
	if !strings.Contains(out, "octo") {
		t.Errorf("composed prompt should contain the base identity:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("base layer must never be empty")
	}
}

func TestComposePair_LeanDropsSkillsMCPAndMemory(t *testing.T) {
	dir := t.TempDir()
	full, lean := ComposePair("USER_RULE", dir, "ENV_BLOCK", "SKILLS_MANIFEST", "MCP_MANIFEST", "MEMORY_BLOCK", true)

	// Full keeps everything; lean drops skills + mcp tools + memory but keeps env + user.
	for _, want := range []string{"SKILLS_MANIFEST", "MCP_MANIFEST", "MEMORY_BLOCK", "ENV_BLOCK", "USER_RULE"} {
		if !strings.Contains(full, want) {
			t.Errorf("full system missing %q", want)
		}
	}
	if strings.Contains(lean, "SKILLS_MANIFEST") || strings.Contains(lean, "MCP_MANIFEST") || strings.Contains(lean, "MEMORY_BLOCK") {
		t.Errorf("lean system should drop skills + mcp tools + memory:\n%s", lean)
	}
	for _, want := range []string{"ENV_BLOCK", "USER_RULE"} {
		if !strings.Contains(lean, want) {
			t.Errorf("lean system should keep %q", want)
		}
	}
}

func TestCompose_LayersInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectContextFile), []byte("PROJECT_RULE_X"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := Compose("USER_RULE_Y", dir, "ENV_BLOCK_Z", "SKILLS_MANIFEST_W", "MCP_MANIFEST_V", "", true)

	baseIdx := strings.Index(out, "octo")
	envIdx := strings.Index(out, "ENV_BLOCK_Z")
	skillsIdx := strings.Index(out, "SKILLS_MANIFEST_W")
	mcpIdx := strings.Index(out, "MCP_MANIFEST_V")
	projIdx := strings.Index(out, "PROJECT_RULE_X")
	userIdx := strings.Index(out, "USER_RULE_Y")

	if baseIdx == -1 || envIdx == -1 || skillsIdx == -1 || mcpIdx == -1 || projIdx == -1 || userIdx == -1 {
		t.Fatalf("all six layers should be present:\n%s", out)
	}
	// Order: base < env < skills < mcp tools < project < user.
	if !(baseIdx < envIdx && envIdx < skillsIdx && skillsIdx < mcpIdx && mcpIdx < projIdx && projIdx < userIdx) {
		t.Errorf("layer order wrong: base=%d env=%d skills=%d mcp=%d project=%d user=%d", baseIdx, envIdx, skillsIdx, mcpIdx, projIdx, userIdx)
	}
	if !strings.Contains(out, ProjectContextFile) {
		t.Errorf("project layer should be labelled with the source file")
	}
}

func TestCompose_SkipsAbsentLayers(t *testing.T) {
	// Isolate from real ~/.octo/{soul,user,octorules}.md so the test runs the
	// same on a developer machine and on a fresh CI runner.
	useIdentityFiles(t, "", "")
	// No env, no skills, no mcp tools, no .octorules, no user prompt → just the base, no separators.
	out := Compose("", t.TempDir(), "", "", "", "", false)
	if strings.Contains(out, "---") {
		t.Errorf("single-layer prompt should have no separator:\n%s", out)
	}
}

func TestReadProjectContext_MissingFileIsEmpty(t *testing.T) {
	if got := readProjectContext(t.TempDir()); got != "" {
		t.Errorf("missing .octorules should yield empty, got %q", got)
	}
	if got := readProjectContext(""); got != "" {
		t.Errorf("empty cwd should yield empty, got %q", got)
	}
}
