package agent

import (
	"context"
	"errors"
	"testing"
)

// TestFinishInterrupted_HistoryShapes covers the three history end-states the
// helper must normalize so the next turn keeps user/assistant alternation.
func TestFinishInterrupted_HistoryShapes(t *testing.T) {
	t.Run("unanswered user input is kept and capped with a note", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("hi"))
		_, err := a.finishInterrupted(nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		msgs := a.History.Snapshot()
		if len(msgs) != 2 {
			t.Fatalf("history len = %d, want 2 (input kept + interrupt note)", len(msgs))
		}
		if msgs[0].Role != RoleUser || msgs[0].Content != "hi" {
			t.Errorf("msg[0] = %+v, want the original user input", msgs[0])
		}
		if last := msgs[1]; last.Role != RoleAssistant || last.Content != interruptNote {
			t.Errorf("msg[1] = %+v, want assistant interrupt note", last)
		}
	})

	t.Run("trailing tool_result is capped with an assistant note", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("read it"))
		a.History.Append(NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "read_file", nil)}))
		a.History.Append(NewToolResultMessage([]ContentBlock{NewToolResultBlock("c1", "ctx canceled", true)}))
		_, err := a.finishInterrupted(nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		msgs := a.History.Snapshot()
		if len(msgs) != 4 {
			t.Fatalf("history len = %d, want 4", len(msgs))
		}
		if last := msgs[3]; last.Role != RoleAssistant || last.Content != interruptNote {
			t.Errorf("last msg = %+v, want assistant interrupt note", last)
		}
	})

	t.Run("history already ending on assistant is untouched", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("hi"))
		a.History.Append(NewAssistantMessage("hello"))
		_, _ = a.finishInterrupted(nil)
		if a.History.Len() != 2 {
			t.Errorf("history len = %d, want 2 (unchanged)", a.History.Len())
		}
	})
}

// cancelOnSendSender cancels the supplied context the first time it is asked to
// produce a reply, simulating Ctrl-C arriving during the provider call.
type cancelOnSendSender struct {
	cancel context.CancelFunc
}

func (s *cancelOnSendSender) SendMessages(ctx context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	s.cancel()
	return Reply{}, ctx.Err()
}

// TestRun_InterruptDuringFirstCall verifies an interrupt on the very first
// model call returns context.Canceled. Turn's error-path contract (pop the
// user message so a retry doesn't duplicate it) applies to every error,
// including cancellation, so history ends up empty here — unlike the
// Run/RunStream loop, where finishInterrupted keeps the input.
func TestRun_InterruptDuringFirstCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := New(&cancelOnSendSender{cancel: cancel}, "m")

	_, err := a.Turn(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if a.History.Len() != 0 {
		t.Errorf("history len = %d, want 0", a.History.Len())
	}
}

// TestRunStream_InterruptNoTools drives the RunStream no-tools fallback
// (turnStream with finishInterrupt=true): an interrupt during the provider
// call must keep the user input capped with the interrupt note — not pop it —
// and emit exactly one EventTurnDone.
func TestRunStream_InterruptNoTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := New(&cancelOnSendSender{cancel: cancel}, "m")

	var turnDones int
	_, err := a.RunStream(ctx, "hi", nil, nil, func(ev AgentEvent) {
		if ev.Kind == EventTurnDone {
			turnDones++
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	msgs := a.History.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("history len = %d, want 2 (input kept + interrupt note): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != RoleUser || msgs[0].Content != "hi" {
		t.Errorf("msg[0] = %+v, want the original user input", msgs[0])
	}
	if msgs[1].Role != RoleAssistant || msgs[1].Content != interruptNote {
		t.Errorf("msg[1] = %+v, want assistant interrupt note", msgs[1])
	}
	if turnDones != 1 {
		t.Errorf("EventTurnDone fired %d times, want exactly 1", turnDones)
	}
}

// TestRun_InterruptNoTools: Run's no-tools fallback must follow the same
// interrupt contract as its tool loop — input kept, capped with the note —
// so an interrupted Run ends identically with or without tools.
func TestRun_InterruptNoTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := New(&cancelOnSendSender{cancel: cancel}, "m")

	_, err := a.Run(ctx, "hi", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	msgs := a.History.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("history len = %d, want 2 (input kept + interrupt note): %+v", len(msgs), msgs)
	}
	if msgs[1].Role != RoleAssistant || msgs[1].Content != interruptNote {
		t.Errorf("msg[1] = %+v, want assistant interrupt note", msgs[1])
	}
}

// TestTakeBackInterrupted covers the take-back contract: the [user, note]
// pair left by a no-output interrupt is removed; any tail showing progress
// (tool_results under the note, or no note at all) is left untouched.
func TestTakeBackInterrupted(t *testing.T) {
	t.Run("strips the interrupted input and note", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("old q"))
		a.History.Append(NewAssistantMessage("old a"))
		a.History.Append(NewUserMessage("taken back"))
		a.History.Append(NewAssistantMessage(interruptNote))
		if !a.TakeBackInterrupted() {
			t.Fatal("TakeBackInterrupted() = false, want true")
		}
		msgs := a.History.Snapshot()
		if len(msgs) != 2 || msgs[1].Content != "old a" {
			t.Errorf("history = %+v, want the pre-turn [old q, old a]", msgs)
		}
	})

	t.Run("leaves a tool-round interrupt untouched", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("q"))
		a.History.Append(NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "do", nil)}))
		a.History.Append(NewToolResultMessage([]ContentBlock{NewToolResultBlock("c1", "canceled", true)}))
		a.History.Append(NewAssistantMessage(interruptNote))
		if a.TakeBackInterrupted() {
			t.Fatal("TakeBackInterrupted() = true, want false (turn made progress)")
		}
		if a.History.Len() != 4 {
			t.Errorf("history len = %d, want 4 (untouched)", a.History.Len())
		}
	})

	t.Run("leaves a normal completion untouched", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("q"))
		a.History.Append(NewAssistantMessage("real answer"))
		if a.TakeBackInterrupted() {
			t.Fatal("TakeBackInterrupted() = true, want false (no interrupt note)")
		}
		if a.History.Len() != 2 {
			t.Errorf("history len = %d, want 2 (untouched)", a.History.Len())
		}
	})
}

// toolThenCancelSender returns a tool_use on the first call; the executor
// cancels the context during dispatch, so the loop must still produce a
// well-formed history (assistant tool_use + tool_result + interrupt note).
type toolThenCancelSender struct{ calls int }

func (s *toolThenCancelSender) SendMessages(_ context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	return Reply{}, nil // unused (tool path goes through SendMessagesWithTools)
}

func (s *toolThenCancelSender) SendMessagesWithTools(_ context.Context, _, _ string, _ []Message, _ int, _ []ToolDefinition) (Reply, error) {
	s.calls++
	return Reply{
		StopReason: "tool_use",
		Blocks:     []ContentBlock{NewToolUseBlock("c1", "do", map[string]any{})},
	}, nil
}

type cancelOnExecExecutor struct{ cancel context.CancelFunc }

func (e cancelOnExecExecutor) Execute(ctx context.Context, _ string, _ map[string]any) (ToolResult, error) {
	e.cancel() // Ctrl-C arrives while the tool runs
	return ToolResult{}, ctx.Err()
}

func TestRun_InterruptDuringToolDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := New(&toolThenCancelSender{}, "m")
	tools := []ToolDefinition{{Name: "do"}}

	_, err := a.Run(ctx, "go", tools, cancelOnExecExecutor{cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// History must be well-formed: user, assistant(tool_use), user(tool_result),
	// assistant(interrupt note) — alternation preserved for the next turn.
	msgs := a.History.Snapshot()
	if len(msgs) != 4 {
		t.Fatalf("history len = %d, want 4: %+v", len(msgs), msgs)
	}
	wantRoles := []Role{RoleUser, RoleAssistant, RoleUser, RoleAssistant}
	for i, want := range wantRoles {
		if msgs[i].Role != want {
			t.Errorf("msg[%d].Role = %q, want %q", i, msgs[i].Role, want)
		}
	}
	if !hasToolResult(msgs[2]) {
		t.Errorf("msg[2] should carry the synthesized tool_result")
	}
	if msgs[3].Content != interruptNote {
		t.Errorf("msg[3] = %q, want interrupt note", msgs[3].Content)
	}
}
