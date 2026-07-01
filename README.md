# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">English</a> · <a href="README_CN.md">简体中文</a>
</p>

> An **MIT-licensed, single Go binary, zero-runtime** AI agent. It does what the big coding agents do —
> skills, CLI / Web / phone-IM, browser control, an OS-level sandbox — but as an **open, self-contained
> binary you fully own**, on **any model** (DeepSeek, Kimi, Anthropic, OpenAI, or anything compatible),
> with the server and your data staying on your own machine. Reuse the skills already in `~/.claude/skills`.
> It's both a **coding agent** (vs Claude Code) and a **general-purpose agent** (vs Hermes) — one binary for
> both your coding and your everyday automation, instead of running two separate tools.

<!-- TODO(demo): record a 15–30s hero GIF (one-line install → octo on DeepSeek → solve a real
     coding task), drop it at docs/assets/demo.gif, and uncomment the block below. -->
<!--
<p align="center">
  <img src="docs/assets/demo.gif" alt="octo demo" width="760">
</p>
-->

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # single binary — no Node / Ruby / Python
octo config                                            # pick a provider, paste a key (DeepSeek / Kimi / …)
octo "Add a --json flag to 'octo config show' and run the tests"   # one prompt → full agentic loop
```

## Why octo — vs Claude Code

octo isn't trying to out-feature the big agents; it's the **open, self-hostable, vendor-neutral** take on
the same idea. If you're happy on a Claude subscription, Claude Code is great. octo is for when you'd
rather own the whole thing and run it on your own models.

|  | **octo-agent** | Claude Code |
|---|---|---|
| License / cost | **MIT, free, self-hosted** | proprietary; most surfaces need a Claude subscription |
| Runtime | **one self-contained Go binary — no Node / Python / Ruby, no dependency tree** | native install tied to an Anthropic account |
| Models | **both protocols + any compatible endpoint** (DeepSeek/Kimi/Bailian/OpenRouter/vLLM) | Anthropic-first (third-party providers on Terminal / VS Code) |
| Deployment / data | **fully self-hosted — server and data stay yours** | Anthropic-managed for most surfaces |
| Skills | reuse `~/.claude/skills` directly | native (the format's origin) |

<sub>Claude Code details per its public docs (2026-07). It also has skills, MCP, sub-agents, an OS sandbox,
browser / computer-use, and IM channels — octo covers the same ground. The difference above is openness,
self-hosting, and model freedom, not a feature checklist.</sub>

**In one line:** you want the Claude Code experience, but open-source, self-hostable, and not locked to a
subscription or a single vendor — that's octo.

## Status

> **Stable (1.0).** All three interfaces are live: the CLI (an interactive TUI in a terminal, a headless agentic one-shot everywhere else), a local web server (`octo serve`), and an IM bridge (running inside `octo serve`; WeChat iLink, Feishu, DingTalk, WeCom, Discord, Telegram). On top of the agent loop there are skills, MCP clients, OS-level sandboxing, persistent memory, sub-agents, background workflows, and a task graph for autonomous multi-step goals.
>
> What you can build on is declared in [COMPATIBILITY.md](COMPATIBILITY.md) (stable config formats, CLI, exit codes — and what isn't covered); the security boundary in [SECURITY.md](SECURITY.md).

## Install

**macOS / Linux (install script).** Detects your OS/arch, downloads the matching
release, verifies its SHA-256, and installs `octo` to your `PATH`:

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh
```

Then start the local server and onboard in your browser:

```bash
octo serve -d                  # run the local server in the background
open http://127.0.0.1:8088     # Linux: xdg-open — opens the dashboard
```

`127.0.0.1` is loopback, so no access key is needed; the page goes straight
into first-run onboarding (pick a provider, paste a key). Stop the server later
with `octo serve --stop`. Prefer the terminal? Just run `octo`.

Prebuilt archives (linux / darwin / windows × amd64 + arm64) and `checksums.txt`
are on the [latest release](https://github.com/open-octo/octo-agent/releases/latest)
if you'd rather grab one by hand.

**Windows (double-click installer).** Download `octo-setup.exe` from the
[latest release](https://github.com/open-octo/octo-agent/releases/latest) and
double-click it. It installs per-user (no administrator prompt), puts `octo` on
your `PATH`, and adds a Start-menu entry. When it finishes it starts the local
server in the background (`octo serve -d`) and opens
<http://127.0.0.1:8088> — a loopback address, so no access key is needed — to
walk you through first-run onboarding (pick a provider, paste a key). The
server is also registered to start on each login (a per-user `Run` entry, no
window), so the dashboard is up after a reboot; uninstalling removes it and
stops the daemon. For a terminal session, open a **new** terminal and run
`octo`. The
installer is not yet code-signed, so Windows SmartScreen shows "Windows
protected your PC" on first launch — click **More info → Run anyway**.
Uninstall from "Add or remove programs" like any other app.

**Upgrading:** `octo upgrade` installs the latest release in place (SHA-256
verified against `checksums.txt`); `octo upgrade --check` only compares
versions. The web UI's version badge offers the same flow.

### Code signing policy

octo's Windows installer (`octo-setup.exe`) is signed through the free
open-source program of the [SignPath Foundation](https://signpath.org/),
which issues the certificate; the signing key is held by SignPath and never
leaves their infrastructure.

- **What is signed:** the `octo-setup.exe` installer attached to each GitHub
  release. The release archives themselves are integrity-checked via the
  `checksums.txt` published alongside them.
- **How:** the installer is built in CI (`.github/workflows/release.yml`) and
  submitted to SignPath for signing as part of the release. Every signing
  request is approved by a project maintainer before it is signed.
- **Who can approve:** the repository maintainers. Anyone with signing-request
  approval or repository write access uses multi-factor authentication on both
  GitHub and SignPath.
- **Verifying:** right-click the installer → *Properties → Digital Signatures*
  to confirm the publisher.

Signing is being rolled out via the SignPath Foundation; until the certificate
is provisioned, released installers may be **unsigned**, in which case Windows
SmartScreen shows "Windows protected your PC" — click **More info → Run anyway**.

**From Go:**

```bash
go install github.com/open-octo/octo-agent/cmd/octo@latest
```

**From source:**

```bash
git clone https://github.com/open-octo/octo-agent.git
cd octo-agent
make build       # produces ./octo
```

## Quick start

```bash
export ANTHROPIC_API_KEY=sk-ant-...      # or OPENAI_API_KEY=...

# One-time setup: save your default provider/model (skip the export above next time)
octo config

# Headless one-shot (claude -p style): one prompt → full agentic tool loop → exit.
# Built-in tools (shell, read/edit files, search), MCP servers, and skills are
# all on by default, so a single message can actually do work.
octo "Add a --json flag to 'octo config show' and run the tests"

# The prompt can also come from a pipe or a file — handy for scripts / CI:
echo "Summarise what changed in the last commit" | octo
octo --prompt-file ./task.md

# Interactive multi-turn: run octo in a terminal with no message to get the TUI
# (rich tool cards, session auto-saved). Resume a previous session with -c.
octo
octo sessions
octo -c                  # pick a recent session from a list
octo -c <session-id>

# Streaming on by default; --stream=false buffers and prints only the final
# reply text (clean for capturing into a file).
octo --stream=false "..."

# OpenAI / DeepSeek / Bailian (OpenAI-compatible)
octo --provider openai --model gpt-4o-mini "..."

# Self-hosted / third-party endpoints use the `custom` vendor — the only one
# that takes a custom base URL. Its wire protocol (openai | anthropic) is chosen
# per config entry, so set it up once with `octo config` (choose Custom → pick
# the protocol → enter base URL + model), then:
CUSTOM_BASE_URL=https://api.deepseek.com/anthropic \
CUSTOM_API_KEY=sk-... \
  octo --model deepseek-chat "..."

# Extended reasoning: set the intensity (Anthropic thinking / OpenAI reasoning_effort)
# and stream the dimmed thinking trace. --show-reasoning=false hides the trace.
octo --reasoning-effort high "..."

# Plain chat with no tools / MCP / skills
octo --no-tools "..."

# Sandbox the tool commands: confine the terminal tool to the project dir + tmp, no network
octo --sandbox "..."

# Generate a .octorules guide for this repo
octo init

# List discovered skills
octo skills list

# Web server + dashboard (binds 127.0.0.1:8088 by default)
octo serve
octo serve -addr :8088   # expose on the LAN — non-localhost clients need the
                         # access key; startup prints a ready-to-open URL

# IM bridge (WeChat iLink): scan-to-login; channels run inside `octo serve`
octo serve   # WeChat login: Channels panel in the web UI (scan QR)
```

## Configuration

Octo composes its system prompt from several optional layers (later overrides earlier):

- `~/.octo/soul.md` — agent identity & behavior, an openclaw/hermes-style persona.
- `~/.octo/user.md` — who you are; a profile injected into every session.
- `~/.octo/octorules.md` — your global, cross-project rules and preferences.
- `.octorules` — per-repo conventions, committed with the project. Generate one with `octo init` (or `/init` in the TUI).
- `--system "..."` — a one-off override for a single run.

The identity and rule files support `@include path/to/fragment.md` to pull in shared content.

### Reasoning

Reasoning models can deliberate before answering. Two knobs control it, both available as CLI flags and as `octo config` defaults:

- `--reasoning-effort low|medium|high` — the intensity. OpenAI-protocol backends receive it as `reasoning_effort`; Anthropic-protocol backends map it to an extended-thinking token budget. Empty (the default) means off.
- `--show-reasoning` (default on) — stream the thinking trace to the terminal, dimmed. `--show-reasoning=false` keeps reasoning enabled but hides the trace.

This unifies Anthropic `thinking` blocks and OpenAI `reasoning_content` behind one pair of controls.

### Defaults (`octo config`)

`octo config` saves your default provider, model, (optionally) base URL, and reasoning settings to `~/.octo/config.yaml`, so a bare `octo` works without re-typing `--provider`/`--model` every time:

```bash
octo config        # interactive wizard
octo config show   # print the effective settings + where each comes from
octo config path   # print the file location
```

Precedence is **CLI flag > env var > `~/.octo/config.yaml` > built-in default**. API keys are read from `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` first; the wizard can store one in the file (mode `0600`), but the env var is recommended.

### MCP Tool Search

When MCP servers expose many tools, uploading every tool schema on every turn wastes context and hurts accuracy. Tool Search keeps built-in tools visible but defers MCP schemas behind a small bridge:

- `mcp_search` — find MCP tools by keyword (returns names + one-line descriptions).
- `mcp_describe` — load the full JSON Schema for one discovered tool.
- `mcp_call` — invoke the tool with arguments matching that schema.

The model uses the same three-step flow automatically. Configure when the bridge activates in `~/.octo/config.yaml`:

```yaml
tools:
  tool_search:
    enabled: auto          # auto (default) | on | off
    threshold_pct: 10      # auto: activate when deferred schemas ≥ N% of context window
    search_default_limit: 5
    max_search_limit: 20
```

- `auto` (default) — only enable when the deferred MCP schemas would occupy at least `threshold_pct` of the model's context window.
- `on` — always defer MCP schemas when any MCP tool is connected.
- `off` — upload all MCP schemas up front, as before.

## Skills

Skills are reusable instruction sets in Claude Code's SKILL.md format, discovered from:

- `~/.octo/skills/<name>/SKILL.md` — user-level, across all projects.
- `.octo/skills/<name>/SKILL.md` — project-level (takes precedence over user-level).

The format is identical to Claude Code's, so you can symlink `~/.claude/skills` to `~/.octo/skills` and reuse what you already have. Each `SKILL.md` is YAML frontmatter plus a markdown body:

```markdown
---
name: review
description: Review the current diff for correctness and style
---
Walk the diff hunk by hunk and flag correctness bugs first, then style.
```

At session start Octo lists each skill's name and description in the system prompt; the model loads a skill's full instructions on demand (via the `skill` tool) when a task matches. You can also trigger one explicitly — `octo skills list` to see what's discovered, then `/skills` to list and `/<name>` (e.g. `/review`) to run one in the TUI.

## Sandboxing

`--sandbox` confines the `terminal` tool to the project directory plus temp, with no network, enforced by the OS (macOS Seatbelt, Linux Landlock + seccomp). It's off by default and fails closed when the OS mechanism is unavailable.

```bash
octo --sandbox                              # confine, deny network
octo --sandbox --sandbox-allow-net          # allow network
octo --sandbox --sandbox-write ./build      # extra writable dir (repeatable)
octo --sandbox --sandbox-read /opt/data     # extra readable dir (repeatable)
```

## Platform notes

octo runs on Linux, macOS, and Windows. A few behaviors differ on Windows:

- **Shell is PowerShell.** The `terminal` tool runs commands through PowerShell (`pwsh`, else Windows PowerShell 5.1), not POSIX `sh` — use PowerShell syntax (`Get-ChildItem`, `Select-String`, `Remove-Item`, `$env:VAR`) and chain with `;` (5.1 has no `&&`). The agent is told this, and the built-in `read_file` / `glob` / `grep` / `write_file` / `edit_file` tools are cross-platform and don't shell out, so prefer them.
- **Deletes go to the trash, like POSIX.** Agent-issued `Remove-Item` / `rm` / `del` are intercepted and the targets copied to `~/.octo/trash/` before deletion, so they're recoverable from the Web UI trash panel — the same protection as the POSIX `rm` wrapper. Best-effort: literal and globbed filesystem paths are backed up; provider paths (`Env:`, registry), pipeline input, and anything that fails to copy fall through to a normal delete.
- **`--sandbox` is unavailable on Windows.** OS confinement is macOS Seatbelt / Linux Landlock only; on Windows `--sandbox` fails closed (refuses to run). The permission engine (interactive prompts) is the safety layer there.
- **`terminal_input`** (writing to a background process's stdin) is reliable only on POSIX shells; PowerShell's `-Command` mode doesn't deterministically forward stdin to a spawned process.

## What's implemented

| Area | Status | Description |
|------|--------|-------------|
| Core CLI | done | Headless agentic one-shot (`claude -p` style) + interactive TUI, streaming, session persistence (`~/.octo/sessions/`), `/cost` `/save` `/sessions` |
| Providers | done | Anthropic Messages + OpenAI Chat Completions, plus any compatible third party |
| Reasoning | done | Unified extended thinking (Anthropic) / `reasoning_content` (OpenAI), `--reasoning-effort`, `--show-reasoning` |
| Tools | done | `terminal` (+ background), file read/write/edit, glob, grep, web fetch/search |
| Agentic loop | done | Multi-step tool calling, permission gating, history compaction, graceful Ctrl-C |
| Memory & config | done | `~/.octo/octorules.md`, `.octorules`, `octo init`, `@include` |
| Skills | done | Claude Code-compatible SKILL.md loader (`octo skills`, `/skills`, `/<name>`) |
| Sandbox | done | OS-enforced `--sandbox` (macOS / Linux) |
| MCP client | done | `mcp.json` stdio + Streamable HTTP servers, tools/resources/prompts, device-flow OAuth; Tool Search defers large MCP schemas until needed |
| Memory | done | Persistent cross-session memory under `~/.octo/memories/`, auto extract/consolidate |
| Sub-agents | done | `sub_agent` fan-out, async + resumable (`sub_agent_send`, `sub_agent_status`, `sub_agent_kill`) |
| Workflows | done | `workflow` tool — deterministic multi-agent orchestration (parallel/pipeline), background runs with liveness + `workflow_kill`, git worktree isolation, structured-output schema; JS or an embedded-Ruby DSL |
| Web server | done | `octo serve` — REST + SSE, the embedded Octo Workbench UI (sessions, tool output, artifacts, sub-agents, tasks, memories, MCP, skills; loopback bind by default; access-key auth for exposed binds, see SECURITY.md) |
| IM bridge | done | runs inside `octo serve` — WeChat iLink / Feishu / DingTalk / WeCom / Discord / Telegram adapters (web QR login, per-user sessions, slash commands) |

## Architecture

Layered, one-directional dependency graph:

```
cmd/octo/          CLI entry (chat one-shot + TUI, serve, mcp, slash commands)
   ↓
internal/agent/    History, sessions, content blocks, Sender interface,
                   Agent.Turn / TurnStream / Run (tool-calling loop)
   ↓
internal/provider/ Provider interface + concrete implementations
                   ├─ anthropic/   x-api-key, system top-level, content[].text
                   └─ openai/      Bearer auth, system in messages[0]
   ↓
internal/tools/    ToolExecutor implementations — terminal (+ background),
                   file read/write/edit, glob, grep, web fetch/search, skill
internal/skills/   SKILL.md discovery + system-prompt manifest
internal/permission/  allow/deny/ask rule engine gating every tool call
internal/mcp/      MCP client (stdio + HTTP, OAuth)
internal/server/   octo serve — HTTP REST + SSE + embedded dashboard
internal/channel/  IM bridge — adapter interface + WeChat iLink / Feishu /
                   DingTalk / WeCom / Discord / Telegram adapters
```

Each provider implements both **buffered** (`Send`) and **streaming** (`SendStream`) variants. The agent layer mirrors with `Sender` / `StreamingSender` / `ToolSender` / `ToolStreamingSender` — interfaces are added incrementally so non-streaming providers still work.

## Development

```bash
make build         # ./octo
make test          # go test -race ./...
make vet           # go vet ./...
make fmt-check     # gofmt -l . must be empty
```

Project conventions live in [`.octorules`](.octorules) (the human-facing rules); [`CLAUDE.md`](CLAUDE.md) expands them with the operational detail AI coding agents need in this repo. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the human PR workflow.

## Prior art & acknowledgements

octo stands on the shoulders of two projects and doesn't pretend otherwise:

- **[Claude Code](https://code.claude.com)** — much of octo's internal design is modeled on how Claude Code
  works: the agent loop, the tool set, the SKILL.md format, permission gating, and general harness behavior.
  octo aims to be a compatible, open, self-hostable take on the same ideas.
- **[OpenClacky](https://github.com/clacky-ai/openclacky)** — a large share of octo's UI and interaction
  design is inspired by OpenClacky (itself a kindred open-source, BYOK agent).

Any bugs or bad decisions are octo's own.

## License

MIT. See [`LICENSE.txt`](LICENSE.txt).
