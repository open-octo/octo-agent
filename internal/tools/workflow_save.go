package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// workflowNamePattern constrains a saved-workflow name to a safe file stem:
// lowercase letters, digits and dashes, so it maps to <name>.rb with no path
// traversal or shell-hostile characters.
var workflowNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// paramNamePattern constrains a declared param name to a valid Ruby hash key
// as it will appear, unquoted, in a `# @param <name> ...` comment line —
// letters, digits and underscore, so a name can never be confused with the
// "required" keyword or split across the whitespace-delimited parse.
var paramNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// WorkflowSaveTool persists a Ruby workflow script to the registry so it can be
// re-run by name with the workflow tool. Like the workflow tool, it is
// advertised only when a Spawner is configured.
type WorkflowSaveTool struct{}

func (WorkflowSaveTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "workflow_save",
		Description: "Save a Ruby workflow script as a reusable named workflow, so you can later " +
			"run it with the workflow tool's `name` parameter (passing `args` to parameterize it). " +
			"Writes <name>.rb to the project's .octo/workflows (default) or the user-level " +
			"~/.octo/workflows. Use after you've built and validated a workflow you'll want again.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Identifier for the workflow (lowercase letters, digits, dashes), used as the <name>.rb filename and the name you pass to the workflow tool.",
				},
				"script": map[string]any{
					"type":        "string",
					"description": "The Ruby workflow script body. Read inputs via the `args` primitive so the saved workflow is reusable.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "One-line summary (stored as a `# @description` comment, shown when listing saved workflows).",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"project", "user"},
					"description": "Where to save: \"project\" (default) = the repo's .octo/workflows; \"user\" = ~/.octo/workflows (available across projects).",
				},
				"params": map[string]any{
					"type": "array",
					"description": "Optional declared inputs for this workflow, stored as `# @param` comments. " +
						"Mark one required to make the workflow tool check for it before running and prompt " +
						"the user for a value when it's missing from `args`, instead of the script failing on " +
						"a nil args[...] lookup at runtime.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "Key read via args[\"name\"] in the script. Letters, digits and underscore only — no spaces.",
							},
							"required": map[string]any{
								"type":        "boolean",
								"description": "Whether the workflow tool must have this value before running.",
							},
							"description": map[string]any{
								"type":        "string",
								"description": "Shown to the user when prompting for a missing value.",
							},
						},
						"required": []string{"name"},
					},
				},
			},
			"required": []string{"name", "script"},
		},
	}
}

func (WorkflowSaveTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	name := strings.TrimSpace(stringArg(input, "name"))
	if !workflowNamePattern.MatchString(name) {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: invalid name %q — use lowercase letters, digits and dashes", name)
	}
	script := strings.TrimSpace(stringArg(input, "script"))
	if script == "" {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: script is required")
	}
	description := strings.TrimSpace(stringArg(input, "description"))
	params, err := parseWorkflowSaveParams(input["params"])
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: %w", err)
	}

	scope := strings.TrimSpace(stringArg(input, "scope"))
	if scope == "" {
		scope = "project"
	}
	var root string
	switch scope {
	case "project":
		root = projectWorkflowsRoot(WorkingDirOrCWD(ctx))
		if root == "" {
			return agent.ToolResult{}, fmt.Errorf("workflow_save: no project root for scope \"project\" — run inside a repository or use scope \"user\"")
		}
	case "user":
		root = userWorkflowsRoot()
		if root == "" {
			return agent.ToolResult{}, fmt.Errorf("workflow_save: cannot resolve the home directory for scope \"user\"")
		}
	default:
		return agent.ToolResult{}, fmt.Errorf("workflow_save: invalid scope %q — use \"project\" or \"user\"", scope)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: create %s: %w", root, err)
	}
	path := filepath.Join(root, name+".rb")
	_, existed := os.Stat(path)
	overwrote := existed == nil

	var b strings.Builder
	if description != "" {
		fmt.Fprintf(&b, "# @description %s\n", description)
	}
	for _, p := range params {
		fmt.Fprintf(&b, "# @param %s\n", formatWorkflowParamComment(p))
	}
	if description != "" || len(params) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(script)
	if !strings.HasSuffix(script, "\n") {
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: write %s: %w", path, err)
	}

	verb := "Saved"
	if overwrote {
		verb = "Overwrote"
	}
	return agent.ToolResult{Text: fmt.Sprintf(
		"%s workflow %q to %s. Run it with workflow(name: %q, args: {...}).",
		verb, name, path, name)}, nil
}

// parseWorkflowSaveParams validates and converts the tool's `params` input
// (an array of {name, required, description} objects) into workflowParams,
// ready to render as `# @param` comment lines. nil input is not an error —
// most workflows have no declared params.
func parseWorkflowSaveParams(raw any) ([]workflowParam, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("params must be an array")
	}
	out := make([]workflowParam, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("each params entry must be an object")
		}
		name := strings.TrimSpace(stringArg(m, "name"))
		if !paramNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid param name %q — use letters, digits and underscore only", name)
		}
		out = append(out, workflowParam{
			name:        name,
			required:    askBool(m, "required"),
			description: strings.TrimSpace(stringArg(m, "description")),
		})
	}
	return out, nil
}

// formatWorkflowParamComment renders a workflowParam as the text following
// `# @param ` in a saved workflow file — the exact shape workflowParams (in
// workflow_registry.go) parses back.
func formatWorkflowParamComment(p workflowParam) string {
	s := p.name
	if p.required {
		s += " required"
	}
	if p.description != "" {
		s += " " + p.description
	}
	return s
}
