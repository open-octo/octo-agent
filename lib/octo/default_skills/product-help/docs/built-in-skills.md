# Built-in Skills

Octo ships with the following default skills:

| Skill | Description |
|---|---|
| **browser-setup** | Configure Chrome/Edge for browser automation |
| **channel-manager** | Set up IM platform integrations (Feishu, WeCom, WeChat, Discord, Telegram) |
| **cron-task-creator** | Create and manage scheduled automated tasks |
| **onboard** | New user onboarding guide |
| **personal-website** | Generate and publish a personal homepage |
| **product-help** | Help with Octo features, configuration, and usage |
| **skill-add** | Install skills from zip URLs or local files |
| **skill-creator** | Create, modify, and optimize skills |

## Built-in Sub-agent Presets

Some former skills are now **sub-agent presets** under
`lib/octo/default_subagents/`. They're invoked by the LLM via the `agent`
tool with `subagent_type:`, not via a slash command.

| Preset | When the agent uses it |
|---|---|
| **explore** | Read-only research; no network, no mutation |
| **plan** | Pure design / planning; no execution side effects |
| **verification** | Read + run tests; checks work without mutating code |
| **general-purpose** | Broad capability sub-agent (only `agent` recursion blocked) |
| **code-explorer** | Workflow-driven codebase exploration (list → README → grep → read → report) |
| **persist-memory** | Write to long-term memory at `~/.octo/memories/` |
| **recall-memory** | Read relevant memories and summarize them back |

## Location

Built-in skills are stored in:

```
lib/octo/default_skills/
```

Built-in sub-agent presets:

```
lib/octo/default_subagents/
```

## Customization

You can override built-in skills by placing a skill with the same name in:

- `.octo/skills/` (project-level)
- `~/.octo/skills/` (user-level)

User-level skills take precedence over built-in skills.

Sub-agent presets can be overridden by placing a directory with the same name
in `~/.octo/subagents/`.

## Removed Skills

The following skills were previously built-in but removed due to being too opinionated:

- **deploy** — Coupled to Railway + Rails
- **new** — Assumed specific Rails scaffolding

These can still be installed as custom skills via `/skill-add`.
