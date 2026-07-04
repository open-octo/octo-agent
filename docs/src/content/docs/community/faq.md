---
title: FAQ & troubleshooting
description: Common questions.
---

### Does any of my data leave my machine?

Only what you explicitly send to your chosen model provider (Anthropic, OpenAI, or whatever
endpoint you configured) as part of a conversation. Sessions, memory, and config all live under
`~/.octo/` on the machine running `octo` — there's no octo-operated backend in between.

### Is there a hosted version?

No — self-hosting is the point. `octo serve` is the same binary you already have; see
[Self-host octo serve](/docs/guides/self-host/) for exposing it beyond your own machine.

### Can I reuse my Claude Code skills?

Yes. The `SKILL.md` format is identical, so symlinking `~/.claude/skills` to `~/.octo/skills` makes
everything you already have available immediately — see [Use skills](/docs/guides/use-skills/).

### How is this different from Claude Code / Codex CLI / Hermes?

Not a feature checklist — see the comparison on the [docs home](/docs/). The short version: same
class of tool, but MIT-licensed, a single Go binary with no runtime dependency, and free to point
at any model that speaks the Anthropic or OpenAI protocol.

### Does `--sandbox` work on Windows?

No — OS-level confinement is macOS Seatbelt / Linux Landlock only, and `--sandbox` fails closed
(refuses to run) on Windows rather than pretending to confine anything. The interactive permission
engine is the safety layer there instead. See [Sandbox the agent](/docs/guides/sandbox-the-agent/).

### What happens if octo crashes or loses network mid-task?

Session history is persisted at round granularity, so you lose at most the in-flight round, not the
whole session — resume with `octo -c`. See [Sessions & history](/docs/concepts/sessions-and-history/).

### I bound `octo serve` beyond localhost and now nothing works

Non-loopback binds require the access key on every request. Startup prints a ready-to-open URL with
the key embedded; see [Self-host octo serve](/docs/guides/self-host/) and the
[security model](/docs/reference/security/).

### Where do I report a bug or request a feature?

[Open an issue](https://github.com/open-octo/octo-agent/issues) on GitHub. For a security
vulnerability, use a
[private security advisory](https://github.com/open-octo/octo-agent/security/advisories/new)
instead of a public issue.
