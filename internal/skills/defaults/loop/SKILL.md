---
name: loop
description: Run a prompt or task repeatedly in the current session — on a fixed interval, or self-paced where you decide the cadence and when to stop. Use when the user wants something checked or run again and again without re-prompting each time, e.g. "/loop 5m check the deploy", "每隔几分钟跑一次", "keep running this until it's green", "poll the build every 2 minutes", "循环执行", "盯着这个直到完成". Trailing text after the interval is the task to loop. Do NOT use for a schedule that must survive a restart or run while the user is away — that's cron-task-creator.
---

# Loop a task in this session

Keep running a task in the **current session** without the user re-prompting,
by scheduling your own next turn with the **`schedule_wakeup`** tool. After a
turn ends and the session goes idle, the system re-runs your `prompt` as a fresh
user turn after the delay — so you come back, do the work again, and decide
whether to continue.

This is an **in-session** loop: it lives in this conversation and stops when the
user steps in. For a schedule that must persist across restarts or fire while
nobody is watching, use the **cron-task-creator** skill instead.

## Read the invocation

The text after `/loop` is `[interval] <task>`:

- **Interval given** (`/loop 5m check the build`, `/loop 30s …`, `/loop 2h …`) →
  **interval mode**. Parse the leading duration to seconds and run the task on
  that fixed cadence.
  - **Rule of thumb:** if the user names a concrete interval, use interval mode
    with `repeat=true`. The system will re-run the task on that cadence; you do
    **not** need to call `schedule_wakeup` again on each tick. Once the goal is
    met (e.g. the PR is merged, CI is green), call `schedule_wakeup(cancel=true)`
    to stop the loop.
- **No interval** (`/loop keep refining the draft until it reads cleanly`) →
  **dynamic mode**. You choose the cadence each turn and you decide when to stop.

If the task itself is unclear, ask one clarifying question before starting — a
loop that runs the wrong task wastes every iteration.

## Interval mode — fixed cadence

Use this when the user wants the same task checked or run on a **steady
cadence** (e.g. polling a CI job, a deploy, a PR merge status, or a queue).

Do the task once, then call `schedule_wakeup` **with `repeat: true`** so the
wakeup re-arms itself automatically:

```
schedule_wakeup(delay_seconds=120, repeat=true,
                reason="checking if PR is merged every 2 minutes",
                prompt="Check whether the PR has been merged. If it has, say so and stop the loop by cancelling the wakeup. If not, just report the current status.")
```

You only call it **once** — the cadence holds on its own. Each tick re-runs
`prompt` as a fresh turn. Pass the task verbatim as `prompt` so every tick does
the same work. **Do NOT call `schedule_wakeup` again inside the tick; the timer
re-arms automatically.** The loop keeps ticking until it's stopped (see
**Stopping**).

## Dynamic mode — you set the pace

Use this when the user did **not** specify an interval, or when the next step
depends on what you learn in this turn and you want to decide the cadence
yourself (e.g. "keep refining this draft until it reads cleanly"). Do **not**
use dynamic mode for fixed-interval polling; that is what interval mode is for.

```
schedule_wakeup(delay_seconds=900, reason="<what you're waiting for>",
                prompt="<the same task, so the next turn repeats it>")
```

In dynamic mode the wakeup fires **once** — to keep looping you must call
`schedule_wakeup` again on the next turn. **Simply not calling it ends the
loop.** This is how you stop: when the task is done (or can't make progress),
finish your reply *without* scheduling a wakeup, and tell the user the loop is
done and why.

## Choosing the delay (cache-window aware)

`delay_seconds` is clamped to **[60, 3600]**. The model's prompt cache has a
~5-minute TTL, so:

- **Under 5 minutes (60–270s)** — context stays warm. Right when you're actively
  polling external state that changes on that scale: a CI run, a deploy, a queue.
- **5 minutes to 1 hour (300–3600s)** — you pay a cache miss, but it's worth it
  when there's genuinely nothing to check sooner.
- **Avoid exactly 300s** — it pays the miss without amortizing it. Drop to ~270s
  to stay warm, or commit to 1200s+ so one miss buys a long wait.
- **Idle ticks with no specific signal** → default to **1200–1800s**.

Think about *what you're waiting for*, not a round number of minutes. If you're
polling an ~8-minute CI run, sleeping 60s burns the cache eight times before it
finishes — sleep ~270s twice instead.

## The `reason` field

One short, specific sentence on what you're waiting for and why this cadence —
it's shown to the user so they can see what the loop is doing. "watching CI run"
beats "waiting".

## Stopping

The loop **coexists with the conversation**: the user can talk to you mid-loop —
ask a question, give a side instruction — and the loop **keeps ticking**. A user
message does *not* stop it. Stop it explicitly:

- **Dynamic mode**: stop by *not* calling `schedule_wakeup` on a turn — say the
  loop is done and why.
- **Interval mode**: it re-arms on its own, so to stop it you must call
  `schedule_wakeup(cancel=true)`. Do this **as soon as the user asks you to stop
  or pause** ("停掉", "stop the loop", "够了"). Acknowledge that you've stopped it.
- In the TUI the user can hard-stop any loop with **Ctrl+C**; over an IM
  channel, `/stop` does the same.
- **Safety cap**: every loop auto-stops after a maximum total runtime (~12h)
  so a forgotten loop can't tick forever — don't rely on it, stop loops
  yourself when the work is done.

When the user interjects mid-loop, answer them normally — and unless they asked
you to stop, the loop carries on; mention the next tick so they know it's still
running.

## Give every iteration a clear stop/idle condition

An open-ended task makes each tick spin until it times out. Spell out what
"nothing to do" looks like so a quiet tick is cheap:

- Bad: "Check if the deploy is done."
- Good: "Run `kubectl rollout status …` once. If it reports complete, say so and
  STOP looping (don't schedule another wakeup). If still rolling out, reply with
  the current status in one line and schedule the next check."
