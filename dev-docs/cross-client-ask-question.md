# Cross-Client Ask Question

## Problem

`ask_user_question` currently broadcasts only to WebSocket connections
subscribed to the **originating session**. This means:

1. The mobile app (a different session) never sees questions raised from the
   desktop, and vice versa.
2. Even the desktop modal only renders the active session's question — a
   question raised from session A is invisible while you're viewing session B.

The goal: a question asked from **any** client must be answerable from **any**
client — **without interrupting** a conversation the user is already in.

## No-interruption rule

The modal must appear **only for the session the user is currently viewing**.
A question arriving for a *different* session must surface as a **non-intrusive
notification** (toast / sidebar badge) the user can click to navigate to the
right session. Only on navigating to that session does the modal appear.

- Viewing session A, session B asks → notification only, no modal.
- User clicks notification → switches to session B → modal appears.
- User leaves session B back to A without answering → no modal; the question
  stays pending in session B and shows again only when the user next opens B.

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

## Design: global broadcast + active-session-only modal

Change the delivery from session-scoped to **global** (all connected WebSocket
clients), but keep the **modal** scoped to the active session. Non-active
questions surface as a clickable notification.

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

**Modal renders only the active session's question** (the existing behavior —
do NOT change this). A question arriving for a *different* session populates
the store but does not show a modal; instead it shows a **toast notification**
with the question text and a "View" action that switches to that session (where
the modal then appears).

The `questionModals` store is keyed by session ID. The modal reads
`$questionModals[$activeSessionId]` only. A separate toast (via the existing
toast store) fires on any `request_user_question` whose `session_id !==
$activeSessionId`.

### Mobile (`web/src/mobile/`)

Add a new `QuestionOverlay.svelte` that:

- Listens for `request_user_question` on the WS shared by the mobile shell.
- **Active session**: renders a mobile-native question card (header + tappable
  option pills + free-text input + submit/cancel).
- **Non-active session**: shows a **toast** with the question preview and a
  "View" action that navigates to that session (where the card then appears).
- Sends `user_question_answer` back through `ws.answerQuestion()`.

Wire it into `MobileApp.svelte` at the root level so it overlays any tab.

## Out of scope

- Push wakeups for killed apps (the server-side `internal/tunnel` wakeup frame
  exists; the mobile push-token wiring is deferred — see mobile README).
- Question targeting (always broadcast to all of the user's clients; no
  per-client routing).

## Verification

1. Start `octo serve --tunnel` + `octo-relay`; open the desktop web UI in one
   tab and the mobile app (simulator) in another, each viewing a different
   session.
2. Trigger an ask from desktop session B while viewing session A → toast
   appears (no modal); sidebar badge updates.
3. Click the toast / badge → switches to session B → modal appears.
4. Answer on mobile → desktop modal dismisses; the agent turn resumes.
5. Trigger from mobile while desktop views a different session → desktop shows
   toast only; navigate to that session → modal appears.
6. Refresh the desktop tab mid-ask → the pending question for the *active*
   session replays as a modal; others surface as toast/badge only.
