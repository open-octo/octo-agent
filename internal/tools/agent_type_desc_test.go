package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// The subagent_type description must name user-defined agents: their names
// appear nowhere else the model can see, so without this it can only reach one
// by guessing the file name.
func TestSubAgentTypeParamDescFor_NamesUserAgents(t *testing.T) {
	profiles := []*agentprofile.Profile{
		{ID: "executor", Description: "Runs well-specified changes", Source: agentprofile.SourceUser},
		{ID: "copywriter", Description: "Curated gallery expert", Source: agentprofile.SourceDefault},
		{ID: "general", Description: "Builtin tier", Source: agentprofile.SourceBuiltin},
	}

	desc := subAgentTypeParamDescFor(profiles)
	if !strings.Contains(desc, "executor (Runs well-specified changes)") {
		t.Errorf("description missing user agent: %q", desc)
	}
	if strings.Contains(desc, "copywriter") {
		t.Errorf("description leaked a curated expert: %q", desc)
	}
	if strings.Contains(desc, "Builtin tier") {
		t.Errorf("description re-listed a builtin tier: %q", desc)
	}
}

func TestSubAgentTypeParamDescFor_NoUserAgentsKeepsBase(t *testing.T) {
	profiles := []*agentprofile.Profile{
		{ID: "copywriter", Description: "Curated", Source: agentprofile.SourceDefault},
	}
	if got := subAgentTypeParamDescFor(profiles); got != subAgentTypeParamBase {
		t.Errorf("want the base description, got %q", got)
	}
}

func TestSubAgentTypeParamDesc_NilStoreKeepsBase(t *testing.T) {
	if got := subAgentTypeParamDesc(nil); got != subAgentTypeParamBase {
		t.Errorf("want the base description, got %q", got)
	}
}

// Descriptions are commonly CJK; a byte-wise cut would emit a broken rune.
func TestClipRunes_CJKBoundary(t *testing.T) {
	got := clipRunes("你好世界你好世界", 4)
	if got != "你好世界…" {
		t.Errorf("clipRunes = %q, want %q", got, "你好世界…")
	}
	if !utf8.ValidString(got) {
		t.Errorf("clipRunes produced invalid UTF-8: %q", got)
	}
	if short := clipRunes("  短  ", 4); short != "短" {
		t.Errorf("clipRunes should trim and pass through short input, got %q", short)
	}
}

// End to end: the profile store reaches the advertised schema through the
// turn context.
func TestAgentTool_DefinitionForCtx_NamesUserAgentFromStore(t *testing.T) {
	dir := t.TempDir()
	md := "---\nname: executor\ndescription: 执行已明确的机械任务\nmodel: lite\n---\n\nYou execute well-specified tasks.\n"
	if err := os.WriteFile(filepath.Join(dir, "executor.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := WithProfileStore(context.Background(), agentprofile.New(dir))
	got := subagentTypeDescOf(t, AgentTool{}.DefinitionForCtx(ctx, ""))

	if !strings.Contains(got, "executor") || !strings.Contains(got, "执行已明确的机械任务") {
		t.Errorf("subagent_type description missing the user agent: %q", got)
	}
}

// Definition() carries no store and must stay on the base sentence.
func TestAgentTool_Definition_KeepsBaseTypeDesc(t *testing.T) {
	if got := subagentTypeDescOf(t, AgentTool{}.Definition()); got != subAgentTypeParamBase {
		t.Errorf("Definition() subagent_type desc = %q, want the base", got)
	}
}

func subagentTypeDescOf(t *testing.T, def agent.ToolDefinition) string {
	t.Helper()
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool definition has no properties map: %#v", def.Parameters)
	}
	field, ok := props["subagent_type"].(map[string]any)
	if !ok {
		t.Fatalf("tool definition has no subagent_type property: %#v", props)
	}
	desc, _ := field["description"].(string)
	return desc
}
