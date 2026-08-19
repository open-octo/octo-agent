package tools

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools/genui"
)

// RenderUITool lets the model render a structured, whitelisted UI panel
// (cards, stats, tables, lists, …) as a rich tool-result card, instead of
// describing the same information in plain text. See dev-docs/genui-design.md
// ("Slice A: render_ui tool") and the genui default skill for the full spec.
//
// The result never reaches the model as anything more than a one-line
// summary — the structured spec travels on ToolResult.UI, the same channel
// the write_file/edit_file/show_artifact tools already use for the web
// Artifacts panel and other rich result cards (internal/agent/tool.go).
type RenderUITool struct{}

func (RenderUITool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "render_ui",
		Description: "Render a structured UI panel (cards, stats, tables, lists, badges, " +
			"progress bars, callouts) in the chat instead of describing the same information in " +
			"plain text. Only use this after loading the genui skill, which documents the full " +
			"node-type schema and when to prefer this over write_file/show_artifact.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"spec": map[string]any{
					"type":        "object",
					"description": "GenUI spec: {title?: string, items: GenuiNode[]}. See the genui skill for the node-type table.",
				},
			},
			"required": []string{"spec"},
		},
	}
}

func (RenderUITool) Execute(_ context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	spec, ok := input["spec"].(map[string]any)
	if !ok {
		return agent.ToolResult{}, fmt.Errorf("render_ui: spec is required and must be an object")
	}
	sanitized, count, err := genui.Sanitize(spec, genui.ReadOnlyNodeTypes)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("render_ui: %w", err)
	}
	return agent.ToolResult{
		Text: fmt.Sprintf("Rendered a UI panel with %d component(s).", count),
		UI:   map[string]any{"type": "genui", "spec": sanitized},
	}, nil
}
