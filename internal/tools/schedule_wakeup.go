package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// Waker schedules a delayed re-entry into the current interactive session: a
// prompt delivered as a fresh user turn after a delay. It backs the loop
// skill's two modes — dynamic (the model re-arms each turn) and interval (the
// wakeup re-arms itself) — and is stamped into the turn ctx per surface. The
// TUI (cmd/octo) and the HTTP/IM server (internal/server) each implement it
// differently, the same ctx-scoping the Asker and ChannelFileSender use. A
// headless one-shot turn stamps none, so schedule_wakeup reports a clean error
// there rather than pretending to loop in a process that exits after one turn.
type Waker interface {
	// ScheduleWakeup arranges for prompt to run as a fresh user turn in the
	// current session after delay. When repeat is true the wakeup re-arms on
	// the same cadence (interval mode); otherwise it fires once and the model
	// re-arms by calling the tool again (dynamic mode). reason is a short
	// human-facing note. Any wakeup already pending for the session is
	// replaced — a session has at most one armed wakeup at a time.
	ScheduleWakeup(delay time.Duration, prompt, reason string, repeat bool) error

	// CancelWakeup stops any pending wakeup for the session — the explicit way
	// to end an interval loop (the dynamic mode ends by simply not re-arming).
	// No-op when nothing is armed.
	CancelWakeup() error
}

// ctxKeyWaker carries the turn-scoped Waker.
type ctxKeyWaker struct{}

// WithWaker stamps the session's Waker for the duration of an interactive turn.
func WithWaker(ctx context.Context, w Waker) context.Context {
	return context.WithValue(ctx, ctxKeyWaker{}, w)
}

// wakerFrom resolves the Waker for this turn, or nil on a surface that can't
// wake a session (the headless one-shot).
func wakerFrom(ctx context.Context) Waker {
	if w, ok := ctx.Value(ctxKeyWaker{}).(Waker); ok && w != nil {
		return w
	}
	return nil
}

// wakerSupported gates schedule_wakeup's advertisement in DefaultToolsFor. The
// interactive surfaces (TUI, server) set it at startup; it stays false in the
// headless one-shot process, where a loop has no live session to wake and the
// tool could only error. Mirrors SetRestarter: process-global, set once.
var wakerSupported bool

// SetWakerSupported toggles whether schedule_wakeup is advertised. Pass true
// from the TUI and server entry points; leave it false (the default) for the
// headless one-shot.
func SetWakerSupported(v bool) { wakerSupported = v }

func wakerEnabled() bool { return wakerSupported }

// Wakeup delay bounds, mirroring Claude Code's ScheduleWakeup: under a minute
// is pointless churn, over an hour belongs in a persistent cron task
// (cron-task-creator) rather than an in-session loop. The runtime clamps
// rather than rejects, so a slightly-off value never costs the model a retry.
const (
	minWakeupDelay = 60 * time.Second
	maxWakeupDelay = 3600 * time.Second
)

// MaxLoopLifetime bounds how long an in-session loop may keep ticking, measured
// from its first wakeup. A runaway loop — the model re-arming forever, or an
// interval loop nobody stops — must not tick indefinitely, especially on the
// server (web/IM) where no one watches it spend tokens. All three surfaces
// (TUI, web, IM) enforce this same bound via LoopExpired: once a loop has run
// this long it stops instead of re-arming. For a schedule that must outlive
// this, use a persistent cron task (cron-task-creator) instead.
const MaxLoopLifetime = 12 * time.Hour

// LoopExpired reports whether a loop that first armed at start has run past the
// anti-leak lifetime and must stop. Shared by every surface so the bound is
// identical everywhere; a zero start (no loop yet) is never expired.
func LoopExpired(start time.Time) bool {
	return !start.IsZero() && time.Since(start) >= MaxLoopLifetime
}

// ScheduleWakeupTool lets the model schedule its own next turn — the mechanism
// behind the loop skill. The model calls it at the end of a turn to come back
// later (dynamic, self-paced mode) or to keep a fixed cadence (repeat = interval
// mode); NOT calling it ends the loop. It only works inside a live session that
// can be re-entered — the interactive TUI or a server-managed web/IM session —
// so it is withheld from the headless one-shot (wakerEnabled gates it in
// DefaultToolsFor) and the per-session Waker is injected into the turn ctx.
type ScheduleWakeupTool struct{}

func (ScheduleWakeupTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "schedule_wakeup",
		Description: "Schedule your own next turn — the mechanism behind the loop skill. After this " +
			"turn ends and the session goes idle, the system re-runs `prompt` as a fresh user turn " +
			"after `delay_seconds`. Use it to pace a recurring task without the user re-prompting.\n\n" +
			"Two modes:\n" +
			"- DYNAMIC (repeat=false, the default): fires once. To keep looping you must call this " +
			"tool again on the next turn; simply NOT calling it ends the loop. Use when you decide " +
			"the cadence turn-by-turn or want to stop once the task is done.\n" +
			"- INTERVAL (repeat=true): the wakeup re-arms itself on the same cadence and keeps firing " +
			"on schedule. Use for a steady \"every N seconds\" loop.\n\n" +
			"delay_seconds is clamped to [60, 3600]. Picking a cadence: the model's prompt cache has a " +
			"~5-minute TTL, so a delay under 300s keeps context warm (right for actively polling " +
			"external state like a CI run or a deploy), while 300s+ pays a cache miss (right when " +
			"there's genuinely nothing to check sooner). Avoid exactly 300s — it pays the miss without " +
			"amortizing it. For idle ticks with no specific signal to watch, default to 1200-1800s.\n\n" +
			"The loop COEXISTS with the user: a message from the user does NOT stop it — the user can " +
			"chat with you while it keeps ticking. To STOP an interval loop, call this tool with " +
			"cancel=true (do this when the user asks you to stop, or pause); a dynamic loop also stops " +
			"by simply not re-arming. The user can also hard-stop with Ctrl+C. This is an in-session " +
			"loop — for a schedule that must survive restarts or run while you're away, use the " +
			"cron-task-creator skill instead.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"delay_seconds": map[string]any{
					"type":        "integer",
					"description": "Seconds until the wakeup fires. Clamped to [60, 3600].",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "One short sentence on what you're waiting for and why this cadence. Shown to the user. Be specific — \"watching CI run\" beats \"waiting\".",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The text delivered as a user turn on wakeup. Pass the same loop task verbatim each time so the next firing repeats the task.",
				},
				"repeat": map[string]any{
					"type":        "boolean",
					"description": "false (default) for dynamic mode (fires once; re-arm yourself to continue). true for interval mode (re-arms automatically on the same cadence).",
				},
				"cancel": map[string]any{
					"type":        "boolean",
					"description": "true to STOP the current loop (cancel any pending wakeup). Use when the user asks you to stop or pause. The other fields are ignored when cancel is true.",
				},
			},
			"required": []string{"delay_seconds", "reason", "prompt"},
		},
	}
}

func (ScheduleWakeupTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	w := wakerFrom(ctx)
	if w == nil {
		return agent.ToolResult{}, fmt.Errorf("schedule_wakeup: no live session to wake — the loop only runs in the interactive TUI or a server (web/IM) session")
	}

	// Explicit stop: cancel any pending wakeup and ignore the other fields.
	if boolArg(input, "cancel") {
		if err := w.CancelWakeup(); err != nil {
			return agent.ToolResult{}, fmt.Errorf("schedule_wakeup: %w", err)
		}
		return agent.ToolResult{
			Text: "Loop stopped — no further wakeups scheduled.",
			UI:   map[string]any{"type": "wakeup", "cancelled": true},
		}, nil
	}

	prompt := strings.TrimSpace(stringArg(input, "prompt"))
	if prompt == "" {
		return agent.ToolResult{}, fmt.Errorf("schedule_wakeup: prompt is required (the task text to resume on wakeup)")
	}
	reason := strings.TrimSpace(stringArg(input, "reason"))

	delay := time.Duration(intArg(input, "delay_seconds", 0)) * time.Second
	if delay < minWakeupDelay {
		delay = minWakeupDelay
	}
	if delay > maxWakeupDelay {
		delay = maxWakeupDelay
	}
	repeat := boolArg(input, "repeat")

	if err := w.ScheduleWakeup(delay, prompt, reason, repeat); err != nil {
		return agent.ToolResult{}, fmt.Errorf("schedule_wakeup: %w", err)
	}

	cadence := fmt.Sprintf("once in %s", delay)
	if repeat {
		cadence = fmt.Sprintf("every %s", delay)
	}
	text := fmt.Sprintf("Wakeup scheduled %s.", cadence)
	if reason != "" {
		text += " " + reason
	}
	return agent.ToolResult{
		Text: text,
		UI: map[string]any{
			"type":          "wakeup",
			"delay_seconds": int(delay.Seconds()),
			"repeat":        repeat,
			"reason":        reason,
		},
	}, nil
}
