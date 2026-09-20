package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// Follow-up tools for sub-agents: send a message to a running/completed
// child, read its status, kill it. They complete the sub_agent surface —
// the SubAgentManager always had Send/ContinueSync/Read/Kill, but until
// these tools nothing exposed them to the model, so an async child could
// only be awaited, never steered or queried.
//
// Two ID namespaces converge here, matching what the model actually sees.
// Which one a given child lands in follows the transport's dispatch choice
// (see AgentTool), not anything the model asked for:
//   - backgrounded spawns return "agent_N" (manager-tracked) — Send delivers
//     asynchronously and the reply arrives as a notification;
//   - inline spawns tag their reply "[agent <id>]" (spawner-side) — those
//     continue synchronously and return the reply inline.
// sub_agent_send tries the manager first and falls back to a synchronous
// continue, so the model can use whichever ID it has.

// AgentSendTool delivers a follow-up message to an existing sub-agent.
type AgentSendTool struct{}

func (AgentSendTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "sub_agent_send",
		Description: "Send a follow-up message to an existing sub-agent — steer it, ask for more " +
			"detail, or continue its task with its context intact. Use whichever id that " +
			"sub-agent gave you — an agent_N handle, or the [agent …] tag on its reply. " +
			"The response reaches you the same way that sub-agent's first result did: " +
			"back in this call, or later as a completion notification.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "The sub-agent to message — its agent_N handle or the [agent …] tag id, whichever the sub_agent result gave you.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The follow-up instruction or question. The sub-agent keeps its previous context.",
				},
			},
			"required": []string{"agent_id", "message"},
		},
	}
}

func (AgentSendTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	if IsSubAgent(ctx) {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_send: a sub-agent cannot message other sub-agents")
	}
	mgr := resolveSubAgentManager(ctx, nil)
	if mgr == nil {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_send: sub-agent dispatch is not configured for this session")
	}
	id := strings.TrimSpace(stringArg(input, "agent_id"))
	msg := strings.TrimSpace(stringArg(input, "message"))
	if id == "" || msg == "" {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_send: agent_id and message are required")
	}

	err := mgr.Send(id, msg)
	switch {
	case err == nil:
		return agent.ToolResult{
			Text: fmt.Sprintf("Message delivered to %s (queued if it was busy). Its reply will arrive as a notification.", id),
		}, nil
	case strings.Contains(err.Error(), "no sub-agent"):
		// Not manager-tracked — treat the id as a spawner-side (sync) child
		// and continue it synchronously.
		res, cerr := mgr.ContinueSync(ctx, id, msg)
		if cerr != nil {
			// Retry the de-prefixed spelling, the same mangling
			// sub_agent_status tolerates. The first attempt found no child, so
			// nothing has run yet and the message can't be delivered twice.
			if bare := bareChildID(id); bare != "" {
				if res2, cerr2 := mgr.ContinueSync(ctx, bare, msg); cerr2 == nil {
					res, cerr = res2, nil
				}
			}
		}
		if cerr != nil {
			return agent.ToolResult{}, fmt.Errorf("sub_agent_send: unknown sub-agent %q (and synchronous continue failed: %v)", id, cerr)
		}
		text := withAgentTag(res.AgentID, res.Reply) + incompleteNote(res.StopReason, res.AgentID)
		return agent.ToolResult{Text: text}, nil
	default:
		// Exited / pending-message errors are real answers, not routing misses.
		return agent.ToolResult{}, fmt.Errorf("sub_agent_send: %w", err)
	}
}

// AgentStatusTool reports one sub-agent's state or lists the running set.
type AgentStatusTool struct{}

func (AgentStatusTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "sub_agent_status",
		Description: "Check on async sub-agents, but do not use this as a polling loop while waiting for a " +
			"background sub-agent to finish — wait for the completion notification instead. With agent_id, report " +
			"that sub-agent's state (working/idle/exited) and its latest result; without agent_id, list all tracked " +
			"sub-agents (working ones plus idle-but-resumable ones). Use this tool only when you suspect a sub-agent is stuck or when you need to know " +
			"which agents are still running. An [agent …] tag id from an inline reply works here too: it reports " +
			"that child's last round and whether it can still be resumed with sub_agent_send.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Optional sub-agent id — an agent_N handle or an [agent …] tag id. Omit to list everything reachable (working, or idle-but-resumable).",
				},
			},
		},
	}
}

func (AgentStatusTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	mgr := resolveSubAgentManager(ctx, nil)
	if mgr == nil {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_status: sub-agent dispatch is not configured for this session")
	}

	id := strings.TrimSpace(stringArg(input, "agent_id"))
	if id == "" {
		infos := mgr.ListRunning()
		// Synchronous sub-agents never appear in ListRunning — RunSync reaps
		// the manager entry when the blocking call returns — so ask the
		// spawner for the children that are still resumable, minus the async
		// ones already listed above under their agent_N handle.
		children := resumableChildren(mgr)
		if len(infos) == 0 && len(children) == 0 {
			return agent.ToolResult{Text: "No sub-agents are currently tracked."}, nil
		}
		if len(infos) == 0 {
			return agent.ToolResult{Text: renderResumableChildren(children)}, nil
		}
		// ListRunning includes COMPLETED agents (idle — retained so
		// sub_agent_send can still resume them), so don't call the whole list
		// "running": that misreads "resumable" as "still working".
		working := 0
		for _, in := range infos {
			if in.Busy {
				working++
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d sub-agent(s) tracked (%d working, %d idle — done, still reachable via sub_agent_send):\n",
			len(infos), working, len(infos)-working)
		for _, in := range infos {
			state := "idle"
			if in.Busy {
				state = "working"
			}
			fmt.Fprintf(&b, "- %s — %s (%s, started %s ago)\n",
				in.ID, in.Description, state, time.Since(in.Start).Round(time.Second))
		}
		if len(children) > 0 {
			b.WriteString("\n" + renderResumableChildren(children) + "\n")
		}
		return agent.ToolResult{Text: strings.TrimRight(b.String(), "\n")}, nil
	}

	result, status, found := mgr.Read(id)
	if !found {
		// Not manager-tracked. A synchronous sub-agent never is once its tool
		// call has returned, yet the child stays resumable in the spawner's
		// registry — so look there before calling the id unknown.
		if snap, ok := inspectChild(mgr, id); ok {
			return agent.ToolResult{Text: renderChildSnapshot(snap)}, nil
		}
		return agent.ToolResult{}, fmt.Errorf("sub_agent_status: unknown sub-agent %q — no async sub-agent has that id and no resumable child does either (an idle child is dropped after a while). Launch a fresh sub-agent instead.", id)
	}
	text := fmt.Sprintf("Sub-agent %s: %s", id, status)
	if result != "" {
		text += "\n\nLatest result:\n" + result
	} else {
		text += "\n\n(no result yet)"
	}
	return agent.ToolResult{Text: text}, nil
}

// childReplyCap bounds how much of a resumable child's last reply
// sub_agent_status echoes back. The full text already reached the parent as
// that sub_agent call's tool result; this is a reminder, not a re-delivery.
const childReplyCap = 4000

// bareChildID strips the "agent_" prefix a model tacks on when it reads a
// synchronous sub-agent's "[agent dbb7aa4b]" tag as the async "agent_N" form.
// Returns "" when there is nothing else to try. Spawner-side ids are 8 hex
// characters, so this can't alias a real agent_N handle — "agent_1" would
// retry as "1", which no child is ever called.
func bareChildID(id string) string {
	bare := strings.TrimPrefix(id, "agent_")
	if bare == id || bare == "" {
		return ""
	}
	return bare
}

// inspectChild looks id up in the spawner's live-child registry, retrying the
// de-prefixed form. Returns false when the spawner keeps no children or has
// none under that id.
func inspectChild(mgr *SubAgentManager, id string) (ChildSnapshot, bool) {
	insp, ok := mgr.Spawner().(ChildInspector)
	if !ok {
		return ChildSnapshot{}, false
	}
	if snap, found := insp.InspectChild(id); found {
		return snap, true
	}
	if bare := bareChildID(id); bare != "" {
		return insp.InspectChild(bare)
	}
	return ChildSnapshot{}, false
}

// resumableChildren lists the spawner's live children that the model can still
// send to: the ones the manager doesn't already report under an agent_N handle,
// and that aren't mid-round. A running child is excluded rather than listed as
// idle — the manager's own listing covers it while it runs, and a follow-up
// addressed to it would block until its round finishes.
func resumableChildren(mgr *SubAgentManager) []ChildSnapshot {
	insp, ok := mgr.Spawner().(ChildInspector)
	if !ok {
		return nil
	}
	tracked := mgr.TrackedBackingIDs()
	var out []ChildSnapshot
	for _, snap := range insp.ListChildren() {
		if !tracked[snap.ID] && !snap.Busy {
			out = append(out, snap)
		}
	}
	return out
}

func renderResumableChildren(children []ChildSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d finished sub-agent(s) not tracked as background agents, but still resumable via sub_agent_send:\n", len(children))
	for _, c := range children {
		outcome := stopReasonLabel(c.StopReason)
		if c.Err != "" {
			outcome = "last round failed: " + c.Err
		}
		fmt.Fprintf(&b, "- %s — %s, %d turn(s), last active %s ago\n",
			c.ID, outcome, c.Turns, c.Idle.Round(time.Second))
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderChildSnapshot reports a child that only the spawner knows about. It
// leads with the resume instruction because the common case for asking is a
// sub-agent that stopped short.
func renderChildSnapshot(s ChildSnapshot) string {
	var b strings.Builder
	if s.Busy {
		// Saying "resumable" here would invite a follow-up that just blocks
		// until the running round finishes.
		fmt.Fprintf(&b, "Sub-agent %s: working right now — wait for it to finish before sending a follow-up.", s.ID)
		fmt.Fprintf(&b, "\nStarted its current round; %d turn(s) recorded from earlier rounds.", s.Turns)
		return b.String()
	}
	fmt.Fprintf(&b, "Sub-agent %s: idle, still resumable — send it a follow-up with sub_agent_send using agent_id %q.",
		s.ID, s.ID)
	b.WriteString("\nIt is not tracked as a background agent because it already returned its result inline, but its context is intact for a follow-up.")
	if s.Err != "" {
		fmt.Fprintf(&b, "\nLast round failed after %d turn(s), %s ago: %s", s.Turns, s.Idle.Round(time.Second), s.Err)
		return b.String()
	}
	fmt.Fprintf(&b, "\nLast round: %s, %d turn(s), %s ago.", stopReasonLabel(s.StopReason), s.Turns, s.Idle.Round(time.Second))
	if s.Reply != "" {
		b.WriteString("\n\nLatest result:\n" + ClipForEvent(s.Reply, childReplyCap))
	}
	return b.String()
}

// stopReasonLabel renders a child's stop reason for the model, spelling out
// the two the agent loop synthesises — a bare "stuck" reads like an opinion
// rather than the loop detector's verdict.
func stopReasonLabel(reason string) string {
	switch reason {
	case "":
		return "no completed round yet"
	case agent.StopReasonStuck:
		return "stopped by the loop detector (repeated the same tool calls without progress)"
	case agent.StopReasonMaxTurns:
		return "hit its turn limit (partial work)"
	default:
		return "stopped on " + reason
	}
}

// AgentKillTool terminates an async sub-agent.
type AgentKillTool struct{}

func (AgentKillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "sub_agent_kill",
		Description: "Terminate a running async sub-agent (agent_N). Use when its task is no " +
			"longer needed or it's clearly stuck. The kill is immediate; partial results " +
			"already reported stay available via sub_agent_status.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "The async sub-agent id (agent_N) to terminate.",
				},
			},
			"required": []string{"agent_id"},
		},
	}
}

func (AgentKillTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	if IsSubAgent(ctx) {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_kill: a sub-agent cannot kill other sub-agents")
	}
	mgr := resolveSubAgentManager(ctx, nil)
	if mgr == nil {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_kill: sub-agent dispatch is not configured for this session")
	}
	id := strings.TrimSpace(stringArg(input, "agent_id"))
	if id == "" {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_kill: agent_id is required")
	}
	if !mgr.Kill(id) {
		return agent.ToolResult{}, fmt.Errorf("sub_agent_kill: unknown sub-agent %q", id)
	}
	return agent.ToolResult{Text: fmt.Sprintf("Killed sub-agent %s.", id)}, nil
}
