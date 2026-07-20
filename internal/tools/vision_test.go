package tools

import (
	"context"
	"testing"
)

// TestModelVisionEnabled_CtxOverridesGlobal proves ModelVisionEnabled prefers
// a ctx-scoped value over a conflicting process-global one — the server's
// per-turn ctx must win even if some other code path (or a leftover CLI
// global from an earlier test) left the global set to the opposite value.
func TestModelVisionEnabled_CtxOverridesGlobal(t *testing.T) {
	SetModelVision(true)
	t.Cleanup(func() { SetModelVision(true) })

	if ModelVisionEnabled(WithModelVision(context.Background(), false)) {
		t.Error("ctx-scoped false should win over global true")
	}

	SetModelVision(false)
	if !ModelVisionEnabled(WithModelVision(context.Background(), true)) {
		t.Error("ctx-scoped true should win over global false")
	}
}

// TestModelVisionEnabled_FallsBackToGlobalWhenCtxUnset proves the CLI's
// one-session-per-process path (which never stamps ctx) still resolves to
// whatever SetModelVision recorded.
func TestModelVisionEnabled_FallsBackToGlobalWhenCtxUnset(t *testing.T) {
	SetModelVision(true)
	t.Cleanup(func() { SetModelVision(true) })
	if !ModelVisionEnabled(context.Background()) {
		t.Error("expected fallback to global true")
	}

	SetModelVision(false)
	if ModelVisionEnabled(context.Background()) {
		t.Error("expected fallback to global false")
	}
}
