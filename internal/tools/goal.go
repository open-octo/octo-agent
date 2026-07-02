package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
)

// GoalStore is the slice of agent.Session the goal tools need. The session
// owns the durable goal record and all its invariants; the tools are thin
// wrappers exposing read/create/finish to the model while keeping usage
// accounting and pause/resume system- and user-owned.
type GoalStore interface {
	GoalSnapshot() (agent.Goal, bool)
	CreateGoal(objective string, tokenBudget int64) (agent.Goal, error)
	SetGoalStatus(status agent.GoalStatus) (agent.Goal, error)
}

// activeGoalStore is the process-global store (interactive CLI/TUI: one
// session per process). Request/response transports stamp a per-turn store
// on the ctx instead.
var activeGoalStore GoalStore

// goalsAvailable advertises the goal tools without a global store — the
// server sets it once at start (every turn carries a ctx-scoped store).
var goalsAvailable bool

// SetGoalStore registers the session the goal tools delegate to. Pass nil to
// disable; the three tools then drop out of DefaultTools (unless
// SetGoalsEnabled keeps them advertised for ctx-scoped dispatch).
func SetGoalStore(s GoalStore) { activeGoalStore = s }

// SetGoalsEnabled advertises the goal tools for transports that provide the
// store per turn via WithGoalStore (the server). The config kill switch
// (goal.enabled: false) simply skips this and SetGoalStore.
func SetGoalsEnabled(on bool) { goalsAvailable = on }

func goalsEnabled() bool { return activeGoalStore != nil || goalsAvailable }

type goalStoreCtxKeyType struct{}

var goalStoreCtxKey = goalStoreCtxKeyType{}

// WithGoalStore returns ctx carrying the per-turn goal store (the session).
func WithGoalStore(ctx context.Context, s GoalStore) context.Context {
	return context.WithValue(ctx, goalStoreCtxKey, s)
}

// resolveGoalStore picks the ctx-scoped store (server) first, then the
// process-global one (CLI/TUI). Nil when goals aren't wired for this turn.
func resolveGoalStore(ctx context.Context) GoalStore {
	if s, _ := ctx.Value(goalStoreCtxKey).(GoalStore); s != nil {
		return s
	}
	return activeGoalStore
}

// WithoutGoalTools removes the goal tool definitions from defs. For turn
// paths that advertise the shared catalog (SetGoalsEnabled) but do not wire
// a goal store — the IM bridge until its sessions carry goals — advertising
// a tool that can only error is worse than hiding it.
func WithoutGoalTools(defs []agent.ToolDefinition) []agent.ToolDefinition {
	out := defs[:0]
	for _, d := range defs {
		switch d.Name {
		case "get_goal", "create_goal", "update_goal":
			continue
		}
		out = append(out, d)
	}
	return out
}

// goalToolResponse is the JSON shape all three goal tools return. Remaining
// tokens and the completion report only appear when meaningful, mirroring
// the Codex tool contract this is ported from.
type goalToolResponse struct {
	Goal            *agent.Goal `json:"goal"`
	RemainingTokens *int64      `json:"remaining_tokens,omitempty"`
	// CompletionBudgetReport instructs the model to report final usage to
	// the user after completing a budgeted or long-running goal.
	CompletionBudgetReport string `json:"completion_budget_report,omitempty"`
}

func goalResult(g *agent.Goal, includeCompletionReport bool) (agent.ToolResult, error) {
	resp := goalToolResponse{Goal: g}
	if g != nil {
		if rem := g.RemainingTokens(); rem >= 0 {
			resp.RemainingTokens = &rem
		}
		if includeCompletionReport && g.Status == agent.GoalComplete && (g.TokenBudget > 0 || g.TimeUsedSeconds > 0) {
			resp.CompletionBudgetReport = "Goal achieved. Report final usage from this tool result's structured goal fields. " +
				"If `goal.token_budget` is present, include token usage from `goal.tokens_used` and `goal.token_budget`. " +
				"If `goal.time_used_seconds` is greater than 0, summarize elapsed time in a concise, human-friendly form " +
				"appropriate to the response language."
		}
	}
	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return agent.ToolResult{}, err
	}
	return agent.ToolResult{Text: string(b)}, nil
}

// ============================================================================
// get_goal
// ============================================================================

// GetGoalTool reads the session's current goal.
//
// Tool name: get_goal.
type GetGoalTool struct{}

func (GetGoalTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "get_goal",
		Description: "Get the current goal for this session, including status, budget, token and " +
			"elapsed-time usage, and remaining token budget.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (GetGoalTool) Execute(ctx context.Context, _ string, _ map[string]any) (agent.ToolResult, error) {
	store := resolveGoalStore(ctx)
	if store == nil {
		return agent.ToolResult{}, fmt.Errorf("goals are not available in this session")
	}
	if g, ok := store.GoalSnapshot(); ok {
		return goalResult(&g, false)
	}
	return goalResult(nil, false)
}

// ============================================================================
// create_goal
// ============================================================================

// CreateGoalTool starts a new active goal for the session.
//
// Tool name: create_goal.
type CreateGoalTool struct{}

func (CreateGoalTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "create_goal",
		Description: "Create a goal only when explicitly requested by the user or system/developer " +
			"instructions; do not infer goals from ordinary tasks. Set token_budget only when an " +
			"explicit token budget is requested. Fails if a goal exists; use update_goal only for status.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{
					"type": "string",
					"description": "Required. The concrete objective to start pursuing. This starts a new " +
						"active goal only when no goal is currently defined; if a goal already exists, this tool fails.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Optional positive token budget for the new active goal.",
				},
			},
			"required": []string{"objective"},
		},
	}
}

func (CreateGoalTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	store := resolveGoalStore(ctx)
	if store == nil {
		return agent.ToolResult{}, fmt.Errorf("goals are not available in this session")
	}
	objective, _ := input["objective"].(string)
	var budget int64
	if v, ok := input["token_budget"]; ok {
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return agent.ToolResult{}, fmt.Errorf("token_budget must be an integer")
		}
		budget = int64(f)
		if budget <= 0 {
			return agent.ToolResult{}, fmt.Errorf("token_budget must be positive when provided")
		}
	}
	g, err := store.CreateGoal(objective, budget)
	if err != nil {
		return agent.ToolResult{}, err
	}
	return goalResult(&g, false)
}

// ============================================================================
// update_goal
// ============================================================================

// UpdateGoalTool marks the existing goal complete or blocked — the only two
// status changes the model owns. Pause/resume belong to the user and the
// budget/usage limits to the system.
//
// Tool name: update_goal.
type UpdateGoalTool struct{}

func (UpdateGoalTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "update_goal",
		Description: "Update the existing goal.\n" +
			"Use this tool only to mark the goal achieved or genuinely blocked.\n" +
			"Set status to `complete` only when the objective has actually been achieved and no required work remains.\n" +
			"Set status to `blocked` only when the same blocking condition has repeated for at least three consecutive " +
			"goal turns, counting the original/user-triggered turn and any automatic continuations, and the agent cannot " +
			"make meaningful progress without user input or an external-state change.\n" +
			"If the user resumes a goal that was previously marked `blocked`, treat the resumed run as a fresh blocked audit. " +
			"If the same blocking condition then repeats for at least three consecutive resumed goal turns, set status to `blocked` again.\n" +
			"Once the blocked threshold is satisfied, do not keep reporting that you are still blocked while leaving the goal active; " +
			"set status to `blocked`.\n" +
			"Do not use `blocked` merely because the work is hard, slow, uncertain, incomplete, or would benefit from clarification.\n" +
			"Do not mark a goal complete merely because its budget is nearly exhausted or because you are stopping work.\n" +
			"You cannot use this tool to pause, resume, budget-limit, or usage-limit a goal; those status changes are controlled " +
			"by the user or system.\n" +
			"When marking a budgeted goal achieved with status `complete`, report the final token usage from the tool result to the user.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type": "string",
					"enum": []string{"complete", "blocked"},
					"description": "Required. Set to `complete` only when the objective is achieved and no required work " +
						"remains. Set to `blocked` only after the same blocking condition has recurred for at least three " +
						"consecutive goal turns and the agent is at an impasse. After a previously blocked goal is resumed, " +
						"the resumed run starts a fresh blocked audit.",
				},
			},
			"required": []string{"status"},
		},
	}
}

func (UpdateGoalTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	store := resolveGoalStore(ctx)
	if store == nil {
		return agent.ToolResult{}, fmt.Errorf("goals are not available in this session")
	}
	status, _ := input["status"].(string)
	switch agent.GoalStatus(status) {
	case agent.GoalComplete, agent.GoalBlocked:
	default:
		return agent.ToolResult{}, fmt.Errorf("status must be \"complete\" or \"blocked\"")
	}
	g, err := store.SetGoalStatus(agent.GoalStatus(status))
	if err != nil {
		return agent.ToolResult{}, err
	}
	return goalResult(&g, true)
}
