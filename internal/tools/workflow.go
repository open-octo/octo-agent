package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/workflow"
)

// defaultWorkflowConcurrency caps how many agent() calls run at once inside one
// workflow, so a parallel() over a large list can't fan out an unbounded number
// of concurrent LLM turns.
const defaultWorkflowConcurrency = 8

// WorkflowTool runs a Ruby (mruby) orchestration script in an embedded wasm
// interpreter. The script drives sub-agents through the agent() / parallel() /
// pipeline() primitives; each agent() call delegates to the same Spawner that
// backs sub_agent. The tool is advertised only when a Spawner is registered.
//
// Like sub_agent, it refuses to run inside a sub-agent — workflow agents are
// themselves marked as sub-agents, so a child can't recursively launch another
// workflow.
type WorkflowTool struct{}

// WorkflowTool streams live progress (log() output + agent lifecycle).
var _ agent.StreamingToolExecutor = WorkflowTool{}

func (WorkflowTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "workflow",
		Description: "Run a Ruby orchestration script for deterministic multi-agent work. " +
			"Use when a task decomposes into many sub-agent calls with explicit control flow " +
			"(fan-out, pipelines, loops, conditionals) that you want executed reliably rather " +
			"than improvised across turns.\n\n" +
			"The script runs in a sandboxed mruby interpreter (no file/network access — all " +
			"effects go through the primitives). Primitives:\n" +
			"- `agent(prompt) -> String`: run one sub-agent to completion, return its reply. " +
			"Inside parallel/pipeline it runs concurrently with siblings.\n" +
			"- `parallel(items) { |it| ... } -> Array`: run the block for every item " +
			"concurrently; returns results in input order.\n" +
			"- `pipeline(items, stage1, stage2, ...) -> Array`: run each item through all " +
			"stages; items flow independently (no barrier between stages). Stages are lambdas.\n" +
			"- `log(msg)`: surface a progress line.\n" +
			"- `budget_remaining -> Integer`: remaining output-token budget.\n\n" +
			"The script's final expression is returned as the result. Example:\n" +
			"```ruby\n" +
			"findings = parallel(%w[auth db cache]) { |area| agent(\"Audit the #{area} module for bugs\") }\n" +
			"\"Reviewed #{findings.size} modules:\\n\" + findings.join(\"\\n---\\n\")\n" +
			"```",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"script": map[string]any{
					"type":        "string",
					"description": "The Ruby workflow script. Its last expression is the returned result. Use agent()/parallel()/pipeline()/log() to orchestrate sub-agents.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Short human-readable label for this workflow (3-7 words). Shown in progress UI.",
				},
				"resume_from": map[string]any{
					"type": "string",
					"description": "Run ID of a prior workflow run to resume from " +
						"(format: wf-YYYYMMDD-HHMMSS-xxxxxxxx, returned as \"[workflow run: ...]\" " +
						"in a previous result). Completed agent() calls are replayed instantly " +
						"without re-running. The script must be identical to the original run.",
				},
			},
			"required": []string{"script"},
		},
	}
}

func (t WorkflowTool) Execute(ctx context.Context, name string, input map[string]any) (agent.ToolResult, error) {
	return t.ExecuteStream(ctx, name, input, func(string) {})
}

// ExecuteStream runs the workflow and streams live progress — the script's
// log() output plus each agent's start/finish — as tool-progress chunks, so the
// UI shows what a long multi-agent run is doing instead of an opaque spinner.
func (WorkflowTool) ExecuteStream(ctx context.Context, _ string, input map[string]any, progress func(chunk string)) (agent.ToolResult, error) {
	if IsSubAgent(ctx) {
		return agent.ToolResult{}, fmt.Errorf("workflow: a sub-agent cannot run a workflow")
	}

	script := strings.TrimSpace(stringArg(input, "script"))
	if script == "" {
		return agent.ToolResult{}, fmt.Errorf("workflow: script is required")
	}

	spawner := ActiveSpawner()
	if spawner == nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: sub-agent dispatch is not configured for this session")
	}

	// agent() delegates to the same Spawner that backs sub_agent. The Spawner
	// marks children as sub-agents, so a workflow agent can't recurse.
	af := func(c context.Context, prompt string) workflow.AgentResult {
		res, err := spawner.Spawn(c, SpawnRequest{
			Description: firstLine(prompt),
			Prompt:      prompt,
		})
		if err != nil {
			return workflow.AgentResult{Err: err}
		}
		return workflow.AgentResult{
			Reply:        res.Reply,
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		}
	}

	// Collect log() lines for the final result, and also stream them — plus
	// agent lifecycle — live via the progress callback.
	var logs []string
	res, err := workflow.Run(ctx, script, workflow.Options{
		Agent: af,
		Log: func(s string) {
			logs = append(logs, s)
			progress(s)
		},
		Progress:      progress,
		MaxConcurrent: defaultWorkflowConcurrency,
		ResumeFrom:    stringArg(input, "resume_from"),
	})
	if err != nil {
		return agent.ToolResult{}, workflowError(err, res, logs)
	}

	text := res.Output
	if len(logs) > 0 {
		text += "\n\n[workflow log]\n" + strings.Join(logs, "\n")
	}
	if res.RunID != "" {
		text += "\n\n[workflow run: " + res.RunID + "]"
	}
	return agent.ToolResult{Text: text}, nil
}

// workflowError turns a failed run into an actionable tool error. A script
// error is the model's own Ruby — it must learn to fix the script and re-call
// workflow rather than treat the failure as terminal — so the message says so
// explicitly. When agents completed before the failure, their results are
// journaled under res.RunID; surfacing it lets the retry pass resume_from to
// skip the work already done. Logs are appended last since they often show how
// far the run got.
func workflowError(err error, res workflow.Result, logs []string) error {
	var b strings.Builder
	if strings.Contains(err.Error(), "script error") {
		b.WriteString("workflow: the Ruby script you wrote failed to run. ")
		b.WriteString("Fix the script and call workflow again.\n\n")
		b.WriteString(err.Error())
		// Resume hint only when at least one agent actually ran (tokens spent);
		// a compile/syntax error journals nothing, so resume would be a no-op.
		if res.RunID != "" && (res.OutputTokens > 0 || res.InputTokens > 0) {
			b.WriteString(fmt.Sprintf(
				"\n\nSome agents completed before the failure. To skip re-running them, "+
					"pass resume_from: %q in the retry (only the agent() calls that didn't "+
					"finish will run again).", res.RunID))
		}
	} else {
		b.WriteString("workflow: ")
		b.WriteString(err.Error())
	}
	if len(logs) > 0 {
		b.WriteString("\n\n[workflow log]\n")
		b.WriteString(strings.Join(logs, "\n"))
	}
	return fmt.Errorf("%s", b.String())
}
