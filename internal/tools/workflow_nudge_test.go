package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/hooks"
)

// obs is a shorthand for one skill-tool call.
func skillCall(name string) (string, map[string]any) {
	return "skill", map[string]any{"name": name}
}

func TestWorkflowNudger_TwoDistinctSkillsNudgeOnce(t *testing.T) {
	n := NewWorkflowNudger()

	// First distinct skill — silent.
	if got := n.observe(skillCall("download-excels")); got != "" {
		t.Fatalf("first skill should be silent, got %q", got)
	}
	// Second distinct skill — nudge.
	got := n.observe(skillCall("merge-excels"))
	if !strings.Contains(got, "workflow_save") || !strings.Contains(got, "<system-reminder>") {
		t.Fatalf("second distinct skill should nudge, got %q", got)
	}
	// Third — no repeat this turn.
	if got := n.observe(skillCall("excels-to-ppt")); got != "" {
		t.Fatalf("nudge must fire at most once per turn, got %q", got)
	}

	// New turn re-arms.
	n.reset()
	if got := n.observe(skillCall("a")); got != "" {
		t.Fatalf("after reset, first skill silent, got %q", got)
	}
	if got := n.observe(skillCall("b")); got == "" {
		t.Fatalf("after reset, second distinct skill should nudge again")
	}
}

func TestWorkflowNudger_DedupsByName(t *testing.T) {
	n := NewWorkflowNudger()
	if got := n.observe(skillCall("same")); got != "" {
		t.Fatalf("first: %q", got)
	}
	// Same skill again is not a second distinct skill.
	if got := n.observe(skillCall("same")); got != "" {
		t.Fatalf("same-name repeat must not count as a second skill, got %q", got)
	}
}

func TestWorkflowNudger_BrowserReplayCounts(t *testing.T) {
	n := NewWorkflowNudger()
	// A plain browser action (not replay) doesn't count.
	if got := n.observe("browser", map[string]any{"action": "navigate", "url": "x"}); got != "" {
		t.Fatalf("browser navigate should not count, got %q", got)
	}
	// A recording replay counts as a skill.
	if got := n.observe("browser", map[string]any{"action": "replay", "name": "rec-1"}); got != "" {
		t.Fatalf("first (replay) should be silent, got %q", got)
	}
	// A second distinct skill (via the skill tool) trips the nudge — mixed sources.
	if got := n.observe(skillCall("merge")); got == "" {
		t.Fatalf("recording + skill should nudge (mixed sources)")
	}
}

func TestWorkflowNudger_RunSkillAliasCounts(t *testing.T) {
	n := NewWorkflowNudger()
	// The deprecated run_skill alias still counts as a skill run.
	n.observe("browser", map[string]any{"action": "run_skill", "name": "rec-1"})
	if got := n.observe(skillCall("merge")); got == "" {
		t.Fatalf("run_skill alias + skill should nudge")
	}
}

func TestWorkflowNudger_ExcludesWorkflowCreator(t *testing.T) {
	n := NewWorkflowNudger()
	if got := n.observe(skillCall("workflow-creator")); got != "" {
		t.Fatalf("workflow-creator itself must not count, got %q", got)
	}
	if got := n.observe(skillCall("real-skill")); got != "" {
		t.Fatalf("only one countable skill so far, should be silent, got %q", got)
	}
	if got := n.observe(skillCall("another")); got == "" {
		t.Fatalf("two countable skills should nudge")
	}
}

func TestWorkflowNudger_SuppressedByWorkflowCall(t *testing.T) {
	n := NewWorkflowNudger()
	// The model is already orchestrating with a workflow this turn.
	n.observe("workflow_save", map[string]any{"name": "x"})
	n.observe(skillCall("a"))
	if got := n.observe(skillCall("b")); got != "" {
		t.Fatalf("a workflow/workflow_save call this turn must suppress the nudge, got %q", got)
	}
	// Next turn, without a workflow call, it nudges again.
	n.reset()
	n.observe(skillCall("a"))
	if got := n.observe(skillCall("b")); got == "" {
		t.Fatalf("after reset (no workflow call) the nudge should fire")
	}
}

// TestWorkflowNudger_HookPath drives the real hook engine (RegisterHooks +
// Inject) rather than calling observe/reset directly, so a wiring mistake (wrong
// event, wrong Payload field, missing reset) would be caught.
func TestWorkflowNudger_HookPath(t *testing.T) {
	e := hooks.NewEngine(nil)
	NewWorkflowNudger().RegisterHooks(e)
	ctx := context.Background()
	post := func(name string) string {
		return e.Inject(ctx, hooks.Payload{
			Event:     hooks.EventPostToolUse,
			ToolName:  "skill",
			ToolInput: map[string]any{"name": name},
		})
	}

	if got := post("a"); got != "" {
		t.Fatalf("first skill via engine should be silent, got %q", got)
	}
	if got := post("b"); !strings.Contains(got, "workflow_save") {
		t.Fatalf("second distinct skill via engine should nudge, got %q", got)
	}
	if got := post("c"); got != "" {
		t.Fatalf("nudge must not repeat within a turn, got %q", got)
	}

	// A user turn boundary must re-arm the nudger through the engine.
	if got := e.Inject(ctx, hooks.Payload{Event: hooks.EventUserPromptSubmit, UserInput: "next"}); got != "" {
		t.Fatalf("UserPromptSubmit hook should contribute no text, got %q", got)
	}
	if got := post("a"); got != "" {
		t.Fatalf("after re-arm, first skill silent, got %q", got)
	}
	if got := post("b"); !strings.Contains(got, "workflow_save") {
		t.Fatalf("after re-arm, second distinct skill should nudge again, got %q", got)
	}
}

func TestWorkflowNudger_NonSkillToolsIgnored(t *testing.T) {
	n := NewWorkflowNudger()
	n.observe("terminal", map[string]any{"command": "ls"})
	n.observe("read_file", map[string]any{"path": "x"})
	if got := n.observe(skillCall("only-one")); got != "" {
		t.Fatalf("non-skill tools must not count toward the threshold, got %q", got)
	}
}
