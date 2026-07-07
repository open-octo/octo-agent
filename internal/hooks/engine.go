package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

// shellHook is one configured shell command for an event. matcher, when set,
// gates the hook on the tool name for the tool events (PreToolUse/PostToolUse);
// it is ignored for the other events. async, honoured only for the side-effect
// events dispatched via Dispatch (Stop/SubagentStop/PreCompact), runs the hook
// off the turn's critical path through the durable spill queue; it is ignored
// for injecting events, whose output must be produced synchronously.
type shellHook struct {
	command string
	matcher *regexp.Regexp // nil → match every tool
	async   bool
	timeout time.Duration
}

// matches reports whether this hook should run for the given event/tool. A nil
// matcher, or any non-tool event, matches unconditionally.
func (h shellHook) matches(event Event, toolName string) bool {
	if h.matcher == nil {
		return true
	}
	switch event {
	case EventPreToolUse, EventPostToolUse:
		return h.matcher.MatchString(toolName)
	default:
		return true
	}
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

// RegisterShell adds a synchronous shell command hook for an event, matching
// every tool. A zero timeout uses the package default.
func (e *Engine) RegisterShell(event Event, command string, timeout time.Duration) {
	_ = e.RegisterShellMatched(event, command, "", false, timeout)
}

// RegisterShellMatched adds a shell command hook whose matcher (a regexp over
// the tool name, honoured only for PreToolUse/PostToolUse) gates whether it
// runs. An empty matcher matches every tool. async runs it off the critical
// path (honoured only for side-effect events). A zero timeout uses the package
// default. Returns an error only when matcher is not a valid regexp.
func (e *Engine) RegisterShellMatched(event Event, command, matcher string, async bool, timeout time.Duration) error {
	command = strings.TrimSpace(command)
	if e == nil || command == "" {
		return nil
	}
	var re *regexp.Regexp
	if m := strings.TrimSpace(matcher); m != "" {
		compiled, err := regexp.Compile(m)
		if err != nil {
			return err
		}
		re = compiled
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
	e.shell[event] = append(e.shell[event], shellHook{command: command, matcher: re, async: async, timeout: timeout})
	return nil
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
// propagate. A hook marked async is handed to the durable spill queue and runs
// off the critical path; the rest run synchronously within their timeout.
// No-op on a nil engine or an unconfigured event.
func (e *Engine) Dispatch(ctx context.Context, p Payload) {
	if e == nil {
		return
	}
	sh, ip := e.hooksFor(p.Event)
	for _, fn := range ip {
		fn(ctx, p) // side-effect events ignore in-proc output
	}
	if len(sh) == 0 {
		return
	}
	stdin := e.lazyStdin(p)
	for _, h := range sh {
		if !h.matches(p.Event, p.ToolName) {
			continue
		}
		if h.async {
			enqueueAsync(asyncItem{Command: h.command, Timeout: h.timeout, Cwd: p.Cwd, Payload: p})
			continue // async path carries the payload itself; no stdin marshal needed
		}
		body, ok := stdin()
		if !ok {
			return
		}
		if _, rerr := execShell(ctx, h.command, body, h.timeout); rerr != nil {
			e.notify(rerr.Error())
		}
	}
}

// lazyStdin returns a function that marshals p to hook-stdin JSON at most once,
// on first call — so an event whose hooks are all filtered out by matcher (or
// all async) never pays the marshal of a possibly-large payload. The second
// return is false if marshalling failed (surfaced via Notify).
func (e *Engine) lazyStdin(p Payload) func() ([]byte, bool) {
	var (
		body []byte
		done bool
		ok   bool
	)
	return func() ([]byte, bool) {
		if !done {
			done = true
			b, err := json.Marshal(p)
			if err != nil {
				e.notify("hooks: marshal " + string(p.Event) + " payload: " + err.Error())
			} else {
				body, ok = b, true
			}
		}
		return body, ok
	}
}

// runShellHooks marshals the payload once and runs each shell hook, joining the
// stdout of injecting events (parsed for the additional_context envelope) with
// blank lines. Side-effect events discard the joined result. Failures go to
// Notify.
func (e *Engine) runShellHooks(ctx context.Context, hooks []shellHook, p Payload) string {
	if len(hooks) == 0 {
		return ""
	}
	stdin := e.lazyStdin(p)
	var parts []string
	for _, h := range hooks {
		if !h.matches(p.Event, p.ToolName) {
			continue
		}
		body, ok := stdin()
		if !ok {
			return strings.Join(parts, "\n\n")
		}
		out, err := execShell(ctx, h.command, body, h.timeout)
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

// ToolDecision is a PreToolUse verdict for a single tool call. At most one of
// Block/Allow is set; both false means "no opinion" (defer to the permission
// gate). Reason accompanies a Block, surfaced to the model as the denial text.
type ToolDecision struct {
	Block  bool
	Allow  bool
	Reason string
}

// toolDecisionOut is the optional structured stdout a PreToolUse hook may emit
// to make an explicit allow/deny decision (beyond the exit-code protocol).
type toolDecisionOut struct {
	Decision string `json:"decision"` // "approve" | "block"
	Reason   string `json:"reason,omitempty"`
}

// PreToolUse runs the PreToolUse hooks for a tool call and returns the
// aggregated verdict. In-process hooks run first (registration order), then
// shell hooks (registration order) — same ordering as Inject. Protocol per
// shell hook, matching Claude Code's:
//   - exit 2                     → block (reason = structured reason, else stderr)
//   - exit 0 + {"decision":...}  → that decision (block / approve)
//   - exit 0, no decision        → no opinion (defer to the gate)
//   - timeout / other exit       → non-blocking error (notify, tool proceeds)
//
// An in-process hook has no exit code, so it opts into a decision the same
// way a shell hook's stdout does: return `{"decision":"block","reason":"..."}`
// or `{"decision":"approve"}` as its string result. Any other return value
// (including "") is no opinion for that hook.
//
// Block wins over Allow: the first hook that blocks short-circuits, and an
// Allow never overrides a later Block. A tool the caller then runs unless
// blocked; an Allow tells the caller to bypass the interactive gate.
func (e *Engine) PreToolUse(ctx context.Context, p Payload) ToolDecision {
	if e == nil {
		return ToolDecision{}
	}
	sh, ip := e.hooksFor(EventPreToolUse)
	if len(sh) == 0 && len(ip) == 0 {
		return ToolDecision{}
	}

	allow := false
	for _, fn := range ip {
		d, ok := parseToolDecision([]byte(e.runInProcHookSafely(fn, ctx, p)))
		if !ok {
			continue
		}
		switch d.Decision {
		case "block":
			return ToolDecision{Block: true, Reason: firstNonEmpty(strings.TrimSpace(d.Reason), "blocked by PreToolUse hook")}
		case "approve":
			allow = true
		}
	}
	if len(sh) == 0 {
		return ToolDecision{Allow: allow}
	}

	stdin, err := json.Marshal(p)
	if err != nil {
		e.notify("hooks: marshal PreToolUse payload: " + err.Error())
		return ToolDecision{Allow: allow}
	}
	for _, h := range sh {
		if !h.matches(EventPreToolUse, p.ToolName) {
			continue
		}
		res := runShellRaw(ctx, h.command, stdin, h.timeout, "")
		switch {
		case res.timedOut:
			e.notify(fmt.Sprintf("hooks: PreToolUse %s timed out after %s (tool allowed to proceed)", h.command, h.timeout))
		case res.exitCode == 2:
			return ToolDecision{Block: true, Reason: blockReason(res)}
		case res.exitCode == 0:
			if d, ok := parseToolDecision(res.stdout); ok {
				switch d.Decision {
				case "block":
					return ToolDecision{Block: true, Reason: firstNonEmpty(strings.TrimSpace(d.Reason), "blocked by PreToolUse hook")}
				case "approve":
					allow = true
				}
			}
		default:
			e.notify(fmt.Sprintf("hooks: PreToolUse %s: exit %d (tool allowed to proceed)", h.command, res.exitCode))
		}
	}
	return ToolDecision{Allow: allow}
}

// runInProcHookSafely calls an in-process PreToolUse hook with a recover, so a
// panicking callback can't take the whole agent turn down with it. A shell
// hook's failure mode is a subprocess crash, which the parent Go process
// never sees; an in-process hook runs in the same goroutine, so without this
// its panic would propagate straight out of PreToolUse. Treated the same as
// any other non-blocking hook failure: notify and return "" (no opinion, tool
// allowed to proceed).
func (e *Engine) runInProcHookSafely(fn InProcHook, ctx context.Context, p Payload) (out string) {
	defer func() {
		if r := recover(); r != nil {
			e.notify(fmt.Sprintf("hooks: PreToolUse in-process hook panicked: %v (tool allowed to proceed)", r))
			out = ""
		}
	}()
	return fn(ctx, p)
}

// blockReason picks the denial text for an exit-2 (or decision:block) hook:
// the structured stdout reason if present, else the stderr tail, else a
// default.
func blockReason(res shellResult) string {
	if d, ok := parseToolDecision(res.stdout); ok {
		if r := strings.TrimSpace(d.Reason); r != "" {
			return r
		}
	}
	if tail := strings.TrimSpace(string(res.stderr)); tail != "" {
		return oneLineCap(tail, 500)
	}
	return "blocked by PreToolUse hook"
}

// parseToolDecision tries to read a structured decision from a hook's stdout.
// Returns ok=false when stdout isn't a JSON object with a recognised decision.
func parseToolDecision(stdout []byte) (toolDecisionOut, bool) {
	trimmed := strings.TrimSpace(string(stdout))
	if !strings.HasPrefix(trimmed, "{") {
		return toolDecisionOut{}, false
	}
	var d toolDecisionOut
	if err := json.Unmarshal([]byte(trimmed), &d); err != nil {
		return toolDecisionOut{}, false
	}
	if d.Decision != "approve" && d.Decision != "block" {
		return toolDecisionOut{}, false
	}
	return d, true
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
		// async so a slow retain script doesn't block the next prompt — the
		// fire-and-forget the legacy post-turn hook intended (and what
		// `octo hooks list` advertises for this shim).
		_ = e.RegisterShellMatched(EventStop, r.PostTurnCmd, "", true, timeout)
	}
	return e
}
