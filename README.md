# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">English</a> · <a href="README_CN.md">简体中文</a>
</p>

> An **MIT-licensed, single Go binary, zero-runtime** AI agent that combines the two things people usually
> reach for two separate tools to get: a **coding agent on par with Claude Code**, and a **personal
> assistant that's lighter and more stable than OpenClaw** — skills, CLI / Web / phone-IM / VS Code / Obsidian, browser control,
> an OS-level sandbox, all as an **open, self-contained binary you fully own**, on **any model** (DeepSeek,
> Kimi, Anthropic, OpenAI, or anything compatible), with the server and your data staying on your own
> machine. Reuse the skills already in `~/.claude/skills`. One binary for both your coding and your everyday
> automation, instead of running two separate tools.

<!-- TODO(demo): record a 15–30s hero GIF (one-line install → octo on DeepSeek → solve a real
     coding task), drop it at landing/assets/demo.gif, and uncomment the block below. -->
<!--
<p align="center">
  <img src="landing/assets/demo.gif" alt="octo demo" width="760">
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

## Why octo — vs OpenClaw

On the personal-assistant side, [OpenClaw](https://github.com/openclaw/openclaw) is the closest kin —
self-hosted, MIT-licensed, chat-driven, reaches you on the apps you already use. octo covers the same
ground, but stays a single static binary instead of a Node.js app with its own dependency tree, and carries
a full coding-agent core (the same tool/permission/sandbox stack from the Claude Code comparison above)
rather than being assistant-only.

|  | **octo-agent** | OpenClaw |
|---|---|---|
| Runtime | **single static Go binary — no Node runtime, no `npm install`, no dependency tree to patch or rebuild** | Node.js (Node 24 recommended) + its npm dependency tree |
| Coding ability | **full agentic coding loop built in** — file read/edit, terminal, permission gating, OS sandbox | assistant/automation-focused; not built around a coding-agent core |
| IM channels | WeChat iLink / Feishu / DingTalk / WeCom / Discord / Telegram | WhatsApp / Telegram / Discord / Slack / Signal / Matrix / WeChat / QQ and 15+ more |
| License / deploy | MIT, self-hosted | MIT, self-hosted |

<sub>OpenClaw details per its public repo/docs (2026-07). Both are self-hosted, MIT-licensed, chat-driven
personal assistants — octo's edge here is running as one static binary (nothing to patch or rebuild) with
a coding-agent core built in, not a channel-coverage checklist; OpenClaw currently covers more IM channels
out of the box.</sub>

**In one line:** for coding, octo aims to match Claude Code; for everyday assistant use, it aims to be the
lighter, more stable alternative to OpenClaw — one binary instead of two separate tools.

## Status

> **Stable (1.0).** Five interfaces are live: the CLI (an interactive TUI in a terminal, a headless agentic one-shot everywhere else), a local web server (`octo serve`), an IM bridge (running inside `octo serve`; WeChat iLink, Feishu, DingTalk, WeCom, Discord, Telegram), a VS Code extension ([`open-octo/octo-vscode`](https://github.com/open-octo/octo-vscode)), and an Obsidian plugin ([`open-octo/octo-obsidian`](https://github.com/open-octo/octo-obsidian)) — the last two connect to `octo serve` over the same HTTP/WebSocket API the Web UI uses. On top of the agent loop there are skills, MCP clients, OS-level sandboxing, persistent memory, sub-agents, background workflows, and a task graph for autonomous multi-step goals.
>
> What you can build on is declared in [COMPATIBILITY.md](COMPATIBILITY.md) (stable config formats, CLI, exit codes — and what isn't covered); the security boundary in [SECURITY.md](SECURITY.md).

## Install

**Linux (install script).** Detects your arch, downloads the matching release,
verifies its SHA-256, and installs `octo` to your `PATH`:

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh
```

Then start the local server and onboard in your browser:

```bash
octo serve -d                  # run the local server in the background
xdg-open http://127.0.0.1:8088 # opens the dashboard
```

`127.0.0.1` is loopback, so no access key is needed; the page goes straight
into first-run onboarding (pick a provider, paste a key). Stop the server later
with `octo serve --stop`. Prefer the terminal? Just run `octo`.

Prebuilt archives (linux / darwin / windows × amd64 + arm64) and `checksums.txt`
are on the [latest release](https://github.com/open-octo/octo-agent/releases/latest)
if you'd rather grab one by hand.

**macOS — two ways to install:**

- **Install script (command line).** Same one-liner as Linux:

  ```bash
  curl -fsSL https://octo-agent.dev/install.sh | sh
  octo serve -d                  # run the local server in the background
  open http://127.0.0.1:8088     # opens the dashboard
  ```

- **Double-click installer.** Download `octo-setup.pkg` from the
  [latest release](https://github.com/open-octo/octo-agent/releases/latest)
  (one universal package covers both Apple Silicon and Intel) and
  double-click it. Installer.app offers only "Install for me only" — no
  administrator password. It installs octo to
  `~/Library/Application Support/octo`, adds it to your `PATH` (appends to
  `~/.zprofile` / `~/.bash_profile` / `~/.profile`), and registers a
  LaunchAgent that starts `octo serve -d` on every login. When it finishes it
  starts the server and opens <http://127.0.0.1:8088> — a loopback address, so
  no access key is needed — to walk you through first-run onboarding (pick a
  provider, paste a key). For a terminal session, open a **new** terminal and
  run `octo`. The installer isn't notarized, so Gatekeeper will warn it's from
  an unidentified developer — right-click (Control-click) the file → **Open**,
  or allow it via **System Settings → Privacy & Security → Open Anyway**.
  There's no App Store-style uninstaller for `.pkg`; run
  `~/Library/Application\ Support/octo/uninstall.sh` to remove everything it
  installed.

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
leaves their infrastructure. The macOS installer (`octo-setup.pkg`) isn't
signed or notarized yet — there's no Apple Developer Program membership behind
this project — so Gatekeeper's unidentified-developer warning is expected for
every release until that changes.

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

# Extended reasoning: set the intensity (Anthropic thinking / OpenAI reasoning_effort).
# --show-reasoning surfaces the trace to the Web UI (octo serve); the terminal never renders it.
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

- `--reasoning-effort low|medium|high|xhigh|max` — the intensity. OpenAI-protocol backends receive it as `reasoning_effort`; Anthropic-protocol backends map it to adaptive thinking or an extended-thinking token budget, normalized per model family. Empty (the default) means off.
- `--show-reasoning` (default **off**) — surface the reasoning trace for the **Web UI** (`octo serve`) to display. The terminal never renders it either way.

This unifies Anthropic `thinking` blocks and OpenAI `reasoning_content` behind one pair of controls.

### Defaults (`octo config`)

`octo config` saves your default provider, model, (optionally) base URL, and reasoning settings to `~/.octo/config.yml`, so a bare `octo` works without re-typing `--provider`/`--model` every time:

```bash
octo config        # interactive wizard
octo config show   # print the effective settings + where each comes from
octo config path   # print the file location
```

Precedence is **CLI flag > env var > `~/.octo/config.yml` > built-in default**. API keys are read from `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` first; the wizard can store one in the file (mode `0600`), but the env var is recommended.

### MCP Tool Search

When MCP servers expose many tools, uploading every tool schema on every turn wastes context and hurts accuracy. Tool Search keeps built-in tools visible but defers MCP *schemas* behind a small bridge — every connected tool's name and one-line description stay listed in the system prompt the whole time, so the model never has to guess whether a tool exists:

- `mcp_describe` — load the full JSON Schema for one listed tool.
- `mcp_call` — invoke the tool with arguments matching that schema.

The model uses the same two-step flow automatically. Configure when the bridge activates in `~/.octo/config.yml`:

```yaml
tools:
  tool_search:
    enabled: auto          # auto (default) | on | off
    threshold_pct: 10      # auto: activate when deferred schemas ≥ N% of context window
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
| MCP client | done | `mcp.json` stdio + Streamable HTTP servers, tools/resources/prompts, OAuth (Authorization Code + PKCE); Tool Search defers large MCP schemas until needed |
| Memory | done | Persistent cross-session memory under `~/.octo/memories/`, auto extract/consolidate |
| Sub-agents | done | `sub_agent` fan-out, async + resumable (`sub_agent_send`, `sub_agent_status`, `sub_agent_kill`) |
| Workflows | done | `workflow` tool — deterministic multi-agent orchestration (parallel/pipeline), background runs with liveness + `workflow_kill`, git worktree isolation, structured-output schema; JS or an embedded-Ruby DSL |
| Web server | done | `octo serve` — REST + WebSocket, the embedded Octo Workbench UI (sessions, tool output, artifacts, sub-agents, tasks, memories, MCP, skills; loopback bind by default; access-key auth for exposed binds, see SECURITY.md) |
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
internal/server/   octo serve — HTTP REST + WebSocket + embedded dashboard
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

Project conventions live in both [`CLAUDE.md`](CLAUDE.md) and [`.octorules`](.octorules) — parallel files carrying the same guidance for two different AI coding tools (Claude Code reads the former; octo reads the latter as a system-prompt layer, see Configuration above). See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the human PR workflow.

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
