---
name: config-setup
system: true
description: |
  Configure octo's global settings through guided conversation — set up AI model endpoints
  (providers, API keys, models), adjust agent defaults (reasoning effort, permission mode,
  coauthor, workspace directory), and manage the default/lite model assignments.
  Trigger on: "configure model", "add provider", "setup endpoint", "add API key",
  "change default model", "switch model", "set reasoning", "change permission mode",
  "config setup", "配置模型", "添加模型", "配置 API", "切换模型", "修改设置".
---

# Configure octo

Your job is to turn "I need my agent to use model X" or "change setting Y" into a
working configuration through octo's REST API. Not every user knows where
settings live — briefly explain when needed.

## Two categories of configuration

```
Agent Defaults                    Endpoints & Models
─────────────────                 ───────────────────
reasoning_effort                  provider selection
permission_mode                   API key
show_reasoning                    base URL (advanced)
coauthor                          model names
workspace_dir                     default / lite model
```

All of these live in `~/.octo/config.yml` and are editable through the REST API
on the running octo server at `http://localhost:<port>` (use `curl` via the `terminal` tool — do NOT use `web_fetch`, localhost is blocked by SSRF).

## Reaching the server

The server listens on `127.0.0.1:8088` by default (the desktop app's built-in
server uses the same port). Loopback requests need no access key.

1. **Try the default first**: `curl -s http://127.0.0.1:8088/api/config`.
   JSON back = you're connected; skip the rest of this section.
2. **Connection refused?** The server may be on a custom port:
   - Started as a daemon (`octo serve -d`): `cat ~/.octo/serve.pid` for the PID,
     then find its listen port — macOS/Linux:
     `lsof -iTCP -sTCP:LISTEN -P -n -a -p <PID>`; Windows (PowerShell):
     `Get-NetTCPConnection -State Listen -OwningProcess <PID>`.
   - A foreground `octo serve` writes no pid file — ask the user which port they
     started it on (it's also in the web UI's address bar).
3. **No server running at all?** Don't stop — fall back to editing
   `~/.octo/config.yml` directly (it is the same file every API call below
   mutates). Read the file first, apply the smallest edit that matches the
   structure you see, then validate with `octo doctor`. Changes are picked up
   by new CLI sessions and by the server next time it starts.

---

## Agent Defaults

These are global settings that govern how the Default Agent behaves.
All are individual `PUT` endpoints returning 200 on success, 400 on invalid input.

### Reasoning Effort

```
PUT /api/config/reasoning_effort
{"reasoning_effort": "off"|"low"|"medium"|"high"|"xhigh"|"max"}
```

| Value | Effect |
|-------|--------|
| `off` | No extended thinking (server default) |
| `low` | Fast, surface-level thinking |
| `medium` | Balanced |
| `high` | Deeper reasoning, slower |
| `xhigh` | Very thorough |
| `max` | Maximum depth |

### Permission Mode

```
PUT /api/config/permission_mode
{"permission_mode": "interactive"|"auto"|"strict"}
```

| Value | Effect |
|-------|--------|
| `interactive` | Ask before dangerous operations (default) |
| `auto` | Auto-approve all tools |
| `strict` | Deny all potentially dangerous tools |

### Show Reasoning

```
PUT /api/config/show_reasoning
{"show_reasoning": true|false}
```

When enabled, the agent's thinking process is visible in the chat.

### Coauthor

```
PUT /api/config/coauthor
{"coauthor": true|false}
```

When enabled, appends a `Co-authored-by: octo-agent` trailer to git commits.

### Workspace Directory

```
PUT /api/config/workspace_dir
{"workspace_dir": "/absolute/path"|"auto"}
```

Sets the default working directory for new sessions. Use `"auto"` to let octo
detect the project directory automatically. Only absolute paths are accepted
(no `~` — expand it to the full path first).

### Read Current Defaults

```
GET /api/config
```

Returns the full current config including all defaults. Use this to show the
user their current settings before making changes.

---

## Endpoints & Models

An "endpoint" groups a provider with its API key, then lists the models available
through it. The two-level structure:

```
Endpoint "anthropic"                  Endpoint "relay-a"
  provider: anthropic                   provider: custom
  api_key: sk-ant-…                     api_key: sk-…
  models:                               models:
    - claude-sonnet-5                     - gpt-4o
    - claude-haiku-4-5                    - deepseek-v3
  default_model: claude-sonnet-5        default_model: gpt-4o
  lite_model: claude-haiku-4-5          lite_model: deepseek-v3
```

The **default model** is what the agent uses for normal turns.
The **lite model** is used for lightweight tasks (title generation, quick lookups).

### List Endpoints

```
GET /api/config/endpoints
```

Returns all configured endpoints with their models, including which is default/lite.

### List Available Providers

```
GET /api/providers
```

Returns the built-in provider registry — each entry has a `name`, `base_url`,
and `models` (suggested model names). Use this to help the user pick a provider.

### Create an Endpoint

```
POST /api/config/endpoints
{
  "id": "my-relay",
  "name": "My Relay",
  "provider": "custom",
  "api_key": "sk-…",
  "base_url": "https://api.example.com",
  "protocol": "openai",
  "models": ["gpt-4o"]
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `id` | yes | Unique slug, `[a-zA-Z0-9_-]+`, e.g. `anthropic`, `my-openai` |
| `name` | no | Display name |
| `provider` | yes | Must match a known provider id from GET /api/providers, or `"custom"` |
| `api_key` | no | The API key (never echo it back to the user after saving) |
| `base_url` | no | Override for API base URL; only for `"custom"` provider — named providers use their registry URL |
| `protocol` | no | `"anthropic"` or `"openai"` — only needed for `"custom"` provider |
| `models` | no | Initial model names to register |

### Update an Endpoint

```
PATCH /api/config/endpoints/{id}
{
  "new_id": "new-name",     // rename (optional)
  "name": "New display",    // new display name (optional)
  "provider": "openai",     // change provider (optional)
  "api_key": "sk-…",        // update key (optional)
  "base_url": "https://…",  // change URL (optional)
  "protocol": "openai"      // change protocol (optional)
}
```

All fields are optional — only include what's changing. To rename, set `new_id`.

### Delete an Endpoint

```
DELETE /api/config/endpoints/{id}
```

Returns 204. **Destructive** — deletes the endpoint and all its models.
Confirm with the user before calling.

### Add a Model to an Endpoint

```
POST /api/config/endpoints/{id}/models
{"model": "gpt-4o", "vision": true}
```

`vision` (optional, default false): whether this model supports image input.

### Delete a Model from an Endpoint

```
DELETE /api/config/endpoints/{id}/models/{model}
```

The model name must be URL-encoded when it contains special characters — this
matters for slash-style names like `deepseek/deepseek-chat`, which becomes
`deepseek%2Fdeepseek-chat` in the path.

### Set Default / Lite Model

```
POST /api/config/endpoints/{id}/default?model=gpt-4o
POST /api/config/endpoints/{id}/lite?model=claude-haiku
DELETE /api/config/endpoints/{id}/lite   // clear lite designation
```

The model is passed as a **query parameter** (`?model=…`), not in the request body.
Omit `?model` to use the endpoint's first model as a fallback.

**Important**: This changes the **server-wide default** that seeds new sessions.
It does **not** switch the model of any session that is already running or already
bound to a specific model. For those, see [Switching the Current Session's Model](#switching-the-current-sessions-model).

### Switching the Current Session's Model

A session is bound to the global default model only at creation time. Once it has
a turn, it keeps that model until you explicitly switch it. This applies to
Web, TUI, CLI one-shot, and IM sessions.

- **Web UI**: Click the model chip in the Composer status bar (the row showing the
current model name, e.g. `K3`), then pick the desired model from the dropdown.
- **IM (Weixin/Feishu/DingTalk/WeCom/Discord/Telegram)**: Send `/model` to list
configured models and see the current one, then `/model <endpoint>::<model>` to
bind the session to that endpoint's model, or `/model default` to make it follow
the server-wide default again. You cannot switch while a turn is running.
- **REST API**:

```
PATCH /api/sessions/{id}/model
{"model_id": "Kimi::k3-256k"}
```

Use `default` as the `model_id` to unbind the session from a specific endpoint
and make it follow the global default on subsequent turns. Unknown or bare model
strings are treated as raw model names on the default sender.

**Restart behavior**: You do **not** need to restart `octo serve` after changing
the global default or a session's model. Both take effect on the next turn. The
only exception is a session that was already bound to a model before the change —
it will not switch unless you use one of the methods above.

---

## Workflow

### Configuring Agent Defaults

1. **Read current state** via `GET /api/config`.
2. **Ask what they want to change.** Show current values and let the user pick.
3. **Call the PUT endpoint** for the changed setting.
4. **Confirm the change** — re-read the setting and report back.

Keep it brief: one question at a time, one change per response.

### Adding a new endpoint

1. **Understand the goal.** "I want to use GPT-4o" → OpenAI provider. "I have a
   relay at company.com" → custom provider. If the user names a specific
   provider, skip to step 3.

2. **Check existing endpoints** via `GET /api/config/endpoints`. If an endpoint
   for this provider already exists, ask if they want to add models to it instead.

3. **Collect the API key.** Ask for it once, never echo it back. If the user
   doesn't have one, point them to the provider's website (use the `website_url`
   from `GET /api/providers`).

4. **Pick models.** Named providers have a suggested model list (from
   `GET /api/providers`). For custom providers, ask what model names the relay
   accepts. Suggest sensible defaults — one powerful model for main turns,
   one fast/cheap model for lite tasks.

5. **Confirm and create** via `POST /api/config/endpoints`. Show a summary
   (provider, models, which is default/lite) before calling.

6. **Verify** via `GET /api/config/endpoints` — confirm the new endpoint
   appears with the right models.

7. **Tell the user** the endpoint is ready. New sessions will use it immediately.
   Existing sessions keep their current model binding unless manually switched.

### Editing an existing endpoint

1. **Fetch current state** via `GET /api/config/endpoints`.
2. **Show the user** the endpoint's current config.
3. **Apply the smallest change** via `PATCH /api/config/endpoints/{id}`.
4. **Verify** the change took effect.
5. **Point out** that updating an endpoint (key, URL, provider, protocol) does not
   automatically switch any bound session to a different model; it only changes
   how the already-bound model is reached. If the user wants a running session to
   use a different model, use the per-session switch.

### Setting default / lite model

1. **List endpoints** to show the user what's available.
2. **Confirm** which model should be default (normal turns) and which lite
   (lightweight tasks).
3. **Call** `POST /api/config/endpoints/{id}/default` etc.
4. **Confirm** the assignment.
5. **Clarify the scope**: this only affects **new** sessions and any session that
   is currently unbound. Sessions already in progress or already switched to a
   specific model continue on that model. Tell the user how to switch the current
   session if they expected it to change immediately (Web UI model chip, IM `/model`,
   or `PATCH /api/sessions/{id}/model`).

---

## Rules

- **Never echo API keys back to the user** after they're saved.
- **Always confirm before destructive operations** (delete endpoint, delete model).
- **Show current state before modifying** — never guess the user's setup.
- **One change at a time** — don't batch unrelated config changes.
- **Provider names must match** the registry from `GET /api/providers`. An
  invalid provider returns 400; list the available ones for the user.
- **Model names are free-form** for custom endpoints — don't validate them,
  just pass through what the user gives you.
- **workspace_dir must be absolute** — expand `~` to the full home path before
  calling the API.
