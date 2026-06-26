# Terminal Manual Promotion

**Status:** Implemented  
**Scope:** `internal/tools` · `cmd/octo` · `internal/server` · `web/src`

## Problem

The sync terminal path secretly starts every command as a hidden background process, then promotes it after a fixed 2-minute timer if it hasn't exited. The result: the model issues a synchronous call expecting a result, and 120 seconds later receives a background process ID it didn't ask for — a surprising state transition driven by a timer rather than user intent.

| Transport | Old behavior | New behavior |
|---|---|---|
| TUI | Promotes at 120 s regardless | `Ctrl+B` promotes immediately; timer stays as backstop |
| Web | Promotes at 120 s regardless | "Background" button on tool card; timer backstop |
| IM | Timer fires at 120 s | No change — timer is the only mechanism |

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
id, _ := mgr.Start(command, WithOnLine(onLine), WithVisible(false))

sess := mgr.BeginSync()
defer mgr.EndSync()

select {
case <-sess.C():
    // User promoted — identical outcome to timer, different result text.
    mgr.Promote(id)
    return ToolResult{Text: fmt.Sprintf("… [promoted to background process %s]\n\n%s", id, BgPollNotice)}

case <-timer.C:
    // Timer backstop — covers IM and forgotten browser tabs.
    mgr.Promote(id)
    return ToolResult{Text: fmt.Sprintf("… [timeout: command exceeded %s …]\n\n%s", TerminalTimeout, BgPollNotice)}

case <-ctx.Done():
    // Kill on interrupt — unchanged.
    mgr.Kill(id); mgr.Remove(id)
    return ToolResult{Text: body + "\n[exit: signal: killed]"}
}
```

The two promote paths return distinct result text so the model can tell whether the user triggered it or the timer did.

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
- `run_in_background:true` commands bypass `BeginSync` entirely — they're already async.
