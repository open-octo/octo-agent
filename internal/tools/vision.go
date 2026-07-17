package tools

import "sync/atomic"

// modelVision gates whether tools hand the model image content. Set per turn
// from the active model's capability (config.ModelVision) by every entry
// point that prepares a tool turn; defaults to true so embedders that never
// set it keep the historical behavior. Shared by the browser tool and
// read_file: an image block sent to a text-only model is silently dropped at
// the provider, and a model that believes it "read" an image it cannot see
// hallucinates its contents.
var modelVision = func() *atomic.Bool { b := &atomic.Bool{}; b.Store(true); return b }()

// SetModelVision records whether the active model accepts image input.
func SetModelVision(on bool) { modelVision.Store(on) }

// ModelVisionEnabled reports the current setting.
func ModelVisionEnabled() bool { return modelVision.Load() }
