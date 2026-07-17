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
expansion, for the file tools). Matches are resolved by **tier, not declaration order — `deny` beats
`ask` beats `allow`** — and the first match within the winning tier is reported as the reason. No
match in any tier falls through to `ask`.

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
  - deny: { path: ["**/.ssh/**", "/etc/**", "**/.env"] }
  # no allow rule for $CWD/** here — see the note below
```

Being inside `$CWD` is **not** a free pass for `write_file`/`edit_file` — only `read_file` treats
the whole filesystem (minus the credential-path denies) as safe to read without asking. A write or
edit anywhere, cwd included, falls through to the implicit `ask` unless a rule says otherwise, so
`--permission-mode` below is what actually decides whether it prompts, auto-allows, or denies.

A tool key you write **fully replaces** the built-in default rule list for that tool — it doesn't
merge with it. Add back anything from the defaults you still want. One exception survives any
replacement: a small set of hardcoded catastrophe denies (`rm -rf /usr`, `dd` onto a device,
`mkfs`, `shutdown`, and the like) is appended after your rules and wins through the deny tier —
an `allow` in your file cannot override them.

### Matching semantics

- A `terminal` pattern starting with `^` is **command-position anchored**: it only matches where a
  command word can appear — at the start, after a chain operator (`;`, `&&`, `|`), or after
  transparent prefixes like `sudo`, `env`, or `VAR=…`. Use it to avoid substring false positives:
  a bare `deny: {pattern: "format"}` also blocks `docker ps --format json`, and `"shutdown"`
  blocks `git commit -m "fix shutdown handling"` — `^format` / `^shutdown` block only the command
  itself, never an argument or quoted text.
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
`interactive` default — it's a one-shot analysis run, not an interactive session. It still writes
`.octorules` without prompting under `strict`, but not because strict mode allows cwd writes: `octo
init` passes its own working directory as an explicit write-allowed root (the same mechanism the
memory directory uses), independent of `permissions.yml` and unaffected by mode.
:::

:::note
Cron task sessions are the other place a write needs to happen with nobody present to answer an
`ask`. A newly created task session defaults to `auto` rather than the global `interactive`
default — an explicit `permission_mode` in `config.yml` (`interactive`, `strict`, or `auto`) is
still honored as-is; only the unconfigured case differs from a web/CLI/IM session's default.
:::

## Remembering a choice

Answering an interactive prompt with "always" allows that exact `(tool, input)` pair for the rest
of the **session only** — it's never written to `permissions.yml`; durable policy stays a deliberate
file edit. This is available identically on all three transports (a TUI/Web modal's "always" option,
or replying `always` / `always allow` / `总是允许` in a chat channel).

`write_file`/`edit_file` are the one exception to "exact `(tool, input)`": their input also carries
the new content, which differs on every call, so remembering the whole input would never hit the
cache a second time. Those two tools remember by path alone — approve one edit and the rest of the
session doesn't ask again for *that file*, but a different file still prompts once.

A `deny` rule always beats a remembered allow — the rule scan runs first and only consults the
remembered cache when the rule verdict isn't `deny`. So tightening `permissions.yml` after a user
said "always allow" takes effect on the very next call. Flipping to `strict` mode, on the other
hand, does **not** retroactively revoke something already remembered — mode only governs unanswered
future prompts. The remembered cache itself is dropped whenever the underlying session is: on
session delete in the Web UI, or on `/bind` / `/unbind` / `/new` / `/clear` in an IM channel (see
[Slash commands](/docs/reference/slash-commands/)).

Next: a [`PreToolUse` hook](/docs/guides/hooks/) can add stricter gates on top of these rules — it
can never loosen them, since an explicit `deny` from a rule is final.
