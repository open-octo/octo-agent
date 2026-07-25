# Cross-Client Ask Question

## Problem

`ask_user_question` currently broadcasts only to WebSocket connections
subscribed to the **originating session**. This means:

1. The mobile app (a different session) never sees questions raised from the
   desktop, and vice versa.
2. Even the desktop modal only renders the active session's question — a
   question raised from session A is invisible while you're viewing session B.

The goal: a question asked from **any** client must be answerable from **any**
client.

## Current flow

```
agent turn → wsAsker.Ask()
               │
               ├─ broadcast(sessionID, request_user_question)  ← session-scoped
               ├─ broadcast("", session_activity question_pending)  ← global (badge only)
               └─ blocks on questionChans[qid]

browser (active session) → ws.on('request_user_question') → QuestionModal
browser (other session)  → never receives it
mobile                  → never receives it (no handler at all)
```

The reply path (`user_question_answer` with `qid`) is already client-agnostic —
any connected client can answer any pending question by ID. Only the *delivery*
is session-scoped.

## Design: global broadcast

Change the delivery from session-scoped to **global** (all connected WebSocket
clients). Everything else stays the same.

### Server (`internal/server/server.go`)

`wsAsker.ask()`:

```go
// before
a.s.wsHub.broadcast(sessionID, ev)
// after
a.s.wsHub.broadcast("", ev)   // "" = global (all connections)
```

Same for the dismiss broadcast and the `session_activity` events (the activity
events are already global — keep them that way).

### Pending-question replay (`internal/server/ws_handlers.go`)

On `(re)subscribe`, currently replays only the subscribing session's pending
question. Change to replay **all** pending questions so a freshly-loaded client
(paused tab re-opened, mobile app launched after a question was asked) surfaces
everything outstanding:

```go
// before: replay s.pendingQuestions[sessionID] only
// after:  iterate s.pendingQuestions, replay every entry
```

The `questionChans` map is already keyed globally by `qid`, so any client's
answer resolves the single in-flight `Ask()` — no change there.

### Desktop (`web/src/components/overlays/QuestionModal.svelte`)

Currently renders only `$questionModals[$activeSessionId]`. Change to render a
**stack** of all entries in the `questionModals` store (one card per pending
question), so questions from any session are visible and answerable regardless
of which session tab is active. The store is already keyed by session ID, so this
is a presentation change.

### Mobile (`web/src/mobile/`)

Add a new `QuestionOverlay.svelte` that:

- Listens for `request_user_question` on the WS shared by the mobile shell.
- Renders a mobile-native question card (header + tappable option pills + free
  text input + submit/cancel), reusing the mobile design tokens from
  `theme.css`.
- Sends `user_question_answer` back through `ws.answerQuestion()`.

Wire it into `MobileApp.svelte` at the root level so it overlays any tab.

## Out of scope

- Push wakeups for killed apps (the server-side `internal/tunnel` wakeup frame
  exists; the mobile push-token wiring is deferred — see mobile README).
- Question targeting (always broadcast to all of the user's clients; no
  per-client routing).

## Verification

1. Start `octo serve --tunnel` + `octo-relay`; open the desktop web UI in one
   tab and the mobile app (simulator) in another, each in a different session.
2. Trigger an ask from a desktop session → modal appears on **both** desktop
   and mobile.
3. Answer on mobile → desktop modal dismisses; the agent turn resumes.
4. Trigger from mobile → appears on desktop; answer there → mobile dismisses.
5. Refresh the desktop tab mid-ask → the pending question replays (regression
   guard for the existing "pending prompt" replay path).
