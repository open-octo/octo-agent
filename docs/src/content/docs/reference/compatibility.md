---
title: Compatibility & exit codes
description: What you can build on, what's best-effort, and what's internal.
---

octo follows [Semantic Versioning](https://semver.org). As of v1.0.0 these tiers are a hard
commitment: breaking a Stable surface requires a major version, always with a **Breaking** callout
in the [release notes](https://github.com/open-octo/octo-agent/releases).

- **Major** — a Stable surface changed incompatibly, after the deprecation window below.
- **Minor** — new features; additive changes to Stable surfaces; any change to Best-effort surfaces.
- **Patch** — fixes; no surface changes.

## Stable

| Surface | The promise |
|---|---|
| `~/.octo/config.yml` | Recognized fields keep their name and meaning; new fields are optional with working defaults; unknown fields are ignored |
| `~/.octo/permissions.yml` | The rule format (tool-keyed `allow`/`deny`/`ask` lists, `pattern`/`hostname`/`path` matchers); first-match-wins semantics are part of the contract |
| `~/.octo/mcp.json`, `.octo/mcp.json` | The `mcpServers` shape (stdio `command`/`args`/`env`, HTTP `url`/`headers`/`auth`); project file overrides user file by name |
| `~/.octo/channels.yml` | The `channels` map keyed by platform, each platform's documented keys |
| `SKILL.md` | YAML frontmatter + markdown body, Claude Code's format, discovered from `~/.octo/skills/` and `.octo/skills/` |
| `~/.octo/agents/<name>.md` | Custom [sub-agent](/docs/guides/sub-agents/) definitions — `description`/`read_only`/`tools`/`disallowed_tools`/`model` frontmatter + a system-prompt body; filename is the type name |
| `soul.md` / `user.md` / `octorules.md` / `.octorules` | Same layering and `@include` support |
| `~/.octo/memories/<slug>/` | `MEMORY.md` index + on-demand topic files, plain markdown |
| Sessions (`~/.octo/sessions/*.jsonl`), tasks (`~/.octo/tasks/`) | **Read guarantee**: every 1.x release reads state written by any earlier 1.x (and 0.19+). Downgrades aren't covered. |
| CLI subcommands & documented flags | Names and semantics keep working; renames keep the old spelling through a deprecation window |
| Exit codes | `0` success · `1` error · `2` usage/unknown-help · `42` from `octo serve` = restart requested (the supervisor contract) |
| `OCTO_*` and per-vendor env vars | Keep their meaning; new ones are additive |

Not covered even under Stable: human-readable stdout/TUI text — don't parse it.

## Best-effort

Documented and real, but changes land in minors with a release-notes callout rather than requiring a
major:

- **The HTTP API (`/api/*`) and WebSocket events** — see the [full reference](/docs/reference/http-api/).
  It's the embedded Web UI's own API, shipped in the same binary, so it can't drift from the UI —
  but there's no versioned `/api/v1` yet.
- **Default content** — built-in permission rules, default skills, prompt composition. Behavior
  tuning, not format (the *format* they're written in is Stable).
- `GET /api/health` / `GET /api/version` stay unauthenticated with a JSON body, but the body may
  gain fields.

## Internal

Everything under `~/.octo/` not named above (`tmp/`, `logs/`, `bin/`, `trash/`, `uploads/`,
`mcp-tokens/`, `history/`, `skills-default/`), the `__`-prefixed entrypoints (`__complete`,
`__sandboxed-exec`, `__trash-backup`), and the Go module's `internal/` packages — octo ships as a
binary, not a library.

## Platform support

Linux and macOS are first-class. Two Windows gaps are stated here rather than promised:

- **Interactive `terminal_input` is POSIX-only** — PowerShell's `-Command` mode doesn't forward
  redirected stdin to a spawned child reliably. Pass input up front via `terminal`'s `stdin`
  parameter instead.
- **`--sandbox` is unavailable on Windows** — OS confinement is Seatbelt/Landlock only; it fails
  closed rather than running unconfined.

## Migration policy

Old formats migrate automatically on read — the way legacy `config.yaml` upgrades into `config.yml`
today, with the original kept as `.bak`. A deprecated format or flag spelling keeps working for at
least one minor release after the release notes announce it; removing read support entirely is a
breaking change reserved for a major release.

A Stable surface that broke without a major version, or without a **Breaking** callout in the
release notes, is a bug — [open an issue](https://github.com/open-octo/octo-agent/issues).
