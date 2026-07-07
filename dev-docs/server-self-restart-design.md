# Server Self-Restart

`octo serve` can restart itself: the process is split into a tiny supervisor
parent and a worker child, connected by an exit-code contract. A restart
request drains the worker gracefully and exits with a reserved code; the
parent re-spawns the worker from the binary path on disk. This is the
mechanism behind two scenarios:

- **Binary upgrade** — a new `octo` binary is placed at the same path, then a
  restart picks it up.
- **Config change** — anything resolved once at startup (provider, model,
  system prompt, `~/.octo` config, `channels.yml`) is re-read by the fresh
  worker.

## Goals

- Restart triggered from inside the server: HTTP API, web UI, or the agent
  itself responding to an IM/web instruction ("重启一下服务").
- One code path on Linux, macOS, and Windows — spawn + wait only, no
  `exec(2)`, no fd passing.
- No external supervisor required, but compose cleanly with one
  (systemd/launchd) when present.
- Bounded graceful drain; session persistence is the safety net beyond the
  bound.

## Non-goals

- **Zero-downtime restart** (nginx-style listener handoff, tableflip). octo
  serve is a personal agent server; a few seconds of port downtime is
  acceptable, web clients already reconnect WS and replay. tableflip also
  has no Windows support.
- **Crash supervision.** The parent does not health-check or auto-restart a
  crashed worker; restart is always a deliberate request. A non-restart exit
  code propagates and the parent exits with it.
- **Fetching or building binaries.** The restart machinery picks up whatever
  binary is at the path; producing that binary (e.g. the agent running
  `make build`, or a future self-update command) is outside this design.

## Process model

`octo serve` runs in supervisor mode by default. The parent:

1. Spawns the worker: same binary path (captured via `os.Executable` at
   startup), same argv, with `OCTO_SERVE_WORKER=1` in the environment.
   stdout/stderr pass through, so logs look exactly like today (stdin is not
   wired — the server never reads it).
2. Waits for the worker to exit.
3. Dispatches on the exit code:
   - `42` (`server.ExitRestart`) — re-spawn from the stored binary path and
     loop. The path is re-opened on each spawn, so a replaced binary takes
     effect.
   - anything else — exit with the same code. `0` stays `0`, crashes
     propagate (a signal-killed worker's `-1` normalises to `1`).

When `OCTO_SERVE_WORKER=1` is set, `runServe` skips the supervisor and runs
the server directly — this is both how the parent's child runs and the opt-out
for users who run octo under systemd/launchd and want their own supervision
(`RestartForceExitStatus=42` / `KeepAlive` then own the respawn). A
`--no-supervisor` flag sets the same mode explicitly.

### Signals

On POSIX, terminal Ctrl-C delivers SIGINT to the whole foreground process
group, so parent and worker both receive it. The parent treats a received
SIGINT/SIGTERM as "quitting": it stops respawning, waits for the worker (whose
existing signal handler runs `Shutdown`), and exits with the worker's code.
When the parent is signalled directly (e.g. `kill <parent-pid>`), it forwards
the signal to the worker. On Windows forwarding is a no-op: the worker already
received the console Ctrl-C event (same console group), and the only
alternative, `Process.Kill`, would land before the worker's graceful Shutdown
and orphan its background processes. A signal that races the worker's exit is
drained before the respawn decision, so the supervisor never spawns a worker
that is already doomed.

## Restart triggers

- `POST /api/restart` — responds `202 Accepted` immediately, then starts the
  drain. This is the endpoint the web UI's version popover calls. It sits on
  the API mux with the same auth posture as every other endpoint (the
  `requireAuth` wrapper is currently a pass-through; the API's trust model is
  the localhost-by-default bind address).
- `restart_server` tool — registered only by the server via
  `tools.SetRestarter` (the same setter pattern as the WebSocket asker), so
  the CLI and TUI never advertise it. The tool requires a `reason`, returns
  success text immediately ("restart scheduled after this turn"), and the
  drain naturally waits for the calling turn to finish, so the agent's final
  reply ("restarting now…") reaches the user before the connection drops.

The tool is ask-class in `internal/permission/defaults.yml` (explicitly, so
nobody relaxes it by accident). On the web that means the browser
confirmation prompt; on IM channels the in-chat reply prompt (see
`im-interactive-ask-design.md` — explicit affirmative only). Server
sub-agents see the tool (the restarter registration is process-global) but
inherit the parent's gate, so they face the same confirmation.

## Drain sequence

A restart request flips the server into draining state (`drainGate` in
`internal/server/drain.go` counts in-flight turns; every turn-execution path
registers with it):

1. **Gate intake.** New turns are refused at every entry point, each in its
   transport's retryable shape: HTTP turn endpoints return `503`, the
   WS path broadcasts an error event, scheduled task runs return an error,
   and an IM message gets a polite "send that again in a moment" reply. IM
   adapters deliberately stay up through the drain — the turn that triggered
   the restart must deliver its final reply through a live adapter. Turn
   loops also stop chaining queued steer messages into fresh turns once the
   drain starts.
2. **Wait for in-flight turns**, bounded by a drain timeout (default 30s).
3. **Shutdown.** The existing single-flight `Shutdown` path runs: stop IM
   adapters, kill background processes, kill session sub-agents, MCP
   cleanup, `http.Shutdown`.
4. `ListenAndServe` returns `ErrRestartRequested`; `runServe` returns exit
   code `42`. (A plain shutdown — Ctrl-C — surfaces as `nil` and exit 0.)

The timeout is a hard bound, not a negotiation: a turn still running at 30s is
abandoned. Round-granularity session persistence plus the WS replay buffer
already cap the damage at one round, and clients reconnect to the new worker —
restart inherits the crash-durability guarantees rather than building its own.

## Upgrade flow

1. New binary lands at the path the parent stored at startup.
   - POSIX: rename-over-running-binary is safe; the old process keeps its
     inode.
   - Windows: a running exe can't be overwritten but *can* be renamed — the
     upgrade step renames `octo.exe` → `octo.exe.old`, moves the new binary
     into place, and the next worker start removes a leftover `.old`.
2. Restart is triggered (API or tool).
3. The parent re-spawns from the path; the new binary serves.

The parent itself never upgrades mid-flight — it is a wait-loop of a few dozen
lines and is expected to be version-compatible across worker upgrades. A
parent-side change requires a full stop/start, which is fine: that's the
frequency of supervisor changes, not of worker changes.

## Config change flow

The worker resolves provider, model, system prompt, skills, env context, and
memory dirs once in `server.New`; channels come from `channels.yml` at
`startChannels`. A restart re-runs all of it. The parent re-spawns with the
original argv, so flag-level settings survive the restart unchanged while
file-based config is re-read. Config surfaces that already hot-reload (the MCP
panel rewriting `mcp.json`) keep working without a restart; restart is the
catch-all for everything else.

## Components

| Piece | Where | What |
|---|---|---|
| Supervisor loop | `cmd/octo/serve_supervisor.go` + `serve.go` | spawn/wait/dispatch, signal handling, `OCTO_SERVE_WORKER` detection, `--no-supervisor` |
| Exit code | `internal/server/restart.go` | `ExitRestart = 42`, `ErrRestartRequested`, `Restart()` |
| Drain state | `internal/server/drain.go` | `drainGate`: in-flight count, intake refusal, drain wait + timeout |
| Restart endpoint | `internal/server/restart.go` | `POST /api/restart`, 202 then background drain |
| Restart tool | `internal/tools/restart.go` + server wiring | `restart_server`, `SetRestarter`-injected, permission-gated |

## Testing

- Drain gate: unit tests cover begin/end pairing, drain-waits-for-active-turn,
  timeout-reports-dirty, and multi-turn ordering.
- Supervisor loop: the spawn/wait/signal mechanics sit behind an injectable
  spawn function, so the loop's respawn/propagate/signal semantics are covered
  by in-process fakes. No live network, per project rule.
- Endpoint + wiring: `httptest` through the real mux — 202 on restart,
  draining 503 on turn endpoints, scheduled-run refusal, polite IM reply via a
  fake adapter, restart-vs-plain-shutdown sentinel on a real ephemeral-port
  listener, single-flight `Shutdown` under `-race`.
- CI matrix (Linux/macOS/Windows) exercises the spawn/wait path on all three;
  the Windows rename dance belongs to the (future) upgrade flow, not this
  machinery.
