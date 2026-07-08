# Hooks — run your own commands at points in the agent loop

octo can shell out to your own scripts at 7 lifecycle points (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SubagentStop`, `PreCompact`) — inject retrieved context, log every tool call, or block a dangerous command before it runs. One engine, shared by the CLI, `octo serve` (web + IM), and sub-agents.

Configured in `~/.octo/hooks.yml` (user-level, always loaded) and `<project>/.octo/hooks.yml` (project-level — trust-on-first-use prompt, since it can run shell commands):

```yaml
hooks:
  PreToolUse:
    - matcher: "terminal"        # regexp on tool name; PreToolUse/PostToolUse only
      command: "./scripts/guard.sh"
  PostToolUse:
    - matcher: "terminal"
      command: "audit-logger"
```

`PreToolUse` can block a call: exit code `2` (or `{"decision":"block","reason":"..."}` on stdout) blocks it; `{"decision":"approve",...}` skips the normal permission prompt entirely. `octo hooks list` shows what's currently configured, built-ins included. `octo hooks` isn't in the top-level `octo --help`, but it's real and working.

Full schema (`matcher`/`timeout`/`async` per field, the exact stdin payload shape, the legacy `OCTO_HOOK_PRE_TURN`/`OCTO_HOOK_POST_TURN` env shim, and a worked blocking example): **https://octo-agent.dev/docs/guides/hooks/** (`web_fetch` — not one of the files bundled in this skill directory).
