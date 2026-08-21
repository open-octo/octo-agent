# Rich output panels for sub-agents and workflows

Design for showing the **full output** of `sub_agent` and `workflow` runs in the web UI — tool results, the child's text replies, and the final result — instead of today's tool-name-plus-args trail, with the complete trail reviewable after the run (and after a page reload).

Scope: web/desktop frontend only. TUI keeps its current compact panel; the event-schema changes are backward-compatible with it.

## Problem

Today the panels are a thin live view:

- `SubAgentEvent` carries only `Kind` (started/tool/tool_error/done), `ToolName`, `ToolInput`, `StopReason`. Tool **outputs**, the child's **assistant text**, and the **final result** never reach the frontend. The final reply is retained server-side (`asyncSubAgent.result`, capped 1 MiB) but is only reachable by the model via `agent_status`.
- `WorkflowEvent` carries only `log()`/`phase()`/lifecycle text lines. Each `agent()` call's prompt, tool trail, and reply are invisible; the web card shows the last 6 lines, each clamped to 3 rows.
- Both panels are ephemeral: sub-agent cards fade 2s after all agents finish and are cleared on the next turn. Nothing can be reviewed afterwards.

## Decisions

1. **Content**: tool outputs + child text replies + final result + per-`agent()` detail inside workflows.
2. **Interaction**: in-card fold/expand (reuse the `toolFold` pattern) — no new drawer surface.
3. **Persistence**: trails survive reload; a finished run is reviewable from the transcript.
4. **Targets**: web/desktop only.

## Design overview

Three layers:

1. **Event schema** — enrich `SubAgentEvent` with tool completion (output), block-level text, and final result; give `WorkflowEvent` structured per-agent kinds by translating the child's sub-agent events inside the `agent()` host function.
2. **Persistence** — the server appends every enriched event to a per-session sidecar JSONL; a REST endpoint reduces it to per-run trail snapshots for hydration. The `sub_agent`/`workflow` tool results gain a small `UI` payload (`agent_id`/`run_id`) so a transcript tool card can claim its trail.
3. **Frontend** — one shared `AgentTrail.svelte` component renders a trail (steps + result); used live in `SubAgentsCard`/`WorkflowsCard` and, for review, inside the transcript's `sub_agent`/`workflow` tool cards.

## 1. Event schema

### SubAgentEvent (internal/tools/subagent_event.go)

New fields and kinds; existing consumers are unaffected (the TUI's `handleSubAgentEvent` switch ignores unknown kinds).

```go
type SubAgentEvent struct {
    // ... existing fields ...
    ToolID     string // pairs "tool" with its "tool_done"/"tool_error"
    ToolOutput string // "tool_done": result text; "tool_error": error text (+ partial output). Capped.
    Text       string // "text": one completed assistant text block. Capped.
    Result     string // "done": the final reply. Capped.
}
```

Kinds become: `started | tool | tool_done | tool_error | text | done`.

Emission (`Spawner.runChild` handler in internal/app/spawner.go):

- `EventToolStarted` → `tool` {ToolID, ToolName, ToolInput} (as today, plus ToolID).
- `EventToolDone` → `tool_done` {ToolID, ToolName, ToolOutput}. The agent loop already caps `Output` at `EventToolOutputCap` (8 KiB); the sink re-caps to `subAgentEventOutputCap = 4 KiB` to bound retention.
- `EventToolError` → `tool_error` {ToolID, ToolName, ToolOutput = Err + partial output, capped}.
- `EventTextDelta` → accumulated in a local buffer; flushed as one `text` event on the next `EventToolStarted` or on `EventTurnDone` (cap 8 KiB per block). Block-level, never per-token — the original event-volume constraint stands.
- `done` (emitted by SubAgentManager as today) gains `Result` — the reply the manager already holds, re-capped for the event to `subAgentEventResultCap = 64 KiB` (the 1 MiB retention cap is too large for a WS frame; `agent_status` still serves the full text).

Retention: `asyncSubAgent.events` keeps its 200-event FIFO cap; with the 4/8 KiB payload caps the worst case is ~1 MiB per agent. Late-join replay (ws_handlers) is unchanged — it now simply replays richer events.

Server broadcast (handlers_prepare_toolturn.go `SubAgentOnEvent`) forwards the new fields as `tool_id`, `tool_output`, `text`, `result`.

### WorkflowEvent (internal/tools/workflow_manager.go)

New kinds and fields for per-`agent()` detail:

```go
type WorkflowEvent struct {
    // ... existing fields ...
    AgentID    string // per-run agent handle: "a1", "a2", ... (agent_* kinds)
    AgentLabel string // firstLine(prompt)
    ToolID     string
    ToolName   string
    ToolInput  map[string]any
    ToolOutput string
    Text       string
    Reply      string // "agent_done": the agent's reply, capped
    Error      string // "agent_done": non-empty when the agent() call failed
}
```

Kinds gain: `agent_started | agent_tool | agent_tool_done | agent_tool_error | agent_text | agent_done`.

Wiring — no engine (wazero) changes:

- `WorkflowManager.Start` stamps a per-run emitter into the detached run ctx: `withWorkflowEmit(ctx, func(ev WorkflowEvent) { ev.RunID = id; run.recordAgentEvent(ev); m.emit(ev) })`.
- The `agent()` host closure (`af` in internal/tools/workflow.go) pulls the emitter from ctx, allocates `AgentID` from a per-run counter, emits `agent_started`, stamps `WithSubAgentEventSink(c, translate)` — where `translate` maps the child's `tool/tool_done/tool_error/text` SubAgentEvents (produced by the same `runChild` path as sub_agent) onto the `agent_*` WorkflowEvent kinds — calls `Spawn`, then emits `agent_done` with the capped Reply or Error. `skill()` calls get the same treatment via `dispatchWorkflowSkill`.
- `workflowRun` retains the structured agent events in a capped buffer (mirror of `asyncSubAgent.events`, 500 events/run) so late-joining clients and the REST reducer see them. The existing `logs []string` (log/phase/lifecycle lines) stays as-is; the frontend may de-duplicate the engine's "→ start / ✓ done" lines against `agent_started`/`agent_done`, but the backend keeps emitting them (the TUI relies on them).

## 2. Persistence and review

### Sidecar event log

The server (not the tools layer) owns persistence, from the same per-session callbacks that broadcast to the WS hub (`SubAgentOnEvent` / `WorkflowOnEvent` in handlers_prepare_toolturn.go): each event is appended as one JSON line to `<sessions dir>/<sid>.agent-events.jsonl`, behind a per-session mutex. Append-only during a run; on load, a file above 8 MiB is compacted by dropping the oldest runs' events.

### REST hydration

`GET /api/sessions/:id/agent-runs` replays the sidecar through the same reducer shape the frontend uses and returns per-run trail snapshots:

```json
{
  "sub_agents": [{ "agent_id": "agent_1", "description": "...", "agent_type": "explore",
                   "status": "done", "started_at": 0, "steps": [...], "result": "..." }],
  "workflows":  [{ "run_id": "wf_1", "description": "...", "status": "done",
                   "logs": [...], "agents": [{ "agent_id": "a1", "label": "...", "steps": [...], "reply": "..." }] }]
}
```

`steps` is the ordered interleave of tool and text entries:
`{kind:"tool", id, name, input, output?, error?}` | `{kind:"text", text}`.

### Transcript anchoring via ToolResult.UI

The launch-time tool results gain a tiny `UI` payload (the existing `ToolResult.UI` mechanism — persisted with the session on the tool_result block, never sent to the model):

- `sub_agent` (async and sync paths in internal/tools/agent.go): `UI: {"agent_id": id}`.
- `workflow` (background path in internal/tools/workflow.go): `UI: {"run_id": runID}`.

The transcript already renders these tool calls as cards; the `agent_id`/`run_id` lets a card claim its trail from the hydrated store. Old sessions without the payload render exactly as today.

## 3. Frontend

### Store

`SubAgentState` (web/src/lib/stores.ts) replaces `tools: SubAgentTool[]` with:

```ts
steps: Array<
  | { kind: 'tool'; id: string; name: string; input?: Record<string, any>;
      output?: string; error?: boolean; status: 'running' | 'done' | 'error' }
  | { kind: 'text'; text: string }
>
result?: string
```

`applySubAgentEvent` folds the new kinds: `tool` appends a running step; `tool_done`/`tool_error` complete the step matched by `ToolID` (fallback: last running step of that name); `text` appends a text step; `done` sets `result`.

`WorkflowRunState` gains `agents: WorkflowAgentState[]` (same step shape plus `label`/`reply`/`error`), folded from the `agent_*` kinds.

A new hydration path fills both stores from `GET /api/sessions/:id/agent-runs` on session open, keyed so live WS events for a still-running trail merge cleanly (by `agent_id`/`run_id`).

### AgentTrail.svelte (new shared component)

Renders one trail: the ordered steps and the final result.

- Each tool step is a `<details>` reusing the `toolFold` open-state logic: errors open, the latest step of a running trail open, everything else closed, with per-step user override.
- Expanded tool step: pretty-printed input JSON + output in a mono `<pre>` capped at ~240px with inner scroll (never stretches the page).
- Text steps render as plain markdown paragraphs between tool steps.
- `result` renders as markdown at the trail's end, in its own fold (open when the trail is done and short, folded when long).

Used in three places:

1. **SubAgentsCard** — each agent row's body becomes an AgentTrail. Header/summary rows, lifecycle (fade 2s after all done, cleared next turn) unchanged: the live card returns to "at-a-glance progress" duty because the durable copy lives in the transcript.
2. **WorkflowsCard** — a run's body shows the log/phase tail (as today) plus a nested agent list, one collapsible row per `agent()` call, each expanding to an AgentTrail.
3. **Transcript tool cards** — when a `sub_agent`/`workflow` tool card's result carries `UI.agent_id`/`UI.run_id` and the store has that trail, the card body embeds the AgentTrail (collapsed by default). This is the review surface: correctly positioned in conversation flow, survives reloads via hydration.

## Compatibility

- **TUI**: unknown SubAgentEvent kinds are no-ops in `handleSubAgentEvent`; unknown WorkflowEvent kinds must be skipped by the TUI workflow line handler (verify — one guard clause if not).
- **IM channels**: no panel; the enriched events flow through the same sinks but IM renders nothing new.
- **Old sessions**: no sidecar and no UI payload → tool cards render as today; no errors.
- **Event volume**: text stays block-level; all payloads capped at emission. No per-token streaming is introduced.
- **Mobile web**: shares the stores; the mobile chat view can adopt AgentTrail later — degrades to current behavior until then.

## Acceptance criteria

1. While a sub-agent runs, expanding a tool step in the live card shows its input and (once finished) its output; the agent's interim text replies appear between steps; on completion the final result renders as markdown.
2. A workflow card lists every `agent()` call as its own expandable entry with the same detail; `log()`/`phase()` lines still show.
3. After the run finishes — and after a full page reload — expanding the `sub_agent`/`workflow` tool card in the transcript shows the identical full trail.
4. A session predating this feature renders unchanged.
5. TUI behavior is unchanged.

## PR breakdown

1. **Backend: SubAgentEvent enrichment** — ToolID/tool_done/text/Result kinds + spawner handler + manager passthrough + server broadcast fields + TUI guard. (Tests: subagent_manager_test, bg_tasks_test event shape.)
2. **Backend: WorkflowEvent agent detail** — ctx emitter, `af`/`skill()` translation, per-run event retention, broadcast, late-join replay. (Tests: workflow_manager_test, live_replay_test.)
3. **Backend: persistence** — sidecar JSONL writer, compaction, `GET /api/sessions/:id/agent-runs`, ToolResult.UI payloads on sub_agent/workflow.
4. **Web: stores + AgentTrail** — step-based state, event folding, hydration, SubAgentsCard/WorkflowsCard rework.
5. **Web: transcript review** — tool cards claim trails via UI payload; hydration on session open.
