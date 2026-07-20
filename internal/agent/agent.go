package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/hooks"
)

// Sender is the minimal slice of provider.Provider the Agent depends on:
// it accepts a model, system prompt, messages, and optional max-tokens, and
// returns the assistant's text reply.
//
// Declaring this interface here (rather than depending on provider.Provider
// directly) keeps the agent package free of an import on provider, which in
// turn keeps the dependency graph one-directional: provider → agent, never
// the other way. The chat subcommand and any future caller is responsible
// for adapting a provider.Provider into a Sender (trivial — provider's
// Request/Response already match).
type Sender interface {
	SendMessages(ctx context.Context, model, system string, messages []Message, maxTokens int) (Reply, error)
}

// StreamingSender extends Sender with the ability to deliver the assistant
// reply chunk-by-chunk via a callback while the upstream stream is open.
//
// The agent type-asserts its Sender to StreamingSender at the start of
// TurnStream; if the underlying provider doesn't implement it, TurnStream
// falls back to the buffered Sender.SendMessages and invokes onChunk once
// with the full content for callers that don't want to branch on capability.
type StreamingSender interface {
	Sender
	StreamMessages(
		ctx context.Context,
		model, system string,
		messages []Message,
		maxTokens int,
		onChunk func(textDelta string),
		onThinking func(thinkingDelta string),
	) (Reply, error)
}

// ToolSender extends Sender with a tool-aware variant that carries tool
// definitions alongside the messages. Implementations return the full
// content-block list (including tool_use blocks) in Reply.Blocks.
type ToolSender interface {
	Sender
	SendMessagesWithTools(
		ctx context.Context,
		model, system string,
		messages []Message,
		maxTokens int,
		tools []ToolDefinition,
	) (Reply, error)
}

// LowEffortSender is implemented by a Sender that can produce a cheaper
// variant of itself with reasoning effort capped to a small, fast budget.
// Suggest and GenerateTitle use this as a fallback when NoReasoningSender
// isn't available: the throwaway call deliberately reuses the turn's own
// Sender (so the request shares the main conversation's prompt-cache prefix —
// see Suggest's doc comment), but that Sender carries whatever
// reasoning_effort the session happens to be configured with. A session
// running "high"/"max" would otherwise pay the model's full reasoning budget
// just to produce a one-line suggestion, for no benefit — and on a slower
// provider this reliably exceeds the throwaway call's timeout. LowEffort caps
// effort to "low" rather than disabling it outright, so the request shape
// stays consistent with earlier turns that may already carry thinking blocks
// in history.
type LowEffortSender interface {
	Sender
	LowEffort() Sender
}

// NoReasoningSender is implemented by a Sender that can produce a variant of
// itself with reasoning disabled entirely. GenerateTitle and Suggest prefer
// this over LowEffortSender: a 6-word title / one-line follow-up suggestion
// needs no reasoning at all, and even "low" reasoning can consume the tight
// token budget or time out. If a sender does not implement this interface,
// both calls fall back to LowEffortSender instead.
type NoReasoningSender interface {
	Sender
	NoReasoning() Sender
}

// ToolInputDeltaFunc receives raw JSON fragments of a tool_use block's
// arguments as they stream in. Fragments concatenate to form the final
// JSON object. May be nil; implementations should treat nil as "don't
// surface tool-input deltas" and skip the callback.
type ToolInputDeltaFunc func(toolID, toolName, partialJSON string)

// ThinkingDeltaFunc receives fragments of a reasoning model's thinking trace
// as they stream in, before the visible reply. May be nil; implementations
// treat nil as "don't surface reasoning" and skip the callback.
type ThinkingDeltaFunc func(thinkingDelta string)

// ToolStreamingSender extends ToolSender and StreamingSender with a streaming
// tool-aware variant. Implementations stream text deltas via onChunk,
// stream tool-argument JSON fragments via onToolDelta (optional, may be
// nil), and accumulate tool_use blocks. The final Reply carries Blocks for
// dispatch.
type ToolStreamingSender interface {
	ToolSender
	StreamMessagesWithTools(
		ctx context.Context,
		model, system string,
		messages []Message,
		maxTokens int,
		tools []ToolDefinition,
		onChunk func(textDelta string),
		onToolDelta ToolInputDeltaFunc,
		onThinking ThinkingDeltaFunc,
	) (Reply, error)
}

// Reply is the agent-level view of a provider response. It deliberately
// mirrors provider.Response field-for-field (same names, same types) but
// lives in this package so users of the agent API don't have to import
// provider.
//
// Blocks is populated when the provider returns content blocks — in
// particular when stop_reason=="tool_use", Blocks will contain the
// tool_use blocks that the agentic loop should dispatch.
type Reply struct {
	Content      string
	Blocks       []ContentBlock
	Model        string
	StopReason   string
	InputTokens  int
	OutputTokens int
	// Cache accounting for this call (0 when the backend reports none).
	// CacheReadTokens is input served from cache; CacheWriteTokens is input
	// written into the cache this turn (Anthropic only).
	CacheReadTokens  int
	CacheWriteTokens int
}

// Agent owns one conversation: the system prompt, the history of turns, the
// model name, and the LLM transport (Sender).
type Agent struct {
	mu        sync.RWMutex // protects Sender (written by TUI event loop, read by turn goroutine)
	Sender    Sender
	System    string
	Model     string
	MaxTokens int
	History   *History

	// LeanSystem, when set, is a lighter variant of System (skills manifest and
	// memory dropped) used to seed cheap read-only sub-agents. Empty falls back
	// to System.
	LeanSystem string

	// LiteSender/LiteModel, when both set, run cheap internal calls —
	// history summarisation (compaction) and session-title generation — on a
	// cheaper model. Unset falls back to Sender/Model. On a lite-call error
	// summarize retries once on the primary sender, so a misconfigured lite
	// model can't break compaction; title generation instead surfaces the
	// error to GenerateTitleOrSnippet's snippet fallback (no retry).
	LiteSender Sender
	LiteModel  string

	// CWD is the working directory used to resolve project context (e.g.
	// .octorules) for the planner. Callers should set this to the repo root
	// before invoking PlanTask.
	CWD string

	// Gate, when non-nil, vets every tool call before execution. A nil
	// Gate means no gating — all tool calls run (the pre-M6.5 behaviour).
	Gate PermissionGate

	// MaxTurns caps the number of provider round-trips in a single Run/
	// RunStream. <= 0 uses defaultMaxTurns. Hitting the cap ends the run
	// with a friendly budget reply (StopReason "max_turns"), not an error.
	MaxTurns int

	// MaxTokensEscalate is the per-response cap retried once, from unchanged
	// history, when a round is truncated by the output cap (StopReasonMaxTokens).
	// It only ever raises the cap: escalation fires only when this exceeds the
	// round's current cap. <= 0 disables escalation. See
	// dev-docs/truncation-recovery.md.
	MaxTokensEscalate int

	// CompactThreshold controls history compaction: when the most recent
	// context sent (lastInputTokens) crosses the effective trigger, the next
	// Run/RunStream summarizes the older turns before continuing. Semantics:
	// <0 disables; ==0 auto (a fraction of the model's context window, the
	// default); >0 is an explicit token count. See compactTriggerTokens.
	CompactThreshold int

	// CompactAutoFraction is the share (0.0–1.0) of the model's context window
	// at which auto-compaction triggers when CompactThreshold == 0. Zero uses
	// the built-in default (0.75). Values outside 0–1 are clamped.
	CompactAutoFraction float64

	// CompactKeepFraction is the share (0.0–1.0) of the model's context window
	// that compaction keeps verbatim as the recent tail; everything older is
	// folded into the summary. Zero uses the built-in default (0.30). It is
	// always capped below the trigger (at half the trigger) so a compaction can
	// reliably bring the context under the trigger with headroom to spare. See
	// compactKeepBudget and dev-docs/compaction-redesign.md.
	CompactKeepFraction float64

	// ArchiveDir, when non-empty, is the directory into which compaction writes
	// the verbatim originals of folded turns (chunk-NNN.md) before replacing
	// them with the summary, so the model can recall details with the read
	// tool. Set by the session-owning layer (CLI/server) via Session.ChunkDir;
	// empty disables archival. Archival is best-effort — a write failure never
	// breaks a compaction. See dev-docs/compaction-redesign.md.
	ArchiveDir string

	// overflow handles "context too long" 400 errors by compressing history
	// and retrying. Aligned with Ruby's perform_context_overflow_compression.
	overflow overflowRecovery

	// usageMu guards the cumulative counters below. They are written by the
	// turn loop (accrueUsage) AND by sub-agent goroutines (AccrueChildUsage,
	// which run concurrently when a sub_agent batch fans out), and read from
	// the TUI goroutine (SessionTokens / ContextUsage / SessionCacheTokens).
	usageMu sync.Mutex
	// Cumulative token counts for this session (all turns combined).
	sessionInputTokens  int
	sessionOutputTokens int
	// Cumulative cache accounting for this session.
	sessionCacheReadTokens  int
	sessionCacheWriteTokens int
	// lastInputTokens is the size of the most recently sent context, used as
	// the compaction trigger.
	lastInputTokens int

	// Inbox holds user messages that arrived while a turn was running.
	// The run loop drains it at the start of each iteration, before the LLM
	// call, so messages enter history in chronological order. This mirrors
	// Ruby octo's @inbox and keeps mid-turn input handling simple.
	Inbox Inbox

	// GoalAcct, when set, receives goal usage accounting after each LLM
	// reply. Wired by the session-owning layer (Session implements
	// GoalAccountant); nil disables goal accounting. The durable goal lives
	// on the Session so per-turn Agents (serve rebuilds one each turn) all
	// account into the same record.
	GoalAcct GoalAccountant

	// goalBaseIn/goalBaseOut snapshot the cumulative session counters at the
	// last goal accounting, so each accounting bills only the delta. Reset at
	// every turn start. (Mid-turn goal creation is handled on the Session
	// side instead: the skip-next-delta flag drops the creating tick's
	// tokens.) Turn goroutine only.
	goalBaseIn, goalBaseOut int

	// Hooks is the per-Agent hook engine. It supersedes the old single-slot
	// UserInputHook/ToolResultHook: the memory injector registers its reminder
	// (UserPromptSubmit) and save-nudge (PostToolUse) as in-process hooks on it,
	// and any shell hooks (env or hooks.yml) live here too, so every transport
	// runs one dispatch path. The engine shares a process-level seen-set so
	// SessionStart resume fires once per OS process. Nil is a no-op.
	Hooks *hooks.Engine

	// HookMeta carries the session identity (id, transport, transcript, cwd,
	// model) folded into every hook Payload. Set by the session-owning layer
	// before a run, alongside ArchiveDir. Model falls back to a.Model when
	// unset.
	HookMeta hooks.Meta

	// turnTools accumulates the tool names dispatched during the current turn,
	// surfaced to the Stop hook as tools_used. Reset at each turn's first user
	// input; read and cleared when Stop fires.
	turnTools []string

	// SessionStarted mirrors the session's durable "SessionStart has fired"
	// flag, seeded by the session-owning layer before the run. The engine's
	// SessionStartDecision uses it (with the process seen-set) to pick
	// startup vs resume; the agent flips it and calls OnSessionStart when
	// startup fires so the layer can persist it.
	SessionStarted bool

	// HookClear, when true, makes the next turn's SessionStart fire with
	// source=clear (set by the session layer right after a /clear). Consumed
	// once, on the next appended user turn.
	HookClear bool

	// OnSessionStart, if set, is invoked when SessionStart fires with
	// source=startup — the seam the session layer uses to persist the durable
	// flag (Session.MarkHookStarted). Runs on the turn goroutine.
	OnSessionStart func()

	// pendingUserBlocks holds content blocks (e.g. images pasted in the TUI)
	// to merge into the next user message. Set via AttachUserBlocks and
	// consumed exactly once by the next appendUserInput, alongside the text.
	pendingUserBlocks []ContentBlock

	// pendingUserCreatedAt, when non-zero, is the timestamp the next appended
	// user message must carry instead of a fresh time.Now(). Set via
	// AttachUserCreatedAt by a caller (the web server) that already stamped the
	// same message for a live broadcast, so the persisted copy shares that exact
	// created_at — the dedup key the frontend matches on. Consumed once.
	pendingUserCreatedAt time.Time

	// turnIterations is the number of provider round-trips (loop iterations)
	// executed during the most recent Run/RunStream call. It is set when the
	// runLoop returns so callers can surface it in UI (e.g. "3 iterations").
	turnIterations int

	// recentToolCalls tracks the fingerprints of the last few tool-use batches
	// so runLoop can detect when the model is stuck repeating the same call(s).
	// Guarded by the implicit serialisation of runLoop (single goroutine).
	recentToolCalls [][]toolCallFingerprint
}

// toolCallFingerprint is a lightweight hash of a single tool-use block.
type toolCallFingerprint struct {
	name     string
	argsHash string // hex-encoded SHA-256 of the JSON-serialised Input map
}

// fingerprintToolUseBlock hashes a tool_use ContentBlock into a comparable
// fingerprint. Empty string on blocks that aren't tool_use.
func fingerprintToolUseBlock(b ContentBlock) toolCallFingerprint {
	if b.Type != "tool_use" {
		return toolCallFingerprint{}
	}
	// Input is a map[string]any; deterministically marshal to JSON for hashing.
	// We don't need crypto strength, just collision resistance for debugging.
	data, _ := json.Marshal(b.Input)
	h := sha256.Sum256(data)
	return toolCallFingerprint{
		name:     b.Name,
		argsHash: hex.EncodeToString(h[:]),
	}
}

// observationToolNames are tools whose only purpose is to inspect state
// started by earlier work (background process output, detached workflow runs).
// Repeating them while waiting for a long-running process is normal behaviour,
// not a stuck loop, so the stuck detector ignores them when fingerprinting a
// batch. The tools themselves still have their own anti-polling guards.
var observationToolNames = map[string]bool{
	"terminal_output": true,
	"workflow_status": true,
}

// fingerprintToolUseBatch hashes an ordered slice of tool_use blocks.
// Observation-only tools are omitted from the fingerprint so that repeatedly
// checking on a background process (e.g. terminal_output on the same id)
// does not trip the duplicate-tool-call loop detector.
func fingerprintToolUseBatch(blocks []ContentBlock) []toolCallFingerprint {
	out := make([]toolCallFingerprint, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "tool_use" && !observationToolNames[b.Name] {
			out = append(out, fingerprintToolUseBlock(b))
		}
	}
	return out
}

// hasConsecutiveDuplicates reports whether the last `window` entries in
// recentToolCalls are all identical to the current batch. The window size
// determines how many consecutive repeats trigger the stuck detector.
func hasConsecutiveDuplicates(recent [][]toolCallFingerprint, current []toolCallFingerprint, window int) bool {
	if len(recent) < window || len(current) == 0 {
		return false
	}
	for i := len(recent) - window; i < len(recent); i++ {
		if !slicesEqual(recent[i], current) {
			return false
		}
	}
	return true
}

func slicesEqual(a, b []toolCallFingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AttachUserBlocks queues content blocks — typically image blocks — to be
// folded into the next user message appended by a Turn/Run/RunStream call.
// The blocks are consumed exactly once (by the next appendUserInput) and then
// cleared. Call it immediately before the run so the text and the attachments
// land on the same user turn. Passing nil clears any queued blocks.
func (a *Agent) AttachUserBlocks(blocks []ContentBlock) {
	a.pendingUserBlocks = blocks
}

// AttachUserCreatedAt pins the timestamp the next appended user message will
// carry, so a caller that pre-stamped the same message (the web server, which
// broadcasts a live created_at before the turn) gets an identical persisted
// timestamp rather than a second, later time.Now(). Consumed once by the next
// appendUserInput. Mirrors AttachUserBlocks.
func (a *Agent) AttachUserCreatedAt(t time.Time) {
	a.pendingUserCreatedAt = t
}

// hookPayload seeds a hook Payload from the Agent's HookMeta, defaulting Model
// to the live a.Model when the session layer left it unset.
func (a *Agent) hookPayload(event hooks.Event) hooks.Payload {
	m := a.HookMeta
	if m.Model == "" {
		m.Model = a.Model
	}
	return m.Payload(event)
}

// appendUserInput appends userInput to history, first prepending any
// UserPromptSubmit hook output (the memory reminder + any shell retrieval
// context, unified through the engine). It stays a single appended message so
// the error-path popLast contract in Turn/TurnStream/runLoop still removes
// exactly one turn. It also re-arms per-turn hook state (the tools_used
// accumulator surfaced to Stop).
func (a *Agent) appendUserInput(ctx context.Context, userInput string) {
	a.turnTools = nil
	// Stamp the turn's timestamp BEFORE any hook work. The server pre-builds the
	// same user message to broadcast a live created_at and relies on this
	// appended copy carrying an identical timestamp (the WS dedup key); letting
	// hook-dispatch latency sit between them would push this stamp into a later
	// millisecond and double-render the message. See ws_handlers user-message
	// build. When a caller pre-stamped the message (AttachUserCreatedAt) reuse
	// that exact timestamp; otherwise stamp now.
	createdAt := a.pendingUserCreatedAt
	a.pendingUserCreatedAt = time.Time{}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	text := userInput
	if a.Hooks != nil {
		var injected []string

		// SessionStart fires once per session opening. SessionStartDecision
		// self-gates on the process seen-set + the durable SessionStarted flag,
		// so attempting it every turn is cheap and only fires on the first
		// (startup/resume) or after a /clear. Its output rides this first user
		// message and thus persists — which is what makes it visible on later
		// turns even though serve rebuilds the Agent each turn.
		if src, fire := a.Hooks.SessionStartDecision(a.HookMeta.SessionID, a.SessionStarted, a.HookClear); fire {
			a.HookClear = false
			if src == hooks.SourceStartup {
				a.SessionStarted = true
				if a.OnSessionStart != nil {
					a.OnSessionStart()
				}
			}
			sp := a.hookPayload(hooks.EventSessionStart)
			sp.Source = src
			if s := a.Hooks.Inject(ctx, sp); s != "" {
				injected = append(injected, s)
			}
		}

		// UserPromptSubmit fires every turn (fresh retrieval / memory reminder).
		up := a.hookPayload(hooks.EventUserPromptSubmit)
		up.UserInput = userInput
		if s := a.Hooks.Inject(ctx, up); s != "" {
			injected = append(injected, s)
		}

		if len(injected) > 0 {
			text = strings.Join(injected, "\n\n") + "\n\n" + userInput
		}
	}
	// Attachments (e.g. a pasted image) ride on the same user turn as the
	// text. Consume them exactly once: build a multi-part message with an
	// optional leading text block followed by the attachment blocks.
	if len(a.pendingUserBlocks) > 0 {
		blocks := make([]ContentBlock, 0, 1+len(a.pendingUserBlocks))
		if text != "" {
			blocks = append(blocks, NewTextBlock(text))
		}
		blocks = append(blocks, a.pendingUserBlocks...)
		a.pendingUserBlocks = nil
		a.History.Append(Message{Role: RoleUser, Blocks: blocks, CreatedAt: createdAt})
		return
	}
	msg := NewUserMessage(text)
	msg.CreatedAt = createdAt
	a.History.Append(msg)
}

// StopReason sentinels set on the Reply when a loop budget is exhausted.
// They are NOT provider stop reasons — the agent synthesises them so callers
// can distinguish "the model finished" from "we cut it off".
const (
	StopReasonMaxTurns    = "max_turns"
	StopReasonInterrupted = "interrupted"
	// StopReasonMaxTokens is the canonical output-truncation sentinel. Provider
	// adapters normalise their wire value to it (Anthropic "max_tokens", OpenAI
	// "length") so the loop checks one thing. The loop also reuses it as the
	// synthetic StopReason when a turn is ended because the response stayed
	// truncated even after escalation. See dev-docs/truncation-recovery.md.
	StopReasonMaxTokens = "max_tokens"
	// StopReasonStuck is set when the agentic loop detects consecutive duplicate
	// tool calls — a sign the model is stuck in a loop with no progress. The run
	// ends gracefully so the caller can intervene (e.g. prompt the user or retry
	// with a different strategy) rather than burning the full turn budget.
	StopReasonStuck = "stuck"
)

// UserFacingError strips internal agent-loop, dispatch, and provider prefixes
// from an error for display to end users. For example:
//
//	"agent: loop[0]: anthropic: HTTP 403 ..." → "HTTP 403 ..."
//	"agent: dispatch tools[1]: openai: HTTP 429 ..." → "HTTP 429 ..."
func UserFacingError(err error) string {
	msg := err.Error()
	// Strip "agent: loop[0]: ", "agent: dispatch tools[1]: ", etc.
	prefix := "agent: "
	if strings.HasPrefix(msg, prefix) {
		rest := msg[len(prefix):]
		if idx := strings.Index(rest, ": "); idx >= 0 {
			msg = rest[idx+2:]
		}
	}
	// Strip known provider prefixes ("anthropic: ", "openai: ") so the user sees
	// "HTTP 403 (permission_error): ..." instead of "anthropic: HTTP 403 ...".
	msg = stripProviderPrefix(msg)
	return msg
}

// knownProviderPrefixes are the provider names used as error-message prefixes
// in internal/provider/*. Adding a new provider should add its name here so
// its error messages are user-friendly.
var knownProviderPrefixes = []string{"anthropic", "openai"}

// stripProviderPrefix removes a leading "<provider>: " from msg if the
// provider is in knownProviderPrefixes.
func stripProviderPrefix(msg string) string {
	for _, p := range knownProviderPrefixes {
		if strings.HasPrefix(msg, p+": ") {
			return msg[len(p)+2:]
		}
	}
	return msg
}

// interruptNote caps an interrupted turn as an assistant message so history
// keeps the user/assistant alternation the next turn depends on.
const interruptNote = "[Interrupted by user.]"

// New constructs an Agent with a fresh History.
//
// Required: sender (otherwise Turn returns an error), model (otherwise the
// provider rejects the request). System and MaxTokens are optional.
func New(sender Sender, model string) *Agent {
	return &Agent{
		Model:   model,
		History: NewHistory(),
		Sender:  sender,
	}
}

// GetSender returns the agent's current sender under a read lock. Callers that
// need a consistent sender across multiple calls (e.g. type-asserting to
// StreamingSender AND calling SendMessages) should capture the returned value
// once and use that snapshot.
func (a *Agent) GetSender() Sender {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Sender
}

// SetSender swaps the agent's sender under a write lock. Used by the TUI's
// /model and /thinking commands to rebuild the sender when the provider or
// base URL changes.
func (a *Agent) SetSender(s Sender) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Sender = s
}

// Turn appends the user's input to history, asks the Sender for a reply,
// appends the reply to history, and returns it. Errors leave History
// unchanged from before the call.
func (a *Agent) Turn(ctx context.Context, userInput string) (Reply, error) {
	return a.turn(ctx, userInput, false)
}

// turn is Turn plus the finishInterrupt contract selector — see turnStream
// for the two cancellation contracts. Run's no-tools fallbacks pass true so
// an interrupted Run keeps the input regardless of tool availability; direct
// Turn callers pass false and keep the pop-on-error retry contract.
func (a *Agent) turn(ctx context.Context, userInput string, finishInterrupt bool) (Reply, error) {
	sender := a.GetSender()
	if sender == nil {
		return Reply{}, fmt.Errorf("agent: no Sender configured")
	}
	if a.Model == "" {
		return Reply{}, fmt.Errorf("agent: Model is required")
	}
	if userInput == "" && len(a.pendingUserBlocks) == 0 {
		return Reply{}, fmt.Errorf("agent: userInput must be non-empty")
	}

	// Append user message first so the snapshot the Sender sees includes it.
	a.appendUserInput(ctx, userInput)

	a.ResetGoalBaseline()
	reply, err := sender.SendMessages(ctx, a.Model, a.System, a.History.Snapshot(), a.MaxTokens)
	if err != nil {
		if finishInterrupt && ctx.Err() != nil {
			return a.finishInterrupted(nil)
		}
		// Pop the user message we just appended so retrying with the same
		// History doesn't duplicate it. Cheaper than transactional locking
		// since History is only mutated from this goroutine in M1.2.
		a.History.popLast()
		return Reply{}, fmt.Errorf("agent: send: %w", err)
	}

	a.History.Append(assistantReplyMessage(reply))
	a.accrueUsage(reply)
	a.accountGoalUsage(nil)
	return reply, nil
}

// TurnStream is the streaming counterpart of Turn. It appends the user input
// to history, calls the Sender (streaming if supported, otherwise falling
// back to SendMessages), invokes onChunk for each text delta, appends the
// final assistant reply to history, and returns it.
//
// onChunk may be nil, in which case the stream is still consumed end-to-end
// but no per-delta callback fires — useful for tests and for callers that
// only want the aggregated Reply.
//
// On error, the user message is popped from History (same contract as Turn),
// so a retry with the same History doesn't duplicate the user turn.
func (a *Agent) TurnStream(
	ctx context.Context,
	userInput string,
	onChunk func(textDelta string),
	onThinking func(thinkingDelta string),
) (Reply, error) {
	return a.turnStream(ctx, userInput, onChunk, onThinking, nil, false)
}

// turnStream is TurnStream plus an optional event handler, so the RunStream
// no-tools fallback keeps emitting EventGoalUpdated for goal accounting.
// Direct TurnStream callers have no event channel and pass nil.
//
// finishInterrupt selects the cancellation contract: RunStream-driven calls
// pass true so an interrupt finalizes via finishInterrupted (user input kept,
// capped with a note, EventTurnDone emitted) exactly like the tool loop;
// direct TurnStream/Turn callers pass false and keep the pop-on-error retry
// contract.
func (a *Agent) turnStream(
	ctx context.Context,
	userInput string,
	onChunk func(textDelta string),
	onThinking func(thinkingDelta string),
	handler EventHandler,
	finishInterrupt bool,
) (Reply, error) {
	sender := a.GetSender()
	if sender == nil {
		return Reply{}, fmt.Errorf("agent: no Sender configured")
	}
	if a.Model == "" {
		return Reply{}, fmt.Errorf("agent: Model is required")
	}
	if userInput == "" && len(a.pendingUserBlocks) == 0 {
		return Reply{}, fmt.Errorf("agent: userInput must be non-empty")
	}

	a.appendUserInput(ctx, userInput)

	a.ResetGoalBaseline()
	var (
		reply Reply
		err   error
	)
	if ss, ok := sender.(StreamingSender); ok {
		reply, err = ss.StreamMessages(ctx, a.Model, a.System, a.History.Snapshot(), a.MaxTokens, onChunk, onThinking)
	} else {
		// Fallback: buffer the call and surface a single "chunk" with the
		// full content. Keeps callers from having to branch on capability
		// at the cost of losing real-time visibility on this backend.
		reply, err = sender.SendMessages(ctx, a.Model, a.System, a.History.Snapshot(), a.MaxTokens)
		if err == nil && onChunk != nil && reply.Content != "" {
			onChunk(reply.Content)
		}
	}
	if err != nil {
		if finishInterrupt && ctx.Err() != nil {
			return a.finishInterrupted(handler)
		}
		a.History.popLast()
		return Reply{}, fmt.Errorf("agent: stream: %w", err)
	}

	a.History.Append(assistantReplyMessage(reply))
	a.accrueUsage(reply)
	a.accountGoalUsage(handler)
	return reply, nil
}

// defaultMaxTurns is the fallback per-Run loop cap when Agent.MaxTurns is
// unset (0). A "turn" here is one provider round-trip inside the agentic
// loop; the cap stops a misbehaving model from looping on tools forever.
const defaultMaxTurns = 200

// unlimitedTurns signals no cap (used for unattended runs).
const unlimitedTurns = -1

// maxTruncationResumes caps how many times layer-2 resume-and-chunk fires per
// run. Each resume appends the partial reply + a recovery prompt, consuming
// another iteration of the loop budget. Three resumes is enough for most
// large prose without risking an infinite loop.
const maxTruncationResumes = 3

// truncationResumePrompt is injected as a user message when a text reply is
// cut off by the output-token cap and escalation (layer 1) didn't help.
// The model sees its own partial output in history and is asked to continue
// from exactly where it left off.
const truncationResumePrompt = "You were cut off mid-thought. Continue exactly where you left off and complete your response. Do not repeat what you've already written."

// Run is the agentic loop: it appends the user message to history then
// repeatedly calls the provider until the model reaches end_turn (no more
// tool calls) or the iteration cap is hit. Run is the buffered, no-event
// counterpart of RunStream — both drive the same runLoop, Run with a nil
// handler so no AgentEvents are emitted.
//
// If tools is nil or executor is nil, Run is equivalent to Turn (single-turn,
// no tool dispatch).
func (a *Agent) Run(ctx context.Context, userInput string, tools []ToolDefinition, executor ToolExecutor) (reply Reply, err error) {
	sender := a.GetSender()
	if sender == nil {
		return Reply{}, fmt.Errorf("agent: no Sender configured")
	}
	if a.Model == "" {
		return Reply{}, fmt.Errorf("agent: Model is required")
	}
	if userInput == "" && len(a.pendingUserBlocks) == 0 {
		return Reply{}, fmt.Errorf("agent: userInput must be non-empty")
	}

	// Fire Stop once the turn concludes — success or failure. Registered after
	// validation so config errors (no turn ran) don't emit a spurious Stop. The
	// closure reads the final named returns.
	defer func() { a.fireStop(userInput, reply, err) }()

	// No tools (or a Sender that can't do tools) → plain single-shot turn,
	// with the loop's interrupt contract (input kept, capped with a note) so
	// an interrupted Run ends the same whether tools were available or not.
	if len(tools) == 0 || executor == nil {
		return a.turn(ctx, userInput, true)
	}
	ts, ok := sender.(ToolSender)
	if !ok {
		return a.turn(ctx, userInput, true)
	}

	// Buffered send + nil handler: runLoop runs the same dispatch/history
	// machinery as the streaming path but emits no events.
	return a.runLoop(ctx, userInput, tools, executor, nil,
		func(ctx context.Context, msgs []Message, maxTokens int) (Reply, error) {
			return ts.SendMessagesWithTools(ctx, a.Model, a.System, msgs, maxTokens, tools)
		})
}

// RunStream is the streaming agentic loop. Behaves like Run but emits
// structured AgentEvents to handler as work progresses — text deltas, tool
// start/done/error, and a final EventTurnDone carrying the aggregated Reply.
//
// If tools is nil or executor is nil, RunStream falls back to TurnStream and
// adapts text deltas into EventTextDelta events. handler may be nil, in
// which case events are discarded but the run completes normally.
func (a *Agent) RunStream(
	ctx context.Context,
	userInput string,
	tools []ToolDefinition,
	executor ToolExecutor,
	handler EventHandler,
) (reply Reply, err error) {
	sender := a.GetSender()
	if sender == nil {
		return Reply{}, fmt.Errorf("agent: no Sender configured")
	}
	if a.Model == "" {
		return Reply{}, fmt.Errorf("agent: Model is required")
	}
	if userInput == "" && len(a.pendingUserBlocks) == 0 {
		return Reply{}, fmt.Errorf("agent: userInput must be non-empty")
	}

	// Fire Stop once the turn concludes — success or failure. See Run.
	defer func() { a.fireStop(userInput, reply, err) }()

	// onChunk adapts text deltas from provider streams into EventTextDelta
	// events. Nil-safe; empty deltas are silently dropped.
	onChunk := func(delta string) {
		if handler == nil || delta == "" {
			return
		}
		handler(AgentEvent{Kind: EventTextDelta, Text: delta})
	}

	// onThinking adapts reasoning-trace fragments into EventThinkingDelta
	// events. Nil-safe; empty deltas are dropped. Fires only when the Sender
	// surfaces reasoning at all.
	onThinking := func(delta string) {
		if handler == nil || delta == "" {
			return
		}
		handler(AgentEvent{Kind: EventThinkingDelta, Text: delta})
	}

	// No tools → plain TurnStream with the event-adapting onChunk. The
	// terminal EventTurnDone is fired here so the caller's contract is
	// identical regardless of whether tools were used.
	if len(tools) == 0 || executor == nil {
		reply, err := a.turnStream(ctx, userInput, onChunk, onThinking, handler, true)
		if err == nil && handler != nil {
			r := reply
			handler(AgentEvent{Kind: EventTurnDone, Reply: &r})
		}
		return reply, err
	}

	// Tool-argument JSON fragments → EventToolInputDelta. Nil-safe.
	onToolDelta := func(toolID, toolName, partialJSON string) {
		if handler == nil || partialJSON == "" {
			return
		}
		handler(AgentEvent{
			Kind:       EventToolInputDelta,
			ToolID:     toolID,
			ToolName:   toolName,
			InputDelta: partialJSON,
		})
	}

	// Try ToolStreamingSender first, then fall back to ToolSender (buffered).
	if tss, ok := sender.(ToolStreamingSender); ok {
		return a.runLoop(ctx, userInput, tools, executor, handler,
			func(ctx context.Context, msgs []Message, maxTokens int) (Reply, error) {
				return tss.StreamMessagesWithTools(ctx, a.Model, a.System, msgs, maxTokens, tools, onChunk, onToolDelta, onThinking)
			})
	}
	if ts, ok := sender.(ToolSender); ok {
		return a.runLoop(ctx, userInput, tools, executor, handler,
			func(ctx context.Context, msgs []Message, maxTokens int) (Reply, error) {
				reply, err := ts.SendMessagesWithTools(ctx, a.Model, a.System, msgs, maxTokens, tools)
				if err == nil && reply.Content != "" {
					onChunk(reply.Content)
				}
				return reply, err
			})
	}

	// Neither tool-aware interface available → plain TurnStream with the
	// event-adapting onChunk. EventTurnDone fires on success.
	reply, err = a.turnStream(ctx, userInput, onChunk, onThinking, handler, true)
	if err == nil && handler != nil {
		r := reply
		handler(AgentEvent{Kind: EventTurnDone, Reply: &r})
	}
	return reply, err
}

// runLoop is the single agentic loop shared by Run and RunStream. The send
// function encapsulates the provider call (streaming or buffered) and is
// responsible for surfacing text deltas itself; this loop owns tool dispatch,
// history bookkeeping, and tool-level event emission.
//
// handler may be nil (the Run path): every emit* helper and the dispatch
// progress path short-circuit on nil, so no events fire.
func (a *Agent) runLoop(
	ctx context.Context,
	userInput string,
	tools []ToolDefinition,
	executor ToolExecutor,
	handler EventHandler,
	send func(ctx context.Context, msgs []Message, maxTokens int) (Reply, error),
) (Reply, error) {
	// Goal accounting brackets the whole turn: the baseline reset pins the
	// counters at turn start (usage accrued between turns is not goal work),
	// and the deferred call catches the final reply and error/interrupt exits
	// (the Codex TaskAborted accounting).
	a.ResetGoalBaseline()
	defer a.accountGoalUsage(handler)

	// Compact older history before starting a new turn, if the last context
	// crossed the threshold. Done here (a safe between-turns boundary, history
	// ends on a complete assistant message) rather than mid-loop, where a
	// tool_use/tool_result pair could be split. A summarization failure is
	// non-fatal — we log nothing and proceed with the full history.
	_ = a.maybeCompact(ctx, handler)

	// Reset per-turn overflow state so a previous turn's failed recovery does
	// not permanently disable overflow recovery for this agent.
	a.overflow.reset()

	// Reset the duplicate-tool-call detector at the start of each user turn.
	// The detector is meant to catch a model looping *within* one turn; letting
	// its fingerprint history bleed across turns means a fresh user message
	// (often "continue") that produces even a single repeat of the prior call
	// trips the stuck-stop immediately, so the user can never break out. A new
	// user turn is a fresh intent — give the model the full within-turn window
	// again.
	a.recentToolCalls = nil

	// History length before this turn appended anything. Used to roll back
	// cleanly on a first-iteration failure: the turn may append more than the
	// user input (drained inbox messages), so truncating to this baseline is
	// what restores the pre-turn state — popLast would only remove one message.
	baseHistoryLen := a.History.Len()
	a.appendUserInput(ctx, userInput)

	limit := a.turnLimit()
	streamStalls := 0      // transient mid-stream stalls re-issued for the current round
	truncationResumes := 0 // layer-2 resume-and-chunk budget
	escalateExhausted := false
	a.turnIterations = 0
	for i := 0; limit == unlimitedTurns || i < limit; i++ {
		a.turnIterations = i + 1
		// Interrupt (Ctrl-C) between iterations — e.g. right after a tool batch.
		if ctx.Err() != nil {
			return a.finishInterrupted(handler)
		}

		// Drain inbox messages that arrived since the last iteration.
		// Done before the LLM call so the model sees mid-turn user input
		// as a first-class message boundary, not folded into tool output.
		if steerItems := a.Inbox.Drain(); len(steerItems) > 0 {
			// History position the first steer item lands at — item k occupies
			// steerBaseIdx+k, since each is appended as its own message below.
			steerBaseIdx := a.History.Len()
			for _, it := range steerItems {
				if len(it.Blocks) > 0 {
					blocks := make([]ContentBlock, 0, 1+len(it.Blocks))
					if it.Text != "" {
						blocks = append(blocks, NewTextBlock(it.Text))
					}
					blocks = append(blocks, it.Blocks...)
					a.History.Append(Message{Role: RoleUser, Blocks: blocks})
				} else {
					a.History.Append(NewUserMessage(it.Text))
				}
			}
			if handler != nil {
				msgs := make([]string, len(steerItems))
				for i, it := range steerItems {
					msgs[i] = it.Text
				}
				handler(AgentEvent{Kind: EventSteerInjected, Messages: msgs, Steer: steerItems, SteerBaseIndex: steerBaseIdx})
			}
		}

		// Defensive check: ensure every tool_use block has a matching tool_result.
		// This handles edge cases where compaction, overflow recovery, or an
		// interrupted dispatchTools left orphaned tool_use blocks. Synthesizing
		// error tool_results prevents Anthropic HTTP 400 errors.
		a.ensureToolPairing()

		reply, err := send(ctx, a.History.Snapshot(), a.MaxTokens)
		if err != nil {
			// Interrupt during the provider call: finalize cleanly rather than
			// surfacing context.Canceled as a turn error.
			if ctx.Err() != nil {
				return a.finishInterrupted(handler)
			}

			// ── Context overflow recovery (aligned with Ruby) ──
			if a.overflow.tryRecover(ctx, a, err, handler) {
				// Compression succeeded — retry the same iteration
				// without incrementing i or popping user message
				continue
			}

			// ── Transient mid-stream stall recovery ──
			// The streaming idle-timeout watchdog (or similar) fired: the server
			// went silent mid-response. The partial reply was never appended, so
			// re-issuing this round from the unchanged history is safe — only the
			// already-streamed text is re-emitted. Bounded so a persistently dead
			// stream still ends the turn.
			if isTransientStreamErr(err) && streamStalls < maxStreamStalls {
				streamStalls++
				if handler != nil {
					handler(AgentEvent{Kind: EventTextDelta, Text: fmt.Sprintf(
						"\n[octo] stream stalled; retrying (%d/%d)…\n", streamStalls, maxStreamStalls)})
				}
				continue
			}

			if i == 0 {
				a.History.TruncateTo(baseHistoryLen)
			}
			return Reply{}, fmt.Errorf("agent: loop[%d]: %w", i, err)
		}

		// Success — reset the overflow and stream-stall budgets for the next round.
		a.overflow.reset()
		streamStalls = 0

		a.accrueUsage(reply)

		// ── Output-truncation recovery (layer 1) ──
		// The response was cut off by the output-token cap. Retry this same
		// round once at the escalated cap, from the UNCHANGED history (the
		// truncated reply is never appended), so a half-written tool call is
		// regenerated with more room — no provider-specific partial-tool_use
		// handling needed. Fires only when escalation raises the cap.
		// See dev-docs/truncation-recovery.md.
		if isTruncated(reply.StopReason) && !escalateExhausted && a.MaxTokensEscalate > a.MaxTokens {
			escalated, eerr := send(ctx, a.History.Snapshot(), a.MaxTokensEscalate)
			switch {
			case eerr == nil:
				reply = escalated
				a.accrueUsage(reply)
				truncationResumes = 0 // escalation solved it — reset resume budget
				escalateExhausted = false
			case ctx.Err() != nil:
				return a.finishInterrupted(handler)
			case isMaxTokensTooLargeErr(eerr):
				// The model's ceiling is below the escalation target (e.g.
				// Claude 3 caps at 4096). Keep the truncated reply and fall
				// through to the graceful stop below.
			default:
				if i == 0 {
					a.History.TruncateTo(baseHistoryLen)
				}
				return Reply{}, fmt.Errorf("agent: loop[%d] escalate: %w", i, eerr)
			}
		}

		// Bill this round's usage to the goal while the turn is still running,
		// so a budget crossing surfaces (and, once wired, steers) mid-turn
		// rather than only at the end.
		a.accountGoalUsage(handler)

		// ── Output-truncation recovery (layer 2) ──
		// When escalation was attempted (cap raised) but the reply is still
		// truncated, keep the partial text in history and prompt the model to
		// continue. This covers long prose replies that exceed even the escalated
		// cap. Limited to maxTruncationResumes to prevent infinite loops. Skipped
		// for truncated tool_use blocks (partial tool calls are unsafe in
		// OpenAI-protocol history) and when escalation is disabled.
		// A truncated reply can carry both leading text and a half-written
		// tool_use block (providers keep StopReason "max_tokens" rather than
		// normalising to "tool_use"). reply.Content holds only the text, so the
		// prose-resume below would append the text and silently drop the partial
		// tool call. Detect any tool_use block and fall through to the graceful
		// stop instead — partial tool calls are unsafe to resume.
		replyHasToolUse := false
		for _, b := range reply.Blocks {
			if b.Type == "tool_use" {
				replyHasToolUse = true
				break
			}
		}
		if isTruncated(reply.StopReason) && a.MaxTokensEscalate > a.MaxTokens && !replyHasToolUse && reply.Content != "" && truncationResumes < maxTruncationResumes {
			a.History.Append(NewAssistantMessage(reply.Content))
			a.History.Append(NewUserMessage(truncationResumePrompt))
			truncationResumes++
			escalateExhausted = true
			if handler != nil {
				handler(AgentEvent{Kind: EventTextDelta, Text: "\n[octo] response truncated — resuming…\n"})
			}
			continue
		}

		// Still truncated and ineligible for layer-2 resume (tool_use truncation
		// or resume budget exhausted): end the turn cleanly rather than dispatching
		// a half-formed tool call or returning an empty reply.
		if isTruncated(reply.StopReason) {
			return a.budgetStop(handler, StopReasonMaxTokens,
				"[octo] Stopped: the response was truncated at the output-token cap. "+
					"Raise --max-tokens / --max-tokens-escalate, or ask me to continue in smaller steps.")
		}

		if reply.StopReason == "tool_use" {
			a.History.Append(NewToolUseMessage(reply.Blocks))

			// ── Duplicate-tool-call loop detection ──
			// If the model keeps issuing the exact same tool_use batch, it is
			// stuck in a loop with no progress. Detect this early and stop
			// gracefully so the caller can intervene instead of burning the
			// full turn budget.
			//
			// Observation-only tools such as terminal_output are exempt: repeatedly
			// checking on a long-running background process is expected behaviour,
			// not a loop. Those tools still enforce their own anti-polling limits.
			const stuckWindow = 4 // require 4 consecutive repeats (5 identical batches total)
			fp := fingerprintToolUseBatch(reply.Blocks)
			if len(fp) > 0 && hasConsecutiveDuplicates(a.recentToolCalls, fp, stuckWindow) {
				return a.budgetStop(handler, StopReasonStuck,
					"[octo] Stopped: detected repeated tool calls without progress. "+
						"The model appears to be stuck in a loop. "+
						"Send another message to continue with a different approach.")
			}
			if len(fp) > 0 {
				a.recentToolCalls = append(a.recentToolCalls, fp)
				if len(a.recentToolCalls) > stuckWindow+1 {
					a.recentToolCalls = a.recentToolCalls[len(a.recentToolCalls)-stuckWindow-1:]
				}
			}

			// Emit EventToolStarted before dispatch so observers see the
			// "thinking → tool call" boundary even if the tool blocks.
			emitToolStartedEvents(handler, reply.Blocks)

			// handler is threaded through to dispatchTools so streaming
			// tools (StreamingToolExecutor) can fire EventToolProgress as
			// output arrives mid-execution.
			resultBlocks, err := dispatchTools(ctx, executor, reply.Blocks, handler, a.effectiveGate())
			if err != nil {
				return Reply{}, fmt.Errorf("agent: dispatch tools[%d]: %w", i, err)
			}

			// Record the tool names dispatched this turn for the Stop hook's
			// tools_used field.
			for _, b := range reply.Blocks {
				if b.Type == "tool_use" {
					a.turnTools = append(a.turnTools, b.Name)
				}
			}

			// Decorate results before events and history so the model and
			// the persisted session see the same text. UI events strip the
			// <system-reminder> spans (emitToolResultEvents) — hook output
			// is model-facing, not part of the tool's visible result.
			a.applyPostToolUse(ctx, reply.Blocks, resultBlocks)

			// Emit EventToolDone / EventToolError per result, pairing
			// each result with the originating tool_use block so ToolName
			// can be carried through (tool_result blocks don't carry it
			// themselves).
			emitToolResultEvents(handler, reply.Blocks, resultBlocks)

			a.History.Append(NewToolResultMessage(resultBlocks))

			// Turn-in compaction check: after a tool batch, history may have
			// grown significantly. If the estimated size is near the window,
			// compact before the next LLM call to avoid a 400.
			if a.shouldCompactBetweenBatches() {
				_ = a.maybeCompact(ctx, handler)
			}
			continue
		}

		content := reply.Content
		if content == "" {
			content = textFromBlocks(reply.Blocks)
		}
		a.History.Append(assistantReplyMessage(reply))
		reply.Content = content

		// A mid-turn steer (text and/or pasted images) arrived while the model
		// was producing this answer. Don't end the turn: loop so the next
		// iteration drains it into history and the model responds in-turn,
		// rather than stranding it for the front-end to re-queue as a fresh
		// turn (which would drop any image blocks). EventTurnDone stays
		// once-only — it fires only when we actually return below.
		if a.Inbox.HasPending() {
			continue
		}

		if handler != nil {
			r := reply
			handler(AgentEvent{Kind: EventTurnDone, Reply: &r})
		}
		a.turnIterations = i + 1
		return reply, nil
	}

	// Loop cap reached while the model still wanted to keep going. End the
	// run gracefully rather than erroring — the history holds the partial
	// progress and the caller gets a clear, non-fatal explanation.
	// turnIterations already holds the last loop index (set at the top of each
	// iteration), so the UI reports the actual count before the cap.
	return a.budgetStop(handler, StopReasonMaxTurns, fmt.Sprintf(
		"[octo] Stopped: reached the max-turns limit (%d). The task may be incomplete — "+
			"raise --max-turns or send another message to continue.", limit))
}

// finishInterrupted finalizes a turn cut short by context cancellation
// (Ctrl-C) so the history stays well-formed for the next turn, then returns
// context.Canceled so the caller can recognise the interrupt. It restores the
// user/assistant alternation invariant:
//
//   - last message is user-role (unanswered input or a tool_result) → cap with
//     an assistant note, so the next user turn doesn't produce two user
//     messages in a row. The unanswered input is deliberately kept: dropping
//     it shrank persisted history below the turn-start watermark, and every
//     UI that re-renders from disk after the turn (web history_reload) lost
//     the message the user just sent — a fresh session went fully blank.
//   - last message is an assistant(tool_use) without tool_result → synthesize
//     error tool_results and cap with an assistant note
//   - last message is already a plain assistant turn → nothing to do
//
// The synthesized tool_result blocks for interrupted tool calls are normally
// produced by dispatchTools itself (cancelled executions become is_error
// results). This function handles the edge case where dispatchTools didn't
// complete before the interrupt was detected.
func (a *Agent) finishInterrupted(handler EventHandler) (Reply, error) {
	msgs := a.History.Snapshot()
	if n := len(msgs); n > 0 {
		last := msgs[n-1]
		switch {
		case last.Role == RoleAssistant && hasToolUse(last):
			// Orphaned assistant(tool_use) — dispatchTools didn't produce results.
			// Synthesize error tool_results so the next send() doesn't 400.
			results := synthesizeInterruptedToolResults(last.Blocks)
			if len(results) > 0 {
				a.History.Append(NewToolResultMessage(results))
			}
			a.History.Append(NewAssistantMessage(interruptNote))
		case last.Role == RoleUser:
			a.History.Append(NewAssistantMessage(interruptNote))
		}
	}
	reply := Reply{Content: interruptNote, StopReason: StopReasonInterrupted}
	if handler != nil {
		r := reply
		handler(AgentEvent{Kind: EventTurnDone, Reply: &r})
	}
	// turnIterations stays at its current value (set by the caller in runLoop
	// before finishInterrupted is reached) so the UI shows how many iterations
	// completed before the interrupt.
	return reply, context.Canceled
}

// TakeBackInterrupted undoes an interrupt that produced no output: when
// history ends with the assistant interrupt note sitting directly on a plain
// user message (no tool_results — nothing ran), both are removed and true is
// returned. UIs that recall the interrupted input into their compose box for
// editing (the TUI's Esc take-back) call this after the turn winds down, so
// the recalled text doesn't also linger in context as a ghost message. Any
// other tail shape means the turn made observable progress; it is left
// untouched and false is returned.
func (a *Agent) TakeBackInterrupted() bool {
	msgs := a.History.Snapshot()
	n := len(msgs)
	if n < 2 {
		return false
	}
	last, prev := msgs[n-1], msgs[n-2]
	if last.Role != RoleAssistant || last.Content != interruptNote {
		return false
	}
	if prev.Role != RoleUser || hasToolResult(prev) {
		return false
	}
	a.History.TruncateTo(n - 2)
	return true
}

// TurnIterations returns the number of provider round-trips executed during
// the most recent Run/RunStream call. It is 0 before the first run.
func (a *Agent) TurnIterations() int {
	return a.turnIterations
}

// turnLimit resolves the per-Run loop cap.
//
//	> 0  → explicit cap
//	0    → defaultMaxTurns (interactive default)
//	< 0  → unlimited (unattended runs)
func (a *Agent) turnLimit() int {
	if a.MaxTurns > 0 {
		return a.MaxTurns
	}
	if a.MaxTurns < 0 {
		return unlimitedTurns
	}
	return defaultMaxTurns
}

// budgetStop ends a run that hit a loop budget (turns or cost). It appends a
// synthetic assistant message so history stays well-formed, surfaces the
// message as a text delta + turn_done event (so streaming callers render it
// like normal reply text), and returns a Reply carrying the budget StopReason
// — never an error, so the partial progress isn't discarded.
func (a *Agent) budgetStop(handler EventHandler, reason, msg string) (Reply, error) {
	a.History.Append(NewAssistantMessage(msg))
	reply := Reply{Content: msg, StopReason: reason}
	if handler != nil {
		handler(AgentEvent{Kind: EventTextDelta, Text: msg})
		r := reply
		handler(AgentEvent{Kind: EventTurnDone, Reply: &r})
	}
	return reply, nil
}

// isTruncated reports whether a reply was cut off by the output-token cap.
// Adapters normalise their wire value to StopReasonMaxTokens, so the loop only
// checks this one sentinel.
func isTruncated(stopReason string) bool {
	return stopReason == StopReasonMaxTokens
}

// maxStreamStalls bounds how many times a single round may be re-issued after a
// transient mid-stream stall before the turn is failed. Reset after any healthy
// round, so a long turn that stalls at different points each gets a fresh budget.
const maxStreamStalls = 3

// transientStreamError is implemented by provider errors that represent a
// recoverable mid-stream stall (e.g. the streaming idle-timeout watchdog
// firing). Declaring it as an interface here lets the loop classify such errors
// without the agent package importing any provider package — Go interface
// satisfaction is structural, so the provider error just needs the method.
type transientStreamError interface{ TransientStream() bool }

// isTransientStreamErr reports whether err (or anything it wraps) is a
// recoverable mid-stream stall. The round can be safely re-issued because the
// partial reply was never appended to history.
func isTransientStreamErr(err error) bool {
	var t transientStreamError
	return errors.As(err, &t) && t.TransientStream()
}

// suggestMaxTokens caps the follow-up suggestion response — it's one short line.
const suggestMaxTokens = 256

const suggestInstruction = "Suggest ONE concise, specific next message I (the user) might send to continue this work. " +
	"Do not call any tools — reply with the message text only, phrased as I would type it, a single line, " +
	"no preamble, no quotes, no numbering."

// Suggest produces a single follow-up message the user might want to send next,
// based on the conversation so far. It is a throwaway provider call: the
// instruction is appended to a snapshot of history (never to the live History),
// so it doesn't pollute the conversation, and its token usage is not accrued
// into the session. Returns "" (no error) when there's nothing to suggest.
//
// tools should be the SAME toolbelt the agentic loop uses. Anthropic's cache
// prefix is ordered tools → system → messages, so sending the identical tools
// makes this call reuse the main conversation's prompt cache (the whole history
// is billed at the cheap cache-read rate) instead of re-billing it in full.
// Without tools the prefix diverges at block 0 and nothing is cached. The model
// is told not to call tools; if it returns a tool_use anyway, Content is empty
// and we simply produce no suggestion that turn.
func (a *Agent) Suggest(ctx context.Context, tools []ToolDefinition) (string, error) {
	sender := a.GetSender()
	if sender == nil || a.Model == "" {
		return "", fmt.Errorf("agent: suggest: not configured")
	}
	snap := a.History.Snapshot()
	if len(snap) == 0 {
		return "", nil
	}
	msgs := make([]Message, 0, len(snap)+1)
	msgs = append(msgs, snap...)
	msgs = append(msgs, NewUserMessage(suggestInstruction))

	// Prefer NoReasoning, fall back to LowEffort: a one-line follow-up
	// suggestion needs no reasoning, and even "low" effort wastes time and
	// tokens that can push this throwaway call past its timeout on a slow
	// provider (same logic as GenerateTitle).
	if nr, ok := sender.(NoReasoningSender); ok {
		sender = nr.NoReasoning()
	} else if le, ok := sender.(LowEffortSender); ok {
		sender = le.LowEffort()
	}

	var reply Reply
	var err error
	if ts, ok := sender.(ToolSender); ok && len(tools) > 0 {
		reply, err = ts.SendMessagesWithTools(ctx, a.Model, a.System, msgs, suggestMaxTokens, tools)
	} else {
		reply, err = sender.SendMessages(ctx, a.Model, a.System, msgs, suggestMaxTokens)
	}
	if err != nil {
		return "", err
	}
	return cleanSuggestion(reply.Content), nil
}

// titleMaxTokens caps the title response. The title itself is a handful of
// words; a tiny cap is enough because LowEffortSender now disables reasoning
// outright for this throwaway call, so the budget only has to cover the title
// line itself.
const titleMaxTokens = 250

// TitleGenerationTimeout bounds a throwaway session-title call. The TUI and
// the server share one title mechanism: within this budget the model produces
// a title, otherwise GenerateTitleOrSnippet falls back to a message snippet,
// so a title always lands ~5s after the first user message.
const TitleGenerationTimeout = 5 * time.Second

// titleContextMaxRunes caps how much of the first user message feeds the
// title prompt. A title only needs the topic; an unbounded paste (a dumped
// log, a whole file) would turn this deliberately-cheap call into a huge
// request — exactly what the lite model is meant to avoid.
const titleContextMaxRunes = 500

// titleInstruction demands brevity twice — in words for spacey languages, in
// characters for CJK — because a sidebar title wraps or truncates past a
// handful of words. 8 words / 25 characters mirrors FirstUserSnippet's
// truncation budget, so a model title and the snippet fallback land at the
// same length.
const titleInstruction = "Generate a very short title for this conversation — at most 8 words, or 25 characters for Chinese or Japanese. " +
	"Reply with the title text only — no preamble, no quotes, no trailing punctuation, no markdown."

// GenerateTitle produces a short title for the conversation so far, for
// display in the session list. It is a throwaway provider call: the request
// carries only the first user message plus the instruction (never the live
// History, no system prompt, no tools), runs on the lite model when one is
// configured, and its token usage is not accrued into the session. Returns ""
// (no error) when there's no user text to title.
func (a *Agent) GenerateTitle(ctx context.Context) (string, error) {
	return a.GenerateTitleFrom(ctx, a.History.Snapshot())
}

// GenerateTitleFrom is GenerateTitle over an explicit message snapshot. It
// exists for title-on-receipt callers: when a turn starts, the loop goroutine
// owns History and hasn't appended the incoming user message yet, so the
// caller passes its own pre-turn snapshot plus that message instead.
//
// The call runs on LiteSender/LiteModel when set, otherwise on the primary
// sender, and there is NO retry on the primary after a lite failure — the
// GenerateTitleOrSnippet snippet fallback already guarantees a title, and a
// retry would double the latency of a call bounded by TitleGenerationTimeout.
func (a *Agent) GenerateTitleFrom(ctx context.Context, snap []Message) (string, error) {
	sender, model := a.GetSender(), a.Model
	if a.LiteSender != nil && a.LiteModel != "" {
		sender, model = a.LiteSender, a.LiteModel
	}
	if sender == nil || model == "" {
		return "", fmt.Errorf("agent: title: not configured")
	}
	text := firstUserText(snap)
	if text == "" {
		return "", nil
	}
	if r := []rune(text); len(r) > titleContextMaxRunes {
		text = string(r[:titleContextMaxRunes])
	}
	msgs := []Message{NewUserMessage(text), NewUserMessage(titleInstruction)}

	if nr, ok := sender.(NoReasoningSender); ok {
		sender = nr.NoReasoning()
	} else if le, ok := sender.(LowEffortSender); ok {
		sender = le.LowEffort()
	}

	reply, err := sender.SendMessages(ctx, model, "", msgs, titleMaxTokens)
	if err != nil {
		return "", err
	}
	return cleanTitle(reply.Content), nil
}

// GenerateTitleOrSnippet is GenerateTitleFrom with a guaranteed result: on
// error, timeout, or an empty model reply it falls back to a truncated snippet
// of the first user message in snap. This is THE session-title mechanism —
// the TUI and the server both call it (wrapped in TitleGenerationTimeout) so
// every frontend gets the same behaviour: an LLM title when the call works, a
// snippet otherwise, always within ~5s of the first user message. Returns ""
// only when snap carries no user text at all.
func (a *Agent) GenerateTitleOrSnippet(ctx context.Context, snap []Message) (string, error) {
	t, err := a.GenerateTitleFrom(ctx, snap)
	if err == nil {
		if t = strings.TrimSpace(t); t != "" {
			return t, nil
		}
	}
	return FirstUserSnippet(snap), err
}

// cleanTitle reduces the model's reply to a single tidy line: first non-empty
// line, stripped of surrounding quotes/markdown and trailing punctuation, and
// capped to the same display-width budget as the snippet fallback — one ruler
// for every title that reaches the sidebar.
func cleanTitle(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "# ")
		// The model sometimes wraps the title in quotes AND adds trailing
		// punctuation (e.g. `"Fix the bug".`), so trim both sets repeatedly
		// until the string stops shrinking — one pass leaves the outer quote
		// stranded behind the period.
		for {
			trimmed := strings.TrimSpace(line)
			trimmed = strings.Trim(trimmed, "\"'`*")
			trimmed = strings.TrimRight(trimmed, ".。!！?？")
			if trimmed == line {
				break
			}
			line = trimmed
		}
		if line == "" {
			continue
		}
		return truncateSnippet(line)
	}
	return ""
}

// cleanSuggestion picks the first non-empty line and strips list/quote
// decoration the model sometimes adds despite the instruction.
func cleanSuggestion(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(strings.Trim(line, "\"'`"))
		if line != "" {
			return line
		}
	}
	return ""
}

// isMaxTokensTooLargeErr best-effort detects a provider rejecting an escalated
// max_tokens because it exceeds the model's ceiling (e.g. Claude 3 caps at
// 4096). Both Anthropic and OpenAI-protocol backends name max_tokens in the
// message; the surrounding wording varies, so this matches loosely. On a false
// negative the escalation error simply surfaces as a normal turn error.
func isMaxTokensTooLargeErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "max_tokens") && !strings.Contains(s, "max tokens") {
		return false
	}
	for _, marker := range []string{"exceed", "greater than", "too large", "maximum", "at most", "less than or equal"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// emitToolStartedEvents fires one EventToolStarted per tool_use block.
// handler may be nil — the loop short-circuits cleanly.
func emitToolStartedEvents(handler EventHandler, useBlocks []ContentBlock) {
	if handler == nil {
		return
	}
	for _, b := range useBlocks {
		if b.Type != "tool_use" {
			continue
		}
		handler(AgentEvent{
			Kind:     EventToolStarted,
			ToolID:   b.ID,
			ToolName: b.Name,
			Input:    b.Input,
		})
	}
}

// emitToolResultEvents pairs each tool_result with the originating tool_use
// (matched on ID) so ToolName flows through to the EventDone / EventError
// payload. tool_result blocks don't carry the tool name themselves — this
// pairing is required to keep events fully self-describing for UI consumers.
func emitToolResultEvents(handler EventHandler, useBlocks, resultBlocks []ContentBlock) {
	if handler == nil {
		return
	}
	// Build an id→name index once.
	nameByID := make(map[string]string, len(useBlocks))
	for _, b := range useBlocks {
		if b.Type == "tool_use" {
			nameByID[b.ID] = b.Name
		}
	}
	for _, r := range resultBlocks {
		if r.Type != "tool_result" {
			continue
		}
		ev := AgentEvent{
			ToolID:   r.ToolUseID,
			ToolName: nameByID[r.ToolUseID],
			Output:   truncateOutput(StripRemindersForDisplay(r.Result)),
			UI:       r.UI,
		}
		if r.IsError {
			ev.Kind = EventToolError
			ev.Err = r.Result // full untruncated error message in Err
		} else {
			ev.Kind = EventToolDone
		}
		handler(ev)
	}
}

// readOnlyTools are the built-in tools safe for concurrent dispatch. A batch
// composed entirely of these can be executed concurrently (see dispatchTools).
// Conservative by design: anything that writes, edits, or shells out is absent,
// so adding a new mutating tool can never accidentally be parallelised.
var readOnlyTools = map[string]bool{
	"read_file":  true,
	"glob":       true,
	"grep":       true,
	"web_fetch":  true,
	"web_search": true,
}

// toolCall pairs a tool_use block with the gate's verdict.
type toolCall struct {
	block      ContentBlock
	denyReason string // non-empty → blocked by the gate; don't execute
}

// dispatchTools runs every tool_use block in blocks and returns the matching
// tool_result blocks (order preserved). Errors become IsError results so the
// model can recover.
//
// The permission gate runs first, serially, for every call — an interactive
// "ask" prompt must never race another on stdin. Execution then happens in one
// of two modes:
//   - Parallel, when the batch has >1 executable call and every executable
//     call is concurrency-safe (concurrencySafe): the read-only built-ins the
//     model frequently fires several of at once (read_file/grep/…), plus
//     sub_agent, whose whole point is a parallel fan-out. Running them
//     concurrently cuts latency — and for sub_agent avoids the "7 sub-agents
//     block one another" serialization that a synchronous fan-out would
//     otherwise hit. None of these stream tool-level progress, so no
//     EventToolProgress is lost.
//   - Serial otherwise (a single call, or any mutating/streaming tool present),
//     preserving EventToolProgress for StreamingToolExecutor tools.
//
// handler may be nil (no events); gate may be nil (no gating). Parallel mode
// requires executor.Execute to be safe for concurrent calls on distinct
// inputs — DefaultRegistry is (its only shared state, the read tracker, is
// mutex-guarded).
func dispatchTools(ctx context.Context, executor ToolExecutor, blocks []ContentBlock, handler EventHandler, gate PermissionGate) ([]ContentBlock, error) {
	// Pass 1 — collect calls and run the gate serially.
	var calls []toolCall
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		c := toolCall{block: b}
		if gate != nil {
			if allowed, reason := gate.Check(ctx, b.Name, b.Input); !allowed {
				if reason == "" {
					reason = "permission denied"
				}
				c.denyReason = reason
			}
		}
		calls = append(calls, c)
	}

	// Pass 2 — execute. Each tool may return multiple blocks (e.g. tool_result +
	// image), so we collect per-tool slices and flatten at the end.
	var resultSlices [][]ContentBlock

	if canParallelize(calls) {
		var wg sync.WaitGroup
		resultSlices = make([][]ContentBlock, len(calls))
		for i := range calls {
			if calls[i].denyReason != "" {
				resultSlices[i] = []ContentBlock{NewToolResultBlock(calls[i].block.ID, calls[i].denyReason, true)}
				continue
			}
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				res, err := executor.Execute(ctx, calls[i].block.Name, calls[i].block.Input)
				resultSlices[i] = toolResultBlocks(calls[i].block.ID, res, err)
			}(i)
		}
		wg.Wait()
		return flattenResults(resultSlices), nil
	}

	streaming, hasStreaming := executor.(StreamingToolExecutor)
	resultSlices = make([][]ContentBlock, len(calls))
	for i := range calls {
		b := calls[i].block
		if calls[i].denyReason != "" {
			resultSlices[i] = []ContentBlock{NewToolResultBlock(b.ID, calls[i].denyReason, true)}
			continue
		}
		var (
			res     ToolResult
			execErr error
		)
		if hasStreaming && handler != nil {
			toolID, toolName := b.ID, b.Name
			progress := func(chunk string) {
				if chunk == "" {
					return
				}
				handler(AgentEvent{Kind: EventToolProgress, ToolID: toolID, ToolName: toolName, Chunk: chunk})
			}
			res, execErr = streaming.ExecuteStream(ctx, b.Name, b.Input, progress)
		} else {
			res, execErr = executor.Execute(ctx, b.Name, b.Input)
		}
		resultSlices[i] = toolResultBlocks(b.ID, res, execErr)
	}
	return flattenResults(resultSlices), nil
}

// concurrencySafe reports whether a tool may be dispatched concurrently with
// its siblings in the same turn. It's the read-only built-ins (no side effects)
// plus sub_agent: each sub-agent runs in its own isolated child (fresh history,
// its own agent loop) and the SubAgentManager and token accounting are
// mutex-guarded, so a fan-out of sub_agent calls is safe to run at once —
// running them serially is exactly the "7 sub-agents block one another" latency
// the concurrent path avoids. sub_agent surfaces its activity through its own
// event sink (not EventToolProgress), so the parallel path loses no progress
// events for it either.
func concurrencySafe(name string) bool {
	return readOnlyTools[name] || name == "sub_agent"
}

// canParallelize reports whether a batch can run concurrently: more than one
// executable (non-denied) call, every one a concurrency-safe tool.
func canParallelize(calls []toolCall) bool {
	executable := 0
	for _, c := range calls {
		if c.denyReason != "" {
			continue
		}
		if !concurrencySafe(c.block.Name) {
			return false
		}
		executable++
	}
	return executable > 1
}

// toolResultBlocks builds a slice of content blocks from a ToolResult.
// The first element is always the tool_result block; any additional blocks
// (e.g. images) from the result are appended after it.
func toolResultBlocks(id string, result ToolResult, err error) []ContentBlock {
	if err != nil {
		return []ContentBlock{NewToolResultBlock(id, microCompact(err.Error()), true)}
	}
	blocks := make([]ContentBlock, 0, 1+len(result.Blocks))
	rb := NewToolResultBlock(id, microCompact(result.Text), false)
	rb.UI = result.UI
	blocks = append(blocks, rb)
	blocks = append(blocks, result.Blocks...)
	return blocks
}

// applyPostToolUse fires the PostToolUse hook for each successful tool result
// and appends any output to that tool_result block. It runs serially after
// dispatchTools returns — never inside the parallel read-only batch — so a
// stateful in-process hook (the memory save-nudge latch) needs no locking.
// Denied and errored calls carry IsError=true and are skipped: a failed action
// is not a milestone.
func (a *Agent) applyPostToolUse(ctx context.Context, uses, results []ContentBlock) {
	if a.Hooks == nil || !a.Hooks.Configured(hooks.EventPostToolUse) {
		return
	}
	byID := make(map[string]*ContentBlock, len(results))
	for i := range results {
		if results[i].Type == "tool_result" && !results[i].IsError {
			byID[results[i].ToolUseID] = &results[i]
		}
	}
	for _, u := range uses {
		if u.Type != "tool_use" {
			continue
		}
		rb := byID[u.ID]
		if rb == nil {
			continue
		}
		p := a.hookPayload(hooks.EventPostToolUse)
		p.ToolName = u.Name
		p.ToolInput = u.Input
		p.ToolResult = rb.Result
		if extra := a.Hooks.Inject(ctx, p); extra != "" {
			if rb.Result == "" {
				rb.Result = extra
			} else {
				rb.Result += "\n\n" + extra
			}
		}
	}
}

// fireStop dispatches the Stop hook at a turn's conclusion — on success AND on
// failure/interrupt alike, since a retention layer wants both (a non-nil err
// populates the payload's error field so a script can choose to skip failures).
// userInput is the turn's original input; reply.Content is the final assistant
// text; tools_used is drained from the per-turn accumulator. Dispatch uses a
// background context so interrupting the turn (ctx cancelled) doesn't also
// cancel retention — mirroring the pre-redesign post-turn hook.
func (a *Agent) fireStop(userInput string, reply Reply, err error) {
	tools := a.turnTools
	a.turnTools = nil
	if a.Hooks == nil || !a.Hooks.Configured(hooks.EventStop) {
		return
	}
	p := a.hookPayload(hooks.EventStop)
	p.UserInput = userInput
	p.AssistantReply = reply.Content
	p.ToolsUsed = tools
	if err != nil {
		p.Error = err.Error()
	}
	a.Hooks.Dispatch(context.Background(), p)
}

// firePreCompact dispatches the PreCompact hook just before a real compaction
// fold. Called only once the compaction paths have committed to summarizing
// (past the reclaim-only and anti-thrash no-op returns), so it doesn't fire on
// every near-threshold turn. Pure side-effect (notify / archive) — it cannot
// veto the compaction. Background context so it isn't cancelled with the turn.
func (a *Agent) firePreCompact() {
	if a.Hooks == nil || !a.Hooks.Configured(hooks.EventPreCompact) {
		return
	}
	a.Hooks.Dispatch(context.Background(), a.hookPayload(hooks.EventPreCompact))
}

// flattenResults collapses a per-tool slice-of-slices into a single flat slice.
func flattenResults(slices [][]ContentBlock) []ContentBlock {
	var total int
	for _, s := range slices {
		total += len(s)
	}
	out := make([]ContentBlock, 0, total)
	for _, s := range slices {
		out = append(out, s...)
	}
	return out
}

// assistantReplyMessage builds the assistant history message for a completed
// turn, preserving any reasoning trace (a "thinking" block with its signature)
// so a web client that replays history after a refresh still shows the thinking
// — the live stream surfaces it from reply.Blocks, but a text-only message drops
// it. Anthropic-protocol models return the final turn as [thinking, text];
// round-tripping those blocks is the same contract already honored for tool-use
// turns, and the OpenAI adapter ignores the thinking block and falls back to
// Content. Plain replies with no thinking keep the lightweight Content form.
func assistantReplyMessage(reply Reply) Message {
	content := reply.Content
	if content == "" {
		content = textFromBlocks(reply.Blocks)
	}
	msg := NewAssistantMessage(content)
	if hasThinkingBlock(reply.Blocks) {
		msg.Blocks = reply.Blocks
	}
	return msg
}

// hasThinkingBlock reports whether blocks carries a reasoning trace worth
// persisting (a non-empty Anthropic-protocol "thinking" block).
func hasThinkingBlock(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "thinking" && b.Thinking != "" {
			return true
		}
	}
	return false
}

// textFromBlocks joins text from all "text" content blocks.
func textFromBlocks(blocks []ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// accrueUsage folds one reply's token/cache counts into the session totals
// and records the context size used by the compaction trigger.
func (a *Agent) accrueUsage(reply Reply) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	a.sessionInputTokens += reply.InputTokens
	a.sessionOutputTokens += reply.OutputTokens
	a.sessionCacheReadTokens += reply.CacheReadTokens
	a.sessionCacheWriteTokens += reply.CacheWriteTokens
	// Context occupancy is the WHOLE prompt sent, not just the uncached part.
	// On a cache hit the provider reports InputTokens as only the non-cached
	// remainder and moves the bulk into CacheReadTokens (cache_read_input_tokens)
	// / CacheWriteTokens (cache_creation_input_tokens) — e.g. Kimi streaming
	// returns input=114, cache_read=2304 for a 2418-token prompt. Summing all
	// three keeps the ctx-usage gauge honest; using InputTokens alone makes it
	// read far too low once the cached prefix dominates.
	a.lastInputTokens = reply.InputTokens + reply.CacheReadTokens + reply.CacheWriteTokens
}

// ResetGoalBaseline pins the goal-accounting baseline to the current session
// counters and restarts the goal wall clock, so the next accounting bills
// only usage — tokens and seconds — from this point on. Called at every turn
// start. (A goal created mid-turn is protected by the Session-side
// skip-next-delta flag instead, since the tool executor never sees the Agent.)
func (a *Agent) ResetGoalBaseline() {
	if a.GoalAcct == nil {
		return
	}
	a.goalBaseIn, a.goalBaseOut = a.SessionTokens()
	a.GoalAcct.ResetGoalWallClock()
}

// accountGoalUsage bills the session-counter delta since the last accounting
// to the goal and emits EventGoalUpdated when the record changed. A nil
// accountant or handler degrades gracefully (no accounting / no event).
// The baseline advances even when the goal is not accruing (paused mid-turn),
// so up to one LLM round of tokens straddling an external pause is dropped
// rather than billed — paused means not billed, and re-billing them on
// resume would be worse.
func (a *Agent) accountGoalUsage(handler EventHandler) {
	if a.GoalAcct == nil {
		return
	}
	in, out := a.SessionTokens()
	delta := int64(in-a.goalBaseIn) + int64(out-a.goalBaseOut)
	if delta < 0 {
		// Counters shrank (a fresh Agent was handed an old baseline —
		// defensive; should not happen with turn-start resets).
		delta = 0
	}
	a.goalBaseIn, a.goalBaseOut = in, out
	goal, changed := a.GoalAcct.AccountGoalUsage(delta)
	if changed && handler != nil {
		g := goal
		handler(AgentEvent{Kind: EventGoalUpdated, Goal: &g})
	}
	// A budget crossing stages a one-time wrap-up steer; inject it so the
	// next loop iteration drains it ahead of the LLM call. On the single-shot
	// Turn/TurnStream paths it reaches the model on the next turn instead.
	if steer, ok := a.GoalAcct.ConsumeGoalBudgetSteer(); ok {
		a.Inbox.Enqueue(steer)
	}
	// An objective edited mid-turn (web/IM/TUI can mutate the goal on a
	// different goroutine than the running turn) stages the same kind of
	// one-time steer; drain it the same way.
	if steer, ok := a.GoalAcct.ConsumeGoalObjectiveSteer(); ok {
		a.Inbox.Enqueue(steer)
	}
}

// SessionTokens returns the cumulative input and output token counts for all
// turns made so far in this Agent's lifetime.
func (a *Agent) SessionTokens() (inputTokens, outputTokens int) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	return a.sessionInputTokens, a.sessionOutputTokens
}

// AccrueChildUsage folds tokens spent by a spawned sub-agent into this
// agent's session totals, so SessionTokens still reports one
// consolidated number even when the LLM used sub_agent. cache totals are
// left untouched — the child runs against the same provider but reports its
// own cache hits internally; the parent only sees the bottom-line counts here.
func (a *Agent) AccrueChildUsage(inputTokens, outputTokens int) {
	a.addUsage(inputTokens, outputTokens)
}

// addUsage folds input/output token counts into the session totals under the
// usage lock. Shared by AccrueChildUsage (concurrent sub-agent goroutines) and
// the internal sub-operation accruals (compaction / consolidation / planning),
// all of which can run while a sub-agent goroutine is still accruing.
func (a *Agent) addUsage(inputTokens, outputTokens int) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	a.sessionInputTokens += inputTokens
	a.sessionOutputTokens += outputTokens
}

// ClearHistory drops the entire conversation history, returning the agent to a
// fresh state while keeping its system prompt, model, and tool wiring intact.
// The context-usage gauge is reset; cumulative session token totals (cost
// accounting) are deliberately left alone. Backs the /clear command.
func (a *Agent) ClearHistory() {
	a.History.Reset()
	a.resetContextTrigger()
	// A wiped conversation is a fresh opening: re-arm SessionStart so the next
	// turn fires with source=clear. Effective on transports that keep the Agent
	// across turns (CLI/TUI/IM); serve rebuilds the Agent per turn, so its /clear
	// re-opening is governed by the persisted flag instead.
	a.HookClear = true
}

// resetContextTrigger zeroes the compaction-trigger context size under the
// usage lock (it's read from the TUI goroutine via ContextUsage).
func (a *Agent) resetContextTrigger() {
	a.usageMu.Lock()
	a.lastInputTokens = 0
	a.usageMu.Unlock()
}

// ContextUsage reports how full the model's context window is: used is the
// most recently sent context size in tokens (reported by the provider), or,
// before any turn has run in this process (e.g. right after resuming a
// session), a heuristic estimate over the restored history — see
// historyTokens. window is the model's approximate context-window size. Lets
// the TUI status bar and the web UI render a "ctx N%" gauge. window is always
// > 0.
func (a *Agent) ContextUsage() (used, window int) {
	a.usageMu.Lock()
	real := a.lastInputTokens
	a.usageMu.Unlock()
	if real > 0 {
		return real, contextWindow(a.Model)
	}
	// Only pay for the History snapshot + heuristic estimate when there's no
	// real count yet (cold start) — this is called at TUI render-tick rate,
	// where a real count is the common case.
	return estimateMessages(a.History.Snapshot()), contextWindow(a.Model)
}

// PersistContextUsage records this agent's current context-window token count on
// the session (Session.LastContextTokens) so an idle or resumed session — one
// with no live Agent in memory — reports its true context usage instead of a
// transcript estimate that omits the system-prompt/tools overhead. Every
// transport (web, IM, CLI, scheduled) calls it at turn end, so a session opened
// in the Web UI shows the right number regardless of where it last ran. No-op
// when no count is available yet; best-effort — callers log any save error.
func (a *Agent) PersistContextUsage(sess *Session) error {
	if sess == nil {
		return nil
	}
	used, _ := a.ContextUsage()
	if used <= 0 {
		return nil
	}
	return sess.SetLastContextTokens(used)
}

// SessionCacheTokens returns the cumulative cache read/write token counts.
// Read is input served from cache (cheap); write is input written into the
// cache (Anthropic only). Both zero when the backend reports no cache info.
func (a *Agent) SessionCacheTokens() (readTokens, writeTokens int) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	return a.sessionCacheReadTokens, a.sessionCacheWriteTokens
}

// popLast is an internal helper used by Turn to undo the user-message append
// when the Sender call fails. Exported users should not need this.
func (h *History) popLast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.messages); n > 0 {
		h.messages = h.messages[:n-1]
		h.rewritten = true
	}
}
