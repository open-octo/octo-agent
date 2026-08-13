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
		{"dropped tasks don't count as queued work", []tasks.Status{tasks.Deleted, tasks.InProgress}, true},
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
	if got := PendingTaskReminder(context.Background()); got != "" {
		t.Fatalf("unconfigured session = %q, want empty", got)
	}
	ctx := WithTaskStore(context.Background(), mkTaskStore(t, tasks.InProgress))
	if got := PendingTaskReminder(ctx); got == "" {
		t.Error("ctx-scoped store with an in_progress task should fire")
	}
}
