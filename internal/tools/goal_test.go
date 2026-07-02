package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func goalToolDefs(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, d := range DefaultTools() {
		names[d.Name] = true
	}
	return names
}

func TestGoalTools_GatedByRegistration(t *testing.T) {
	names := goalToolDefs(t)
	if names["get_goal"] || names["create_goal"] || names["update_goal"] {
		t.Fatal("goal tools must be hidden when neither store nor flag is registered")
	}

	SetGoalStore(agent.NewSession("m", ""))
	t.Cleanup(func() { SetGoalStore(nil) })
	names = goalToolDefs(t)
	if !names["get_goal"] || !names["create_goal"] || !names["update_goal"] {
		t.Error("goal tools must be advertised when a store is registered")
	}
	SetGoalStore(nil)

	SetGoalsEnabled(true)
	t.Cleanup(func() { SetGoalsEnabled(false) })
	if names = goalToolDefs(t); !names["get_goal"] {
		t.Error("goal tools must be advertised when the server flag is set (ctx-scoped stores)")
	}
}

func TestGoalTools_EndToEnd(t *testing.T) {
	sess := agent.NewSession("m", "")
	ctx := WithGoalStore(context.Background(), sess)

	// get_goal with no goal → null goal.
	res, err := (GetGoalTool{}).Execute(ctx, "get_goal", nil)
	if err != nil {
		t.Fatalf("get_goal: %v", err)
	}
	var resp struct {
		Goal            *agent.Goal `json:"goal"`
		RemainingTokens *int64      `json:"remaining_tokens"`
	}
	if err := json.Unmarshal([]byte(res.Text), &resp); err != nil {
		t.Fatalf("get_goal result not JSON: %v", err)
	}
	if resp.Goal != nil {
		t.Error("no goal should serialize as null")
	}

	// create_goal with a budget.
	res, err = (CreateGoalTool{}).Execute(ctx, "create_goal", map[string]any{
		"objective":    "ship the release",
		"token_budget": float64(50000),
	})
	if err != nil {
		t.Fatalf("create_goal: %v", err)
	}
	if err := json.Unmarshal([]byte(res.Text), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Goal == nil || resp.Goal.Status != agent.GoalActive || resp.Goal.TokenBudget != 50000 {
		t.Errorf("created goal = %+v", resp.Goal)
	}
	if resp.RemainingTokens == nil || *resp.RemainingTokens != 50000 {
		t.Errorf("remaining_tokens = %v, want 50000", resp.RemainingTokens)
	}

	// A second create fails (goal exists).
	if _, err := (CreateGoalTool{}).Execute(ctx, "create_goal", map[string]any{"objective": "another"}); err == nil {
		t.Error("create_goal over an existing goal must fail")
	}

	// update_goal only accepts complete|blocked.
	if _, err := (UpdateGoalTool{}).Execute(ctx, "update_goal", map[string]any{"status": "paused"}); err == nil {
		t.Error("update_goal must reject statuses the model does not own")
	}
	res, err = (UpdateGoalTool{}).Execute(ctx, "update_goal", map[string]any{"status": "complete"})
	if err != nil {
		t.Fatalf("update_goal complete: %v", err)
	}
	if !strings.Contains(res.Text, "completion_budget_report") {
		t.Error("completing a budgeted goal must include the completion report instruction")
	}
	if g, _ := sess.GoalSnapshot(); g.Status != agent.GoalComplete {
		t.Errorf("session goal status = %q, want complete", g.Status)
	}
}

func TestGoalTools_InputValidation(t *testing.T) {
	ctx := WithGoalStore(context.Background(), agent.NewSession("m", ""))

	if _, err := (CreateGoalTool{}).Execute(ctx, "create_goal", map[string]any{"objective": "x", "token_budget": float64(-5)}); err == nil {
		t.Error("negative budget must fail")
	}
	if _, err := (CreateGoalTool{}).Execute(ctx, "create_goal", map[string]any{"objective": "x", "token_budget": float64(1.5)}); err == nil {
		t.Error("fractional budget must fail")
	}
	if _, err := (CreateGoalTool{}).Execute(ctx, "create_goal", map[string]any{"objective": "   "}); err == nil {
		t.Error("blank objective must fail")
	}
	if _, err := (GetGoalTool{}).Execute(context.Background(), "get_goal", nil); err == nil {
		t.Error("unwired goal tools must error cleanly")
	}
}

func TestGoalTools_CtxStoreOverridesGlobal(t *testing.T) {
	global := agent.NewSession("m", "")
	perTurn := agent.NewSession("m", "")
	SetGoalStore(global)
	t.Cleanup(func() { SetGoalStore(nil) })

	ctx := WithGoalStore(context.Background(), perTurn)
	if _, err := (CreateGoalTool{}).Execute(ctx, "create_goal", map[string]any{"objective": "per-turn goal"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := global.GoalSnapshot(); ok {
		t.Error("ctx-scoped store must win over the global")
	}
	if g, ok := perTurn.GoalSnapshot(); !ok || g.Objective != "per-turn goal" {
		t.Errorf("per-turn store missed the goal: %+v", g)
	}
}
