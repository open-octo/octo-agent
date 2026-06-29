---
name: cron-task-creator
description: Create, inspect, run, edit, enable/disable, and delete octo's scheduled cron tasks — recurring agent prompts stored in ~/.octo/tasks/*.json and executed by the octo serve scheduler. Use when the user wants to schedule a recurring task, e.g. "run X every morning", "schedule a daily report", "set up a cron job", "定时任务", "每天自动跑".
---

# Create and manage octo cron tasks

octo runs an agent prompt on a schedule. Each task is a JSON file in
`~/.octo/tasks/`, loaded by the scheduler inside `octo serve`. When a task
fires, the scheduler runs one agent turn with the task's prompt and **reuses the
same session across runs**, so the task accumulates history from earlier runs.
Each run is bounded by a **30-minute wall-clock timeout** (the only hard cap on
a run).

## Task schema

| Field | Required | Meaning |
|-------|----------|---------|
| `name` | yes | Human-readable task name |
| `cron` | yes | Schedule expression — see format below |
| `prompt` | yes | The prompt sent to the agent on each run |
| `model` | no | Model override; defaults to the server's model |
| `agent` | no | `"general"` or `"coding"` |
| `directory` | no | Working directory the run executes in |
| `notify` | no | IM chats to push each run's final reply (or failure) to — see the notify table |
| `enabled` | yes | Whether the schedule is active |

`id`, `created_at`, `last_run`, `session_id` are server-managed — never set them
by hand except `id` in the file-write fallback below.

## Cron expression — 6 fields, seconds first

The scheduler uses robfig/cron **with a seconds field**. A standard 5-field
crontab line is **invalid** — always prepend a seconds field:

```
seconds minutes hours day-of-month month day-of-week
```

| Want | Expression |
|------|------------|
| Every day at 09:00 | `0 0 9 * * *` |
| Every 30 minutes | `0 */30 * * * *` |
| Weekdays at 18:30 | `0 30 18 * * 1-5` |
| 1st of each month at 08:00 | `0 0 8 1 * *` |

Descriptors also work: `@hourly`, `@daily`, `@weekly`, `@every 90m`. Times are
in the server's local timezone.

## Workflow

1. **Gather** the schedule, the prompt, and any optional fields. If the schedule
   is vague ("every morning"), pick a concrete time and confirm.
2. **Translate** to a 6-field expression and **echo it back in plain words**
   ("every weekday at 18:30") before creating anything.
3. **Write a self-contained prompt.** The task session has no access to this
   conversation — the prompt must carry all context: what to do, where, and what
   the output should look like.
4. **Give the prompt an explicit stop condition.** An open-ended prompt makes the
   model keep re-verifying until the 30-minute timeout instead of finishing.
   Spell out when the task is done, especially the empty case:
   - Bad: "Check the repository for any new open issues that need attention."
   - Good: "List open issues created in the last 24h via one `gh issue list`
     call. If there are none, reply exactly 'no new issues' and stop. Otherwise
     summarize each in one line and stop — do not re-check."
5. **Create**, then **verify** by listing. Offer a one-off immediate run to test.

## API — one surface, all under `/api/tasks`

Prefer the API whenever `octo serve` is up (default `:8080`): every change
reschedules the running process immediately.

```bash
# Create — returns {"id":"task_..."}. Include any optional field (directory,
# model, agent, notify) right here.
curl -s -X POST http://127.0.0.1:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"name":"daily-report","cron":"0 0 9 * * *","prompt":"Summarize ...","directory":"/srv/repo"}'

curl -s http://127.0.0.1:8080/api/tasks                      # list
curl -s -X POST   http://127.0.0.1:8080/api/tasks/{id}/run   # run now
curl -s -X DELETE http://127.0.0.1:8080/api/tasks/{id}       # delete

# Edit any subset of fields — this is also how you enable/disable.
curl -s -X PATCH http://127.0.0.1:8080/api/tasks/{id} \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"new prompt ...","enabled":false}'
```

`PATCH /api/tasks/{id}` accepts `enabled`, `cron`, `prompt`, `model`, `agent`,
`directory`, `notify` — send only the fields you want to change. Look up `{id}`
from the create response or the list. (Earlier builds had a separate
`/api/cron-tasks/...` route and a `/toggle` endpoint; both are gone — everything
is `/api/tasks` now.)

### Fallback — direct file write (server not running)

Write `~/.octo/tasks/<id>.json` with `write_file` (`id` format
`task_<unix-millis>`; filename must equal `<id>.json`):

```json
{
  "id": "task_1717999999999",
  "name": "daily-report",
  "cron": "0 0 9 * * *",
  "prompt": "Summarize ...",
  "directory": "/srv/repo",
  "enabled": true,
  "created_at": "2026-06-10T09:00:00Z"
}
```

The file is picked up the next time `octo serve` starts. A hand-written file
with a bad cron expression fails silently at load (logged to stderr only) —
double-check the 6-field format. **File edits to an already-running server are
ignored until restart** — when the server is up, always go through the API.

## Caveats — mention when relevant

- **Tasks only fire while `octo serve` is running.** No serve → no runs. Missed
  schedules are not replayed on restart.
- **API changes take effect immediately; hand-edited files don't** (until the
  next serve start).
- **A failed IM push is logged on the server and never affects the run.**

## notify — per-platform `chat_id`

`notify` is a list (a single bare object is also accepted); every entry is
pushed: `[{"platform":"feishu","chat_id":"oc_..."}, ...]`.

| Platform | `chat_id` | Notes |
|----------|-----------|-------|
| `feishu` | `oc_…` chat id | Works with app creds in `~/.octo/channels.yml`; get the id from chat settings or the server log after messaging the bot. |
| `dingtalk` | staff id (1:1) or `cid…` openConversationId (group) | A DM's conversation id does NOT work — use the staff id. Needs "robot message send" permission. |
| `weixin` | iLink user id | User must have messaged the bot once (refreshes the `context_token` the push reads); a long-stale token may be rejected. |
| `telegram` | Telegram chat id (user/group/channel) | Bot must be able to message it (user started it, or bot is a member). |
| `discord` | channel id | Bot needs Send Messages permission in that channel. |
| `wecom` | (ignored) | Pushes go through a group-robot webhook (`webhook_key`/`webhook_url` in channel config); bound to one group, so `chat_id` is just a label. |
