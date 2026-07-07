// Package approval provides small adapters for implementing a PermissionGate
// without importing internal packages.
package approval

import (
	"context"

	"github.com/open-octo/octo-agent/pkg/octoagent"
)

// GateFunc adapts a plain function into an octoagent.PermissionGate.
type GateFunc func(ctx context.Context, name string, input map[string]any) (allowed bool, reason string)

// Check implements octoagent.PermissionGate.
func (f GateFunc) Check(ctx context.Context, name string, input map[string]any) (allowed bool, reason string) {
	return f(ctx, name, input)
}

// Ensure GateFunc satisfies the interface at compile time.
var _ octoagent.PermissionGate = GateFunc(nil)
