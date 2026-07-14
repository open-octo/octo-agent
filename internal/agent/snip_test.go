package agent

import (
	"context"
	"strings"
	"testing"
)

func TestSnip_NoFireWhenBelowThreshold(t *testing.T) {
	a := New(&summarizeFake{}, "test-model")
	a.CompactThreshold = 100000
	a.History.Append(NewUserMessage("hello"))
	a.History.Append(NewAssistantMessage("hi"))
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSnip_SkipsFirstMessage(t *testing.T) {
	a := New(&summarizeFake{}, "test-model")
	a.CompactThreshold = 2000
	a.History.Append(NewUserMessage("first turn"))
	a.History.Append(NewAssistantMessage("a0"))
	a.History.Append(NewUserMessage("drop"))
	a.History.Append(NewAssistantMessage("a1"))
	a.History.Append(NewUserMessage(strings.Repeat("x", 12000)))
	a.History.Append(NewAssistantMessage("a2"))
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range a.History.Snapshot() {
		if m.Role == RoleUser && strings.Contains(m.Content, "first turn") {
			return
		}
	}
	t.Error("first turn was dropped")
}

func TestSnip_SkipsErrorTurns(t *testing.T) {
	a := New(&summarizeFake{}, "test-model")
	a.CompactThreshold = 2000
	a.History.Append(NewUserMessage("safe"))
	a.History.Append(NewAssistantMessage("a0"))
	keepErr := Message{Role: RoleUser, Blocks: []ContentBlock{{Type: "tool_result", Result: "err", IsError: true}}}
	a.History.Append(keepErr)
	a.History.Append(NewAssistantMessage("a2"))
	a.History.Append(NewUserMessage("drop"))
	a.History.Append(NewAssistantMessage("a3"))
	a.History.Append(NewUserMessage(strings.Repeat("x", 12000)))
	a.History.Append(NewAssistantMessage("a4"))
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range a.History.Snapshot() {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" && b.IsError {
				return
			}
		}
	}
	t.Error("error turn was dropped")
}

func TestSnip_RespectsMinTriggerTokens(t *testing.T) {
	a := New(&summarizeFake{}, "test-model")
	a.CompactThreshold = 20
	a.History.Append(NewUserMessage("only"))
	a.History.Append(NewAssistantMessage("a1"))
	if a.snipBeforeSummarize(a.History.Snapshot(), 20, nil) {
		t.Error("snip returned true with no safe candidate")
	}
}

func TestSnip_EmitEvent(t *testing.T) {
	a := New(&summarizeFake{}, "test-model")
	a.CompactThreshold = 2000
	a.History.Append(NewUserMessage("m0"))
	a.History.Append(NewAssistantMessage("a0"))
	a.History.Append(NewUserMessage(strings.Repeat("y", 12000))) // ~3000 tokens
	a.History.Append(NewAssistantMessage("a1"))
	a.History.Append(NewUserMessage("m2"))
	a.History.Append(NewAssistantMessage("a2"))
	events := make([]EventKind, 0)
	handler := func(e AgentEvent) { events = append(events, e.Kind) }
	result := a.snipBeforeSummarize(a.History.Snapshot(), 2000, handler)
	if !result {
		t.Fatal("snip returned false")
	}
	for _, e := range events {
		if e == EventSnip {
			return
		}
	}
	t.Error("EventSnip not emitted")
}

func TestSnip_NoSafeCandidateWhenAllHaveErrors(t *testing.T) {
	a := New(&summarizeFake{}, "test-model")
	a.CompactThreshold = 2000
	a.History.Append(NewUserMessage("task definition"))
	a.History.Append(NewAssistantMessage("ok"))
	err1 := Message{Role: RoleUser, Blocks: []ContentBlock{{Type: "tool_result", Result: "fail", IsError: true}}}
	a.History.Append(err1)
	a.History.Append(NewAssistantMessage("a1"))
	err2 := Message{Role: RoleUser, Blocks: []ContentBlock{{Type: "tool_result", Result: "also fail", IsError: true}}}
	a.History.Append(err2)
	a.History.Append(NewAssistantMessage("a2"))
	a.History.Append(NewUserMessage(strings.Repeat("y", 12000)))
	a.History.Append(NewAssistantMessage("a3"))

	result := a.snipBeforeSummarize(a.History.Snapshot(), 2000, nil)
	if result {
		t.Error("snip returned true when all candidates had errors")
	}
}
