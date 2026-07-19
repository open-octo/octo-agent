package app

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/audit"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tools"
)

// PermissionAsk prompts the user to approve one tool call (already unwrapped to
// its real tool name). It returns whether to allow the call and whether to
// remember that decision for the rest of the session. A nil PermissionAsk makes
// the gate non-interactive: ask-class verdicts resolve straight to deny.
type PermissionAsk func(ctx context.Context, toolName string, toolInput map[string]any) (allow, remember bool, err error)

// NewPermissionGate builds the single agent.PermissionGate every transport
// uses. The engine resolves allow/deny/ask policy. Pass a non-nil ask for an
// interactive transport (the CLI prompts the user on ask-class verdicts); pass
// nil for a non-interactive one (HTTP server, IM bridge), where ask resolves to
// deny — the same posture the old per-transport gates had.
//
// The optional auditLog exists for tests: pass audit.NewAt("") (a no-op
// logger) or a temp-path logger so test checks never land in the real
// ~/.octo/audit.log. Production callers omit it and audit to the default path.
func NewPermissionGate(engine *permission.Engine, ask PermissionAsk, auditLog ...*audit.Logger) agent.PermissionGate {
	l := audit.New()
	if len(auditLog) > 0 {
		l = auditLog[0]
	}
	return &permissionGate{engine: engine, ask: ask, audit: l}
}

type permissionGate struct {
	engine *permission.Engine
	ask    PermissionAsk
	audit  *audit.Logger
}

// Check implements agent.PermissionGate.
func (g *permissionGate) Check(ctx context.Context, name string, input map[string]any) (bool, string) {
	// A Tool Search mcp_call wraps the real MCP tool — evaluate policy (and
	// prompt) against the wrapped tool, not the opaque "mcp_call" bridge.
	if real, realInput, ok := tools.ToolCallTarget(name, input); ok {
		name, input = real, realInput
	}
	decision := g.engine.Check(name, input)
	switch decision {
	case permission.Allow:
		return true, ""
	case permission.Deny:
		reason := g.engine.DenialReason(name, input)
		g.audit.Log(name, input, string(decision), reason)
		return false, reason
	case permission.Ask:
		if g.ask == nil {
			// Non-interactive: resolve ask → deny with the policy's reason.
			reason := g.engine.DenialReason(name, input)
			g.audit.Log(name, input, "ask-denied", reason)
			return false, reason
		}
		allow, remember, err := g.ask(ctx, name, input)
		if err != nil || !allow {
			reason := fmt.Sprintf("permission_denied: user declined to run %s", name)
			g.audit.Log(name, input, "user-declined", reason)
			return false, reason
		}
		if remember {
			g.engine.Remember(name, input, permission.Allow)
		}
		g.audit.Log(name, input, "user-allowed", "")
		return true, ""
	}
	reason := g.engine.DenialReason(name, input)
	g.audit.Log(name, input, "deny", reason)
	return false, reason
}
