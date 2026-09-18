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

// Shadowing a built-in tier is a supported way to retune delegation, and the
// shadowing file is SourceUser — so it gets named alongside the base sentence
// that already mentions the tier. The repetition is deliberate: the tier name
// now means the user's definition, and the model should see its description.
func TestSubAgentTypeParamDescFor_NamesAShadowedBuiltinTier(t *testing.T) {
	profiles := []*agentprofile.Profile{
		{ID: "general", Description: "Executes on the lite model", Source: agentprofile.SourceUser},
	}

	desc := subAgentTypeParamDescFor(profiles)
	if !strings.Contains(desc, "general (Executes on the lite model)") {
		t.Errorf("shadowed tier not named: %q", desc)
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

// A store-less caller can't name what's installed, but it must not imply
// nothing is — that would tell the model less than the old static text did.
func TestSubAgentTypeParamDesc_NilStoreKeepsTheHint(t *testing.T) {
	got := subAgentTypeParamDesc(nil)
	if got != subAgentTypeParamUnknown {
		t.Errorf("want the unknown-store description, got %q", got)
	}
	if got == subAgentTypeParamBase {
		t.Error("nil store must not collapse to the bare tier list")
	}
}

// Descriptions are commonly CJK; a byte-wise cut would emit a broken rune.
func TestClipDesc_CJKBoundary(t *testing.T) {
	got := clipDesc("你好世界你好世界", 4)
	if got != "你好世界…" {
		t.Errorf("clipDesc = %q, want %q", got, "你好世界…")
	}
	if !utf8.ValidString(got) {
		t.Errorf("clipDesc produced invalid UTF-8: %q", got)
	}
	if short := clipDesc("  短  ", 4); short != "短" {
		t.Errorf("clipDesc should trim and pass through short input, got %q", short)
	}
}

// A multi-line frontmatter description must not break the single-line
// parameter text.
func TestClipDesc_FlattensWhitespace(t *testing.T) {
	if got := clipDesc("first line\n\tsecond   line\n", 120); got != "first line second line" {
		t.Errorf("clipDesc = %q, want the lines collapsed onto one", got)
	}
}

// The clip has to survive the trip through the description builder, not just
// the helper — that is what actually reaches the schema.
func TestSubAgentTypeParamDescFor_ClipsLongDescription(t *testing.T) {
	long := strings.Repeat("长", maxAgentDescRunes+50)
	profiles := []*agentprofile.Profile{
		{ID: "verbose", Description: long, Source: agentprofile.SourceUser},
	}

	desc := subAgentTypeParamDescFor(profiles)
	if strings.Contains(desc, long) {
		t.Error("builder emitted the full oversized description")
	}
	if !strings.Contains(desc, strings.Repeat("长", maxAgentDescRunes)+"…") {
		t.Errorf("description not clipped to %d runes: %q", maxAgentDescRunes, desc)
	}
}

// List() sorts by ID, and that ordering is the only thing keeping this string
// stable across turns — an unstable one would needlessly bust the provider's
// tools-prompt cache.
func TestSubAgentTypeParamDescFor_JoinsMultipleInOrder(t *testing.T) {
	profiles := []*agentprofile.Profile{
		{ID: "alpha", Description: "First", Source: agentprofile.SourceUser},
		{ID: "beta", Description: "Second", Source: agentprofile.SourceUser},
	}

	want := subAgentTypeParamBase + " User-defined agents: alpha (First); beta (Second)."
	if got := subAgentTypeParamDescFor(profiles); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// End to end: the profile store reaches the advertised schema through the
// turn context.
//
// The store also scans the machine's real agents-default root, which this
// package can't redirect (defaultAgentsRoot is a var inside agentprofile).
// That's harmless here — everything it finds is SourceDefault and filtered
// out — and it's why the filtering itself is tested through
// subAgentTypeParamDescFor, which takes an explicit list.
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

// The advertised tool list — not DefinitionForCtx directly — is what every
// transport actually builds, so the store has to survive that trip too. This
// is the path the TUI takes after its recomputes were given a context.
func TestDefaultToolsForCtx_SubAgentNamesUserAgent(t *testing.T) {
	store := agentprofile.New(t.TempDir())
	if err := store.Create(&agentprofile.Profile{
		ID:          "executor",
		Description: "Runs well-specified changes",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := WithProfileStore(context.Background(), store)
	ctx = WithSubAgentManager(ctx, NewSubAgentManager(&resultSpawnerSync{reply: "ok"}))

	var def agent.ToolDefinition
	for _, d := range DefaultToolsForCtx(ctx, "", 0) {
		if d.Name == "sub_agent" {
			def = d
			break
		}
	}
	if def.Name == "" {
		t.Fatal("sub_agent was not advertised")
	}
	if got := subagentTypeDescOf(t, def); !strings.Contains(got, "executor") {
		t.Errorf("subagent_type description missing the user agent: %q", got)
	}
}

// Definition() carries no store, so it keeps the store-less wording.
func TestAgentTool_Definition_KeepsTheHint(t *testing.T) {
	if got := subagentTypeDescOf(t, AgentTool{}.Definition()); got != subAgentTypeParamUnknown {
		t.Errorf("Definition() subagent_type desc = %q, want the unknown-store wording", got)
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
