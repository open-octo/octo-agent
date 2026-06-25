package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// toolRound appends an assistant tool_use + its user tool_result to h.
func toolRound(h *History, id, tool, result string) {
	h.Append(Message{Role: RoleAssistant, Blocks: []ContentBlock{NewToolUseBlock(id, tool, map[string]any{"path": "x"})}})
	h.Append(Message{Role: RoleUser, Blocks: []ContentBlock{NewToolResultBlock(id, result, false)}})
}

func toolResults(msgs []Message) []ContentBlock {
	var out []ContentBlock
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" {
				out = append(out, b)
			}
		}
	}
	return out
}

func TestReclaimStaleToolResults(t *testing.T) {
	a := New(&summarizeFake{}, "m")
	big := strings.Repeat("y", 5000)
	const small = "ok"

	a.History.Append(NewUserMessage("start"))
	ids := make([]string, 10)
	for i := 0; i < 10; i++ {
		ids[i] = fmt.Sprintf("c%d", i)
		content := big
		if i == 0 {
			content = small // oldest is small → must NOT be elided
		}
		toolRound(a.History, ids[i], "read_file", content)
	}

	reclaimed := a.reclaimStaleToolResults()
	if reclaimed <= 0 {
		t.Fatalf("expected reclaim > 0, got %d", reclaimed)
	}

	got := toolResults(a.History.Snapshot())
	if len(got) != 10 {
		t.Fatalf("tool_result count = %d, want 10", len(got))
	}
	// Candidates are the oldest 4 (10 − hotToolResults=6). Index 0 is small and
	// must survive; 1..3 are big and must be elided. 4..9 are the hot tail.
	if got[0].Result != small {
		t.Errorf("small old result must be untouched, got %q", got[0].Result)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(got[i].Result, "elided") || !strings.Contains(got[i].Result, "read_file") {
			t.Errorf("result %d should be an elided read_file placeholder, got %q", i, got[i].Result)
		}
		if got[i].ToolUseID != ids[i] {
			t.Errorf("result %d lost its ToolUseID: %q != %q", i, got[i].ToolUseID, ids[i])
		}
	}
	for i := 4; i < 10; i++ {
		if got[i].Result != big {
			t.Errorf("hot-tail result %d must be verbatim", i)
		}
	}
}

func TestReclaimStaleToolResults_NoopWhenFew(t *testing.T) {
	a := New(&summarizeFake{}, "m")
	big := strings.Repeat("y", 5000)
	for i := 0; i < hotToolResults; i++ { // exactly the hot-tail count → nothing stale
		toolRound(a.History, fmt.Sprintf("c%d", i), "read_file", big)
	}
	if got := a.reclaimStaleToolResults(); got != 0 {
		t.Errorf("reclaim with ≤hot results should be a no-op, got %d", got)
	}
}

// When stale-tool-result reclamation alone drops the context under the trigger,
// maybeCompact must skip the summarize call and emit a reclaim-only done event.
func TestMaybeCompact_ReclaimOnlySkipsSummarize(t *testing.T) {
	f := &summarizeFake{summary: "S"}
	a := New(f, "m")
	a.CompactThreshold = 10_000 // explicit trigger above the hot-tail size

	big := strings.Repeat("y", 5000) // ~1250 tokens each
	a.History.Append(NewUserMessage("start"))
	for i := 0; i < 12; i++ {
		toolRound(a.History, fmt.Sprintf("c%d", i), "read_file", big)
	}

	var events []AgentEvent
	if err := a.maybeCompact(context.Background(), func(ev AgentEvent) { events = append(events, ev) }); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Errorf("reclaim-only path must not summarize; calls=%d", f.calls)
	}
	var done *CompactStats
	for _, ev := range events {
		if ev.Kind == EventCompactDone {
			done = ev.Compact
		}
	}
	if done == nil {
		t.Fatal("expected a compact_done event")
	}
	if done.ReclaimedTokens <= 0 || done.FoldedMsgs != 0 {
		t.Errorf("done should be reclaim-only: reclaimed=%d folded=%d", done.ReclaimedTokens, done.FoldedMsgs)
	}
	if done.AfterTokens >= done.BeforeTokens {
		t.Errorf("reclamation should reduce tokens: %d → %d", done.BeforeTokens, done.AfterTokens)
	}
}
