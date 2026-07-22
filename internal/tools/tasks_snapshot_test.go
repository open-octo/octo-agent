package tools

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/tasks"
)

func TestAllTasksComplete(t *testing.T) {
	if AllTasksComplete(nil) {
		t.Error("nil store should be false")
	}

	s := tasks.New()
	if AllTasksComplete(s) {
		t.Error("empty store should be false (nothing to close)")
	}

	id1, _ := s.Create("a", "", "")
	id2, _ := s.Create("b", "", "")
	if AllTasksComplete(s) {
		t.Error("pending tasks should be false")
	}

	done := tasks.Completed
	if _, err := s.Update(id1, tasks.UpdateField{Status: &done}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if AllTasksComplete(s) {
		t.Error("one completed, one pending should be false")
	}

	if _, err := s.Update(id2, tasks.UpdateField{Status: &done}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !AllTasksComplete(s) {
		t.Error("all completed should be true")
	}

	// A deleted task is ignored; the rest are still all completed.
	id3, _ := s.Create("c", "", "")
	del := tasks.Deleted
	if _, err := s.Update(id3, tasks.UpdateField{Status: &del}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !AllTasksComplete(s) {
		t.Error("deleted task ignored, rest completed → true")
	}
}

func TestTaskSnapshot(t *testing.T) {
	if TaskSnapshot(nil) != nil {
		t.Error("nil store → nil")
	}
	s := tasks.New()
	if TaskSnapshot(s) != nil {
		t.Error("empty store → nil")
	}
	if _, err := s.Create("x", "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	snap := TaskSnapshot(s)
	if len(snap) != 1 || snap[0]["content"] != "x" || snap[0]["status"] != "pending" {
		t.Errorf("snapshot = %v, want one {x, pending}", snap)
	}
}
