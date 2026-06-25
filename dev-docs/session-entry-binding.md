# Session Entry Binding

> A session can be actively used from only one entry at a time. The session
> file is the single source of truth; server processes keep a short-lived
> in-memory cache purely as an optimisation.

---

## Problem

octo-agent has multiple entry points (`cli`, `tui`, `web`, `api`, `channel`,
`cron`, `setup`) that all operate on the same persisted session files. Before
this design, nothing prevented two entries from running turns against the same
session concurrently. This led to:

- **Interleaved transcript writes** — two turns append messages to the same
  JSONL file in an undefined order.
- **Lost state changes** — one entry's `Compact` or model change could overwrite
  another's.
- **Confusing UX** — a user could have the TUI and the Web UI both "live" on
  the same session without knowing which one was in control.

## Decision

Add a binding that ties each session to exactly one entry. Binding is
authoritative on disk; an in-process cache only avoids re-reading the file on
 every turn. Any process that loads the session can see who owns it and whether
 a turn is in flight.

## Model

### `Session` fields

- `BoundEntry string` — the entry that currently owns the session (empty if
  unbound).
- `BoundAt time.Time` — when `BoundEntry` was last set, for diagnostics.
- `LeaseEntry string` and `LeaseExpires time.Time` — cross-process
  "turn-in-flight" marker. Written as an append-only `lease` record so it can
  be updated without rewriting the whole file.

`InFlight int` still exists but is purely in-memory and per-process; the lease
is what protects against concurrent turns across processes.

### Records on disk

The transcript file already stores `meta`, `message`, `title`, and
`model_config` JSONL records. We add `type: "lease"`:

```json
{"type":"lease","lease_entry":"web","lease_expires":"2026-06-26T17:45:00Z"}
{"type":"lease","lease_entry":"","lease_expires":"0001-01-01T00:00:00Z"}
```

`LoadSession` takes the **last** lease record as authoritative. An empty
`lease_entry` clears the marker. This keeps binding updates cheap (append-only)
while still letting `rewriteAll` fold the final lease back into the meta header
when the file is compacted.

### Binding lifecycle

1. **Creation** — every entry that creates a session immediately calls
   `session.Bind(entry, false)` and saves. IM channel sessions also write the
   binding at creation so the Web UI sees them as occupied.
2. **Turn start** — the server writes a lease (`WriteLease(entry, now+2m)`)
   after acquiring the binding. The lease is visible to other processes on
   their next `LoadSession`.
3. **Turn end** — the server clears the lease (`ClearLease`) and optionally
   unbinds if it does not expect another immediate turn.
4. **Takeover** — a different entry may steal the binding only if:
   - the caller passes `steal=true`;
   - the current owner's lease has expired (or there is no lease); and
   - the current owner is not this process (checked via the in-memory cache).
5. **Exit / unbind** — TUI unbinds on exit; IM `/unbind` releases the binding;
   Web SSE/WebSocket release after the turn.

## Server implementation

`internal/server/server.go` owns the binding machinery:

- `acquireSessionBinding(id, entry, steal)` reloads from disk, claims/releases,
  and refreshes the cache. It is serialised per session id by
  `sessionBindingLocks`.
- `releaseSessionBinding(id, entry)` reloads before clearing so it does not
  clobber a binding set by another process after the turn started.
- `cachedEntryBinding` keeps a 30-second in-process cache; the on-disk lease
  is written for 2 minutes.

The binding is acquired at:

- `POST /api/chat` (create + bind)
- `POST /api/chat/:id/turn`
- SSE turn
- WebSocket user message
- Cron task execution
- IM `/compact`
- IM channel message

`handleUpdateSessionModel` also acquires the binding before mutating the model.

## CLI / TUI

`cmd/octo/chat.go` binds on create/resume and unbinds before exit.
`cmd/octo/tuirepl.go` binds when starting/resuming a TUI session and unbinds
(and saves) on exit. Because the CLI/TUI owns the process, it does not reload
from disk before binding; it writes its binding directly and saves.

## IM channel

`internal/channel/persist.go` preserves an existing `BoundEntry` when restoring
a session and sets `BoundEntry = EntryChannel` for newly created sessions.
`internal/channel/manager.go` rejects `/bind` to a session owned by another
entry and releases `BoundEntry` on `/unbind`.

## Trade-offs

- **Lease expiry = 2 minutes.** Long enough to cover most turns, short enough
  that a crashed process releases quickly. Very long turns may need to extend
  the lease; currently the lease is written once per turn.
- **Server cache = 30 seconds.** Avoids re-reading the session file on every
  request, but a process crash clears the cache (the file lease still protects
  cross-process ownership).
- **CLI/TUI do not reload before binding.** This is acceptable because a
  single OS-level process is the owner; concurrent access within one process
  is already serialised by the process itself.
- **Takeover cannot see in-flight turns in another process.** It relies on the
  lease for that. If the lease expires while a turn is still running (e.g., a
  very long provider call), another process could theoretically steal. This is
  intentional: a lease longer than the longest reasonable turn would make
  recovery after crashes too slow.

## Testing

- `internal/agent/session_test.go` — binding semantics and lease-based steal
  rejection.
- `internal/channel/persist_test.go` — IM `/bind` rejection and `/unbind`
  release.
- `internal/server/channel_route_test.go` — channel message flows with
  binding.

All tests run with `-race` locally and in CI (macOS, Ubuntu, Windows).
