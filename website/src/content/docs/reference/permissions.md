---
title: Permissions
description: The allow/deny/ask rule engine that gates every tool call.
---

Every tool call — in the CLI, the Web UI, or an IM channel — passes through the same permission
engine before it runs. This page is the rule-file reference; see
[The agent loop](/docs/concepts/agent-loop/) for where it sits in the bigger picture.

## `~/.octo/permissions.yml`

Top-level keys are tool names; each holds an ordered list of rules. Each rule is exactly one of
`allow:`, `deny:`, or `ask:`, and each clause is exactly one of `pattern` (a substring match, for
`terminal`), `hostname` (a glob list, for `web_fetch`), or `path` (a glob list with `$CWD`
expansion, for the file tools). **First match wins; no match falls through to `ask`.**

```yaml
terminal:
  - deny:  { pattern: "rm -rf /" }
  - ask:   { pattern: "sudo " }
  - allow: { pattern: "git status" }
  # anything else => implicit ask

web_fetch:
  - deny:  { hostname: ["10.*", "192.168.*", "127.*", "localhost", "*.local"] }
  - allow: { hostname: ["github.com", "*.github.com"] }

write_file:
  - deny:  { path: ["**/.ssh/**", "/etc/**", "**/.env"] }
  - allow: { path: ["$CWD/**"] }
```

A tool key you write **fully replaces** the built-in default rule list for that tool — it doesn't
merge with it. Add back anything from the defaults you still want.

### Matching semantics

- A `terminal` pattern ending in `/` or `~` is boundary-anchored: `deny: {pattern: "rm -rf /"}`
  blocks a root wipe but not `rm -rf /Users/me/project`.
- `terminal` **allow** rules are stricter than `deny`/`ask`: the command must start with the
  pattern (after trimming) *and* contain no shell-chaining metacharacters (`; | & $ ( ) < > `` ` ``
  newline) — so `ls && rm -rf /` can't ride through an `allow: "ls"` rule.
- `hostname` globs match one DNS label per `*` — `*.dev` matches `foo.dev`, not `foo.bar.dev`.
- `path` globs support `**` for any number of path components; `$CWD` expands to the engine's
  working directory at construction time.
- A missing `permissions.yml` isn't an error — the embedded defaults apply on their own.

## `--permission-mode`

Three values, resolved with the same precedence as everything else (flag > `config.yml` >
built-in default):

| Mode | What it does to an `ask` verdict |
|---|---|
| `interactive` (default for the main CLI) | Passes through unchanged — the caller prompts |
| `auto` | Resolved to `allow`, no prompt |
| `strict` | Resolved to `deny` — the posture for evals, the IM bridge, and other unattended runs |

Mode only ever resolves the *implicit or explicit* `ask` case — an explicit `allow` or `deny` from a
matched rule is never overridden by the mode.

:::note
`octo init` defaults its own `--permission-mode` to `strict`, independent of the main CLI's
`interactive` default — it's a one-shot analysis run, not an interactive session.
:::

## Remembering a choice

Answering an interactive prompt with "always" allows that exact `(tool, input)` pair for the rest
of the **session only** — it's never written to `permissions.yml`; durable policy stays a deliberate
file edit. This is available identically on all three transports (a TUI/Web modal's "always" option,
or replying `always` / `always allow` / `总是允许` in a chat channel).

A `deny` rule always beats a remembered allow — the rule scan runs first and only consults the
remembered cache when the rule verdict isn't `deny`. So tightening `permissions.yml` after a user
said "always allow" takes effect on the very next call. Flipping to `strict` mode, on the other
hand, does **not** retroactively revoke something already remembered — mode only governs unanswered
future prompts. The remembered cache itself is dropped whenever the underlying session is: on
session delete in the Web UI, or on `/bind` / `/unbind` / `/new` / `/clear` in an IM channel (see
[Slash commands](/docs/reference/slash-commands/)).

Next: a [`PreToolUse` hook](/docs/guides/hooks/) can add stricter gates on top of these rules — it
can never loosen them, since an explicit `deny` from a rule is final.
