# Background workflow execution

The `workflow` tool runs in the background on every transport. A call registers
a run, starts it detached from the turn, and returns a run handle immediately;
the model collects the outcome later through the `workflow_status` tool. The web
UI additionally renders a live panel of background runs. This keeps a long
multi-agent workflow from blocking the turn (and the user) while it executes.

## Why background-only

A workflow that fans out to dozens of sub-agents can run for minutes. Run
synchronously, it holds the turn open the whole time: the user can't interact,
and a transport with a request timeout may drop the connection. Making
*every* call background — rather than an opt-in flag — keeps one contract
across CLI, web, and IM, and matches how the async sub-agent system already
behaves.

## Lifecycle

A run is owned by a `WorkflowManager`, not by the turn:

1. `workflow(script)` → the tool registers the script with the session's
   `WorkflowManager`, which assigns a run id (`wf-…`, reusing the journal id
   format) and launches `workflow.Run` in a goroutine under a **detached
   context** (derived from `context.Background()`, not the turn ctx) so the run
   survives turn completion.
2. The tool returns immediately: the run id plus a one-line instruction to poll
   `workflow_status`.
3. The goroutine streams progress (`log()` lines + agent lifecycle) into the
   run's buffer and, on web, onto a WS event stream.
4. On completion the manager stores the final result (or error) and fires a
   notification hook — the same mechanism async sub-agents use to nudge the
   model on the next turn.

The manager mirrors `SubAgentManager`: a process-global default for CLI/TUI,
and per-session instances (`SessionWorkflowManager(id)`) stamped onto the ctx by
the web server and IM bridge so each conversation's runs are isolated and reaped
on session close.

## Tools

- **`workflow(script, description?, resume_from?)`** — unchanged inputs, new
  contract: starts the run in the background and returns
  `[workflow run: wf-…] started — poll workflow_status to collect the result`.
- **`workflow_status(run_id?)`** — no id: list this session's runs with their
  status (`running` / `done` / `error`) and a one-line label. With an id: the
  full result (or error + resume hint) plus the captured log. This is the
  collection path on every transport, including CLI/IM where there is no panel.

## Web panel (Phase 2)

A dedicated right-sidebar panel (sibling to the artifacts / sub-agents panels)
lists background runs for the active session, each with live status, elapsed
time, the streaming progress tail, and the final result on completion. Fed by
`workflow_started` / `workflow_progress` / `workflow_done` WS events broadcast
by the server-side manager hook.

## Phasing

1. **Backend foundation** — `WorkflowManager`, the async `workflow` tool
   contract, the `workflow_status` tool, per-session wiring (app/server/CLI/IM),
   completion notifications, tests. Transport-agnostic; complete on its own.
2. **Web panel** — the Svelte panel + WS events + server broadcast.

## Non-goals

- No persistence of running workflows across a daemon restart — a run is
  in-memory for the process lifetime (the journal already lets the model resume
  a crashed run via `resume_from`).
- No change to the DSL surface (`agent`/`parallel`/`pipeline`/`log`/`phase`).
