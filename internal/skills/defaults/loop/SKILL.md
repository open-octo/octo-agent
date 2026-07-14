---
name: loop
description: Run a prompt or task repeatedly in the current session — on a fixed interval, or self-paced where you decide the cadence and when to stop. Use when the user wants something checked or run again and again without re-prompting each time, e.g. "/loop 5m check the deploy", "每隔几分钟跑一次", "keep running this until it's green", "poll the build every 2 minutes", "循环执行", "盯着这个直到完成". Trailing text after the interval is the task to loop. Do NOT use for a schedule that must survive a restart or run while the user is away — that's cron-task-creator.
---

# Loop a task in this session

Keep a task running in the **current session** without the user re-prompting,
using the **`schedule_wakeup`** tool — it re-runs your `prompt` as a fresh user
turn after a delay. The tool's own description carries the operational detail:
how to shape each tick, the cadence/cache trade-offs, and how to stop. This
skill only maps a `/loop` invocation onto that tool.

This is an **in-session** loop — it lives in this conversation. For a schedule
that must survive restarts or fire while nobody is watching, use
**cron-task-creator** instead.

## Read the invocation

The text after `/loop` is `[interval] <task>`:

- **Interval given** (`/loop 5m check the build`, `/loop 30s …`, `/loop 2h …`) →
  **interval mode**: parse the leading duration to seconds and call
  `schedule_wakeup` **once** with `repeat=true`. The cadence re-arms on its own —
  do not call the tool again on each tick.
- **No interval** (`/loop keep refining the draft until it reads cleanly`) →
  **dynamic mode**: call `schedule_wakeup` with `repeat=false` and re-arm each
  turn; end the loop by simply not re-arming.

If the task itself is unclear, ask one clarifying question before starting — a
loop that runs the wrong task wastes every iteration.
