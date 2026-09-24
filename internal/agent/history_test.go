package agent

import (
	"strings"
	"sync"
	"testing"
)

func TestHistory_AppendAndSnapshot(t *testing.T) {
	h := NewHistory()
	if h.Len() != 0 {
		t.Fatalf("fresh history Len = %d, want 0", h.Len())
	}

	h.Append(NewUserMessage("hi"))
	h.Append(NewAssistantMessage("hello"))

	if h.Len() != 2 {
		t.Fatalf("Len after 2 appends = %d, want 2", h.Len())
	}

	snap := h.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(snap))
	}
	if snap[0].Role != RoleUser || snap[1].Role != RoleAssistant {
		t.Errorf("Snapshot roles = [%q, %q], want [user, assistant]", snap[0].Role, snap[1].Role)
	}

	// Snapshot must be a copy — mutating it does not change History.
	snap[0].Content = "MUTATED"
	if h.Snapshot()[0].Content == "MUTATED" {
		t.Errorf("Snapshot leaked mutability back into History")
	}
}

func TestHistory_Reset(t *testing.T) {
	h := NewHistory()
	h.Append(NewUserMessage("hi"))
	h.Reset()
	if h.Len() != 0 {
		t.Errorf("Len after Reset = %d, want 0", h.Len())
	}
}

func TestHistory_ConcurrentAppend(t *testing.T) {
	h := NewHistory()
	const writers = 50
	const perWriter = 20

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				h.Append(NewUserMessage("x"))
			}
		}()
	}
	wg.Wait()

	if h.Len() != writers*perWriter {
		t.Errorf("Len after concurrent appends = %d, want %d", h.Len(), writers*perWriter)
	}
}

// Loading what is already on disk is not a rewrite: the next save should
// append, not rewrite the whole file. ReplaceAll (compaction) is one.
func TestHistory_ReplaceWithPersistedIsNotARewrite(t *testing.T) {
	msgs := []Message{NewUserMessage("a"), NewAssistantMessage("b")}

	h := NewHistory()
	h.ReplaceAll(msgs)
	if !h.RewriteDirty() {
		t.Fatal("ReplaceAll should flag a rewrite")
	}
	h.ReplaceWithPersisted(msgs)
	if h.RewriteDirty() {
		t.Error("ReplaceWithPersisted should clear the rewrite flag")
	}
	if h.Len() != 2 {
		t.Errorf("Len = %d, want 2", h.Len())
	}
	msgs[0].Content = "changed"
	if h.Snapshot()[0].Content != "a" {
		t.Error("history must hold its own copy of the messages")
	}
}

func TestHistory_TokensSince(t *testing.T) {
	h := NewHistory()
	h.Append(NewUserMessage("old"))
	h.Append(NewUserMessage("MARK in content"))
	h.Append(NewAssistantMessage("after the mark"))
	h.Append(NewToolResultMessage([]ContentBlock{NewToolResultBlock("t", "MARK in a tool result", false)}))
	all := h.Snapshot()
	isMark := func(s string) bool { return strings.Contains(s, "MARK") }

	got, found := h.TokensSince(isMark)
	if !found {
		t.Fatal("the user message carrying the mark was not found")
	}
	if want := EstimateTokens(all[2:]); got != want {
		t.Errorf("tokens since = %d, want %d (the two messages after it; a tool result never matches)", got, want)
	}

	h.Append(Message{Role: RoleUser, Blocks: []ContentBlock{NewTextBlock("MARK in a text block")}})
	if got, found := h.TokensSince(isMark); !found || got != 0 {
		t.Errorf("mark in a text block of the newest message: got %d, %v; want 0, true", got, found)
	}

	none := func(string) bool { return false }
	if got, found := h.TokensSince(none); found || got != EstimateTokens(h.Snapshot()) {
		t.Errorf("no match: got %d, %v; want the whole history, false", got, found)
	}
}
