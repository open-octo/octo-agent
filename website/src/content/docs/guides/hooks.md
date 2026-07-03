---
title: Automate with hooks
description: Run your own shell commands at fixed points in the agent lifecycle.
---

Hooks run an external command at a fixed lifecycle point — Claude Code's hook model, ported to
every octo transport (CLI, web, IM) behind one engine.

## The seven events

| Event | Fires | Can it block? |
|---|---|---|
| `SessionStart` | once per logical session opening | stdout folds into context |
| `UserPromptSubmit` | before each user turn | stdout folds into context |
| `PreToolUse` | before each tool dispatch | yes — can allow/block the call |
| `PostToolUse` | after each successful tool result | stdout folds into that tool result's text |
| `Stop` | when an assistant turn ends, success or error | side-effect only |
| `SubagentStop` | when a spawned sub-agent finishes | side-effect only |
| `PreCompact` | before history compaction | side-effect only |

`PreToolUse` is the only gate in the strict sense. The other six are observation/side-effect points;
`SessionStart`, `UserPromptSubmit`, and `PostToolUse` additionally fold their stdout back into the
model's context — the other three (`Stop`, `SubagentStop`, `PreCompact`) discard stdout entirely.

## Where hooks live

Two YAML files, both loaded and both run (not one overriding the other):

- `~/.octo/hooks.yml` — user-level, always loaded.
- `.octo/hooks.yml` — project-level. octo prompts to trust it the first time (a fingerprint check),
  since a project file is something a repo you clone could ship.

```yaml
hooks:
  PreToolUse:
    - matcher: "terminal"                # regexp on tool name; PreToolUse/PostToolUse only
      command: "./scripts/guard.sh"
      timeout: 5s                        # Go duration string; default 5s, capped at 30s

  PostToolUse:
    - matcher: "terminal"
      command: "audit-logger"            # stdout folds into that tool_result's text

  Stop:
    - command: "./scripts/notify-on-commit.sh"
      async: true                        # only valid on Stop / SubagentStop / PreCompact
```

| Field | Required | Notes |
|---|---|---|
| `command` | yes | Runs via the platform shell (`sh -c` / PowerShell); the JSON payload arrives on stdin |
| `matcher` | no | Regexp against the tool name; default matches everything. Ignored (not an error) outside `PreToolUse`/`PostToolUse` |
| `timeout` | no | Duration string; invalid/empty falls back to the 5s default, capped at 30s regardless of what you set |
| `async` | no | `false` by default. Setting it on `SessionStart`/`UserPromptSubmit`/`PostToolUse` — the three events that inject context — is a **hard load-time error**: those must run synchronously to have something to fold in |

An unknown event name is also a hard error at load time — no silently-ignored typos.

:::note[Legacy env-var shim]
`OCTO_HOOK_PRE_TURN` / `OCTO_HOOK_POST_TURN` / `OCTO_HOOK_TIMEOUT` still work, converted
automatically into a `UserPromptSubmit` hook (PRE_TURN) and a `Stop` hook (POST_TURN, forced
async). Prefer `hooks.yml` for anything beyond a single quick command.
:::

## The `PreToolUse` contract

Each `PreToolUse` hook for a matching tool runs in registration order; the first one to block wins:

- **Exit code `2`** → blocked. The reason is stdout's `{"decision":"block","reason":"..."}` if
  present, else the last 500 characters of stderr, else a generic "blocked by PreToolUse hook".
- **Exit code `0`** with stdout parsing as `{"decision":"approve"|"block","reason":"..."}` → that
  decision wins outright — `approve` **skips the normal permission engine entirely** for this call.
- **Exit code `0`**, no parseable decision → no opinion; the normal permission engine still decides.
- **Timeout, or any other non-zero exit** → treated as a non-blocking error; the tool call proceeds
  as if the hook had said nothing.

Only shell hooks run for `PreToolUse` — there's no in-process hook path for this event.

Stdin payload (`PreToolUse` shown; other events carry the same envelope shape with event-specific
fields):

```json
{
  "event": "PreToolUse",
  "session_id": "sess_abc123",
  "cwd": "/repo",
  "transcript_path": "~/.octo/sessions/sess_abc123.json",
  "model": "claude-sonnet-5",
  "transport": "cli",
  "tool_name": "terminal",
  "tool_input": { "command": "rm -rf /" }
}
```

A blocking example — `scripts/guard.sh`:

```bash
#!/bin/sh
payload=$(cat)
cmd=$(echo "$payload" | jq -r '.tool_input.command // empty')
case "$cmd" in
  *"rm -rf"*)
    echo "refusing destructive rm -rf" >&2
    exit 2                 # exit 2 blocks; stderr becomes the reason
    ;;
esac
exit 0                      # no opinion — falls through to the permission engine
```

## How injected output reaches the model

For `SessionStart`, `UserPromptSubmit`, and `PostToolUse`, each hook's stdout is either the
`additional_context` field of a `{"additional_context": "..."}` JSON object, or — if stdout isn't
that shape — the raw text as-is. Multiple hooks on the same event have their outputs joined with a
blank line between them.

## `octo hooks list`

```bash
octo hooks list
```

Prints, in order: any env-var-shim hooks, every user-level hook (event, command, matcher, async) read
straight from `~/.octo/hooks.yml`, every project-level hook plus its trust status
(`trusted` / `UNTRUSTED — run octo in this repo and approve to enable`), and a fixed line naming the
two hooks octo ships internally (a memory reminder on `UserPromptSubmit`, a save-nudge on
`PostToolUse`, both only active when a memory directory exists). If nothing is configured, it says
so plainly rather than printing an empty section.

Next: a common pairing is a `PostToolUse` hook on `terminal` that nudges a memory save after
`git commit` — see [Give it memory](/docs/guides/memory/).
