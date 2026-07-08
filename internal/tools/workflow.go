package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/workflow"
)

// defaultWorkflowConcurrency caps how many agent() calls run at once inside one
// workflow, so a parallel() over a large list can't fan out an unbounded number
// of concurrent LLM turns.
const defaultWorkflowConcurrency = 8

// workflowForeground, when set, makes the workflow tool run scripts to
// completion inside the tool call instead of detaching a background run. The
// headless one-shot enables it: that mode exits when the turn ends (killing
// still-running background work), so a detached run could never deliver its
// result — the completion notification the background texts promise would
// never arrive. Interactive transports (TUI, web, IM) leave it off.
var workflowForeground atomic.Bool

// SetWorkflowForeground toggles foreground (blocking) workflow execution for
// this process. Set it before the tool definitions are built — the workflow
// tool's description tells the model which mode it is in.
func SetWorkflowForeground(v bool) { workflowForeground.Store(v) }

// workflowDiscoveryCWD holds the working directory savedWorkflowsParamDesc and
// ListNamedWorkflows resolve project-level workflows from. It exists because
// agent.ToolDefinition's Definition() method takes no context — there is no
// way for it to see a specific turn's WithWorkingDir value directly — so
// prepareToolTurn (regular sessions) and the scheduled-task runner (cron
// tasks) stamp this process-global right before building tool definitions for
// their turn, restoring the previous value once the turn ends (see
// SetWorkflowDiscoveryCWD). This is the one process-global swap-and-restore
// prepareToolTurn still does — sub_agent/workflow advertisement itself moved
// to the ctx-aware DefaultToolsForCtx and no longer needs one (#1133). Empty
// (the zero value) falls back to the process CWD, matching every caller that
// never sets it (CLI, tests).
var workflowDiscoveryCWD atomic.Value // string

// SetWorkflowDiscoveryCWD records the directory the next Definition()-driven
// workflow listing (the `workflow` tool's `name` parameter description, and
// ListNamedWorkflows for the web panel) should resolve project-level
// workflows from. Callers should restore the previous value (via
// ActiveWorkflowDiscoveryCWD, read before overwriting) once their turn ends.
func SetWorkflowDiscoveryCWD(cwd string) { workflowDiscoveryCWD.Store(cwd) }

// ActiveWorkflowDiscoveryCWD returns the cwd most recently set by
// SetWorkflowDiscoveryCWD, or "" if never set.
func ActiveWorkflowDiscoveryCWD() string {
	v, _ := workflowDiscoveryCWD.Load().(string)
	return v
}

// workflowJournalDir overrides the workflow runtime's journal directory
// (~/.octo/workflow-journals by default, resolved by internal/workflow).
// Empty (the zero value) leaves the runtime default in place — every real
// entry point (CLI, server, IM) never sets this. Tests point it at a temp dir
// so running the suite doesn't write into a developer's real journal
// directory.
var workflowJournalDir atomic.Value // string

// SetWorkflowJournalDir overrides the workflow runtime's journal directory for
// this process. Pass "" to restore the default.
func SetWorkflowJournalDir(dir string) { workflowJournalDir.Store(dir) }

// ActiveWorkflowJournalDir returns the dir set by SetWorkflowJournalDir, or ""
// (the runtime default) if never set.
func ActiveWorkflowJournalDir() string {
	v, _ := workflowJournalDir.Load().(string)
	return v
}

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
	// The execution contract differs by transport: interactive transports run
	// workflows detached (with a completion notification), the headless
	// one-shot runs them inline. Tell the model which one it gets.
	mode := "Runs in the BACKGROUND: this call returns a run id immediately, and the system " +
		"automatically notifies you with the result when the run finishes — do NOT poll " +
		"workflow_status while it runs. (A long multi-agent run won't block you or the " +
		"user while it executes.)"
	if workflowForeground.Load() {
		mode = "Runs in the FOREGROUND: this call blocks until the script completes and " +
			"returns the final result directly — no run id to track, nothing to poll, " +
			"no notification to wait for."
	}
	return agent.ToolDefinition{
		Name: "workflow",
		Description: "Run a Ruby orchestration script for deterministic multi-agent work — " +
			"either inline via `script`, or a saved workflow by `name` (see the name parameter). " +
			"Use when a task decomposes into many sub-agent calls with explicit control flow " +
			"(fan-out, pipelines, loops, conditionals) that you want executed reliably rather " +
			"than improvised across turns.\n\n" +
			mode + "\n\n" +
			"The script runs in a sandboxed, IO-free mruby interpreter: only Array/Hash/String/" +
			"Integer logic and JSON.parse/JSON.generate are available. There is NO File, Dir, " +
			"Time, Process, or shell backticks (`cmd`) — referencing any of them raises a Ruby " +
			"error before the script produces any result. The ONLY way to touch the filesystem, " +
			"run a shell/git/gh command, or get the current date is through agent(prompt, opts) " +
			"below, which delegates to a real sub-agent with real tools. To persist a report or " +
			"state file, tell an agent() call to write it (it has write_file) — never call " +
			"File.write yourself. Primitives:\n" +
			"- `agent(prompt, opts = {}) -> String`: run one sub-agent to completion, return " +
			"its reply. Inside parallel/pipeline it runs concurrently with siblings. " +
			"Optional opts: `model:` (override the model for this call, e.g. a cheaper model " +
			"for mechanical stages), `tools:` (Array restricting the child's tools), " +
			"`read_only: true` (strip write_file/edit_file), `schema:` (a JSON Schema as a " +
			"JSON **string** — the call then returns the sub-agent's reply as JSON text matching it), " +
			"`isolation: \"worktree\"` (run the sub-agent in a fresh git worktree so its file changes " +
			"don't touch the main checkout — useful for parallel agents that write files; changes are " +
			"left on a branch named in the reply).\n" +
			"- `parallel(items) { |it| ... } -> Array`: run the block for every item " +
			"concurrently; returns results in input order.\n" +
			"- `pipeline(items, stage1, stage2, ...) -> Array`: run each item through all " +
			"stages; items flow independently (no barrier between stages). Stages are lambdas.\n" +
			"- `log(msg)`: surface a progress line.\n" +
			"- `phase(title)`: mark the start of a named stage; groups the progress " +
			"stream into steps (cosmetic, does not affect scheduling).\n" +
			"- `budget_remaining -> Integer`: remaining output-token budget.\n" +
			"- `args -> Hash/Array/scalar`: the input value passed as this tool's `args` " +
			"parameter, parsed from JSON into native Ruby (nil when none). Use it to " +
			"parameterize a reusable script, e.g. `target = args[\"target\"]`.\n" +
			"- `JSON.parse(str)` / `JSON.generate(obj)` are available: decode a " +
			"schema-constrained agent() reply, or encode structured data back into a prompt.\n\n" +
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
					"description": "The Ruby workflow script. Its last expression is the run's result. Use agent()/parallel()/pipeline()/log() to orchestrate sub-agents. Provide exactly one of script or name.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": savedWorkflowsParamDesc(),
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Short human-readable label for this workflow (3-7 words). Shown in the status list and progress UI.",
				},
				"args": map[string]any{
					"type":        "object",
					"description": "Input value for the script, readable as the `args` primitive (parsed to native Ruby). Use to parameterize a reusable workflow instead of hardcoding values into the script.",
				},
				"resume_from": map[string]any{
					"type": "string",
					"description": "Run id of a prior workflow run to resume from " +
						"(format: wf-YYYYMMDD-HHMMSS-xxxxxxxx, shown as \"[workflow run: ...]\" " +
						"in a previous result). Completed agent() calls are replayed instantly " +
						"without re-running. The script must be identical to the original run.",
				},
			},
		},
	}
}

// savedWorkflowsParamDesc builds the `name` parameter description, listing the
// saved workflows currently in the registries (~/.octo/workflows and the
// project's .octo/workflows) so the model knows what it can run by name.
// Project-level workflows are resolved from ActiveWorkflowDiscoveryCWD when
// set (prepareToolTurn / the scheduled-task runner stamp it before building
// tool definitions for their turn) — Definition() itself takes no context, so
// this is the only way it can see a specific turn's working directory.
func savedWorkflowsParamDesc() string {
	var b strings.Builder
	b.WriteString("Run a saved workflow by name (from ~/.octo/workflows or the project's " +
		".octo/workflows). Provide exactly one of script or name; args are passed in either way.")
	saved := listWorkflows(WithWorkingDir(context.Background(), ActiveWorkflowDiscoveryCWD()))
	if len(saved) == 0 {
		b.WriteString(" (No saved workflows found yet — author one with workflow_save.)")
		return b.String()
	}
	b.WriteString(" Available:")
	for _, w := range saved {
		line := "\n- " + w.name
		if w.description != "" {
			line += " — " + w.description
		}
		if req := requiredParamNames(w.params); len(req) > 0 {
			line += " (requires: " + strings.Join(req, ", ") + ")"
		}
		b.WriteString(line)
	}
	return b.String()
}

// requiredParamNames returns the names of params marked required, in
// declaration order.
func requiredParamNames(params []workflowParam) []string {
	var out []string
	for _, p := range params {
		if p.required {
			out = append(out, p.name)
		}
	}
	return out
}

// ensureRequiredWorkflowParams checks a saved workflow's declared `# @param
// name required ...` inputs against the tool's `args` input. Any that are
// missing are filled by prompting the user (via the same Asker the
// ask_user_question tool uses) rather than letting the script hit a nil
// args[...] lookup at runtime. Returns the args map to use when at least one
// value was filled in (nil, nil when nothing was missing), or an error if a
// required value couldn't be obtained (no asker available, or the user
// cancelled).
func ensureRequiredWorkflowParams(ctx context.Context, workflowName string, params []workflowParam, args map[string]any) (map[string]any, error) {
	var missing []workflowParam
	for _, p := range params {
		if !p.required {
			continue
		}
		if v, ok := args[p.name]; ok && v != nil && v != "" {
			continue
		}
		missing = append(missing, p)
	}
	if len(missing) == 0 {
		return nil, nil
	}

	asker := askerFrom(ctx)
	if asker == nil {
		names := make([]string, len(missing))
		for i, p := range missing {
			names[i] = p.name
		}
		return nil, fmt.Errorf("workflow %q is missing required arg(s) %s and no user is available to "+
			"ask for them in this mode — pass them in `args` instead", workflowName, strings.Join(names, ", "))
	}

	filled := make(map[string]any, len(args)+len(missing))
	for k, v := range args {
		filled[k] = v
	}
	for _, p := range missing {
		question := fmt.Sprintf("Workflow %q needs a value for %q", workflowName, p.name)
		if p.description != "" {
			question += ": " + p.description
		}
		res, err := asker.Ask(ctx, AskRequest{Question: question, Header: p.name})
		if err != nil {
			return nil, fmt.Errorf("workflow: %w", err)
		}
		if res.Cancelled {
			return nil, fmt.Errorf("workflow %q: user cancelled while providing required arg %q", workflowName, p.name)
		}
		value := res.Custom
		if value == "" && len(res.Choices) > 0 {
			value = res.Choices[0]
		}
		filled[p.name] = value
	}
	return filled, nil
}

// Execute runs the workflow: detached with a run handle in background mode,
// or to completion with the result returned inline in foreground mode.
func (WorkflowTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	if IsSubAgent(ctx) {
		return agent.ToolResult{}, fmt.Errorf("workflow: a sub-agent cannot run a workflow")
	}

	script := strings.TrimSpace(stringArg(input, "script"))
	name := strings.TrimSpace(stringArg(input, "name"))
	description := strings.TrimSpace(stringArg(input, "description"))
	switch {
	case script != "" && name != "":
		return agent.ToolResult{}, fmt.Errorf("workflow: provide exactly one of script or name, not both")
	case script == "" && name == "":
		return agent.ToolResult{}, fmt.Errorf("workflow: provide a script, or a name of a saved workflow")
	case name != "":
		w, ok := lookupWorkflow(ctx, name)
		if !ok {
			return agent.ToolResult{}, fmt.Errorf("workflow: no saved workflow named %q (looked in ~/.octo/workflows and .octo/workflows)", name)
		}
		script = w.script
		if description == "" {
			description = w.description
		}
		if len(w.params) > 0 {
			argsMap, _ := input["args"].(map[string]any)
			filled, err := ensureRequiredWorkflowParams(ctx, name, w.params, argsMap)
			if err != nil {
				return agent.ToolResult{}, err
			}
			if filled != nil {
				input["args"] = filled
			}
		}
	}

	// Prefer the ctx-scoped SubAgentManager (server/IM per-turn/per-session) so a
	// workflow spawned by one session doesn't accidentally use another session's
	// global Spawner under concurrency. Fall back to the process-global Spawner
	// for CLI/TUI paths that never stamp a ctx manager.
	var spawner Spawner
	if mgr := resolveSubAgentManager(ctx, nil); mgr != nil {
		spawner = mgr.Spawner()
	}
	if spawner == nil {
		spawner = ActiveSpawner()
	}
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
			Isolation:   opts.Isolation,
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

	// skill() dispatches by name to a recorded browser skill (deterministic
	// replay) or a SKILL.md skill (a sub-agent via the same spawner).
	sf := func(c context.Context, name, paramsJSON, schema string) workflow.AgentResult {
		return dispatchWorkflowSkill(c, spawner, name, paramsJSON, schema)
	}

	argsJSON, err := encodeWorkflowArgs(input["args"])
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: args must be a JSON value: %w", err)
	}

	mgr := resolveWorkflowManager(ctx)
	req := WorkflowRunRequest{
		Description:   description,
		Script:        script,
		Args:          argsJSON,
		Agent:         af,
		Skill:         sf,
		MaxConcurrent: defaultWorkflowConcurrency,
		ResumeFrom:    stringArg(input, "resume_from"),
		Foreground:    workflowForeground.Load(),
		// Carried into Start's detached context so the script's own
		// agent()/skill() calls (and anything nested they do, like
		// workflow_save) still resolve against this turn's directory instead
		// of falling back to the server's own launch directory once the run
		// is no longer tied to this request's ctx.
		WorkingDir: WorkingDirOrCWD(ctx),
	}

	runID, err := mgr.Start(req)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: %w", err)
	}

	// Foreground (one-shot) mode: run to completion inside this call and hand
	// the result back directly. Going through the manager keeps the run visible
	// to progress hooks and workflow_status, and enforces the concurrency cap.
	if req.Foreground {
		snap, werr := mgr.Wait(ctx, runID)
		if werr != nil {
			return agent.ToolResult{}, fmt.Errorf("workflow: run interrupted: %w", werr)
		}
		return agent.ToolResult{Text: formatRunDetail(snap)}, nil
	}

	return agent.ToolResult{Text: fmt.Sprintf(
		"Workflow started in the background as %s.\n"+
			"<system-reminder>This run executes in the background. DO NOT poll workflow_status "+
			"while it runs — the system will automatically notify you when it finishes, carrying "+
			"the result. While it runs, you may continue with other independent tasks. If you have "+
			"no other task to do, report the launch to the user and stop — do not spin in a "+
			"polling loop. (workflow_status(%q) exists for on-demand progress checks, e.g. when "+
			"the user asks.)</system-reminder>", runID, runID)}, nil
}

// encodeWorkflowArgs serializes the tool's `args` input to the JSON string the
// workflow runtime serves to the script's args primitive. A nil/absent value
// yields "" (the script's args returns nil).
func encodeWorkflowArgs(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WorkflowStatusTool reports on background workflow runs: a list with no
// argument, or one run's full status + result when given a run id.
type WorkflowStatusTool struct{}

func (WorkflowStatusTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "workflow_status",
		Description: "Check background workflow runs started with the workflow tool. " +
			"With no run_id: list this session's runs and their status (running/done/error). " +
			"With a run_id: the full result (or error + how to fix/resume) plus the captured log. " +
			"Use this for ON-DEMAND checks (e.g. the user asks how a run is going) or to collect " +
			"a result after the completion notification — do NOT call it in a polling loop; the " +
			"system pushes a notification when a run finishes. Repeated still-running reads of " +
			"the same run are detected as polling and trigger a hard STOP reminder.",
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

// workflowPollStopText is the hard stop appended once a no-progress polling
// streak crosses workflowPollStopThreshold. The agent loop exempts
// workflow_status from its duplicate-tool-call detector (it is a legitimate
// observation tool), so this guard is what breaks a polling loop — mirroring
// terminal_output's empty-snapshot guard.
const workflowPollStopText = "\n\n[STOP: repeated workflow_status polling detected. " +
	"Do not poll again. The system will push a notification with the result " +
	"when a run finishes.]"

func (WorkflowStatusTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	mgr := resolveWorkflowManager(ctx)
	runID := strings.TrimSpace(stringArg(input, "run_id"))
	if runID == "" {
		runs := mgr.List()
		if len(runs) == 0 {
			return agent.ToolResult{Text: "No background workflows have been started in this session."}, nil
		}
		lines := make([]string, 0, len(runs))
		var latest time.Time
		anyRunning := false
		for _, r := range runs {
			lines = append(lines, statusLine(r))
			if r.Status == "running" {
				anyRunning = true
				if r.LastActivity.After(latest) {
					latest = r.LastActivity
				}
			}
		}
		text := strings.Join(lines, "\n")
		// The list form observes running work too; give it the same anti-poll
		// escalation, keyed under "" off the freshest activity across running runs.
		if mgr.RecordStatusRead("", anyRunning, latest) >= workflowPollStopThreshold {
			text += workflowPollStopText
		}
		return agent.ToolResult{Text: text}, nil
	}
	snap, ok := mgr.Read(runID)
	if !ok {
		return agent.ToolResult{}, fmt.Errorf("workflow_status: no run named %q in this session", runID)
	}
	text := formatRunDetail(snap)
	if mgr.RecordStatusRead(runID, snap.Status == "running", snap.LastActivity) >= workflowPollStopThreshold {
		text += workflowPollStopText
	}
	return agent.ToolResult{Text: text}, nil
}

// WorkflowKillTool cancels a running background workflow by id — for a run that
// has stalled (workflow_status shows a large, growing "last activity" gap) or
// is no longer wanted.
type WorkflowKillTool struct{}

func (WorkflowKillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "workflow_kill",
		Description: "Cancel a running background workflow by run id (e.g. \"wf_1\"). Use when " +
			"workflow_status shows a run is stuck (a large, growing 'last activity' gap) or you " +
			"no longer want its result. Cancellation propagates to the workflow's in-flight " +
			"sub-agents. A run that already finished is left as-is.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"run_id": map[string]any{
					"type":        "string",
					"description": "The run id to cancel (from the workflow tool / workflow_status).",
				},
			},
			"required": []string{"run_id"},
		},
	}
}

func (WorkflowKillTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	runID := strings.TrimSpace(stringArg(input, "run_id"))
	if runID == "" {
		return agent.ToolResult{}, fmt.Errorf("workflow_kill: run_id is required")
	}
	mgr := resolveWorkflowManager(ctx)
	found, wasRunning := mgr.Kill(runID)
	switch {
	case !found:
		return agent.ToolResult{}, fmt.Errorf("workflow_kill: no run named %q in this session", runID)
	case !wasRunning:
		return agent.ToolResult{Text: fmt.Sprintf("Workflow %s had already finished — nothing to cancel.", runID)}, nil
	default:
		return agent.ToolResult{Text: fmt.Sprintf("Cancelled workflow %s. It will report as killed shortly; "+
			"workflow_status(%q) confirms.", runID, runID)}, nil
	}
}
