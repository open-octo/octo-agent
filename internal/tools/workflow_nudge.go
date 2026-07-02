package tools

import (
	"context"
	"sync"

	"github.com/open-octo/octo-agent/internal/hooks"
)

// workflowNudgeText is appended to the tool result of the second distinct skill
// run by hand in one turn, nudging the model to offer capturing the sequence as
// a reusable workflow. Wrapped in <system-reminder> so the display layer strips
// it (same convention as the memory save-nudge).
const workflowNudgeText = "<system-reminder>\n" +
	"You've run more than one skill by hand this turn. If this is a repeatable flow, offer to capture it as a saved workflow — guide it with the workflow-creator skill, or write the script and persist it with workflow_save — so it can be rerun by name (and scheduled). If this was a one-off, ignore this.\n" +
	"</system-reminder>"

// WorkflowNudger suggests saving a reusable workflow once the model has manually
// chained >=2 distinct skills within a single turn (the passive complement to
// the workflow-creator skill). It mirrors memory.Injector's save-nudge but keys
// off skill composition instead of a milestone command. One per session; its
// latches re-arm on each user turn. A mutex guards the state so it is safe even
// when a transport dispatches hooks off the serial run loop.
type WorkflowNudger struct {
	mu         sync.Mutex
	seen       map[string]bool // distinct skill names invoked this turn
	suppressed bool            // a workflow / workflow_save already ran this turn
	nudged     bool            // nudge already emitted this turn
}

// NewWorkflowNudger returns a ready nudger.
func NewWorkflowNudger() *WorkflowNudger {
	return &WorkflowNudger{seen: map[string]bool{}}
}

// reset re-arms the nudger at the start of a user turn.
func (n *WorkflowNudger) reset() {
	n.mu.Lock()
	n.seen = map[string]bool{}
	n.suppressed = false
	n.nudged = false
	n.mu.Unlock()
}

// observe records one tool call and returns the nudge text when the second
// distinct skill of the turn lands (at most once per turn); "" otherwise.
func (n *WorkflowNudger) observe(toolName string, input map[string]any) string {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Already orchestrating with workflows this turn — don't nudge.
	if toolName == "workflow" || toolName == "workflow_save" {
		n.suppressed = true
		return ""
	}

	name := skillNameFromCall(toolName, input)
	// workflow-creator is how you'd *build* a workflow, so running it isn't
	// "manual chaining" worth nudging about.
	if name == "" || name == "workflow-creator" {
		return ""
	}
	n.seen[name] = true

	if n.suppressed || n.nudged || len(n.seen) < 2 {
		return ""
	}
	n.nudged = true
	return workflowNudgeText
}

// skillNameFromCall extracts the skill name from a skill-running tool call: the
// `skill` tool (loading a SKILL.md) or the browser tool's run_skill action
// (replaying a recording). Any other call yields "".
func skillNameFromCall(toolName string, input map[string]any) string {
	switch toolName {
	case "skill":
		// The skill tool loads a SKILL.md body — it doesn't by itself prove the
		// skill ran to completion (not observable from tool calls). Loading two
		// distinct skills is a good-enough "chaining" signal; the nudge is hedged
		// and one-shot, so an occasional false positive is acceptable.
		name, _ := input["name"].(string)
		return name
	case "browser":
		if action, _ := input["action"].(string); action == "run_skill" {
			name, _ := input["name"].(string)
			return name
		}
	}
	return ""
}

// RegisterHooks wires the nudger onto a session's hook engine: observe tool
// calls on PostToolUse, re-arm on UserPromptSubmit. Independent of memory — wire
// it whether or not a memory directory exists.
func (n *WorkflowNudger) RegisterHooks(e *hooks.Engine) {
	if n == nil || e == nil {
		return
	}
	e.RegisterInProc(hooks.EventPostToolUse, func(_ context.Context, p hooks.Payload) string {
		return n.observe(p.ToolName, p.ToolInput)
	})
	e.RegisterInProc(hooks.EventUserPromptSubmit, func(_ context.Context, _ hooks.Payload) string {
		n.reset()
		return ""
	})
}
