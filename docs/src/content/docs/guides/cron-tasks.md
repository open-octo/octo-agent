---
title: Schedule cron tasks
description: Recurring agent prompts that fire on a schedule, run by octo serve.
---

A cron task is a recurring agent prompt — "check the queue every 5 minutes", "write a daily
report at 9am" — that fires on its own while `octo serve` is running, with nobody watching.

```bash
octo serve
```

## How it works

Each task is a JSON file in `~/.octo/tasks/`, loaded by a scheduler that runs inside `octo serve`.
When a task fires, the scheduler runs one agent turn with the task's prompt. Each run is bounded by
a **30-minute wall-clock timeout** — the only hard cap.

**Tasks only fire while `octo serve` is running.** No serve, no runs — and a schedule missed while
the server was down is not replayed on restart.

## Task fields

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Human-readable task name |
| `cron` | yes | Schedule expression — see below |
| `prompt` | yes | The prompt sent to the agent on each run |
| `model` | no | Model override; defaults to the server's model |
| `agent` | no | `"general"` or `"coding"` |
| `directory` | no | Working directory the run executes in |
| `notify` | no | IM chats to push each run's final reply (or failure) to |
| `enabled` | yes | Whether the schedule is currently active |
| `session_mode` | no | `"shared"` (default) reuses one session across runs — history accumulates. `"fresh"` creates a new session every run — clean transcript each time. |

The prompt runs in its own session with no access to whatever conversation created the task, so it
needs to be self-contained: what to do, where, and what the output should look like. Give it an
explicit stop condition too — an open-ended prompt keeps the model re-verifying until the 30-minute
timeout instead of finishing once the answer is "nothing to report."

## Session modes

A task's `session_mode` controls whether runs share a session or start fresh:

- **`shared`** (default) — every run reuses the same session, so the agent sees its prior work and
  can build on it. Good for recurring reports that reference their own prior output.
- **`fresh`** — every run creates a brand-new session with an empty transcript. Good for one-shot
  reminders or tasks that should not see earlier runs. Previous sessions are kept on disk; the task
  list links to the most recent one for traceability.

The default is `"shared"` (and an empty value behaves as `"shared"`), so existing tasks are
unaffected. Switch a task's mode at any time via `PATCH /api/tasks/{id}` — the change takes effect on
the very next run. Any value other than `"shared"` or `"fresh"` is rejected with a 400 error.

## Cron expression — 6 fields, seconds first

The scheduler is [robfig/cron](https://github.com/robfig/cron) **with a seconds field** — a
standard 5-field crontab line is invalid here; always prepend a seconds field:

```
seconds minutes hours day-of-month month day-of-week
```

| Want | Expression |
|---|---|
| Every day at 09:00 | `0 0 9 * * *` |
| Every 30 minutes | `0 */30 * * * *` |
| Weekdays at 18:30 | `0 30 18 * * 1-5` |
| 1st of each month at 08:00 | `0 0 8 1 * *` |

Descriptors also work: `@hourly`, `@daily`, `@weekly`, `@every 90m`. Times are in the server's
local timezone.

## Managing tasks via the API

Every change through the API reschedules the running process immediately — the recommended path
whenever `octo serve` is up.

```bash
# Create — returns {"id":"task_..."}. Any optional field (directory, model,
# agent, notify, session_mode) goes right in the create body.
curl -s -X POST http://127.0.0.1:8088/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"name":"daily-report","cron":"0 0 9 * * *","prompt":"Summarize ...","directory":"/srv/repo"}'

# Fresh-mode example — new session every run.
curl -s -X POST http://127.0.0.1:8088/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"name":"one-shot","cron":"15 10 18 8 *","prompt":"Remind me: ...","session_mode":"fresh"}'

curl -s http://127.0.0.1:8088/api/tasks                # list
curl -s -X DELETE http://127.0.0.1:8088/api/tasks/{id} # delete

# Run now, out of schedule
curl -s -X POST http://127.0.0.1:8088/api/tasks/{id}/run

# Edit any subset of fields — this is also how you enable/disable
curl -s -X PATCH http://127.0.0.1:8088/api/tasks/{id} \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"new prompt ...","enabled":false}'
```

`PATCH /api/tasks/{id}` accepts `enabled`, `cron`, `prompt`, `model`, `agent`, `directory`, `notify`,
`session_mode` — send only what you're changing. The Web UI's scheduler panel is a client of this
same API, so a task created by `curl` shows up there and vice versa; the panel is also the
recommended place to **smoke-test a new task's `Run` button** rather than triggering
`/api/tasks/{id}/run` from a chat session — a run is a full agent turn (up to 30 minutes) in the
task's *own* session, so firing it from a conversation just blocks that conversation while the
actual output lands somewhere nobody is watching it.

### Without a running server

Write `~/.octo/tasks/<id>.json` directly (`id` format `task_<unix-millis>`; filename must equal
`<id>.json`):

```json
{
  "id": "task_1717999999999",
  "name": "daily-report",
  "cron": "0 0 9 * * *",
  "prompt": "Summarize ...",
  "directory": "/srv/repo",
  "session_mode": "shared",
  "enabled": true,
  "created_at": "2026-06-10T09:00:00Z"
}
```

The file is picked up the next time `octo serve` starts. A hand-written file with a bad cron
expression fails silently at load (logged to stderr only). **File edits made while the server is
already running are ignored until restart** — once it's up, go through the API instead.

## Notifications

`notify` is a list of IM targets (a single bare object is also accepted); every entry gets pushed
the run's final reply on success, or a short failure note on error. A failed push is logged on the
server and never affects the run itself.

| Platform | `chat_id` | Notes |
|---|---|---|
| `feishu` | `oc_…` chat id | Needs app creds in `channels.yml`; get the id from chat settings or the server log after messaging the bot |
| `dingtalk` | staff id (1:1) or `cid…` conversation id (group) | A DM's conversation id does not work — use the staff id |
| `weixin` (iLink) | user id | User must have messaged the bot at least once |
| `telegram` | chat id (user/group/channel) | Bot must already be able to message it |
| `discord` | channel id | Bot needs Send Messages permission there |
| `wecom` | ignored | Pushes go through a group-robot webhook bound to one group instead |

Next: for a shorter-lived, in-conversation repeat that doesn't need to survive a restart, see
[`/loop`](/docs/guides/loop/) instead.
