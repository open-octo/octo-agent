package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/workflow"
)

// synthesizeFromSchema builds a value matching a JSON Schema fragment: every
// declared array property gets exactly one synthesized item (so "ready" /
// "code" / "safe"-style routing branches in a workflow script are non-empty),
// every boolean is true, every enum picks its first value, and every other
// scalar gets a short placeholder.
func synthesizeFromSchema(node map[string]any, depth int) any {
	if depth > 8 {
		return nil
	}
	if enumVals, ok := node["enum"].([]any); ok && len(enumVals) > 0 {
		return enumVals[0]
	}
	switch node["type"] {
	case "object":
		obj := map[string]any{}
		if props, ok := node["properties"].(map[string]any); ok {
			for k, v := range props {
				if propSchema, ok := v.(map[string]any); ok {
					obj[k] = synthesizeFromSchema(propSchema, depth+1)
				}
			}
		}
		return obj
	case "array":
		items, ok := node["items"].(map[string]any)
		if !ok {
			return []any{}
		}
		return []any{synthesizeFromSchema(items, depth+1)}
	case "boolean":
		return true
	case "integer", "number":
		return 1
	default:
		return "ok"
	}
}

// schemaFakeAgent synthesizes a JSON reply matching whatever schema an
// agent() call requests, so an embedded workflow script can be driven all the
// way to its final expression without a real LLM or real tool access.
func schemaFakeAgent(_ context.Context, _ string, opts workflow.AgentOptions) workflow.AgentResult {
	if opts.Schema == "" {
		return workflow.AgentResult{Reply: "ok"}
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(opts.Schema), &schema); err != nil {
		return workflow.AgentResult{Reply: "ok"}
	}
	reply, err := json.Marshal(synthesizeFromSchema(schema, 0))
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("synthesize reply: %w", err)}
	}
	return workflow.AgentResult{Reply: string(reply)}
}

// TestEmbeddedDefaultWorkflows_Execute proves every embedded default workflow
// actually runs through the mruby sandbox to a final result, not just that
// its name resolves in the registry (TestLookupWorkflow_EmbeddedDefaultAlwaysAvailable
// only checks the latter). The sandbox is IO-free — no File, Dir, Time, or
// shell backticks — so a script that assumes otherwise fails here with a
// Ruby-level error before ever reaching its final expression.
func TestEmbeddedDefaultWorkflows_Execute(t *testing.T) {
	useWorkflowRoots(t, "", "")

	type run struct {
		name string
		args string // JSON; "" means the script's `args` primitive returns nil
	}
	runs := []run{
		{"adversarial-review", ""},
		{"parallel-understand", ""},
		{"batch-migrate", `{"change": "test"}`},
		{"daily-triage", ""},
	}

	for _, r := range runs {
		t.Run(fmt.Sprintf("%s/%s", r.name, r.args), func(t *testing.T) {
			w, ok := lookupWorkflow(r.name)
			if !ok {
				t.Fatalf("lookupWorkflow(%q): not found", r.name)
			}
			got, err := workflow.Run(context.Background(), w.script, workflow.Options{
				Agent: schemaFakeAgent,
				Args:  r.args,
			})
			if err != nil {
				t.Fatalf("workflow.Run(%q, args=%s): %v", r.name, r.args, err)
			}
			if strings.TrimSpace(got.Output) == "" {
				t.Errorf("workflow.Run(%q, args=%s): empty output", r.name, r.args)
			}
		})
	}
}

// TestLoopEngineeringTemplates_Execute proves the loop-engineering skill's
// reference templates (internal/skills/defaults/loop-engineering/templates/)
// are runnable mruby scripts, even though they're deliberately NOT embedded
// workflow-registry defaults (see TestLookupWorkflow_ReferenceTemplatesNotEmbedded).
// A user or agent copies one of these into a `workflow` call or `workflow_save`;
// a template that can't actually execute would be a worse starting point than
// no template at all.
func TestLoopEngineeringTemplates_Execute(t *testing.T) {
	type run struct {
		name string
		args string
	}
	runs := []run{
		{"issue-triage", ""},
		{"pr-babysitter", ""},
		{"ci-sweeper", ""},
		{"ci-sweeper", `{"dry_run": false, "retry_flaky": true}`},
		{"dependency-sweeper", ""},
		{"dependency-sweeper", `{"dry_run": false}`},
		{"changelog-drafter", ""},
		{"post-merge-cleanup", ""},
		{"post-merge-cleanup", `{"apply": true}`},
	}

	for _, r := range runs {
		t.Run(fmt.Sprintf("%s/%s", r.name, r.args), func(t *testing.T) {
			path := filepath.Join("..", "skills", "defaults", "loop-engineering", "templates", r.name+".rb")
			script, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading template %q: %v", path, err)
			}
			got, err := workflow.Run(context.Background(), string(script), workflow.Options{
				Agent: schemaFakeAgent,
				Args:  r.args,
			})
			if err != nil {
				t.Fatalf("workflow.Run(%q, args=%s): %v", r.name, r.args, err)
			}
			if strings.TrimSpace(got.Output) == "" {
				t.Errorf("workflow.Run(%q, args=%s): empty output", r.name, r.args)
			}
		})
	}
}
