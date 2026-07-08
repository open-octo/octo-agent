---
title: Repeat a task with /loop
description: Keep re-running a prompt in the current session without re-typing it every time.
---

`/loop` keeps re-running a task in the **current session** without you re-prompting each time, by
having the model schedule its own next turn.

```text
/loop 5m check the deploy
/loop keep refining the draft until it reads cleanly
```

The text after `/loop` is `[interval] <task>`:

- **An interval given** (`/loop 5m …`, `/loop 30s …`, `/loop 2h …`) — **interval mode**: the task
  runs on that fixed cadence until stopped.
- **No interval** (`/loop keep polling until the build is green`) — **dynamic mode**: the model
  picks the cadence itself each turn, and decides on its own when the loop is done.

Under the hood both modes use the same `schedule_wakeup` tool: after a turn ends and the session
goes idle, the system re-runs the task as a fresh user turn once the delay elapses. Delays are
clamped to **60 seconds – 1 hour** per tick.

## It coexists with the conversation

A loop doesn't block you out of the session — you can talk to the model mid-loop (ask a question,
give a side instruction) and the loop keeps ticking; a plain message does not stop it.

## Stopping a loop

- **Dynamic mode** stops itself: once the model decides the task is done (or stuck), it simply
  doesn't schedule another wakeup and tells you why.
- **Interval mode** re-arms on every tick, so ask explicitly ("stop the loop") to end it.
- **Ctrl+C** in the TUI, or `/stop` in an IM channel, hard-stops any loop immediately.
- Every loop auto-stops after a safety cap of roughly **12 hours** of total runtime regardless — a
  backstop for a forgotten loop, not something to rely on instead of stopping it yourself.

## `/loop` vs. a cron task

`/loop` lives inside one conversation: it's cheap to start, ends when you say so or when you close
the session, and has no persistence of its own. For a schedule that needs to **survive a restart**
or **fire while nobody is watching** — a daily report, a recurring health check — use a
[scheduled cron task](/docs/guides/cron-tasks/) instead; it's a separate, durable mechanism run by
`octo serve`, not a `/loop` running unattended.

Next: [session goals](/docs/guides/goals/) build on the same underlying wakeup machinery, applied to
a single durable objective instead of a recurring prompt.
