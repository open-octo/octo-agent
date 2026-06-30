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
- **No interval** (`/loop keep refining the draft until it reads cleanly`) →
  **dynamic mode**. You choose the cadence each turn and you decide when to stop.

If the task itself is unclear, ask one clarifying question before starting — a
loop that runs the wrong task wastes every iteration.

## Interval mode — fixed cadence

Do the task once, then call `schedule_wakeup` **with `repeat: true`** so the
wakeup re-arms itself automatically:

```
schedule_wakeup(delay_seconds=300, repeat=true,
                reason="polling the deploy every 5m until it's live",
                prompt="<the task to run each tick>")
```

You only call it **once** — the cadence holds on its own. Each tick re-runs
`prompt` as a fresh turn. The loop stops when the user sends a message or
interrupts. Pass the task verbatim as `prompt` so every tick does the same work.

## Dynamic mode — you set the pace

Do the task, then call `schedule_wakeup` **with `repeat: false`** (the default)
to come back later:

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

- **Dynamic mode**: stop by *not* calling `schedule_wakeup` — say the loop is done.
- **Interval mode**: it runs until the user interrupts or sends a message.
- Either way, **a new user message cancels the loop** and hands control back to
  the user. You can re-arm `/loop` later if asked.

## Give every iteration a clear stop/idle condition

An open-ended task makes each tick spin until it times out. Spell out what
"nothing to do" looks like so a quiet tick is cheap:

- Bad: "Check if the deploy is done."
- Good: "Run `kubectl rollout status …` once. If it reports complete, say so and
  STOP looping (don't schedule another wakeup). If still rolling out, reply with
  the current status in one line and schedule the next check."
