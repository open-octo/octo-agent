package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/tools"
)

// seqSender returns a different canned reply on each successive call, so a test
// can drive the schema retry (invalid JSON first, valid JSON on the retry).
type seqSender struct {
	replies []string
	calls   int32
}

func (s *seqSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	i := int(atomic.AddInt32(&s.calls, 1)) - 1
	if i >= len(s.replies) {
		i = len(s.replies) - 1
	}
	return agent.Reply{Content: s.replies[i]}, nil
}

func TestExtractJSON(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare", `{"a":1}`, `{"a":1}`},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced-plain", "```\n[1,2,3]\n```", `[1,2,3]`},
		{"prose-wrapped", `Here you go: {"a":1} — hope it helps`, `{"a":1}`},
		{"array", `  [ {"x":1} ]  `, `[ {"x":1} ]`},
		{"no-json", `sorry, I cannot`, `sorry, I cannot`},
	}
	for _, c := range cases {
		if got := extractJSON(c.in); got != c.want {
			t.Errorf("%s: extractJSON(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestAgentSpawner_SchemaInjectsInstruction verifies the JSON-only instruction
// (and the schema text) reach the child's system prompt.
func TestAgentSpawner_SchemaInjectsInstruction(t *testing.T) {
	send := &subAgentSender{reply: `{"ok":true}`}
	parent := agent.New(send, "parent-model")
	sp := NewSpawner(parent, nilExecutor{}, func() []agent.ToolDefinition { return nil })

	_, err := sp.Spawn(context.Background(), tools.SpawnRequest{
		Prompt: "extract the fields",
		Schema: `{"type":"object","properties":{"ok":{"type":"boolean"}}}`,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(send.lastSystem, "ONLY a single valid JSON") {
		t.Error("child system prompt missing the JSON-only instruction")
	}
	if !strings.Contains(send.lastSystem, `"properties"`) {
		t.Error("child system prompt missing the schema text")
	}
}

// TestAgentSpawner_SchemaRetriesOnInvalidJSON verifies an invalid first reply
// triggers exactly one corrective re-prompt and the valid retry is returned.
func TestAgentSpawner_SchemaRetriesOnInvalidJSON(t *testing.T) {
	send := &seqSender{replies: []string{
		"Sure! ```json\n{ not valid \n```", // first: unparseable
		`{"ok":true}`,                      // retry: valid
	}}
	parent := agent.New(send, "parent-model")
	sp := NewSpawner(parent, nilExecutor{}, func() []agent.ToolDefinition { return nil })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{
		Prompt: "do it",
		Schema: `{"type":"object"}`,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := atomic.LoadInt32(&send.calls); got != 2 {
		t.Errorf("sender called %d times, want 2 (one retry)", got)
	}
	if res.Reply != `{"ok":true}` {
		t.Errorf("Reply = %q, want the valid retry JSON", res.Reply)
	}
}

// TestAgentSpawner_SchemaCleansFencesNoRetry verifies a fenced-but-valid first
// reply is cleaned without a retry.
func TestAgentSpawner_SchemaCleansFencesNoRetry(t *testing.T) {
	send := &subAgentSender{reply: "```json\n{\"a\":1}\n```"}
	parent := agent.New(send, "parent-model")
	sp := NewSpawner(parent, nilExecutor{}, func() []agent.ToolDefinition { return nil })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Prompt: "x", Schema: `{}`})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := atomic.LoadInt32(&send.calls); got != 1 {
		t.Errorf("sender called %d times, want 1 (no retry for valid fenced JSON)", got)
	}
	if res.Reply != `{"a":1}` {
		t.Errorf("Reply = %q, want fence-stripped {\"a\":1}", res.Reply)
	}
}
