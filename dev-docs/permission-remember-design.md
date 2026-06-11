# Permission "Always Allow" (Session Remember)

An ask-class permission prompt can be answered "always": the call is allowed
and the decision is remembered for the rest of the session, so the exact
same (tool, input) pair stops prompting. All three transports offer it:

| Transport | Prompt | Always answer |
|---|---|---|
| CLI/TUI | permission modal | the modal's always option (`UserResponse.Always`) |
| Web | confirmation modal (`kind: yes_no_always`) | "Always allow" button → result `always` |
| IM | in-chat reply prompt | `always` / `always allow` / `总是允许` / `一直允许` |

## Scope: session, exact signature

A remembered decision is keyed by the (tool, input) signature — the same
hash `permission.Engine` always used — so "always allow `sudo make
install`" does not allow `sudo rm`. It lives for the session and is never
written to `permissions.yml`: durable policy stays an explicit user edit.

## The store outlives the engine

The CLI keeps one engine for the whole session, so remembering on the
engine works there. The server and IM bridge rebuild their engine **every
turn** (to pick up policy and mode changes) — a decision remembered on the
engine would die with the turn. `permission.Remembered` is the extracted,
mutex-guarded decision store:

- `Engine` is born with a private store; `AttachRemembered` swaps in a
  shared one.
- The server keeps one store per session (`rememberedFor`, keyed by web
  session ID or `im:<session key>`), attaches it in `prepareToolTurn` /
  `handleChannelMessage`, and drops it with the rest of the session state
  in `forgetTurnLock`.
- The gate (`app.NewPermissionGate`) already called `engine.Remember` when
  an ask returned `remember=true`; nothing changed on that surface.

## Answer mapping

- Web: `mapConfirmResult` — `yes` → allow once, `always` → allow+remember,
  anything else denies.
- IM: `isAffirmative` covers both sets; `isAlways` flags the remember
  subset. Everything else (arbitrary replies, timeout, /stop) denies, as
  before.
- Auto-approve mode still bypasses prompting entirely; strict mode still
  denies ask-class outright. Remember only matters in interactive mode.

## The wire-shape bug this uncovered

Both web prompt events were dead on arrival: `wsEventRequestConfirmation`
and `wsEventRequestUserQuestion` carried no `session_id`, so the
dispatcher's session filter dropped them before rendering — every web
permission ask silently timed out to deny, and every web
`ask_user_question` hung. The confirmation event also sent `conf_id` where
the frontend reads `ev.id`, and the browser's answer (`id`) was parsed by
the server as `conf_id`, so even a rendered modal couldn't have replied.
The wire shapes now match what the dispatcher reads (`session_id` + `id`),
with `conf_id` still accepted on the answer path for compatibility.

## Testing

- `Remembered` shared across engine rebuilds (and isolated when not
  attached); per-session store identity + cleanup on session delete.
- Web: `mapConfirmResult` table; replay path carries `session_id`/`id`.
- IM: `总是允许` approves with remember; full store→ask→fresh-engine cycle.
