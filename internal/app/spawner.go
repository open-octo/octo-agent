package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tools"
)

// Spawner implements tools.Spawner by building a child agent on each
// sub_agent call. The child shares the parent's Sender (one provider
// connection) and System (same harness identity), but runs in isolation:
// fresh History, no visibility into the parent's conversation, its own
// loop budget. The final child reply text is returned to the parent as the
// sub_agent tool_result; the child's token usage is rolled into the
// parent's session totals so they report one consolidated number.
//
// toolsFn is a deferred lookup of the LLM-facing tool catalog (DefaultTools),
// given the ctx the child is being spawned under — so a server/cron caller
// can pass tools.DefaultToolsForCtx and have the child's own tool list
// reflect that turn's ctx-scoped sub-agent manager instead of depending on
// process-global state (#1133). Resolving it on each Spawn — rather than
// capturing a slice at construction — also lets cmd/octo set up the spawner
// before computing the tool list, since SetSpawner has to run first for
// sub_agent to appear in DefaultTools().
type Spawner struct {
	parent   *agent.Agent
	executor agent.ToolExecutor
	toolsFn  func(ctx context.Context) []agent.ToolDefinition
	// reg keeps spawned children alive after Spawn returns so a later
	// Continue can re-run them with their history intact. In-memory and
	// session-scoped: it lives as long as this spawner (one per REPL session),
	// and a fresh process starts empty.
	reg *childRegistry
}

func NewSpawner(parent *agent.Agent, executor agent.ToolExecutor, toolsFn func(ctx context.Context) []agent.ToolDefinition) *Spawner {
	return &Spawner{
		parent:   parent,
		executor: executor,
		toolsFn:  toolsFn,
		reg:      newChildRegistry(),
	}
}

// childMaxTurns caps the sub-agent's tool loop per round. Deliberately lower
// than the parent's defaultMaxTurns (1000): a sub-task should make focused
// progress and check back rather than run unbounded. Each Continue re-arms
// this budget for the next round.
const childMaxTurns = 100

// Spawn implements tools.Spawner. It builds an isolated child, registers it so
// a later Continue can resume it, runs the first prompt, and returns the
// child's id alongside its reply.
func (s *Spawner) Spawn(ctx context.Context, req tools.SpawnRequest) (tools.SpawnResult, error) {
	childTools := filterChildTools(s.toolsFn(ctx), req.Tools, req.DisallowedTools, req.ReadOnly)

	// The child runs on the parent's sender + model unless the request
	// overrides the model explicitly. No preset downgrades to the lite model
	// SILENTLY — a sub-agent's output gates the parent's next step, so cost
	// is trimmed via the lean system prompt (below) by default — but an
	// explicit "lite" override (call parameter or frontmatter `model: lite`)
	// opts the child onto the parent's lite sender/model.
	sender, model := s.parent.GetSender(), req.Model
	if strings.EqualFold(model, "lite") {
		model = "" // no lite configured: inherit the parent's model
		if s.parent.LiteSender != nil && s.parent.LiteModel != "" {
			sender, model = s.parent.LiteSender, s.parent.LiteModel
		}
	}
	if model == "" {
		model = s.parent.Model
	}

	// Lean presets are seeded with the lean system prompt (skills + memory
	// dropped) when the parent has one; everyone else shares the full identity.
	baseSystem := s.parent.System // base + soul + env + skills + memory + …
	if req.LeanSystem && s.parent.LeanSystem != "" {
		baseSystem = s.parent.LeanSystem
	}

	child := agent.New(sender, model)
	child.System = baseSystem
	// Preset agents append a persona after the shared identity, so the child
	// keeps the harness context but takes on its specialized role. A schema
	// request appends a strict JSON-only instruction on top of that.
	suffix := req.SystemSuffix
	if req.Schema != "" {
		suffix = appendSuffix(suffix, schemaInstruction(req.Schema))
	}
	if suffix != "" {
		child.System = baseSystem + "\n\n" + suffix
	}
	child.MaxTokens = s.parent.MaxTokens
	child.Gate = s.parent.Gate
	child.MaxTurns = childMaxTurns
	// Children compact on the same lite model as the parent.
	child.LiteSender = s.parent.LiteSender
	child.LiteModel = s.parent.LiteModel

	// A child gets its own describer rather than the parent's: the two may run
	// different models, and the describer decides whether to translate images
	// from the model it is bound to. Built from config rather than gated on
	// the parent's field — reading parent.Describer here would race the mu it
	// is documented to be guarded by. Nil (no helper configured, or config
	// unreadable) leaves the child's images untouched, as before.
	if cfg, err := config.LoadCached(); err == nil {
		child.SetImageDescriber(NewVisionDescriber(child, cfg))
	}

	// Create the session dir before registering the child: a permissions
	// failure here must abort the spawn, not leave a dead entry in the registry
	// whose later Save() would silently fail.
	if req.SessionDir != "" {
		if err := os.MkdirAll(req.SessionDir, 0o755); err != nil {
			return tools.SpawnResult{}, fmt.Errorf("spawner: create session dir: %w", err)
		}
	}

	// Each child gets its own read-before-write state. The gate means "this
	// context has seen these bytes", and a child reasons over its own history:
	// sharing the parent's tracker (or the previous child's) would let it
	// write_file over a file nobody in its context ever read. The executor is
	// kept on the liveChild, so a later Continue resumes with the reads the
	// child itself made.
	executor := s.executor
	if fresh, ok := executor.(tools.TrackerForking); ok {
		executor = fresh.WithFreshTracker()
	}

	lc := &liveChild{agent: child, tools: childTools, executor: executor, sessionDir: req.SessionDir}
	id := s.reg.put(lc)

	if req.SessionDir != "" {
		sess := agent.NewSession(child.Model, child.System)
		sess.ID = id
		sess.Dir = req.SessionDir
		_ = sess.SetPermissionMode(string(permission.ResolveDefaultMode()))
		lc.session = sess
	}

	// Worktree isolation: create a fresh worktree and root the child's tools in
	// it (terminal + file ops both honor WorkingDir(ctx)), so its changes don't
	// touch the main checkout. Set up before runChild so both the first prompt
	// and any schema-retry run inside the worktree.
	var wt *worktree
	if req.Isolation == "worktree" {
		var werr error
		wt, werr = newWorktree(id)
		if werr != nil {
			return tools.SpawnResult{}, fmt.Errorf("spawner: %w", werr)
		}
		ctx = tools.WithWorkingDir(ctx, wt.dir)
	}

	reply, in, out, stop, turns, err := s.runChild(ctx, lc, req.Prompt)
	if err != nil {
		if wt != nil {
			wt.finish() // reconcile/clean even on error so we don't leak the worktree
		}
		return tools.SpawnResult{}, err
	}

	// Schema requested: clean the reply down to its JSON. If the model wrapped
	// it in prose / markdown fences and it doesn't parse, re-prompt the same
	// child once (in-context) with a corrective nudge, then take the best of
	// the two. We don't fail the spawn on still-invalid JSON — the caller gets
	// the cleaned text and can decide — but a single retry catches the common
	// "```json …```" wrapping that fence-stripping alone misses mid-string.
	if req.Schema != "" {
		cleaned := extractJSON(reply)
		if !json.Valid([]byte(cleaned)) {
			r2, in2, out2, stop2, turns2, err2 := s.runChild(ctx, lc, schemaRetryPrompt)
			if err2 != nil {
				if wt != nil {
					wt.finish() // reconcile/clean even on retry error so we don't leak the worktree
				}
				return tools.SpawnResult{}, err2
			}
			in, out, turns, stop = in+in2, out+out2, turns+turns2, stop2
			if c2 := extractJSON(r2); json.Valid([]byte(c2)) {
				cleaned = c2
			} else if c2 != "" {
				cleaned = c2 // still invalid, but the retry is the model's latest attempt
			}
		}
		reply = cleaned
	}

	// Reconcile the worktree: clean up an unchanged run, or commit changes onto
	// its branch and tell the caller where to find them.
	if wt != nil {
		if note := wt.finish(); note != "" {
			if reply != "" {
				reply += "\n\n" + note
			} else {
				reply = note
			}
		}
	}

	return tools.SpawnResult{
		AgentID:      id,
		Reply:        reply,
		InputTokens:  in,
		OutputTokens: out,
		Turns:        turns,
		StopReason:   stop,
	}, nil
}

// schemaRetryPrompt re-prompts a child whose first reply wasn't valid JSON.
const schemaRetryPrompt = "Your previous reply was not valid JSON. Respond with ONLY the raw JSON " +
	"value matching the schema — no prose, no explanation, no markdown code fences."

// schemaInstruction is appended to a schema-constrained child's system prompt.
func schemaInstruction(schema string) string {
	return "You must respond with ONLY a single valid JSON value that conforms to this JSON " +
		"Schema. Output the raw JSON and nothing else — no prose, no explanation, no markdown " +
		"code fences.\n\nJSON Schema:\n" + schema
}

// appendSuffix joins two system-prompt fragments, skipping empties.
func appendSuffix(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n\n" + b
	}
}

// extractJSON pulls the JSON payload out of a model reply: it strips a leading
// ```json / ``` fence and trailing ```, then trims to the outermost { } or [ ]
// span so surrounding prose doesn't defeat json.Valid. Returns the trimmed
// text (unchanged when no JSON-looking span is found).
func extractJSON(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		// Drop the opening fence line (``` or ```json) and the closing fence.
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = t[nl+1:]
		}
		if i := strings.LastIndex(t, "```"); i >= 0 {
			t = t[:i]
		}
		t = strings.TrimSpace(t)
	}
	// Trim to the outermost object/array span.
	start := strings.IndexAny(t, "{[")
	end := strings.LastIndexAny(t, "}]")
	if start >= 0 && end > start {
		return t[start : end+1]
	}
	return t
}

// Continue implements tools.Spawner. It re-runs a still-alive child with a new
// message. An unknown / evicted id returns an error whose text steers the model
// to start a fresh sub-agent.
func (s *Spawner) Continue(ctx context.Context, agentID, message string) (tools.SpawnResult, error) {
	lc, ok := s.reg.get(agentID)
	if !ok {
		return tools.SpawnResult{}, fmt.Errorf(
			"agent %s is no longer alive (idle-expired or evicted); launch a fresh sub-agent instead", agentID)
	}
	reply, in, out, stop, turns, err := s.runChild(ctx, lc, message)
	if err != nil {
		return tools.SpawnResult{}, err
	}
	return tools.SpawnResult{
		AgentID:      agentID,
		Reply:        reply,
		InputTokens:  in,
		OutputTokens: out,
		Turns:        turns,
		StopReason:   stop,
	}, nil
}

// InspectChild implements tools.ChildInspector. It answers for the ids a
// synchronous sub-agent reports ("[agent <id>]"), which SubAgentManager stops
// tracking as soon as the blocking tool call returns.
func (s *Spawner) InspectChild(id string) (tools.ChildSnapshot, bool) {
	return s.reg.snapshot(id)
}

// ListChildren implements tools.ChildInspector.
func (s *Spawner) ListChildren() []tools.ChildSnapshot {
	return s.reg.snapshots()
}

// runChild is the shared body of Spawn and Continue. It serializes calls to a
// single child (a child's history can't take two interleaved turns), re-stamps
// the sub-agent context marker so the child can't recurse, and accrues only the
// token delta for this round into the parent (SessionTokens is cumulative, so
// re-accruing the total would double-count earlier rounds). The returned (in,
// out) are this round's delta; turns is the child's TurnIterations count.
//
// A max-turns checkpoint is NOT an error: the partial reply and StopReason
// ("max_turns") are returned so the caller can checkpoint and Continue.
// Tokens spent on partial rounds — checkpoint OR error — are still accrued
// into the parent before the error is returned. Callers that drive a
// continuation can inspect the child StopReason themselves.
func (s *Spawner) runChild(ctx context.Context, lc *liveChild, prompt string) (reply string, in, out int, stopReason string, turns int, err error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	// A round in flight must not be advertised as a finished, resumable child:
	// the registry holds the child from before its first round (Spawn registers
	// it, then runs), so without this a running sub-agent would be listed as
	// idle — and a follow-up addressed to it would block on lc.mu for the rest
	// of the round.
	lc.setBusy(true)
	defer lc.setBusy(false)

	childCtx := tools.WithSubAgentMarker(ctx)

	// When the manager stamped an event sink into ctx (live panels), stream
	// the child's activity to it: tool dispatches with their (capped) outputs,
	// and the assistant's text as whole blocks — deltas are aggregated locally
	// and flushed on the next tool dispatch or at turn end, never per token, to
	// keep event volume sane with several sub-agents running at once. No sink
	// (headless) => nil handler => no events; unlike Run, a nil-handler
	// RunStream still keeps a streamed partial reply in history when a round
	// dies on an error.
	var handler agent.EventHandler
	if sink := tools.SubAgentEventSink(ctx); sink != nil {
		var textBuf strings.Builder
		flushText := func() {
			if textBuf.Len() == 0 {
				return
			}
			sink(tools.SubAgentEvent{Kind: "text", Text: tools.ClipForEvent(textBuf.String(), tools.SubAgentEventTextCap)})
			textBuf.Reset()
		}
		handler = func(ev agent.AgentEvent) {
			switch ev.Kind {
			case agent.EventTextDelta:
				textBuf.WriteString(ev.Text)
			case agent.EventToolStarted:
				flushText()
				sink(tools.SubAgentEvent{Kind: "tool", ToolID: ev.ToolID, ToolName: ev.ToolName, ToolInput: ev.Input})
			case agent.EventToolDone:
				sink(tools.SubAgentEvent{Kind: "tool_done", ToolID: ev.ToolID, ToolName: ev.ToolName,
					ToolOutput: tools.ClipForEvent(ev.Output, tools.SubAgentEventOutputCap)})
			case agent.EventToolError:
				out := ev.Err
				if ev.Output != "" {
					out += "\n" + ev.Output
				}
				sink(tools.SubAgentEvent{Kind: "tool_error", ToolID: ev.ToolID, ToolName: ev.ToolName,
					ToolOutput: tools.ClipForEvent(out, tools.SubAgentEventOutputCap)})
			case agent.EventTurnDone:
				flushText()
			}
		}
	}
	r, err := lc.agent.RunStream(childCtx, prompt, lc.tools, lc.executor, handler)
	turns = lc.agent.TurnIterations()

	// Accrue the round's token delta BEFORE the error check: a failed or
	// max-turns-checkpoint round still burned real tokens on its partial
	// work (a streamed partial reply is kept in history on error), so the
	// parent's session totals and per-turn cache utilization must see it.
	// Cache deltas ride along so the readout stays a true value.
	totIn, totOut := lc.agent.SessionTokens()
	totCR, totCW := lc.agent.SessionCacheTokens()
	in, out = totIn-lc.accruedIn, totOut-lc.accruedOut
	lc.accruedIn, lc.accruedOut = totIn, totOut
	s.parent.AccrueChildUsage(in, out, totCR-lc.accruedCacheRead, totCW-lc.accruedCacheWrite)
	lc.accruedCacheRead, lc.accruedCacheWrite = totCR, totCW

	if err != nil {
		// Record the failure too. The child stays resumable, so a status query
		// that reported the previous round as the latest one would describe a
		// round that has since been superseded by a failure.
		lc.setSnapErr(err, turns)
		return "", in, out, "", turns, err
	}

	lc.syncSession()

	// A loop-budget stop replaces the model's text with the agent loop's own
	// notice, so carry the work the child had already produced (see
	// carryPartialWork) — otherwise the round reaches the parent as a stop
	// reason and nothing else.
	reply = r.Content
	if budgetNoticeOnly(r.StopReason) {
		reply = carryPartialWork(lc.agent.History, reply)
	}
	lc.setSnap(reply, r.StopReason, turns)

	s.fireSubagentStop(reply)
	return reply, in, out, r.StopReason, turns, nil
}

// maxChildSnapshotReply caps the retained reply, matching what the manager
// keeps per async sub-agent (tools.maxSubAgentResultBytes). A child's reply is
// bounded by the model's output cap in practice, but the registry holds several
// children for the life of the session, so the bound is explicit.
const maxChildSnapshotReply = 1 << 20

// setSnap records the outcome of the round that just finished, for
// sub_agent_status to read off a child the manager no longer tracks.
func (lc *liveChild) setSnap(reply, stopReason string, turns int) {
	if len(reply) > maxChildSnapshotReply {
		reply = reply[:maxChildSnapshotReply] + "\n...[truncated]"
	}
	lc.snapMu.Lock()
	defer lc.snapMu.Unlock()
	lc.lastReply = reply
	lc.lastStop = stopReason
	lc.lastErr = ""
	lc.lastTurns = turns
}

// setSnapErr records a round that failed outright. It clears the previous
// round's reply: keeping it would let a status query present stale text as the
// child's latest word.
func (lc *liveChild) setSnapErr(err error, turns int) {
	lc.snapMu.Lock()
	defer lc.snapMu.Unlock()
	lc.lastReply = ""
	lc.lastStop = ""
	lc.lastErr = err.Error()
	lc.lastTurns = turns
}

func (lc *liveChild) setBusy(v bool) {
	lc.snapMu.Lock()
	defer lc.snapMu.Unlock()
	lc.busy = v
}

// snapshot renders the child's last-round state for the tools layer. withReply
// is false for listings, which render only the header fields — copying every
// child's full reply to discard it is pure waste, and it happens under the
// registry lock.
func (lc *liveChild) snapshot(id string, idle time.Duration, withReply bool) tools.ChildSnapshot {
	lc.snapMu.Lock()
	defer lc.snapMu.Unlock()
	snap := tools.ChildSnapshot{
		ID:         id,
		StopReason: lc.lastStop,
		Err:        lc.lastErr,
		Turns:      lc.lastTurns,
		Idle:       idle,
		Busy:       lc.busy,
	}
	if withReply {
		snap.Reply = lc.lastReply
	}
	return snap
}

// budgetNoticeOnly reports whether the agent loop ended the round with a
// synthetic explanation in place of model text. budgetStop does that at all
// three of its call sites — the turn cap, the output-token cap, and the
// stuck-loop detector — which is exactly when the caller most needs to see how
// far the child got. An interrupted round doesn't reach here: it comes back as
// a context error.
func budgetNoticeOnly(stopReason string) bool {
	return stopReason == agent.StopReasonMaxTurns ||
		stopReason == agent.StopReasonStuck ||
		stopReason == agent.StopReasonMaxTokens
}

// carriedWorkLabel marks text that carryPartialWork recovered. It says where
// the text came from because the last thing a child said can be anything from
// a finished summary to an opening "let me look" many rounds back — labelled,
// the parent can judge it; unlabelled, the stop notice's "partial result"
// would vouch for it.
const carriedWorkLabel = "[partial — the sub-agent's last message before it was cut off]"

// carryPartialWork prefixes a budget-stop notice with the child's last
// substantive assistant text, pulled back out of its history. Returns the
// notice unchanged when the child produced no text before it was cut off —
// a run that only ever called tools has nothing to carry.
func carryPartialWork(h *agent.History, notice string) string {
	msgs := h.Snapshot()
	// The notice is the message budgetStop just appended; start above it.
	for i := len(msgs) - 2; i >= 0; i-- {
		if roundStart(msgs[i]) {
			// Walked back past this round's prompt. Anything earlier belongs to
			// a previous round the caller already received — carrying it would
			// label a delivered answer as this round's partial work.
			return notice
		}
		if msgs[i].Role != agent.RoleAssistant {
			continue
		}
		if text := strings.TrimSpace(assistantText(msgs[i])); text != "" {
			return carriedWorkLabel + "\n" + text + "\n\n" + notice
		}
	}
	return notice
}

// roundStart reports whether m opens a round: a plain user message, as opposed
// to the tool_result messages that carry a round forward. Both are RoleUser, so
// the blocks decide. A mid-round user message the loop injects itself (a
// truncation resume, a compaction summary) reads as a boundary too, which only
// makes the carry more conservative.
func roundStart(m agent.Message) bool {
	if m.Role != agent.RoleUser {
		return false
	}
	for _, b := range m.Blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return true
}

// assistantText is the plain text of an assistant message: the joined text
// blocks when it carries any (a reply that also called tools keeps its prose
// there), else the plain Content field.
func assistantText(m agent.Message) string {
	if len(m.Blocks) == 0 {
		return m.Content
	}
	var sb strings.Builder
	for _, b := range m.Blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// fireSubagentStop dispatches the parent's SubagentStop hook after a child round
// completes — the top-level session's signal that a spawned agent finished. Uses
// the parent's hook identity (a child's own turns fire Stop separately if the
// child has an engine). Background context so a cancelled parent turn doesn't
// abort retention. No-op when the parent has no engine.
func (s *Spawner) fireSubagentStop(reply string) {
	if s.parent == nil || s.parent.Hooks == nil {
		return
	}
	p := s.parent.HookMeta.Payload(hooks.EventSubagentStop)
	if p.Model == "" {
		p.Model = s.parent.Model
	}
	p.AssistantReply = reply
	s.parent.Hooks.Dispatch(context.Background(), p)
}

// syncSession persists the child's conversation to disk when sessionDir was
// supplied. Called at the end of every runChild round (Spawn + Continue) so
// the transcript is always up-to-date. Failures are silent — session logging
// is best-effort and must never block the sub-agent.
func (lc *liveChild) syncSession() {
	if lc.session == nil || lc.sessionDir == "" {
		return
	}
	lc.session.SyncFrom(lc.agent.History)
	_ = lc.session.Save()
}

// filterChildTools drops Agent (a sub-agent cannot spawn another sub-agent —
// that stays top-level-only) and, when allowed is non-empty, intersects with
// that allowlist so the parent can hand the child a restricted toolbelt (e.g.
// read-only research). When readOnly is set, the mutating tools (write_file,
// edit_file) are dropped too — used by read-only presets so the child keeps
// terminal/MCP/codegraph but can't change files. The two filters compose: a
// readOnly preset still honours allowed.
func filterChildTools(parent []agent.ToolDefinition, allowed, disallowed []string, readOnly bool) []agent.ToolDefinition {
	var allowSet map[string]bool
	if len(allowed) > 0 {
		allowSet = make(map[string]bool, len(allowed))
		for _, a := range allowed {
			allowSet[a] = true
		}
	}
	var denySet map[string]bool
	if len(disallowed) > 0 {
		denySet = make(map[string]bool, len(disallowed))
		for _, d := range disallowed {
			denySet[d] = true
		}
	}
	out := make([]agent.ToolDefinition, 0, len(parent))
	for _, td := range parent {
		if td.Name == "sub_agent" {
			continue
		}
		if readOnly && (td.Name == "write_file" || td.Name == "edit_file") {
			continue
		}
		if allowSet != nil && !allowSet[td.Name] {
			continue
		}
		if denySet[td.Name] {
			continue
		}
		out = append(out, td)
	}
	return out
}

// Live-child registry — keeps spawned sub-agents addressable for Continue.

const (
	// maxLiveChildren caps how many sub-agents stay resumable at once. Beyond
	// this the least-recently-used is evicted (its history is dropped; a
	// Continue to it then fails and the model relaunches).
	maxLiveChildren = 8
	// childIdleTTL evicts a sub-agent that hasn't been touched in this long,
	// so abandoned children don't pin their histories in memory for the whole
	// session.
	childIdleTTL = 30 * time.Minute
)

// liveChild is one resumable sub-agent: its Agent (history accumulates across
// Run calls), the toolbelt + executor it was spawned with (a Continue reuses
// them), and the bookkeeping for serialization, eviction, and delta-accounting.
type liveChild struct {
	agent    *agent.Agent
	tools    []agent.ToolDefinition
	executor agent.ToolExecutor

	mu sync.Mutex // serializes runChild on this child

	// snapMu guards the last-round fields below. Deliberately not mu: mu is
	// held for the whole round, and sub_agent_status must be answerable while
	// the child is mid-run rather than blocking behind it.
	snapMu    sync.Mutex
	busy      bool
	lastReply string
	lastStop  string
	lastErr   string
	lastTurns int

	// accruedIn/accruedOut/accruedCacheRead/accruedCacheWrite track how much
	// of the child's cumulative SessionTokens/SessionCacheTokens has already
	// been folded into the parent, so each round accrues only its delta.
	accruedIn         int
	accruedOut        int
	accruedCacheRead  int
	accruedCacheWrite int

	lastUsed time.Time // for TTL eviction
	seq      uint64    // monotonic touch order, for LRU eviction (clock-independent)

	// sessionDir + session persist the sub-agent transcript when the caller
	// (the caller) wants post-mortem traceability. Empty sessionDir means no
	// persistence (the default for chat/REPL sub-agents).
	sessionDir string
	session    *agent.Session
}

// childRegistry holds the live children for one spawner (one REPL session).
// Purely in-memory: nothing is persisted, and the map is dropped when the
// spawner goes away with the session.
type childRegistry struct {
	mu     sync.Mutex
	m      map[string]*liveChild
	seqCtr uint64
	now    func() time.Time // injectable for tests
}

func newChildRegistry() *childRegistry {
	return &childRegistry{m: make(map[string]*liveChild), now: time.Now}
}

// put registers a child under a fresh id and returns it. Eviction runs after
// insertion so the cap holds even counting the new entry; the just-added child
// is the most-recently-used, so LRU never evicts it.
func (r *childRegistry) put(lc *liveChild) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.freshIDLocked()
	r.touchLocked(lc)
	r.m[id] = lc
	r.evictLocked()
	return id
}

// get returns the child for id (refreshing its LRU/TTL standing) or (nil,
// false) if it's unknown or already evicted.
func (r *childRegistry) get(id string) (*liveChild, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()
	lc, ok := r.m[id]
	if ok {
		r.touchLocked(lc)
	}
	return lc, ok
}

// snapshot reports one child's state without refreshing its standing — a
// status query is a read, not a use. Expired children are reaped first, so an
// idle-expired id correctly reports as gone.
func (r *childRegistry) snapshot(id string) (tools.ChildSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()
	lc, ok := r.m[id]
	if !ok {
		return tools.ChildSnapshot{}, false
	}
	return lc.snapshot(id, r.now().Sub(lc.lastUsed), true), true
}

// snapshots reports every live child, most recently used first.
func (r *childRegistry) snapshots() []tools.ChildSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()
	now := r.now()
	out := make([]tools.ChildSnapshot, 0, len(r.m))
	for id, lc := range r.m {
		out = append(out, lc.snapshot(id, now.Sub(lc.lastUsed), false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Idle < out[j].Idle })
	return out
}

// touchLocked stamps a child as most-recently-used. Caller holds r.mu.
func (r *childRegistry) touchLocked(lc *liveChild) {
	r.seqCtr++
	lc.seq = r.seqCtr
	lc.lastUsed = r.now()
}

// evictLocked drops TTL-expired children, then trims to maxLiveChildren by
// least-recently-used. Caller holds r.mu.
func (r *childRegistry) evictLocked() {
	now := r.now()
	for id, lc := range r.m {
		if now.Sub(lc.lastUsed) > childIdleTTL {
			delete(r.m, id)
		}
	}
	for len(r.m) > maxLiveChildren {
		var oldestID string
		var oldestSeq uint64
		first := true
		for id, lc := range r.m {
			if first || lc.seq < oldestSeq {
				oldestID, oldestSeq, first = id, lc.seq, false
			}
		}
		delete(r.m, oldestID)
	}
}

// freshIDLocked returns an 8-hex-char id not currently in use. Same shape as
// agent.Session / short ids. Caller holds r.mu.
func (r *childRegistry) freshIDLocked() string {
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			// crypto/rand.Read is documented never to fail; a failure means the
			// OS entropy source is broken, so refuse to mint a predictable id.
			panic(fmt.Sprintf("spawner: crypto/rand unavailable: %v", err))
		}
		id := hex.EncodeToString(b[:])
		if _, taken := r.m[id]; !taken {
			return id
		}
	}
}
