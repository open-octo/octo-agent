package agent

import (
	"context"

	"github.com/Leihb/octo-agent/internal/hooks"
)

// hookGate composes a PreToolUse hook in front of the interactive permission
// gate. It runs the hook first; a block denies the call outright, an allow
// bypasses the inner gate entirely, and "no opinion" defers to the inner gate
// (interactive prompt / always-allow rules). This is the hook-first, bidirectional
// ordering: programmatic policy up front, human/rule gate as the fallback.
//
// Auto-allow bypassing the gate is why a project-level hooks.yml is gated behind
// trust-on-first-use — an untrusted repo must not silently approve a tool.
type hookGate struct {
	engine *hooks.Engine
	meta   hooks.Meta
	inner  PermissionGate // may be nil (no interactive gate configured)
}

func (g *hookGate) Check(ctx context.Context, name string, input map[string]any) (bool, string) {
	p := g.meta.Payload(hooks.EventPreToolUse)
	p.ToolName = name
	p.ToolInput = input
	switch dec := g.engine.PreToolUse(ctx, p); {
	case dec.Block:
		reason := dec.Reason
		if reason == "" {
			reason = "blocked by PreToolUse hook"
		}
		return false, reason
	case dec.Allow:
		return true, "" // programmatic approval: skip the interactive gate
	default:
		if g.inner == nil {
			return true, ""
		}
		return g.inner.Check(ctx, name, input)
	}
}

// effectiveGate returns the permission gate dispatchTools should use: the bare
// a.Gate when no PreToolUse hook is configured, or a hookGate wrapping it when
// one is. Keeping the composition here means dispatchTools stays a plain
// gate consumer.
func (a *Agent) effectiveGate() PermissionGate {
	if a.Hooks == nil || !a.Hooks.Configured(hooks.EventPreToolUse) {
		return a.Gate
	}
	m := a.HookMeta
	if m.Model == "" {
		m.Model = a.Model
	}
	return &hookGate{engine: a.Hooks, meta: m, inner: a.Gate}
}
