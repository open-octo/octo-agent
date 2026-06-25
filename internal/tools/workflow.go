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

func (WorkflowTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "workflow",
		Description: "Start a Ruby orchestration script for deterministic multi-agent work. " +
			"Use when a task decomposes into many sub-agent calls with explicit control flow " +
			"(fan-out, pipelines, loops, conditionals) that you want executed reliably rather " +
			"than improvised across turns.\n\n" +
			"Runs in the BACKGROUND: this call returns a run id immediately; collect the result " +
			"later with the workflow_status tool. (A long multi-agent run won't block you or the " +
			"user while it executes.)\n\n" +
			"The script runs in a sandboxed mruby interpreter (no file/network access — all " +
			"effects go through the primitives). Primitives:\n" +
			"- `agent(prompt, opts = {}) -> String`: run one sub-agent to completion, return " +
			"its reply. Inside parallel/pipeline it runs concurrently with siblings. " +
			"Optional opts: `model:` (override the model for this call, e.g. a cheaper model " +
			"for mechanical stages), `tools:` (Array restricting the child's tools), " +
			"`read_only: true` (strip write_file/edit_file), `schema:` (a JSON Schema as a " +
			"JSON **string** — the call then returns the sub-agent's reply as JSON text matching it).\n" +
			"- `parallel(items) { |it| ... } -> Array`: run the block for every item " +
			"concurrently; returns results in input order.\n" +
			"- `pipeline(items, stage1, stage2, ...) -> Array`: run each item through all " +
			"stages; items flow independently (no barrier between stages). Stages are lambdas.\n" +
			"- `log(msg)`: surface a progress line.\n" +
			"- `phase(title)`: mark the start of a named stage; groups the progress " +
			"stream into steps (cosmetic, does not affect scheduling).\n" +
			"- `budget_remaining -> Integer`: remaining output-token budget.\n\n" +
			"The script's final expression is the run's result. Example:\n" +
			"```ruby\n" +
			"findings = parallel(%w[auth db cache]) { |area| agent(\"Audit the #{area} module for bugs\") }\n" +
			"\"Reviewed #{findings.size} modules:\\n\" + findings.join(\"\\n---\\n\")\n" +
			"```",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"script": map[string]any{
					"type":        "string",
					"description": "The Ruby workflow script. Its last expression is the run's result. Use agent()/parallel()/pipeline()/log() to orchestrate sub-agents.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Short human-readable label for this workflow (3-7 words). Shown in the status list and progress UI.",
				},
				"resume_from": map[string]any{
					"type": "string",
					"description": "Run id of a prior workflow run to resume from " +
						"(format: wf-YYYYMMDD-HHMMSS-xxxxxxxx, shown as \"[workflow run: ...]\" " +
						"in a previous result). Completed agent() calls are replayed instantly " +
						"without re-running. The script must be identical to the original run.",
				},
			},
			"required": []string{"script"},
		},
	}
}

// Execute starts the workflow in the background and returns its run handle.
func (WorkflowTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
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
	af := func(c context.Context, prompt string, opts workflow.AgentOptions) workflow.AgentResult {
		res, err := spawner.Spawn(c, SpawnRequest{
			Description: firstLine(prompt),
			Prompt:      prompt,
			Model:       opts.Model,
			Tools:       opts.Tools,
			ReadOnly:    opts.ReadOnly,
			Schema:      opts.Schema,
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

	mgr := resolveWorkflowManager(ctx)
	runID, err := mgr.Start(WorkflowRunRequest{
		Description:   strings.TrimSpace(stringArg(input, "description")),
		Script:        script,
		Agent:         af,
		MaxConcurrent: defaultWorkflowConcurrency,
		ResumeFrom:    stringArg(input, "resume_from"),
	})
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: %w", err)
	}
	return agent.ToolResult{Text: fmt.Sprintf(
		"Workflow started in the background as %s. It runs while you continue; "+
			"call workflow_status(%q) to check progress and collect the result "+
			"(or workflow_status with no argument to list all runs).", runID, runID)}, nil
}

// WorkflowStatusTool reports on background workflow runs: a list with no
// argument, or one run's full status + result when given a run id.
type WorkflowStatusTool struct{}

func (WorkflowStatusTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "workflow_status",
		Description: "Check background workflow runs started with the workflow tool. " +
			"With no run_id: list this session's runs and their status (running/done/error). " +
			"With a run_id: the full result (or error + how to fix/resume) plus the captured log.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"run_id": map[string]any{
					"type":        "string",
					"description": "A run id from the workflow tool (e.g. \"wf_1\"). Omit to list all runs.",
				},
			},
		},
	}
}

func (WorkflowStatusTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	mgr := resolveWorkflowManager(ctx)
	runID := strings.TrimSpace(stringArg(input, "run_id"))
	if runID == "" {
		runs := mgr.List()
		if len(runs) == 0 {
			return agent.ToolResult{Text: "No background workflows have been started in this session."}, nil
		}
		lines := make([]string, 0, len(runs))
		for _, r := range runs {
			lines = append(lines, statusLine(r))
		}
		return agent.ToolResult{Text: strings.Join(lines, "\n")}, nil
	}
	snap, ok := mgr.Read(runID)
	if !ok {
		return agent.ToolResult{}, fmt.Errorf("workflow_status: no run named %q in this session", runID)
	}
	return agent.ToolResult{Text: formatRunDetail(snap)}, nil
}
