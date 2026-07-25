---
name: expert-agent-manager
description: Manage octo's agent profiles through conversation — create, modify, delete, list, and bind agents to IM chats by calling REST APIs. This skill is exclusive to the Default Agent (full-access). Use when the user wants to manage agents conversationally, e.g. "create a code review agent", "list all agents", "bind agent X to this group", "delete agent Y", "帮我建一个 agent", "管理 agent".
---

# Manage Agent Profiles

octo's multi-agent system lets users define **agent profiles** — each with its
own system prompt, model, tool allowlist, and IM chat bindings. Profiles are
stored as Markdown files in `~/.octo/agents/<id>.md` (body = system prompt,
YAML frontmatter = metadata).

This skill manages profiles by calling the REST API on the running octo
server. It is **only available to the Default Agent** — expert agents cannot
create or modify profiles (they can only enable/disable from their own
allowlist).

## API Base

All endpoints below are served by the octo server at `http://localhost:<port>`.
Use the `web_fetch` tool to call them. All requests return JSON.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/agents` | List all profiles (excludes default) |
| `POST` | `/api/agents` | Create a new profile |
| `GET` | `/api/agents/:id` | Get a single profile |
| `PUT` | `/api/agents/:id` | Update a profile |
| `DELETE` | `/api/agents/:id` | Delete a profile |
| `POST` | `/api/agents/:id/bind` | Bind a profile to an IM chat |
| `DELETE` | `/api/agents/:id/bind` | Remove an IM binding |

## Profile Shape

```json
{
  "id": "code-review",
  "name": "Code Reviewer",
  "description": "Reviews code for bugs and style",
  "system_prompt": "You are a thorough code reviewer. Be concise but precise.",
  "model": "claude-sonnet-4-20250514",
  "tools": ["read_file", "grep", "glob"],
  "tool_skills": ["code-review"],
  "mention_as": ["@review"]
}
```

- `id`: filename slug (lowercase, `[a-z0-9-]`); immutable after creation.
- `name`: display name.
- `description`: required; shown in listings.
- `system_prompt`: the agent's system prompt (the Markdown body of `~/.octo/agents/<id>.md`); required for the agent to behave differently from the default agent.
- `model`: optional model override (must be in `~/.octo/config.yml`'s models).
- `tools`: tool allowlist; empty = all tools (default agent behavior).
- `tool_skills`: skills exposed as tools.
- `mention_as`: @-aliases for IM routing (each must start with `@`, globally unique).

## Workflow

### Creating an agent

1. **Gather requirements.** Ask (or infer from context):
   - Name and description
   - System prompt (the .md body)
   - Model (optional — defaults to server default)
   - Tool allowlist (optional — empty = all tools)
   - IM @-aliases (optional)
   - IM channel bindings (optional)

2. **Confirm before creating.** Show the user the profile you're about to
   create and confirm.

3. **Call `POST /api/agents`.** Body is the profile JSON. Include `system_prompt`
   in the JSON body (it becomes the Markdown body of the `.md` file). The server
   derives the `id` from the name (slug). On success (201), echo back the created
   profile.

4. **Verify.** Call `GET /api/agents/:id` to confirm it was written.

### Modifying an agent

1. **Fetch the current profile** via `GET /api/agents/:id`.
2. **Show the user the current state** and ask what to change.
3. **Apply the smallest edit** that satisfies the request.
4. **Call `PUT /api/agents/:id`** with the full updated profile (the API
   replaces the whole object — send all fields, not just changed ones).

Note: `id` and `created_at` are immutable. `channel_bindings` from the
existing profile are preserved unless the user explicitly changes them.

### Deleting an agent

1. **Confirm with the user** — deletion is destructive and cannot be undone.
2. **Call `DELETE /api/agents/:id`.** Returns 204 on success.
3. **Error cases:** the profile has active channel bindings (409 — unbind
   first), or it's a builtin profile (403/409 — cannot delete defaults).

### Binding to an IM chat

1. **Collect platform + chat ID** (e.g. `weixin` + group ID).
2. **Call `POST /api/agents/:id/bind`** with `{"platform": "...", "chat_id": "..."}`.
3. **Verify** with `GET /api/agents/:id`.

### Listing agents

1. **Call `GET /api/agents`.** Returns all profiles (excludes default).
2. **Present the list** to the user in a readable format.

## Rules

- **Always confirm before destructive operations** (delete, overwrite).
- **Show the user the current state** before modifying — don't guess.
- **Mention aliases must be globally unique** — creating a profile with a
   duplicate `@alias` returns 409. Check existing profiles first.
- **Model must exist in config** — the server validates against
  `~/.octo/config.yml`'s `models` list. An invalid model returns 400.
- **This skill only works on the Default Agent** — if you detect you're running
  as an expert agent (narrow tool access, specific system prompt), refuse and
  tell the user to switch to the Default Agent.

## Troubleshooting

- **409 "already exists"** — the ID (derived from name) is taken. Ask for a
  different name or use the existing profile.
- **400 "model not found"** — the model isn't in config. List available models
  via `GET /api/config/endpoints` and ask the user to pick one.
- **409 "alias already claimed"** — another profile uses the same `@alias`.
  List existing profiles and suggest an alternative.
- **409 "channel binding exists"** — the chat is already bound to this or
  another profile. Check existing bindings first.
