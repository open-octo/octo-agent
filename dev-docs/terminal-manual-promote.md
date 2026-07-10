# Terminal Manual Promotion

**Status:** Implemented  
**Scope:** `internal/tools` · `cmd/octo` · `internal/server` · `web/src`

## Problem

A synchronous terminal command runs under a timeout (`TerminalTimeout` = 120 s by default, or the call's explicit `timeout`). On timeout it is **killed and an error returned** — deterministic, and the model never gets back a process id it didn't ask for. But a command is sometimes legitimately slow, and a human watching would rather keep it running than have it killed and re-run (which for an expensive build/test wastes the work already done). Manual promotion is that escape hatch: the user turns the still-running synchronous command into a tracked async background process **before** the timeout fires, so it keeps running instead of being killed.

| Transport | Behavior |
|---|---|
| TUI | `Ctrl+B` promotes the running sync command; otherwise it's killed at the timeout |
| Web | "Background" button on the tool card; otherwise killed at the timeout |
| IM | No promote UI — the timeout just kills |

## Signal Primitive — SyncSession

`SyncSession` wraps a channel with `sync.Once` so multiple callers are safe. `BackgroundManager` holds one slot (at most one sync terminal runs per session — the agent loop is serial).

```go
// internal/tools/background.go

type SyncSession struct {
    ch   chan struct{}
    once sync.Once
}

func (s *SyncSession) Signal() { s.once.Do(func() { close(s.ch) }) }
func (s *SyncSession) C() <-chan struct{} { return s.ch }

// BackgroundManager gains a sync-session slot.
type BackgroundManager struct {
    // … existing fields …
    syncMu   sync.Mutex
    syncSess *SyncSession
}

func (m *BackgroundManager) BeginSync() *SyncSession
func (m *BackgroundManager) EndSync()
func (m *BackgroundManager) HasSync() bool
func (m *BackgroundManager) PromoteSync()

// Package-level helpers for TUI (delegates to defaultBg).
func HasActiveSync() bool
func PromoteCurrentSync()
```

## Terminal Tool

`ExecuteStream` brackets the sync polling loop with `BeginSync`/`EndSync` and adds a third `select` case:

```go
mgr := t.managerFor(ctx)
id, _ := mgr.Start(command, BgModeAsync, WithOnLine(onLine), WithVisible(false))

sess := mgr.BeginSync()
defer mgr.EndSync()

select {
case <-promoteCh:
    // User promoted (Ctrl+B / button) — the ONLY path that backgrounds a sync
    // command. Keeps it running; NOT reaped.
    mgr.Promote(id)
    return ToolResult{Text: fmt.Sprintf("… [promoted to async background process %s]\n\n%s", id, AsyncModeNotice)}

case <-timer.C:
    // Timeout — kill and reap, return the partial output plus an error. NOT promoted.
    mgr.Kill(id); mgr.Remove(id)
    return ToolResult{Text: fmt.Sprintf("%s\n[timeout: command exceeded %s and was killed — it was NOT moved to the background. …]", body, timeout)}

case <-ctx.Done():
    // Kill on interrupt (Esc / turn cancel) — unchanged.
    mgr.Kill(id); mgr.Remove(id)
    return ToolResult{Text: body + "\n[exit: signal: killed]"}
}
```

Only the manual-promote arm backgrounds the command; the timer kills it. `timeout` is `parseTimeout(input)` (the explicit `timeout`, else `TerminalTimeout`), shortened to the caller's ctx deadline when that's sooner.

A **sub-agent's** `terminal` call skips `BeginSync` entirely (guarded on `IsSubAgent(ctx)`), so it never occupies the manager's sync slot — `HasActiveSync` can't see it and `Ctrl+B` / the Web button can't target it — and `promoteCh` is a nil channel whose arm never fires. Its timeout still kills like any other; it just can't be rescued by a manual promote. Rationale: a sub-agent has no turn after the one that spawned it in which to read a promoted background process, and promoting one would leak its `[BACKGROUND COMPLETED]` notice into the parent session.

## TUI

`Ctrl+B` is a new binding, active only while a sync terminal is polling. `Esc` behavior is completely unchanged.

```go
// cmd/octo/tuirepl_view.go

case tea.KeyCtrlB:
    if m.turnRunning && tools.HasActiveSync() {
        tools.PromoteCurrentSync()
    }
    return m, nil
```

A hint line appears on the activity indicator only while `HasActiveSync()` is true:

```
⠿ terminal(go build ./...) (12s)
  [Ctrl+B] background  [Esc] kill
```

## Web

The browser sends `promote_sync_terminal` over WebSocket; the server routes it to the session's `BackgroundManager`.

```go
// internal/server/ws_hub.go
case "promote_sync_terminal":
    var msg wsInPromoteSyncTerminal
    json.Unmarshal(raw, &msg)
    tools.SessionBackgroundManager(msg.SessionID).PromoteSync()
```

```typescript
// web/src/lib/ws.ts
promoteSyncTerminal(sessionId: string): void {
    this.send({ type: "promote_sync_terminal", session_id: sessionId })
}
```

A "Background" button appears on running `terminal`/`bash` tool cards:

```svelte
{#if tool.name === 'terminal' || tool.name === 'bash'}
  <button class="promote-btn" onclick={() => ws.promoteSyncTerminal($activeSessionId)}>
    Background
  </button>
{/if}
```

**Note:** `handlers.go:677` already stamps `WithBackgroundManager(ctx, SessionBackgroundManager(sid))` on every web turn context, so `t.managerFor(ctx)` in the terminal tool correctly resolves to the per-session manager. The WS handler calls `SessionBackgroundManager(sessionID).PromoteSync()` on the same instance.

## IM

No code changes. The `sess.C()` case is wired but nobody fires it; the 2-minute timer fires as before.

## Invariants

- The promoted process continues running — no kill on promote.
- Partial output is returned on both promote paths.
- `TerminalTimeout` (120 s) is unchanged.
- `ctx.Done()` (kill) is unchanged.
- `run_in_background:"async"` / `"interactive"` commands bypass `BeginSync` entirely — they're already background tasks.
