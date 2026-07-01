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
			},
			"required": []string{"name", "script"},
		},
	}
}

func (WorkflowSaveTool) Execute(_ context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	name := strings.TrimSpace(stringArg(input, "name"))
	if !workflowNamePattern.MatchString(name) {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: invalid name %q — use lowercase letters, digits and dashes", name)
	}
	script := strings.TrimSpace(stringArg(input, "script"))
	if script == "" {
		return agent.ToolResult{}, fmt.Errorf("workflow_save: script is required")
	}
	description := strings.TrimSpace(stringArg(input, "description"))

	scope := strings.TrimSpace(stringArg(input, "scope"))
	if scope == "" {
		scope = "project"
	}
	var root string
	switch scope {
	case "project":
		root = projectWorkflowsRoot()
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
		fmt.Fprintf(&b, "# @description %s\n\n", description)
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
