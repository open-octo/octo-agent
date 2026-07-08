# Hooks — run your own commands at points in the agent loop

octo can shell out to your own scripts at fixed points in the agent loop — inject retrieved context, log every tool call, block a dangerous command before it runs, or clean up after a turn. The model, the CLI, `octo serve` (web + IM), and sub-agents all share the same hook engine and the same `hooks.yml`.

`octo hooks` isn't listed in the top-level `octo --help`, but it's a real, working feature — `octo hooks list` shows what's currently configured (built-ins plus anything from `hooks.yml`).

## Config files

| File | Scope |
|------|-------|
| `~/.octo/hooks.yml` | User-global — loaded for every session |
| `<project>/.octo/hooks.yml` | Project-local — layered on top (runs after user hooks) |

A project-level `hooks.yml` can run shell commands on your machine, so it's **trust-on-first-use**: the first time an interactive session (TUI, or a one-shot at a terminal) sees a project file it hasn't approved, it prompts `Trust and run this repo's hooks? [y/N]`. Approving records the file's content fingerprint, so it won't ask again until the file changes. A non-interactive session (piped stdin) declines silently rather than running an untrusted repo's hooks unattended. `octo serve` auto-trusts its own operator-chosen working directory.

## Schema

```yaml
hooks:
  UserPromptSubmit:
    - command: "hindsight-retrieve"
      timeout: 5s
  PostToolUse:
    - matcher: "terminal|write_file"   # regexp over the tool name
      command: "audit-logger"
  Stop:
    - command: "log-session-end"
      async: true
```

Each event maps to a list of hooks (an event can fan out to several commands). Fields per hook:

| Field | Required | Description |
|-------|----------|--------------|
| `command` | yes | Shell command to run |
| `matcher` | no | Regexp over the tool name — only honored for `PreToolUse`/`PostToolUse` |
| `timeout` | no | Go duration string (e.g. `5s`); empty uses the package default |
| `async` | no | Run off the turn's critical path via a durable queue — only honored for the side-effect events (`Stop`/`SubagentStop`/`PreCompact`); rejected as a config error on the events whose output is folded into the model stream |

An unknown event name or an invalid `matcher` regexp is a hard config error naming the offending entry (so a typo surfaces instead of silently doing nothing); a hook that fails at runtime is reported via a notice and just contributes no text — it never crashes the turn.

## Events

Mirrors Claude Code's hook model (a CC hook script ports with just field-name changes) — 7 lifecycle points:

| Event | When | Hook's stdout |
|-------|------|----------------|
| `SessionStart` | Once per logical session opening | Injected into the first user message (persisted) |
| `UserPromptSubmit` | Before each user turn | Folded into that turn's user message |
| `PreToolUse` | Before each tool dispatch | Can **block** the tool (see below) |
| `PostToolUse` | After each successful tool result | Appended to that tool result's text |
| `Stop` | An assistant turn ends (success or failure/interrupt) | Discarded — side-effect only |
| `SubagentStop` | A spawned sub-agent finishes | Discarded — side-effect only |
| `PreCompact` | Before history compaction | Discarded — side-effect only |

Every hook receives a JSON payload on stdin with `event`, `session_id`, `cwd`, `transcript_path`, `model`, `transport`, plus event-specific fields.

## Blocking (`PreToolUse` / `UserPromptSubmit`)

A hook on one of these events can veto the action:

- **exit code 2** → block (reason = a structured `{"decision":"block","reason":"..."}` on stdout if present, else stderr)
- **exit 0 with `{"decision":"block"|"approve","reason":"..."}` on stdout** → that decision
- **timeout or any other exit** → non-blocking error (reported via a notice; the tool/turn proceeds)

The first hook that blocks short-circuits the rest; a later `approve` never overrides an earlier `block`. An `approve` tells octo to bypass the interactive permission prompt for that call — it doesn't otherwise change permission-engine behavior (see `PERMISSIONS.md`).

## Legacy env-var shim

The pre-`hooks.yml` mechanism still works and is layered in first (`hooks.yml` entries run after it):

```
OCTO_HOOK_PRE_TURN=/path/to/pre-script    # optional
OCTO_HOOK_POST_TURN=/path/to/post-script  # optional
OCTO_HOOK_TIMEOUT=5s                      # optional
```

## Built-ins

Two hooks are always registered even with no `hooks.yml` present: a memory reminder (`UserPromptSubmit`) and a save-nudge (`PostToolUse`). `octo hooks list` shows these plus anything you've configured.
