package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/agent"
)

// Tool Search defers MCP tool schemas behind two bridge tools instead of
// uploading every schema on every turn. The tool's name and a one-line
// description are always visible to the model (rendered into the system
// prompt via MCPManifestFor, the same way skills.RenderManifest surfaces
// "# Available skills") so the model never needs a round trip just to learn
// whether a tool exists. mcp_describe loads one schema on demand, and
// mcp_call invokes it — which routes straight into executeMCP, so all the
// existing mcp__-prefix dispatch, permission, and hook machinery runs against
// the real tool name. See dev-docs/tool-search-mcp.md.

// Bridge tool names. The mcp_ prefix is deliberate: it tells the model these
// two tools are the MCP-only describe/invoke path, so it doesn't mistake
// mcp_call for a generic dispatcher and route built-in tools (sub_agent,
// read_file, …) through it. mcp_call is the only path by which a deferred MCP
// tool is actually invoked when Tool Search is active.
const (
	toolDescribeName = "mcp_describe"
	toolCallName     = "mcp_call"
)

// mcpCatalog returns the deferred MCP tool catalog (the same defs mcpToolDefs
// would upload in full). A package var so tests can inject a fixed catalog
// without standing up a live MCP registry — mirrors jinaReaderHostForTest.
var mcpCatalog = mcpToolDefs

// ── configuration ──────────────────────────────────────────────────────────

// ToolSearchMode selects when the bridge replaces full MCP schema upload.
type ToolSearchMode int

const (
	// ToolSearchAuto activates the bridge only when deferred MCP schemas would
	// occupy at least ThresholdPct% of the model's context window.
	ToolSearchAuto ToolSearchMode = iota
	// ToolSearchOn activates the bridge whenever any MCP tool is present.
	ToolSearchOn
	// ToolSearchOff never activates the bridge — MCP schemas upload in full.
	ToolSearchOff
)

// ToolSearchConfig is the tools-package view of the user's tool_search config.
// cmd/octo maps the ~/.octo/config.yml block onto this and installs it via
// SetToolSearchConfig, mirroring SetSandbox / SetMCPRegistry.
type ToolSearchConfig struct {
	Mode         ToolSearchMode
	ThresholdPct int // auto-mode activation threshold, percent of context window
}

// defaultToolSearchConfig matches the documented defaults (auto / 10%).
func defaultToolSearchConfig() ToolSearchConfig {
	return ToolSearchConfig{Mode: ToolSearchAuto, ThresholdPct: 10}
}

var (
	toolSearchCfgMu sync.RWMutex
	toolSearchCfg   = defaultToolSearchConfig()
)

// SetToolSearchConfig installs the active tool_search configuration. Fields
// left at their zero value fall back to the documented defaults so a partial
// config block behaves sensibly.
func SetToolSearchConfig(c ToolSearchConfig) {
	d := defaultToolSearchConfig()
	if c.ThresholdPct <= 0 {
		c.ThresholdPct = d.ThresholdPct
	}
	toolSearchCfgMu.Lock()
	toolSearchCfg = c
	toolSearchCfgMu.Unlock()
}

func toolSearchConfig() ToolSearchConfig {
	toolSearchCfgMu.RLock()
	defer toolSearchCfgMu.RUnlock()
	return toolSearchCfg
}

// toolSearchActive decides whether to replace the MCP defs with the bridge for
// a given model. mcpDefs is the catalog that would otherwise be uploaded.
//
// An empty model (the DefaultTools back-compat entry) never activates the
// bridge, so callers that don't know the model keep the original behaviour.
func toolSearchActive(model string, mcpDefs []agent.ToolDefinition) bool {
	if len(mcpDefs) == 0 {
		return false
	}
	switch toolSearchConfig().Mode {
	case ToolSearchOff:
		return false
	case ToolSearchOn:
		return true
	default: // auto
		if model == "" {
			return false
		}
		window := agent.ContextWindow(model)
		budget := window * toolSearchConfig().ThresholdPct / 100
		return estimateSchemaTokens(mcpDefs) >= budget
	}
}

// estimateSchemaTokens approximates how many tokens the MCP tool definitions
// occupy. A coarse bytes/4 heuristic is plenty for a threshold decision.
func estimateSchemaTokens(defs []agent.ToolDefinition) int {
	bytes := 0
	for _, d := range defs {
		bytes += len(d.Name) + len(d.Description)
		if b, err := json.Marshal(d.Parameters); err == nil {
			bytes += len(b)
		}
	}
	return bytes / 4
}

// ── bridge tool definitions ────────────────────────────────────────────────

// toolSearchBridgeDefs returns the two bridge tools advertised in place of
// the full MCP catalog when Tool Search is active.
func toolSearchBridgeDefs() []agent.ToolDefinition {
	return []agent.ToolDefinition{
		{
			Name: toolDescribeName,
			Description: "Load the full JSON Schema (parameters) for one MCP tool listed under " +
				"\"# Available MCP tools\" in the system prompt. Call this before mcp_call so you " +
				"know the tool's exact arguments.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The exact tool name from the \"# Available MCP tools\" list (e.g. 'mcp__github__create_issue').",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name: toolCallName,
			Description: "Invoke an MCP tool (and ONLY an MCP tool — its name starts with mcp__) " +
				"listed under \"# Available MCP tools\" in the system prompt. Pass the tool name and " +
				"its arguments (matching the schema from mcp_describe). This is the only way to call " +
				"a deferred MCP tool while Tool Search is active. Do NOT route built-in tools " +
				"(sub_agent, read_file, terminal, …) through here — call those directly by their own name.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The exact MCP tool name to invoke (e.g. 'mcp__github__create_issue').",
					},
					"arguments": map[string]any{
						"type":        "object",
						"description": "The arguments object for the tool, matching its schema from mcp_describe.",
					},
				},
				"required": []string{"name", "arguments"},
			},
		},
	}
}

// ── system-prompt manifest ──────────────────────────────────────────────────

// MCPManifestFor renders the "# Available MCP tools" catalog injected into
// the system prompt when Tool Search is active for model: a name + one-line
// description for every deferred MCP tool, with no cap or pagination (this
// list is name+description only, never schema, so its size is negligible
// next to the schema budget that triggers the bridge in the first place). The
// model reads this once from the prompt and picks a tool directly — no
// discovery round trip.
//
// Returns "" when the bridge isn't active for model (the full per-tool
// definitions, schema included, are already inline in the tools array, so
// repeating the names in the prompt would just duplicate them) or when there
// are no MCP tools connected.
func MCPManifestFor(model string) string {
	catalog := mcpCatalog()
	if !toolSearchActive(model, catalog) {
		return ""
	}
	return renderMCPManifest(catalog)
}

// renderMCPManifest builds the manifest text for a given catalog. Split out
// from MCPManifestFor so tests can exercise the rendering independently of
// the activation gate.
func renderMCPManifest(catalog []agent.ToolDefinition) string {
	if len(catalog) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Available MCP tools\n\n")
	b.WriteString("These MCP tools are connected but their full parameter schemas are not " +
		"loaded up front. Call mcp_describe with a tool's exact name to load its schema, then " +
		"mcp_call to invoke it.\n\n")
	for _, d := range catalog {
		desc := firstLine(d.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- %s: %s\n", d.Name, desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── dispatch (called from DefaultRegistry.Execute) ─────────────────────────

// execToolDescribe returns the full JSON Schema of one catalog tool.
func execToolDescribe(input map[string]any) (agent.ToolResult, error) {
	name := strings.TrimSpace(stringArg(input, "name"))
	if name == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("mcp_describe: name is required")
	}
	for _, d := range mcpCatalog() {
		if d.Name == name {
			schema, err := json.MarshalIndent(d.Parameters, "", "  ")
			if err != nil {
				return agent.ToolResult{Text: ""}, fmt.Errorf("mcp_describe: marshal schema: %w", err)
			}
			return agent.ToolResult{Text: fmt.Sprintf("%s\n\n%s\n\n%s", d.Name, firstLine(d.Description), string(schema))}, nil
		}
	}
	return agent.ToolResult{Text: ""}, fmt.Errorf("mcp_describe: no tool named %q (check the \"# Available MCP tools\" list in the system prompt for the exact name)", name)
}

// execToolCall unwraps {name, arguments} and forwards to executeMCP, so the
// real MCP dispatch (and the permission/hook machinery keyed on the real name)
// runs unchanged.
func execToolCall(ctx context.Context, input map[string]any) (agent.ToolResult, error) {
	name := strings.TrimSpace(stringArg(input, "name"))
	if name == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("mcp_call: name is required")
	}
	args, _ := input["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	out, ok, err := executeMCP(ctx, name, args)
	if !ok {
		// executeMCP only declines names without the mcp__ prefix.
		return agent.ToolResult{Text: ""}, fmt.Errorf("mcp_call: %q is not an MCP tool (MCP names look like 'mcp__<server>__<tool>'). If it's a built-in tool such as sub_agent, call it directly by name instead of through mcp_call", name)
	}
	return agent.ToolResult{Text: out}, err
}

// ToolCallTarget unwraps an mcp_call bridge invocation into the real MCP tool
// name and its arguments, so permission checks and hooks key on the real tool
// rather than the "mcp_call" wrapper. ok is false when name isn't an mcp_call
// (or carries no inner tool name), in which case the caller uses name/input
// unchanged.
func ToolCallTarget(name string, input map[string]any) (realName string, realInput map[string]any, ok bool) {
	if name != toolCallName {
		return "", nil, false
	}
	realName = strings.TrimSpace(stringArg(input, "name"))
	if realName == "" {
		return "", nil, false
	}
	realInput, _ = input["arguments"].(map[string]any)
	if realInput == nil {
		realInput = map[string]any{}
	}
	return realName, realInput, true
}
