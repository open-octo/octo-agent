// Package hooks aliases octo-agent's lifecycle hook engine so external
// consumers of pkg/octoagent can configure Agent.Hooks without importing
// internal packages.
//
// Engine construction here is programmatic only: NewEngine takes no local
// files or environment variables, matching the "zero ambient dependency" bar
// set by provider.NewSender and toolenv.WireForSession. The CLI/server-only
// constructor that reads ~/.octo/hooks.yml (internal/hooks.EngineFromEnvAndFiles)
// is intentionally not aliased — external consumers register hooks in code via
// RegisterInProc/RegisterShell/RegisterShellMatched instead.
package hooks

import "github.com/open-octo/octo-agent/internal/hooks"

// Engine dispatches lifecycle hooks (shell commands and in-process callbacks)
// at the events an Agent fires during its loop. Set Agent.Hooks to a
// configured *Engine and Agent.HookMeta to identify the session; the agent
// loop invokes it automatically, including gating tool calls via a
// PreToolUse hook's Block/Allow verdict — callers never call into Engine
// directly from outside a hook.
type Engine = hooks.Engine

// Meta is the per-session identity folded into every hook Payload's common
// envelope (SessionID, Transport, TranscriptPath, Cwd, Model). Set
// Agent.HookMeta before running turns.
type Meta = hooks.Meta

// Event names one of the seven points in the agent loop a hook can attach to.
type Event = hooks.Event

// Payload is the data passed to a hook at Event time — the common envelope
// from Meta plus event-specific fields (ToolName/ToolInput for
// PreToolUse/PostToolUse, UserInput for UserPromptSubmit, etc).
type Payload = hooks.Payload

// InProcHook is an in-process hook callback: it receives the event payload
// and returns a string. For every event except EventPreToolUse, a non-empty
// return value is folded into the transcript the same way a shell hook's
// stdout would be. For EventPreToolUse the return value is instead read as a
// decision — see EventPreToolUse.
type InProcHook = hooks.InProcHook

// ToolDecision is a PreToolUse verdict for a single tool call. At most one of
// Block/Allow is set; both false defers to the permission gate.
type ToolDecision = hooks.ToolDecision

// SeenSet dedupes repeated hook firings (e.g. a resumed session replaying
// SessionStart) across every Engine that shares it. Pass nil to NewEngine to
// give an Engine its own, unshared SeenSet.
type SeenSet = hooks.SeenSet

// NewEngine constructs an Engine with no hooks registered. Reads no files or
// environment variables. Pass nil for seen to give the Engine its own SeenSet.
func NewEngine(seen *SeenSet) *Engine { return hooks.NewEngine(seen) }

// NewSeenSet constructs an empty SeenSet for sharing across Engines that
// should dedupe hook firings against each other (e.g. one Engine per
// sub-agent, sharing the parent's SeenSet).
func NewSeenSet() *SeenSet { return hooks.NewSeenSet() }

// Event constants — see internal/hooks.Payload for which Payload fields are
// populated at each one.
const (
	// EventSessionStart fires once per logical session opening.
	EventSessionStart = hooks.EventSessionStart
	// EventUserPromptSubmit fires before each user turn. Its return value is
	// folded into the transcript ahead of the user's message.
	EventUserPromptSubmit = hooks.EventUserPromptSubmit
	// EventPreToolUse fires before each tool dispatch and can block/allow it.
	// An InProcHook registered for this event opts into a verdict by
	// returning `{"decision":"block","reason":"..."}` or
	// `{"decision":"approve"}`; any other return value (including "") is no
	// opinion for that hook. In-process hooks are evaluated before shell
	// hooks; the first Block from either kind short-circuits the rest.
	EventPreToolUse = hooks.EventPreToolUse
	// EventPostToolUse fires after each successful tool result.
	EventPostToolUse = hooks.EventPostToolUse
	// EventStop fires when an assistant turn ends, on success and on error.
	EventStop = hooks.EventStop
	// EventSubagentStop fires when a spawned sub-agent finishes.
	EventSubagentStop = hooks.EventSubagentStop
	// EventPreCompact fires before history compaction.
	EventPreCompact = hooks.EventPreCompact
)
