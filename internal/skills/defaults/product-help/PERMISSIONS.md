# Permission System

Every tool call — CLI, web, or IM — passes through the same rule-driven engine before it runs. Rules live in `~/.octo/permissions.yml` (fully replaces the embedded default list per tool it mentions — it doesn't merge) plus the embedded defaults for everything else. First matching rule wins; no match falls through to `ask`.

```yaml
terminal:
  - deny:  { pattern: "rm -rf /" }
  - ask:   { pattern: "sudo " }
  - allow: { pattern: "git status" }

write_file:
  - deny:  { path: ["**/.ssh/**", "**/.env"] }
  - allow: { path: ["$CWD/**"] }

web_fetch:
  - deny:  { hostname: ["10.*", "192.168.*", "127.*", "localhost"] }
  - allow: { hostname: ["github.com", "*.github.com"] }
```

`pattern` (substring, `terminal`), `path` (glob with `$CWD` expansion, file tools), `hostname` (glob, `web_fetch`) — pick the one that matches the tool.

## Modes

| Mode | What an `ask` verdict resolves to |
|------|------|
| **interactive** (default) | Prompt the user |
| **strict** | Auto-deny — default for non-interactive callers (HTTP server, IM bridge) |
| **auto** | Auto-allow — trusted, repetitive workflows only |

Cycle in the TUI with **Shift+Tab**; a web session can also override just its own mode via the composer status bar or `PATCH /api/sessions/{id}/permission_mode` — per-session only, never touches the global default in `~/.octo/config.yml`.

Answering an interactive prompt with "always" remembers that exact `(tool, input)` pair for the rest of the session only — never written to `permissions.yml`.

Full matching semantics (pattern boundary-anchoring, allow-rule strictness against shell-chaining) and how a `PreToolUse` hook composes with this: **https://octo-agent.dev/docs/reference/permissions/** (`web_fetch`).
