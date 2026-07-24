package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
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

	// Pick the child's sender + model. A lean preset (explore/plan) runs on the
	// parent's lite model when one is configured — its own cheaper sender, not
	// the main one, since a named lite model may live on a different provider.
	// An explicit req.Model always wins; otherwise lean → lite, else parent's.
	sender, model := s.parent.GetSender(), req.Model
	if model == "" {
		if req.LeanContext && s.parent.LiteModel != "" && s.parent.LiteSender != nil {
			sender, model = s.parent.LiteSender, s.parent.LiteModel
		} else {
			model = s.parent.Model
		}
	}

	// Lean presets are seeded with the lean system prompt (skills + memory
	// dropped) when the parent has one; everyone else shares the full identity.
	baseSystem := s.parent.System // base + soul + env + skills + memory + …
	if req.LeanContext && s.parent.LeanSystem != "" {
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

	// True fork: seed the child with the parent's conversation so it continues
	// with full context. runChild then appends the fork prompt as the next
	// user turn. The seed is normally pre-captured by the sub_agent tool at
	// tool-execution time (req.ForkHistory) — a background Spawn runs on its
	// own goroutine, and snapshotting here would race the still-running parent
	// turn, seeding the child with the parent's own "waiting for sub-agents"
	// follow-ups. Snapshotting live is the fallback for direct synchronous
	// callers; either way the in-flight assistant turn that called sub_agent
	// is trimmed (see forkHistorySnapshot) so the copied history doesn't end
	// on a dangling tool_use the provider would reject.
	if req.ForkConversation {
		hist := req.ForkHistory
		if hist == nil {
			hist = s.parent.History.Snapshot()
		}
		child.History.ReplaceAll(forkHistorySnapshot(hist))
	}

	// Create the session dir before registering the child: a permissions
	// failure here must abort the spawn, not leave a dead entry in the registry
	// whose later Save() would silently fail.
	if req.SessionDir != "" {
		if err := os.MkdirAll(req.SessionDir, 0o755); err != nil {
			return tools.SpawnResult{}, fmt.Errorf("spawner: create session dir: %w", err)
		}
	}

	lc := &liveChild{agent: child, tools: childTools, executor: s.executor, sessionDir: req.SessionDir}
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

	// A fork child's first prompt gets an explicit role pin: the seeded
	// conversation usually reads as "I am orchestrating sub-agents", and
	// without the pin a weaker model keeps playing the parent (replying
	// "waiting for the sub-agents…") instead of doing the task appended at
	// the end.
	prompt := req.Prompt
	if req.ForkConversation {
		prompt = forkTaskFraming + prompt
	}

	reply, in, out, stop, turns, err := s.runChild(ctx, lc, prompt)
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

// forkTaskFraming prefixes a fork child's first prompt (see Spawn).
const forkTaskFraming = "<system-reminder>You are a sub-agent forked from the conversation above. " +
	"That conversation is background context only — do not continue its narrative, do not act as " +
	"the orchestrator, and do not wait for or report on other sub-agents. Your entire job is the " +
	"single task below: execute it yourself, using your tools as needed, and reply with its " +
	"result.</system-reminder>\n\n"

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

// runChild is the shared body of Spawn and Continue. It serializes calls to a
// single child (a child's history can't take two interleaved turns), re-stamps
// the sub-agent context marker so the child can't recurse, and accrues only the
// token delta for this round into the parent (SessionTokens is cumulative, so
// re-accruing the total would double-count earlier rounds). The returned (in,
// out) are this round's delta; turns is the child's TurnIterations count.
//
// A max-turns checkpoint is NOT an error: the partial reply and StopReason
// ("max_turns") are returned so the caller can checkpoint and Continue.
// Tokens spent on the partial round are still accrued. Callers that
// drive a continuation can inspect the child StopReason themselves.
func (s *Spawner) runChild(ctx context.Context, lc *liveChild, prompt string) (reply string, in, out int, stopReason string, turns int, err error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	childCtx := tools.WithSubAgentMarker(ctx)

	// When the manager stamped an event sink into ctx (TUI live panel), stream
	// the child's tool-level activity to it. Only tool_started/tool_error are
	// forwarded — not per-token text — to keep event volume sane with several
	// sub-agents running at once. No sink (headless) => nil handler =>
	// RunStream behaves exactly like Run.
	var handler agent.EventHandler
	if sink := tools.SubAgentEventSink(ctx); sink != nil {
		handler = func(ev agent.AgentEvent) {
			switch ev.Kind {
			case agent.EventToolStarted:
				sink(tools.SubAgentEvent{Kind: "tool", ToolName: ev.ToolName, ToolInput: ev.Input})
			case agent.EventToolError:
				sink(tools.SubAgentEvent{Kind: "tool_error", ToolName: ev.ToolName})
			}
		}
	}
	r, err := lc.agent.RunStream(childCtx, prompt, lc.tools, lc.executor, handler)
	turns = lc.agent.TurnIterations()
	if err != nil {
		return "", 0, 0, "", turns, err
	}

	// Accrue the round's token delta even on a max-turns checkpoint — the
	// partial work cost real tokens.
	totIn, totOut := lc.agent.SessionTokens()
	in, out = totIn-lc.accruedIn, totOut-lc.accruedOut
	lc.accruedIn, lc.accruedOut = totIn, totOut
	s.parent.AccrueChildUsage(in, out)

	lc.syncSession()
	s.fireSubagentStop(r.Content)
	return r.Content, in, out, r.StopReason, turns, nil
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
// ForkSnapshot captures the parent conversation for seeding a fork child,
// trimmed of the in-flight assistant turn (see forkHistorySnapshot). It
// implements tools.ForkSnapshotter: the sub_agent tool calls it synchronously
// at tool-execution time so a background fork seeds from the conversation as
// the model saw it when it made the call.
func (s *Spawner) ForkSnapshot() []agent.Message {
	return forkHistorySnapshot(s.parent.History.Snapshot())
}

// forkHistorySnapshot trims a parent-history snapshot for seeding a fork. The
// parent is mid-turn: the assistant message that called sub_agent is already in
// history, but its tool_result hasn't been produced. Copying it verbatim would
// leave a trailing tool_use with no matching tool_result, which the provider
// rejects. Drop trailing assistant turns carrying a tool_use so the seeded
// history ends on a complete exchange; runChild then appends the fork prompt.
func forkHistorySnapshot(msgs []agent.Message) []agent.Message {
	for len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		if last.Role == agent.RoleAssistant && messageHasToolUse(last) {
			msgs = msgs[:len(msgs)-1]
			continue
		}
		break
	}
	return msgs
}

// messageHasToolUse reports whether m carries any tool_use content block.
func messageHasToolUse(m agent.Message) bool {
	for _, b := range m.Blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

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

	// accruedIn/accruedOut track how much of the child's cumulative
	// SessionTokens has already been folded into the parent, so each round
	// accrues only its delta.
	accruedIn  int
	accruedOut int

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
