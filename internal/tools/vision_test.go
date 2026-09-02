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

// TestImageDescriberActive_CtxOverridesGlobal mirrors the ModelVisionEnabled
// contract: the server stamps a per-turn value into ctx (two sessions may run
// different models), the CLI sets the process global.
func TestImageDescriberActive_CtxOverridesGlobal(t *testing.T) {
	SetImageDescriberActive(false)
	t.Cleanup(func() { SetImageDescriberActive(false) })

	if !ImageDescriberActive(WithImageDescriberActive(context.Background(), true)) {
		t.Error("ctx value true must win over global false")
	}
	SetImageDescriberActive(true)
	if ImageDescriberActive(WithImageDescriberActive(context.Background(), false)) {
		t.Error("ctx value false must win over global true")
	}
	if !ImageDescriberActive(context.Background()) {
		t.Error("unstamped ctx must fall back to the global")
	}
}

// TestImageDescriberActive_DefaultsOff pins that callers who never call the
// setter keep the historical refusal behaviour. ModelVision
// defaults to true for the same reason in reverse — together they mean "refuse
// only when the model can't see AND nothing will describe".
func TestImageDescriberActive_DefaultsOff(t *testing.T) {
	SetImageDescriberActive(false)
	if ImageDescriberActive(context.Background()) {
		t.Error("vision helper must default to inactive")
	}
}

// TestImagesAllowed_Matrix is the gate every image-producing tool consults.
func TestImagesAllowed_Matrix(t *testing.T) {
	t.Cleanup(func() { SetModelVision(true); SetImageDescriberActive(false) })

	tests := []struct {
		name      string
		vision    bool
		describer bool
		want      bool
	}{
		{"vision model, no helper", true, false, true},
		{"vision model with helper", true, true, true},
		{"text-only model, no helper", false, false, false},
		{"text-only model with helper", false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithImageDescriberActive(WithModelVision(context.Background(), tt.vision), tt.describer)
			if got := ImagesAllowed(ctx); got != tt.want {
				t.Errorf("ImagesAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
