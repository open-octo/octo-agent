package tools

import (
	"context"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

type fakeWaker struct {
	delay  time.Duration
	prompt string
	reason string
	repeat bool
	calls  int
}

func (w *fakeWaker) ScheduleWakeup(delay time.Duration, prompt, reason string, repeat bool) error {
	w.delay, w.prompt, w.reason, w.repeat = delay, prompt, reason, repeat
	w.calls++
	return nil
}

func TestScheduleWakeup_ClampsDelay(t *testing.T) {
	cases := []struct{ in, want int }{
		{10, 60},     // below the floor → clamped up
		{60, 60},     // at the floor
		{600, 600},   // in range, untouched
		{9999, 3600}, // above the ceiling → clamped down
	}
	for _, c := range cases {
		fw := &fakeWaker{}
		ctx := WithWaker(context.Background(), fw)
		_, err := (ScheduleWakeupTool{}).Execute(ctx, "schedule_wakeup", map[string]any{
			"delay_seconds": c.in, "prompt": "task", "reason": "r",
		})
		if err != nil {
			t.Fatalf("delay %d: unexpected error %v", c.in, err)
		}
		if got := int(fw.delay.Seconds()); got != c.want {
			t.Errorf("delay %d → %d, want %d", c.in, got, c.want)
		}
	}
}

func TestScheduleWakeup_NoWaker(t *testing.T) {
	_, err := (ScheduleWakeupTool{}).Execute(context.Background(), "schedule_wakeup", map[string]any{
		"delay_seconds": 60, "prompt": "x",
	})
	if err == nil {
		t.Fatal("expected an error when no Waker is stamped (headless one-shot)")
	}
}

func TestScheduleWakeup_RequiresPrompt(t *testing.T) {
	ctx := WithWaker(context.Background(), &fakeWaker{})
	_, err := (ScheduleWakeupTool{}).Execute(ctx, "schedule_wakeup", map[string]any{"delay_seconds": 60})
	if err == nil {
		t.Fatal("expected an error when prompt is missing")
	}
}

func TestScheduleWakeup_ForwardsArgs(t *testing.T) {
	fw := &fakeWaker{}
	ctx := WithWaker(context.Background(), fw)
	_, err := (ScheduleWakeupTool{}).Execute(ctx, "schedule_wakeup", map[string]any{
		"delay_seconds": 120, "prompt": "do it", "reason": "why", "repeat": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fw.calls != 1 || !fw.repeat || fw.prompt != "do it" || fw.reason != "why" {
		t.Fatalf("args not forwarded to the waker: %+v", fw)
	}
}

func TestScheduleWakeup_AdvertisementGate(t *testing.T) {
	SetWakerSupported(false)
	if toolListed(DefaultTools(), "schedule_wakeup") {
		t.Error("schedule_wakeup should be withheld when wakeups are unsupported")
	}
	SetWakerSupported(true)
	defer SetWakerSupported(false)
	if !toolListed(DefaultTools(), "schedule_wakeup") {
		t.Error("schedule_wakeup should be advertised when wakeups are supported")
	}
}

func toolListed(defs []agent.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}
