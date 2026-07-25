---
title: Expert Agents
description: Create and use specialised agents — each with their own persona, tool set, and skills — for different kinds of work.
---

Expert agents let you partition work across specialised personas. Instead of one
agent that juggles code review, debugging, research, and writing all at once, you
create one agent per role — each with its own system prompt, tool allowlist, and
skill set.

## Quick start

Create a code-review agent through the Web UI or conversation:

```bash
# Via the CLI
octo chat --agent code-review "review the diff"
```

The Web UI also has an **Agents** panel (`/agents`) where you can create, edit,
and delete expert agents — plus assign them to IM channels.

## Creating an agent

### From the Web UI

1. Open the **Agents** panel (sidebar → Agents, or press `Cmd+K` → "Agents").
2. Click **New Agent**.
3. Fill in:
   - **Name** — how it appears in the sidebar and agent picker.
   - **Description** — shown in the agent list. Required.
   - **System Prompt** — the persona. Write what the agent is, what it does, and
     how it should behave. This replaces the Default Agent's identity layers
     (soul.md, user.md).
   - **Model** — leave empty to use the default model, or pick a specific one.
   - **Tools** — check the tools the agent may use. Leave all unchecked to
     grant full tool access (Default Agent behaviour).
   - **Skills** — check the skills the agent may load on demand.
4. Click **Save**.

### From conversation

Ask the Default Agent:

> Create a code-review agent with read-only file access. Give it the
> code-review skill.

The Default Agent uses the `expert-agent-manager` skill to create and configure
the agent through the same underlying API.

## How expert agents differ from the Default Agent

| Aspect | Default Agent | Expert Agent |
|--------|:---:|:---:|
| Persona | `~/.octo/soul.md` + `~/.octo/user.md` | Profile's system prompt (standalone) |
| User rules | `~/.octo/octorules.md` + `.octorules` | ❌ Skipped |
| Tools | Full toolbelt | Per-profile allowlist |
| Skills | All available skills | Per-profile allowlist; system skills hidden |
| MCP servers | All connected | Per-profile allowlist |
| Session pool | Own pool | Own pool, isolated by `<agentID>#` key prefix |
| Management | Creates/edits/deletes expert agents | Cannot modify profiles or install skills |

The key principle: an expert agent's system prompt **stands alone**. It does not
inherit the Default Agent's persona (soul.md), user profile (user.md), or user
conventions (octorules.md). If you want your expert agent to follow those rules,
include them directly in its system prompt.

## Using expert agents

### Web — new session picker

The sidebar's "New Session" button has a caret (▼) on the right side. Click it to
pick which agent handles the new session. Sessions, once created, are permanently
bound to the chosen agent — switch agents by opening a new session.

Each session in the sidebar is tagged with its agent name.

### IM channels

Assign an expert agent to an IM channel through the agent's settings in the
Web UI. Once assigned, all messages from that channel route directly to the
agent — no `/bind` or `@mention` needed.

The Default Agent can also be used in IM: just don't assign a channel to
any expert agent, and messages will route to Default.

### CLI

```bash
# Start an interactive session
octo --agent code-review

# One-shot
octo --agent code-review -p "review the latest commit"

# List available agents
octo --agent
# → agent "code-review" not found (available: default, code-review, ops-helper)
```

### TUI

Use `/agent` inside an interactive session to list or switch agents:

```
/agent                # list available agents
/agent code-review    # switch to code-review (creates a new session)
```

## Per-agent tool and skill filtering

When you create an expert agent, you pick which tools and skills it can use. At
runtime:

- **Tools** are filtered on every turn — an expert agent only sees the tools in
  its allowlist. An empty allowlist means "all tools" (like the Default Agent).
- **Skills** are filtered in the system prompt manifest. System-level skills
  (`skill-creator`, `expert-agent-manager`, `channel-manager`, etc.) are
  automatically hidden from expert agents — they only make sense for the
  Default Agent.
- **Browser-recorded skills** are hidden unless the `browser` tool is in the
  agent's allowlist.

## Per-agent session isolation

Each agent has its own session pool, separated by an `<agentID>#` prefix on the
session key. This means:

- A chat routed to `code-review` has its own history, `/bind`, `/list`, and `/stop`
  — independent of the Default Agent's sessions in the same chat.
- Existing sessions are never migrated; they stay with whichever agent created
  them.
- Old sessions from before the multi-agent upgrade belong to the Default Agent.

## Sub-agents can use expert profiles

The `sub_agent` tool accepts `subagent_type` to dispatch a child agent with a
specific profile's capability set:

```
sub_agent(subagent_type: "code-review", prompt: "review the diff")
```

This runs a fresh, ephemeral sub-agent with the code-review profile's tools and
system prompt — it never enters that agent's session pool.

## Cron tasks and agent ownership

Cron tasks are owned by the agent that created them. In the Web UI's Tasks panel:

- The **Agent** column shows which agent owns each cron task.
- Default Agent users see a **Transfer** action to reassign a task to another agent.
- Filter by `?agent_id=` to see one agent's tasks.

## Best practices

- **One agent, one job**: "code-review", not "dev-ops-review-writer". Narrow
  personas make prompts shorter and results better.
- **Start with a clear system prompt**: two or three sentences about what the
  agent is and isn't. You can always refine it later.
- **Restrict tools**: only check the tools the agent genuinely needs. An agent
  that only reviews code doesn't need `terminal` or `write_file`.
- **Give it exactly one or two skills**: the skill description is the model's
  trigger cue — don't drown the signal.
- **Test with a one-shot**: before assigning an agent to a channel, try `octo
  --agent new-agent -p "test prompt"` to see how it responds.
