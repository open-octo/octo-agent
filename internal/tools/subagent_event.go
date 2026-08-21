package tools

import "context"

// SubAgentEvent is a runtime progress event from a sub-agent, forwarded to a
// SubAgentManager's onEvent hook for live display. It is distinct from
// SubAgentNotification, which is the one-shot completion record: events stream
// while the sub-agent works, the notification fires once when it finishes.
//
// Only block-level activity is carried — never per-token text — so a live
// panel can show each sub-agent's full trail (tools with their outputs, the
// interim text replies, the final result) without the event volume of N
// concurrent sub-agents streaming their prose token by token. Every free-text
// payload is capped at emission (see the SubAgentEvent*Cap constants).
type SubAgentEvent struct {
	AgentID     string // manager handle, e.g. "agent_1"
	Description string // human-readable label from sub_agent
	AgentType   string // subagent_type, e.g. "explore" (empty for workflow-spawned agents)
	// Kind is one of:
	//   "started"    — the sub-agent began a task (or a Continue round)
	//   "tool"       — it dispatched a tool (ToolID/ToolName/ToolInput set)
	//   "tool_done"  — a tool finished (ToolID/ToolName set; ToolOutput carries
	//                  the capped result text)
	//   "tool_error" — a tool returned an error (ToolID/ToolName set; ToolOutput
	//                  carries the capped error text plus any partial output)
	//   "text"       — the sub-agent produced one assistant text block (Text set)
	//   "done"       — the round finished (sync return or async completion);
	//                  live panels drop the entry on this. Result carries the
	//                  capped final reply.
	Kind       string
	ToolID     string // pairs "tool" with its "tool_done"/"tool_error"
	ToolName   string
	ToolInput  map[string]any // optional: the tool's input arguments for UI display
	ToolOutput string         // "tool_done"/"tool_error": capped result / error text
	Text       string         // "text": one completed assistant text block, capped
	// StopReason is the agent's final stop reason on a "done" event (e.g.
	// "end_turn", "tool_use", "max_turns", "max_tokens", "promoted"). Empty or
	// sentinel values like "error" / "killed" indicate the agent exited
	// abnormally so the live panel can render it differently from a clean
	// completion.
	StopReason string
	Result     string // "done": the final reply, capped to SubAgentEventResultCap
}

// Payload caps applied at emission. They bound both the WS frame size and the
// per-agent retention buffer: with maxSubAgentEvents (200) events retained the
// worst case is ~1 MiB per agent. The full untruncated reply is still held by
// the manager (maxSubAgentResultBytes) and served through agent_status.
const (
	SubAgentEventOutputCap = 4 * 1024  // per tool_done/tool_error output
	SubAgentEventTextCap   = 8 * 1024  // per assistant text block
	SubAgentEventResultCap = 64 * 1024 // final reply on "done"
)

// ClipForEvent bounds a free-text event payload to max bytes, marking the cut.
// Byte-oriented like the other truncation helpers in this codebase; a rune
// split at the boundary is acceptable for display-only payloads.
func ClipForEvent(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…[truncated]"
}

type subAgentSinkKey struct{}

// WithSubAgentEventSink returns a context carrying sink, which the execution
// layer (the agentSpawner) pulls out to forward a child's runtime events. The
// SubAgentManager stamps this in before calling Spawn/Continue, so the Spawner
// interface itself stays unchanged and the non-TUI path (no sink) emits
// nothing.
//
// Note: the SubAgentManager always stamps a sink for a tracked agent, even if
// no live onEvent hook is set, so events can be retained for late-joining
// subscribers.
func WithSubAgentEventSink(ctx context.Context, sink func(SubAgentEvent)) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, subAgentSinkKey{}, sink)
}

// SubAgentEventSink returns the sink stamped by WithSubAgentEventSink, or nil
// when none is set (headless or tests where no manager is involved).
func SubAgentEventSink(ctx context.Context) func(SubAgentEvent) {
	sink, _ := ctx.Value(subAgentSinkKey{}).(func(SubAgentEvent))
	return sink
}
