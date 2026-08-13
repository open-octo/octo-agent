package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/tasks"
)

// mkTaskStore builds a store whose tasks land in the given statuses, in order.
func mkTaskStore(t *testing.T, statuses ...tasks.Status) *tasks.Store {
	t.Helper()
	s := tasks.New()
	for i, st := range statuses {
		id, err := s.Create("task "+string(rune('A'+i)), "", "")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if st == tasks.Pending {
			continue
		}
		if _, err := s.Update(id, tasks.UpdateField{Status: &st}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	return s
}

func TestPendingTaskReminder(t *testing.T) {
	cases := []struct {
		name     string
		statuses []tasks.Status
		want     bool
	}{
		{"the reported bug: last step left in_progress", []tasks.Status{tasks.Completed, tasks.InProgress}, true},
		{"plan fully closed", []tasks.Status{tasks.Completed, tasks.Completed}, false},
		{"mid-plan: work still queued behind the active task", []tasks.Status{tasks.InProgress, tasks.Pending}, false},
		{"nothing started yet", []tasks.Status{tasks.Pending, tasks.Pending}, false},
		{"no plan at all", nil, false},
		{"a dropped task is not queued work", []tasks.Status{tasks.Deleted, tasks.InProgress}, true},
		{"several tasks left open", []tasks.Status{tasks.InProgress, tasks.InProgress}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pendingTaskReminder(mkTaskStore(t, tc.statuses...))
			if (got != "") != tc.want {
				t.Fatalf("reminder = %q, want fired=%v", got, tc.want)
			}
			if got == "" {
				return
			}
			if !strings.HasPrefix(got, "<system-reminder>") || !strings.HasSuffix(got, "</system-reminder>") {
				t.Errorf("reminder is not a system-reminder span: %q", got)
			}
			if !strings.Contains(got, "task_update") {
				t.Errorf("reminder doesn't name the tool to call: %q", got)
			}
		})
	}
}

// The in_progress tasks are named so the model doesn't have to call task_list
// to find out which ones the guard means.
func TestPendingTaskReminder_NamesTheOpenTasks(t *testing.T) {
	got := pendingTaskReminder(mkTaskStore(t, tasks.Completed, tasks.InProgress))
	if !strings.Contains(got, "#2 task B") {
		t.Errorf("reminder should name task #2: %q", got)
	}
	if strings.Contains(got, "task A") {
		t.Errorf("reminder should not list the completed task: %q", got)
	}
}

func TestPendingTaskReminder_NilStore(t *testing.T) {
	if got := pendingTaskReminder(nil); got != "" {
		t.Errorf("nil store = %q, want empty", got)
	}
}

// The exported entry point resolves the same per-turn store the task_* tools
// dispatch to, so the guard reads what they just wrote.
func TestPendingTaskReminder_ResolvesCtxStore(t *testing.T) {
	SetTaskStore(nil)
	t.Cleanup(func() { SetTaskStore(nil) })
	planTools := []string{"task_update"}
	if got := PendingTaskReminder(context.Background(), planTools); got != "" {
		t.Fatalf("unconfigured session = %q, want empty", got)
	}
	ctx := WithTaskStore(context.Background(), mkTaskStore(t, tasks.InProgress))
	if got := PendingTaskReminder(ctx, planTools); got == "" {
		t.Error("ctx-scoped store with an in_progress task should fire")
	}
}

// An unfinished plan survives across turns, and the model is allowed to answer
// "that task really isn't done" — so a turn that never touched the plan must
// not re-bill a round-trip for it, however many turns later it is.
func TestPendingTaskReminder_OnlyOnTurnsThatTouchedThePlan(t *testing.T) {
	SetTaskStore(nil)
	t.Cleanup(func() { SetTaskStore(nil) })
	ctx := WithTaskStore(context.Background(), mkTaskStore(t, tasks.InProgress))

	for _, tc := range []struct {
		name      string
		toolsUsed []string
		want      bool
	}{
		{"no tools at all", nil, false},
		{"unrelated work", []string{"read_file", "terminal"}, false},
		{"the plan was updated", []string{"terminal", "task_update"}, true},
		{"the plan was created", []string{"task_create"}, true},
		{"the plan was only read", []string{"task_list"}, true},
		{"a lookalike tool name", []string{"tasks_of_mine"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PendingTaskReminder(ctx, tc.toolsUsed) != ""; got != tc.want {
				t.Errorf("fired = %v, want %v", got, tc.want)
			}
		})
	}
}
