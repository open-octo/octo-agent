package agent

import (
	"context"
	"testing"

	"github.com/Leihb/octo-agent/internal/hooks"
)

func TestFirePreCompact_DispatchesWhenConfigured(t *testing.T) {
	a := New(&fakeSender{}, "m")
	a.Hooks = hooks.NewEngine(nil)
	fired := 0
	a.Hooks.RegisterInProc(hooks.EventPreCompact, func(_ context.Context, p hooks.Payload) string {
		if p.Event != hooks.EventPreCompact {
			t.Errorf("payload event = %q", p.Event)
		}
		fired++
		return ""
	})
	a.firePreCompact()
	if fired != 1 {
		t.Errorf("PreCompact hook fired %d times, want 1", fired)
	}
}

func TestFirePreCompact_NoopWhenUnconfigured(t *testing.T) {
	a := New(&fakeSender{}, "m")
	a.Hooks = hooks.NewEngine(nil) // no PreCompact hook
	a.firePreCompact()             // must not panic / no-op
	a.Hooks = nil
	a.firePreCompact() // nil engine must not panic
}
