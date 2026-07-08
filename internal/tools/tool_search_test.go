package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// fakeCatalog installs a fixed MCP catalog for the duration of a test, so the
// bridge logic can be exercised without a live MCP registry.
func fakeCatalog(t *testing.T, defs []agent.ToolDefinition) {
	t.Helper()
	prev := mcpCatalog
	mcpCatalog = func() []agent.ToolDefinition { return defs }
	t.Cleanup(func() { mcpCatalog = prev })
}

// resetToolSearchConfig restores defaults after a test mutates the global.
func resetToolSearchConfig(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetToolSearchConfig(defaultToolSearchConfig()) })
}

func sampleCatalog() []agent.ToolDefinition {
	return []agent.ToolDefinition{
		{Name: "mcp__github__create_issue", Description: "Create a new GitHub issue in a repository",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"}}}},
		{Name: "mcp__github__list_pulls", Description: "List pull requests",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"type": "string"}}}},
		{Name: "mcp__slack__post_message", Description: "Post a message to a Slack channel",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"channel": map[string]any{"type": "string"}}}},
	}
}

func TestExecToolDescribe_ReturnsSchema(t *testing.T) {
	fakeCatalog(t, sampleCatalog())
	res, err := execToolDescribe(map[string]any{"name": "mcp__github__create_issue"})
	if err != nil {
		t.Fatalf("execToolDescribe: %v", err)
	}
	for _, want := range []string{"mcp__github__create_issue", "properties", "title", "body"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("schema missing %q:\n%s", want, res.Text)
		}
	}
}

func TestExecToolDescribe_UnknownName(t *testing.T) {
	fakeCatalog(t, sampleCatalog())
	if _, err := execToolDescribe(map[string]any{"name": "mcp__nope__nothing"}); err == nil {
		t.Error("expected error for unknown tool name")
	}
}

func TestToolCallTarget_Unwraps(t *testing.T) {
	real, args, ok := ToolCallTarget(toolCallName, map[string]any{
		"name":      "mcp__github__create_issue",
		"arguments": map[string]any{"title": "hi"},
	})
	if !ok || real != "mcp__github__create_issue" {
		t.Fatalf("unwrap = (%q, %v)", real, ok)
	}
	if args["title"] != "hi" {
		t.Errorf("args not carried through: %v", args)
	}
	// A non-mcp_call name is left alone.
	if _, _, ok := ToolCallTarget("terminal", map[string]any{"command": "ls"}); ok {
		t.Error("ToolCallTarget should not unwrap a non-mcp_call name")
	}
}

func TestExecToolCall_RejectsNonMCP(t *testing.T) {
	// mcp_call only proxies mcp__ tools; a bare name must error cleanly.
	if _, err := execToolCall(context.Background(), map[string]any{"name": "terminal", "arguments": map[string]any{}}); err == nil {
		t.Error("expected error proxying a non-MCP tool name")
	}
}

func TestToolSearchActive_Modes(t *testing.T) {
	resetToolSearchConfig(t)
	cat := sampleCatalog()

	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOff})
	if toolSearchActive("claude-opus-4-8", cat) {
		t.Error("off mode must never activate")
	}

	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOn})
	if !toolSearchActive("claude-opus-4-8", cat) {
		t.Error("on mode must activate when MCP tools present")
	}
	if toolSearchActive("claude-opus-4-8", nil) {
		t.Error("on mode must not activate with an empty catalog")
	}

	// auto with an empty model never activates (back-compat for DefaultTools()).
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchAuto, ThresholdPct: 10})
	if toolSearchActive("", cat) {
		t.Error("auto with empty model must not activate")
	}
}

func TestToolSearchActive_AutoThreshold(t *testing.T) {
	resetToolSearchConfig(t)
	// A tiny catalog is well under 10% of a 1M-token window → no activation.
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchAuto, ThresholdPct: 10})
	if toolSearchActive("claude-opus-4-8", sampleCatalog()) {
		t.Error("small catalog should stay under the auto threshold for a 1M window")
	}
	// A 0% threshold means any non-empty catalog crosses it.
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchAuto, ThresholdPct: 1})
	big := make([]agent.ToolDefinition, 0, 400)
	for i := 0; i < 400; i++ {
		big = append(big, agent.ToolDefinition{
			Name:        "mcp__srv__tool",
			Description: strings.Repeat("x", 500),
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
		})
	}
	if !toolSearchActive("claude-haiku-4-5", big) {
		t.Error("a large catalog should cross the auto threshold")
	}
}

func TestDefaultToolsFor_BridgeReplacesCatalog(t *testing.T) {
	resetToolSearchConfig(t)
	fakeCatalog(t, sampleCatalog())
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOn})

	defs := DefaultToolsFor("claude-opus-4-8")
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{toolDescribeName, toolCallName} {
		if !names[want] {
			t.Errorf("expected bridge tool %q in the list", want)
		}
	}
	// The raw MCP tools must NOT be advertised when the bridge is active.
	if names["mcp__github__create_issue"] {
		t.Error("raw MCP tool should be deferred behind the bridge, not advertised")
	}
}

func TestDefaultToolsFor_OffUploadsFullCatalog(t *testing.T) {
	resetToolSearchConfig(t)
	fakeCatalog(t, sampleCatalog())
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOff})

	defs := DefaultToolsFor("claude-opus-4-8")
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["mcp__github__create_issue"] {
		t.Error("off mode must advertise the raw MCP tools")
	}
	if names[toolDescribeName] {
		t.Error("off mode must not advertise the bridge")
	}
}

func TestMCPManifestFor_ActiveListsNamesNotSchema(t *testing.T) {
	resetToolSearchConfig(t)
	fakeCatalog(t, sampleCatalog())
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOn})

	manifest := MCPManifestFor("claude-opus-4-8")
	for _, want := range []string{"mcp__github__create_issue", "mcp__github__list_pulls", "mcp__slack__post_message", "# Available MCP tools"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
	// The manifest is name + one-line description only — no schema, no cap.
	if strings.Contains(manifest, "properties") || strings.Contains(manifest, "\"title\"") {
		t.Errorf("manifest leaked schema:\n%s", manifest)
	}
}

func TestMCPManifestFor_InactiveReturnsEmpty(t *testing.T) {
	resetToolSearchConfig(t)
	fakeCatalog(t, sampleCatalog())
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOff})

	if got := MCPManifestFor("claude-opus-4-8"); got != "" {
		t.Errorf("bridge inactive should yield no manifest, got:\n%s", got)
	}
}

func TestMCPManifestFor_EmptyCatalogReturnsEmpty(t *testing.T) {
	resetToolSearchConfig(t)
	fakeCatalog(t, nil)
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOn})

	if got := MCPManifestFor("claude-opus-4-8"); got != "" {
		t.Errorf("empty catalog should yield no manifest, got:\n%s", got)
	}
}

// TestMCPManifestFor_NoCap is the regression guard for the "no truncation"
// decision (#1243 v2): a catalog far larger than would ever realistically
// occur must still list every tool.
func TestMCPManifestFor_NoCap(t *testing.T) {
	resetToolSearchConfig(t)
	big := make([]agent.ToolDefinition, 0, 200)
	for i := 0; i < 200; i++ {
		big = append(big, agent.ToolDefinition{
			Name:        "mcp__srv__tool_" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Description: "does a thing",
			Parameters:  map[string]any{"type": "object"},
		})
	}
	fakeCatalog(t, big)
	SetToolSearchConfig(ToolSearchConfig{Mode: ToolSearchOn})

	manifest := MCPManifestFor("claude-opus-4-8")
	got := strings.Count(manifest, "\n- mcp__srv__tool_")
	if got != len(big) {
		t.Errorf("manifest listed %d of %d tools, want all of them uncapped", got, len(big))
	}
}
