# octo Overview

octo is an **MIT-licensed, single Go binary, self-hosted** AI agent — a coding
agent on par with Claude Code and a personal assistant, running on **any model**
(DeepSeek, Kimi, Anthropic, OpenAI, or anything compatible), with the server and
data staying on the user's own machine. No Node / Python / Ruby runtime.

## Status

**Stable (1.0).** Eight interfaces are planned (one per arm of the octopus); seven are live:

- **CLI** — interactive TUI in a terminal, headless one-shot everywhere else
- **Web UI** — `octo serve`, a local dashboard over REST + WebSocket
- **Desktop app** — native window + system tray (macOS / Windows / Linux)
- **IM bridge** — WeChat iLink, Feishu, DingTalk, WeCom, Discord, Telegram, inside `octo serve`
- **VS Code extension** and **Obsidian plugin** — connect to `octo serve` over the same HTTP/WS API
- **Go SDK** (`pkg/octoagent`) — embed the agent loop in your own programs

The eighth, a mobile app, is next. On top of the agent loop: skills, MCP clients,
OS-level sandboxing, persistent memory, sub-agents, background workflows, and a
task graph for autonomous multi-step goals.

## Install

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
- **Desktop app** — installer from the [latest release](https://github.com/open-octo/octo-agent/releases/latest): `octo-setup.pkg` (macOS), `octo-setup.exe` (Windows), `Octo-x86_64.AppImage` (Linux)
- **Go** — `go install github.com/open-octo/octo-agent/cmd/octo@latest`

`octo upgrade` installs the latest release in place (`--check` only compares versions).

## First run

```bash
octo config                # one-time: pick provider/model, paste an API key
octo "explain this repo"   # headless one-shot: prompt → agentic tool loop → exit
octo                       # interactive TUI in a terminal; octo -c resumes a session
octo serve -d              # Web UI + IM bridge at http://127.0.0.1:8088
```

Built-in tools (shell, file read/edit, search), MCP servers, and skills are on by
default, so a single message can do real work. Loopback binds need no access key;
a non-loopback `octo serve -addr :8088` requires one (printed at startup).

## Where to look next

For depth on a specific area, read the sibling file in this skill directory:
`CLI.md`, `CONFIG.md`, `SKILLS.md`, `MCP.md`, `MEMORY.md`, `PERMISSIONS.md`,
`HOOKS.md`, `IM.md`, `TUI.md`, `WORKFLOW.md`, `TROUBLESHOOTING.md`.

Full online documentation (fetch with `web_fetch` when the summaries don't fully
answer): getting started — **https://octo-agent.dev/docs/getting-started/quickstart/**,
choose a provider — **https://octo-agent.dev/docs/getting-started/choose-a-provider/**,
install details — **https://octo-agent.dev/docs/getting-started/install/**.
