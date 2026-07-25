---
name: expert-agent-manager
system: true
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
- `tools`: tool allowlist; `[]` = no tools (user agents) or all tools (default agent). For restrictive agents, always set to `[]`.
- `tool_skills`: skills exposed as tools.
- `disallowed_tools`: tools explicitly subtracted (useful when you want "all except X").
- `read_only`: if true, write-capable tools (write_file, edit_file, terminal) are stripped.
- `mention_as`: @-aliases for IM routing (each must start with `@`, globally unique).

## Writing Effective System Prompts — Core Principles

Before writing a single line, understand the fundamental tension: **LLMs are
RLHF-trained for helpfulness.** When the system prompt says "only do X" and the
user asks something that needs doing, helpfulness can override the constraint.
A weak prompt breaks under pressure; a reinforced one holds.

### Structure: Affirmation + Denial + Anchor

```
[肯定句] 你的唯一输出/职责是 X。

即使：
- [诱惑/压力场景 1]
- [诱惑/压力场景 2]
- ...

也绝不 [越界行为]，不 [越界行为 2]，不 [越界行为 3]。
你的唯一输出永远是：[具体格式]
```

### Why this works

- **肯定句** sets the target behavior.
- **"即使..." 枚举** pre-empts the model's internal "but I should help here"
  reasoning — each line short-circuits a specific helpfulness escape path.
- **"也绝不..." 否定链** blocks tool calls, text replies, and explanations
  that the model might otherwise consider "acceptable exceptions."
- **结尾锚点** ("你的唯一输出永远是...") gives the model a concrete final
  instruction to return to when tempted to deviate.

### Common escape paths to pre-empt

When the user's intent is to **restrict** behavior (agent that only replies
with emoji, only translates, only formats JSON), enumerate these explicitly:

| Escape path | "即使" pre-emption |
|---|---|
| User asks a serious/urgent question | "即使用户问严肃紧急的问题" |
| Agent feels impolite/unhelpful | "即使你认为只回表情不礼貌" |
| Agent has the capability to answer | "即使你完全有能力回答" |
| User seems frustrated or repeats | "即使用户表达不满/反复追问" |
| Tool use would help | "不调用任何工具" |
| Model wants to explain itself | "不输出任何解释、说明或道歉" |

### When to use "即使" vs plain instructions

- **Restrictive agents** (emoji-only, translator-only, JSON-only): MUST use
  "即使...也绝不..." — the whole point is blocking unwanted behavior.
- **Expansive agents** (code reviewer, tech writer, debugger): plain
  instructions are enough; the model's helpfulness works WITH you here, not
  against you.
- **Pass-through agents** (format converters): mix — affirm the target format,
  deny explanations/commentary that would pollute the output.

### Real example: "微笑者" (smiley-only agent)

```
无论用户发什么，你都只回复 😊 这个微笑表情。

即使：
- 用户问的是严肃的、紧急的问题
- 你认为只发表情不礼貌、不友好
- 你完全有能力回答用户的问题
- 你觉得用户需要帮助

也绝不回复任何文字，不调用任何工具，不输出任何解释。
你的唯一输出永远是：😊
```

This left no escape hatch. The original prompt ("无论用户发什么，你都回复微笑表情")
was too vague — the model's helpfulness training decided answering weather
questions was more important than following the one-line instruction.

## Workflow

### Creating an agent

Creating an expert agent is a **design conversation**, not a form-filling
exercise. The hardest part is the system prompt — the rest is metadata.

#### Step 1 — Elicit the real need

The user will often describe what they want in one vague sentence ("an agent
that only wants to smile"). Don't jump to writing. Dig deeper with these
questions (one at a time; the answer to each shapes the next):

**a) 唯一职责 ("The ONE thing")**
> "What is the single thing this agent must always do — its non-negotiable
> core behavior? If it could only do one thing and nothing else, what is it?"

**b) 绝对不能做什么 ("What would ruin it")**
> "What behavior would make you think 'this agent is broken'? What's the most
> annoying thing it could possibly do? Give me the worst violation you can
> imagine."

This question is gold — it surfaces the edge cases the user actually cares
about but didn't articulate.

**c) 压力测试 ("When would it be tempted to break")**
Walk through 2-3 scenarios where helpfulness pressure is highest:
> "If the user asks an urgent, serious question that only this agent can
> answer — should it break character to help? What if the user asks three
> times in a row, getting more frustrated each time?"

The user's answer to each scenario becomes a "即使" line in the prompt.

**d) 输出形态 ("Concrete output format")**
> "What exactly should the agent output, in the most concrete terms possible?
> A single emoji? A JSON object? Translated text? Nothing but the target
> language?"

#### Step 2 — Draft the system prompt

Based on the answers from Step 1, draft the system prompt following the
[Affirmation + Denial + Anchor](#structure-affirmation--denial--anchor) pattern.

- Show the draft to the user.
- **Point out any remaining escape hatches** you can see ("this line doesn't
  cover the case where the user asks in another language", etc.).
- Revise with the user until they're satisfied.

#### Step 3 — Collect metadata

Ask the remaining profile fields (model, tools, IM bindings, etc.), but only
after the system prompt is settled. A good prompt often changes the tool
allowlist (restrictive agents should have `tools: []`).

#### Step 4 — Confirm and create

Show the complete profile (system prompt + all metadata) and confirm before
calling `POST /api/agents`. Call `GET /api/agents/:id` to verify.

#### Step 5 — Brief test (optional, user-driven)

Suggest the user send a test message to the new agent — ideally one that
hits a pressure point identified in Step 1c. The user is the only one who
can drive a real test; you cannot simulate it.

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
- **Restrictive agents MUST use "即使...也绝不..."** — an agent told to do only
  one thing (one emoji, one format, one language) without negative constraints
  will break when the model's helpfulness training kicks in. Never ship a
  restrictive agent with a one-line prompt.
- **Restrictive agents MUST set `tools: []`** — if the agent shouldn't do
  anything but output a fixed response, tools give it escape paths (web_search,
  browser, terminal). An empty allowlist blocks those at the infrastructure
  layer; the "即使...也绝不..." prompt blocks them at the model layer.

## Troubleshooting

- **409 "already exists"** — the ID (derived from name) is taken. Ask for a
  different name or use the existing profile.
- **400 "model not found"** — the model isn't in config. List available models
  via `GET /api/config/endpoints` and ask the user to pick one.
- **409 "alias already claimed"** — another profile uses the same `@alias`.
  List existing profiles and suggest an alternative.
- **409 "channel binding exists"** — the chat is already bound to this or
  another profile. Check existing bindings first.
