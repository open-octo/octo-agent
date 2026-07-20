package tools

import (
	"context"
	"sync/atomic"
)

// modelVision gates whether tools hand the model image content when no
// per-turn value is stamped into ctx via WithModelVision — the CLI's
// one-session-per-process path (app.WireTools), which never stamps ctx.
// Defaults to true so embedders that never set it keep the historical
// behavior. Shared by the browser tool and read_file: an image block sent to
// a text-only model is silently dropped at the provider, and a model that
// believes it "read" an image it cannot see hallucinates its contents.
var modelVision = func() *atomic.Bool { b := &atomic.Bool{}; b.Store(true); return b }()

// SetModelVision records whether the active model accepts image input, for
// the CLI's one-session-per-process path. The server instead stamps
// WithModelVision into each turn's ctx — two concurrent sessions running
// different models would otherwise race on this process-global.
func SetModelVision(on bool) { modelVision.Store(on) }

type modelVisionCtxKeyType struct{}

var modelVisionCtxKey = modelVisionCtxKeyType{}

// WithModelVision returns ctx carrying the per-turn vision setting.
func WithModelVision(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, modelVisionCtxKey, on)
}

// ModelVisionEnabled picks the ctx-scoped setting (server, stamped fresh every
// turn by prepareToolTurn) first, then falls back to the process-global one
// (CLI/TUI, and any caller that never stamps ctx).
func ModelVisionEnabled(ctx context.Context) bool {
	if on, ok := ctx.Value(modelVisionCtxKey).(bool); ok {
		return on
	}
	return modelVision.Load()
}
