---
name: expert-agent-manager
system: true
description: Manage octo's agent profiles through conversation — create, modify, delete, list, and bind agents to IM chats by calling REST APIs. This skill is exclusive to the Default Agent (full-access). Use when the user wants to manage agents conversationally, e.g. "create a code review agent", "list all agents", "bind agent X to this group", "delete agent Y", "创建一个专家", "帮我建个专家", "管理专家", "编辑专家", "删除专家", "专家列表", "把专家 X 绑定到这个群".
---

# Manage Agent Profiles

## Terminology

When conversing in Chinese, refer to this concept as **专家** (not 智能体, and
not a bare "agent") — this matches the desktop/web UI's Chinese labels
(创建专家 / 编辑专家 / 删除专家). English conversations keep "agent". This is
a conversational naming convention only — API fields, IDs, and code
identifiers (`agent_id`, `/api/agents`, etc.) are unaffected.

octo's multi-agent system lets users define **agent profiles** — each with its
own system prompt, model, tool allowlist, and IM chat bindings. User-created
profiles are stored as Markdown files in `~/.octo/agents/<id>.md` (body =
system prompt, YAML frontmatter = metadata).

This skill manages profiles by calling the REST API on the running octo
server. It is **only available to the Default Agent** — expert agents cannot
create or modify profiles (they can only enable/disable from their own
allowlist).

### Curated vs. user-created experts

`GET`/`POST`/`PUT`/`DELETE` on `/api/agents` see two kinds of profile,
distinguished by a `source` field on every response:

- **`"source": "user"`** — created via this skill or the Web UI form, stored
  in `~/.octo/agents/`. Freely editable and deletable.
- **`"source": "default"`** — an **officially curated expert** shipped in the
  binary (the ones shown in the Web UI's expert gallery, e.g. copywriter,
  resume-coach, trip-planner). These are **read-only**: `PUT` and `DELETE` on
  one both refuse. They can only be **hidden**. An official expert is
  identical on every machine and keeps receiving content updates when octo
  ships a new curated-persona revision — editing one used to fork it into a
  `~/.octo/agents/<id>.md` override and silently forfeit those updates, which
  is why it no longer does.

  **When the user asks to change a curated expert**, don't try to edit it:
  create a NEW user agent (a different id) with the persona they want, and
  say plainly that the official one stays as shipped and can be hidden if
  they'd rather not see both. A user file at a curated expert's id is ignored
  on load, so writing one by hand achieves nothing either.

  A curated expert can be **hidden** from the gallery by the user (or by this
  skill). While hidden, `GET /api/agents/:id` (and `PUT`/`DELETE` on that id)
  return **404 — indistinguishable from the profile not existing at all.**
  Only the list endpoint, `GET /api/agents`, still shows it, tagged with
  `"enabled": false`. **If a lookup or edit 404s and you don't recognize the
  id as one you or the user created, don't assume it was deleted** — call
  `GET /api/agents` and check for that id with `enabled: false` before telling
  the user it doesn't exist. See "Modifying an agent" below for the full
  recovery flow.

## API Base

All endpoints below are served by the octo server at `http://localhost:<port>`.
Use `curl` via the `terminal` tool to call them. (Do NOT use `web_fetch` —
localhost is blocked by SSRF protection.) All requests return JSON.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/agents` | List all profiles (excludes the default agent; includes hidden curated experts, tagged `enabled: false`) |
| `POST` | `/api/agents` | Create a new user profile (refuses an id taken by a curated expert) |
| `GET` | `/api/agents/:id` | Get a single profile (404 if hidden — see "Curated vs. user-created experts" above) |
| `PUT` | `/api/agents/:id` | Update a **user** profile (refuses a curated one — create your own instead) |
| `DELETE` | `/api/agents/:id` | Delete a user profile (409 for a curated one — hide it instead, see toggle below) |
| `PATCH` | `/api/agents/:id/toggle` | Hide/show a **curated** expert; returns `{"id", "enabled"}` (400 on a user profile — those aren't hideable, just delete them) |
| `POST` | `/api/agents/:id/bind` | Bind a profile to an IM chat |
| `DELETE` | `/api/agents/:id/bind` | Remove an IM binding |

## No octo server reachable (TUI-only sessions)

**The API above only exists while `octo serve` (or the desktop app, which is
also a serve process) is running as its own process.** A bare `octo` TUI/CLI
session — the one this skill usually runs inside — does **not** open any HTTP
listener itself; it's purely in-process. If nothing else on the machine
happens to be running `octo serve`, every endpoint above is unreachable —
`curl` will just fail to connect.

**Check first, before assuming curl will work:**
```
curl -s --max-time 1 http://localhost:8088/api/version
```
(8088 is the default `octo serve` port; if the user has a custom `--addr`,
try that instead.) No response within ~1s → no server is running. Don't keep
retrying or guessing other ports — fall back to the file-based approach below
for anything it covers, and tell the user plainly when something (see "Hiding
a curated expert" below) genuinely requires a running server.

### Creating/editing a user agent by hand-writing its file

`~/.octo/agents/<id>.md` is the exact on-disk form of a user profile — the
Store is read-through (any path that touches this directory takes effect on
the very next read, no restart, no reload call). Writing this file directly
with the `write_file` tool is a fully supported, first-class way to manage
profiles when there's no server to call — but you take over every validation
the API normally does for you:

1. **Pick an id** matching `^[a-z0-9][a-z0-9-]{0,31}$` (lowercase, digits,
   hyphens, 1-32 chars, starts alphanumeric) and not `default`/`explore`/
   `general`/`code-review` (the four reserved builtin ids).
2. **Check for a collision yourself** — `ls ~/.octo/agents/` and
   `ls ~/.octo/agents-default/`. The API refuses to silently overwrite an
   existing profile (409); a raw `write_file` has no such guard and will just
   clobber whatever's already at that path.
3. **Write the file**:
   ```markdown
   ---
   name: Code Reviewer
   description: Reviews code for bugs and style
   model: claude-sonnet-4-20250514
   tools: [read_file, grep, glob]
   tool_skills: [code-review]
   ---
   You are a thorough code reviewer. Be concise but precise.
   ```
   `description` is the only field the Store itself enforces as non-empty on
   read — everything else is on you to get right (see the checklist below).
4. **Validate the rest yourself** — the API layer normally catches all of
   this; a raw file skips every check:
   - `name` ≤ 32 characters, if set.
   - The Markdown body (the system prompt) ≤ 10,000 characters.
   - Every entry in `tools`/`tool_skills` is a real tool/skill name — an unknown
     name isn't rejected, it's just silently dropped from the agent's actual
     allowlist at runtime. Cross-check spelling against what you know is
     available (skills you've seen listed, the standard tool names) since
     there's no listing endpoint to call here either.
   - `model`, if set, should be a model you've confirmed the user has
     configured — nothing validates this at all, on the API path either;
     an unresolvable model just fails at the next turn, not at save time.

**You cannot edit a curated (`source: "default"`) expert this way either.** A
user file whose id matches one under `~/.octo/agents-default/` is ignored on
load, so writing one leaves a file that never takes effect. Give the new
persona its own id instead (check `ls ~/.octo/agents-default/` for the ids
that are taken).

**Deleting a user agent created this way**: `rm ~/.octo/agents/<id>.md` after
confirming with the user (same destructive-operation rule as always).

### Hiding a curated expert without a server

There's no file-based equivalent for this one — the hidden/shown state lives
in `~/.octo/config.yml`'s `agents.disabled_defaults` list, and that file also
holds endpoint credentials, so freehand edits there carry real risk. If asked
to hide/show a curated expert and no server is reachable:

1. Prefer telling the user to start `octo serve` (or open the desktop app)
   and use the gallery card's hide/show action, or ask you again once it's
   running — that's the safe, validated path.
2. Only edit `config.yml` directly if the user insists: read the whole file
   first, add or remove exactly one string under `agents:` → `disabled_defaults:`
   (creating those two keys, appended at the end of the file, if they don't
   exist yet), and change **nothing else** — show the user the exact diff
   before saving. Never rewrite the file wholesale; a full rewrite risks
   losing or corrupting the `endpoints:` block's API keys.

## Profile Shape

```json
{
  "id": "code-review",
  "name": "Code Reviewer",
  "description": "Reviews code for bugs and style",
  "system_prompt": "You are a thorough code reviewer. Be concise but precise.",
  "model": "claude-sonnet-4-20250514",
  "tools": ["read_file", "grep", "glob"],
  "tool_skills": ["code-review"]
}
```

- `id`: filename slug (lowercase, `[a-z0-9-]`); immutable after creation.
- `name`: display name.
- `description`: required; shown in listings.
- `system_prompt`: the agent's system prompt (the Markdown body of `~/.octo/agents/<id>.md`); required for the agent to behave differently from the default agent.
- `model`: optional model override (must be in `~/.octo/config.yml`'s models).
- `tools`: tool allowlist; `[]` = no tools. User-created agents with empty `tools` get nothing (unlike the default agent which gets all tools with empty allowlist).
- `tool_skills`: skills exposed as tools.
- `source` (`"user"` or `"default"`) and `enabled` (bool): **response-only** —
  present on every `GET`, never sent in a `POST`/`PUT` body. See "Curated vs.
  user-created experts" above.

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
   - **If this 404s, don't tell the user the agent doesn't exist yet.** Call
     `GET /api/agents` (the list) and look for that id. If it's there with
     `"enabled": false`, it's a hidden curated expert — tell the user it's
     currently hidden, and (with their OK) call
     `PATCH /api/agents/:id/toggle` to re-enable it (this call itself never
     404s on a hidden id — it's the one endpoint that can still find it), then
     retry step 1. Only report "no such agent" if it's absent from the list
     entirely.
2. **Show the user the current state** and ask what to change.
3. **Apply the smallest edit** that satisfies the request.
4. **Call `PUT /api/agents/:id`** with the full updated profile (the API
   replaces the whole object — send all fields, not just changed ones). If
   the profile's `source` is `"default"`, mention to the user that this edit
   forks it into a personal copy that will no longer receive octo's future
   updates to that curated persona's content.

Note: `id` is immutable. `channel_bindings` from the
existing profile are preserved unless the user explicitly changes them.
`source` and `enabled` are response-only — never send them in the request body.

### Deleting an agent

1. **Confirm with the user** — deletion is destructive and cannot be undone.
2. **If the user wants to get rid of a curated (`source: "default"`) expert,
   don't delete it — hide it instead** via `PATCH /api/agents/:id/toggle`.
   Deletion is permanent and only applies to user-created profiles; curated
   ones can always be re-shown later, which a delete could never undo.
3. **Call `DELETE /api/agents/:id`.** Returns 200 with `{"deleted": id}` on
   success.
4. **Error cases:** the profile has active channel bindings (409 — unbind
   first), or it's a builtin/curated profile (409 — cannot delete; hide the
   curated ones instead, see step 2).

### Binding to an IM chat

1. **Collect platform + chat ID.** The format depends on the platform:
   - **WeChat (weixin):** only private chats are supported. The `chat_id` is the
     user's private chat ID (e.g. `o9cq8...@im.wechat`). Group chats are not
     available on this platform.
   - **Other platforms** (Telegram, Discord, Feishu, DingTalk, WeCom): both
     private and group chat IDs are supported.
   When the user says "bind to a WeChat group", correct them — it isn't possible.
2. **Call `POST /api/agents/:id/bind`** with `{"platform": "...", "chat_id": "..."}`.
3. **Verify** with `GET /api/agents/:id`.

### Listing agents

1. **Call `GET /api/agents`.** Returns all profiles (excludes the default
   agent) — user-created and curated, including curated experts currently
   hidden from the gallery (tagged `"enabled": false`).
2. **Present the list** to the user in a readable format — note each one's
   `source` (curated vs. their own) and, for curated ones, whether it's
   currently hidden (`enabled: false`).

## Rules

- **Always confirm before destructive operations** (delete, overwrite).
- **Show the user the current state** before modifying — don't guess.
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
- **404 on `GET`/`PUT`/bind for an id you didn't just delete yourself** — don't
  assume it's gone. Check `GET /api/agents` for that id with
  `"enabled": false`; if found, it's a hidden curated expert — see "Modifying
  an agent" above for the re-enable-then-retry flow.
- **409 on `DELETE`** — either it's a builtin/curated profile (hide the
  curated ones instead via toggle, see "Deleting an agent") or it still has
  active channel bindings (unbind first).
- **400 on `PATCH .../toggle`** — the id belongs to a user-created profile,
  not a curated one; toggling only applies to curated experts. Delete it
  instead if the user wants it gone.
- **Duplicate bindings** — binding the same chat twice returns 200 (idempotent);
  no error to handle.
