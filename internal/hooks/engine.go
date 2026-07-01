package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Engine is the single hook-dispatch surface. It supersedes Runner: instead of
// two hard-coded shell hooks (pre/post-turn) wired only in the CLI, it holds a
// set of hooks per Event and is invoked from the agent core, so every transport
// (CLI/TUI, serve web+IM, sub-agents) shares one dispatch path. It also carries
// in-process hooks (Go funcs), which is how the memory injector's reminder /
// save-nudge fold into the same pipeline the shell hooks use.
//
// One Engine per process, constructed in internal/app and attached to every
// Agent (Agent.Hooks). The zero value is a valid no-op — Dispatch/Inject return
// cleanly with no work done, so callers never branch on "are hooks configured".
type Engine struct {
	mu     sync.Mutex
	shell  map[Event][]shellHook
	inproc map[Event][]InProcHook

	// seen is the process-level SessionStart seen-set. It is SHARED across every
	// Engine in the process (serve rebuilds the Agent — and thus its Engine —
	// every turn, and hosts many sessions), which is what makes resume fire once
	// per OS process rather than once per turn. Never nil after NewEngine.
	seen *SeenSet

	// Notify surfaces hook failures / traces as REPL notices. Nil-safe.
	Notify func(string)
}

// SeenSet records which sessions a process instance has already fired
// SessionStart for. It is shared by pointer across all Engines in a process
// (each Engine otherwise owns per-session in-process hooks) and carries its own
// lock so that sharing is goroutine-safe independent of any Engine's lock.
type SeenSet struct {
	mu sync.Mutex
	m  map[string]bool
}

// NewSeenSet returns an empty seen-set. Tests pass their own for isolation;
// production engines share the process singleton via SharedSeen.
func NewSeenSet() *SeenSet {
	return &SeenSet{m: make(map[string]bool)}
}

// sharedSeen is the one process-level seen-set. SessionStart resume dedup is
// inherently process-global (serve rebuilds the Agent every turn and hosts many
// sessions), so every production Engine shares this instance.
var sharedSeen = NewSeenSet()

// SharedSeen returns the process-level seen-set. Pass it to NewEngine/
// EngineFromEnv at every production construction site so resume fires once per
// OS process rather than once per turn.
func SharedSeen() *SeenSet { return sharedSeen }

// markSeen records sessionID and reports whether it was already present.
func (s *SeenSet) markSeen(sessionID string) (already bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	already = s.m[sessionID]
	s.m[sessionID] = true
	return already
}

// InProcHook is an in-process hook: a Go func invoked with the same Payload a
// shell hook would receive on stdin. For injecting events it returns the text
// to fold in ("" = nothing); for side-effect events the return is ignored. This
// is the surface the memory injector registers on.
type InProcHook func(ctx context.Context, p Payload) string

// shellHook is one configured shell command for an event. matcher/async land in
// a later phase; for now every shell hook runs synchronously and unconditionally.
type shellHook struct {
	command string
	timeout time.Duration
}

// Session-start source labels (parity with Claude Code's SessionStart.source).
const (
	SourceStartup = "startup"
	SourceResume  = "resume"
	SourceClear   = "clear"
)

// NewEngine returns an empty engine ready for RegisterShell/RegisterInProc,
// sharing the given process-level seen-set. Pass nil for a standalone engine
// (tests, or a context with no cross-Agent sharing) and it allocates its own.
func NewEngine(seen *SeenSet) *Engine {
	if seen == nil {
		seen = NewSeenSet()
	}
	return &Engine{
		shell:  make(map[Event][]shellHook),
		inproc: make(map[Event][]InProcHook),
		seen:   seen,
	}
}

// RegisterShell adds a shell command hook for an event. A zero timeout uses the
// package default.
func (e *Engine) RegisterShell(event Event, command string, timeout time.Duration) {
	command = strings.TrimSpace(command)
	if e == nil || command == "" {
		return
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	} else if timeout > timeoutCeiling {
		timeout = timeoutCeiling
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shell == nil {
		e.shell = make(map[Event][]shellHook)
	}
	e.shell[event] = append(e.shell[event], shellHook{command: command, timeout: timeout})
}

// RegisterInProc adds an in-process hook for an event.
func (e *Engine) RegisterInProc(event Event, fn InProcHook) {
	if e == nil || fn == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inproc == nil {
		e.inproc = make(map[Event][]InProcHook)
	}
	e.inproc[event] = append(e.inproc[event], fn)
}

// Configured reports whether any hook (shell or in-process) is registered for
// event. Callers use it to skip payload construction on the common no-hook path.
func (e *Engine) Configured(event Event) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.shell[event]) > 0 || len(e.inproc[event]) > 0
}

// hooksFor snapshots the registered hooks for an event under the lock so
// dispatch runs without holding it (a shell hook can block for its timeout).
func (e *Engine) hooksFor(event Event) ([]shellHook, []InProcHook) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sh := append([]shellHook(nil), e.shell[event]...)
	ip := append([]InProcHook(nil), e.inproc[event]...)
	return sh, ip
}

// Inject runs the hooks registered for an injecting event (SessionStart /
// UserPromptSubmit / PostToolUse) and returns their combined output to fold into
// the model stream. In-process hooks run first, then shell hooks, in
// registration order; non-empty outputs are joined with a blank line. A shell
// hook failure is surfaced via Notify and contributes no text — it never blocks
// the turn. Returns "" when nothing was produced (or the engine is nil / the
// event isn't an injecting one).
func (e *Engine) Inject(ctx context.Context, p Payload) string {
	if e == nil || !p.Event.injects() {
		return ""
	}
	sh, ip := e.hooksFor(p.Event)
	if len(sh) == 0 && len(ip) == 0 {
		return ""
	}
	var parts []string
	for _, fn := range ip {
		if out := strings.TrimSpace(fn(ctx, p)); out != "" {
			parts = append(parts, out)
		}
	}
	if out := e.runShellHooks(ctx, sh, p); out != "" {
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n\n")
}

// Dispatch runs the hooks for a side-effect event (Stop / SubagentStop /
// PreCompact): output is ignored, failures surface via Notify but never
// propagate. Runs synchronously within each hook's timeout (async execution
// lands in a later phase). No-op on a nil engine or an unconfigured event.
func (e *Engine) Dispatch(ctx context.Context, p Payload) {
	if e == nil {
		return
	}
	sh, ip := e.hooksFor(p.Event)
	for _, fn := range ip {
		fn(ctx, p) // side-effect events ignore in-proc output
	}
	e.runShellHooks(ctx, sh, p)
}

// runShellHooks marshals the payload once and runs each shell hook, joining the
// stdout of injecting events (parsed for the additional_context envelope) with
// blank lines. Side-effect events discard the joined result. Failures go to
// Notify.
func (e *Engine) runShellHooks(ctx context.Context, hooks []shellHook, p Payload) string {
	if len(hooks) == 0 {
		return ""
	}
	stdin, err := json.Marshal(p)
	if err != nil {
		e.notify("hooks: marshal " + string(p.Event) + " payload: " + err.Error())
		return ""
	}
	var parts []string
	for _, h := range hooks {
		out, err := execShell(ctx, h.command, stdin, h.timeout)
		if err != nil {
			e.notify(err.Error())
			continue
		}
		if p.Event.injects() {
			if txt := parsePreOutput(out); txt != "" {
				parts = append(parts, txt)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func (e *Engine) notify(msg string) {
	if e != nil && e.Notify != nil {
		e.Notify(msg)
	}
}

// SessionStartDecision resolves the SessionStart source for a turn and reports
// whether the event should fire, updating the process-level seen-set. Inputs:
//
//	persistedStarted — the session's durable "SessionStart has fired before"
//	                   flag (shared across all three transports).
//	isClear          — this turn follows a /clear (ClearHistory).
//
// Semantics (see the design doc's SessionStart section):
//   - isClear                     → (clear, fire)
//   - never started before        → (startup, fire)  [caller then persists the flag]
//   - started, unseen this process → (resume, fire)   [a new OS process attached]
//   - started, already seen        → ("", don't fire) [same process, later turn]
//
// The seen-set is why click-around / subsequent messages in one serve process
// don't re-fire resume, while a process restart (empty seen-set) does.
func (e *Engine) SessionStartDecision(sessionID string, persistedStarted, isClear bool) (source string, fire bool) {
	if e == nil {
		return "", false
	}
	switch {
	case isClear:
		e.seen.markSeen(sessionID)
		return SourceClear, true
	case !persistedStarted:
		e.seen.markSeen(sessionID)
		return SourceStartup, true
	default:
		if already := e.seen.markSeen(sessionID); !already {
			return SourceResume, true
		}
		return "", false
	}
}

// EngineFromEnv builds an Engine from the legacy OCTO_HOOK_* env vars, mapping
// the pre-turn hook to UserPromptSubmit and the post-turn hook to Stop. This is
// the compatibility shim: existing Hindsight wiring keeps working unchanged and
// — because the engine is invoked in the agent core — now also fires on the web
// and IM transports, not just the CLI. Always returns a usable engine.
func EngineFromEnv(seen *SeenSet) *Engine {
	e := NewEngine(seen)
	r := LoadFromEnv() // reuse the env parsing + timeout ceiling
	timeout := r.Timeout
	if r.PreTurnCmd != "" {
		e.RegisterShell(EventUserPromptSubmit, r.PreTurnCmd, timeout)
	}
	if r.PostTurnCmd != "" {
		e.RegisterShell(EventStop, r.PostTurnCmd, timeout)
	}
	return e
}
